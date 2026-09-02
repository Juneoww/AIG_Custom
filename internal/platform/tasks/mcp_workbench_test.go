package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPWorkbenchUsesCreatedAtWindowOwnerScopeAndSafeTaskFields(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 17, 23, 45, 0, 0, time.FixedZone("CST", 8*60*60))
	lower := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	upper := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, nil, nil)

	fixtures := []*Task{
		mcpWorkbenchTask("running-repository", "alice", "mcp_scan", StatusRunning, lower, upper.Add(-time.Minute), json.RawMessage(`{"source_kind":"repository","token":"private"}`)),
		mcpWorkbenchTask("dispatching-service", "alice", "Mcp-Scan", StatusDispatching, upper.Add(-2*time.Hour), upper.Add(-2*time.Hour), json.RawMessage(`{"source_kind":"service","authorization_confirmed":true}`)),
		mcpWorkbenchTask("pending-legacy", "alice", "mcp_scan", StatusPending, upper.Add(-3*time.Hour), upper.Add(-3*time.Hour), json.RawMessage(`{"model_id":"private-model"}`)),
		mcpWorkbenchTask("unknown", "alice", "mcp_scan", StatusDispatchUnknown, upper.Add(-4*time.Hour), upper.Add(-4*time.Hour), json.RawMessage(`{"source_kind":"repository"}`)),
		mcpWorkbenchTask("completed", "alice", "mcp_scan", StatusSucceeded, upper.Add(-5*time.Hour), upper.Add(-5*time.Hour), json.RawMessage(`{"source_kind":"service"}`)),
		mcpWorkbenchTask("dispatch-failed-still-active", "alice", "mcp_scan", StatusDispatchFailed, upper.Add(-6*time.Hour), upper.Add(-6*time.Hour), json.RawMessage(`{"source_kind":"service"}`)),
		mcpWorkbenchTask("before-window", "alice", "mcp_scan", StatusRunning, lower.Add(-time.Nanosecond), upper, json.RawMessage(`{"source_kind":"repository"}`)),
		mcpWorkbenchTask("at-upper-bound", "alice", "mcp_scan", StatusPending, upper, upper, json.RawMessage(`{"source_kind":"repository"}`)),
		mcpWorkbenchTask("bob-private", "bob", "mcp_scan", StatusRunning, upper.Add(-time.Hour), upper.Add(-time.Hour), json.RawMessage(`{"source_kind":"service"}`)),
		mcpWorkbenchTask("not-mcp", "alice", "agent_scan", StatusRunning, upper.Add(-time.Hour), upper.Add(-time.Hour), json.RawMessage(`{"source_kind":"service"}`)),
	}
	for _, task := range fixtures {
		_, _, err := repository.CreateOrGet(ctx, task)
		require.NoError(t, err)
	}

	projection, err := service.MCPWorkbench(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, now)
	require.NoError(t, err)
	assert.Equal(t, 2, projection.Running)
	assert.Equal(t, 2, projection.Pending)
	require.Len(t, projection.ActiveTasks, 5)
	assert.Equal(t, []string{"running-repository", "dispatching-service", "pending-legacy", "unknown", "dispatch-failed-still-active"}, mcpWorkbenchTaskIDs(projection.ActiveTasks))
	assert.Equal(t, []string{"repository", "service", "legacy_unknown", "repository", "service"}, mcpWorkbenchTaskSourceKinds(projection.ActiveTasks))

	encoded, err := json.Marshal(projection)
	require.NoError(t, err)
	for _, forbidden := range []string{"private", "private-model", "authorization_confirmed", "content", "params", "attachment"} {
		assert.NotContains(t, string(encoded), forbidden)
	}

	global, err := service.MCPWorkbench(ctx, identity.Subject{Role: identity.RoleAuditor}, now)
	require.NoError(t, err)
	assert.Equal(t, 3, global.Running)
	assert.Contains(t, mcpWorkbenchTaskIDs(global.ActiveTasks), "bob-private")

	admin, err := service.MCPWorkbench(ctx, identity.Subject{Role: identity.RoleAdmin}, now)
	require.NoError(t, err)
	assert.Equal(t, global, admin)

	_, err = service.MCPWorkbench(ctx, identity.Subject{}, now)
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestMCPWorkbenchCapsActiveTasksByUpdatedAtThenID(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, nil, nil)
	for index := range 12 {
		id := fmt.Sprintf("task-%02d", index)
		updatedAt := now.Add(-time.Hour)
		if index < 2 {
			updatedAt = now
		}
		_, _, err := repository.CreateOrGet(ctx, mcpWorkbenchTask(id, "alice", "mcp_scan", StatusPending, now, updatedAt, json.RawMessage(`{"source_kind":"repository"}`)))
		require.NoError(t, err)
	}

	projection, err := service.MCPWorkbench(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, now)
	require.NoError(t, err)
	assert.Equal(t, 12, projection.Pending)
	require.Len(t, projection.ActiveTasks, 10)
	assert.Equal(t, []string{"task-01", "task-00", "task-11", "task-10", "task-09", "task-08", "task-07", "task-06", "task-05", "task-04"}, mcpWorkbenchTaskIDs(projection.ActiveTasks))
}

func TestGormMCPWorkbenchUsesScopedCountsAndNarrowActiveTaskProjection(t *testing.T) {
	ctx := context.Background()
	db := openTaskSnapshotPostgresDB(t)
	repository := NewGormRepository(db)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for _, task := range []*Task{
		mcpWorkbenchTask("alice-running", "alice", "mcp_scan", StatusRunning, now, now, json.RawMessage(`{"source_kind":"repository","token":"private"}`)),
		mcpWorkbenchTask("alice-pending", "alice", "Mcp-Scan", StatusPending, now, now.Add(-time.Minute), json.RawMessage(`{"source_kind":"service"}`)),
		mcpWorkbenchTask("alice-completed", "alice", "mcp_scan", StatusSucceeded, now, now.Add(-2*time.Minute), json.RawMessage(`{"source_kind":"repository"}`)),
		mcpWorkbenchTask("alice-legacy", "alice", "mcp_scan", StatusDispatchUnknown, now, now.Add(-3*time.Minute), json.RawMessage(`{"model_id":"private-model"}`)),
		mcpWorkbenchTask("bob-running", "bob", "mcp_scan", StatusRunning, now, now, json.RawMessage(`{"source_kind":"service"}`)),
		mcpWorkbenchTask("agent-running", "alice", "agent_scan", StatusRunning, now, now, json.RawMessage(`{"source_kind":"service"}`)),
	} {
		_, _, err := repository.CreateOrGet(ctx, task)
		require.NoError(t, err)
	}
	capture := &taskQueryCaptureLogger{Interface: db.Config.Logger}
	db.Config.Logger = capture

	projection, err := repository.MCPWorkbench(ctx, MCPWorkbenchQuery{OwnerUserID: "alice", Now: now})
	require.NoError(t, err)
	assert.Equal(t, 1, projection.Running)
	assert.Equal(t, 2, projection.Pending)
	require.Len(t, projection.ActiveTasks, 3)
	assert.Equal(t, []string{"alice-running", "alice-pending", "alice-legacy"}, mcpWorkbenchTaskIDs(projection.ActiveTasks))
	assert.Equal(t, []string{"repository", "service", "legacy_unknown"}, mcpWorkbenchTaskSourceKinds(projection.ActiveTasks))
	require.Len(t, capture.statements, 3)
	activeSQL := capture.statements[len(capture.statements)-1]
	projectionSQL := strings.SplitN(activeSQL, " from ", 2)[0]
	assert.Contains(t, activeSQL, "owner_user_id = 'alice'")
	assert.Contains(t, activeSQL, "created_at >=")
	assert.Contains(t, activeSQL, "created_at <")
	assert.Contains(t, activeSQL, "task_type in ('mcp_scan','mcp-scan')")
	assert.Contains(t, activeSQL, "order by updated_at desc, id desc")
	assert.Contains(t, activeSQL, "limit 10")
	assert.Contains(t, projectionSQL, "params ->> 'source_kind'")
	for _, forbidden := range []string{"content", "attachment_refs", "engine_session_id", "dispatch_error", "dispatch_claim_token", "dispatch_lease_until", "private", "private-model"} {
		assert.NotContains(t, projectionSQL, forbidden)
	}
}

func mcpWorkbenchTask(id, owner, taskType string, status Status, createdAt, updatedAt time.Time, params json.RawMessage) *Task {
	return &Task{
		ID: id, OwnerUserID: owner, OwnerUsername: owner, IdempotencyKey: "key-" + id,
		EngineSessionID: "engine-" + id, TaskType: taskType, Content: "https://mcp.private.example/sse", Params: params,
		AttachmentRefs: json.RawMessage(`[]`), Status: status, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
}

func mcpWorkbenchTaskIDs(items []MCPWorkbenchTask) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.TaskID)
	}
	return ids
}

func mcpWorkbenchTaskSourceKinds(items []MCPWorkbenchTask) []string {
	kinds := make([]string, 0, len(items))
	for _, item := range items {
		kinds = append(kinds, item.SourceKind)
	}
	return kinds
}

package mcpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingTaskReader struct {
	projection tasks.MCPWorkbenchProjection
	err        error
	subject    identity.Subject
	now        time.Time
}

func (reader *recordingTaskReader) MCPWorkbench(_ context.Context, subject identity.Subject, now time.Time) (tasks.MCPWorkbenchProjection, error) {
	reader.subject = subject
	reader.now = now
	return reader.projection, reader.err
}

type recordingReportReader struct {
	projection reports.MCPWorkbenchProjection
	err        error
	subject    identity.Subject
	now        time.Time
}

func (reader *recordingReportReader) MCPWorkbench(_ context.Context, subject identity.Subject, now time.Time) (reports.MCPWorkbenchProjection, error) {
	reader.subject = subject
	reader.now = now
	return reader.projection, reader.err
}

func TestServiceCombinesOnlyWhitelistedMCPWorkbenchFields(t *testing.T) {
	now := time.Date(2026, 8, 17, 23, 45, 0, 0, time.FixedZone("CST", 8*60*60))
	tasksReader := &recordingTaskReader{projection: tasks.MCPWorkbenchProjection{
		Running: 2, Pending: 3,
		ActiveTasks: []tasks.MCPWorkbenchTask{
			{TaskID: "12345678-90ab-cdef-1234-567890abcdef", SourceKind: "repository", Status: tasks.StatusRunning, UpdatedAt: now},
			{TaskID: "abcdef01", SourceKind: "unsafe-value", Status: tasks.StatusDispatchUnknown, UpdatedAt: now.Add(-time.Minute)},
		},
	}}
	reportsReader := &recordingReportReader{projection: reports.MCPWorkbenchProjection{
		Completed30d: 9, HighRisk: 5,
		Highlights: []reports.MCPRiskHighlight{
			{ReportID: "report-1", TaskID: "task-1", Severity: "high", Category: "dangerous_tool", Summary: "检测到高风险危险工具调用。", CompletedAt: now},
			{ReportID: "report-2", TaskID: "task-2", Severity: "unknown", Category: "dangerous_tool", Summary: "must-not-appear", CompletedAt: now},
		},
	}}
	service := NewService(reportsReader, tasksReader)
	service.now = func() time.Time { return now }
	subject := identity.Subject{UserID: "alice", Username: "alice", Role: identity.RoleUser}

	view, err := service.Get(context.Background(), subject)
	require.NoError(t, err)
	assert.Equal(t, Metrics{Running: 2, Pending: 3, HighRisk: 5, Completed30d: 9}, view.Metrics)
	require.Len(t, view.ActiveTasks, 2)
	assert.Equal(t, "12345678-90ab-cdef-1234-567890abcdef", view.ActiveTasks[0].TaskID)
	assert.Equal(t, "MCP 扫描 · 12345678", view.ActiveTasks[0].Label)
	assert.Equal(t, "repository", view.ActiveTasks[0].SourceKind)
	assert.Nil(t, view.ActiveTasks[0].Phase)
	assert.Equal(t, tasks.StatusRunning, view.ActiveTasks[0].Status)
	assert.Equal(t, now.UTC(), view.ActiveTasks[0].UpdatedAt)
	assert.Equal(t, "legacy_unknown", view.ActiveTasks[1].SourceKind)
	require.Len(t, view.RecentRisks, 1)
	assert.Equal(t, "report-1", view.RecentRisks[0].ReportID)
	assert.Equal(t, "dangerous_tool", view.RecentRisks[0].Category)
	assert.Equal(t, "high", view.RecentRisks[0].Severity)
	assert.Equal(t, subject, tasksReader.subject)
	assert.Equal(t, subject, reportsReader.subject)
	assert.Equal(t, now, tasksReader.now)
	assert.Equal(t, now, reportsReader.now)

	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.ElementsMatch(t, []string{"metrics", "active_tasks", "recent_risks"}, mapKeys(wire))
	assert.ElementsMatch(t, []string{"running", "pending", "high_risk", "completed_30d"}, mapKeys(wire["metrics"].(map[string]any)))
	assert.ElementsMatch(t, []string{"task_id", "label", "source_kind", "phase", "status", "updated_at"}, mapKeys(wire["active_tasks"].([]any)[0].(map[string]any)))
	assert.ElementsMatch(t, []string{"report_id", "task_id", "severity", "category", "summary", "completed_at"}, mapKeys(wire["recent_risks"].([]any)[0].(map[string]any)))
	for _, forbidden := range []string{"must-not-appear", "content", "endpoint", "raw_result", "model_id", "headers", "authorization", "attachment", "log"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
}

func TestServiceMapsScopeErrorsAndPreservesFixedErrors(t *testing.T) {
	tasksReader := &recordingTaskReader{err: tasks.ErrForbidden}
	service := NewService(&recordingReportReader{}, tasksReader)
	_, err := service.Get(context.Background(), identity.Subject{Role: identity.RoleUser})
	assert.ErrorIs(t, err, ErrForbidden)

	service = NewService(&recordingReportReader{err: errors.New("postgres private-password")}, &recordingTaskReader{})
	_, err = service.Get(context.Background(), identity.Subject{Role: identity.RoleAdmin})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrForbidden)
}

func TestServiceBoundsDefensiveProjectionLists(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	active := make([]tasks.MCPWorkbenchTask, 0, 11)
	highlights := make([]reports.MCPRiskHighlight, 0, 6)
	for range 11 {
		active = append(active, tasks.MCPWorkbenchTask{TaskID: "abcdefgh-task", SourceKind: "service", Status: tasks.StatusPending, UpdatedAt: now})
	}
	for range 6 {
		highlights = append(highlights, reports.MCPRiskHighlight{ReportID: "report", TaskID: "task", Severity: "low", Category: "other", Summary: "safe", CompletedAt: now})
	}
	service := NewService(&recordingReportReader{projection: reports.MCPWorkbenchProjection{Highlights: highlights}}, &recordingTaskReader{projection: tasks.MCPWorkbenchProjection{ActiveTasks: active}})
	service.now = func() time.Time { return now }

	view, err := service.Get(context.Background(), identity.Subject{Role: identity.RoleAdmin})
	require.NoError(t, err)
	assert.Len(t, view.ActiveTasks, 10)
	assert.Len(t, view.RecentRisks, 5)
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

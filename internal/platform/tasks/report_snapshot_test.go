package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/common/portscan"
	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrustedSuccessPersistsOneBrandVersionedSnapshot(t *testing.T) {
	ctx := context.Background()
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	tasks := NewService(NewMemoryRepository(), engine, audit.NewService(audit.NewMemoryRepository()))
	brands := brand.NewService(brand.NewMemoryRepository())
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	logoV1 := taskSnapshotPNG(t, color.NRGBA{R: 22, G: 119, B: 255, A: 255})
	logoV2 := taskSnapshotPNG(t, color.NRGBA{R: 82, G: 196, B: 26, A: 255})
	brandV1, err := brands.Update(ctx, admin, brand.Config{ProductName: "v1", PrimaryColor: "#1677FF", Logo: logoV1, LogoMIME: "image/png"})
	require.NoError(t, err)
	snapshots := reports.NewMemoryRepository()
	tasks.SetReportSnapshotService(reports.NewService(snapshots, brands))
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	first := createRunningTask(t, tasks, owner, "snapshot-v1")
	raw := json.RawMessage(`{"id":"event-1","type":"resultUpdate","timestamp":1,"result":{"score":73,"results":[{"level":"high"},{"level":"medium"},{"level":"low"}]}}`)
	setEngineResult(engine, first.EngineSessionID, raw)

	require.NoError(t, tasks.RecordEngineEvent(ctx, first.EngineSessionID, EngineStateSucceeded, ""))
	require.NoError(t, tasks.RecordEngineEvent(ctx, first.EngineSessionID, EngineStateSucceeded, ""))
	storedTask, err := tasks.repository.Get(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, storedTask.Status)
	storedSnapshot, err := snapshots.GetByTaskID(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, raw, storedSnapshot.RawResult)
	assert.Equal(t, 1, storedSnapshot.Risk.High)
	assert.Equal(t, "risk-v2", storedSnapshot.Risk.MappingVersion)
	assert.Equal(t, 73, storedSnapshot.Risk.Score)
	assert.Equal(t, brandV1.ProductName, storedSnapshot.Brand.ProductName)
	assert.Equal(t, logoV1, storedSnapshot.Brand.Logo)
	assert.Equal(t, int64(1), engine.resultReads.Load())

	_, err = brands.Update(ctx, admin, brand.Config{ProductName: "v2", PrimaryColor: "#1677FF", Logo: logoV2, LogoMIME: "image/png"})
	require.NoError(t, err)
	unchanged, err := snapshots.GetByTaskID(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "v1", unchanged.Brand.ProductName)

	second := createRunningTask(t, tasks, owner, "snapshot-v2")
	setEngineResult(engine, second.EngineSessionID, json.RawMessage(`{"id":"event-2","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[{"level":"low"}]}}`))
	require.NoError(t, tasks.RecordEngineEvent(ctx, second.EngineSessionID, EngineStateSucceeded, ""))
	secondSnapshot, err := snapshots.GetByTaskID(ctx, second.ID)
	require.NoError(t, err)
	assert.Equal(t, "v2", secondSnapshot.Brand.ProductName)
}

func TestTrustedSuccessSnapshotNeverContainsTaskRemark(t *testing.T) {
	ctx := context.Background()
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	repository := NewMemoryRepository()
	tasks := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	snapshots := reports.NewMemoryRepository()
	tasks.SetReportSnapshotService(reports.NewService(snapshots, brand.NewService(brand.NewMemoryRepository())))
	owner := identity.Subject{UserID: "remark-snapshot-owner", Username: "alice", Role: identity.RoleUser}
	remark := "remark-must-not-enter-report-snapshot"
	created, err := tasks.Create(ctx, owner, CreateInput{
		IdempotencyKey: "remark-snapshot", TaskType: "mcp_scan", Content: "scan", Remark: remark,
	})
	require.NoError(t, err)
	setEngineResult(engine, created.EngineSessionID, json.RawMessage(`{"id":"remark-event","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[]}}`))

	require.NoError(t, tasks.RecordEngineEvent(ctx, created.EngineSessionID, EngineStateSucceeded, ""))
	snapshot, err := snapshots.GetByTaskID(ctx, created.ID)
	require.NoError(t, err)
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), remark)
	assert.NotContains(t, string(snapshot.RawResult), remark)
	assert.NotContains(t, string(snapshot.RenderData), remark)
}

func TestRecoveredSuccessPreservesTrustedEngineCompletionTime(t *testing.T) {
	ctx := context.Background()
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	tasks := NewService(NewMemoryRepository(), engine, audit.NewService(audit.NewMemoryRepository()))
	snapshots := reports.NewMemoryRepository()
	tasks.SetReportSnapshotService(reports.NewService(snapshots, brand.NewService(brand.NewMemoryRepository())))
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	task := createRunningTask(t, tasks, owner, "historical-completion")
	completedAt := time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC)
	setEngineResult(engine, task.EngineSessionID, json.RawMessage(`{"id":"historical-event","type":"resultUpdate","timestamp":1786354200,"result":{"score":100,"results":[]}}`))
	engine.mu.Lock()
	engine.status[task.EngineSessionID] = EngineStatus{State: EngineStateSucceeded, CompletedAt: completedAt}
	engine.mu.Unlock()

	require.NoError(t, tasks.RecordEngineEvent(ctx, task.EngineSessionID, EngineStateSucceeded, ""))
	snapshot, err := snapshots.GetByTaskID(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, completedAt, snapshot.CompletedAt)
}

func TestTrustedSuccessSnapshotDerivesInfrastructurePortScanFromPersistedParams(t *testing.T) {
	ctx := context.Background()
	completedAt := time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name     string
		params   string
		wantMode portscan.Mode
		wantSpec string
	}{
		{name: "fixed", params: `{"port_scan_mode":"fixed_ai"}`, wantMode: portscan.FixedAI, wantSpec: portscan.FixedAIPortSpec},
		{name: "full", params: `{"port_scan_mode":"full_tcp"}`, wantMode: portscan.FullTCP, wantSpec: portscan.FullTCPPortSpec},
		{name: "historical missing mode", params: `{}`, wantMode: portscan.FixedAI, wantSpec: portscan.FixedAIPortSpec},
		{name: "unknown mode omitted", params: `{"port_scan_mode":"agent-supplied"}`},
		{name: "duplicate canonical mode omitted", params: `{"port_scan_mode":"agent-supplied","port_scan_mode":"full_tcp"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			engine := &recordingEngine{results: map[string]json.RawMessage{}}
			repository := NewMemoryRepository()
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			snapshots := reports.NewMemoryRepository()
			service.SetReportSnapshotService(reports.NewService(snapshots, brand.NewService(brand.NewMemoryRepository())))
			id := "snapshot-port-" + testCase.name
			_, _, err := repository.CreateOrGet(ctx, &Task{ID: id, OwnerUserID: "alice", OwnerUsername: "alice", IdempotencyKey: id,
				EngineSessionID: "engine-" + id, TaskType: "ai_infra_scan", Content: "127.0.0.1", Params: json.RawMessage(testCase.params), AttachmentRefs: json.RawMessage(`[]`),
				Status: StatusRunning, CreatedAt: completedAt.Add(-time.Minute), UpdatedAt: completedAt})
			require.NoError(t, err)
			setEngineResult(engine, "engine-"+id, json.RawMessage(`{"id":"port-result","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[],"port_scan_mode":"agent-supplied","port_spec":"sentinel-port-spec"}}`))
			engine.mu.Lock()
			engine.status["engine-"+id] = EngineStatus{State: EngineStateSucceeded, CompletedAt: completedAt}
			engine.mu.Unlock()

			require.NoError(t, service.RecordEngineEvent(ctx, "engine-"+id, EngineStateSucceeded, ""))
			snapshot, err := snapshots.GetByTaskID(ctx, id)
			require.NoError(t, err)
			var render reports.RenderModel
			require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
			assert.Equal(t, string(testCase.wantMode), render.PortScanMode)
			assert.Equal(t, testCase.wantSpec, render.PortSpec)
			assert.NotContains(t, string(snapshot.RenderData), "agent-supplied")
			assert.NotContains(t, string(snapshot.RenderData), "sentinel-port-spec")
		})
	}
}

func TestTrustedSuccessRequiresNonZeroEngineCompletionTimeBeforeSnapshot(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status EngineStatus
		remove bool
	}{
		{name: "status unavailable", remove: true},
		{name: "zero completion time", status: EngineStatus{State: EngineStateSucceeded}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			engine := &recordingEngine{results: map[string]json.RawMessage{}}
			repository := NewMemoryRepository()
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			snapshots := reports.NewMemoryRepository()
			service.SetReportSnapshotService(reports.NewService(snapshots, brand.NewService(brand.NewMemoryRepository())))
			task := createRunningTask(t, service, identity.Subject{UserID: "alice", Role: identity.RoleUser}, "missing-completion-"+testCase.name)
			setEngineResult(engine, task.EngineSessionID, json.RawMessage(`{"id":"completion-event","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[]}}`))
			engine.mu.Lock()
			if testCase.remove {
				delete(engine.status, task.EngineSessionID)
			} else {
				engine.status[task.EngineSessionID] = testCase.status
			}
			engine.mu.Unlock()

			err := service.RecordEngineEvent(ctx, task.EngineSessionID, EngineStateSucceeded, "")
			require.Error(t, err)
			stored, getErr := repository.Get(ctx, task.ID)
			require.NoError(t, getErr)
			assert.Equal(t, StatusRunning, stored.Status)
			_, snapshotErr := snapshots.GetByTaskID(ctx, task.ID)
			assert.ErrorIs(t, snapshotErr, reports.ErrNotFound)
		})
	}
}

func taskSnapshotPNG(t *testing.T, value color.NRGBA) []byte {
	t.Helper()
	logo := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	logo.Set(0, 0, value)
	var encoded bytes.Buffer
	require.NoError(t, png.Encode(&encoded, logo))
	return encoded.Bytes()
}

func TestTrustedSuccessKeepsTaskRunningWhenSnapshotPersistenceFails(t *testing.T) {
	ctx := context.Background()
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	audits := audit.NewMemoryRepository()
	tasks := NewService(NewMemoryRepository(), engine, audit.NewService(audits))
	tasks.SetReportSnapshotService(&failingSnapshotter{err: errors.New("snapshot persistence failed")})
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	task := createRunningTask(t, tasks, owner, "snapshot-failure")
	setEngineResult(engine, task.EngineSessionID, json.RawMessage(`{"id":"event-3","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[]}}`))

	err := tasks.RecordEngineEvent(ctx, task.EngineSessionID, EngineStateSucceeded, "")
	require.Error(t, err)
	stored, getErr := tasks.repository.Get(ctx, task.ID)
	require.NoError(t, getErr)
	assert.Equal(t, StatusRunning, stored.Status)
	events, listErr := audits.List(ctx, audit.Filter{ResourceID: task.ID})
	require.NoError(t, listErr)
	for _, event := range events {
		if event.Action == audit.ActionTaskChanged {
			assert.NotEqual(t, audit.OutcomeSuccess, event.Outcome)
		}
	}
}

func createRunningTask(t *testing.T, service *Service, owner identity.Subject, key string) View {
	t.Helper()
	created, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: key, TaskType: "mcp_scan", Content: "scan"})
	require.NoError(t, err)
	require.Equal(t, StatusRunning, created.Status)
	return created
}

func setEngineResult(engine *recordingEngine, sessionID string, raw json.RawMessage) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.status == nil {
		engine.status = map[string]EngineStatus{}
	}
	engine.status[sessionID] = EngineStatus{
		State:       EngineStateSucceeded,
		CompletedAt: time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC),
	}
	engine.results[sessionID] = append(json.RawMessage(nil), raw...)
}

type failingSnapshotter struct{ err error }

func (snapshotter *failingSnapshotter) Prepare(context.Context, reports.CompletedTask) (*reports.Snapshot, error) {
	return &reports.Snapshot{ID: "candidate"}, nil
}

func (snapshotter *failingSnapshotter) Persist(context.Context, *reports.Snapshot) error {
	return snapshotter.err
}

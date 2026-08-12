package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletedTaskSourceReadsOnlySucceededEngineResultAndCopiesTerminalMetadata(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	platformUpdatedAt := time.Date(2026, 8, 12, 18, 10, 0, 0, time.UTC)
	for _, status := range []Status{StatusPending, StatusDispatching, StatusRunning, StatusEngineFailed, StatusCancelled, StatusDispatchFailed, StatusDispatchUnknown} {
		putCompletedSourceTask(t, repository, "blocked-"+string(status), status, platformUpdatedAt)
		_, err := service.GetCompletedTask(ctx, "blocked-"+string(status))
		require.Error(t, err, status)
	}
	assert.Zero(t, engine.resultReads.Load(), "non-succeeded tasks must not read the engine result")

	putCompletedSourceTask(t, repository, "succeeded", StatusSucceeded, platformUpdatedAt)
	setEngineResult(engine, "engine-succeeded", json.RawMessage(`{"findings":[{"severity":"high"}]}`))
	engineCompletedAt := time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC)
	engine.mu.Lock()
	engine.status["engine-succeeded"] = EngineStatus{State: EngineStateSucceeded, CompletedAt: engineCompletedAt}
	engine.mu.Unlock()
	completed, err := service.GetCompletedTask(ctx, "succeeded")
	require.NoError(t, err)
	assert.Equal(t, reports.CompletedTask{TaskID: "succeeded", OwnerUserID: "owner", TaskType: "mcp_scan", RawResult: json.RawMessage(`{"findings":[{"severity":"high"}]}`), CompletedAt: engineCompletedAt}, completed)
	completed.RawResult[2] = 'X'
	assert.Equal(t, int64(1), engine.resultReads.Load())
	engine.mu.Lock()
	assert.JSONEq(t, `{"findings":[{"severity":"high"}]}`, string(engine.results["engine-succeeded"]))
	engine.mu.Unlock()
}

func TestCompletedTaskSourceRequiresTrustedNonZeroEngineCompletionTime(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status EngineStatus
		remove bool
	}{
		{name: "status unavailable", remove: true},
		{name: "not succeeded", status: EngineStatus{State: EngineStateRunning}},
		{name: "zero completion time", status: EngineStatus{State: EngineStateSucceeded}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{results: map[string]json.RawMessage{}}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			putCompletedSourceTask(t, repository, "untrusted-time", StatusSucceeded, time.Now().UTC())
			setEngineResult(engine, "engine-untrusted-time", json.RawMessage(`{"findings":[]}`))
			engine.mu.Lock()
			if testCase.remove {
				delete(engine.status, "engine-untrusted-time")
			} else {
				engine.status["engine-untrusted-time"] = testCase.status
			}
			engine.mu.Unlock()

			_, err := service.GetCompletedTask(context.Background(), "untrusted-time")
			require.Error(t, err)
			assert.Zero(t, engine.resultReads.Load(), "raw result must not be read before trusted completion time is confirmed")
		})
	}
}

func TestCompletedTaskSourceHidesEngineErrorsAndUsesFixedMissingTaskError(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	_, err := service.GetCompletedTask(context.Background(), "missing")
	assert.ErrorIs(t, err, ErrNotFound)

	putCompletedSourceTask(t, repository, "broken", StatusSucceeded, time.Now().UTC())
	engine.resultErr = errors.New("engine token secret")
	_, err = service.GetCompletedTask(context.Background(), "broken")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "token")
	assert.NotContains(t, err.Error(), "secret")
}

func putCompletedSourceTask(t *testing.T, repository *MemoryRepository, id string, status Status, updatedAt time.Time) {
	t.Helper()
	_, _, err := repository.CreateOrGet(context.Background(), &Task{ID: id, OwnerUserID: "owner", OwnerUsername: "owner", IdempotencyKey: id,
		EngineSessionID: "engine-" + id, TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
		Status: status, CreatedAt: updatedAt.Add(-time.Minute), UpdatedAt: updatedAt})
	require.NoError(t, err)
}

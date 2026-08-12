package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type recordingEngine struct {
	submits     atomic.Int64
	statusReads atomic.Int64
	resultReads atomic.Int64
	err         error
	resultErr   error
	mu          sync.Mutex
	status      map[string]EngineStatus
	results     map[string]json.RawMessage
	last        EngineTask
}

func (engine *recordingEngine) SubmitTask(_ context.Context, task EngineTask) (string, error) {
	engine.submits.Add(1)
	engine.mu.Lock()
	engine.last = task
	engine.mu.Unlock()
	if engine.err != nil {
		return "", engine.err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.status == nil {
		engine.status = map[string]EngineStatus{}
	}
	engine.status[task.PlatformTaskID] = EngineStatus{State: EngineStateRunning}
	return task.PlatformTaskID, nil
}

func (engine *recordingEngine) GetTaskStatus(_ context.Context, sessionID string) (EngineStatus, error) {
	engine.statusReads.Add(1)
	engine.mu.Lock()
	defer engine.mu.Unlock()
	status, ok := engine.status[sessionID]
	if !ok {
		return EngineStatus{}, ErrEngineTaskNotFound
	}
	return status, nil
}

type leaseRaceEngine struct {
	submits      atomic.Int64
	firstEntered chan struct{}
	releaseFirst chan struct{}
}

func (engine *leaseRaceEngine) SubmitTask(_ context.Context, task EngineTask) (string, error) {
	if engine.submits.Add(1) == 1 {
		close(engine.firstEntered)
		<-engine.releaseFirst
		return task.PlatformTaskID, nil
	}
	return "", errors.New("current claim dispatch failed")
}

func (*leaseRaceEngine) GetTaskStatus(context.Context, string) (EngineStatus, error) {
	return EngineStatus{}, ErrEngineTaskNotFound
}

func (*leaseRaceEngine) GetResult(context.Context, string) (json.RawMessage, error) {
	return nil, ErrResultNotReady
}

func (*leaseRaceEngine) CancelTask(context.Context, string) error { return nil }

type blockingCancelEngine struct {
	recordingEngine
	cancelEntered chan struct{}
	releaseCancel chan struct{}
}

type recoveryTimeoutEngine struct {
	statusReads atomic.Int64
	statuses    map[string]EngineStatus
}

func (*recoveryTimeoutEngine) SubmitTask(context.Context, EngineTask) (string, error) {
	return "", ErrEngineTaskNotFound
}
func (engine *recoveryTimeoutEngine) GetTaskStatus(ctx context.Context, sessionID string) (EngineStatus, error) {
	engine.statusReads.Add(1)
	if sessionID == "engine-blocked" {
		<-ctx.Done()
		return EngineStatus{}, ctx.Err()
	}
	status, ok := engine.statuses[sessionID]
	if !ok {
		return EngineStatus{}, ErrEngineTaskNotFound
	}
	return status, nil
}
func (*recoveryTimeoutEngine) GetResult(context.Context, string) (json.RawMessage, error) {
	return nil, ErrResultNotReady
}
func (*recoveryTimeoutEngine) CancelTask(context.Context, string) error { return nil }

func (engine *blockingCancelEngine) CancelTask(context.Context, string) error {
	close(engine.cancelEntered)
	<-engine.releaseCancel
	return nil
}

func (engine *recordingEngine) GetResult(_ context.Context, sessionID string) (json.RawMessage, error) {
	engine.resultReads.Add(1)
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.resultErr != nil {
		return nil, engine.resultErr
	}
	result, ok := engine.results[sessionID]
	if !ok {
		return nil, ErrResultNotReady
	}
	return append(json.RawMessage(nil), result...), nil
}

func TestCompletedResultRecoveryFiltersTerminalTasksAndContinuesAfterOneTimedOutEngine(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	for index := 0; index < 250; index++ {
		status := StatusSucceeded
		if index%2 == 1 {
			status = StatusCancelled
		}
		_, _, err := repository.CreateOrGet(ctx, &Task{
			ID: fmt.Sprintf("terminal-%03d", index), OwnerUserID: "alice", IdempotencyKey: fmt.Sprintf("terminal-%03d", index),
			EngineSessionID: fmt.Sprintf("terminal-engine-%03d", index), Status: status,
		})
		require.NoError(t, err)
	}
	for _, task := range []Task{
		{ID: "recoverable-001", OwnerUserID: "alice", IdempotencyKey: "recoverable-001", EngineSessionID: "engine-blocked", Status: StatusRunning},
		{ID: "recoverable-002", OwnerUserID: "alice", IdempotencyKey: "recoverable-002", EngineSessionID: "engine-success", Status: StatusRunning},
		{ID: "no-engine-session", OwnerUserID: "alice", IdempotencyKey: "no-engine-session", Status: StatusRunning},
	} {
		candidate := task
		_, _, err := repository.CreateOrGet(ctx, &candidate)
		require.NoError(t, err)
	}

	listed, err := repository.ListRecoverable(ctx, "", completedResultRecoveryBatchSize)
	require.NoError(t, err)
	require.Len(t, listed, 2, "terminal tasks and tasks without an engine session must be filtered by the repository")
	engine := &recoveryTimeoutEngine{statuses: map[string]EngineStatus{"engine-success": {State: EngineStateSucceeded}}}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	service.recoveryItemTimeout = 10 * time.Millisecond

	err = service.ReconcileCompletedEngineResults(ctx)
	require.Error(t, err, "a timed-out item is reported without aborting the bounded pass")
	stored, err := repository.Get(ctx, "recoverable-002")
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, stored.Status, "a later durable completion must still converge")
	assert.Equal(t, int64(2), engine.statusReads.Load(), "filtered terminal and empty-session tasks must not reach the engine")
}

func TestCompletedResultRecoveryProcessesAtMostOneKeysetBatchPerPass(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	engine := &recoveryTimeoutEngine{statuses: map[string]EngineStatus{}}
	for index := 0; index < completedResultRecoveryBatchSize*2+5; index++ {
		id := fmt.Sprintf("bounded-%03d", index)
		engineID := "engine-" + id
		engine.statuses[engineID] = EngineStatus{State: EngineStateRunning}
		_, _, err := repository.CreateOrGet(ctx, &Task{
			ID: id, OwnerUserID: "alice", IdempotencyKey: id, EngineSessionID: engineID, Status: StatusRunning,
		})
		require.NoError(t, err)
	}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))

	require.NoError(t, service.ReconcileCompletedEngineResults(ctx))
	assert.Equal(t, int64(completedResultRecoveryBatchSize), engine.statusReads.Load())
	require.NoError(t, service.ReconcileCompletedEngineResults(ctx))
	assert.Equal(t, int64(completedResultRecoveryBatchSize*2), engine.statusReads.Load(), "the next pass advances the keyset cursor")
}

func (engine *recordingEngine) CancelTask(context.Context, string) error { return nil }

func TestCancelCannotOverwriteConcurrentTerminalEngineState(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &blockingCancelEngine{cancelEntered: make(chan struct{}), releaseCancel: make(chan struct{})}
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	owner := identity.Subject{UserID: "cancel-owner", Username: "alice", Role: identity.RoleUser}
	now := time.Now().UTC()
	task := &Task{
		ID: "cancel-terminal-race", OwnerUserID: owner.UserID, OwnerUsername: owner.Username,
		IdempotencyKey: "cancel-terminal-race", EngineSessionID: "cancel-terminal-race",
		TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
		Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	_, _, err := repository.CreateOrGet(context.Background(), task)
	require.NoError(t, err)

	cancelDone := make(chan error, 1)
	go func() { cancelDone <- service.Cancel(context.Background(), owner, task.ID) }()
	select {
	case <-engine.cancelEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not reach the deterministic barrier")
	}
	transitioned, err := repository.TransitionStatus(context.Background(), task.ID, []Status{StatusRunning}, StatusSucceeded, "", now.Add(time.Second))
	require.NoError(t, err)
	require.True(t, transitioned)
	close(engine.releaseCancel)
	require.NoError(t, <-cancelDone)

	stored, err := repository.Get(context.Background(), task.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, stored.Status)
	events, err := auditRepository.List(context.Background(), audit.Filter{Action: audit.Action("task.cancelled"), ResourceID: task.ID})
	require.NoError(t, err)
	for _, event := range events {
		assert.NotEqual(t, audit.OutcomeSuccess, event.Outcome, "lost cancel CAS must not produce a successful cancelled audit")
	}
	late, err := repository.TransitionStatus(context.Background(), task.ID, []Status{StatusRunning}, StatusEngineFailed, "agent reported task failure", now.Add(2*time.Second))
	require.NoError(t, err)
	assert.False(t, late, "a late engine terminal update must not replace the winner")
}

func TestConcurrentIdempotentCreatePersistsOneTaskAndSubmitsOnce(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	input := CreateInput{IdempotencyKey: "same-key", TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{"model_id":"model-1"}`)}

	start := make(chan struct{})
	results := make(chan View, 12)
	errs := make(chan error, 12)
	var calls sync.WaitGroup
	for range 12 {
		calls.Add(1)
		go func() {
			defer calls.Done()
			<-start
			view, err := service.Create(context.Background(), subject, input)
			results <- view
			errs <- err
		}()
	}
	close(start)
	calls.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	var taskID string
	for result := range results {
		if taskID == "" {
			taskID = result.ID
		}
		assert.Equal(t, taskID, result.ID)
	}
	tasks, err := repository.List(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, int64(1), engine.submits.Load())
	assert.Equal(t, StatusRunning, tasks[0].Status)
}

func TestCreateRejectsRawModelCredentialsRecursivelyButAllowsModelIDs(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	for name, params := range map[string]string{
		"top-level token": `{"token":"plain-secret"}`,
		"nested api key":  `{"provider":{"api_key":"plain-secret"}}`,
		"legacy model":    `{"model":{"token":"plain-secret","base_url":"https://model.invalid"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "secret-" + name, TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(params),
			})
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
	assert.Zero(t, engine.submits.Load())

	_, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "model-ids", TaskType: "mcp_scan", Content: "scan",
		Params: json.RawMessage(`{"model_id":"model-1","eval_model_id":"model-2"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), engine.submits.Load())
}

func TestDispatchFailureRetainsTaskAndRecordsSeparateStatus(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{err: NewTransientDispatchError(errors.New("agent temporarily unavailable"))}
	audits := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audits))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	view, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "dispatch-failure", TaskType: "mcp_scan", Content: "scan",
	})
	require.ErrorIs(t, err, ErrDispatchFailed)
	assert.NotEmpty(t, view.ID)

	stored, getErr := repository.Get(context.Background(), view.ID)
	require.NoError(t, getErr)
	assert.Equal(t, StatusDispatchFailed, stored.Status)
	assert.Equal(t, "engine temporarily unavailable", stored.DispatchError)
	assert.NotZero(t, stored.DispatchAttempts)
	assert.LessOrEqual(t, stored.DispatchAttempts, MaxDispatchAttempts)

	events, listErr := audits.List(context.Background(), audit.Filter{ResourceID: view.ID})
	require.NoError(t, listErr)
	var actions []audit.Action
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	assert.Contains(t, actions, audit.Action("task.created"))
	assert.Contains(t, actions, audit.Action("task.dispatch_failed"))
}

func TestDispatchFailureNeverPersistsOrReturnsEngineSecrets(t *testing.T) {
	const secret = "engine-plain-secret"
	repository := NewMemoryRepository()
	service := NewService(repository, &recordingEngine{err: NewTransientDispatchError(errors.New("unavailable " + secret))}, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	view, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "safe-dispatch-error", TaskType: "mcp_scan", Content: "scan",
	})
	require.ErrorIs(t, err, ErrDispatchFailed)
	assert.NotContains(t, view.DispatchError, secret)
	stored, getErr := repository.Get(context.Background(), view.ID)
	require.NoError(t, getErr)
	assert.NotContains(t, stored.DispatchError, secret)
}

func TestTaskAuthorizationUsesOwnerUserIDAndRole(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository, &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	task, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "owner-test", TaskType: "mcp_scan", Content: "scan"})
	require.NoError(t, err)

	_, err = service.Get(context.Background(), identity.Subject{UserID: "user-2", Username: "alice", Role: identity.RoleUser}, task.ID)
	require.ErrorIs(t, err, ErrForbidden)
	_, err = service.Get(context.Background(), identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}, task.ID)
	require.NoError(t, err)
	err = service.Cancel(context.Background(), identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}, task.ID)
	require.ErrorIs(t, err, ErrForbidden)
	err = service.Cancel(context.Background(), identity.Subject{UserID: "admin", Role: identity.RoleAdmin}, task.ID)
	require.NoError(t, err)

	stored, err := repository.Get(context.Background(), task.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusCancelled, stored.Status)
}

func TestTaskListUsesOwnerUserIDAndGlobalReadRolesWithoutEnginePoll(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	alice := identity.Subject{UserID: "list-alice", Username: "alice", Role: identity.RoleUser}
	bob := identity.Subject{UserID: "list-bob", Username: "bob", Role: identity.RoleUser}
	aliceTask, err := service.Create(context.Background(), alice, CreateInput{IdempotencyKey: "alice", TaskType: "mcp_scan", Content: "scan"})
	require.NoError(t, err)
	bobTask, err := service.Create(context.Background(), bob, CreateInput{IdempotencyKey: "bob", TaskType: "mcp_scan", Content: "scan"})
	require.NoError(t, err)
	readsBefore := engine.statusReads.Load()

	aliceTasks, err := service.List(context.Background(), alice)
	require.NoError(t, err)
	require.Len(t, aliceTasks, 1)
	assert.Equal(t, aliceTask.ID, aliceTasks[0].ID)
	auditorTasks, err := service.List(context.Background(), identity.Subject{UserID: "auditor", Role: identity.RoleAuditor})
	require.NoError(t, err)
	require.Len(t, auditorTasks, 2)
	assert.ElementsMatch(t, []string{aliceTask.ID, bobTask.ID}, []string{auditorTasks[0].ID, auditorTasks[1].ID})
	adminTasks, err := service.List(context.Background(), identity.Subject{UserID: "admin", Role: identity.RoleAdmin})
	require.NoError(t, err)
	assert.Equal(t, auditorTasks, adminTasks)
	assert.Equal(t, readsBefore, engine.statusReads.Load())
}

func TestCreateRequiresBoundedIdempotencyKey(t *testing.T) {
	service := NewService(NewMemoryRepository(), &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	_, err := service.Create(context.Background(), subject, CreateInput{TaskType: "mcp_scan", Content: "scan"})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = service.Create(context.Background(), subject, CreateInput{IdempotencyKey: string(make([]byte, MaxIdempotencyKeyLength+1)), TaskType: "mcp_scan", Content: "scan"})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestMemoryRepositoryListUsesStableCreatedAtAndIDAscendingOrder(t *testing.T) {
	repository := NewMemoryRepository()
	base := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	fixtures := []Task{
		{ID: "task-d", OwnerUserID: "owner-d", IdempotencyKey: "key-d", CreatedAt: base.Add(time.Minute)},
		{ID: "task-b", OwnerUserID: "owner-b", IdempotencyKey: "key-b", CreatedAt: base},
		{ID: "task-f", OwnerUserID: "owner-f", IdempotencyKey: "key-f", CreatedAt: base.Add(2 * time.Minute)},
		{ID: "task-a", OwnerUserID: "owner-a", IdempotencyKey: "key-a", CreatedAt: base},
		{ID: "task-e", OwnerUserID: "owner-e", IdempotencyKey: "key-e", CreatedAt: base.Add(time.Minute)},
		{ID: "task-c", OwnerUserID: "owner-c", IdempotencyKey: "key-c", CreatedAt: base.Add(time.Minute)},
	}
	for index := range fixtures {
		_, created, err := repository.CreateOrGet(context.Background(), &fixtures[index])
		require.NoError(t, err)
		require.True(t, created)
	}

	expected := []string{"task-a", "task-b", "task-c", "task-d", "task-e", "task-f"}
	for attempt := 0; attempt < 128; attempt++ {
		tasks, err := repository.List(context.Background())
		require.NoError(t, err)
		actual := make([]string, 0, len(tasks))
		for index := range tasks {
			actual = append(actual, tasks[index].ID)
		}
		require.Equal(t, expected, actual, "list attempt %d", attempt)
	}
}

func TestGormRepositoryUsesUniqueOwnerIdempotencyAndCASDispatchLease(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_tasks").Error)
	repository := NewGormRepository(db)
	now := time.Now().UTC()

	start := make(chan struct{})
	created := atomic.Int64{}
	var calls sync.WaitGroup
	for index := range 16 {
		calls.Add(1)
		go func(index int) {
			defer calls.Done()
			<-start
			_, wasCreated, createErr := repository.CreateOrGet(context.Background(), &Task{
				ID: "task-" + string(rune('a'+index)), OwnerUserID: "owner-1", OwnerUsername: "alice",
				IdempotencyKey: "same-key", EngineSessionID: "engine-" + string(rune('a'+index)),
				TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`),
				AttachmentRefs: json.RawMessage(`[]`), Status: StatusPending, CreatedAt: now, UpdatedAt: now,
			})
			require.NoError(t, createErr)
			if wasCreated {
				created.Add(1)
			}
		}(index)
	}
	close(start)
	calls.Wait()
	assert.Equal(t, int64(1), created.Load())

	tasks, err := repository.List(context.Background())
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	taskID := tasks[0].ID
	claims := atomic.Int64{}
	start = make(chan struct{})
	for range 12 {
		calls.Add(1)
		go func() {
			defer calls.Done()
			<-start
			_, claimed, claimErr := repository.ClaimDispatch(context.Background(), taskID, now, now.Add(time.Minute))
			require.NoError(t, claimErr)
			if claimed {
				claims.Add(1)
			}
		}()
	}
	close(start)
	calls.Wait()
	assert.Equal(t, int64(1), claims.Load())

	_, claimed, err := repository.ClaimDispatch(context.Background(), taskID, now.Add(2*time.Minute), now.Add(3*time.Minute))
	require.NoError(t, err)
	assert.True(t, claimed, "an expired dispatch lease must be recoverable by a later idempotent request")
}

func TestExpiredPostgresDispatchClaimCannotOverwriteCurrentClaim(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_tasks").Error)
	repository := NewGormRepository(db)
	engine := &leaseRaceEngine{firstEntered: make(chan struct{}), releaseFirst: make(chan struct{})}
	owner := identity.Subject{UserID: "lease-owner", Username: "alice", Role: identity.RoleUser}
	input := CreateInput{IdempotencyKey: "lease-race", TaskType: "mcp_scan", Content: "scan"}
	base := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	first := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	first.now = func() time.Time { return base }
	firstDone := make(chan error, 1)
	go func() {
		_, createErr := first.Create(context.Background(), owner, input)
		firstDone <- createErr
	}()
	select {
	case <-engine.firstEntered:
	case firstErr := <-firstDone:
		require.NoError(t, firstErr)
		t.Fatal("first dispatcher exited before entering SubmitTask")
	}

	second := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	second.now = func() time.Time { return base.Add(dispatchLeaseDuration + time.Second) }
	secondView, secondErr := second.Create(context.Background(), owner, input)
	require.ErrorIs(t, secondErr, ErrDispatchFailed)
	assert.Equal(t, StatusDispatchFailed, secondView.Status)
	close(engine.releaseFirst)
	require.ErrorIs(t, <-firstDone, ErrDispatchLeaseLost)

	stored, err := repository.Get(context.Background(), secondView.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusDispatchFailed, stored.Status, "the expired claimant must not overwrite the current claim")
	assert.Equal(t, int64(2), engine.submits.Load())
}

func TestPostgresDispatchAttemptBudgetPersistsAcrossRecoveredLease(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_tasks").Error)
	repository := NewGormRepository(db)
	owner := identity.Subject{UserID: "attempt-owner", Username: "alice", Role: identity.RoleUser}
	input := CreateInput{IdempotencyKey: "attempt-budget", TaskType: "mcp_scan", Content: "scan"}
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(owner.UserID+"\x00"+input.IdempotencyKey)).String()
	base := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	_, created, err := repository.CreateOrGet(context.Background(), &Task{
		ID: taskID, OwnerUserID: owner.UserID, OwnerUsername: owner.Username,
		IdempotencyKey: input.IdempotencyKey, EngineSessionID: taskID, TaskType: input.TaskType,
		Content: input.Content, Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
		Status: StatusPending, DispatchAttempts: MaxDispatchAttempts - 1, CreatedAt: base, UpdatedAt: base,
	})
	require.NoError(t, err)
	require.True(t, created)
	engine := &recordingEngine{err: NewTransientDispatchError(errors.New("definitely not accepted"))}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	service.now = func() time.Time { return base.Add(time.Minute) }

	view, err := service.Create(context.Background(), owner, input)
	require.ErrorIs(t, err, ErrDispatchFailed)
	assert.Equal(t, int64(1), engine.submits.Load(), "only the globally remaining attempt may call the engine")
	assert.Equal(t, MaxDispatchAttempts, view.DispatchAttempts)
}

func TestAcknowledgementUnknownPendingStatusNeverResubmits(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{err: ErrSubmitAcknowledgementUnknown, status: map[string]EngineStatus{}}
	owner := identity.Subject{UserID: "ack-owner", Username: "alice", Role: identity.RoleUser}
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(owner.UserID+"\x00ack-pending")).String()
	engine.status[taskID] = EngineStatus{State: EngineStatePending}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))

	view, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "ack-pending", TaskType: "mcp_scan", Content: "scan"})
	require.ErrorIs(t, err, ErrDispatchFailed)
	assert.Equal(t, StatusDispatchUnknown, view.Status)
	assert.Equal(t, int64(1), engine.submits.Load(), "an uncertain acknowledgement must be read back, never submitted again")
}

func TestAuditorGetIsReadOnlyAndDoesNotPollEngine(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	owner := identity.Subject{UserID: "read-owner", Username: "alice", Role: identity.RoleUser}
	created, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "auditor-read", TaskType: "mcp_scan", Content: "scan"})
	require.NoError(t, err)
	engine.mu.Lock()
	engine.status[created.EngineSessionID] = EngineStatus{State: EngineStateSucceeded}
	engine.mu.Unlock()
	beforeEvents, err := auditRepository.List(context.Background(), audit.Filter{})
	require.NoError(t, err)
	readsBefore := engine.statusReads.Load()

	view, err := service.Get(context.Background(), identity.Subject{UserID: "auditor", Username: "auditor", Role: identity.RoleAuditor}, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, view.Status)
	assert.Equal(t, readsBefore, engine.statusReads.Load())
	afterEvents, err := auditRepository.List(context.Background(), audit.Filter{})
	require.NoError(t, err)
	assert.Len(t, afterEvents, len(beforeEvents))
	stored, err := repository.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, stored.Status)
}

func TestRunningTaskReconcilesOnlyThroughTrustedEngineEvent(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	created, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "refresh", TaskType: "mcp_scan", Content: "scan"})
	require.NoError(t, err)

	engine.mu.Lock()
	engine.results = map[string]json.RawMessage{created.EngineSessionID: json.RawMessage(`{"result":"safe"}`)}
	engine.mu.Unlock()
	require.NoError(t, service.RecordEngineEvent(context.Background(), created.EngineSessionID, EngineStateSucceeded, ""))
	refreshed, err := service.Get(context.Background(), owner, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, refreshed.Status)

	result, err := service.Result(context.Background(), owner, created.ID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"result":"safe"}`, string(result))
	_, err = service.Result(context.Background(), identity.Subject{UserID: "other", Role: identity.RoleUser}, created.ID)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestPendingEngineReadbackDoesNotPretendSubmissionIsRunning(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{err: NewTransientDispatchError(errors.New("temporary")), status: map[string]EngineStatus{}}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(owner.UserID+"\x00pending-readback")).String()
	engine.status[taskID] = EngineStatus{State: EngineStatePending}

	view, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "pending-readback", TaskType: "mcp_scan", Content: "scan"})
	require.ErrorIs(t, err, ErrDispatchFailed)
	assert.Equal(t, StatusDispatchFailed, view.Status)
	assert.Equal(t, int64(MaxDispatchAttempts), engine.submits.Load())
}

func TestAttachmentUploadIsPrivateBoundedAndResolvesOnlyForOwningTask(t *testing.T) {
	repository := NewMemoryRepository()
	attachmentAudits := audit.NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, audit.NewService(attachmentAudits))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	_, err = attachments.Upload(context.Background(), owner, "too-large.txt", strings.NewReader("123456789"))
	require.ErrorIs(t, err, ErrAttachmentTooLarge)
	failureEvents, err := attachmentAudits.List(context.Background(), audit.Filter{
		Action: audit.ActionAttachmentCreated, ActorUserID: owner.UserID,
	})
	require.NoError(t, err)
	require.Len(t, failureEvents, 2)
	assert.Equal(t, audit.OutcomePending, failureEvents[0].Outcome)
	assert.Equal(t, audit.OutcomeFailure, failureEvents[1].Outcome)
	assert.NotContains(t, string(failureEvents[1].Metadata), attachments.config.UploadDir)
	view, err := attachments.Upload(context.Background(), owner, "targets.txt", strings.NewReader("target-1"))
	require.NoError(t, err)
	assert.NotEmpty(t, view.ID)
	assert.Equal(t, "targets.txt", view.Filename)
	serialized, err := json.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "storage")
	assert.NotContains(t, string(serialized), attachments.config.UploadDir)

	_, _, _, err = attachments.Open(context.Background(), identity.Subject{UserID: "other", Role: identity.RoleUser}, view.ID)
	require.ErrorIs(t, err, ErrForbidden)
	file, _, _, err := attachments.Open(context.Background(), identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}, view.ID)
	require.NoError(t, err)
	file.Close()

	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	service.SetAttachmentService(attachments)
	created, err := service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "with-attachment", TaskType: "ai_infra_scan", Content: "scan", AttachmentIDs: []string{view.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{view.ID}, created.AttachmentIDs)
	engine.mu.Lock()
	require.Len(t, engine.last.Attachments, 1)
	assert.NotEqual(t, view.ID, engine.last.Attachments[0])
	assert.NotContains(t, engine.last.Attachments[0], attachments.config.UploadDir)
	engine.mu.Unlock()

	_, err = service.Create(context.Background(), identity.Subject{UserID: "other", Username: "mallory", Role: identity.RoleUser}, CreateInput{
		IdempotencyKey: "forged-attachment", TaskType: "ai_infra_scan", Content: "scan", AttachmentIDs: []string{view.ID},
	})
	require.ErrorIs(t, err, ErrForbidden)
}

func TestInternalArtifactUploadDerivesOwnerFromTrustedPlatformTask(t *testing.T) {
	repository := NewMemoryRepository()
	owner := identity.Subject{UserID: "user-artifact-owner", Username: "alice", Role: identity.RoleUser}
	service := NewService(repository, &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	task, err := service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "artifact-owner", TaskType: "ai_infra_scan", Content: "scan",
	})
	require.NoError(t, err)
	attachmentAudits := audit.NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, audit.NewService(attachmentAudits))
	require.NoError(t, err)

	artifact, err := attachments.UploadForEngineSession(context.Background(), task.EngineSessionID, "result.json", strings.NewReader("private"))
	require.NoError(t, err)
	assert.NotEmpty(t, artifact.ID)
	file, _, _, err := attachments.Open(context.Background(), owner, artifact.ID)
	require.NoError(t, err)
	file.Close()
	_, _, _, err = attachments.Open(context.Background(), identity.Subject{UserID: "other", Role: identity.RoleUser}, artifact.ID)
	require.ErrorIs(t, err, ErrForbidden)
	events, err := attachmentAudits.List(context.Background(), audit.Filter{ResourceID: artifact.ID})
	require.NoError(t, err)
	assert.NotEmpty(t, events)
	assert.Equal(t, audit.ActionAttachmentCreated, events[len(events)-1].Action)
	_, err = attachments.UploadForEngineSession(context.Background(), "browser-supplied-engine-id", "result.json", strings.NewReader("private"))
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, repository.UpdateStatus(context.Background(), task.ID, StatusSucceeded, time.Now().UTC()))
	_, err = attachments.UploadForEngineSession(context.Background(), task.EngineSessionID, "late.json", strings.NewReader("private"))
	require.ErrorIs(t, err, ErrForbidden)
}

func TestChunkUploadEnforcesSingleAndCumulativeLimitsAndMergeSize(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	view, err := attachments.BeginChunked(context.Background(), owner, "chunked.txt", 7)
	require.NoError(t, err)

	err = attachments.UploadChunk(context.Background(), owner, view.ID, 0, strings.NewReader("12345"))
	require.ErrorIs(t, err, ErrAttachmentTooLarge)
	require.NoError(t, attachments.UploadChunk(context.Background(), owner, view.ID, 0, strings.NewReader("1234")))
	require.NoError(t, attachments.UploadChunk(context.Background(), owner, view.ID, 1, strings.NewReader("567")))
	err = attachments.UploadChunk(context.Background(), owner, view.ID, 2, strings.NewReader("8"))
	require.ErrorIs(t, err, ErrAttachmentTooLarge)

	_, err = attachments.Merge(context.Background(), owner, view.ID, 2, 6)
	require.ErrorIs(t, err, ErrAttachmentSizeMismatch)
	merged, err := attachments.Merge(context.Background(), owner, view.ID, 2, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(7), merged.Size)
	file, _, _, err := attachments.Open(context.Background(), owner, view.ID)
	require.NoError(t, err)
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 8))
	require.NoError(t, err)
	assert.Equal(t, "1234567", string(content))
	assert.NoDirExists(t, filepath.Join(attachments.config.UploadDir, ".chunks", view.ID))
}

func TestAttachmentConfigHasSafeDefaultsAndRejectsInvalidValues(t *testing.T) {
	uploadDir := t.TempDir()
	t.Setenv("AIG_MAX_UPLOAD_BYTES", "")
	t.Setenv("AIG_MAX_CHUNK_BYTES", "")
	config, err := LoadAttachmentConfigFromEnv(uploadDir)
	require.NoError(t, err)
	assert.Greater(t, config.MaxFileBytes, int64(0))
	assert.Greater(t, config.MaxChunkBytes, int64(0))
	assert.LessOrEqual(t, config.MaxChunkBytes, config.MaxFileBytes)

	t.Setenv("AIG_MAX_UPLOAD_BYTES", "not-a-number")
	_, err = LoadAttachmentConfigFromEnv(uploadDir)
	require.Error(t, err)
	assert.NotContains(t, fmt.Sprint(err), uploadDir)
}

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

type readerCallback func([]byte) (int, error)

func (callback readerCallback) Read(buffer []byte) (int, error) { return callback(buffer) }

func (*recordingEngine) ValidateTaskReferences(context.Context, EngineTask) error { return nil }

func mcpRepositoryCreateInput(idempotencyKey string) CreateInput {
	return CreateInput{
		IdempotencyKey: idempotencyKey,
		TaskType:       "mcp_scan",
		Content:        "https://github.com/example/mcp-server.git",
		Params:         json.RawMessage(`{"source_kind":"repository"}`),
	}
}

type countingAttachmentRepository struct {
	*MemoryRepository
	reads atomic.Int64
}

type rejectingReferenceEngine struct{ recordingEngine }

func (*rejectingReferenceEngine) ValidateTaskReferences(context.Context, EngineTask) error {
	return ErrInvalid
}

type controlledReferenceEngine struct {
	recordingEngine
	referenceCalls atomic.Int64
	referenceErr   error
}

func (engine *controlledReferenceEngine) ValidateTaskReferences(context.Context, EngineTask) error {
	engine.referenceCalls.Add(1)
	return engine.referenceErr
}

type toggledTaskAuditRepository struct {
	*audit.MemoryRepository
	failErr error
}

func (repository *toggledTaskAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	if repository.failErr != nil {
		return repository.failErr
	}
	return repository.MemoryRepository.Append(ctx, event)
}

type gatedReferenceEngine struct {
	recordingEngine
	referenceCalls atomic.Int64
	firstEntered   chan struct{}
	releaseFirst   chan struct{}
	secondEntered  chan struct{}
	releaseSecond  chan struct{}
	secondErr      error
}

func (engine *gatedReferenceEngine) ValidateTaskReferences(context.Context, EngineTask) error {
	switch engine.referenceCalls.Add(1) {
	case 1:
		close(engine.firstEntered)
		<-engine.releaseFirst
		return nil
	case 2:
		close(engine.secondEntered)
		if engine.releaseSecond != nil {
			<-engine.releaseSecond
		}
		return engine.secondErr
	default:
		return errors.New("unexpected repeated reference validation")
	}
}

type createResult struct {
	view View
	err  error
}

func (repository *countingAttachmentRepository) GetAttachment(ctx context.Context, id string) (*Attachment, error) {
	repository.reads.Add(1)
	return repository.MemoryRepository.GetAttachment(ctx, id)
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

func (*leaseRaceEngine) ValidateTaskReferences(context.Context, EngineTask) error { return nil }

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

func (*recoveryTimeoutEngine) ValidateTaskReferences(context.Context, EngineTask) error { return nil }

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

func TestCreateAIInfraTargetRangeSucceeds(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	owner := identity.Subject{UserID: "target-range-owner", Username: "alice", Role: identity.RoleUser}

	created, err := service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "target-range", TaskType: "ai_infra_scan", Content: "192.168.10.2-192.168.10.10",
	})
	require.NoError(t, err)
	assert.Equal(t, "192.168.10.2-192.168.10.10", created.Content)
	assert.Equal(t, int64(1), engine.submits.Load())
}

func TestCreateAIInfraTargetValidationRejectsInvalidWildcardAndExpansionLimit(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "partial wildcard", content: "22.*.10.*"},
		{name: "ipv4 port range", content: "192.168.10.2:80-192.168.10.10:80"},
		{name: "invalid ipv4 port range", content: "192.168.10.2:99999-192.168.10.10:99999"},
		{name: "ipv6 range", content: "2001:db8::1-2001:db8::2"},
		{name: "bracketed ipv6 port range", content: "[2001:db8::1]:80-[2001:db8::2]:80"},
		{name: "single bracketed ipv6 port", content: "[2001:db8::1]:443"},
		{name: "unicode whitespace", content: "192.168.10.2\u00a0192.168.10.3"},
		{name: "whitespace separated urls", content: "https://a.example.test https://b.example.test"},
		{name: "too many expanded targets", content: "22.2.*.*\n1.1.1.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			owner := identity.Subject{UserID: "target-reject-owner", Username: "alice", Role: identity.RoleUser}

			_, err := service.Create(context.Background(), owner, CreateInput{
				IdempotencyKey: "target-reject-" + strings.ReplaceAll(test.name, " ", "-"),
				TaskType:       "ai_infra_scan",
				Content:        test.content,
			})
			require.ErrorIs(t, err, ErrInvalid)
			stored, listErr := repository.List(context.Background())
			require.NoError(t, listErr)
			assert.Empty(t, stored)
			assert.Zero(t, engine.submits.Load())
		})
	}
}

func TestCreateAIInfraTargetValidationPreservesHTTPURLSpecialCharacters(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	owner := identity.Subject{UserID: "target-url-owner", Username: "alice", Role: identity.RoleUser}
	content := "https://ai.example.com/~health\nhttps://ai.example.com/search?q=*"

	created, err := service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "target-url-special-characters", TaskType: "ai_infra_scan", Content: content,
	})
	require.NoError(t, err)
	assert.Equal(t, content, created.Content)
	assert.Equal(t, int64(1), engine.submits.Load())
}

func TestCreateAIInfraTargetAttachmentExpressionsCombineWithBody(t *testing.T) {
	repository := NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 2 << 20, MaxChunkBytes: 1 << 20,
	}, audits)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "target-attachment-owner", Username: "alice", Role: identity.RoleUser}
	attachment, err := attachments.Upload(context.Background(), owner, "targets.txt", strings.NewReader("192.168.10.4-192.168.10.5\n"))
	require.NoError(t, err)
	engine := &recordingEngine{}
	service := NewService(repository, engine, audits)
	service.SetAttachmentService(attachments)

	created, err := service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "target-attachment", TaskType: "ai_infra_scan", Content: "192.168.10.2-192.168.10.3",
		AttachmentIDs: []string{attachment.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, "192.168.10.2-192.168.10.3", created.Content, "audit and persisted content retain the raw user expression")
	assert.Equal(t, int64(1), engine.submits.Load())
}

func TestCreateAIInfraTargetValidationRejectsTooManyAttachmentExpressions(t *testing.T) {
	repository := NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 2 << 20, MaxChunkBytes: 1 << 20,
	}, audits)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "target-expression-limit-owner", Username: "alice", Role: identity.RoleUser}
	attachment, err := attachments.Upload(context.Background(), owner, "targets.txt", strings.NewReader(strings.Repeat("example.com\n", 65537)))
	require.NoError(t, err)
	engine := &recordingEngine{}
	service := NewService(repository, engine, audits)
	service.SetAttachmentService(attachments)

	_, err = service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "too-many-target-expressions", TaskType: "ai_infra_scan", Content: "192.168.10.1",
		AttachmentIDs: []string{attachment.ID},
	})
	require.ErrorIs(t, err, ErrInvalid)
	stored, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, stored)
	assert.Zero(t, engine.submits.Load())
}

func TestCreateAIInfraTargetAttachmentValidationRejectsUnsafeLists(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		attachment string
	}{
		{name: "invalid wildcard", content: "192.168.10.1", attachment: "22.*.10.*"},
		{name: "combined expansion limit", content: "22.2.*.*", attachment: "1.1.1.1"},
		{name: "oversized text list", content: "192.168.10.1", attachment: strings.Repeat("x", (1<<20)+1)},
		{name: "non utf8 text list", content: "192.168.10.1", attachment: string([]byte{0xff, 0xfe})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			audits := audit.NewService(audit.NewMemoryRepository())
			attachments, err := NewAttachmentService(repository, AttachmentConfig{
				UploadDir: t.TempDir(), MaxFileBytes: 2 << 20, MaxChunkBytes: 1 << 20,
			}, audits)
			require.NoError(t, err)
			owner := identity.Subject{UserID: "target-unsafe-owner", Username: "alice", Role: identity.RoleUser}
			attachment, err := attachments.Upload(context.Background(), owner, "targets.txt", strings.NewReader(test.attachment))
			require.NoError(t, err)
			engine := &recordingEngine{}
			service := NewService(repository, engine, audits)
			service.SetAttachmentService(attachments)

			_, err = service.Create(context.Background(), owner, CreateInput{
				IdempotencyKey: "target-unsafe-" + strings.ReplaceAll(test.name, " ", "-"),
				TaskType:       "ai_infra_scan",
				Content:        test.content,
				AttachmentIDs:  []string{attachment.ID},
			})
			require.ErrorIs(t, err, ErrInvalid)
			stored, listErr := repository.List(context.Background())
			require.NoError(t, listErr)
			assert.Empty(t, stored)
			assert.Zero(t, engine.submits.Load())
		})
	}
}

func TestCreateAIInfraTargetAttachmentValidationRejectsSymlink(t *testing.T) {
	repository := NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 2 << 20, MaxChunkBytes: 1 << 20,
	}, audits)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "target-symlink-owner", Username: "alice", Role: identity.RoleUser}
	attachment, err := attachments.Upload(context.Background(), owner, "targets.txt", strings.NewReader("192.168.10.2"))
	require.NoError(t, err)
	storedAttachment, err := repository.GetAttachment(context.Background(), attachment.ID)
	require.NoError(t, err)
	storagePath, err := attachments.storagePath(storedAttachment.StorageName)
	require.NoError(t, err)
	replacementPath := filepath.Join(t.TempDir(), "replacement.txt")
	require.NoError(t, os.WriteFile(replacementPath, []byte("192.168.10.3"), 0600))
	require.NoError(t, os.Remove(storagePath))
	if err := os.Symlink(replacementPath, storagePath); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	engine := &recordingEngine{}
	service := NewService(repository, engine, audits)
	service.SetAttachmentService(attachments)
	_, err = service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "target-symlink", TaskType: "ai_infra_scan", Content: "192.168.10.1",
		AttachmentIDs: []string{attachment.ID},
	})
	require.ErrorIs(t, err, ErrInvalid)
	storedTasks, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, storedTasks)
	assert.Zero(t, engine.submits.Load())
}

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
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("same-key")
	input.Params = json.RawMessage(`{"source_kind":"repository","model_id":"model-1"}`)

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
	events, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: taskID, Action: audit.Action("task.created")})
	require.NoError(t, err)
	assert.Len(t, events, 2, "并发同键创建只能保留一组 pending/success 审计")
}

func TestIdempotentCreateReturnsPersistedTaskWhenLiveReferencesBecomeUnavailable(t *testing.T) {
	for name, unavailableErr := range map[string]error{
		"引用已删除":     ErrInvalid,
		"引用服务暂时不可用": errors.New("reference registry unavailable"),
	} {
		t.Run(name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &controlledReferenceEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
			input := mcpRepositoryCreateInput("stable-retry")
			input.Params = json.RawMessage(`{"source_kind":"repository","model_id":"model-1"}`)

			created, err := service.Create(context.Background(), subject, input)
			require.NoError(t, err)
			engine.referenceErr = unavailableErr

			retried, err := service.Create(context.Background(), subject, input)
			require.NoError(t, err)
			assert.Equal(t, created.ID, retried.ID)
			assert.Equal(t, int64(1), engine.referenceCalls.Load(), "已持久化的同载荷重试不得再次读取实时引用")
			assert.Equal(t, int64(1), engine.submits.Load(), "幂等重试不得重复分发任务")
		})
	}
}

func TestLegacyAIInfrastructureTaskRetriesNormalizePortScanMode(t *testing.T) {
	tests := []struct {
		name           string
		legacyParams   json.RawMessage
		omittedParams  json.RawMessage
		explicitParams json.RawMessage
	}{
		{
			name:           "model and timeout",
			legacyParams:   json.RawMessage(`{"model_id":"model-1","timeout":30}`),
			omittedParams:  json.RawMessage(`{"model_id":"model-1","timeout":30}`),
			explicitParams: json.RawMessage(`{"model_id":"model-1","timeout":30,"port_scan_mode":"fixed_ai"}`),
		},
		{
			name:           "empty params",
			legacyParams:   json.RawMessage(`{}`),
			omittedParams:  nil,
			explicitParams: json.RawMessage(`{"port_scan_mode":"fixed_ai"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			subject := identity.Subject{UserID: "legacy-owner", Username: "alice", Role: identity.RoleUser}
			idempotencyKey := "legacy-idempotency-" + strings.ReplaceAll(test.name, " ", "-")
			taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+idempotencyKey)).String()
			now := time.Now().UTC()
			_, created, err := repository.CreateOrGet(context.Background(), &Task{
				ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
				IdempotencyKey: idempotencyKey, EngineSessionID: taskID, TaskType: "ai_infra_scan",
				Content: "127.0.0.1", Params: append(json.RawMessage(nil), test.legacyParams...), AttachmentRefs: json.RawMessage(`[]`),
				Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
			})
			require.NoError(t, err)
			require.True(t, created)

			for _, params := range []json.RawMessage{test.omittedParams, test.explicitParams} {
				view, createErr := service.Create(context.Background(), subject, CreateInput{
					IdempotencyKey: idempotencyKey, TaskType: "ai_infra_scan", Content: "127.0.0.1", Params: params,
				})
				require.NoError(t, createErr)
				assert.Equal(t, taskID, view.ID)
			}

			stored, err := repository.Get(context.Background(), taskID)
			require.NoError(t, err)
			assert.JSONEq(t, string(test.legacyParams), string(stored.Params), "legacy task parameters must not be rewritten")
			assert.Zero(t, engine.submits.Load())
		})
	}
}

func TestLegacyMCPTaskRetryReturnsEquivalentPersistedTaskWithoutMutation(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &controlledReferenceEngine{}
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "legacy-mcp-owner", Username: "alice", Role: identity.RoleUser}
	idempotencyKey := "legacy-mcp-idempotency"
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+idempotencyKey)).String()
	now := time.Now().UTC()
	legacy := &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: idempotencyKey, EngineSessionID: taskID, TaskType: "mcp_scan",
		Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
		Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	_, created, err := repository.CreateOrGet(context.Background(), legacy)
	require.NoError(t, err)
	require.True(t, created)

	view, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: idempotencyKey, TaskType: "mcp_scan", Content: legacy.Content, Params: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	assert.Equal(t, taskID, view.ID)
	assert.Equal(t, StatusRunning, view.Status)
	assert.Zero(t, engine.referenceCalls.Load(), "legacy idempotent retry must not revalidate live references")
	assert.Zero(t, engine.submits.Load(), "legacy idempotent retry must not dispatch again")

	stored, err := repository.Get(context.Background(), taskID)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(stored.Params), "legacy params must not be inferred or rewritten")
	assert.Equal(t, now, stored.CreatedAt)
	assert.Equal(t, now, stored.UpdatedAt)
	tasks, err := repository.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, tasks, 1, "legacy retry must not persist another task")
	events, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: taskID})
	require.NoError(t, err)
	assert.Empty(t, events, "legacy retry must not append creation audit records")
}

func TestLegacyMCPTaskRetryDoesNotBypassValidationOrConflictSemantics(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &controlledReferenceEngine{}
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "legacy-mcp-owner", Username: "alice", Role: identity.RoleUser}
	idempotencyKey := "legacy-mcp-conflict"
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+idempotencyKey)).String()
	now := time.Now().UTC()
	legacy := &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: idempotencyKey, EngineSessionID: taskID, TaskType: "mcp_scan",
		Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
		Status: StatusRunning, CreatedAt: now, UpdatedAt: now,
	}
	_, created, err := repository.CreateOrGet(context.Background(), legacy)
	require.NoError(t, err)
	require.True(t, created)

	_, err = service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: idempotencyKey, TaskType: "mcp_scan", Content: legacy.Content,
		Params: json.RawMessage(`{"source_kind":"repository"}`),
	})
	require.ErrorIs(t, err, ErrInvalid, "a changed modern payload must use the normal idempotency conflict path")

	otherSubject := identity.Subject{UserID: "other-owner", Username: "bob", Role: identity.RoleUser}
	_, err = service.Create(context.Background(), otherSubject, CreateInput{
		IdempotencyKey: idempotencyKey, TaskType: "mcp_scan", Content: legacy.Content, Params: json.RawMessage(`{}`),
	})
	require.ErrorIs(t, err, ErrInvalid, "a legacy task owned by someone else must not bypass new-request validation")

	stored, err := repository.Get(context.Background(), taskID)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(stored.Params))
	assert.Equal(t, now, stored.UpdatedAt)
	tasks, err := repository.List(context.Background())
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Zero(t, engine.referenceCalls.Load())
	assert.Zero(t, engine.submits.Load())
	events, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: taskID})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestLegacyAIInfrastructureTaskDispatchNormalizesPortScanMode(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "legacy-dispatch-owner", Username: "alice", Role: identity.RoleUser}
	idempotencyKey := "legacy-dispatch"
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+idempotencyKey)).String()
	legacyParams := json.RawMessage(`{"model_id":"model-1","timeout":30}`)
	now := time.Now().UTC()
	_, created, err := repository.CreateOrGet(context.Background(), &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: idempotencyKey, EngineSessionID: taskID, TaskType: "ai_infra_scan",
		Content: "127.0.0.1", Params: legacyParams, AttachmentRefs: json.RawMessage(`[]`),
		Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, created)

	view, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: idempotencyKey, TaskType: "ai_infra_scan", Content: "127.0.0.1", Params: legacyParams,
	})
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, view.Status)
	assert.Equal(t, int64(1), engine.submits.Load())

	engine.mu.Lock()
	dispatchedParams := append(json.RawMessage(nil), engine.last.Params...)
	engine.mu.Unlock()
	var dispatched map[string]any
	require.NoError(t, json.Unmarshal(dispatchedParams, &dispatched))
	assert.Equal(t, "fixed_ai", dispatched["port_scan_mode"])
	assert.Equal(t, "model-1", dispatched["model_id"])
	assert.Equal(t, float64(30), dispatched["timeout"])

	stored, err := repository.Get(context.Background(), taskID)
	require.NoError(t, err)
	assert.JSONEq(t, string(legacyParams), string(stored.Params), "dispatch must not rewrite legacy task parameters")
}

func TestMalformedLegacyAIInfrastructureTaskDoesNotDispatch(t *testing.T) {
	for name, params := range map[string]json.RawMessage{
		"unknown mode":  json.RawMessage(`{"port_scan_mode":"not-approved"}`),
		"explicit null": json.RawMessage(`{"port_scan_mode":null}`),
		"empty string":  json.RawMessage(`{"port_scan_mode":""}`),
	} {
		t.Run(name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			now := time.Now().UTC()
			taskID := "malformed-legacy-ai-" + strings.ReplaceAll(name, " ", "-")
			task := &Task{
				ID: taskID, OwnerUserID: "legacy-owner", OwnerUsername: "alice",
				IdempotencyKey: taskID, EngineSessionID: taskID, TaskType: "ai_infra_scan",
				Content: "127.0.0.1", Params: params, AttachmentRefs: json.RawMessage(`[]`),
				Status: StatusPending, CreatedAt: now, UpdatedAt: now,
			}
			_, created, err := repository.CreateOrGet(context.Background(), task)
			require.NoError(t, err)
			require.True(t, created)
			claim, claimed, err := repository.ClaimDispatch(context.Background(), task.ID, now, now.Add(dispatchLeaseDuration))
			require.NoError(t, err)
			require.True(t, claimed)

			_, err = service.dispatch(context.Background(), identity.Subject{UserID: task.OwnerUserID, Username: task.OwnerUsername, Role: identity.RoleUser}, task, claim)
			require.ErrorIs(t, err, ErrInvalid)
			assert.Zero(t, engine.submits.Load())
		})
	}
}

func TestIdempotentRetryBypassesCreationAuditOutage(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	auditRepository := &toggledTaskAuditRepository{MemoryRepository: audit.NewMemoryRepository()}
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "audit-retry-owner", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("audit-retry")

	created, err := service.Create(context.Background(), subject, input)
	require.NoError(t, err)
	eventsBefore, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: created.ID, Action: audit.Action("task.created")})
	require.NoError(t, err)
	require.Len(t, eventsBefore, 2)
	auditRepository.failErr = errors.New("audit append unavailable")

	retried, err := service.Create(context.Background(), subject, input)
	require.NoError(t, err)
	assert.Equal(t, created.ID, retried.ID)
	assert.Equal(t, int64(1), engine.submits.Load(), "已有任务重放不得重复提交")
	eventsAfter, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: created.ID, Action: audit.Action("task.created")})
	require.NoError(t, err)
	assert.Len(t, eventsAfter, len(eventsBefore), "已有任务重放不得追加创建审计")
}

func TestExistingPendingTaskRecoversDuringCreationAuditOutage(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	auditRepository := &toggledTaskAuditRepository{
		MemoryRepository: audit.NewMemoryRepository(), failErr: errors.New("audit append unavailable"),
	}
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "audit-recovery-owner", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("audit-recovery")
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+input.IdempotencyKey)).String()
	now := time.Now().UTC()
	_, created, err := repository.CreateOrGet(context.Background(), &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: input.IdempotencyKey, EngineSessionID: taskID, TaskType: input.TaskType,
		Content: input.Content, Params: append(json.RawMessage(nil), input.Params...), AttachmentRefs: json.RawMessage(`[]`),
		Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, created)

	view, err := service.Create(context.Background(), subject, input)
	require.NoError(t, err)
	assert.Equal(t, taskID, view.ID)
	assert.Equal(t, StatusRunning, view.Status)
	assert.Equal(t, int64(1), engine.submits.Load(), "已有pending任务仍应恢复其一次分发")
}

func TestIdempotentCreateRejectsDifferentPersistedPayloadBeforeSideEffects(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &controlledReferenceEngine{}
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("payload-mismatch")
	input.Params = json.RawMessage(`{"source_kind":"repository","model_id":"model-1"}`)

	created, err := service.Create(context.Background(), subject, input)
	require.NoError(t, err)
	eventsBefore, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: created.ID})
	require.NoError(t, err)
	changed := input
	changed.Content = "https://github.com/example/other-mcp-server.git"

	_, err = service.Create(context.Background(), subject, changed)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, int64(1), engine.referenceCalls.Load(), "载荷冲突不得访问实时引用")
	assert.Equal(t, int64(1), engine.submits.Load(), "载荷冲突不得分发任务")
	stored, err := repository.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, input.Content, stored.Content)
	eventsAfter, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: created.ID})
	require.NoError(t, err)
	assert.Len(t, eventsAfter, len(eventsBefore), "载荷冲突不得创建审计变更")
}

func TestConcurrentIdempotentRetrySerializesBeforeLiveReferenceValidation(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &gatedReferenceEngine{
		firstEntered: make(chan struct{}), releaseFirst: make(chan struct{}),
		secondEntered: make(chan struct{}), secondErr: errors.New("reference registry unavailable"),
	}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("concurrent-stable")
	firstDone := make(chan createResult, 1)
	go func() {
		view, err := service.Create(context.Background(), subject, input)
		firstDone <- createResult{view: view, err: err}
	}()
	select {
	case <-engine.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first create did not enter reference validation")
	}
	secondStarted := make(chan struct{})
	secondDone := make(chan createResult, 1)
	go func() {
		close(secondStarted)
		view, err := service.Create(context.Background(), subject, input)
		secondDone <- createResult{view: view, err: err}
	}()
	<-secondStarted
	secondValidated := false
	select {
	case <-engine.secondEntered:
		secondValidated = true
	case <-time.After(100 * time.Millisecond):
	}
	close(engine.releaseFirst)
	first := <-firstDone
	second := <-secondDone

	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.False(t, secondValidated, "同键请求必须在读取与实时引用校验前串行")
	assert.Equal(t, first.view.ID, second.view.ID)
	assert.Equal(t, int64(1), engine.referenceCalls.Load())
	assert.Equal(t, int64(1), engine.submits.Load())
}

func TestConcurrentIdempotencyConflictHasNoValidatorAuditOrSubmitSideEffects(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &gatedReferenceEngine{
		firstEntered: make(chan struct{}), releaseFirst: make(chan struct{}),
		secondEntered: make(chan struct{}), releaseSecond: make(chan struct{}),
	}
	auditRepository := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(auditRepository))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("concurrent-conflict")
	firstDone := make(chan createResult, 1)
	go func() {
		view, err := service.Create(context.Background(), subject, input)
		firstDone <- createResult{view: view, err: err}
	}()
	select {
	case <-engine.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first create did not enter reference validation")
	}
	changed := input
	changed.Content = "https://github.com/example/other-mcp-server.git"
	secondStarted := make(chan struct{})
	secondDone := make(chan createResult, 1)
	go func() {
		close(secondStarted)
		view, err := service.Create(context.Background(), subject, changed)
		secondDone <- createResult{view: view, err: err}
	}()
	<-secondStarted
	select {
	case <-engine.secondEntered:
	case <-time.After(100 * time.Millisecond):
	}
	close(engine.releaseFirst)
	first := <-firstDone
	require.NoError(t, first.err)
	eventsBefore, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: first.view.ID})
	require.NoError(t, err)
	close(engine.releaseSecond)
	second := <-secondDone

	require.ErrorIs(t, second.err, ErrInvalid)
	assert.Equal(t, int64(1), engine.referenceCalls.Load(), "冲突请求不得访问实时引用")
	assert.Equal(t, int64(1), engine.submits.Load(), "冲突请求不得分发")
	eventsAfter, err := auditRepository.List(context.Background(), audit.Filter{ResourceID: first.view.ID})
	require.NoError(t, err)
	assert.Len(t, eventsAfter, len(eventsBefore), "冲突请求不得创建审计变更")
}

func TestMemoryCreateKeyLockNeverAppliesAfterContextCancellation(t *testing.T) {
	repository := NewMemoryRepository()
	var applied atomic.Int64
	for index := range 256 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := repository.WithinCreateKeyLock(ctx, "cancelled-owner", fmt.Sprintf("cancelled-%d", index), func(context.Context) error {
			applied.Add(1)
			return nil
		})
		if err != nil {
			require.ErrorIs(t, err, context.Canceled)
		}
	}
	assert.Zero(t, applied.Load(), "已取消请求不得进入任务创建临界区")
	repository.createLocksMu.Lock()
	defer repository.createLocksMu.Unlock()
	assert.Empty(t, repository.createLocks, "取消后不得遗留键锁引用")
}

func TestCreateUsesExactPerTaskParameterSchemas(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	for name, params := range map[string]string{
		"top-level token":    `{"source_kind":"repository","token":"plain-secret"}`,
		"nested api key":     `{"source_kind":"repository","provider":{"api_key":"plain-secret"}}`,
		"legacy model":       `{"source_kind":"repository","model":{"token":"plain-secret","base_url":"https://model.invalid"}}`,
		"access token alias": `{"source_kind":"repository","access_token":"plain-secret"}`,
		"nested credentials": `{"source_kind":"repository","metadata":{"credentials":{"value":"plain-secret"}}}`,
		"wrong type field":   `{"source_kind":"repository","thread":"4"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "secret-" + name, TaskType: "mcp_scan", Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(params),
			})
			require.ErrorIs(t, err, ErrInvalid)
		})
	}
	assert.Zero(t, engine.submits.Load())

	valid := []CreateInput{
		{IdempotencyKey: "valid-mcp", TaskType: "mcp_scan", Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(`{"source_kind":"repository","model_id":"model-1","thread":4}`)},
		{IdempotencyKey: "valid-infra", TaskType: "ai_infra_scan", Content: "target", Params: json.RawMessage(`{"model_id":"model-1","timeout":30}`)},
		{IdempotencyKey: "valid-redteam", TaskType: "model_redteam_report", Content: "prompt", Params: json.RawMessage(`{"model_id":["model-1"],"eval_model_id":"model-2","dataset":{"numPrompts":10,"randomSeed":7,"promptColumn":"prompt"},"techniques":["BASE64"]}`)},
		{IdempotencyKey: "valid-agent", TaskType: "agent_scan", Content: "scan", Params: json.RawMessage(`{"agent_id":"agent-1","eval_model_id":"model-2"}`)},
	}
	for _, input := range valid {
		_, err := service.Create(context.Background(), subject, input)
		require.NoError(t, err, input.TaskType)
	}
	assert.Equal(t, int64(len(valid)), engine.submits.Load())
}

func TestCreateMCPRejectsMissingSourceKindBeforeMutation(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	audits := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audits))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	_, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "mcp-missing-source-kind",
		TaskType:       "mcp_scan",
		Content:        "https://github.com/example/mcp-server.git",
		Params:         json.RawMessage(`{"model_id":"model-1","thread":4}`),
	})

	require.ErrorIs(t, err, ErrInvalid)
	tasks, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.submits.Load())
	events, listErr := audits.List(context.Background(), audit.Filter{Action: audit.Action("task.created")})
	require.NoError(t, listErr)
	assert.Empty(t, events)
}

func TestCreateMCPRejectsUnknownSourceKindBeforeMutation(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	audits := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audits))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	_, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "mcp-unknown-source-kind",
		TaskType:       "mcp_scan",
		Content:        "https://github.com/example/mcp-server.git",
		Params:         json.RawMessage(`{"source_kind":"filesystem","model_id":"model-1","thread":4}`),
	})

	require.ErrorIs(t, err, ErrInvalid)
	tasks, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.submits.Load())
	events, listErr := audits.List(context.Background(), audit.Filter{Action: audit.Action("task.created")})
	require.NoError(t, listErr)
	assert.Empty(t, events)
}

func TestMCPRepositoryReferenceSourceSyntax(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "https://github.com/example/mcp-server.git", valid: true},
		{value: "http://git.example.test/group/mcp-server", valid: true},
		{value: "ssh://git@git.example.test:2222/group/mcp-server.git", valid: true},
		{value: "git@git.example.test:group/mcp-server.git", valid: true},
		{value: "https://git.example.test/", valid: false},
		{value: "https://alice:secret@git.example.test/group/mcp-server", valid: false},
		{value: "ssh://alice@git.example.test/group/mcp-server", valid: false},
		{value: "ssh://git:secret@git.example.test/group/mcp-server", valid: false},
		{value: "https://git.example.test/group/mcp-server?token=secret", valid: false},
		{value: "https://git.example.test/group/mcp-server#private", valid: false},
		{value: "git@@git.example.test:group/mcp-server", valid: false},
		{value: "git@git.example.test:", valid: false},
	} {
		t.Run(test.value, func(t *testing.T) {
			assert.Equal(t, test.valid, validMCPRepositoryReference(test.value))
		})
	}
}

func TestCreateMCPRepositorySourceAllowsReadyAttachmentWithEmptyContent(t *testing.T) {
	repository := NewMemoryRepository()
	auditService := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, auditService)
	require.NoError(t, err)
	service := NewService(repository, &recordingEngine{}, auditService)
	service.SetAttachmentService(attachments)
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	attachment, err := attachments.Upload(context.Background(), subject, "unclassified-upload", strings.NewReader("code"))
	require.NoError(t, err)

	created, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "repository-ready-attachment",
		TaskType:       "mcp_scan",
		Content:        "",
		Params:         json.RawMessage(`{"source_kind":"repository"}`),
		AttachmentIDs:  []string{attachment.ID},
	})

	require.NoError(t, err)
	stored, err := repository.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.JSONEq(t, fmt.Sprintf("[%q]", attachment.ID), string(stored.AttachmentRefs))
}

func TestCreateMCPRepositorySourceRejectsUngovernedContentWithAttachment(t *testing.T) {
	repository := NewMemoryRepository()
	auditService := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, auditService)
	require.NoError(t, err)
	engine := &recordingEngine{}
	service := NewService(repository, engine, auditService)
	service.SetAttachmentService(attachments)
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	attachment, err := attachments.Upload(context.Background(), subject, "unclassified-upload", strings.NewReader("code"))
	require.NoError(t, err)

	_, err = service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "repository-attachment-ungoverned-content",
		TaskType:       "mcp_scan",
		Content:        "https://user:credential@git.example.test/group/mcp-server",
		Params:         json.RawMessage(`{"source_kind":"repository"}`),
		AttachmentIDs:  []string{attachment.ID},
	})

	require.ErrorIs(t, err, ErrInvalid)
	tasks, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.submits.Load())
}

func TestCreateMCPRepositorySourceRejectsReferenceAndAttachmentTogether(t *testing.T) {
	repository := NewMemoryRepository()
	auditService := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, auditService)
	require.NoError(t, err)
	engine := &recordingEngine{}
	service := NewService(repository, engine, auditService)
	service.SetAttachmentService(attachments)
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	attachment, err := attachments.Upload(context.Background(), subject, "unclassified-upload", strings.NewReader("code"))
	require.NoError(t, err)

	_, err = service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "repository-reference-and-attachment",
		TaskType:       "mcp_scan",
		Content:        "https://github.com/example/mcp-server.git",
		Params:         json.RawMessage(`{"source_kind":"repository"}`),
		AttachmentIDs:  []string{attachment.ID},
	})

	require.ErrorIs(t, err, ErrInvalid)
	tasks, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.submits.Load())
}

func TestCreateMCPRepositorySourceValidation(t *testing.T) {
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	for _, test := range []struct {
		name    string
		content string
		wantErr bool
	}{
		{name: "https Git URL", content: "https://github.com/example/mcp-server.git"},
		{name: "http Git URL without suffix", content: "http://git.example.test/group/mcp-server"},
		{name: "ssh Git URL", content: "ssh://git@git.example.test:2222/group/mcp-server.git"},
		{name: "SCP Git reference", content: "git@git.example.test:group/mcp-server.git"},
		{name: "root URL", content: "https://git.example.test/", wantErr: true},
		{name: "HTTP credentials", content: "https://alice:secret@git.example.test/group/mcp-server", wantErr: true},
		{name: "SSH non Git user", content: "ssh://alice@git.example.test/group/mcp-server", wantErr: true},
		{name: "SSH password", content: "ssh://git:secret@git.example.test/group/mcp-server", wantErr: true},
		{name: "query", content: "https://git.example.test/group/mcp-server?token=secret", wantErr: true},
		{name: "fragment", content: "https://git.example.test/group/mcp-server#private", wantErr: true},
		{name: "not a Git reference", content: "scan this project", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))

			_, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "repository-source-" + strings.ReplaceAll(test.name, " ", "-"),
				TaskType:       "mcp_scan",
				Content:        test.content,
				Params:         json.RawMessage(`{"source_kind":"repository","model_id":"model-1","thread":4}`),
			})

			if test.wantErr {
				require.ErrorIs(t, err, ErrInvalid)
				tasks, listErr := repository.List(context.Background())
				require.NoError(t, listErr)
				assert.Empty(t, tasks)
				assert.Zero(t, engine.submits.Load())
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int64(1), engine.submits.Load())
		})
	}
}

func TestCreateMCPRepositorySourceRejectsAuthorizationConfirmation(t *testing.T) {
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	for _, value := range []string{"true", "false", "null"} {
		t.Run(value, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))

			_, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "repository-authorization-" + value,
				TaskType:       "mcp_scan",
				Content:        "https://github.com/example/mcp-server.git",
				Params:         json.RawMessage(`{"source_kind":"repository","authorization_confirmed":` + value + `}`),
			})

			require.ErrorIs(t, err, ErrInvalid)
			tasks, listErr := repository.List(context.Background())
			require.NoError(t, listErr)
			assert.Empty(t, tasks)
			assert.Zero(t, engine.submits.Load())
		})
	}
}

func TestCreateMCPServiceSourceRequiresEndpointAndAuthorization(t *testing.T) {
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	for _, test := range []struct {
		name          string
		content       string
		params        string
		attachmentIDs []string
		wantErr       bool
	}{
		{name: "valid service", content: "https://mcp.example.test/rpc", params: `{"source_kind":"service","authorization_confirmed":true,"model_id":"model-1","thread":2}`},
		{name: "missing confirmation", content: "https://mcp.example.test/rpc", params: `{"source_kind":"service"}`, wantErr: true},
		{name: "false confirmation", content: "https://mcp.example.test/rpc", params: `{"source_kind":"service","authorization_confirmed":false}`, wantErr: true},
		{name: "invalid endpoint", content: "git@git.example.test:group/mcp-server", params: `{"source_kind":"service","authorization_confirmed":true}`, wantErr: true},
		{name: "userinfo is forbidden", content: "https://user:secret@mcp.example.test/sse", params: `{"source_kind":"service","authorization_confirmed":true}`, wantErr: true},
		{name: "credential query is forbidden", content: "https://mcp.example.test/sse?token=secret", params: `{"source_kind":"service","authorization_confirmed":true}`, wantErr: true},
		{name: "query is forbidden", content: "https://mcp.example.test/sse?version=1", params: `{"source_kind":"service","authorization_confirmed":true}`, wantErr: true},
		{name: "force query is forbidden", content: "https://mcp.example.test/sse?", params: `{"source_kind":"service","authorization_confirmed":true}`, wantErr: true},
		{name: "fragment is forbidden", content: "https://mcp.example.test/sse#private", params: `{"source_kind":"service","authorization_confirmed":true}`, wantErr: true},
		{name: "attachments are forbidden", content: "https://mcp.example.test/rpc", params: `{"source_kind":"service","authorization_confirmed":true}`, attachmentIDs: []string{"attachment-1"}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			auditRepository := audit.NewMemoryRepository()
			service := NewService(repository, engine, audit.NewService(auditRepository))

			_, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "service-source-" + strings.ReplaceAll(test.name, " ", "-"),
				TaskType:       "mcp_scan",
				Content:        test.content,
				Params:         json.RawMessage(test.params),
				AttachmentIDs:  test.attachmentIDs,
			})

			if test.wantErr {
				require.ErrorIs(t, err, ErrInvalid)
				tasks, listErr := repository.List(context.Background())
				require.NoError(t, listErr)
				assert.Empty(t, tasks)
				assert.Zero(t, engine.submits.Load())
				events, listErr := auditRepository.List(context.Background(), audit.Filter{Action: audit.Action("task.created")})
				require.NoError(t, listErr)
				assert.Empty(t, events)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int64(1), engine.submits.Load())
		})
	}
}

func TestCreateMCPSourceNormalizesSafeParams(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	created, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "normalized-service-source",
		TaskType:       "mcp_scan",
		Content:        "https://mcp.example.test/rpc",
		Params:         json.RawMessage(`{"thread":4,"source_kind":"service","authorization_confirmed":true,"model_id":"model-1"}`),
	})
	require.NoError(t, err)
	const expected = `{"source_kind":"service","model_id":"model-1","thread":4,"authorization_confirmed":true}`
	assert.Equal(t, expected, string(created.Params))

	stored, err := repository.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, expected, string(stored.Params))
	engine.mu.Lock()
	dispatched := append(json.RawMessage(nil), engine.last.Params...)
	engine.mu.Unlock()
	assert.Equal(t, expected, string(dispatched))

	for name, params := range map[string]string{
		"unknown field":       `{"source_kind":"service","authorization_confirmed":true,"unexpected":"drop-me"}`,
		"nested credentials":  `{"source_kind":"service","authorization_confirmed":true,"provider":{"api_key":"secret"}}`,
		"nested confirmation": `{"source_kind":"service","authorization_confirmed":{"value":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, createErr := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "invalid-normalized-" + strings.ReplaceAll(name, " ", "-"),
				TaskType:       "mcp_scan",
				Content:        "https://mcp.example.test/rpc",
				Params:         json.RawMessage(params),
			})
			require.ErrorIs(t, createErr, ErrInvalid)
		})
	}
}

func TestCreateMCPSourceAuditMetadataIsSafeAndLinked(t *testing.T) {
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	for _, test := range []struct {
		name              string
		content           string
		params            string
		wantSourceKind    string
		wantAuthorization bool
	}{
		{name: "repository", content: "https://github.com/example/mcp-server.git", params: `{"source_kind":"repository","model_id":"private-model"}`, wantSourceKind: "repository"},
		{name: "service", content: "https://private-mcp.example.test/private-sse", params: `{"source_kind":"service","authorization_confirmed":true,"model_id":"private-model"}`, wantSourceKind: "service", wantAuthorization: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			auditRepository := audit.NewMemoryRepository()
			service := NewService(repository, &recordingEngine{}, audit.NewService(auditRepository))

			created, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "mcp-source-audit-" + test.name,
				TaskType:       "mcp_scan",
				Content:        test.content,
				Params:         json.RawMessage(test.params),
			})
			require.NoError(t, err)

			events, listErr := auditRepository.List(context.Background(), audit.Filter{Action: audit.Action("task.created"), ResourceID: created.ID})
			require.NoError(t, listErr)
			require.Len(t, events, 2)
			for _, event := range events {
				assert.Equal(t, subject.UserID, event.ActorUserID)
				assert.Equal(t, subject.Username, event.ActorUsername)
				assert.Equal(t, created.ID, event.ResourceID)
				assert.False(t, event.OccurredAt.IsZero())
				metadata := map[string]any{}
				require.NoError(t, json.Unmarshal(event.Metadata, &metadata))
				assert.ElementsMatch(t, []string{"source_kind", "authorization_confirmed", "phase"}, mapKeys(metadata))
				assert.Equal(t, test.wantSourceKind, metadata["source_kind"])
				assert.Equal(t, test.wantAuthorization, metadata["authorization_confirmed"])
				serialized, marshalErr := json.Marshal(metadata)
				require.NoError(t, marshalErr)
				for _, secret := range []string{"private-mcp.example.test", "private-sse", "mcp-server.git", "private-model"} {
					assert.NotContains(t, string(serialized), secret)
				}
			}
		})
	}
}

func TestCreateAIInfrastructureNormalizesPortScanMode(t *testing.T) {
	tests := []struct {
		name   string
		params string
		want   string
	}{
		{name: "omitted defaults to fixed AI", params: `{"model_id":"model-1","timeout":30}`, want: "fixed_ai"},
		{name: "full TCP is preserved", params: `{"model_id":"model-1","timeout":30,"port_scan_mode":"full_tcp"}`, want: "full_tcp"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := NewMemoryRepository()
			engine := &recordingEngine{}
			service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
			subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

			created, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "port-scan-" + test.want, TaskType: "ai_infra_scan", Content: "127.0.0.1",
				Params: json.RawMessage(test.params),
			})
			require.NoError(t, err)

			var viewParams map[string]any
			require.NoError(t, json.Unmarshal(created.Params, &viewParams))
			assert.Equal(t, test.want, viewParams["port_scan_mode"])
			assert.Equal(t, "model-1", viewParams["model_id"])
			assert.Equal(t, float64(30), viewParams["timeout"])

			stored, err := repository.Get(context.Background(), created.ID)
			require.NoError(t, err)
			var storedParams map[string]any
			require.NoError(t, json.Unmarshal(stored.Params, &storedParams))
			assert.Equal(t, test.want, storedParams["port_scan_mode"])

			engine.mu.Lock()
			dispatchedParams := append(json.RawMessage(nil), engine.last.Params...)
			engine.mu.Unlock()
			var dispatched map[string]any
			require.NoError(t, json.Unmarshal(dispatchedParams, &dispatched))
			assert.Equal(t, test.want, dispatched["port_scan_mode"])

			detail, err := service.BrowserGet(context.Background(), subject, created.ID)
			require.NoError(t, err)
			assert.Equal(t, test.want, detail.InputSummary.PortScanMode)
		})
	}
}

func TestCreateAIInfrastructureRejectsInvalidPortScanModeBeforeMutation(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &controlledReferenceEngine{}
	audits := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audits))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	for name, params := range map[string]string{
		"unknown string":        `{"port_scan_mode":"full"}`,
		"value case variant":    `{"port_scan_mode":"FULL_TCP"}`,
		"uppercase field name":  `{"PORT_SCAN_MODE":"full_tcp"}`,
		"mixed case field name": `{"PoRt_ScAn_MoDe":"full_tcp"}`,
		"explicit null":         `{"port_scan_mode":null}`,
		"empty string":          `{"port_scan_mode":""}`,
		"whitespace string":     `{"port_scan_mode":" "}`,
		"array":                 `{"port_scan_mode":["fixed_ai"]}`,
		"number":                `{"port_scan_mode":1}`,
		"unknown field":         `{"port_scan_mode":"fixed_ai","unexpected":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Create(context.Background(), subject, CreateInput{
				IdempotencyKey: "invalid-port-scan-" + strings.ReplaceAll(name, " ", "-"),
				TaskType:       "ai_infra_scan",
				Content:        "127.0.0.1",
				Params:         json.RawMessage(params),
			})
			require.ErrorIs(t, err, ErrInvalid)
		})
	}

	tasks, err := repository.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.referenceCalls.Load())
	assert.Zero(t, engine.submits.Load())
	events, err := audits.List(context.Background(), audit.Filter{Action: audit.Action("task.created")})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestCreateRejectsCaseVariantTaskParameterFieldNames(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	for _, test := range []CreateInput{
		{IdempotencyKey: "case-mcp", TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{"MODEL_ID":"model-1","thread":4}`)},
		{IdempotencyKey: "case-redteam", TaskType: "model_redteam_report", Content: "scan", Params: json.RawMessage(`{"MODEL_ID":["model-1"],"eval_model_id":"model-2"}`)},
		{IdempotencyKey: "case-agent", TaskType: "agent_scan", Content: "scan", Params: json.RawMessage(`{"AGENT_ID":"agent-1","eval_model_id":"model-2"}`)},
	} {
		_, err := service.Create(context.Background(), subject, test)
		require.ErrorIs(t, err, ErrInvalid, test.TaskType)
	}

	tasks, err := repository.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.submits.Load())
}

func TestCreateAIInfrastructureTreatsOmittedAndExplicitFixedPortScanModeAsSameRequest(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	created, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "same-port-scan-mode", TaskType: "ai_infra_scan", Content: "127.0.0.1",
		Params: json.RawMessage(`{"model_id":"model-1","timeout":30}`),
	})
	require.NoError(t, err)
	retried, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "same-port-scan-mode", TaskType: "ai_infra_scan", Content: "127.0.0.1",
		Params: json.RawMessage(`{"model_id":"model-1","timeout":30,"port_scan_mode":"fixed_ai"}`),
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, retried.ID)
	assert.Equal(t, int64(1), engine.submits.Load())

	var params map[string]any
	require.NoError(t, json.Unmarshal(retried.Params, &params))
	assert.Equal(t, "fixed_ai", params["port_scan_mode"])
}

func TestTaskInputSummaryOnlyProjectsNormalizedInfrastructurePortScanMode(t *testing.T) {
	trusted := taskDetailOf(&Task{
		TaskType: "ai_infra_scan", Content: "127.0.0.1",
		Params: json.RawMessage(`{"model_id":"model-1","timeout":30,"port_scan_mode":"full_tcp"}`),
	})
	assert.Equal(t, "full_tcp", trusted.InputSummary.PortScanMode)

	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"port_scan_mode":"FULL_TCP"}`),
		json.RawMessage(`{"port_scan_mode":"fixed_ai","unexpected":"do-not-project"}`),
		json.RawMessage(`{"port_scan_mode":""}`),
		json.RawMessage(`{}`),
	} {
		detail := taskDetailOf(&Task{TaskType: "ai_infra_scan", Content: "https://secret.example.com", Params: raw})
		assert.Empty(t, detail.InputSummary.PortScanMode)
		encoded, err := json.Marshal(detail)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "do-not-project")
		assert.NotContains(t, string(encoded), "secret.example.com")
	}
}

func TestCreateAuditUsesOnlyNormalizedInfrastructurePortScanModeMetadata(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	audits := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audits))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	infrastructure, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "audited-full-tcp", TaskType: "ai_infra_scan", Content: "https://ai.example.com/private-target",
		Params: json.RawMessage(`{"model_id":"model-private","port_scan_mode":"full_tcp"}`),
	})
	require.NoError(t, err)
	mcp, err := service.Create(context.Background(), subject, mcpRepositoryCreateInput("audited-mcp"))
	require.NoError(t, err)

	metadataFor := func(taskID string) map[audit.Outcome]map[string]any {
		t.Helper()
		events, listErr := audits.List(context.Background(), audit.Filter{ResourceID: taskID, Action: audit.Action("task.created")})
		require.NoError(t, listErr)
		metadataByOutcome := make(map[audit.Outcome]map[string]any, len(events))
		for _, event := range events {
			metadata := map[string]any{}
			require.NoError(t, json.Unmarshal(event.Metadata, &metadata))
			metadataByOutcome[event.Outcome] = metadata
		}
		return metadataByOutcome
	}

	infrastructureMetadata := metadataFor(infrastructure.ID)
	for outcome, phase := range map[audit.Outcome]string{
		audit.OutcomePending: "requested",
		audit.OutcomeSuccess: "succeeded",
	} {
		metadata, exists := infrastructureMetadata[outcome]
		require.True(t, exists, outcome)
		assert.ElementsMatch(t, []string{"task_type", "port_scan_mode", "port_spec", "phase"}, mapKeys(metadata))
		assert.Equal(t, "ai_infra_scan", metadata["task_type"])
		assert.Equal(t, "full_tcp", metadata["port_scan_mode"])
		assert.Equal(t, "1-65535", metadata["port_spec"])
		assert.Equal(t, phase, metadata["phase"])
		serializedMetadata, marshalErr := json.Marshal(metadata)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(serializedMetadata), "private-target")
		assert.NotContains(t, string(serializedMetadata), "model-private")
	}

	mcpMetadata := metadataFor(mcp.ID)
	for outcome, phase := range map[audit.Outcome]string{
		audit.OutcomePending: "requested",
		audit.OutcomeSuccess: "succeeded",
	} {
		metadata, exists := mcpMetadata[outcome]
		require.True(t, exists, outcome)
		assert.ElementsMatch(t, []string{"source_kind", "authorization_confirmed", "phase"}, mapKeys(metadata))
		assert.Equal(t, "repository", metadata["source_kind"])
		assert.Equal(t, false, metadata["authorization_confirmed"])
		assert.Equal(t, phase, metadata["phase"])
		assert.NotContains(t, metadata, "port_scan_mode")
		assert.NotContains(t, metadata, "port_spec")
		serializedMetadata, marshalErr := json.Marshal(metadata)
		require.NoError(t, marshalErr)
		assert.NotContains(t, string(serializedMetadata), "github.com")
	}
}

func TestCreateRejectsUnboundedOrNonCanonicalInputBeforeAttachmentReads(t *testing.T) {
	repository := &countingAttachmentRepository{MemoryRepository: NewMemoryRepository()}
	audits := audit.NewService(audit.NewMemoryRepository())
	service := NewService(repository, &recordingEngine{}, audits)
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audits)
	require.NoError(t, err)
	service.SetAttachmentService(attachments)
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	tests := map[string]CreateInput{
		"unknown task":          {IdempotencyKey: "unknown", TaskType: "future_task", Content: "scan"},
		"legacy alias":          {IdempotencyKey: "alias", TaskType: "Mcp-Scan", Content: "scan"},
		"large content":         {IdempotencyKey: "content", TaskType: "mcp_scan", Content: strings.Repeat("x", (32<<10)+1), Params: json.RawMessage(`{"source_kind":"repository"}`)},
		"invalid country":       {IdempotencyKey: "country", TaskType: "mcp_scan", Content: "https://github.com/example/mcp-server.git", CountryIsoCode: "zh_CN_extra", Params: json.RawMessage(`{"source_kind":"repository"}`)},
		"too many attachments":  {IdempotencyKey: "many-attachments", TaskType: "mcp_scan", Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(`{"source_kind":"repository"}`), AttachmentIDs: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11"}},
		"duplicate attachments": {IdempotencyKey: "duplicate-attachments", TaskType: "mcp_scan", Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(`{"source_kind":"repository"}`), AttachmentIDs: []string{"opaque-1", "opaque-1"}},
		"long attachment id":    {IdempotencyKey: "long-attachment", TaskType: "mcp_scan", Content: "https://github.com/example/mcp-server.git", Params: json.RawMessage(`{"source_kind":"repository"}`), AttachmentIDs: []string{strings.Repeat("a", 129)}},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			before := repository.reads.Load()
			_, err := service.Create(context.Background(), subject, input)
			require.ErrorIs(t, err, ErrInvalid)
			assert.Equal(t, before, repository.reads.Load(), "非法输入不得触发附件数据库读取")
		})
	}
}

func TestCreateValidatesGovernedReferencesBeforePersistence(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &rejectingReferenceEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	_, err := service.Create(context.Background(), subject, CreateInput{
		IdempotencyKey: "raw-reference", TaskType: "model_redteam_report", Content: "scan",
		Params: json.RawMessage(`{"model_id":["sk-browser-sensitive-value"],"eval_model_id":"model-safe"}`),
	})

	require.ErrorIs(t, err, ErrInvalid)
	tasks, listErr := repository.List(context.Background())
	require.NoError(t, listErr)
	assert.Empty(t, tasks)
	assert.Zero(t, engine.submits.Load())
}

func TestDispatchFailureRetainsTaskAndRecordsSeparateStatus(t *testing.T) {
	repository := NewMemoryRepository()
	engine := &recordingEngine{err: NewTransientDispatchError(errors.New("agent temporarily unavailable"))}
	audits := audit.NewMemoryRepository()
	service := NewService(repository, engine, audit.NewService(audits))
	subject := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}

	view, err := service.Create(context.Background(), subject, mcpRepositoryCreateInput("dispatch-failure"))
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

	view, err := service.Create(context.Background(), subject, mcpRepositoryCreateInput("safe-dispatch-error"))
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
	task, err := service.Create(context.Background(), owner, mcpRepositoryCreateInput("owner-test"))
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
	aliceTask, err := service.Create(context.Background(), alice, mcpRepositoryCreateInput("alice"))
	require.NoError(t, err)
	bobTask, err := service.Create(context.Background(), bob, mcpRepositoryCreateInput("bob"))
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

func TestPostgresCreateLockSerializesLiveValidationAcrossServiceInstances(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_tasks").Error)
	engine := &gatedReferenceEngine{
		firstEntered: make(chan struct{}), releaseFirst: make(chan struct{}),
		secondEntered: make(chan struct{}), secondErr: errors.New("reference registry unavailable"),
	}
	firstService := NewService(NewGormRepository(db), engine, audit.NewService(audit.NewMemoryRepository()))
	secondService := NewService(NewGormRepository(db.Session(&gorm.Session{NewDB: true})), engine, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "postgres-lock-owner", Username: "alice", Role: identity.RoleUser}
	input := mcpRepositoryCreateInput("postgres-create-lock")
	firstDone := make(chan createResult, 1)
	go func() {
		view, createErr := firstService.Create(context.Background(), subject, input)
		firstDone <- createResult{view: view, err: createErr}
	}()
	select {
	case <-engine.firstEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("first service did not enter reference validation")
	}
	secondStarted := make(chan struct{})
	secondDone := make(chan createResult, 1)
	go func() {
		close(secondStarted)
		view, createErr := secondService.Create(context.Background(), subject, input)
		secondDone <- createResult{view: view, err: createErr}
	}()
	<-secondStarted
	secondValidated := false
	select {
	case <-engine.secondEntered:
		secondValidated = true
	case <-time.After(100 * time.Millisecond):
	}
	close(engine.releaseFirst)
	first := <-firstDone
	second := <-secondDone

	require.NoError(t, first.err)
	require.NoError(t, second.err)
	assert.False(t, secondValidated, "PostgreSQL 锁必须跨服务实例覆盖读取与实时校验")
	assert.Equal(t, first.view.ID, second.view.ID)
	assert.Equal(t, int64(1), engine.referenceCalls.Load())
	assert.Equal(t, int64(1), engine.submits.Load())
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
	input := mcpRepositoryCreateInput("lease-race")
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
	input := mcpRepositoryCreateInput("attempt-budget")
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(owner.UserID+"\x00"+input.IdempotencyKey)).String()
	base := time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC)
	_, created, err := repository.CreateOrGet(context.Background(), &Task{
		ID: taskID, OwnerUserID: owner.UserID, OwnerUsername: owner.Username,
		IdempotencyKey: input.IdempotencyKey, EngineSessionID: taskID, TaskType: input.TaskType,
		Content: input.Content, Params: input.Params, AttachmentRefs: json.RawMessage(`[]`),
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

	view, err := service.Create(context.Background(), owner, mcpRepositoryCreateInput("ack-pending"))
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
	created, err := service.Create(context.Background(), owner, mcpRepositoryCreateInput("auditor-read"))
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
	created, err := service.Create(context.Background(), owner, mcpRepositoryCreateInput("refresh"))
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

	view, err := service.Create(context.Background(), owner, mcpRepositoryCreateInput("pending-readback"))
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
	require.ErrorIs(t, err, ErrNotFound)
	_, _, _, err = attachments.Open(context.Background(), identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}, view.ID)
	require.ErrorIs(t, err, ErrForbidden)

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
	bound, err := repository.GetAttachment(context.Background(), view.ID)
	require.NoError(t, err)
	assert.Equal(t, AttachmentStateAttached, bound.State)
	require.ErrorIs(t, attachments.Abort(context.Background(), owner, view.ID), ErrAttachmentNotReady, "已绑定附件不得被回收")
	file, _, _, err := attachments.Open(context.Background(), owner, view.ID)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	retried, err := service.Create(context.Background(), owner, CreateInput{
		IdempotencyKey: "with-attachment", TaskType: "ai_infra_scan", Content: "scan", AttachmentIDs: []string{view.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, created.ID, retried.ID)

	_, err = service.Create(context.Background(), identity.Subject{UserID: "other", Username: "mallory", Role: identity.RoleUser}, CreateInput{
		IdempotencyKey: "forged-attachment", TaskType: "ai_infra_scan", Content: "scan", AttachmentIDs: []string{view.ID},
	})
	require.ErrorIs(t, err, ErrForbidden)
}

func TestIdempotentCreateComparesAttachmentRefsAndKeepsOwnersIsolated(t *testing.T) {
	repository := NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, audits)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	first, err := attachments.Upload(context.Background(), owner, "first.txt", strings.NewReader("first"))
	require.NoError(t, err)
	second, err := attachments.Upload(context.Background(), owner, "second.txt", strings.NewReader("second"))
	require.NoError(t, err)
	engine := &controlledReferenceEngine{}
	service := NewService(repository, engine, audits)
	service.SetAttachmentService(attachments)
	input := CreateInput{
		IdempotencyKey: "attachment-retry", TaskType: "ai_infra_scan", Content: "scan",
		AttachmentIDs: []string{first.ID},
	}

	created, err := service.Create(context.Background(), owner, input)
	require.NoError(t, err)
	engine.referenceErr = ErrInvalid
	retried, err := service.Create(context.Background(), owner, input)
	require.NoError(t, err)
	assert.Equal(t, created.ID, retried.ID)
	assert.Equal(t, int64(1), engine.referenceCalls.Load())

	changed := input
	changed.AttachmentIDs = []string{second.ID}
	_, err = service.Create(context.Background(), owner, changed)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, int64(1), engine.referenceCalls.Load(), "附件引用冲突不得进入实时引用校验")

	otherOwner := identity.Subject{UserID: "user-2", Username: "bob", Role: identity.RoleUser}
	_, err = service.Create(context.Background(), otherOwner, input)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, int64(2), engine.referenceCalls.Load(), "不同所有者不得复用原任务的幂等结果")
	assert.Equal(t, int64(1), engine.submits.Load())
}

func TestRegularAttachmentUploadIsTraceableBeforeReadingRequestBytes(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-1", Username: "alice", Role: identity.RoleUser}
	observedUploading := false
	sent := false
	reader := readerCallback(func(buffer []byte) (int, error) {
		repository.mu.Lock()
		for _, attachment := range repository.attachments {
			observedUploading = observedUploading || attachment.State == AttachmentStateUploading
		}
		repository.mu.Unlock()
		if sent {
			return 0, io.EOF
		}
		sent = true
		return copy(buffer, "1234"), nil
	})

	view, err := attachments.Upload(context.Background(), owner, "traceable.txt", reader)
	require.NoError(t, err)
	assert.True(t, observedUploading, "请求字节写入前必须已有可供TTL回收的记录")
	stored, err := repository.GetAttachment(context.Background(), view.ID)
	require.NoError(t, err)
	assert.Equal(t, AttachmentStateReady, stored.State)
	assert.NoFileExists(t, filepath.Join(attachments.config.UploadDir, stored.StorageName+".uploading"))
}

type failingAttachmentAuditRecorder struct {
	err error
}

func (recorder *failingAttachmentAuditRecorder) Record(context.Context, identity.Subject, audit.EventInput) error {
	return recorder.err
}

func TestAttachmentDownloadOwnerAuditorAndAdminAuthorization(t *testing.T) {
	ctx := context.Background()
	downloadAction := audit.ActionAttachmentDownloadAuthorized
	repository := NewMemoryRepository()
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(auditRepository)
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	other := identity.Subject{UserID: "user-other", Username: "other", Role: identity.RoleUser}
	auditor := identity.Subject{UserID: "user-auditor", Username: "auditor", Role: identity.RoleAuditor}
	admin := identity.Subject{UserID: "user-admin", Username: "admin", Role: identity.RoleAdmin}

	ownerAttachment, err := attachments.Upload(ctx, owner, "private-name.txt", strings.NewReader("private-content"))
	require.NoError(t, err)
	adminAttachment, err := attachments.Upload(ctx, admin, "admin-owned.txt", strings.NewReader("admin-content"))
	require.NoError(t, err)
	storedOwnerAttachment, err := repository.GetAttachment(ctx, ownerAttachment.ID)
	require.NoError(t, err)
	auditorAttachment := *storedOwnerAttachment
	auditorAttachment.ID = "auditor-owned-attachment"
	auditorAttachment.OwnerUserID = auditor.UserID
	require.NoError(t, repository.CreateAttachment(ctx, &auditorAttachment))
	var storageOpenCalls atomic.Int64
	attachments.openFile = func(path string) (*os.File, error) {
		storageOpenCalls.Add(1)
		return os.Open(path)
	}

	file, filename, _, err := attachments.Open(ctx, owner, ownerAttachment.ID)
	require.NoError(t, err)
	assert.Equal(t, "private-name.txt", filename)
	content, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, "private-content", string(content))
	assert.Equal(t, int64(1), storageOpenCalls.Load())

	_, _, _, err = attachments.Open(ctx, other, ownerAttachment.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, _, _, err = attachments.Open(ctx, auditor, ownerAttachment.ID)
	require.ErrorIs(t, err, ErrForbidden)
	_, _, _, err = attachments.Open(ctx, auditor, auditorAttachment.ID)
	require.ErrorIs(t, err, ErrForbidden, "auditors cannot download their own raw attachments")
	_, _, _, err = attachments.Open(ctx, auditor, "missing-attachment")
	require.ErrorIs(t, err, ErrForbidden, "auditor denial must not reveal attachment existence")
	assert.Equal(t, int64(1), storageOpenCalls.Load(), "denied user and auditor paths must not call Storage Open")

	file, _, _, err = attachments.Open(ctx, admin, adminAttachment.ID)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, int64(2), storageOpenCalls.Load())
	sameOwnerEvents, err := auditRepository.List(ctx, audit.Filter{Action: downloadAction, ResourceID: adminAttachment.ID})
	require.NoError(t, err)
	assert.Empty(t, sameOwnerEvents, "an administrator following the owner path is not a cross-owner governance download")

	var successDurableAtOpen atomic.Bool
	attachments.openFile = func(path string) (*os.File, error) {
		storageOpenCalls.Add(1)
		visible, listErr := auditRepository.List(ctx, audit.Filter{Action: downloadAction, ResourceID: ownerAttachment.ID})
		if listErr == nil && len(visible) == 1 && visible[0].Outcome == audit.OutcomeSuccess {
			successDurableAtOpen.Store(true)
		}
		return os.Open(path)
	}
	file, _, _, err = attachments.Open(ctx, admin, ownerAttachment.ID)
	require.NoError(t, err)
	content, err = io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, "private-content", string(content))
	assert.True(t, successDurableAtOpen.Load(), "successful governance audit must be durable before Storage Open")

	events, err := auditRepository.List(ctx, audit.Filter{Action: downloadAction, ResourceID: ownerAttachment.ID})
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, downloadAction, event.Action)
	assert.Equal(t, audit.OutcomeSuccess, event.Outcome)
	assert.Equal(t, admin.UserID, event.ActorUserID)
	var metadata map[string]any
	require.NoError(t, json.Unmarshal(event.Metadata, &metadata))
	assert.ElementsMatch(t, []string{"attachment_id", "owner_user_id", "governance_action"}, mapKeys(metadata))
	assert.Equal(t, ownerAttachment.ID, metadata["attachment_id"])
	assert.Equal(t, owner.UserID, metadata["owner_user_id"])
	assert.Equal(t, "cross_owner_download_authorized", metadata["governance_action"])
	serialized := string(event.Metadata)
	for _, secret := range []string{"private-name.txt", storedOwnerAttachment.StorageName, "private-content", attachments.config.UploadDir, "token", "phase"} {
		assert.NotContains(t, serialized, secret)
	}
	legacyEvents, err := auditRepository.List(ctx, audit.Filter{Action: audit.ActionAttachmentDownloaded, ResourceID: ownerAttachment.ID})
	require.NoError(t, err)
	assert.Empty(t, legacyEvents, "authorization must not claim that file delivery completed")
}

func TestAttachmentDownloadAdminAuditFailureFailsClosed(t *testing.T) {
	ctx := context.Background()
	downloadAction := audit.ActionAttachmentDownloadAuthorized
	repository := NewMemoryRepository()
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(auditRepository)
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	admin := identity.Subject{UserID: "user-admin", Username: "admin", Role: identity.RoleAdmin}
	view, err := attachments.Upload(ctx, owner, "private-name.txt", strings.NewReader("private-content"))
	require.NoError(t, err)

	var storageOpenCalls atomic.Int64
	attachments.openFile = func(path string) (*os.File, error) {
		storageOpenCalls.Add(1)
		return os.Open(path)
	}
	injected := errors.New("injected attachment audit append failure")
	attachments.audits = &failingAttachmentAuditRecorder{err: injected}
	file, _, _, err := attachments.Open(ctx, admin, view.ID)
	if file != nil {
		_ = file.Close()
	}
	assert.EqualError(t, err, "无法持久化附件下载授权审计")
	assert.NotErrorIs(t, err, injected)
	assert.Nil(t, file)
	assert.Zero(t, storageOpenCalls.Load(), "audit append failure must occur before Storage Open")

	events, err := auditRepository.List(ctx, audit.Filter{Action: downloadAction, ResourceID: view.ID})
	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestAttachmentDownloadAdminMissingStorageDoesNotRecordFalseSuccess(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(auditRepository)
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	now := time.Now().UTC()
	attachment := &Attachment{
		ID: "missing-storage", OwnerUserID: "user-owner", OriginalName: "private-name.txt", StorageName: "missing-storage.txt",
		Size: 15, ChunkBytes: 15, State: AttachmentStateReady, CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repository.CreateAttachment(ctx, attachment))
	admin := identity.Subject{UserID: "user-admin", Username: "admin", Role: identity.RoleAdmin}

	file, _, _, err := attachments.Open(ctx, admin, attachment.ID)
	assert.Nil(t, file)
	require.ErrorIs(t, err, ErrNotFound)
	events, err := auditRepository.List(ctx, audit.Filter{Action: audit.ActionAttachmentDownloadAuthorized, ResourceID: attachment.ID})
	require.NoError(t, err)
	assert.Empty(t, events, "a missing storage object must not produce a successful download audit")
}

func TestAttachmentDownloadAdminOpenFailureRecordsAuthorizationNotDelivery(t *testing.T) {
	tests := []struct {
		name       string
		open       func(string) (*os.File, error)
		notFound   bool
		storageErr bool
	}{
		{name: "not found", open: func(string) (*os.File, error) { return nil, os.ErrNotExist }, notFound: true},
		{name: "permission", open: func(string) (*os.File, error) { return nil, os.ErrPermission }, storageErr: true},
		{name: "file stat", open: func(path string) (*os.File, error) {
			file, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			if err := file.Close(); err != nil {
				return nil, err
			}
			return file, nil
		}, storageErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			repository := NewMemoryRepository()
			auditRepository := audit.NewMemoryRepository()
			auditService := audit.NewService(auditRepository)
			attachments, err := NewAttachmentService(repository, AttachmentConfig{
				UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8,
			}, auditService)
			require.NoError(t, err)
			owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
			admin := identity.Subject{UserID: "user-admin", Username: "admin", Role: identity.RoleAdmin}
			view, err := attachments.Upload(ctx, owner, "private-name.txt", strings.NewReader("private-content"))
			require.NoError(t, err)
			attachments.openFile = test.open

			file, _, _, err := attachments.Open(ctx, admin, view.ID)
			if file != nil {
				_ = file.Close()
			}
			assert.Nil(t, file)
			if test.notFound {
				require.ErrorIs(t, err, ErrNotFound)
			}
			if test.storageErr {
				require.ErrorIs(t, err, ErrAttachmentStorage)
				assert.EqualError(t, err, "附件存储暂时不可用")
				assert.NotContains(t, err.Error(), attachments.config.UploadDir)
			}
			events, err := auditRepository.List(ctx, audit.Filter{Action: audit.ActionAttachmentDownloadAuthorized, ResourceID: view.ID})
			require.NoError(t, err)
			require.Len(t, events, 1)
			assert.Equal(t, audit.OutcomeSuccess, events[0].Outcome)
			var metadata map[string]any
			require.NoError(t, json.Unmarshal(events[0].Metadata, &metadata))
			assert.ElementsMatch(t, []string{"attachment_id", "owner_user_id", "governance_action"}, mapKeys(metadata))
			assert.Equal(t, "cross_owner_download_authorized", metadata["governance_action"])
		})
	}
}

func TestAttachmentDownloadAdminRejectsReplacedStorageObject(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(auditRepository)
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	admin := identity.Subject{UserID: "user-admin", Username: "admin", Role: identity.RoleAdmin}
	view, err := attachments.Upload(ctx, owner, "private-name.txt", strings.NewReader("private-content"))
	require.NoError(t, err)
	replacementPath := filepath.Join(t.TempDir(), "replacement.txt")
	require.NoError(t, os.WriteFile(replacementPath, []byte("wrong-content"), 0o600))
	replacement, err := os.Open(replacementPath)
	require.NoError(t, err)
	attachments.openFile = func(string) (*os.File, error) { return replacement, nil }

	file, _, _, err := attachments.Open(ctx, admin, view.ID)
	assert.Nil(t, file)
	require.ErrorIs(t, err, ErrAttachmentStorage)
	assert.EqualError(t, err, "附件存储暂时不可用")
	buffer := make([]byte, 1)
	_, readErr := replacement.Read(buffer)
	assert.Error(t, readErr, "rejected replacement handle must be closed")
	events, err := auditRepository.List(ctx, audit.Filter{Action: audit.ActionAttachmentDownloadAuthorized, ResourceID: view.ID})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomeSuccess, events[0].Outcome)
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
	require.ErrorIs(t, err, ErrNotFound)
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

	err = attachments.UploadChunk(context.Background(), owner, view.ID, 0, strings.NewReader(""))
	require.ErrorIs(t, err, ErrInvalid)
	err = attachments.UploadChunk(context.Background(), owner, view.ID, 0, strings.NewReader("12345"))
	require.ErrorIs(t, err, ErrAttachmentTooLarge)
	require.NoError(t, attachments.UploadChunk(context.Background(), owner, view.ID, 0, strings.NewReader("1234")))
	require.NoError(t, attachments.UploadChunk(context.Background(), owner, view.ID, 1, strings.NewReader("567")))
	err = attachments.UploadChunk(context.Background(), owner, view.ID, 2, strings.NewReader("8"))
	require.ErrorIs(t, err, ErrInvalid)

	_, err = attachments.Merge(context.Background(), owner, view.ID, 3, 7)
	require.ErrorIs(t, err, ErrInvalid)
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

func TestAttachmentAbortIsOwnerScopedAndRemovesUploadingChunks(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	other := identity.Subject{UserID: "user-other", Username: "other", Role: identity.RoleUser}
	auditor := identity.Subject{UserID: "user-auditor", Username: "auditor", Role: identity.RoleAuditor}
	admin := identity.Subject{UserID: "user-admin", Username: "admin", Role: identity.RoleAdmin}
	view, err := attachments.BeginChunked(context.Background(), owner, "chunked.txt", 7)
	require.NoError(t, err)
	require.NoError(t, attachments.UploadChunk(context.Background(), owner, view.ID, 0, strings.NewReader("1234")))

	require.ErrorIs(t, attachments.Abort(context.Background(), other, view.ID), ErrNotFound)
	require.ErrorIs(t, attachments.Abort(context.Background(), auditor, view.ID), ErrForbidden)
	require.NoError(t, attachments.Abort(context.Background(), owner, view.ID))
	_, err = repository.GetAttachment(context.Background(), view.ID)
	require.ErrorIs(t, err, ErrNotFound)
	assert.NoDirExists(t, filepath.Join(attachments.config.UploadDir, ".chunks", view.ID))

	adminTarget, err := attachments.BeginChunked(context.Background(), owner, "admin.txt", 4)
	require.NoError(t, err)
	require.NoError(t, attachments.Abort(context.Background(), admin, adminTarget.ID))

	ready, err := attachments.Upload(context.Background(), owner, "ready.txt", strings.NewReader("1234"))
	require.NoError(t, err)
	require.NoError(t, attachments.Abort(context.Background(), owner, ready.ID), "未绑定 ready 附件也必须可回收")
	_, err = repository.GetAttachment(context.Background(), ready.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAttachmentAbortRetainsDeletingTombstoneUntilStorageCleanupSucceeds(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	ready, err := attachments.Upload(context.Background(), owner, "retry.txt", strings.NewReader("1234"))
	require.NoError(t, err)

	attachments.removeFile = func(string) error { return errors.New("storage unavailable") }
	require.ErrorIs(t, attachments.Abort(context.Background(), owner, ready.ID), ErrAttachmentStorage)
	retained, err := repository.GetAttachment(context.Background(), ready.ID)
	require.NoError(t, err)
	assert.Equal(t, AttachmentStateDeleting, retained.State)

	attachments.removeFile = os.Remove
	require.NoError(t, attachments.Abort(context.Background(), owner, ready.ID))
	_, err = repository.GetAttachment(context.Background(), ready.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAttachmentServicePurgesExpiredUploadingRecordsAtBeginBoundary(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	attachments.now = func() time.Time { return base }
	expired, err := attachments.BeginChunked(context.Background(), owner, "expired.txt", 4)
	require.NoError(t, err)
	require.NoError(t, attachments.UploadChunk(context.Background(), owner, expired.ID, 0, strings.NewReader("1234")))
	ready, err := attachments.Upload(context.Background(), owner, "ready.txt", strings.NewReader("1234"))
	require.NoError(t, err)

	attachments.now = func() time.Time { return base.Add(2 * time.Hour) }
	_, err = attachments.BeginChunked(context.Background(), owner, "next.txt", 4)
	require.NoError(t, err)
	_, err = repository.GetAttachment(context.Background(), expired.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repository.GetAttachment(context.Background(), ready.ID)
	require.ErrorIs(t, err, ErrNotFound)
	assert.NoDirExists(t, filepath.Join(attachments.config.UploadDir, ".chunks", expired.ID))
}

func TestAttachmentServicePurgesExpiredUnboundRecordsAtRegularUploadBoundary(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "user-owner", Username: "owner", Role: identity.RoleUser}
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	attachments.now = func() time.Time { return base }
	stale, err := attachments.Upload(context.Background(), owner, "stale.txt", strings.NewReader("1234"))
	require.NoError(t, err)

	attachments.now = func() time.Time { return base.Add(2 * time.Hour) }
	_, err = attachments.Upload(context.Background(), owner, "next.txt", strings.NewReader("5678"))
	require.NoError(t, err)
	_, err = repository.GetAttachment(context.Background(), stale.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAttachmentPurgeProcessesAtMostOneBoundedBatch(t *testing.T) {
	repository := NewMemoryRepository()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	attachments.now = func() time.Time { return base.Add(2 * time.Hour) }
	for index := 0; index < expiredUploadBatchSize+1; index++ {
		require.NoError(t, repository.CreateAttachment(context.Background(), &Attachment{
			ID: fmt.Sprintf("stale-%03d", index), OwnerUserID: "owner", OriginalName: "old", StorageName: fmt.Sprintf("old-%03d", index),
			State: AttachmentStateUploading, CreatedAt: base, UpdatedAt: base,
		}))
	}
	require.NoError(t, attachments.PurgeExpiredUploads(context.Background()))
	repository.mu.Lock()
	remaining := len(repository.attachments)
	repository.mu.Unlock()
	assert.Equal(t, 1, remaining)
}

func TestAttachmentPurgeRemovesTrackedTemporaryFilesAfterProcessCrash(t *testing.T) {
	repository := NewMemoryRepository()
	uploadDir := t.TempDir()
	attachments, err := NewAttachmentService(repository, AttachmentConfig{
		UploadDir: uploadDir, MaxFileBytes: 8, MaxChunkBytes: 4, UploadTTL: time.Hour,
	}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	for _, fixture := range []struct {
		id, storage, suffix string
	}{
		{id: "crashed-upload", storage: "upload-artifact", suffix: ".uploading"},
		{id: "crashed-merge", storage: "merge-artifact", suffix: ".merging"},
	} {
		require.NoError(t, repository.CreateAttachment(context.Background(), &Attachment{
			ID: fixture.id, OwnerUserID: "owner", OriginalName: fixture.id + ".txt", StorageName: fixture.storage,
			State: AttachmentStateUploading, CreatedAt: base, UpdatedAt: base,
		}))
		require.NoError(t, os.WriteFile(filepath.Join(uploadDir, fixture.storage+fixture.suffix), []byte("partial"), 0o600))
	}
	attachments.now = func() time.Time { return base.Add(2 * time.Hour) }

	require.NoError(t, attachments.PurgeExpiredUploads(context.Background()))
	assert.NoFileExists(t, filepath.Join(uploadDir, "upload-artifact.uploading"))
	assert.NoFileExists(t, filepath.Join(uploadDir, "merge-artifact.merging"))
	_, err = repository.GetAttachment(context.Background(), "crashed-upload")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repository.GetAttachment(context.Background(), "crashed-merge")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGormAttachmentCleanupUsesUnboundStatesAndDeletingTombstone(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_attachments").Error)
	repository := NewGormRepository(db)
	base := time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC)
	for _, attachment := range []*Attachment{
		{ID: "expired-upload", OwnerUserID: "owner", OriginalName: "old", StorageName: "old", Size: 4, State: AttachmentStateUploading, CreatedAt: base, UpdatedAt: base},
		{ID: "active-upload", OwnerUserID: "owner", OriginalName: "new", StorageName: "new", Size: 4, State: AttachmentStateUploading, CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
		{ID: "ready-old", OwnerUserID: "owner", OriginalName: "ready", StorageName: "ready", Size: 4, State: AttachmentStateReady, CreatedAt: base, UpdatedAt: base},
		{ID: "attached-old", OwnerUserID: "owner", OriginalName: "bound", StorageName: "bound", Size: 4, State: AttachmentStateAttached, CreatedAt: base, UpdatedAt: base},
		{ID: "deleting", OwnerUserID: "owner", OriginalName: "retry", StorageName: "retry", Size: 4, State: AttachmentStateDeleting, CreatedAt: base, UpdatedAt: base.Add(2 * time.Hour)},
	} {
		require.NoError(t, repository.CreateAttachment(context.Background(), attachment))
	}
	expired, err := repository.ListAttachmentCleanupCandidates(context.Background(), base.Add(time.Hour), 100)
	require.NoError(t, err)
	require.Len(t, expired, 3)
	assert.Equal(t, []string{"expired-upload", "ready-old", "deleting"}, []string{expired[0].ID, expired[1].ID, expired[2].ID})
	marked, err := repository.MarkAttachmentDeleting(context.Background(), "expired-upload", "owner", base.Add(time.Hour), base.Add(3*time.Hour))
	require.NoError(t, err)
	assert.True(t, marked)
	deleted, err := repository.DeleteMarkedAttachment(context.Background(), "expired-upload", "owner")
	require.NoError(t, err)
	assert.True(t, deleted)
	_, err = repository.GetAttachment(context.Background(), "expired-upload")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = repository.GetAttachment(context.Background(), "active-upload")
	require.NoError(t, err)
	_, err = repository.GetAttachment(context.Background(), "ready-old")
	require.NoError(t, err)
	_, err = repository.GetAttachment(context.Background(), "attached-old")
	require.NoError(t, err)
}

func TestAttachmentConfigHasSafeDefaultsAndRejectsInvalidValues(t *testing.T) {
	uploadDir := t.TempDir()
	t.Setenv("AIG_MAX_UPLOAD_BYTES", "")
	t.Setenv("AIG_MAX_CHUNK_BYTES", "")
	t.Setenv("AIG_ATTACHMENT_UPLOAD_TTL", "")
	config, err := LoadAttachmentConfigFromEnv(uploadDir)
	require.NoError(t, err)
	assert.Greater(t, config.MaxFileBytes, int64(0))
	assert.Greater(t, config.MaxChunkBytes, int64(0))
	assert.LessOrEqual(t, config.MaxChunkBytes, config.MaxFileBytes)
	assert.Greater(t, config.UploadTTL, time.Duration(0))

	t.Setenv("AIG_MAX_UPLOAD_BYTES", "not-a-number")
	_, err = LoadAttachmentConfigFromEnv(uploadDir)
	require.Error(t, err)
	assert.NotContains(t, fmt.Sprint(err), uploadDir)

	t.Setenv("AIG_MAX_UPLOAD_BYTES", "")
	t.Setenv("AIG_ATTACHMENT_UPLOAD_TTL", "0s")
	_, err = LoadAttachmentConfigFromEnv(uploadDir)
	require.Error(t, err)
}

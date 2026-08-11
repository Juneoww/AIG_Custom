package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failOnRecord struct {
	delegate audit.Recorder
	failOn   int
	calls    int
}

type completionIntentFailingRepository struct {
	delegate *audit.MemoryRepository
}

func (repository *completionIntentFailingRepository) Append(ctx context.Context, event *audit.Event) error {
	return repository.delegate.Append(ctx, event)
}

func (repository *completionIntentFailingRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *completionIntentFailingRepository) EnqueueCompletion(context.Context, *audit.CompletionOutbox) error {
	return errors.New("injected completion intent failure")
}

func (repository *completionIntentFailingRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *completionIntentFailingRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *completionIntentFailingRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}

func (repository *completionIntentFailingRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *completionIntentFailingRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func (recorder *failOnRecord) Record(ctx context.Context, subject identity.Subject, input audit.EventInput) error {
	recorder.calls++
	if recorder.calls == recorder.failOn {
		return errors.New("injected audit failure")
	}
	return recorder.delegate.Record(ctx, subject, input)
}

func (recorder *failOnRecord) PersistCompletion(ctx context.Context, subject identity.Subject, input audit.EventInput) (string, error) {
	delegate, ok := recorder.delegate.(audit.CompletionRecorder)
	if !ok {
		return "", errors.New("delegate does not support completion persistence")
	}
	return delegate.PersistCompletion(ctx, subject, input)
}

func (recorder *failOnRecord) DeliverCompletion(ctx context.Context, id string) error {
	delegate, ok := recorder.delegate.(audit.CompletionRecorder)
	if !ok {
		return errors.New("delegate does not support completion delivery")
	}
	return delegate.DeliverCompletion(ctx, id)
}

func TestOnlyAdministratorCanChangeKnowledgeAndEveryChangeIsAudited(t *testing.T) {
	ctx := context.Background()
	auditService := audit.NewService(audit.NewMemoryRepository())
	service := NewService(auditService)
	admin := identity.Subject{UserID: "admin-id", Username: "admin", Role: identity.RoleAdmin}
	user := identity.Subject{UserID: "user-id", Role: identity.RoleUser}
	auditor := identity.Subject{UserID: "auditor-id", Role: identity.RoleAuditor}
	content := "old"

	for _, subject := range []identity.Subject{user, auditor} {
		called := false
		err := service.Apply(ctx, subject, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
			called = true
			content = "forbidden"
			return nil
		})
		assert.ErrorIs(t, err, ErrForbidden)
		assert.False(t, called)
	}

	require.NoError(t, service.Apply(ctx, admin, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
		content = "new"
		return nil
	}))
	assert.Equal(t, "new", content)
	events, err := auditService.Query(ctx, admin, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, "fingerprint", events[1].ResourceType)
	assert.Equal(t, "demo", events[1].ResourceID)
	assert.Equal(t, events[0].RequestID, events[1].RequestID)
}

func TestKnowledgeChangeDoesNotMutatePreviouslyCapturedReportContent(t *testing.T) {
	ctx := context.Background()
	auditService := audit.NewService(audit.NewMemoryRepository())
	service := NewService(auditService)
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	currentRules := []byte("severity: high\n")
	reportSnapshot := append([]byte(nil), currentRules...)

	require.NoError(t, service.Apply(ctx, admin, Change{Kind: KindVulnerability, Operation: OperationUpdate, ResourceID: "CVE-TEST"}, func() error {
		currentRules = []byte("severity: low\n")
		return nil
	}))
	assert.Equal(t, "severity: high\n", string(reportSnapshot))
	assert.Equal(t, "severity: low\n", string(currentRules))
}

func TestKnowledgeMutationRequiresDurableAuditIntentBeforeChangingContent(t *testing.T) {
	ctx := context.Background()
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	content := "old"
	service := NewService(&failOnRecord{delegate: audit.NewService(audit.NewMemoryRepository()), failOn: 1})

	err := service.Apply(ctx, admin, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
		content = "new"
		return nil
	})
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrPendingAuditRecovery)
	assert.Equal(t, "old", content)
}

func TestKnowledgeMutationDoesNotStartWhenCompletionIntentCannotBePrepared(t *testing.T) {
	ctx := context.Background()
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	content := "old"
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(&completionIntentFailingRepository{delegate: auditRepository})
	service := NewService(auditService)

	err := service.Apply(ctx, admin, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
		content = "new"
		return nil
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrPendingAuditRecovery)
	assert.Equal(t, "old", content)
	events, listErr := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, listErr)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
}

func TestKnowledgeBusinessFailureRemainsTheOriginalError(t *testing.T) {
	ctx := context.Background()
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	repository := audit.NewMemoryRepository()
	service := NewService(audit.NewService(repository))
	businessErr := errors.New("legacy mutation rejected")
	mutations := 0

	err := service.Apply(ctx, admin, Change{Kind: KindFingerprint, Operation: OperationUpdate, ResourceID: "demo"}, func() error {
		mutations++
		return businessErr
	})
	assert.ErrorIs(t, err, businessErr)
	assert.NotErrorIs(t, err, ErrPendingAuditRecovery)
	assert.Equal(t, 1, mutations)
	events, listErr := repository.List(ctx, audit.Filter{})
	require.NoError(t, listErr)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomeFailure, events[1].Outcome)
}

func TestAsyncKnowledgeAuditSeparatesRequestFromActualCompletion(t *testing.T) {
	ctx := context.Background()
	repository := audit.NewMemoryRepository()
	auditService := audit.NewService(repository)
	service := NewService(auditService)
	admin := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}

	completion, err := service.BeginAsync(ctx, admin, Change{Kind: KindSystemData, Operation: OperationUpdate, ResourceID: "system-data"})
	require.NoError(t, err)
	require.NoError(t, completion(ctx, true, map[string]any{"files_updated": 7}))

	events, err := auditService.Query(ctx, admin, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.ActionKnowledgeChangeRequested, events[0].Action)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}

package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failOnAppendRepository struct {
	delegate *MemoryRepository
	failOn   int
	calls    int
}

func (repository *failOnAppendRepository) Append(ctx context.Context, event *Event) error {
	repository.calls++
	if repository.calls == repository.failOn {
		return errors.New("injected audit append failure")
	}
	return repository.delegate.Append(ctx, event)
}

func (repository *failOnAppendRepository) List(ctx context.Context, filter Filter) ([]Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *failOnAppendRepository) EnqueueCompletion(ctx context.Context, completion *CompletionOutbox) error {
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *failOnAppendRepository) Completion(ctx context.Context, id string) (*CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *failOnAppendRepository) ListPendingCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}

func (repository *failOnAppendRepository) UpdateCompletion(ctx context.Context, completion *CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *failOnAppendRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func TestServiceRecordsQueryableSanitizedEvents(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository)
	actor := identity.Subject{UserID: "admin-id", Username: "admin", Role: identity.RoleAdmin}

	err := service.Record(context.Background(), actor, EventInput{
		Action:       ActionAccountCreated,
		ResourceType: "user",
		ResourceID:   "user-id",
		Outcome:      OutcomeSuccess,
		Metadata: map[string]any{
			"token":        "plain-model-token",
			"nested":       map[string]any{"api_key": "another-secret", "safe": "kept"},
			"typed_nested": map[string]string{"client_secret": "typed-secret", "label": "typed-kept"},
		},
	})
	require.NoError(t, err)

	events, err := service.Query(context.Background(), actor, Filter{Limit: 20})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, ActionAccountCreated, events[0].Action)
	assert.Equal(t, actor.UserID, events[0].ActorUserID)

	encoded, err := json.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "plain-model-token")
	assert.NotContains(t, string(encoded), "another-secret")
	assert.NotContains(t, string(encoded), "typed-secret")
	assert.Contains(t, string(encoded), RedactedValue)
	assert.Contains(t, string(encoded), "kept")
	assert.Contains(t, string(encoded), "typed-kept")
}

func TestAuditQueryIsReadOnlyForAdminAndAuditor(t *testing.T) {
	service := NewService(NewMemoryRepository())
	ctx := context.Background()
	admin := identity.Subject{UserID: "admin", Role: identity.RoleAdmin}
	auditor := identity.Subject{UserID: "auditor", Role: identity.RoleAuditor}
	user := identity.Subject{UserID: "user", Role: identity.RoleUser}

	require.NoError(t, service.Record(ctx, admin, EventInput{Action: ActionSystemConfigurationChanged, Outcome: OutcomeSuccess}))
	_, err := service.Query(ctx, auditor, Filter{})
	require.NoError(t, err)
	_, err = service.Query(ctx, user, Filter{})
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestAuthenticationAttemptsHaveStableAuditBoundary(t *testing.T) {
	service := NewService(NewMemoryRepository())
	ctx := context.Background()

	require.NoError(t, service.AuthenticationAttempt(ctx, identity.AuthenticationEvent{
		Username: "alice",
		Subject:  identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser},
		Success:  true,
		ClientIP: "192.0.2.10",
	}))
	require.NoError(t, service.AuthenticationAttempt(ctx, identity.AuthenticationEvent{
		Username: "unknown",
		Success:  false,
		ClientIP: "192.0.2.11",
	}))

	events, err := service.Query(ctx, identity.Subject{Role: identity.RoleAuditor}, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, ActionLoginSuccess, events[0].Action)
	assert.Equal(t, ActionLoginFailure, events[1].Action)
}

func TestMutationCompletionFailureQueuesDurableOutboxAndReconcilesIdempotently(t *testing.T) {
	ctx := context.Background()
	actor := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}

	beginFailure := NewService(&failOnAppendRepository{delegate: NewMemoryRepository(), failOn: 1})
	_, err := BeginMutation(ctx, beginFailure, actor, EventInput{Action: ActionKnowledgeChanged, ResourceType: "fingerprint", ResourceID: "demo"})
	assert.Error(t, err, "a mutation must not begin when its durable audit intent cannot be stored")

	delegate := NewMemoryRepository()
	completionFailure := NewService(&failOnAppendRepository{delegate: delegate, failOn: 2})
	mutation, err := BeginMutation(ctx, completionFailure, actor, EventInput{Action: ActionKnowledgeChanged, ResourceType: "fingerprint", ResourceID: "demo"})
	require.NoError(t, err)
	require.NoError(t, mutation.Succeeded(ctx, "", map[string]any{"token": "completion-secret"}), "a persisted completion must not turn a successful mutation into an API error")

	events, err := delegate.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, OutcomePending, events[0].Outcome)
	assert.NotEmpty(t, events[0].RequestID, "pending intent correlates later completion or reconciliation")
	pending, err := completionFailure.PendingCompletions(ctx, actor, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, events[0].RequestID, pending[0].RequestID)
	assert.NotContains(t, string(pending[0].Metadata), "completion-secret")
	assert.Contains(t, string(pending[0].Metadata), RedactedValue)

	reconciled, err := completionFailure.Reconcile(ctx, actor, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, reconciled)
	events, err = delegate.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, events[0].RequestID, events[1].RequestID)
	reconciled, err = completionFailure.Reconcile(ctx, actor, 10)
	require.NoError(t, err)
	assert.Zero(t, reconciled)
	events, err = delegate.List(ctx, Filter{})
	require.NoError(t, err)
	assert.Len(t, events, 2, "reconciliation must not duplicate a delivered completion")
}

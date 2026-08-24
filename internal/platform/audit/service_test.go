package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failOnAppendRepository struct {
	delegate *MemoryRepository
	failOn   int
	calls    int
}

type ambiguousCompletionRepository struct {
	delegate  *MemoryRepository
	uncertain bool
}

type ambiguousTransactionRecorder struct {
	*Service
	transactions int
}

func (recorder *ambiguousTransactionRecorder) WithinTransaction(ctx context.Context, apply func(context.Context) error) error {
	recorder.transactions++
	if err := apply(ctx); err != nil {
		return err
	}
	return errors.New("transaction commit acknowledgement is uncertain")
}

func (repository *ambiguousCompletionRepository) Append(ctx context.Context, event *Event) error {
	return repository.delegate.Append(ctx, event)
}

func (repository *ambiguousCompletionRepository) List(ctx context.Context, filter Filter) ([]Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *ambiguousCompletionRepository) EnqueueCompletion(ctx context.Context, completion *CompletionOutbox) error {
	if err := repository.delegate.EnqueueCompletion(ctx, completion); err != nil {
		return err
	}
	if !repository.uncertain {
		repository.uncertain = true
		return errors.New("completion commit result is uncertain")
	}
	return nil
}

func (repository *ambiguousCompletionRepository) Completion(ctx context.Context, id string) (*CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *ambiguousCompletionRepository) ListPendingCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *ambiguousCompletionRepository) ListReadyCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}

func (repository *ambiguousCompletionRepository) UpdateCompletion(ctx context.Context, completion *CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *ambiguousCompletionRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
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
func (repository *failOnAppendRepository) ListReadyCompletions(ctx context.Context, limit int) ([]CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
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

func TestAuditPaginationUsesFilteredCountAndSanitizesStoredMetadata(t *testing.T) {
	repository := NewMemoryRepository()
	base := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	for index := 0; index < 13; index++ {
		action := ActionModelUpdated
		if index%2 != 0 {
			action = ActionTaskChanged
		}
		require.NoError(t, repository.Append(context.Background(), &Event{
			ID: fmt.Sprintf("event-%02d", index), OccurredAt: base.Add(time.Duration(index) * time.Second),
			Action: action, Outcome: OutcomeSuccess,
			Metadata: json.RawMessage(`{"internal_error":"database-sentinel","token":"token-sentinel","safe":"kept"}`),
		}))
	}

	events, total, err := NewService(repository).QueryPage(
		context.Background(), identity.Subject{Role: identity.RoleAuditor}, Filter{Action: ActionModelUpdated}, 2, 3,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(7), total)
	require.Len(t, events, 3)
	encoded, err := json.Marshal(events)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "database-sentinel")
	assert.NotContains(t, string(encoded), "token-sentinel")
	assert.Contains(t, string(encoded), RedactedValue)
	assert.Contains(t, string(encoded), "kept")
}

func TestAuditPaginationSanitizesBrowserMetadataAtArbitraryArrayDepth(t *testing.T) {
	repository := NewMemoryRepository()
	metadata := map[string]any{
		"safe":               "kept",
		"withdrawal_count":   "withdrawal-kept",
		"draw_calls":         "draw-kept",
		"error_count":        "error-count-kept",
		"contention_count":   "contention-kept",
		"dispatch_error":     "dispatch-error-sentinel",
		"raw_output":         "raw-output-sentinel",
		"artifact_path":      "artifact-path-sentinel",
		"request_headers":    "request-headers-sentinel",
		"dispatchError":      "camel-error-sentinel",
		"rawOutput":          "camel-raw-sentinel",
		"artifactPath":       "camel-path-sentinel",
		"requestHeaders":     "camel-headers-sentinel",
		"api_key":            "api-key-sentinel",
		"private-key":        "private-key-sentinel",
		"prefix_error_count": "disguised-error-count-sentinel",
		"nested": []any{[]any{map[string]any{
			"raw_result":  "raw-sentinel",
			"config_path": "path-sentinel",
			"safe_nested": []any{true, float64(42), nil, map[string]any{
				"error":      "error-sentinel",
				"token":      "token-sentinel",
				"password":   "password-sentinel",
				"credential": "credential-sentinel",
				"header":     "header-sentinel",
				"content":    "content-sentinel",
				"label":      "deep-kept",
			}},
		}}},
	}
	encodedMetadata, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NoError(t, repository.Append(context.Background(), &Event{
		ID: "nested-browser-metadata", OccurredAt: time.Now().UTC(), Action: ActionModelUpdated,
		Outcome: OutcomeSuccess, Metadata: encodedMetadata,
	}))

	events, _, err := NewService(repository).QueryPage(
		context.Background(), identity.Subject{Role: identity.RoleAuditor}, Filter{}, 1, 20,
	)
	require.NoError(t, err)
	require.Len(t, events, 1)
	wire, err := json.Marshal(events)
	require.NoError(t, err)
	for _, sentinel := range []string{
		"raw-sentinel", "path-sentinel", "error-sentinel", "token-sentinel", "password-sentinel",
		"credential-sentinel", "header-sentinel", "content-sentinel",
		"dispatch-error-sentinel", "raw-output-sentinel", "artifact-path-sentinel", "request-headers-sentinel",
		"camel-error-sentinel", "camel-raw-sentinel", "camel-path-sentinel", "camel-headers-sentinel",
		"api-key-sentinel", "private-key-sentinel", "disguised-error-count-sentinel",
	} {
		assert.NotContains(t, string(wire), sentinel)
	}
	assert.Contains(t, string(wire), "kept")
	assert.Contains(t, string(wire), "deep-kept")
	for _, safeValue := range []string{"withdrawal-kept", "draw-kept", "error-count-kept", "contention-kept"} {
		assert.Contains(t, string(wire), safeValue)
	}
}

func TestAuditBrowserSensitiveKeyTokensCoverPluralAndCompactForms(t *testing.T) {
	for _, key := range []string{
		"private_keys", "privateKeys", "PRIVATE_KEYS", "privatekeys",
		"api_keys", "apiKeys", "API_KEYS", "apikeys",
		"cookies", "tokens", "secrets", "passwords", "credentials", "headers", "paths", "errors", "contents",
		"request_header", "request_headers", "requestHeader", "requestHeaders", "requestheader", "requestheaders",
		"authorization_header", "authorization_headers", "authorizationHeader", "authorizationHeaders",
		"authorizationheader", "authorizationheaders",
	} {
		t.Run(key, func(t *testing.T) {
			assert.True(t, sensitiveBrowserKey(key))
		})
	}
	for _, key := range []string{"withdrawal_count", "draw_calls", "error_count", "contention_count"} {
		t.Run("safe_"+key, func(t *testing.T) {
			assert.False(t, sensitiveBrowserKey(key))
		})
	}
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

func TestPersistCompletionRetryUsesStableOpaqueIDsAfterUncertainCommit(t *testing.T) {
	ctx := context.Background()
	actor := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	delegate := NewMemoryRepository()
	service := NewService(&ambiguousCompletionRepository{delegate: delegate})
	input := EventInput{
		RequestID: "request-visible-correlation-id",
		Action:    ActionModelCreated, ResourceType: "model", ResourceID: "model-id",
		Outcome: OutcomeSuccess, Metadata: map[string]any{"phase": "succeeded"},
	}

	_, err := service.PersistCompletion(ctx, actor, input)
	require.Error(t, err)
	pending, err := service.PendingCompletions(ctx, actor, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	first := pending[0]
	assert.NotEqual(t, input.RequestID, first.ID)
	assert.NotEqual(t, input.RequestID, first.EventID)

	retriedID, err := service.PersistCompletion(ctx, actor, input)
	require.NoError(t, err)
	assert.Equal(t, first.ID, retriedID)
	pending, err = service.PendingCompletions(ctx, actor, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, first.EventID, pending[0].EventID)

	require.NoError(t, service.DeliverCompletion(ctx, retriedID))
	require.NoError(t, service.DeliverCompletion(ctx, retriedID))
	events, err := delegate.List(ctx, Filter{})
	require.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestMutationRunTreatsUncertainCommitAsSuccessOnlyWhenStableCompletionIsReady(t *testing.T) {
	ctx := context.Background()
	actor := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	repository := NewMemoryRepository()
	recorder := &ambiguousTransactionRecorder{Service: NewService(repository)}
	const requestID = "uncertain-transaction-request"
	mutation, err := BeginMutation(ctx, recorder, actor, EventInput{
		RequestID: requestID, Action: ActionModelCreated, ResourceType: "model", ResourceID: "model-id",
	})
	require.NoError(t, err)
	businessCalls := 0

	err = mutation.Run(ctx, "", map[string]any{"scope": "global"}, func(context.Context) error {
		businessCalls++
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 1, businessCalls)
	assert.Equal(t, 1, recorder.transactions)

	completion, err := repository.Completion(ctx, stableCompletionID(requestID, "outbox"))
	require.NoError(t, err)
	assert.Equal(t, CompletionStateReady, completion.State)
	assert.NotNil(t, completion.DeliveredAt)
	events, err := repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, OutcomePending, events[0].Outcome)
	assert.Equal(t, OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, events[0].RequestID, events[1].RequestID)
}

func TestPreparedCompletionIsVisibleButReconcileDoesNotDeliverIt(t *testing.T) {
	ctx := context.Background()
	actor := identity.Subject{UserID: "admin-id", Role: identity.RoleAdmin}
	repository := NewMemoryRepository()
	service := NewService(repository)
	mutation, err := BeginMutation(ctx, service, actor, EventInput{
		Action: ActionKnowledgeChanged, ResourceType: "fingerprint", ResourceID: "demo",
	})
	require.NoError(t, err)
	require.NoError(t, mutation.Prepare(ctx))

	pending, err := service.PendingCompletions(ctx, actor, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, CompletionStatePrepared, pending[0].State)
	reconciled, err := service.Reconcile(ctx, actor, 10)
	require.NoError(t, err)
	assert.Zero(t, reconciled)
	events, err := repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, OutcomePending, events[0].Outcome)

	require.NoError(t, mutation.Succeeded(ctx, "", nil))
	events, err = repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, OutcomeSuccess, events[1].Outcome)
}

func TestFinalizePreparedRecoversOneCompletionWithoutRepeatingTheBusinessMutation(t *testing.T) {
	ctx := context.Background()
	actor := identity.Subject{UserID: "original-admin", Username: "original", Role: identity.RoleAdmin}
	recoveryAdmin := identity.Subject{UserID: "recovery-admin", Username: "recovery", Role: identity.RoleAdmin}
	repository := NewMemoryRepository()
	service := NewService(repository)
	const requestID = "knowledge-recovery-request"
	mutation, err := BeginMutation(ctx, service, actor, EventInput{
		RequestID: requestID, Action: ActionKnowledgeChanged, ResourceType: "fingerprint", ResourceID: "demo",
		Metadata: map[string]any{"operation": "update"},
	})
	require.NoError(t, err)
	require.NoError(t, mutation.Prepare(ctx))

	completionID, err := service.FinalizePrepared(ctx, recoveryAdmin, requestID, OutcomeSuccess, map[string]any{
		"files_updated": 1,
		"api_token":     "must-be-redacted",
	})
	require.NoError(t, err)
	assert.Equal(t, stableCompletionID(requestID, "outbox"), completionID)
	// A recovery client can safely retry the same result, even if it no longer
	// has the original metadata payload.
	retriedID, err := service.FinalizePrepared(ctx, recoveryAdmin, requestID, OutcomeSuccess, nil)
	require.NoError(t, err)
	assert.Equal(t, completionID, retriedID)

	_, err = service.FinalizePrepared(ctx, recoveryAdmin, requestID, OutcomeFailure, nil)
	assert.ErrorIs(t, err, ErrCompletionConflict)
	_, err = service.FinalizePrepared(ctx, recoveryAdmin, "missing-request", OutcomeSuccess, nil)
	assert.ErrorIs(t, err, ErrCompletionNotFound)
	_, err = service.FinalizePrepared(ctx, recoveryAdmin, requestID, OutcomePending, nil)
	assert.ErrorIs(t, err, ErrInvalidCompletionOutcome)
	for _, subject := range []identity.Subject{{Role: identity.RoleAuditor}, {Role: identity.RoleUser}} {
		_, err = service.FinalizePrepared(ctx, subject, requestID, OutcomeSuccess, nil)
		assert.ErrorIs(t, err, ErrForbidden)
	}

	reconciled, err := service.Reconcile(ctx, recoveryAdmin, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, reconciled)
	events, err := repository.List(ctx, Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, actor.UserID, events[1].ActorUserID, "recovery preserves the original mutation actor")
	assert.Equal(t, requestID, events[1].RequestID)
	assert.NotContains(t, string(events[1].Metadata), "must-be-redacted")
	assert.Contains(t, string(events[1].Metadata), RedactedValue)
}

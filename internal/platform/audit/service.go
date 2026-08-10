package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrForbidden                = errors.New("无权访问审计日志")
	ErrInvalidCompletionOutcome = errors.New("审计完成结果必须是 success 或 failure")
)

const RedactedValue = "[REDACTED]"

var completionIDNamespace = uuid.MustParse("90fb82b0-8c85-4de8-9c9c-b348be4cb9c6")

type Recorder interface {
	Record(context.Context, identity.Subject, EventInput) error
}

// CompletionRecorder persists mutation results before attempting delivery to
// the append-only audit stream. Delivery failures are therefore retryable and
// never need to rewrite a successful business response.
type CompletionRecorder interface {
	Recorder
	PersistCompletion(context.Context, identity.Subject, EventInput) (string, error)
	DeliverCompletion(context.Context, string) error
}

type transactionCoordinator interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

type completionConfirmer interface {
	CompletionReady(context.Context, string) (bool, error)
}

type transactionDBProvider interface {
	TransactionDB() *gorm.DB
}

// Mutation is a durable write-ahead audit ticket. The pending event is stored
// before the governed state change begins, so a later audit outage cannot
// leave an otherwise invisible mutation. Completion events share RequestID and
// can be reconciled if their append temporarily fails.
type Mutation struct {
	recorder         CompletionRecorder
	actor            identity.Subject
	input            EventInput
	completionAction Action
}

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}
}

func BeginMutation(ctx context.Context, recorder Recorder, actor identity.Subject, input EventInput) (*Mutation, error) {
	return beginMutation(ctx, recorder, actor, input, input.Action)
}

func BeginMutationWithCompletionAction(ctx context.Context, recorder Recorder, actor identity.Subject, input EventInput, completionAction Action) (*Mutation, error) {
	return beginMutation(ctx, recorder, actor, input, completionAction)
}

func beginMutation(ctx context.Context, recorder Recorder, actor identity.Subject, input EventInput, completionAction Action) (*Mutation, error) {
	if recorder == nil {
		return nil, errors.New("治理审计服务未配置")
	}
	completionRecorder, ok := recorder.(CompletionRecorder)
	if !ok {
		return nil, errors.New("治理审计服务未配置持久化完成投递")
	}
	if input.RequestID == "" {
		input.RequestID = uuid.NewString()
	}
	input.Outcome = OutcomePending
	input.Metadata = withPhase(input.Metadata, "requested")
	if err := recorder.Record(ctx, actor, input); err != nil {
		return nil, err
	}
	return &Mutation{recorder: completionRecorder, actor: actor, input: input, completionAction: completionAction}, nil
}

func (mutation *Mutation) Succeeded(ctx context.Context, resourceID string, metadata map[string]any) error {
	return mutation.complete(ctx, OutcomeSuccess, "succeeded", resourceID, metadata)
}

func (mutation *Mutation) Failed(ctx context.Context, resourceID string, metadata map[string]any) error {
	return mutation.complete(ctx, OutcomeFailure, "failed", resourceID, metadata)
}

func (mutation *Mutation) Prepare(ctx context.Context) error {
	input := mutation.completionInput(OutcomePending, "prepared", "", mutation.input.Metadata)
	_, err := mutation.recorder.PersistCompletion(ctx, mutation.actor, input)
	return err
}

// RequestID returns the opaque correlation identifier shared by the pending
// audit event and its deterministic completion state.
func (mutation *Mutation) RequestID() string { return mutation.input.RequestID }

// Run prepares a durable intent, applies the business mutation, and promotes
// the same completion row to ready. Database-backed recorders coordinate both
// writes in one transaction; file-backed callers retain the prepared intent
// until they explicitly retry finalization.
func (mutation *Mutation) Run(ctx context.Context, resourceID string, metadata map[string]any, mutate func(context.Context) error) error {
	if mutate == nil {
		return errors.New("治理业务变更不能为空")
	}
	if err := mutation.Prepare(ctx); err != nil {
		return err
	}

	completionInput := mutation.completionInput(OutcomeSuccess, "succeeded", resourceID, metadata)
	completionID := stableCompletionID(completionInput.RequestID, "outbox")
	var businessErr error
	apply := func(transactionContext context.Context) error {
		if err := mutate(transactionContext); err != nil {
			businessErr = err
			return err
		}
		_, err := mutation.recorder.PersistCompletion(transactionContext, mutation.actor, completionInput)
		return err
	}

	var err error
	if coordinator, ok := mutation.recorder.(transactionCoordinator); ok {
		err = coordinator.WithinTransaction(ctx, apply)
	} else {
		err = apply(ctx)
	}
	if businessErr != nil {
		if completionErr := mutation.Failed(ctx, resourceID, metadata); completionErr != nil {
			return fmt.Errorf("%w; 持久化失败审计结果: %v", businessErr, completionErr)
		}
		return businessErr
	}
	if err != nil {
		// A PostgreSQL commit acknowledgement can be lost after the commit has
		// become durable. Confirm the deterministic ready row before deciding
		// whether the governed mutation failed.
		if confirmer, ok := mutation.recorder.(completionConfirmer); ok {
			ready, confirmErr := confirmer.CompletionReady(ctx, completionID)
			if confirmErr == nil && ready {
				_ = mutation.recorder.DeliverCompletion(ctx, completionID)
				return nil
			}
		}
		return err
	}
	_ = mutation.recorder.DeliverCompletion(ctx, completionID)
	return nil
}

func (mutation *Mutation) complete(ctx context.Context, outcome Outcome, phase, resourceID string, metadata map[string]any) error {
	input := mutation.completionInput(outcome, phase, resourceID, metadata)
	completionID, err := mutation.recorder.PersistCompletion(ctx, mutation.actor, input)
	if err != nil {
		return err
	}
	// Once the completion is durable, delivery is best-effort for this request.
	// A failed append remains in the outbox for the explicit Reconcile boundary.
	_ = mutation.recorder.DeliverCompletion(ctx, completionID)
	return nil
}

func (mutation *Mutation) completionInput(outcome Outcome, phase, resourceID string, metadata map[string]any) EventInput {
	input := mutation.input
	input.Action = mutation.completionAction
	input.Outcome = outcome
	if resourceID != "" {
		input.ResourceID = resourceID
	}
	input.Metadata = withPhase(metadata, phase)
	return input
}

func withPhase(metadata map[string]any, phase string) map[string]any {
	copy := make(map[string]any, len(metadata)+1)
	for key, value := range metadata {
		copy[key] = value
	}
	copy["phase"] = phase
	return copy
}

func (service *Service) Record(ctx context.Context, actor identity.Subject, input EventInput) error {
	metadata, err := sanitizedMetadata(input.Metadata)
	if err != nil {
		return err
	}
	outcome := input.Outcome
	if outcome == "" {
		outcome = OutcomeSuccess
	}
	event := &Event{
		ID:            uuid.NewString(),
		OccurredAt:    service.now(),
		ActorUserID:   actor.UserID,
		ActorUsername: actor.Username,
		ActorRole:     string(actor.Role),
		Action:        input.Action,
		ResourceType:  input.ResourceType,
		ResourceID:    input.ResourceID,
		Outcome:       outcome,
		ClientIP:      input.ClientIP,
		RequestID:     input.RequestID,
		Metadata:      metadata,
	}
	return service.repository.Append(ctx, event)
}

// WithinTransaction is the governance unit-of-work boundary. Repositories only
// consume the transaction carried in context and never start or commit one.
func (service *Service) WithinTransaction(ctx context.Context, apply func(context.Context) error) error {
	if apply == nil {
		return errors.New("治理事务操作不能为空")
	}
	provider, ok := service.repository.(transactionDBProvider)
	if !ok || provider.TransactionDB() == nil {
		return apply(ctx)
	}
	return provider.TransactionDB().WithContext(ctx).Transaction(func(transaction *gorm.DB) error {
		return apply(txcontext.WithGorm(ctx, transaction))
	})
}

func (service *Service) CompletionReady(ctx context.Context, completionID string) (bool, error) {
	repository, err := service.completionRepository()
	if err != nil {
		return false, err
	}
	completion, err := repository.Completion(ctx, completionID)
	if err != nil {
		return false, err
	}
	return completion.State == CompletionStateReady, nil
}

// FinalizePrepared promotes a durable mutation intent without re-running the
// governed business operation. It is an explicit administrator recovery
// boundary for file-backed mutations whose final database write was disrupted.
func (service *Service) FinalizePrepared(ctx context.Context, subject identity.Subject, requestID string, outcome Outcome, metadata map[string]any) (string, error) {
	if subject.Role != identity.RoleAdmin {
		return "", ErrForbidden
	}
	if outcome != OutcomeSuccess && outcome != OutcomeFailure {
		return "", ErrInvalidCompletionOutcome
	}
	repository, err := service.completionRepository()
	if err != nil {
		return "", err
	}
	requestID = strings.TrimSpace(requestID)
	completionID := stableCompletionID(requestID, "outbox")
	completion, err := repository.Completion(ctx, completionID)
	if err != nil {
		return "", err
	}
	if completion.RequestID != requestID {
		return "", ErrCompletionNotFound
	}
	if completion.State == CompletionStateReady {
		if completion.Outcome == outcome {
			return completion.ID, nil
		}
		return "", ErrCompletionConflict
	}
	if completion.State != CompletionStatePrepared {
		return "", ErrCompletionConflict
	}

	recoveredMetadata := map[string]any{}
	if len(completion.Metadata) != 0 {
		if err := json.Unmarshal(completion.Metadata, &recoveredMetadata); err != nil {
			return "", err
		}
	}
	for key, value := range metadata {
		recoveredMetadata[key] = value
	}
	phase := "succeeded"
	if outcome == OutcomeFailure {
		phase = "failed"
	}
	recoveredMetadata = withPhase(recoveredMetadata, phase)
	originalActor := identity.Subject{
		UserID: completion.ActorUserID, Username: completion.ActorUsername, Role: identity.Role(completion.ActorRole),
	}
	return service.PersistCompletion(ctx, originalActor, EventInput{
		RequestID: requestID, Action: completion.Action, ResourceType: completion.ResourceType, ResourceID: completion.ResourceID,
		Outcome: outcome, ClientIP: completion.ClientIP, Metadata: recoveredMetadata,
	})
}

func (service *Service) PersistCompletion(ctx context.Context, actor identity.Subject, input EventInput) (string, error) {
	repository, err := service.completionRepository()
	if err != nil {
		return "", err
	}
	metadata, err := sanitizedMetadata(input.Metadata)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(input.RequestID) == "" {
		return "", errors.New("审计完成投递缺少 request_id")
	}
	now := service.now()
	state := CompletionStateReady
	readyAt := &now
	if input.Outcome == OutcomePending {
		state = CompletionStatePrepared
		readyAt = nil
	}
	completion := &CompletionOutbox{
		ID: stableCompletionID(input.RequestID, "outbox"), EventID: stableCompletionID(input.RequestID, "event"), RequestID: input.RequestID,
		ActorUserID: actor.UserID, ActorUsername: actor.Username, ActorRole: string(actor.Role),
		Action: input.Action, ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Outcome: input.Outcome, ClientIP: input.ClientIP, Metadata: metadata, State: state, CreatedAt: now, ReadyAt: readyAt,
	}
	if err := repository.EnqueueCompletion(ctx, completion); err != nil {
		return "", err
	}
	return completion.ID, nil
}

func stableCompletionID(requestID, kind string) string {
	return uuid.NewSHA1(completionIDNamespace, []byte("audit-completion/"+kind+"\x00"+requestID)).String()
}

func (service *Service) DeliverCompletion(ctx context.Context, completionID string) error {
	repository, err := service.completionRepository()
	if err != nil {
		return err
	}
	completion, err := repository.Completion(ctx, completionID)
	if err != nil {
		return err
	}
	if completion.DeliveredAt != nil {
		return nil
	}
	if completion.State != CompletionStateReady {
		return ErrCompletionNotReady
	}
	completion.Attempts++
	exists, err := repository.EventExists(ctx, completion.EventID)
	if err != nil {
		_ = repository.UpdateCompletion(ctx, completion)
		return err
	}
	if !exists {
		event := &Event{
			ID: completion.EventID, OccurredAt: completion.CreatedAt,
			ActorUserID: completion.ActorUserID, ActorUsername: completion.ActorUsername, ActorRole: completion.ActorRole,
			Action: completion.Action, ResourceType: completion.ResourceType, ResourceID: completion.ResourceID,
			Outcome: completion.Outcome, ClientIP: completion.ClientIP, RequestID: completion.RequestID,
			Metadata: append([]byte(nil), completion.Metadata...),
		}
		if err := service.repository.Append(ctx, event); err != nil {
			exists, existsErr := repository.EventExists(ctx, completion.EventID)
			if existsErr != nil || !exists {
				_ = repository.UpdateCompletion(ctx, completion)
				return err
			}
		}
	}
	deliveredAt := service.now()
	completion.DeliveredAt = &deliveredAt
	return repository.UpdateCompletion(ctx, completion)
}

func (service *Service) PendingCompletions(ctx context.Context, subject identity.Subject, limit int) ([]CompletionOutbox, error) {
	if !identity.HasAnyRole(subject, identity.RoleAdmin, identity.RoleAuditor) {
		return nil, ErrForbidden
	}
	repository, err := service.completionRepository()
	if err != nil {
		return nil, err
	}
	return repository.ListPendingCompletions(ctx, limit)
}

func (service *Service) Reconcile(ctx context.Context, subject identity.Subject, limit int) (int, error) {
	if subject.Role != identity.RoleAdmin {
		return 0, ErrForbidden
	}
	repository, err := service.completionRepository()
	if err != nil {
		return 0, err
	}
	pending, err := repository.ListPendingCompletions(ctx, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	var firstErr error
	for index := range pending {
		if pending[index].State != CompletionStateReady {
			continue
		}
		if err := service.DeliverCompletion(ctx, pending[index].ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		delivered++
	}
	return delivered, firstErr
}

func (service *Service) completionRepository() (CompletionRepository, error) {
	repository, ok := service.repository.(CompletionRepository)
	if !ok {
		return nil, errors.New("审计仓库未配置完成投递 outbox")
	}
	return repository, nil
}

func sanitizedMetadata(input map[string]any) (json.RawMessage, error) {
	normalizedMetadata, err := normalizeMetadata(input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(sanitizeMap(normalizedMetadata))
}

func normalizeMetadata(input map[string]any) (map[string]any, error) {
	if input == nil {
		return map[string]any{}, nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	normalized := map[string]any{}
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (service *Service) Query(ctx context.Context, subject identity.Subject, filter Filter) ([]Event, error) {
	if !identity.HasAnyRole(subject, identity.RoleAdmin, identity.RoleAuditor) {
		return nil, ErrForbidden
	}
	return service.repository.List(ctx, filter)
}

func (service *Service) AuthenticationAttempt(ctx context.Context, event identity.AuthenticationEvent) error {
	action := ActionLoginFailure
	outcome := OutcomeFailure
	if event.Success {
		action = ActionLoginSuccess
		outcome = OutcomeSuccess
	}
	actor := event.Subject
	if actor.Username == "" {
		actor.Username = strings.TrimSpace(event.Username)
	}
	return service.Record(ctx, actor, EventInput{
		Action:       action,
		ResourceType: "session",
		ResourceID:   actor.UserID,
		Outcome:      outcome,
		ClientIP:     event.ClientIP,
	})
}

func (service *Service) BeginPasswordReset(ctx context.Context, actor identity.Subject, targetUserID string) (identity.GovernanceCompletion, error) {
	mutation, err := BeginMutation(ctx, service, actor, EventInput{
		Action: ActionPasswordResetRequested, ResourceType: "user", ResourceID: targetUserID,
	})
	if err != nil {
		return nil, err
	}
	return func(completionContext context.Context, success bool) error {
		if success {
			return mutation.Succeeded(completionContext, "", nil)
		}
		return mutation.Failed(completionContext, "", nil)
	}, nil
}

func (service *Service) GovernPasswordReset(ctx context.Context, actor identity.Subject, targetUserID string, apply identity.GovernedMutation) error {
	mutation, err := BeginMutation(ctx, service, actor, EventInput{
		Action: ActionPasswordResetRequested, ResourceType: "user", ResourceID: targetUserID,
	})
	if err != nil {
		return err
	}
	return mutation.Run(ctx, "", nil, func(transactionContext context.Context) error {
		return apply(transactionContext)
	})
}

func sanitizeMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		if sensitiveKey(key) {
			out[key] = RedactedValue
			continue
		}
		out[key] = sanitizeValue(value)
	}
	return out
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return sanitizeMap(typed)
	case []any:
		out := make([]any, len(typed))
		for index := range typed {
			out[index] = sanitizeValue(typed[index])
		}
		return out
	default:
		return value
	}
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{"token", "secret", "apikey", "authorization", "password", "credential"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

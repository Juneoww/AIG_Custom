package audit

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/google/uuid"
)

var ErrForbidden = errors.New("无权访问审计日志")

const RedactedValue = "[REDACTED]"

type Recorder interface {
	Record(context.Context, identity.Subject, EventInput) error
}

// Mutation is a durable write-ahead audit ticket. The pending event is stored
// before the governed state change begins, so a later audit outage cannot
// leave an otherwise invisible mutation. Completion events share RequestID and
// can be reconciled if their append temporarily fails.
type Mutation struct {
	recorder         Recorder
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
	if input.RequestID == "" {
		input.RequestID = uuid.NewString()
	}
	input.Outcome = OutcomePending
	input.Metadata = withPhase(input.Metadata, "requested")
	if err := recorder.Record(ctx, actor, input); err != nil {
		return nil, err
	}
	return &Mutation{recorder: recorder, actor: actor, input: input, completionAction: completionAction}, nil
}

func (mutation *Mutation) Succeeded(ctx context.Context, resourceID string, metadata map[string]any) error {
	return mutation.complete(ctx, OutcomeSuccess, "succeeded", resourceID, metadata)
}

func (mutation *Mutation) Failed(ctx context.Context, resourceID string, metadata map[string]any) error {
	return mutation.complete(ctx, OutcomeFailure, "failed", resourceID, metadata)
}

func (mutation *Mutation) complete(ctx context.Context, outcome Outcome, phase, resourceID string, metadata map[string]any) error {
	input := mutation.input
	input.Action = mutation.completionAction
	input.Outcome = outcome
	if resourceID != "" {
		input.ResourceID = resourceID
	}
	input.Metadata = withPhase(metadata, phase)
	return mutation.recorder.Record(ctx, mutation.actor, input)
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
	normalizedMetadata, err := normalizeMetadata(input.Metadata)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(sanitizeMap(normalizedMetadata))
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

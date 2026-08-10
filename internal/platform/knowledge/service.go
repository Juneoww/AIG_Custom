package knowledge

import (
	"context"
	"errors"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

var (
	ErrForbidden            = errors.New("仅管理员可修改规则与知识库")
	ErrPendingAuditRecovery = errors.New("规则或知识库变更已完成，审计结果待管理员恢复")
)

// PendingAuditRecoveryError is returned only after the file-backed mutation
// has completed but promotion of its already-durable prepared audit intent has
// failed. Its public message omits the storage error and mutation metadata.
type PendingAuditRecoveryError struct {
	RequestID string
	cause     error
}

func (err *PendingAuditRecoveryError) Error() string { return ErrPendingAuditRecovery.Error() }

func (err *PendingAuditRecoveryError) Unwrap() error { return ErrPendingAuditRecovery }

type Kind string

const (
	KindFingerprint      Kind = "fingerprint"
	KindVulnerability    Kind = "vulnerability"
	KindEvaluation       Kind = "evaluation"
	KindMCP              Kind = "mcp"
	KindPromptCollection Kind = "prompt_collection"
	KindAgentConfig      Kind = "agent_config"
	KindSystemData       Kind = "system_data"
)

type Operation string

const (
	OperationCreate Operation = "create"
	OperationUpdate Operation = "update"
	OperationDelete Operation = "delete"
)

type Change struct {
	Kind       Kind
	Operation  Operation
	ResourceID string
}

type AsyncCompletion func(context.Context, bool, map[string]any) error

type Service struct{ audits audit.Recorder }

func NewService(audits audit.Recorder) *Service { return &Service{audits: audits} }

// Apply is the only write boundary for platform knowledge mutations. The
// existing parsers and on-disk formats remain inside mutate and are therefore
// unchanged; only future scans observe the new file content.
func (service *Service) Apply(ctx context.Context, subject identity.Subject, change Change, mutate func() error) error {
	if subject.Role != identity.RoleAdmin {
		return ErrForbidden
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionKnowledgeChanged, ResourceType: string(change.Kind), ResourceID: change.ResourceID,
		Metadata: map[string]any{"operation": change.Operation},
	})
	if err != nil {
		return err
	}
	if err := mutation.Prepare(ctx); err != nil {
		return err
	}
	if err := mutate(); err != nil {
		_ = mutation.Failed(ctx, "", map[string]any{"operation": change.Operation})
		return err
	}
	if err := mutation.Succeeded(ctx, "", map[string]any{"operation": change.Operation}); err != nil {
		return &PendingAuditRecoveryError{RequestID: mutation.RequestID(), cause: err}
	}
	return nil
}

func (service *Service) BeginAsync(ctx context.Context, subject identity.Subject, change Change) (AsyncCompletion, error) {
	if subject.Role != identity.RoleAdmin {
		return nil, ErrForbidden
	}
	mutation, err := audit.BeginMutationWithCompletionAction(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionKnowledgeChangeRequested, ResourceType: string(change.Kind), ResourceID: change.ResourceID,
		Metadata: map[string]any{"operation": change.Operation},
	}, audit.ActionKnowledgeChanged)
	if err != nil {
		return nil, err
	}
	if err := mutation.Prepare(ctx); err != nil {
		return nil, err
	}
	return func(completionContext context.Context, success bool, metadata map[string]any) error {
		metadata = mergeMetadata(metadata, map[string]any{"operation": change.Operation})
		if success {
			return mutation.Succeeded(completionContext, "", metadata)
		}
		return mutation.Failed(completionContext, "", metadata)
	}, nil
}

func mergeMetadata(first, second map[string]any) map[string]any {
	merged := make(map[string]any, len(first)+len(second))
	for key, value := range first {
		merged[key] = value
	}
	for key, value := range second {
		merged[key] = value
	}
	return merged
}

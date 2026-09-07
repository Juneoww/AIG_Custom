package mcpscans

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/google/uuid"
)

const maxRepositoryURLBytes = 8 << 10

var ErrInvalidCreate = errors.New("MCP 扫描创建请求无效")

// SpecializedTaskCreator is deliberately narrower than tasks.Service.Create:
// MCP creation may only reach the governed, no-dispatch write port.
type SpecializedTaskCreator interface {
	CreateSpecializedInUnitOfWork(context.Context, identity.Subject, tasks.SpecializedCreateInput) (*tasks.Task, error)
}

// TaskConnectionLocker keeps current config/version eligibility and row locks
// in the MCP connection domain, rather than copying endpoint material into a
// generic task service.
type TaskConnectionLocker interface {
	LockTaskConnectionForCreate(context.Context, identity.Subject, string, int) (mcpconnections.TaskConnectionReference, error)
}

// TaskBindingRepository owns dedicated MCP task binding persistence. The task
// repository itself never receives a repository URL or connection payload.
type TaskBindingRepository interface {
	CreateTaskBinding(context.Context, *mcpconnections.TaskBinding) error
}

type RepositorySourceSealer interface {
	SealRepositorySource(*mcpconnections.TaskBinding, mcpconnections.BindingEncryptionContext, mcpconnections.RepositorySourceSnapshot) error
}

type RepositorySourcePolicy interface {
	RequireControlledDialer() error
	ValidateGitURL(context.Context, string) error
}

// PostCommitDispatcher can be wired by a later runtime adapter. Its call site
// is intentionally after idempotency.Execute returns, so it never observes an
// uncommitted task or binding.
type PostCommitDispatcher interface {
	DispatchMCPAfterCommit(context.Context, identity.Subject, string) error
}

type CreateUnitOfWorkDependencies struct {
	Models interface {
		Describe(context.Context, string, string) (string, error)
	}
	Idempotency *idempotency.Service
	Audits      audit.Recorder
	Tasks       SpecializedTaskCreator
	Connections TaskConnectionLocker
	Bindings    TaskBindingRepository
	Keyring     RepositorySourceSealer
	Policy      RepositorySourcePolicy
	Dispatcher  PostCommitDispatcher
}

// CreateUnitOfWork coordinates the only MCP task-create write path. It has no
// browser/HTTP dependency and does not expose raw connection material.
type CreateUnitOfWork struct {
	models interface {
		Describe(context.Context, string, string) (string, error)
	}
	idempotency *idempotency.Service
	audits      audit.Recorder
	tasks       SpecializedTaskCreator
	connections TaskConnectionLocker
	bindings    TaskBindingRepository
	keyring     RepositorySourceSealer
	policy      RepositorySourcePolicy
	dispatcher  PostCommitDispatcher
	now         func() time.Time
	newID       func() string
}

func NewCreateUnitOfWork(dependencies CreateUnitOfWorkDependencies) *CreateUnitOfWork {
	return &CreateUnitOfWork{
		models:      dependencies.Models,
		idempotency: dependencies.Idempotency,
		audits:      dependencies.Audits,
		tasks:       dependencies.Tasks,
		connections: dependencies.Connections,
		bindings:    dependencies.Bindings,
		keyring:     dependencies.Keyring,
		policy:      dependencies.Policy,
		dispatcher:  dependencies.Dispatcher,
		now:         func() time.Time { return time.Now().UTC() },
		newID:       uuid.NewString,
	}
}

// Create acquires a private, server-derived idempotency scope before recording
// a durable audit intent. Mutation.Run then owns the sole business transaction
// containing source validation locks, attachment locking/binding, safe task
// persistence, dedicated binding persistence, and the safe replay response.
func (workflow *CreateUnitOfWork) Create(ctx context.Context, subject identity.Subject, input CreateInput) (CreateResult, error) {
	normalized, err := normalizeCreateInput(input)
	if err != nil {
		return CreateResult{}, err
	}
	if workflow == nil || workflow.idempotency == nil ||
		(subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin) || strings.TrimSpace(subject.UserID) == "" {
		return CreateResult{}, ErrInvalidCreate
	}

	payload, err := idempotencyPayload(normalized)
	if err != nil {
		return CreateResult{}, ErrInvalidCreate
	}
	result, err := workflow.idempotency.Execute(ctx, subject, idempotency.Operation{
		Scope: idempotency.ScopePrivate, Method: "POST", Path: CreateOperationPath, Key: normalized.IdempotencyKey, Payload: payload,
	}, func(lockedContext context.Context, claim *idempotency.Claim) error {
		// Everything below is required for a fresh write only. Keeping mutable
		// policy and source eligibility inside the winner callback ensures a
		// previously committed safe response can always replay without being
		// blocked by a later DNS/gateway/configuration change.
		if workflow.audits == nil || workflow.tasks == nil || workflow.bindings == nil {
			return ErrInvalidCreate
		}
		if normalized.ModelID != "" {
			if workflow.models == nil {
				return ErrInvalidCreate
			}
			if _, err := workflow.models.Describe(lockedContext, subject.Username, normalized.ModelID); err != nil {
				return ErrInvalidCreate
			}
		}
		if normalized.SourceKind == SourceKindRepository {
			if workflow.policy == nil || workflow.policy.RequireControlledDialer() != nil {
				return mcpconnections.ErrControlledEgressRequired
			}
			if normalized.RepositoryURL != "" {
				if err := workflow.policy.ValidateGitURL(lockedContext, normalized.RepositoryURL); err != nil {
					return err
				}
			}
			if workflow.keyring == nil {
				return ErrInvalidCreate
			}
		}
		if normalized.SourceKind == SourceKindService && workflow.connections == nil {
			return ErrInvalidCreate
		}
		taskID := workflow.newID()
		if _, parseErr := uuid.Parse(taskID); parseErr != nil || taskID != strings.ToLower(taskID) {
			return ErrInvalidCreate
		}
		metadata := map[string]any{"task_type": "mcp_scan", "source_kind": string(normalized.SourceKind)}
		mutation, beginErr := audit.BeginMutation(lockedContext, workflow.audits, subject, audit.EventInput{
			Action: audit.ActionTaskCreated, ResourceType: "mcp_scan", ResourceID: taskID, Metadata: metadata,
		})
		if beginErr != nil {
			return beginErr
		}
		return mutation.Run(lockedContext, taskID, metadata, func(transactionContext context.Context) error {
			binding, bindingErr := workflow.bindingForCreate(transactionContext, subject, taskID, normalized)
			if bindingErr != nil {
				return bindingErr
			}
			params, paramsErr := safeTaskParams(normalized)
			if paramsErr != nil {
				return ErrInvalidCreate
			}
			created, createErr := workflow.tasks.CreateSpecializedInUnitOfWork(transactionContext, subject, tasks.SpecializedCreateInput{
				TaskID: taskID, Params: params, AttachmentIDs: append([]string(nil), normalized.AttachmentIDs...),
			})
			if createErr != nil {
				return createErr
			}
			if created == nil || created.ID != taskID || created.TaskType != "mcp_scan" || created.Status != tasks.StatusPending {
				return ErrInvalidCreate
			}
			if err := workflow.bindings.CreateTaskBinding(transactionContext, binding); err != nil {
				return err
			}
			return claim.PersistSuccess(transactionContext, 201, idempotency.SafeResponse{TaskID: created.ID, Status: string(created.Status)})
		})
	})
	if err != nil {
		return CreateResult{}, err
	}
	created := CreateResult{TaskID: result.Response.TaskID, Status: tasks.Status(result.Response.Status), Replay: result.Replay}
	if !created.Replay && workflow.dispatcher != nil {
		// 创建事务及幂等成功响应已经提交。调度结果由任务状态机持久化，
		// 不能再返回创建失败并丢掉 task_id，否则客户端换键重试会重复扫描。
		// 此处返回受理快照；客户端通过专属详情读取实际调度状态。
		_ = workflow.dispatcher.DispatchMCPAfterCommit(ctx, subject, created.TaskID)
	}
	return created, nil
}

func (workflow *CreateUnitOfWork) bindingForCreate(
	ctx context.Context,
	subject identity.Subject,
	taskID string,
	input CreateInput,
) (*mcpconnections.TaskBinding, error) {
	binding := &mcpconnections.TaskBinding{
		ID: workflow.newID(), TaskID: taskID, SourceKind: string(input.SourceKind),
		CreatedAt: workflow.now(), UpdatedAt: workflow.now(),
	}
	if _, err := uuid.Parse(binding.ID); err != nil {
		return nil, ErrInvalidCreate
	}
	switch input.SourceKind {
	case SourceKindRepository:
		if err := workflow.keyring.SealRepositorySource(binding, mcpconnections.BindingEncryptionContext{
			OwnerUserID: subject.UserID, Scope: mcpconnections.ScopePrivate, Version: 1,
		}, mcpconnections.RepositorySourceSnapshot{RepositoryURL: input.RepositoryURL}); err != nil {
			return nil, ErrInvalidCreate
		}
	case SourceKindService:
		reference, err := workflow.connections.LockTaskConnectionForCreate(ctx, subject, input.ConnectionConfigID, input.ConnectionConfigVersion)
		if err != nil {
			return nil, err
		}
		if reference.ConnectionConfigID != input.ConnectionConfigID || reference.ConnectionConfigVersion != input.ConnectionConfigVersion {
			return nil, mcpconnections.ErrConflict
		}
		configID, version := reference.ConnectionConfigID, reference.ConnectionConfigVersion
		binding.ConnectionConfigID = &configID
		binding.ConnectionConfigVersion = &version
	default:
		return nil, ErrInvalidCreate
	}
	return binding, nil
}

func normalizeCreateInput(input CreateInput) (CreateInput, error) {
	normalized := input
	normalized.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	normalized.AttachmentIDs = append([]string(nil), input.AttachmentIDs...)
	if input.Thread != nil {
		thread := *input.Thread
		normalized.Thread = &thread
	}
	if input.SourceKind != SourceKind(strings.TrimSpace(string(input.SourceKind))) ||
		input.RepositoryURL != strings.TrimSpace(input.RepositoryURL) ||
		input.ConnectionConfigID != strings.TrimSpace(input.ConnectionConfigID) ||
		input.ModelID != strings.TrimSpace(input.ModelID) ||
		!validOptionalReference(input.ModelID) || !validAttachmentIDs(normalized.AttachmentIDs) {
		return CreateInput{}, ErrInvalidCreate
	}
	if normalized.Thread != nil && (*normalized.Thread < 1 || *normalized.Thread > 32) {
		return CreateInput{}, ErrInvalidCreate
	}
	switch normalized.SourceKind {
	case SourceKindRepository:
		if normalized.AuthorizationConfirmed || normalized.ConnectionConfigID != "" || normalized.ConnectionConfigVersion != 0 {
			return CreateInput{}, ErrInvalidCreate
		}
		if normalized.RepositoryURL == "" {
			if len(normalized.AttachmentIDs) == 0 {
				return CreateInput{}, ErrInvalidCreate
			}
			return normalized, nil
		}
		if len(normalized.RepositoryURL) > maxRepositoryURLBytes || len(normalized.AttachmentIDs) != 0 {
			return CreateInput{}, ErrInvalidCreate
		}
		return normalized, nil
	case SourceKindService:
		if !normalized.AuthorizationConfirmed || normalized.RepositoryURL != "" || len(normalized.AttachmentIDs) != 0 ||
			!validReference(normalized.ConnectionConfigID) || normalized.ConnectionConfigVersion < 1 {
			return CreateInput{}, ErrInvalidCreate
		}
		return normalized, nil
	default:
		return CreateInput{}, ErrInvalidCreate
	}
}

func safeTaskParams(input CreateInput) (json.RawMessage, error) {
	type taskParams struct {
		SourceKind             SourceKind `json:"source_kind"`
		ModelID                string     `json:"model_id,omitempty"`
		Thread                 *int       `json:"thread,omitempty"`
		AuthorizationConfirmed *bool      `json:"authorization_confirmed,omitempty"`
	}
	params := taskParams{SourceKind: input.SourceKind, ModelID: input.ModelID, Thread: input.Thread}
	if input.SourceKind == SourceKindService {
		confirmed := true
		params.AuthorizationConfirmed = &confirmed
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func idempotencyPayload(input CreateInput) (json.RawMessage, error) {
	type payload struct {
		SourceKind              SourceKind `json:"source_kind"`
		RepositoryURL           string     `json:"repository_url,omitempty"`
		AttachmentIDs           []string   `json:"attachment_ids,omitempty"`
		ConnectionConfigID      string     `json:"connection_config_id,omitempty"`
		ConnectionConfigVersion int        `json:"connection_config_version,omitempty"`
		AuthorizationConfirmed  bool       `json:"authorization_confirmed,omitempty"`
		ModelID                 string     `json:"model_id,omitempty"`
		Thread                  *int       `json:"thread,omitempty"`
	}
	encoded, err := json.Marshal(payload{
		SourceKind: input.SourceKind, RepositoryURL: input.RepositoryURL, AttachmentIDs: input.AttachmentIDs,
		ConnectionConfigID: input.ConnectionConfigID, ConnectionConfigVersion: input.ConnectionConfigVersion,
		AuthorizationConfirmed: input.AuthorizationConfirmed, ModelID: input.ModelID, Thread: input.Thread,
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

func validAttachmentIDs(ids []string) bool {
	if len(ids) > tasks.MaxTaskAttachmentCount {
		return false
	}
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !validReference(id) {
			return false
		}
		if _, exists := seen[id]; exists {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

func validOptionalReference(value string) bool {
	return value == "" || validReference(value)
}

func validReference(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= tasks.MaxTaskReferenceLength
}

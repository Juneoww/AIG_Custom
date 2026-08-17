package tasks

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxIdempotencyKeyLength = 128
	MaxDispatchAttempts     = 3
	dispatchLeaseDuration   = 30 * time.Second
)

var (
	ErrForbidden         = errors.New("无权访问任务")
	ErrNotFound          = errors.New("任务不存在")
	ErrInvalid           = errors.New("任务参数无效")
	ErrDispatchFailed    = errors.New("任务分发失败")
	ErrDispatchLeaseLost = errors.New("任务分发租约已失效")
	ErrStatusTransition  = errors.New("任务状态已变更")
)

var taskIDNamespace = uuid.MustParse("87c68739-a2fc-413d-8e22-ed74960bfab9")

const (
	completedResultRecoveryBatchSize = 100
	defaultRecoveryItemTimeout       = 5 * time.Second
)

type Repository interface {
	CreateOrGet(context.Context, *Task) (*Task, bool, error)
	ClaimDispatch(context.Context, string, time.Time, time.Time) (string, bool, error)
	ReserveDispatchAttempt(context.Context, string, string, time.Time) (int, bool, error)
	MarkRunning(context.Context, string, string, string, time.Time) error
	MarkDispatchFailed(context.Context, string, string, string, time.Time) error
	MarkDispatchUnknown(context.Context, string, string, string, time.Time) error
	UpdateStatus(context.Context, string, Status, time.Time) error
	TransitionStatus(context.Context, string, []Status, Status, string, time.Time) (bool, error)
	Get(context.Context, string) (*Task, error)
	GetBrowser(context.Context, string, string) (*Task, error)
	GetByEngineSessionID(context.Context, string) (*Task, error)
	List(context.Context) ([]Task, error)
	ListBrowser(context.Context, TaskListQuery) ([]Task, int64, error)
	ListRecoverable(context.Context, string, int) ([]Task, error)
}

type TaskListQuery struct {
	OwnerUserID string
	Limit       int
	Offset      int
}

type Service struct {
	repository          Repository
	engine              EngineAdapter
	audits              audit.Recorder
	attachments         *AttachmentService
	reportSnapshots     reportSnapshotter
	now                 func() time.Time
	recoveryItemTimeout time.Duration
	recoveryMu          sync.Mutex
	recoveryAfterID     string
}

type reportSnapshotter interface {
	Prepare(context.Context, reports.CompletedTask) (*reports.Snapshot, error)
	Persist(context.Context, *reports.Snapshot) error
}

type engineEventCoordinator interface {
	WithinEngineEventLock(context.Context, string, func(context.Context) error) error
}

func NewService(repository Repository, engine EngineAdapter, audits audit.Recorder) *Service {
	return &Service{repository: repository, engine: engine, audits: audits, now: func() time.Time { return time.Now().UTC() }, recoveryItemTimeout: defaultRecoveryItemTimeout}
}

func (service *Service) SetAttachmentService(attachments *AttachmentService) {
	service.attachments = attachments
}

func (service *Service) SetReportSnapshotService(snapshotter reportSnapshotter) {
	service.reportSnapshots = snapshotter
}

func (service *Service) Create(ctx context.Context, subject identity.Subject, input CreateInput) (View, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.TaskType = strings.TrimSpace(input.TaskType)
	if subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin || subject.UserID == "" ||
		input.IdempotencyKey == "" || len(input.IdempotencyKey) > MaxIdempotencyKeyLength || input.TaskType == "" {
		return View{}, ErrInvalid
	}
	params := input.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	if !json.Valid(params) {
		return View{}, ErrInvalid
	}
	if containsForbiddenTaskParameter(params) {
		return View{}, ErrInvalid
	}
	attachmentRefs, err := json.Marshal(input.AttachmentIDs)
	if err != nil {
		return View{}, ErrInvalid
	}
	if len(input.AttachmentIDs) > 0 {
		if service.attachments == nil {
			return View{}, ErrInvalid
		}
		if _, err := service.attachments.ResolveReady(ctx, subject.UserID, input.AttachmentIDs); err != nil {
			return View{}, err
		}
	}
	now := service.now()
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+input.IdempotencyKey)).String()
	candidate := &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: input.IdempotencyKey, EngineSessionID: taskID, TaskType: input.TaskType,
		Content: input.Content, Params: append(json.RawMessage(nil), params...), AttachmentRefs: attachmentRefs,
		CountryIsoCode: input.CountryIsoCode, Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	}

	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.Action("task.created"), ResourceType: "task", ResourceID: taskID,
	})
	if err != nil {
		return View{}, err
	}
	var persisted *Task
	var repositoryErr error
	err = mutation.Run(ctx, taskID, map[string]any{"task_type": input.TaskType}, func(transactionContext context.Context) error {
		persisted, _, repositoryErr = service.repository.CreateOrGet(transactionContext, candidate)
		return repositoryErr
	})
	if repositoryErr != nil {
		return View{}, repositoryErr
	}
	if err != nil {
		return View{}, err
	}

	claim, claimed, err := service.repository.ClaimDispatch(ctx, persisted.ID, now, now.Add(dispatchLeaseDuration))
	if err != nil {
		return viewOf(persisted), err
	}
	if !claimed {
		current, getErr := service.repository.Get(ctx, persisted.ID)
		if getErr != nil {
			return viewOf(persisted), getErr
		}
		return viewOf(current), nil
	}

	current, err := service.dispatch(ctx, subject, persisted, claim)
	return viewOf(current), err
}

func containsForbiddenTaskParameter(params json.RawMessage) bool {
	var value any
	if json.Unmarshal(params, &value) != nil {
		return true
	}
	forbidden := map[string]struct{}{
		"token": {}, "apikey": {}, "authorization": {}, "secret": {}, "password": {},
		"model": {}, "evalmodel": {},
	}
	var visit func(any) bool
	visit = func(current any) bool {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.TrimSpace(key)))
				if _, blocked := forbidden[normalized]; blocked {
					return true
				}
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range typed {
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(value)
}

func (service *Service) dispatch(ctx context.Context, subject identity.Subject, task *Task, claim string) (*Task, error) {
	engineTask := EngineTask{
		PlatformTaskID: task.ID, OwnerUsername: task.OwnerUsername, TaskType: task.TaskType,
		Content: task.Content, Params: append(json.RawMessage(nil), task.Params...), CountryIsoCode: task.CountryIsoCode,
	}
	var attachmentIDs []string
	_ = json.Unmarshal(task.AttachmentRefs, &attachmentIDs)
	if len(attachmentIDs) > 0 {
		if service.attachments == nil {
			return task, ErrInvalid
		}
		resolved, err := service.attachments.ResolveReady(ctx, task.OwnerUserID, attachmentIDs)
		if err != nil {
			return task, err
		}
		engineTask.Attachments = resolved
	}
	var dispatchErr error
	attempts := task.DispatchAttempts
	for attempts < MaxDispatchAttempts {
		var reserved bool
		var reserveErr error
		attempts, reserved, reserveErr = service.repository.ReserveDispatchAttempt(ctx, task.ID, claim, service.now())
		if reserveErr != nil {
			return task, reserveErr
		}
		if !reserved {
			break
		}
		engineSessionID, err := service.engine.SubmitTask(ctx, engineTask)
		if err == nil {
			if engineSessionID == "" {
				engineSessionID = task.ID
			}
			if markErr := service.repository.MarkRunning(ctx, task.ID, claim, engineSessionID, service.now()); markErr != nil {
				return service.currentAfterLeaseLoss(ctx, task, markErr)
			}
			return service.repository.Get(ctx, task.ID)
		}
		dispatchErr = err
		if errors.Is(err, ErrSubmitAcknowledgementUnknown) {
			status, statusErr := service.engine.GetTaskStatus(ctx, task.ID)
			if statusErr == nil && status.State != "" && status.State != EngineStatePending {
				if markErr := service.repository.MarkRunning(ctx, task.ID, claim, task.ID, service.now()); markErr != nil {
					return service.currentAfterLeaseLoss(ctx, task, markErr)
				}
				return service.repository.Get(ctx, task.ID)
			}
			if markErr := service.repository.MarkDispatchUnknown(ctx, task.ID, claim, "engine acknowledgement unknown", service.now()); markErr != nil {
				return service.currentAfterLeaseLoss(ctx, task, markErr)
			}
			current, getErr := service.repository.Get(ctx, task.ID)
			if getErr != nil {
				return task, getErr
			}
			return current, fmt.Errorf("%w: engine acknowledgement unknown", ErrDispatchFailed)
		}
		if isTransientDispatchError(err) && attempts < MaxDispatchAttempts {
			continue
		}
		break
	}

	safeError := "engine dispatch failed"
	if isTransientDispatchError(dispatchErr) {
		safeError = "engine temporarily unavailable"
	} else if errors.Is(dispatchErr, ErrSubmitAcknowledgementUnknown) {
		safeError = "engine acknowledgement unknown"
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.Action("task.dispatch_failed"), ResourceType: "task", ResourceID: task.ID,
	})
	if err != nil {
		return task, err
	}
	var updateErr error
	err = mutation.Run(ctx, task.ID, map[string]any{"attempts": attempts}, func(transactionContext context.Context) error {
		updateErr = service.repository.MarkDispatchFailed(transactionContext, task.ID, claim, safeError, service.now())
		return updateErr
	})
	if updateErr != nil {
		return task, updateErr
	}
	if err != nil {
		return task, err
	}
	current, getErr := service.repository.Get(ctx, task.ID)
	if getErr != nil {
		return task, getErr
	}
	return current, fmt.Errorf("%w: %s", ErrDispatchFailed, safeError)
}

func (service *Service) currentAfterLeaseLoss(ctx context.Context, fallback *Task, err error) (*Task, error) {
	if !errors.Is(err, ErrDispatchLeaseLost) {
		return fallback, err
	}
	current, getErr := service.repository.Get(ctx, fallback.ID)
	if getErr != nil {
		return fallback, getErr
	}
	return current, ErrDispatchLeaseLost
}

func (service *Service) Get(ctx context.Context, subject identity.Subject, id string) (View, error) {
	task, err := service.repository.Get(ctx, id)
	if err != nil {
		return View{}, err
	}
	if !canRead(subject, task) {
		return View{}, ErrForbidden
	}
	return viewOf(task), nil
}

func (service *Service) BrowserGet(ctx context.Context, subject identity.Subject, id string) (TaskDetail, error) {
	query, err := taskListQueryFor(subject)
	if err != nil {
		return TaskDetail{}, err
	}
	task, err := service.repository.GetBrowser(ctx, id, query.OwnerUserID)
	if err != nil {
		return TaskDetail{}, err
	}
	return taskDetailOf(task), nil
}

func (service *Service) Browse(ctx context.Context, subject identity.Subject, page, pageSize int) (TaskListResponse, error) {
	query, err := taskListQueryFor(subject)
	if err != nil {
		return TaskListResponse{}, err
	}
	if page < 1 || page > maxTaskPage || pageSize < 1 || pageSize > maxTaskPageSize || page-1 > int(^uint(0)>>1)/pageSize {
		return TaskListResponse{}, ErrInvalid
	}
	query.Limit = pageSize
	query.Offset = (page - 1) * pageSize
	tasks, total, err := service.repository.ListBrowser(ctx, query)
	if err != nil {
		return TaskListResponse{}, err
	}
	items := make([]TaskSummary, 0, len(tasks))
	for index := range tasks {
		items = append(items, taskSummaryOf(&tasks[index]))
	}
	return TaskListResponse{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (service *Service) List(ctx context.Context, subject identity.Subject) ([]View, error) {
	query, err := taskListQueryFor(subject)
	if err != nil {
		return nil, err
	}
	tasks, _, err := service.repository.ListBrowser(ctx, query)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(tasks))
	for index := range tasks {
		views = append(views, viewOf(&tasks[index]))
	}
	return views, nil
}

func taskListQueryFor(subject identity.Subject) (TaskListQuery, error) {
	switch subject.Role {
	case identity.RoleUser:
		if subject.UserID == "" {
			return TaskListQuery{}, ErrForbidden
		}
		return TaskListQuery{OwnerUserID: subject.UserID}, nil
	case identity.RoleAuditor, identity.RoleAdmin:
		return TaskListQuery{}, nil
	default:
		return TaskListQuery{}, ErrForbidden
	}
}

func (service *Service) Result(ctx context.Context, subject identity.Subject, id string) (json.RawMessage, error) {
	task, err := service.repository.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !canRead(subject, task) {
		return nil, ErrForbidden
	}
	return service.engine.GetResult(ctx, task.EngineSessionID)
}

func (service *Service) refresh(ctx context.Context, subject identity.Subject, task *Task) (*Task, error) {
	if task.Status != StatusRunning {
		return task, nil
	}
	status, err := service.engine.GetTaskStatus(ctx, task.EngineSessionID)
	if errors.Is(err, ErrEngineTaskNotFound) {
		return task, nil
	}
	if err != nil {
		return nil, err
	}
	mapped := task.Status
	switch status.State {
	case EngineStateSucceeded:
		mapped = StatusSucceeded
	case EngineStateFailed:
		mapped = StatusEngineFailed
	case EngineStateCancelled:
		mapped = StatusCancelled
	case EngineStatePending, EngineStateRunning:
		return task, nil
	default:
		return nil, errors.New("engine returned an invalid task status")
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionTaskChanged, ResourceType: "task", ResourceID: task.ID,
	})
	if err != nil {
		return nil, err
	}
	var updateErr error
	err = mutation.Run(ctx, task.ID, map[string]any{"status": mapped}, func(transactionContext context.Context) error {
		var transitioned bool
		transitioned, updateErr = service.repository.TransitionStatus(transactionContext, task.ID, []Status{task.Status}, mapped, "", service.now())
		if updateErr == nil && !transitioned {
			updateErr = ErrStatusTransition
		}
		return updateErr
	})
	if updateErr != nil {
		return nil, updateErr
	}
	if err != nil {
		return nil, err
	}
	return service.repository.Get(ctx, task.ID)
}

func (service *Service) Cancel(ctx context.Context, subject identity.Subject, id string) error {
	task, err := service.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if !canWrite(subject, task) {
		return ErrForbidden
	}
	if task.Status == StatusCancelled || task.Status == StatusSucceeded || task.Status == StatusEngineFailed {
		return nil
	}
	mutation, err := audit.BeginMutationWithCompletionAction(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionTaskCancelRequested, ResourceType: "task", ResourceID: task.ID,
		Metadata: map[string]any{"requested_action": "cancel"},
	}, audit.ActionTaskChanged)
	if err != nil {
		return err
	}
	targetStatus := StatusCancelled
	if shouldCancelEngine(task.Status) {
		if cancelErr := service.engine.CancelTask(ctx, task.EngineSessionID); cancelErr != nil {
			if auditErr := mutation.Failed(ctx, task.ID, map[string]any{"requested_action": "cancel"}); auditErr != nil {
				return fmt.Errorf("%w; persist cancellation failure audit: %v", cancelErr, auditErr)
			}
			return cancelErr
		}
		// CancelTask is an idempotent engine CAS. Its trusted readback distinguishes
		// cancellation from a terminal Agent event that won the same race. If the
		// post-CAS readback itself is unavailable, the accepted cancellation remains
		// the conservative durable outcome.
		if engineStatus, statusErr := service.engine.GetTaskStatus(ctx, task.EngineSessionID); statusErr == nil {
			switch engineStatus.State {
			case EngineStateSucceeded:
				targetStatus = StatusSucceeded
			case EngineStateFailed:
				targetStatus = StatusEngineFailed
			case EngineStateCancelled:
				targetStatus = StatusCancelled
			}
		}
	}
	var businessErr error
	err = mutation.Run(ctx, task.ID, map[string]any{"requested_action": "cancel", "status": targetStatus}, func(transactionContext context.Context) error {
		var transitioned bool
		transitioned, businessErr = service.repository.TransitionStatus(transactionContext, task.ID, []Status{task.Status}, targetStatus, "", service.now())
		if businessErr == nil && !transitioned {
			businessErr = ErrStatusTransition
		}
		return businessErr
	})
	if businessErr != nil {
		if errors.Is(businessErr, ErrStatusTransition) {
			current, getErr := service.repository.Get(ctx, task.ID)
			if getErr != nil {
				return getErr
			}
			if isTerminalStatus(current.Status) {
				return nil
			}
		}
		return businessErr
	}
	return err
}

func shouldCancelEngine(status Status) bool {
	return status == StatusRunning || status == StatusDispatching || status == StatusDispatchUnknown
}

func isTerminalStatus(status Status) bool {
	return status == StatusCancelled || status == StatusSucceeded || status == StatusEngineFailed
}

// RecordEngineEvent is the trusted internal reconciliation boundary used by
// authenticated Agent connections. Browser GET handlers never invoke it.
func (service *Service) RecordEngineEvent(ctx context.Context, engineSessionID string, state EngineState, reason string) error {
	task, err := service.repository.GetByEngineSessionID(ctx, engineSessionID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	to := task.Status
	sanitizedReason := ""
	switch state {
	case EngineStateRunning:
		to = StatusRunning
	case EngineStateSucceeded:
		to = StatusSucceeded
	case EngineStateFailed:
		to = StatusEngineFailed
		sanitizedReason = sanitizeEngineFailureReason(reason)
	case EngineStateCancelled:
		to = StatusCancelled
	default:
		return nil
	}
	if isTerminalStatus(task.Status) {
		return nil
	}
	var snapshot *reports.Snapshot
	if state == EngineStateSucceeded && service.reportSnapshots != nil {
		engineStatus, statusErr := service.engine.GetTaskStatus(ctx, task.EngineSessionID)
		if statusErr != nil || engineStatus.State != EngineStateSucceeded || engineStatus.CompletedAt.IsZero() {
			return errors.New("无法确认任务完成时间")
		}
		completedAt := engineStatus.CompletedAt.UTC()
		rawResult, resultErr := service.engine.GetResult(ctx, task.EngineSessionID)
		if resultErr != nil {
			return errors.New("无法生成任务报告快照")
		}
		snapshot, resultErr = service.reportSnapshots.Prepare(ctx, reports.CompletedTask{
			TaskID: task.ID, OwnerUserID: task.OwnerUserID, TaskType: task.TaskType,
			RawResult: rawResult, CompletedAt: completedAt,
		})
		if resultErr != nil {
			return errors.New("无法生成任务报告快照")
		}
	}
	apply := func(lockContext context.Context) error {
		current, currentErr := service.repository.GetByEngineSessionID(lockContext, engineSessionID)
		if errors.Is(currentErr, ErrNotFound) || currentErr == nil && isTerminalStatus(current.Status) {
			return nil
		}
		if currentErr != nil {
			return currentErr
		}
		return service.recordEngineEventMutation(lockContext, current, to, sanitizedReason, snapshot)
	}
	if coordinator, ok := service.repository.(engineEventCoordinator); ok {
		return coordinator.WithinEngineEventLock(ctx, engineSessionID, apply)
	}
	return apply(ctx)
}

func (service *Service) recordEngineEventMutation(ctx context.Context, task *Task, to Status, sanitizedReason string, snapshot *reports.Snapshot) error {
	system := identity.Subject{UserID: "system", Username: "internal-agent", Role: identity.RoleAdmin}
	mutation, err := audit.BeginMutation(ctx, service.audits, system, audit.EventInput{
		Action: audit.ActionTaskChanged, ResourceType: "task", ResourceID: task.ID,
	})
	if err != nil {
		return err
	}
	var transitionErr error
	err = mutation.Run(ctx, task.ID, map[string]any{"status": to, "reason": sanitizedReason}, func(transactionContext context.Context) error {
		if snapshot != nil {
			if persistErr := service.reportSnapshots.Persist(transactionContext, snapshot); persistErr != nil {
				if errors.Is(persistErr, reports.ErrSnapshotExists) {
					// Snapshot uniqueness is keyed by this task ID, so a conflict here
					// means another trusted reconciler already persisted this task's
					// immutable snapshot. Continue the same task-status CAS.
				} else {
					transitionErr = errors.New("无法保存任务报告快照")
					return transitionErr
				}
			}
		}
		var transitioned bool
		transitioned, transitionErr = service.repository.TransitionStatus(
			transactionContext, task.ID,
			[]Status{StatusPending, StatusDispatching, StatusDispatchUnknown, StatusRunning},
			to, sanitizedReason, service.now(),
		)
		if transitionErr == nil && !transitioned {
			transitionErr = ErrStatusTransition
		}
		return transitionErr
	})
	if transitionErr != nil {
		if errors.Is(transitionErr, ErrStatusTransition) {
			return nil
		}
		return transitionErr
	}
	return err
}

// ReconcileCompletedEngineResults closes the gap between durable engine completion
// and the platform task/audit transaction. It only trusts the engine adapter's
// persisted status and result; callers never provide result data to this method.
func (service *Service) ReconcileCompletedEngineResults(ctx context.Context) error {
	service.recoveryMu.Lock()
	defer service.recoveryMu.Unlock()
	failed := false
	tasks, err := service.repository.ListRecoverable(ctx, service.recoveryAfterID, completedResultRecoveryBatchSize)
	if err != nil {
		return err
	}
	if len(tasks) == 0 && service.recoveryAfterID != "" {
		service.recoveryAfterID = ""
		tasks, err = service.repository.ListRecoverable(ctx, "", completedResultRecoveryBatchSize)
		if err != nil {
			return err
		}
	}
	for _, task := range tasks {
		itemContext, cancel := context.WithTimeout(ctx, service.recoveryItemTimeout)
		status, statusErr := service.engine.GetTaskStatus(itemContext, task.EngineSessionID)
		if statusErr == nil && status.State == EngineStateSucceeded {
			statusErr = service.RecordEngineEvent(itemContext, task.EngineSessionID, EngineStateSucceeded, "")
		}
		cancel()
		if statusErr != nil {
			failed = true
		}
	}
	if len(tasks) == completedResultRecoveryBatchSize {
		service.recoveryAfterID = tasks[len(tasks)-1].ID
	} else {
		service.recoveryAfterID = ""
	}
	if failed {
		return errors.New("部分任务结果恢复失败")
	}
	return nil
}

// GetCompletedTask is the trusted source used only by report backfill. It
// deliberately accepts a platform task ID rather than browser-supplied result data.
func (service *Service) GetCompletedTask(ctx context.Context, taskID string) (reports.CompletedTask, error) {
	task, err := service.repository.Get(ctx, taskID)
	if err != nil {
		return reports.CompletedTask{}, err
	}
	if task.Status != StatusSucceeded {
		return reports.CompletedTask{}, ErrInvalid
	}
	engineStatus, err := service.engine.GetTaskStatus(ctx, task.EngineSessionID)
	if err != nil || engineStatus.State != EngineStateSucceeded || engineStatus.CompletedAt.IsZero() {
		return reports.CompletedTask{}, errors.New("无法确认任务完成时间")
	}
	rawResult, err := service.engine.GetResult(ctx, task.EngineSessionID)
	if err != nil {
		return reports.CompletedTask{}, errors.New("无法读取任务结果")
	}
	return reports.CompletedTask{TaskID: task.ID, OwnerUserID: task.OwnerUserID, TaskType: task.TaskType,
		RawResult: append(json.RawMessage(nil), rawResult...), CompletedAt: engineStatus.CompletedAt.UTC()}, nil
}

func sanitizeEngineFailureReason(reason string) string {
	if reason == "agent connection lost" {
		return reason
	}
	return "agent reported task failure"
}

func canRead(subject identity.Subject, task *Task) bool {
	return subject.Role == identity.RoleAdmin || subject.Role == identity.RoleAuditor ||
		subject.Role == identity.RoleUser && subject.UserID != "" && subject.UserID == task.OwnerUserID
}

func canWrite(subject identity.Subject, task *Task) bool {
	return subject.Role == identity.RoleAdmin ||
		subject.Role == identity.RoleUser && subject.UserID != "" && subject.UserID == task.OwnerUserID
}

type MemoryRepository struct {
	mu          sync.Mutex
	tasks       map[string]*Task
	byOwner     map[string]string
	attachments map[string]*Attachment
}

type GormRepository struct{ db *gorm.DB }

func NewGormRepository(db *gorm.DB) *GormRepository { return &GormRepository{db: db} }

func (repository *GormRepository) Init() error {
	if repository == nil || repository.db == nil {
		return errors.New("任务数据库不能为空")
	}
	if !repository.db.Migrator().HasTable(&Task{}) || !repository.db.Migrator().HasTable(&Attachment{}) {
		return errors.New("任务数据库尚未迁移，请先运行 aig migrate")
	}
	return nil
}

func (repository *GormRepository) CreateOrGet(ctx context.Context, task *Task) (*Task, bool, error) {
	db := txcontext.Gorm(ctx, repository.db)
	result := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "owner_user_id"}, {Name: "idempotency_key"}},
		DoNothing: true,
	}).Create(task)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return cloneTask(task), true, nil
	}
	var existing Task
	if err := db.Where("owner_user_id = ? AND idempotency_key = ?", task.OwnerUserID, task.IdempotencyKey).First(&existing).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func (repository *GormRepository) ClaimDispatch(ctx context.Context, id string, now, leaseUntil time.Time) (string, bool, error) {
	claim := uuid.NewString()
	result := txcontext.Gorm(ctx, repository.db).Model(&Task{}).
		Where("id = ? AND dispatch_attempts < ? AND (status = ? OR (status = ? AND dispatch_lease_until <= ?))", id, MaxDispatchAttempts, StatusPending, StatusDispatching, now).
		Updates(map[string]any{"status": StatusDispatching, "dispatch_claim_token": claim, "dispatch_lease_until": leaseUntil, "updated_at": now})
	return claim, result.RowsAffected == 1, result.Error
}

func (repository *GormRepository) ReserveDispatchAttempt(ctx context.Context, id, claim string, now time.Time) (int, bool, error) {
	db := txcontext.Gorm(ctx, repository.db)
	result := db.Model(&Task{}).Where("id = ? AND status = ? AND dispatch_claim_token = ? AND dispatch_attempts < ?", id, StatusDispatching, claim, MaxDispatchAttempts).
		Updates(map[string]any{"dispatch_attempts": gorm.Expr("dispatch_attempts + 1"), "updated_at": now})
	if result.Error != nil {
		return 0, false, result.Error
	}
	var task Task
	if err := db.Where("id = ?", id).First(&task).Error; err != nil {
		return 0, false, err
	}
	return task.DispatchAttempts, result.RowsAffected == 1, nil
}

func (repository *GormRepository) MarkRunning(ctx context.Context, id, claim, engineID string, now time.Time) error {
	result := txcontext.Gorm(ctx, repository.db).Model(&Task{}).Where("id = ? AND status = ? AND dispatch_claim_token = ?", id, StatusDispatching, claim).Updates(map[string]any{
		"status": StatusRunning, "engine_session_id": engineID,
		"dispatch_error": "", "dispatch_claim_token": "", "dispatch_lease_until": nil, "updated_at": now,
	})
	return dispatchClaimError(result)
}

func (repository *GormRepository) MarkDispatchFailed(ctx context.Context, id, claim, message string, now time.Time) error {
	result := txcontext.Gorm(ctx, repository.db).Model(&Task{}).Where("id = ? AND status = ? AND dispatch_claim_token = ?", id, StatusDispatching, claim).Updates(map[string]any{
		"status": StatusDispatchFailed, "dispatch_error": message, "dispatch_claim_token": "", "dispatch_lease_until": nil, "updated_at": now,
	})
	return dispatchClaimError(result)
}

func (repository *GormRepository) MarkDispatchUnknown(ctx context.Context, id, claim, message string, now time.Time) error {
	result := txcontext.Gorm(ctx, repository.db).Model(&Task{}).Where("id = ? AND status = ? AND dispatch_claim_token = ?", id, StatusDispatching, claim).Updates(map[string]any{
		"status": StatusDispatchUnknown, "dispatch_error": message, "dispatch_claim_token": "", "dispatch_lease_until": nil, "updated_at": now,
	})
	return dispatchClaimError(result)
}

func dispatchClaimError(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDispatchLeaseLost
	}
	return nil
}

func (repository *GormRepository) UpdateStatus(ctx context.Context, id string, status Status, now time.Time) error {
	result := txcontext.Gorm(ctx, repository.db).Model(&Task{}).Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": now})
	return affectedTaskError(result)
}

func (repository *GormRepository) TransitionStatus(ctx context.Context, id string, from []Status, status Status, reason string, now time.Time) (bool, error) {
	result := txcontext.Gorm(ctx, repository.db).Model(&Task{}).
		Where("id = ? AND status IN ?", id, from).
		Updates(map[string]any{"status": status, "dispatch_error": reason, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}

// WithinEngineEventLock serializes one trusted engine session across service
// instances. All work uses one physical connection so the session lock does
// not consume an extra pool slot; audit.Service still owns the business
// transaction opened on that connection.
func (repository *GormRepository) WithinEngineEventLock(ctx context.Context, engineSessionID string, apply func(context.Context) error) (resultErr error) {
	if repository == nil || repository.db == nil || apply == nil {
		return ErrInvalid
	}
	database, err := repository.db.DB()
	if err != nil {
		return err
	}
	connection, err := database.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	lockKey := "platform-task-engine-event:" + engineSessionID
	if _, err = connection.ExecContext(ctx, "SELECT pg_advisory_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return err
	}
	defer func() {
		unlockContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultRecoveryItemTimeout)
		defer cancel()
		var unlocked bool
		unlockErr := connection.QueryRowContext(unlockContext,
			"SELECT pg_advisory_unlock(hashtextextended($1, 0))", lockKey,
		).Scan(&unlocked)
		if unlockErr == nil && unlocked {
			return
		}
		// Never return a physical connection with an unknown session-lock state
		// to the pool. database/sql discards it when Raw returns ErrBadConn.
		_ = connection.Raw(func(any) error { return driver.ErrBadConn })
		if resultErr == nil {
			resultErr = errors.New("释放任务事件锁失败")
		}
	}()
	lockedDB := repository.db.Session(&gorm.Session{Context: ctx, NewDB: true})
	lockedDB.Statement.ConnPool = connection
	return apply(txcontext.WithGorm(ctx, lockedDB))
}

func (repository *GormRepository) Get(ctx context.Context, id string) (*Task, error) {
	var task Task
	if err := txcontext.Gorm(ctx, repository.db).Where("id = ?", id).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &task, nil
}

func (repository *GormRepository) GetBrowser(ctx context.Context, id, ownerUserID string) (*Task, error) {
	query := txcontext.Gorm(ctx, repository.db).Where("id = ?", id)
	if ownerUserID != "" {
		query = query.Where("owner_user_id = ?", ownerUserID)
	}
	var task Task
	if err := query.First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &task, nil
}

func (repository *GormRepository) GetByEngineSessionID(ctx context.Context, engineSessionID string) (*Task, error) {
	var task Task
	if err := txcontext.Gorm(ctx, repository.db).Where("engine_session_id = ?", engineSessionID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &task, nil
}

func (repository *GormRepository) List(ctx context.Context) ([]Task, error) {
	var tasks []Task
	err := txcontext.Gorm(ctx, repository.db).Order("created_at ASC, id ASC").Find(&tasks).Error
	return tasks, err
}

func (repository *GormRepository) ListBrowser(ctx context.Context, query TaskListQuery) ([]Task, int64, error) {
	db := txcontext.Gorm(ctx, repository.db).Model(&Task{})
	if query.OwnerUserID != "" {
		db = db.Where("owner_user_id = ?", query.OwnerUserID)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	db = db.Select("id", "owner_username", "task_type", "status", "created_at", "updated_at").
		Order("created_at DESC, id DESC")
	if query.Offset > 0 {
		db = db.Offset(query.Offset)
	}
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}
	var tasks []Task
	if err := db.Find(&tasks).Error; err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func (repository *GormRepository) ListRecoverable(ctx context.Context, afterID string, limit int) ([]Task, error) {
	query := txcontext.Gorm(ctx, repository.db).
		Where("status NOT IN ? AND engine_session_id <> ''", []Status{StatusCancelled, StatusSucceeded, StatusEngineFailed}).
		Order("id ASC")
	if afterID != "" {
		query = query.Where("id > ?", afterID)
	}
	if limit <= 0 || limit > completedResultRecoveryBatchSize {
		limit = completedResultRecoveryBatchSize
	}
	var tasks []Task
	err := query.Limit(limit).Find(&tasks).Error
	return tasks, err
}

func (repository *GormRepository) CreateAttachment(ctx context.Context, attachment *Attachment) error {
	return txcontext.Gorm(ctx, repository.db).Create(attachment).Error
}

func (repository *GormRepository) GetAttachment(ctx context.Context, id string) (*Attachment, error) {
	var attachment Attachment
	if err := txcontext.Gorm(ctx, repository.db).Where("id = ?", id).First(&attachment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &attachment, nil
}

func (repository *GormRepository) AddAttachmentChunkBytes(ctx context.Context, id, owner string, delta, limit int64, now time.Time) error {
	result := txcontext.Gorm(ctx, repository.db).Model(&Attachment{}).
		Where("id = ? AND owner_user_id = ? AND state = ? AND chunk_bytes + ? <= ?", id, owner, AttachmentStateUploading, delta, limit).
		Updates(map[string]any{"chunk_bytes": gorm.Expr("chunk_bytes + ?", delta), "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAttachmentTooLarge
	}
	return nil
}

func (repository *GormRepository) MarkAttachmentReady(ctx context.Context, id, owner string, size int64, now time.Time) error {
	result := txcontext.Gorm(ctx, repository.db).Model(&Attachment{}).
		Where("id = ? AND owner_user_id = ? AND state = ?", id, owner, AttachmentStateUploading).
		Updates(map[string]any{"state": AttachmentStateReady, "size": size, "updated_at": now})
	return affectedTaskError(result)
}

func affectedTaskError(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

var (
	ErrAttachmentTooLarge     = errors.New("附件超过大小限制")
	ErrAttachmentSizeMismatch = errors.New("附件大小不匹配")
	ErrAttachmentNotReady     = errors.New("附件尚未就绪")
)

const (
	defaultMaxFileBytes  int64 = 50 << 20
	defaultMaxChunkBytes int64 = 5 << 20
)

type AttachmentConfig struct {
	UploadDir     string
	MaxFileBytes  int64
	MaxChunkBytes int64
}

func LoadAttachmentConfigFromEnv(uploadDir string) (AttachmentConfig, error) {
	config := AttachmentConfig{UploadDir: uploadDir, MaxFileBytes: defaultMaxFileBytes, MaxChunkBytes: defaultMaxChunkBytes}
	for name, destination := range map[string]*int64{
		"AIG_MAX_UPLOAD_BYTES": &config.MaxFileBytes,
		"AIG_MAX_CHUNK_BYTES":  &config.MaxChunkBytes,
	} {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed <= 0 {
			return AttachmentConfig{}, fmt.Errorf("%s must be a positive integer", name)
		}
		*destination = parsed
	}
	if config.MaxChunkBytes > config.MaxFileBytes {
		return AttachmentConfig{}, errors.New("AIG_MAX_CHUNK_BYTES must not exceed AIG_MAX_UPLOAD_BYTES")
	}
	return config, nil
}

type AttachmentView struct {
	ID        string          `json:"id"`
	Filename  string          `json:"filename"`
	Size      int64           `json:"size"`
	State     AttachmentState `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
}

type AttachmentRepository interface {
	CreateAttachment(context.Context, *Attachment) error
	GetAttachment(context.Context, string) (*Attachment, error)
	AddAttachmentChunkBytes(context.Context, string, string, int64, int64, time.Time) error
	MarkAttachmentReady(context.Context, string, string, int64, time.Time) error
}

type PlatformTaskAttachmentRepository interface {
	Repository
	AttachmentRepository
}

type AttachmentService struct {
	repository AttachmentRepository
	tasks      Repository
	audits     audit.Recorder
	config     AttachmentConfig
	now        func() time.Time
}

func NewAttachmentService(repository PlatformTaskAttachmentRepository, config AttachmentConfig, audits audit.Recorder) (*AttachmentService, error) {
	if repository == nil || audits == nil || strings.TrimSpace(config.UploadDir) == "" || config.MaxFileBytes <= 0 ||
		config.MaxChunkBytes <= 0 || config.MaxChunkBytes > config.MaxFileBytes {
		return nil, ErrInvalid
	}
	abs, err := filepath.Abs(config.UploadDir)
	if err != nil {
		return nil, ErrInvalid
	}
	config.UploadDir = filepath.Clean(abs)
	if err := os.MkdirAll(config.UploadDir, 0700); err != nil {
		return nil, errors.New("无法初始化附件目录")
	}
	return &AttachmentService{repository: repository, tasks: repository, audits: audits, config: config, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (service *AttachmentService) MaxFileBytes() int64 { return service.config.MaxFileBytes }

func failAttachmentMutation(ctx context.Context, mutation *audit.Mutation, resourceID string, businessErr error) error {
	if completionErr := mutation.Failed(ctx, resourceID, nil); completionErr != nil {
		return fmt.Errorf("%w; 持久化附件失败审计结果: %v", businessErr, completionErr)
	}
	return businessErr
}

func (service *AttachmentService) Upload(ctx context.Context, subject identity.Subject, filename string, source io.Reader) (AttachmentView, error) {
	if !attachmentCreator(subject) || source == nil || !validAttachmentFilename(filename) {
		return AttachmentView{}, ErrInvalid
	}
	id := uuid.NewString()
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionAttachmentCreated, ResourceType: "attachment", ResourceID: id,
	})
	if err != nil {
		return AttachmentView{}, err
	}
	storageName := id + strings.ToLower(filepath.Ext(filename))
	path, err := service.storagePath(storageName)
	if err != nil {
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, id, err)
	}
	temporary := path + ".uploading"
	written, err := copyBoundedFile(temporary, source, service.config.MaxFileBytes)
	if err != nil {
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, id, err)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, id, errors.New("无法保存附件"))
	}
	now := service.now()
	attachment := &Attachment{
		ID: id, OwnerUserID: subject.UserID, OriginalName: filename, StorageName: storageName,
		Size: written, ChunkBytes: written, State: AttachmentStateReady, CreatedAt: now, UpdatedAt: now,
	}
	var createErr error
	err = mutation.Run(ctx, id, map[string]any{"size": written}, func(transactionContext context.Context) error {
		createErr = service.repository.CreateAttachment(transactionContext, attachment)
		return createErr
	})
	if createErr != nil {
		_ = os.Remove(path)
		return AttachmentView{}, createErr
	}
	if err != nil {
		_ = os.Remove(path)
		return AttachmentView{}, err
	}
	return attachmentView(attachment), nil
}

func (service *AttachmentService) UploadForEngineSession(ctx context.Context, engineSessionID, filename string, source io.Reader) (AttachmentView, error) {
	task, err := service.tasks.GetByEngineSessionID(ctx, engineSessionID)
	if err != nil {
		return AttachmentView{}, err
	}
	if task.EngineSessionID != engineSessionID || task.Status != StatusRunning {
		return AttachmentView{}, ErrForbidden
	}
	owner := identity.Subject{UserID: task.OwnerUserID, Username: task.OwnerUsername, Role: identity.RoleUser}
	return service.Upload(ctx, owner, filename, source)
}

func (service *AttachmentService) BeginChunked(ctx context.Context, subject identity.Subject, filename string, size int64) (AttachmentView, error) {
	if !attachmentCreator(subject) || !validAttachmentFilename(filename) || size <= 0 {
		return AttachmentView{}, ErrInvalid
	}
	if size > service.config.MaxFileBytes {
		return AttachmentView{}, ErrAttachmentTooLarge
	}
	now := service.now()
	id := uuid.NewString()
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionAttachmentCreated, ResourceType: "attachment", ResourceID: id,
	})
	if err != nil {
		return AttachmentView{}, err
	}
	attachment := &Attachment{
		ID: id, OwnerUserID: subject.UserID, OriginalName: filename,
		StorageName: id + strings.ToLower(filepath.Ext(filename)), Size: size,
		State: AttachmentStateUploading, CreatedAt: now, UpdatedAt: now,
	}
	var createErr error
	err = mutation.Run(ctx, id, map[string]any{"declared_size": size}, func(transactionContext context.Context) error {
		createErr = service.repository.CreateAttachment(transactionContext, attachment)
		return createErr
	})
	if createErr != nil {
		return AttachmentView{}, createErr
	}
	if err != nil {
		return AttachmentView{}, err
	}
	return attachmentView(attachment), nil
}

func (service *AttachmentService) UploadChunk(ctx context.Context, subject identity.Subject, id string, index int, source io.Reader) error {
	if source == nil || index < 0 || index > 100000 {
		return ErrInvalid
	}
	attachment, err := service.repository.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	if !canWriteAttachment(subject, attachment) {
		return ErrForbidden
	}
	if attachment.State != AttachmentStateUploading {
		return ErrAttachmentNotReady
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionAttachmentChunkUploaded, ResourceType: "attachment", ResourceID: attachment.ID,
	})
	if err != nil {
		return err
	}
	chunkDir, err := service.storagePath(".chunks", attachment.ID)
	if err != nil {
		return failAttachmentMutation(ctx, mutation, attachment.ID, err)
	}
	if err := os.MkdirAll(chunkDir, 0700); err != nil {
		return failAttachmentMutation(ctx, mutation, attachment.ID, errors.New("无法保存附件分片"))
	}
	chunkPath, err := service.storagePath(".chunks", attachment.ID, fmt.Sprintf("chunk_%08d", index))
	if err != nil {
		return failAttachmentMutation(ctx, mutation, attachment.ID, err)
	}
	written, err := copyBoundedExclusiveFile(chunkPath, source, service.config.MaxChunkBytes)
	if err != nil {
		return failAttachmentMutation(ctx, mutation, attachment.ID, err)
	}
	limit := attachment.Size
	if service.config.MaxFileBytes < limit {
		limit = service.config.MaxFileBytes
	}
	var updateErr error
	err = mutation.Run(ctx, attachment.ID, map[string]any{"chunk_index": index, "size": written}, func(transactionContext context.Context) error {
		updateErr = service.repository.AddAttachmentChunkBytes(transactionContext, attachment.ID, attachment.OwnerUserID, written, limit, service.now())
		return updateErr
	})
	if updateErr != nil {
		_ = os.Remove(chunkPath)
		return updateErr
	}
	if err != nil {
		_ = os.Remove(chunkPath)
		return err
	}
	return nil
}

func (service *AttachmentService) Merge(ctx context.Context, subject identity.Subject, id string, totalChunks int, declaredSize int64) (AttachmentView, error) {
	if totalChunks <= 0 || totalChunks > 100000 || declaredSize <= 0 || declaredSize > service.config.MaxFileBytes {
		return AttachmentView{}, ErrInvalid
	}
	attachment, err := service.repository.GetAttachment(ctx, id)
	if err != nil {
		return AttachmentView{}, err
	}
	if !canWriteAttachment(subject, attachment) {
		return AttachmentView{}, ErrForbidden
	}
	if attachment.State != AttachmentStateUploading {
		return AttachmentView{}, ErrAttachmentNotReady
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionAttachmentMerged, ResourceType: "attachment", ResourceID: attachment.ID,
	})
	if err != nil {
		return AttachmentView{}, err
	}
	if attachment.Size != declaredSize || attachment.ChunkBytes != declaredSize {
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, attachment.ID, ErrAttachmentSizeMismatch)
	}
	finalPath, err := service.storagePath(attachment.StorageName)
	if err != nil {
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, attachment.ID, err)
	}
	temporary := finalPath + ".merging"
	destination, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, attachment.ID, errors.New("无法合并附件"))
	}
	var actual int64
	mergeErr := func() error {
		defer destination.Close()
		for index := 0; index < totalChunks; index++ {
			chunkPath, pathErr := service.storagePath(".chunks", attachment.ID, fmt.Sprintf("chunk_%08d", index))
			if pathErr != nil {
				return pathErr
			}
			chunk, openErr := os.Open(chunkPath)
			if openErr != nil {
				return ErrAttachmentSizeMismatch
			}
			written, copyErr := io.Copy(destination, io.LimitReader(chunk, service.config.MaxChunkBytes+1))
			_ = chunk.Close()
			actual += written
			if copyErr != nil || written > service.config.MaxChunkBytes || actual > service.config.MaxFileBytes {
				return ErrAttachmentTooLarge
			}
		}
		if actual != declaredSize {
			return ErrAttachmentSizeMismatch
		}
		return destination.Sync()
	}()
	if mergeErr != nil {
		_ = os.Remove(temporary)
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, attachment.ID, mergeErr)
	}
	if err := os.Rename(temporary, finalPath); err != nil {
		_ = os.Remove(temporary)
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, attachment.ID, errors.New("无法保存合并附件"))
	}
	var updateErr error
	err = mutation.Run(ctx, attachment.ID, map[string]any{"size": actual, "chunks": totalChunks}, func(transactionContext context.Context) error {
		updateErr = service.repository.MarkAttachmentReady(transactionContext, attachment.ID, attachment.OwnerUserID, actual, service.now())
		return updateErr
	})
	if updateErr != nil {
		_ = os.Remove(finalPath)
		return AttachmentView{}, updateErr
	}
	if err != nil {
		_ = os.Remove(finalPath)
		return AttachmentView{}, err
	}
	chunkDir, _ := service.storagePath(".chunks", attachment.ID)
	_ = os.RemoveAll(chunkDir)
	attachment.State, attachment.Size, attachment.UpdatedAt = AttachmentStateReady, actual, service.now()
	return attachmentView(attachment), nil
}

func (service *AttachmentService) Open(ctx context.Context, subject identity.Subject, id string) (*os.File, string, int64, error) {
	attachment, err := service.repository.GetAttachment(ctx, id)
	if err != nil {
		return nil, "", 0, err
	}
	if !canReadAttachment(subject, attachment) {
		return nil, "", 0, ErrForbidden
	}
	if attachment.State != AttachmentStateReady {
		return nil, "", 0, ErrAttachmentNotReady
	}
	path, err := service.storagePath(attachment.StorageName)
	if err != nil {
		return nil, "", 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", 0, ErrNotFound
	}
	return file, attachment.OriginalName, attachment.Size, nil
}

func (service *AttachmentService) ResolveReady(ctx context.Context, ownerUserID string, ids []string) ([]string, error) {
	resolved := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		if _, duplicate := seen[id]; duplicate || strings.TrimSpace(id) == "" {
			return nil, ErrInvalid
		}
		seen[id] = struct{}{}
		attachment, err := service.repository.GetAttachment(ctx, id)
		if err != nil {
			return nil, err
		}
		if attachment.OwnerUserID != ownerUserID {
			return nil, ErrForbidden
		}
		if attachment.State != AttachmentStateReady {
			return nil, ErrAttachmentNotReady
		}
		resolved = append(resolved, attachment.StorageName)
	}
	return resolved, nil
}

func (service *AttachmentService) storagePath(elements ...string) (string, error) {
	joined := filepath.Join(append([]string{service.config.UploadDir}, elements...)...)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", ErrInvalid
	}
	relative, err := filepath.Rel(service.config.UploadDir, abs)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrInvalid
	}
	return abs, nil
}

func copyBoundedFile(path string, source io.Reader, limit int64) (int64, error) {
	destination, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, errors.New("无法保存附件")
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, limit+1))
	closeErr := destination.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return 0, errors.New("无法保存附件")
	}
	if written > limit {
		_ = os.Remove(path)
		return 0, ErrAttachmentTooLarge
	}
	return written, nil
}

func copyBoundedExclusiveFile(path string, source io.Reader, limit int64) (int64, error) {
	return copyBoundedFile(path, source, limit)
}

func validAttachmentFilename(filename string) bool {
	filename = strings.TrimSpace(filename)
	return filename != "" && len(filename) <= 255 && filename == filepath.Base(filename) &&
		!strings.Contains(filename, "..") && !strings.ContainsAny(filename, `/\\`)
}

func attachmentCreator(subject identity.Subject) bool {
	return subject.UserID != "" && (subject.Role == identity.RoleUser || subject.Role == identity.RoleAdmin)
}

func canReadAttachment(subject identity.Subject, attachment *Attachment) bool {
	return subject.Role == identity.RoleAdmin || subject.Role == identity.RoleAuditor ||
		subject.Role == identity.RoleUser && subject.UserID == attachment.OwnerUserID
}

func canWriteAttachment(subject identity.Subject, attachment *Attachment) bool {
	return subject.Role == identity.RoleAdmin ||
		subject.Role == identity.RoleUser && subject.UserID == attachment.OwnerUserID
}

func attachmentView(attachment *Attachment) AttachmentView {
	return AttachmentView{ID: attachment.ID, Filename: attachment.OriginalName, Size: attachment.Size, State: attachment.State, CreatedAt: attachment.CreatedAt}
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{tasks: map[string]*Task{}, byOwner: map[string]string{}, attachments: map[string]*Attachment{}}
}

func cloneTask(task *Task) *Task {
	copy := *task
	copy.Params = append(json.RawMessage(nil), task.Params...)
	copy.AttachmentRefs = append(json.RawMessage(nil), task.AttachmentRefs...)
	return &copy
}

func (repository *MemoryRepository) CreateOrGet(_ context.Context, task *Task) (*Task, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	key := task.OwnerUserID + "\x00" + task.IdempotencyKey
	if id, exists := repository.byOwner[key]; exists {
		return cloneTask(repository.tasks[id]), false, nil
	}
	repository.tasks[task.ID] = cloneTask(task)
	repository.byOwner[key] = task.ID
	return cloneTask(task), true, nil
}

func (repository *MemoryRepository) ClaimDispatch(_ context.Context, id string, now, leaseUntil time.Time) (string, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return "", false, ErrNotFound
	}
	if task.DispatchAttempts >= MaxDispatchAttempts || task.Status != StatusPending && !(task.Status == StatusDispatching && task.DispatchLeaseUntil != nil && !task.DispatchLeaseUntil.After(now)) {
		return "", false, nil
	}
	claim := uuid.NewString()
	task.Status = StatusDispatching
	task.DispatchClaimToken = claim
	task.DispatchLeaseUntil = &leaseUntil
	task.UpdatedAt = now
	return claim, true, nil
}

func (repository *MemoryRepository) ReserveDispatchAttempt(_ context.Context, id, claim string, now time.Time) (int, bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return 0, false, ErrNotFound
	}
	if task.Status != StatusDispatching || task.DispatchClaimToken != claim || task.DispatchAttempts >= MaxDispatchAttempts {
		return task.DispatchAttempts, false, nil
	}
	task.DispatchAttempts++
	task.UpdatedAt = now
	return task.DispatchAttempts, true, nil
}

func (repository *MemoryRepository) MarkRunning(_ context.Context, id, claim, engineID string, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return ErrNotFound
	}
	if task.Status != StatusDispatching || task.DispatchClaimToken != claim {
		return ErrDispatchLeaseLost
	}
	task.Status, task.EngineSessionID, task.DispatchClaimToken, task.DispatchLeaseUntil, task.UpdatedAt = StatusRunning, engineID, "", nil, now
	return nil
}

func (repository *MemoryRepository) markDispatchOutcome(id, claim string, status Status, message string, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return ErrNotFound
	}
	if task.Status != StatusDispatching || task.DispatchClaimToken != claim {
		return ErrDispatchLeaseLost
	}
	task.Status, task.DispatchError, task.DispatchClaimToken, task.DispatchLeaseUntil, task.UpdatedAt = status, message, "", nil, now
	return nil
}

func (repository *MemoryRepository) MarkDispatchFailed(_ context.Context, id, claim, message string, now time.Time) error {
	return repository.markDispatchOutcome(id, claim, StatusDispatchFailed, message, now)
}

func (repository *MemoryRepository) MarkDispatchUnknown(_ context.Context, id, claim, message string, now time.Time) error {
	return repository.markDispatchOutcome(id, claim, StatusDispatchUnknown, message, now)
}

func (repository *MemoryRepository) UpdateStatus(_ context.Context, id string, status Status, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return ErrNotFound
	}
	task.Status, task.UpdatedAt = status, now
	return nil
}

func (repository *MemoryRepository) TransitionStatus(_ context.Context, id string, from []Status, status Status, reason string, now time.Time) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return false, ErrNotFound
	}
	allowed := false
	for _, candidate := range from {
		if task.Status == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return false, nil
	}
	task.Status, task.DispatchError, task.UpdatedAt = status, reason, now
	return true, nil
}

func (repository *MemoryRepository) Get(_ context.Context, id string) (*Task, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneTask(task), nil
}

func (repository *MemoryRepository) GetBrowser(_ context.Context, id, ownerUserID string) (*Task, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[id]
	if !exists || ownerUserID != "" && task.OwnerUserID != ownerUserID {
		return nil, ErrNotFound
	}
	return cloneTask(task), nil
}

func (repository *MemoryRepository) GetByEngineSessionID(_ context.Context, engineSessionID string) (*Task, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, task := range repository.tasks {
		if task.EngineSessionID == engineSessionID {
			return cloneTask(task), nil
		}
	}
	return nil, ErrNotFound
}

func (repository *MemoryRepository) List(_ context.Context) ([]Task, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	tasks := make([]Task, 0, len(repository.tasks))
	for _, task := range repository.tasks {
		tasks = append(tasks, *cloneTask(task))
	}
	sort.Slice(tasks, func(left, right int) bool {
		if tasks[left].CreatedAt.Equal(tasks[right].CreatedAt) {
			return tasks[left].ID < tasks[right].ID
		}
		return tasks[left].CreatedAt.Before(tasks[right].CreatedAt)
	})
	return tasks, nil
}

func (repository *MemoryRepository) ListBrowser(_ context.Context, query TaskListQuery) ([]Task, int64, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	tasks := make([]Task, 0, len(repository.tasks))
	for _, task := range repository.tasks {
		if query.OwnerUserID == "" || task.OwnerUserID == query.OwnerUserID {
			tasks = append(tasks, *cloneTask(task))
		}
	}
	sort.Slice(tasks, func(left, right int) bool {
		if tasks[left].CreatedAt.Equal(tasks[right].CreatedAt) {
			return tasks[left].ID > tasks[right].ID
		}
		return tasks[left].CreatedAt.After(tasks[right].CreatedAt)
	})
	total := len(tasks)
	if query.Offset >= total {
		return []Task{}, int64(total), nil
	}
	if query.Offset > 0 {
		tasks = tasks[query.Offset:]
	}
	if query.Limit > 0 && len(tasks) > query.Limit {
		tasks = tasks[:query.Limit]
	}
	return tasks, int64(total), nil
}

func (repository *MemoryRepository) ListRecoverable(_ context.Context, afterID string, limit int) ([]Task, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if limit <= 0 || limit > completedResultRecoveryBatchSize {
		limit = completedResultRecoveryBatchSize
	}
	tasks := make([]Task, 0, limit)
	for _, task := range repository.tasks {
		if task.ID <= afterID || isTerminalStatus(task.Status) || task.EngineSessionID == "" {
			continue
		}
		tasks = append(tasks, *cloneTask(task))
	}
	sort.Slice(tasks, func(left, right int) bool { return tasks[left].ID < tasks[right].ID })
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	return tasks, nil
}

func cloneAttachment(attachment *Attachment) *Attachment {
	copy := *attachment
	return &copy
}

func (repository *MemoryRepository) CreateAttachment(_ context.Context, attachment *Attachment) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if _, exists := repository.attachments[attachment.ID]; exists {
		return ErrInvalid
	}
	repository.attachments[attachment.ID] = cloneAttachment(attachment)
	return nil
}

func (repository *MemoryRepository) GetAttachment(_ context.Context, id string) (*Attachment, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	attachment, exists := repository.attachments[id]
	if !exists {
		return nil, ErrNotFound
	}
	return cloneAttachment(attachment), nil
}

func (repository *MemoryRepository) AddAttachmentChunkBytes(_ context.Context, id, owner string, delta, limit int64, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	attachment, exists := repository.attachments[id]
	if !exists {
		return ErrNotFound
	}
	if attachment.OwnerUserID != owner {
		return ErrForbidden
	}
	if attachment.ChunkBytes+delta > limit {
		return ErrAttachmentTooLarge
	}
	attachment.ChunkBytes += delta
	attachment.UpdatedAt = now
	return nil
}

func (repository *MemoryRepository) MarkAttachmentReady(_ context.Context, id, owner string, size int64, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	attachment, exists := repository.attachments[id]
	if !exists {
		return ErrNotFound
	}
	if attachment.OwnerUserID != owner {
		return ErrForbidden
	}
	attachment.State, attachment.Size, attachment.UpdatedAt = AttachmentStateReady, size, now
	return nil
}

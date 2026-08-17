package reports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
)

type CompletedTask struct {
	TaskID      string
	OwnerUserID string
	TaskType    string
	RawResult   json.RawMessage
	CompletedAt time.Time
}

type brandReader interface {
	Get(context.Context) (brand.Config, error)
}

type Service struct {
	repository Repository
	brands     brandReader
	audits     audit.Recorder
	renderer   PDFRenderer
	source     CompletedTaskSource
	now        func() time.Time
}

type PDFRenderer interface {
	Render(context.Context, *Snapshot) ([]byte, error)
}

type CompletedTaskSource interface {
	GetCompletedTask(context.Context, string) (CompletedTask, error)
}

type visibleReportRepository interface {
	GetVisible(context.Context, string, string) (*Snapshot, error)
}

func NewService(repository Repository, brands interface {
	Get(context.Context) (brand.Config, error)
}) *Service {
	return &Service{repository: repository, brands: brands, now: func() time.Time { return time.Now().UTC() }}
}

func NewGovernedService(repository Repository, brands interface {
	Get(context.Context) (brand.Config, error)
}, audits audit.Recorder, renderer PDFRenderer, source CompletedTaskSource) *Service {
	service := NewService(repository, brands)
	service.audits = audits
	service.renderer = renderer
	service.source = source
	return service
}

func (service *Service) Prepare(ctx context.Context, task CompletedTask) (*Snapshot, error) {
	if service == nil || service.brands == nil {
		return nil, ErrInvalidSnapshot
	}
	branding, err := service.brands.Get(ctx)
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	generatedAt := service.now()
	trend, err := service.repository.Trend(ctx, TrendQuery{Now: generatedAt, Days: 30, OwnerUserID: task.OwnerUserID})
	if err != nil {
		return nil, ErrInvalidSnapshot
	}
	return buildSnapshotAt(task.TaskID, task.OwnerUserID, task.TaskType, task.RawResult, branding, task.CompletedAt, generatedAt, trend)
}

func (service *Service) Persist(ctx context.Context, snapshot *Snapshot) error {
	if service == nil || service.repository == nil {
		return ErrInvalidSnapshot
	}
	return service.repository.Create(ctx, snapshot)
}

func (service *Service) List(ctx context.Context, subject identity.Subject, page, pageSize int) ([]Snapshot, error) {
	query, err := listQueryFor(subject)
	if err != nil {
		return nil, err
	}
	if page < 1 || page > maxReportPage || pageSize < 1 || pageSize > maxReportPageSize {
		return nil, ErrInvalidSnapshot
	}
	query.Limit = pageSize
	query.Offset = (page - 1) * pageSize
	return service.repository.List(ctx, query)
}

func (service *Service) Get(ctx context.Context, subject identity.Subject, reportID string) (*Snapshot, error) {
	if !reportReader(subject) {
		return nil, ErrForbidden
	}
	if subject.Role == identity.RoleUser {
		if repository, ok := service.repository.(visibleReportRepository); ok {
			return repository.GetVisible(ctx, reportID, subject.UserID)
		}
	}
	snapshot, err := service.repository.Get(ctx, reportID)
	if err != nil {
		return nil, err
	}
	if subject.Role == identity.RoleUser && snapshot.OwnerUserID != subject.UserID {
		return nil, ErrNotFound
	}
	return snapshot, nil
}

func (service *Service) Trend(ctx context.Context, subject identity.Subject, days int, now time.Time) ([]TrendPoint, error) {
	query, err := listQueryFor(subject)
	if err != nil || days < 1 || days > 30 || now.IsZero() {
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidSnapshot
	}
	return service.repository.Trend(ctx, TrendQuery{Now: now, Days: days, OwnerUserID: query.OwnerUserID})
}

func (service *Service) Dashboard(ctx context.Context, subject identity.Subject, from, to time.Time, attentionLimit int) (DashboardProjection, error) {
	query, err := listQueryFor(subject)
	if err != nil {
		return DashboardProjection{}, err
	}
	repository, ok := service.repository.(DashboardRepository)
	if !ok {
		return DashboardProjection{}, ErrInvalidSnapshot
	}
	return repository.Dashboard(ctx, DashboardQuery{
		OwnerUserID: query.OwnerUserID, From: from, To: to, AttentionLimit: attentionLimit,
	})
}

func (service *Service) SetDashboardTaskVerifier(verifier DashboardTaskVerifier) {
	if repository, ok := service.repository.(*MemoryRepository); ok {
		repository.SetDashboardTaskVerifier(verifier)
	}
}

func (service *Service) ExportPDF(ctx context.Context, subject identity.Subject, reportID string) ([]byte, error) {
	snapshot, err := service.Get(ctx, subject, reportID)
	if err != nil {
		return nil, err
	}
	if service.renderer == nil || service.audits == nil {
		return nil, errors.New("报告导出服务未配置")
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionReportExported, ResourceType: "report", ResourceID: snapshot.ID,
		Metadata: map[string]any{"format": "pdf"},
	})
	if err != nil {
		return nil, errors.New("报告导出失败")
	}
	if err := mutation.Prepare(ctx); err != nil {
		return nil, errors.New("报告导出失败")
	}
	pdf, renderErr := service.renderer.Render(ctx, snapshot)
	if renderErr != nil {
		_ = mutation.Failed(ctx, snapshot.ID, map[string]any{"format": "pdf"})
		return nil, errors.New("报告导出失败")
	}
	if err := mutation.Succeeded(ctx, snapshot.ID, map[string]any{"format": "pdf"}); err != nil {
		return nil, errors.New("报告导出失败")
	}
	return pdf, nil
}

func (service *Service) Backfill(ctx context.Context, subject identity.Subject, taskID string) (*Snapshot, error) {
	if subject.Role != identity.RoleAdmin || taskID == "" {
		return nil, ErrForbidden
	}
	existing, err := service.repository.GetByTaskID(ctx, taskID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNotFound) || service.audits == nil || service.source == nil {
		return nil, errors.New("报告补建失败")
	}
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.ActionReportBackfilled, ResourceType: "report", ResourceID: taskID,
		Metadata: map[string]any{"operation": "backfill"},
	})
	if err != nil {
		return nil, errors.New("报告补建失败")
	}
	if err := mutation.Prepare(ctx); err != nil {
		return nil, errors.New("报告补建失败")
	}
	task, err := service.source.GetCompletedTask(ctx, taskID)
	if err != nil {
		_ = mutation.Failed(ctx, taskID, map[string]any{"operation": "backfill"})
		return nil, errors.New("报告补建失败")
	}
	candidate, err := service.Prepare(ctx, task)
	if err != nil {
		_ = mutation.Failed(ctx, taskID, map[string]any{"operation": "backfill"})
		return nil, errors.New("报告补建失败")
	}
	stored := candidate
	err = mutation.Run(ctx, taskID, map[string]any{"operation": "backfill"}, func(transactionContext context.Context) error {
		if persistErr := service.Persist(transactionContext, candidate); persistErr != nil {
			if errors.Is(persistErr, ErrSnapshotExists) {
				var getErr error
				stored, getErr = service.repository.GetByTaskID(transactionContext, taskID)
				return getErr
			}
			return persistErr
		}
		return nil
	})
	if err != nil {
		return nil, errors.New("报告补建失败")
	}
	return cloneSnapshot(stored), nil
}

func listQueryFor(subject identity.Subject) (ListQuery, error) {
	switch subject.Role {
	case identity.RoleUser:
		if subject.UserID == "" {
			return ListQuery{}, ErrForbidden
		}
		return ListQuery{OwnerUserID: subject.UserID}, nil
	case identity.RoleAuditor, identity.RoleAdmin:
		return ListQuery{}, nil
	default:
		return ListQuery{}, ErrForbidden
	}
}

func reportReader(subject identity.Subject) bool {
	_, err := listQueryFor(subject)
	return err == nil
}

package mcpscans

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
)

type Summary struct {
	ID         string       `json:"id"`
	Owner      string       `json:"owner"`
	Status     tasks.Status `json:"status"`
	SourceKind string       `json:"source_kind"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}
type InputSummary struct {
	Language   string `json:"language"`
	SourceKind string `json:"source_kind"`
	ModelID    string `json:"model_id,omitempty"`
	Thread     int    `json:"thread,omitempty"`
}
type Detail struct {
	Summary
	InputSummary InputSummary `json:"input_summary"`
	ReportID     string       `json:"report_id,omitempty"`
}
type ListResponse struct {
	Items    []Summary `json:"items"`
	Total    int64     `json:"total"`
	Page     int       `json:"page"`
	PageSize int       `json:"page_size"`
}
type Service struct {
	create     *CreateUnitOfWork
	repository tasks.Repository
	tasks      *tasks.Service
	replay     *idempotency.Service
	audits     audit.Recorder
}

func NewService(create *CreateUnitOfWork, repository tasks.Repository, controller *tasks.Service, replay *idempotency.Service, audits audit.Recorder) *Service {
	return &Service{create: create, repository: repository, tasks: controller, replay: replay, audits: audits}
}
func (service *Service) Create(ctx context.Context, subject identity.Subject, input CreateInput) (CreateResult, error) {
	if service.create == nil {
		return CreateResult{}, ErrInvalidCreate
	}
	return service.create.Create(ctx, subject, input)
}
func (service *Service) List(ctx context.Context, subject identity.Subject, page, size int, status tasks.Status) (ListResponse, error) {
	if page < 1 || page > 1000 || size < 1 || size > 100 {
		return ListResponse{}, ErrInvalidCreate
	}
	switch status {
	case "", tasks.StatusPending, tasks.StatusDispatching, tasks.StatusRunning, tasks.StatusSucceeded, tasks.StatusEngineFailed, tasks.StatusCancelled, tasks.StatusDispatchFailed, tasks.StatusDispatchUnknown:
	default:
		return ListResponse{}, ErrInvalidCreate
	}
	query := tasks.TaskListQuery{IncludeMCPSummary: true, TaskType: "mcp_scan", Status: status, Limit: size, Offset: (page - 1) * size}
	switch subject.Role {
	case identity.RoleUser:
		query.OwnerUserID = subject.UserID
	case identity.RoleAdmin, identity.RoleAuditor:
	default:
		return ListResponse{}, tasks.ErrForbidden
	}
	if subject.UserID == "" {
		return ListResponse{}, tasks.ErrForbidden
	}
	records, total, err := service.repository.ListBrowser(ctx, query)
	if err != nil {
		return ListResponse{}, err
	}
	response := ListResponse{Items: []Summary{}, Total: total, Page: page, PageSize: size}
	for _, record := range records {
		response.Items = append(response.Items, Summary{ID: record.ID, Owner: record.OwnerUsername, Status: record.Status, SourceKind: sourceKind(record.Params), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt})
	}
	return response, nil
}
func sourceKind(raw json.RawMessage) string {
	var value struct {
		SourceKind string `json:"source_kind"`
	}
	if len(raw) <= tasks.MaxTaskParamsLength && json.Unmarshal(raw, &value) == nil && (value.SourceKind == "repository" || value.SourceKind == "service") {
		return value.SourceKind
	}
	return "legacy_unknown"
}
func (service *Service) Get(ctx context.Context, subject identity.Subject, id string) (Detail, error) {
	view, err := service.tasks.BrowserGet(ctx, subject, id)
	if err != nil {
		return Detail{}, err
	}
	if view.TaskType != "mcp_scan" {
		return Detail{}, tasks.ErrNotFound
	}
	modelID, err := service.tasks.MCPModelReference(ctx, subject, id)
	if err != nil {
		return Detail{}, err
	}
	kind := view.InputSummary.SourceKind
	if kind != "repository" && kind != "service" {
		kind = "legacy_unknown"
	}
	return Detail{Summary: Summary{ID: view.ID, Owner: view.Owner, Status: view.Status, SourceKind: kind, CreatedAt: view.CreatedAt, UpdatedAt: view.UpdatedAt}, InputSummary: InputSummary{Language: "zh_CN", SourceKind: kind, ModelID: modelID, Thread: view.InputSummary.Thread}, ReportID: view.ReportID}, nil
}
func (service *Service) Cancel(ctx context.Context, subject identity.Subject, id, key string) (CreateResult, error) {
	if subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin {
		return CreateResult{}, tasks.ErrForbidden
	}
	if _, err := service.Get(ctx, subject, id); err != nil {
		return CreateResult{}, err
	}
	result, err := service.replay.Execute(ctx, subject, idempotency.Operation{Scope: idempotency.ScopePrivate, Method: http.MethodPost, Path: CreateOperationPath + "/" + id + "/cancel", Key: key, Payload: json.RawMessage(`{}`)}, func(locked context.Context, claim *idempotency.Claim) error {
		return service.tasks.CancelMCPWithCompletion(locked, subject, id, func(tx context.Context, status tasks.Status) error {
			return claim.PersistSuccess(tx, http.StatusOK, idempotency.SafeResponse{TaskID: id, Status: string(status)})
		})
	})
	if err != nil {
		return CreateResult{}, err
	}
	return CreateResult{TaskID: result.Response.TaskID, Status: tasks.Status(result.Response.Status), Replay: result.Replay}, nil
}

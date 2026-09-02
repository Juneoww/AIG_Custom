package tasks

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Juneoww/AIG_Custom/common/portscan"
	"github.com/Juneoww/AIG_Custom/common/runner"
	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	MaxIdempotencyKeyLength                      = 128
	MaxDispatchAttempts                          = 3
	MaxTaskContentLength                         = 32 << 10
	MaxTaskParamsLength                          = 64 << 10
	MaxTaskAttachmentCount                       = 10
	MaxTaskReferenceLength                       = 128
	maxInfrastructureTargetAttachmentBytes int64 = 1 << 20
	dispatchLeaseDuration                        = 30 * time.Second
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
	WithinCreateKeyLock(context.Context, string, string, func(context.Context) error) error
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
	Status      Status
	TaskType    string
	Limit       int
	Offset      int
}

type TaskListFilters struct {
	Status   Status
	TaskType string
}

// RecentRepository is an optional bounded browser-summary read model.
type RecentRepository interface {
	ListRecent(context.Context, TaskListQuery) ([]Task, error)
}

type dashboardTaskStatusRepository interface {
	DashboardTaskSucceeded(context.Context, string, string) (bool, error)
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
		input.IdempotencyKey == "" || len(input.IdempotencyKey) > MaxIdempotencyKeyLength ||
		!isBrowserTaskType(input.TaskType) || len(input.Content) > MaxTaskContentLength ||
		!validTaskCountry(input.CountryIsoCode) || !validTaskAttachmentIDs(input.AttachmentIDs) {
		return View{}, ErrInvalid
	}
	rawParams := input.Params
	if len(rawParams) == 0 {
		rawParams = json.RawMessage(`{}`)
	}
	if len(rawParams) > MaxTaskParamsLength {
		return View{}, ErrInvalid
	}
	attachmentRefs, err := json.Marshal(input.AttachmentIDs)
	if err != nil {
		return View{}, ErrInvalid
	}
	taskID := uuid.NewSHA1(taskIDNamespace, []byte(subject.UserID+"\x00"+input.IdempotencyKey)).String()
	var persisted *Task
	err = service.repository.WithinCreateKeyLock(ctx, subject.UserID, input.IdempotencyKey, func(lockContext context.Context) error {
		if input.TaskType == "mcp_scan" && mcpParamsOmitSourceKind(rawParams) {
			existing, getErr := service.repository.Get(lockContext, taskID)
			if getErr == nil && sameLegacyMCPRetry(existing, subject, input, rawParams, attachmentRefs, taskID) {
				persisted = existing
				return nil
			}
			if getErr != nil && !errors.Is(getErr, ErrNotFound) {
				return getErr
			}
		}
		params, valid := normalizeTaskParams(input.TaskType, rawParams)
		if !valid {
			return ErrInvalid
		}
		if input.TaskType == "mcp_scan" && !validMCPCreateSource(input, params) {
			return ErrInvalid
		}
		var createErr error
		persisted, createErr = service.createLocked(lockContext, subject, input, params, attachmentRefs, taskID)
		return createErr
	})
	if err != nil {
		return View{}, err
	}
	now := service.now()
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

func (service *Service) createLocked(
	ctx context.Context,
	subject identity.Subject,
	input CreateInput,
	params json.RawMessage,
	attachmentRefs json.RawMessage,
	taskID string,
) (*Task, error) {
	now := service.now()
	candidate := &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: input.IdempotencyKey, EngineSessionID: taskID, TaskType: input.TaskType,
		Content: input.Content, Params: append(json.RawMessage(nil), params...), AttachmentRefs: attachmentRefs,
		CountryIsoCode: input.CountryIsoCode, Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	}
	existing, getErr := service.repository.Get(ctx, taskID)
	if getErr == nil {
		if !sameCreateRequest(existing, candidate) {
			return nil, ErrInvalid
		}
		return existing, nil
	} else if !errors.Is(getErr, ErrNotFound) {
		return nil, getErr
	} else {
		if err := service.engine.ValidateTaskReferences(ctx, EngineTask{
			OwnerUsername: subject.Username, TaskType: input.TaskType, Params: append(json.RawMessage(nil), params...),
		}); err != nil {
			if errors.Is(err, ErrInvalid) {
				return nil, ErrInvalid
			}
			return nil, err
		}
		if len(input.AttachmentIDs) > 0 {
			if service.attachments == nil {
				return nil, ErrInvalid
			}
			if _, resolveErr := service.attachments.ResolveReady(ctx, subject.UserID, input.AttachmentIDs); resolveErr != nil {
				return nil, resolveErr
			}
		}
		if input.TaskType == "ai_infra_scan" {
			if validateErr := service.validateInfrastructureTargets(ctx, subject.UserID, input.Content, input.AttachmentIDs); validateErr != nil {
				return nil, validateErr
			}
		}
	}

	metadata := taskCreatedAuditMetadata(input.TaskType, params)
	mutation, err := audit.BeginMutation(ctx, service.audits, subject, audit.EventInput{
		Action: audit.Action("task.created"), ResourceType: "task", ResourceID: taskID, Metadata: metadata,
	})
	if err != nil {
		return nil, err
	}
	var persisted *Task
	var created bool
	var repositoryErr error
	err = mutation.Run(ctx, taskID, metadata, func(transactionContext context.Context) error {
		persisted, created, repositoryErr = service.repository.CreateOrGet(transactionContext, candidate)
		if repositoryErr == nil && !sameCreateRequest(persisted, candidate) {
			repositoryErr = ErrInvalid
		}
		if repositoryErr == nil && created && len(input.AttachmentIDs) > 0 {
			repositoryErr = service.attachments.repository.BindReadyAttachments(
				transactionContext, subject.UserID, input.AttachmentIDs, now,
			)
		}
		return repositoryErr
	})
	if repositoryErr != nil {
		return nil, repositoryErr
	}
	if err != nil {
		return nil, err
	}
	return persisted, nil
}

func (service *Service) validateInfrastructureTargets(ctx context.Context, ownerUserID, content string, attachmentIDs []string) error {
	expressions, err := runner.AppendTargetExpressionLines(nil, content)
	if err != nil {
		return ErrInvalid
	}
	if len(attachmentIDs) > 0 {
		if service.attachments == nil {
			return ErrInvalid
		}
		attachmentExpressions, err := service.attachments.ReadReadyTargetExpressions(ctx, ownerUserID, attachmentIDs, expressions)
		if err != nil {
			return ErrInvalid
		}
		expressions = attachmentExpressions
	}
	if _, err := runner.ParseTargets(expressions); err != nil {
		return ErrInvalid
	}
	return nil
}

func sameCreateRequest(persisted, candidate *Task) bool {
	if persisted == nil || candidate == nil || persisted.OwnerUserID != candidate.OwnerUserID ||
		persisted.IdempotencyKey != candidate.IdempotencyKey || persisted.TaskType != candidate.TaskType ||
		persisted.Content != candidate.Content || persisted.CountryIsoCode != candidate.CountryIsoCode {
		return false
	}
	persistedRaw, candidateRaw := persisted.Params, candidate.Params
	if persisted.TaskType == "ai_infra_scan" {
		var persistedValid, candidateValid bool
		persistedRaw, persistedValid = normalizeInfrastructureTaskParams(persistedRaw)
		candidateRaw, candidateValid = normalizeInfrastructureTaskParams(candidateRaw)
		if !persistedValid || !candidateValid {
			return false
		}
	}
	persistedParams, persistedParamsOK := canonicalJSON(persistedRaw)
	candidateParams, candidateParamsOK := canonicalJSON(candidateRaw)
	return persistedParamsOK && candidateParamsOK && bytes.Equal(persistedParams, candidateParams) &&
		sameAttachmentRefs(persisted.AttachmentRefs, candidate.AttachmentRefs)
}

func sameLegacyMCPRetry(
	persisted *Task,
	subject identity.Subject,
	input CreateInput,
	rawParams json.RawMessage,
	attachmentRefs json.RawMessage,
	taskID string,
) bool {
	if persisted == nil || input.TaskType != "mcp_scan" || persisted.TaskType != "mcp_scan" ||
		!mcpParamsOmitSourceKind(rawParams) || !mcpParamsOmitSourceKind(persisted.Params) {
		return false
	}
	candidate := &Task{
		ID: taskID, OwnerUserID: subject.UserID, OwnerUsername: subject.Username,
		IdempotencyKey: input.IdempotencyKey, EngineSessionID: taskID, TaskType: input.TaskType,
		Content: input.Content, Params: append(json.RawMessage(nil), rawParams...), AttachmentRefs: attachmentRefs,
		CountryIsoCode: input.CountryIsoCode,
	}
	return sameCreateRequest(persisted, candidate)
}

func mcpParamsOmitSourceKind(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	_, provided := fields["source_kind"]
	return !provided
}

func sameAttachmentRefs(persisted, candidate json.RawMessage) bool {
	var persistedIDs, candidateIDs []string
	if json.Unmarshal(persisted, &persistedIDs) != nil || json.Unmarshal(candidate, &candidateIDs) != nil ||
		len(persistedIDs) != len(candidateIDs) {
		return false
	}
	for index := range persistedIDs {
		if persistedIDs[index] != candidateIDs[index] {
			return false
		}
	}
	return true
}

func canonicalJSON(raw json.RawMessage) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, false
	}
	normalized, err := json.Marshal(value)
	return normalized, err == nil
}

type mcpTaskParams struct {
	SourceKind             string `json:"source_kind"`
	ModelID                string `json:"model_id,omitempty"`
	Thread                 *int   `json:"thread,omitempty"`
	AuthorizationConfirmed *bool  `json:"authorization_confirmed,omitempty"`
}

func mcpTaskSourceKind(raw json.RawMessage) string {
	var params mcpTaskParams
	if json.Unmarshal(raw, &params) != nil {
		return ""
	}
	return params.SourceKind
}

func validMCPCreateSource(input CreateInput, raw json.RawMessage) bool {
	switch mcpTaskSourceKind(raw) {
	case "repository":
		if len(input.AttachmentIDs) > 0 {
			return input.Content == ""
		}
		return validMCPRepositoryReference(input.Content)
	case "service":
		return len(input.AttachmentIDs) == 0 && validMCPServiceEndpoint(input.Content)
	default:
		return false
	}
}

func validMCPRepositoryReference(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "?#") {
		return false
	}
	if parsed, err := url.ParseRequestURI(value); err == nil && parsed.Hostname() != "" &&
		strings.Trim(parsed.Path, "/") != "" && parsed.RawQuery == "" && parsed.Fragment == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			return parsed.User == nil
		case "ssh":
			if parsed.User == nil || parsed.User.Username() != "git" {
				return false
			}
			_, hasPassword := parsed.User.Password()
			return !hasPassword
		}
	}
	return validMCPRepositorySCPReference(value)
}

func validMCPRepositorySCPReference(value string) bool {
	if !strings.HasPrefix(value, "git@") || strings.ContainsAny(value, " \t\r\n?#") {
		return false
	}
	hostAndPath := strings.TrimPrefix(value, "git@")
	separator := strings.IndexByte(hostAndPath, ':')
	if separator <= 0 || separator == len(hostAndPath)-1 {
		return false
	}
	host, path := hostAndPath[:separator], hostAndPath[separator+1:]
	return !strings.ContainsAny(host, "/@") && strings.Trim(path, "/") != ""
}

func validMCPServiceEndpoint(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

func decodeMCPTaskParams(raw json.RawMessage) (mcpTaskParams, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return mcpTaskParams{}, false
	}
	var params mcpTaskParams
	if !decodeExactJSON(raw, &params) || (params.SourceKind != "repository" && params.SourceKind != "service") ||
		!validOptionalReference(fields, "model_id", params.ModelID) ||
		(params.Thread != nil && (*params.Thread < 1 || *params.Thread > 1_024)) {
		return mcpTaskParams{}, false
	}
	if params.SourceKind == "repository" {
		_, authorizationSupplied := fields["authorization_confirmed"]
		return params, !authorizationSupplied
	}
	return params, params.AuthorizationConfirmed != nil && *params.AuthorizationConfirmed
}

func normalizeMCPTaskParams(raw json.RawMessage) (json.RawMessage, bool) {
	params, valid := decodeMCPTaskParams(raw)
	if !valid {
		return nil, false
	}
	normalized, err := json.Marshal(params)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(normalized), true
}

type infrastructureTaskParams struct {
	ModelID      string `json:"model_id,omitempty"`
	Timeout      *int   `json:"timeout,omitempty"`
	PortScanMode string `json:"port_scan_mode"`
}

type redteamDatasetParams struct {
	NumPrompts   *int   `json:"numPrompts"`
	RandomSeed   *int64 `json:"randomSeed"`
	PromptColumn string `json:"promptColumn"`
}

type redteamTaskParams struct {
	ModelIDs    []string              `json:"model_id"`
	EvalModelID string                `json:"eval_model_id"`
	Dataset     *redteamDatasetParams `json:"dataset"`
	Techniques  []string              `json:"techniques"`
}

type agentTaskParams struct {
	AgentID     string `json:"agent_id"`
	EvalModelID string `json:"eval_model_id"`
}

func validTaskParams(taskType string, raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return false
	}
	switch taskType {
	case "mcp_scan":
		_, valid := decodeMCPTaskParams(raw)
		return valid
	case "ai_infra_scan":
		_, valid := normalizeInfrastructureTaskParams(raw)
		return valid
	case "model_redteam_report":
		var params redteamTaskParams
		if !decodeExactJSON(raw, &params) || !validReferences(params.ModelIDs, 10) || !validReference(params.EvalModelID) ||
			len(params.Techniques) > 64 || !validOptionalStrings(params.Techniques, 128) {
			return false
		}
		return params.Dataset == nil ||
			(params.Dataset.NumPrompts == nil || *params.Dataset.NumPrompts >= 1 && *params.Dataset.NumPrompts <= 1_000_000) &&
				(params.Dataset.PromptColumn == "" || validBoundedString(params.Dataset.PromptColumn, 128))
	case "agent_scan":
		var params agentTaskParams
		return decodeExactJSON(raw, &params) && validReference(params.AgentID) && validReference(params.EvalModelID)
	default:
		return false
	}
}

func normalizeTaskParams(taskType string, raw json.RawMessage) (json.RawMessage, bool) {
	if taskType == "mcp_scan" {
		return normalizeMCPTaskParams(raw)
	}
	if taskType == "ai_infra_scan" {
		return normalizeInfrastructureTaskParams(raw)
	}
	if !validTaskParams(taskType, raw) {
		return nil, false
	}
	return append(json.RawMessage(nil), raw...), true
}

func normalizeInfrastructureTaskParams(raw json.RawMessage) (json.RawMessage, bool) {
	params, fields, valid := decodeInfrastructureTaskParams(raw)
	if !valid {
		return nil, false
	}
	mode, valid := normalizedInfrastructurePortScanModeField(fields)
	if !valid {
		return nil, false
	}
	params.PortScanMode = string(mode)
	normalized, err := json.Marshal(params)
	if err != nil {
		return nil, false
	}
	return json.RawMessage(normalized), true
}

func normalizedInfrastructurePortScanMode(raw json.RawMessage) (portscan.Mode, bool) {
	_, fields, valid := decodeInfrastructureTaskParams(raw)
	if !valid {
		return "", false
	}
	if _, exists := fields["port_scan_mode"]; !exists {
		return "", false
	}
	mode, valid := normalizedInfrastructurePortScanModeField(fields)
	if !valid || portscan.PortSpec(mode) == "" {
		return "", false
	}
	return mode, true
}

func decodeInfrastructureTaskParams(raw json.RawMessage) (infrastructureTaskParams, map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return infrastructureTaskParams{}, nil, false
	}
	var params infrastructureTaskParams
	if !decodeExactJSON(raw, &params) || !validOptionalReference(fields, "model_id", params.ModelID) ||
		params.Timeout != nil && (*params.Timeout < 1 || *params.Timeout > 86_400) {
		return infrastructureTaskParams{}, nil, false
	}
	return params, fields, true
}

func normalizedInfrastructurePortScanModeField(fields map[string]json.RawMessage) (portscan.Mode, bool) {
	raw, exists := fields["port_scan_mode"]
	if !exists {
		return portscan.DefaultMode, true
	}
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil || strings.TrimSpace(*value) == "" {
		return "", false
	}
	mode, err := portscan.Normalize(*value)
	if err != nil {
		return "", false
	}
	return mode, true
}

func taskCreatedAuditMetadata(taskType string, params json.RawMessage) map[string]any {
	if taskType == "mcp_scan" {
		mcpParams, valid := decodeMCPTaskParams(params)
		if valid {
			return map[string]any{
				"source_kind":             mcpParams.SourceKind,
				"authorization_confirmed": mcpParams.AuthorizationConfirmed != nil && *mcpParams.AuthorizationConfirmed,
			}
		}
	}
	metadata := map[string]any{"task_type": taskType}
	if taskType != "ai_infra_scan" {
		return metadata
	}
	mode, valid := normalizedInfrastructurePortScanMode(params)
	if !valid {
		return metadata
	}
	metadata["port_scan_mode"] = string(mode)
	metadata["port_spec"] = portscan.PortSpec(mode)
	return metadata
}

func decodeExactJSON(raw json.RawMessage, target any) bool {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil || decoder.Decode(&struct{}{}) != io.EOF || !hasExactJSONFieldNames(decoded, reflect.TypeOf(target)) {
		return false
	}

	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func hasExactJSONFieldNames(value any, targetType reflect.Type) bool {
	if value == nil {
		return true
	}
	for targetType.Kind() == reflect.Pointer {
		targetType = targetType.Elem()
	}
	switch targetType.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		fields := exactJSONStructFields(targetType)
		for name, child := range object {
			fieldType, exists := fields[name]
			if !exists || !hasExactJSONFieldNames(child, fieldType) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if !hasExactJSONFieldNames(item, targetType.Elem()) {
				return false
			}
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return false
		}
		for _, child := range object {
			if !hasExactJSONFieldNames(child, targetType.Elem()) {
				return false
			}
		}
	}
	return true
}

func exactJSONStructFields(targetType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, targetType.NumField())
	for index := 0; index < targetType.NumField(); index++ {
		field := targetType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := field.Tag.Get("json")
		if comma := strings.IndexByte(name, ','); comma >= 0 {
			name = name[:comma]
		}
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func validTaskCountry(country string) bool {
	return country == "" || country == "zh" || country == "zh_CN" || country == "en"
}

func validTaskAttachmentIDs(ids []string) bool {
	if len(ids) > MaxTaskAttachmentCount {
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

func validOptionalReference(fields map[string]json.RawMessage, key, value string) bool {
	_, exists := fields[key]
	return !exists || validReference(value)
}

func validReferences(values []string, maximum int) bool {
	if len(values) == 0 || len(values) > maximum {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validReference(value) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validOptionalStrings(values []string, maximumLength int) bool {
	for _, value := range values {
		if !validBoundedString(value, maximumLength) {
			return false
		}
	}
	return true
}

func validReference(value string) bool {
	return validBoundedString(value, MaxTaskReferenceLength)
}

func validBoundedString(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum
}

func (service *Service) dispatch(ctx context.Context, subject identity.Subject, task *Task, claim string) (*Task, error) {
	params := append(json.RawMessage(nil), task.Params...)
	if task.TaskType == "ai_infra_scan" {
		var valid bool
		params, valid = normalizeInfrastructureTaskParams(params)
		if !valid {
			return task, ErrInvalid
		}
	}
	engineTask := EngineTask{
		PlatformTaskID: task.ID, OwnerUsername: task.OwnerUsername, TaskType: task.TaskType,
		Content: task.Content, Params: params, CountryIsoCode: task.CountryIsoCode,
	}
	var attachmentIDs []string
	_ = json.Unmarshal(task.AttachmentRefs, &attachmentIDs)
	if len(attachmentIDs) > 0 {
		if service.attachments == nil {
			return task, ErrInvalid
		}
		resolved, err := service.attachments.ResolveAttached(ctx, task.OwnerUserID, attachmentIDs)
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

func (service *Service) Browse(ctx context.Context, subject identity.Subject, page, pageSize int, filters TaskListFilters) (TaskListResponse, error) {
	query, err := taskListQueryFor(subject)
	if err != nil {
		return TaskListResponse{}, err
	}
	if page < 1 || page > maxTaskPage || pageSize < 1 || pageSize > maxTaskPageSize || page-1 > int(^uint(0)>>1)/pageSize ||
		filters.Status != "" && !isBrowserTaskStatus(filters.Status) ||
		filters.TaskType != "" && !isBrowserTaskType(filters.TaskType) {
		return TaskListResponse{}, ErrInvalid
	}
	query.Status = filters.Status
	query.TaskType = filters.TaskType
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

func isBrowserTaskStatus(status Status) bool {
	switch status {
	case StatusPending, StatusDispatching, StatusRunning, StatusSucceeded, StatusEngineFailed,
		StatusDispatchFailed, StatusDispatchUnknown, StatusCancelled:
		return true
	default:
		return false
	}
}

func isBrowserTaskType(taskType string) bool {
	switch taskType {
	case "mcp_scan", "ai_infra_scan", "model_redteam_report", "agent_scan":
		return true
	default:
		return false
	}
}

func browserStoredTaskTypes(taskType string) []string {
	switch taskType {
	case "mcp_scan":
		return []string{"mcp_scan", "Mcp-Scan"}
	case "ai_infra_scan":
		return []string{"ai_infra_scan", "AI-Infra-Scan"}
	case "model_redteam_report":
		return []string{"model_redteam_report", "Model-Redteam-Report"}
	case "agent_scan":
		return []string{"agent_scan", "Agent-Scan"}
	default:
		return nil
	}
}

func (service *Service) Recent(ctx context.Context, subject identity.Subject, limit int) ([]TaskSummary, error) {
	query, err := taskListQueryFor(subject)
	if err != nil {
		return nil, err
	}
	if limit < 1 || limit > 5 {
		return nil, ErrInvalid
	}
	repository, ok := service.repository.(RecentRepository)
	if !ok {
		return nil, ErrInvalid
	}
	query.Limit = limit
	recent, err := repository.ListRecent(ctx, query)
	if err != nil {
		return nil, err
	}
	items := make([]TaskSummary, 0, len(recent))
	for index := range recent {
		items = append(items, taskSummaryOf(&recent[index]))
	}
	return items, nil
}

func (service *Service) DashboardTaskSucceeded(ctx context.Context, taskID, ownerUserID string) (bool, error) {
	if taskID == "" || ownerUserID == "" {
		return false, nil
	}
	repository, ok := service.repository.(dashboardTaskStatusRepository)
	if !ok {
		return false, ErrInvalid
	}
	return repository.DashboardTaskSucceeded(ctx, taskID, ownerUserID)
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
		if subject.Role == identity.RoleUser {
			return ErrNotFound
		}
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
		snapshot, resultErr = service.reportSnapshots.Prepare(ctx, completedTaskForReport(task, rawResult, completedAt))
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
	return completedTaskForReport(task, rawResult, engineStatus.CompletedAt.UTC()), nil
}

func completedTaskForReport(task *Task, rawResult json.RawMessage, completedAt time.Time) reports.CompletedTask {
	completed := reports.CompletedTask{
		TaskID: task.ID, OwnerUserID: task.OwnerUserID, TaskType: task.TaskType,
		RawResult: append(json.RawMessage(nil), rawResult...), CompletedAt: completedAt.UTC(),
	}
	mode, spec, valid := trustedReportInfrastructurePortScan(task.TaskType, task.Params)
	if valid {
		completed.PortScanMode = mode
		completed.PortSpec = spec
	}
	return completed
}

func trustedReportInfrastructurePortScan(taskType string, raw json.RawMessage) (portscan.Mode, string, bool) {
	if taskType != "ai_infra_scan" || !hasUniqueJSONObjectKeys(raw) {
		return "", "", false
	}
	_, fields, valid := decodeInfrastructureTaskParams(raw)
	if !valid {
		return "", "", false
	}
	mode, valid := normalizedInfrastructurePortScanModeField(fields)
	if !valid {
		return "", "", false
	}
	spec := portscan.PortSpec(mode)
	if spec == "" {
		return "", "", false
	}
	return mode, spec, true
}

func hasUniqueJSONObjectKeys(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	start, ok := token.(json.Delim)
	if !ok || start != '{' {
		return false
	}

	keys := make(map[string]struct{})
	for decoder.More() {
		token, err = decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return false
		}
		if _, exists := keys[key]; exists {
			return false
		}
		keys[key] = struct{}{}

		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
	}

	token, err = decoder.Token()
	end, ok := token.(json.Delim)
	if err != nil || !ok || end != '}' {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
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
	mu            sync.Mutex
	tasks         map[string]*Task
	byOwner       map[string]string
	attachments   map[string]*Attachment
	createLocksMu sync.Mutex
	createLocks   map[string]*memoryCreateLock
}

type memoryCreateLock struct {
	gate       chan struct{}
	references int
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

func (repository *GormRepository) WithinCreateKeyLock(
	ctx context.Context,
	ownerUserID string,
	idempotencyKey string,
	apply func(context.Context) error,
) error {
	if ownerUserID == "" || idempotencyKey == "" {
		return ErrInvalid
	}
	lockDigest := uuid.NewSHA1(taskIDNamespace, []byte(ownerUserID+"\x00"+idempotencyKey)).String()
	return repository.withinSessionLock(
		ctx, "platform-task-create:"+lockDigest, "释放任务创建锁失败", apply,
	)
}

// WithinEngineEventLock serializes one trusted engine session across service instances.
func (repository *GormRepository) WithinEngineEventLock(ctx context.Context, engineSessionID string, apply func(context.Context) error) error {
	return repository.withinSessionLock(ctx, "platform-task-engine-event:"+engineSessionID, "释放任务事件锁失败", apply)
}

// withinSessionLock keeps validation and its following audited transaction on
// one physical connection, so PostgreSQL serializes the key across instances.
func (repository *GormRepository) withinSessionLock(
	ctx context.Context,
	lockKey string,
	releaseError string,
	apply func(context.Context) error,
) (resultErr error) {
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
			resultErr = errors.New(releaseError)
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
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	if query.TaskType != "" {
		db = db.Where("task_type IN ?", browserStoredTaskTypes(query.TaskType))
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

func (repository *GormRepository) ListRecent(ctx context.Context, query TaskListQuery) ([]Task, error) {
	db := txcontext.Gorm(ctx, repository.db).Model(&Task{}).
		Select("id", "owner_username", "task_type", "status", "created_at", "updated_at").
		Order("updated_at DESC, id DESC").Limit(query.Limit)
	if query.OwnerUserID != "" {
		db = db.Where("owner_user_id = ?", query.OwnerUserID)
	}
	var recent []Task
	if err := db.Find(&recent).Error; err != nil {
		return nil, err
	}
	return recent, nil
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

func (repository *GormRepository) BindReadyAttachments(ctx context.Context, owner string, ids []string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	result := txcontext.Gorm(ctx, repository.db).Model(&Attachment{}).
		Where("owner_user_id = ? AND id IN ? AND state = ?", owner, ids, AttachmentStateReady).
		Updates(map[string]any{"state": AttachmentStateAttached, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return ErrAttachmentNotReady
	}
	return nil
}

func (repository *GormRepository) ListAttachmentCleanupCandidates(ctx context.Context, before time.Time, limit int) ([]Attachment, error) {
	if limit <= 0 || limit > expiredUploadBatchSize {
		limit = expiredUploadBatchSize
	}
	var attachments []Attachment
	err := txcontext.Gorm(ctx, repository.db).
		Where("(state IN ? AND updated_at < ?) OR state = ?", []AttachmentState{AttachmentStateUploading, AttachmentStateReady}, before, AttachmentStateDeleting).
		Order("updated_at ASC, id ASC").Limit(limit).Find(&attachments).Error
	return attachments, err
}

func (repository *GormRepository) MarkAttachmentDeleting(ctx context.Context, id, owner string, before, now time.Time) (bool, error) {
	query := txcontext.Gorm(ctx, repository.db).Model(&Attachment{}).
		Where("id = ? AND owner_user_id = ?", id, owner)
	if !before.IsZero() {
		query = query.Where("(state IN ? AND updated_at < ?) OR state = ?", []AttachmentState{AttachmentStateUploading, AttachmentStateReady}, before, AttachmentStateDeleting)
	} else {
		query = query.Where("state IN ?", []AttachmentState{AttachmentStateUploading, AttachmentStateReady, AttachmentStateDeleting})
	}
	result := query.Updates(map[string]any{"state": AttachmentStateDeleting, "updated_at": now})
	return result.RowsAffected == 1, result.Error
}

func (repository *GormRepository) DeleteMarkedAttachment(ctx context.Context, id, owner string) (bool, error) {
	result := txcontext.Gorm(ctx, repository.db).
		Where("id = ? AND owner_user_id = ? AND state = ?", id, owner, AttachmentStateDeleting).
		Delete(&Attachment{})
	return result.RowsAffected == 1, result.Error
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
	ErrAttachmentTooLarge      = errors.New("附件超过大小限制")
	ErrAttachmentSizeMismatch  = errors.New("附件大小不匹配")
	ErrAttachmentNotReady      = errors.New("附件尚未就绪")
	ErrAttachmentStorage       = errors.New("附件存储暂时不可用")
	errAttachmentDownloadAudit = errors.New("无法持久化附件下载授权审计")
)

const (
	defaultMaxFileBytes    int64 = 50 << 20
	defaultMaxChunkBytes   int64 = 5 << 20
	defaultUploadTTL             = 24 * time.Hour
	expiredUploadBatchSize       = 100
)

type AttachmentConfig struct {
	UploadDir     string
	MaxFileBytes  int64
	MaxChunkBytes int64
	UploadTTL     time.Duration
}

func LoadAttachmentConfigFromEnv(uploadDir string) (AttachmentConfig, error) {
	config := AttachmentConfig{UploadDir: uploadDir, MaxFileBytes: defaultMaxFileBytes, MaxChunkBytes: defaultMaxChunkBytes, UploadTTL: defaultUploadTTL}
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
	if value := strings.TrimSpace(os.Getenv("AIG_ATTACHMENT_UPLOAD_TTL")); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed <= 0 {
			return AttachmentConfig{}, errors.New("AIG_ATTACHMENT_UPLOAD_TTL must be a positive duration")
		}
		config.UploadTTL = parsed
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
	BindReadyAttachments(context.Context, string, []string, time.Time) error
	ListAttachmentCleanupCandidates(context.Context, time.Time, int) ([]Attachment, error)
	MarkAttachmentDeleting(context.Context, string, string, time.Time, time.Time) (bool, error)
	DeleteMarkedAttachment(context.Context, string, string) (bool, error)
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
	openFile   func(string) (*os.File, error)
	removeFile func(string) error
	removeTree func(string) error
}

func NewAttachmentService(repository PlatformTaskAttachmentRepository, config AttachmentConfig, audits audit.Recorder) (*AttachmentService, error) {
	if config.UploadTTL == 0 {
		config.UploadTTL = defaultUploadTTL
	}
	if repository == nil || audits == nil || strings.TrimSpace(config.UploadDir) == "" || config.MaxFileBytes <= 0 ||
		config.MaxChunkBytes <= 0 || config.MaxChunkBytes > config.MaxFileBytes || config.UploadTTL <= 0 {
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
	return &AttachmentService{
		repository: repository, tasks: repository, audits: audits, config: config,
		now: func() time.Time { return time.Now().UTC() }, openFile: os.Open,
		removeFile: os.Remove, removeTree: os.RemoveAll,
	}, nil
}

func (service *AttachmentService) MaxFileBytes() int64 { return service.config.MaxFileBytes }

func (service *AttachmentService) maxChunkCount() int64 {
	count := service.config.MaxFileBytes / service.config.MaxChunkBytes
	if service.config.MaxFileBytes%service.config.MaxChunkBytes != 0 {
		count++
	}
	return count
}

func (service *AttachmentService) PurgeExpiredUploads(ctx context.Context) error {
	before := service.now().Add(-service.config.UploadTTL)
	expired, err := service.repository.ListAttachmentCleanupCandidates(ctx, before, expiredUploadBatchSize)
	if err != nil {
		return err
	}
	for index := range expired {
		attachment := &expired[index]
		marked, markErr := service.repository.MarkAttachmentDeleting(ctx, attachment.ID, attachment.OwnerUserID, before, service.now())
		if markErr != nil {
			return markErr
		}
		if !marked {
			continue
		}
		if cleanupErr := service.cleanupAttachmentFiles(attachment); cleanupErr != nil {
			return cleanupErr
		}
		if _, deleteErr := service.repository.DeleteMarkedAttachment(ctx, attachment.ID, attachment.OwnerUserID); deleteErr != nil {
			return deleteErr
		}
	}
	return nil
}

func (service *AttachmentService) Abort(ctx context.Context, subject identity.Subject, id string) error {
	if subject.Role == identity.RoleAuditor {
		return ErrForbidden
	}
	attachment, err := service.repository.GetAttachment(ctx, id)
	if err != nil {
		return err
	}
	if subject.Role == identity.RoleUser && subject.UserID != attachment.OwnerUserID {
		return ErrNotFound
	}
	if subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin {
		return ErrForbidden
	}
	if attachment.State == AttachmentStateAttached {
		return ErrAttachmentNotReady
	}
	marked, err := service.repository.MarkAttachmentDeleting(ctx, attachment.ID, attachment.OwnerUserID, time.Time{}, service.now())
	if err != nil {
		return err
	}
	if !marked {
		return ErrAttachmentNotReady
	}
	if err := service.cleanupAttachmentFiles(attachment); err != nil {
		return err
	}
	deleted, err := service.repository.DeleteMarkedAttachment(ctx, attachment.ID, attachment.OwnerUserID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNotFound
	}
	return nil
}

func (service *AttachmentService) cleanupAttachmentFiles(attachment *Attachment) error {
	for _, storageName := range []string{
		attachment.StorageName,
		attachment.StorageName + ".uploading",
		attachment.StorageName + ".merging",
	} {
		storagePath, err := service.storagePath(storageName)
		if err != nil {
			return err
		}
		if err := service.removeFile(storagePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrAttachmentStorage
		}
	}
	chunkDir, err := service.storagePath(".chunks", attachment.ID)
	if err != nil {
		return err
	}
	if err := service.removeTree(chunkDir); err != nil {
		return ErrAttachmentStorage
	}
	return nil
}

func (service *AttachmentService) discardUnboundAttachment(ctx context.Context, attachment *Attachment) error {
	marked, err := service.repository.MarkAttachmentDeleting(ctx, attachment.ID, attachment.OwnerUserID, time.Time{}, service.now())
	if err != nil {
		return err
	}
	if !marked {
		return ErrAttachmentNotReady
	}
	if err := service.cleanupAttachmentFiles(attachment); err != nil {
		return err
	}
	deleted, err := service.repository.DeleteMarkedAttachment(ctx, attachment.ID, attachment.OwnerUserID)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrNotFound
	}
	return nil
}

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
	if err := service.PurgeExpiredUploads(ctx); err != nil {
		return AttachmentView{}, err
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
	now := service.now()
	attachment := &Attachment{
		ID: id, OwnerUserID: subject.UserID, OriginalName: filename, StorageName: storageName,
		State: AttachmentStateUploading, CreatedAt: now, UpdatedAt: now,
	}
	if err := service.repository.CreateAttachment(ctx, attachment); err != nil {
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, id, err)
	}
	failUpload := func(businessErr error) (AttachmentView, error) {
		if cleanupErr := service.discardUnboundAttachment(ctx, attachment); cleanupErr != nil {
			return AttachmentView{}, failAttachmentMutation(ctx, mutation, id, ErrAttachmentStorage)
		}
		return AttachmentView{}, failAttachmentMutation(ctx, mutation, id, businessErr)
	}
	temporary := path + ".uploading"
	written, err := copyBoundedFile(temporary, source, service.config.MaxFileBytes)
	if err != nil {
		return failUpload(err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return failUpload(errors.New("无法保存附件"))
	}
	readyAt := service.now()
	var readyErr error
	err = mutation.Run(ctx, id, map[string]any{"size": written}, func(transactionContext context.Context) error {
		readyErr = service.repository.MarkAttachmentReady(transactionContext, attachment.ID, attachment.OwnerUserID, written, readyAt)
		return readyErr
	})
	if readyErr != nil {
		if cleanupErr := service.discardUnboundAttachment(ctx, attachment); cleanupErr != nil {
			return AttachmentView{}, ErrAttachmentStorage
		}
		return AttachmentView{}, readyErr
	}
	if err != nil {
		if cleanupErr := service.discardUnboundAttachment(ctx, attachment); cleanupErr != nil {
			return AttachmentView{}, ErrAttachmentStorage
		}
		return AttachmentView{}, err
	}
	attachment.State, attachment.Size, attachment.ChunkBytes, attachment.UpdatedAt = AttachmentStateReady, written, written, readyAt
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
	if err := service.PurgeExpiredUploads(ctx); err != nil {
		return AttachmentView{}, err
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
	if source == nil || index < 0 || int64(index) >= service.maxChunkCount() {
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
	if written == 0 {
		_ = os.Remove(chunkPath)
		return failAttachmentMutation(ctx, mutation, attachment.ID, ErrInvalid)
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
	if totalChunks <= 0 || int64(totalChunks) > service.maxChunkCount() || declaredSize <= 0 || declaredSize > service.config.MaxFileBytes {
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
	if subject.Role == identity.RoleAuditor {
		return nil, "", 0, ErrForbidden
	}
	attachment, err := service.repository.GetAttachment(ctx, id)
	if err != nil {
		return nil, "", 0, err
	}
	if subject.Role == identity.RoleUser && subject.UserID != attachment.OwnerUserID {
		return nil, "", 0, ErrNotFound
	}
	if subject.Role != identity.RoleUser && subject.Role != identity.RoleAdmin {
		return nil, "", 0, ErrForbidden
	}
	if attachment.State != AttachmentStateReady && attachment.State != AttachmentStateAttached {
		return nil, "", 0, ErrAttachmentNotReady
	}
	path, err := service.storagePath(attachment.StorageName)
	if err != nil {
		return nil, "", 0, err
	}
	var preOpenInfo os.FileInfo
	if subject.Role == identity.RoleAdmin && subject.UserID != attachment.OwnerUserID {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, "", 0, classifyAttachmentStorageError(statErr)
		}
		if !info.Mode().IsRegular() {
			return nil, "", 0, ErrAttachmentStorage
		}
		preOpenInfo = info
		metadata := map[string]any{
			"attachment_id":     attachment.ID,
			"owner_user_id":     attachment.OwnerUserID,
			"governance_action": "cross_owner_download_authorized",
		}
		// This synchronous success records durable governance authorization,
		// not downstream file-stream delivery. The authorization must precede
		// Storage Open; later I/O failures return no bytes and do not change it.
		if auditErr := service.audits.Record(ctx, subject, audit.EventInput{
			Action: audit.ActionAttachmentDownloadAuthorized, ResourceType: "attachment", ResourceID: attachment.ID,
			Outcome: audit.OutcomeSuccess, Metadata: metadata,
		}); auditErr != nil {
			return nil, "", 0, errAttachmentDownloadAudit
		}
	}
	file, err := service.openFile(path)
	if err != nil {
		return nil, "", 0, classifyAttachmentStorageError(err)
	}
	if preOpenInfo != nil {
		postOpenInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return nil, "", 0, classifyAttachmentStorageError(statErr)
		}
		if !postOpenInfo.Mode().IsRegular() || !os.SameFile(preOpenInfo, postOpenInfo) {
			_ = file.Close()
			return nil, "", 0, ErrAttachmentStorage
		}
	}
	return file, attachment.OriginalName, attachment.Size, nil
}

func classifyAttachmentStorageError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return ErrAttachmentStorage
}

func (service *AttachmentService) ResolveReady(ctx context.Context, ownerUserID string, ids []string) ([]string, error) {
	return service.resolveWithState(ctx, ownerUserID, ids, AttachmentStateReady)
}

func (service *AttachmentService) ResolveAttached(ctx context.Context, ownerUserID string, ids []string) ([]string, error) {
	return service.resolveWithState(ctx, ownerUserID, ids, AttachmentStateAttached)
}

// ReadReadyTargetExpressions reads ready, owner-scoped target-list attachments without exposing storage paths.
func (service *AttachmentService) ReadReadyTargetExpressions(ctx context.Context, ownerUserID string, ids []string, expressions []string) ([]string, error) {
	if _, err := service.ResolveReady(ctx, ownerUserID, ids); err != nil {
		return nil, err
	}
	for _, id := range ids {
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
		if attachment.Size > maxInfrastructureTargetAttachmentBytes {
			return nil, ErrAttachmentTooLarge
		}
		path, err := service.storagePath(attachment.StorageName)
		if err != nil {
			return nil, err
		}
		preOpenInfo, err := os.Lstat(path)
		if err != nil {
			return nil, classifyAttachmentStorageError(err)
		}
		if !preOpenInfo.Mode().IsRegular() {
			return nil, ErrAttachmentStorage
		}
		file, err := service.openFile(path)
		if err != nil {
			return nil, classifyAttachmentStorageError(err)
		}
		postOpenInfo, statErr := file.Stat()
		if statErr != nil || !postOpenInfo.Mode().IsRegular() || !os.SameFile(preOpenInfo, postOpenInfo) {
			_ = file.Close()
			if statErr != nil {
				return nil, classifyAttachmentStorageError(statErr)
			}
			return nil, ErrAttachmentStorage
		}
		contents, readErr := io.ReadAll(io.LimitReader(file, maxInfrastructureTargetAttachmentBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return nil, ErrAttachmentStorage
		}
		if int64(len(contents)) > maxInfrastructureTargetAttachmentBytes {
			return nil, ErrAttachmentTooLarge
		}
		if int64(len(contents)) != attachment.Size {
			return nil, ErrAttachmentStorage
		}
		if !utf8.Valid(contents) {
			return nil, ErrInvalid
		}
		expressions, err = runner.AppendTargetExpressionLines(expressions, string(contents))
		if err != nil {
			return nil, ErrInvalid
		}
	}
	return expressions, nil
}

func (service *AttachmentService) resolveWithState(ctx context.Context, ownerUserID string, ids []string, state AttachmentState) ([]string, error) {
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
		if attachment.State != state {
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

func canWriteAttachment(subject identity.Subject, attachment *Attachment) bool {
	return subject.Role == identity.RoleAdmin ||
		subject.Role == identity.RoleUser && subject.UserID == attachment.OwnerUserID
}

func attachmentView(attachment *Attachment) AttachmentView {
	state := attachment.State
	if state == AttachmentStateAttached {
		state = AttachmentStateReady
	}
	return AttachmentView{ID: attachment.ID, Filename: attachment.OriginalName, Size: attachment.Size, State: state, CreatedAt: attachment.CreatedAt}
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		tasks: map[string]*Task{}, byOwner: map[string]string{}, attachments: map[string]*Attachment{},
		createLocks: map[string]*memoryCreateLock{},
	}
}

func cloneTask(task *Task) *Task {
	copy := *task
	copy.Params = append(json.RawMessage(nil), task.Params...)
	copy.AttachmentRefs = append(json.RawMessage(nil), task.AttachmentRefs...)
	return &copy
}

func (repository *MemoryRepository) WithinCreateKeyLock(
	ctx context.Context,
	ownerUserID string,
	idempotencyKey string,
	apply func(context.Context) error,
) error {
	if repository == nil || ownerUserID == "" || idempotencyKey == "" || apply == nil {
		return ErrInvalid
	}
	key := ownerUserID + "\x00" + idempotencyKey
	repository.createLocksMu.Lock()
	if repository.createLocks == nil {
		repository.createLocks = map[string]*memoryCreateLock{}
	}
	lock := repository.createLocks[key]
	if lock == nil {
		lock = &memoryCreateLock{gate: make(chan struct{}, 1)}
		lock.gate <- struct{}{}
		repository.createLocks[key] = lock
	}
	lock.references++
	repository.createLocksMu.Unlock()

	acquired := false
	select {
	case <-ctx.Done():
	case <-lock.gate:
		acquired = true
	}
	if !acquired {
		repository.releaseCreateKeyLock(key, lock, false)
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		repository.releaseCreateKeyLock(key, lock, true)
		return err
	}
	defer repository.releaseCreateKeyLock(key, lock, true)
	return apply(ctx)
}

func (repository *MemoryRepository) releaseCreateKeyLock(key string, lock *memoryCreateLock, acquired bool) {
	if acquired {
		lock.gate <- struct{}{}
	}
	repository.createLocksMu.Lock()
	lock.references--
	if lock.references == 0 {
		delete(repository.createLocks, key)
	}
	repository.createLocksMu.Unlock()
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
		if query.OwnerUserID != "" && task.OwnerUserID != query.OwnerUserID {
			continue
		}
		if query.Status != "" && task.Status != query.Status {
			continue
		}
		if query.TaskType != "" && canonicalTaskType(task.TaskType) != query.TaskType {
			continue
		}
		tasks = append(tasks, *cloneTask(task))
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

func (repository *MemoryRepository) ListRecent(_ context.Context, query TaskListQuery) ([]Task, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	recent := make([]Task, 0, len(repository.tasks))
	for _, task := range repository.tasks {
		if query.OwnerUserID == "" || task.OwnerUserID == query.OwnerUserID {
			recent = append(recent, Task{
				ID: task.ID, OwnerUsername: task.OwnerUsername, TaskType: task.TaskType, Status: task.Status,
				CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
			})
		}
	}
	sort.Slice(recent, func(left, right int) bool {
		if recent[left].UpdatedAt.Equal(recent[right].UpdatedAt) {
			return recent[left].ID > recent[right].ID
		}
		return recent[left].UpdatedAt.After(recent[right].UpdatedAt)
	})
	if query.Limit > 0 && len(recent) > query.Limit {
		recent = recent[:query.Limit]
	}
	return recent, nil
}

func (repository *MemoryRepository) DashboardTaskSucceeded(_ context.Context, taskID, ownerUserID string) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	task, exists := repository.tasks[taskID]
	return exists && task.OwnerUserID == ownerUserID && task.Status == StatusSucceeded, nil
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

func (repository *MemoryRepository) BindReadyAttachments(_ context.Context, owner string, ids []string, now time.Time) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, id := range ids {
		attachment, exists := repository.attachments[id]
		if !exists || attachment.OwnerUserID != owner || attachment.State != AttachmentStateReady {
			return ErrAttachmentNotReady
		}
	}
	for _, id := range ids {
		attachment := repository.attachments[id]
		attachment.State = AttachmentStateAttached
		attachment.UpdatedAt = now
	}
	return nil
}

func (repository *MemoryRepository) ListAttachmentCleanupCandidates(_ context.Context, before time.Time, limit int) ([]Attachment, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if limit <= 0 || limit > expiredUploadBatchSize {
		limit = expiredUploadBatchSize
	}
	attachments := make([]Attachment, 0, limit)
	for _, attachment := range repository.attachments {
		staleUnbound := (attachment.State == AttachmentStateUploading || attachment.State == AttachmentStateReady) && attachment.UpdatedAt.Before(before)
		if staleUnbound || attachment.State == AttachmentStateDeleting {
			attachments = append(attachments, *cloneAttachment(attachment))
		}
	}
	sort.Slice(attachments, func(left, right int) bool {
		if attachments[left].UpdatedAt.Equal(attachments[right].UpdatedAt) {
			return attachments[left].ID < attachments[right].ID
		}
		return attachments[left].UpdatedAt.Before(attachments[right].UpdatedAt)
	})
	if len(attachments) > limit {
		attachments = attachments[:limit]
	}
	return attachments, nil
}

func (repository *MemoryRepository) MarkAttachmentDeleting(_ context.Context, id, owner string, before, now time.Time) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	attachment, exists := repository.attachments[id]
	if !exists || attachment.OwnerUserID != owner || attachment.State == AttachmentStateAttached {
		return false, nil
	}
	if !before.IsZero() && attachment.State != AttachmentStateDeleting &&
		!((attachment.State == AttachmentStateUploading || attachment.State == AttachmentStateReady) && attachment.UpdatedAt.Before(before)) {
		return false, nil
	}
	if before.IsZero() && attachment.State != AttachmentStateUploading && attachment.State != AttachmentStateReady && attachment.State != AttachmentStateDeleting {
		return false, nil
	}
	attachment.State = AttachmentStateDeleting
	attachment.UpdatedAt = now
	return true, nil
}

func (repository *MemoryRepository) DeleteMarkedAttachment(_ context.Context, id, owner string) (bool, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	attachment, exists := repository.attachments[id]
	if !exists || attachment.OwnerUserID != owner || attachment.State != AttachmentStateDeleting {
		return false, nil
	}
	delete(repository.attachments, id)
	return true, nil
}

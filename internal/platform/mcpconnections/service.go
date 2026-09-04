package mcpconnections

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/google/uuid"
)

var (
	ErrForbidden                 = errors.New("MCP 连接操作无权访问")
	ErrTaskConnectionUnavailable = errors.New("MCP 连接当前不可用于创建任务")
)

// ConnectionRepository 是连接服务使用的最小持久化边界。Task 4 的任务 UoW
// 不属于该接口，避免服务层在本任务中越过其事务和绑定职责。
type ConnectionRepository interface {
	Create(context.Context, *ConnectionConfig, *ConnectionVersion) error
	GetConfig(context.Context, string) (*ConnectionConfig, error)
	GetVersion(context.Context, string, int) (*ConnectionVersion, error)
	ListConfigs(context.Context) ([]ConnectionConfig, error)
	RecordProbeResult(context.Context, string, int, Transport, ProbeStatus) error
	SetEnabled(context.Context, string, bool) (*ConnectionConfig, error)
}

type Service struct {
	repository ConnectionRepository
	keyring    *Keyring
	prober     *ProbeEngine
	policy     *OutboundPolicy
	now        func() time.Time
	newID      func() string
}

func NewService(repository ConnectionRepository, keyring *Keyring, prober *ProbeEngine, policy *OutboundPolicy) *Service {
	return &Service{
		repository: repository,
		keyring:    keyring,
		prober:     prober,
		policy:     policy,
		now:        func() time.Time { return time.Now().UTC() },
		newID:      uuid.NewString,
	}
}

// Create 只保存 disabled/not_tested 的新版本。即使浏览器伪造已测试或 enabled
// 字段也无效；实际可用性只能由受控 gateway 完成 probe 后再启用。
func (service *Service) Create(ctx context.Context, subject identity.Subject, input CreateConnectionInput) (*ConnectionSummary, error) {
	if service == nil || service.repository == nil || service.keyring == nil {
		return nil, ErrInvalid
	}
	if !canCreate(subject, input.Scope) {
		return nil, ErrForbidden
	}
	if !validCreateInput(input) {
		return nil, ErrInvalid
	}
	if service.policy == nil || service.policy.ValidateServerURL(ctx, input.ServerURL) != nil {
		return nil, ErrOutboundDenied
	}
	now := service.now()
	config := &ConnectionConfig{
		ID:               service.newID(),
		OwnerUserID:      subject.UserID,
		Scope:            input.Scope,
		Name:             strings.TrimSpace(input.Name),
		Description:      strings.TrimSpace(input.Description),
		CurrentVersion:   1,
		ResourceRevision: "1",
		Enabled:          false,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	version := &ConnectionVersion{
		ID:                 service.newID(),
		ConnectionConfigID: config.ID,
		Version:            1,
		Transport:          input.Transport,
		ProbeStatus:        ProbeStatusNotTested,
		CreatedAt:          now,
	}
	payload := ConnectionPayload{Endpoint: strings.TrimSpace(input.ServerURL), Authentication: input.Authentication, Headers: cloneHeaders(input.Headers)}
	if err := service.keyring.SealConnectionPayload(config, version, payload); err != nil {
		return nil, ErrInvalid
	}
	if err := service.repository.Create(ctx, config, version); err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	summary := summaryOf(config, version)
	return &summary, nil
}

func (service *Service) List(ctx context.Context, subject identity.Subject) ([]ConnectionSummary, error) {
	if service == nil || service.repository == nil || !validReader(subject) {
		return nil, ErrForbidden
	}
	configs, err := service.repository.ListConfigs(ctx)
	if err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	items := make([]ConnectionSummary, 0, len(configs))
	for index := range configs {
		config := configs[index]
		if !canRead(subject, &config) {
			continue
		}
		version, getErr := service.repository.GetVersion(ctx, config.ID, config.CurrentVersion)
		if getErr != nil {
			return nil, mapServiceRepositoryError(getErr)
		}
		items = append(items, summaryOf(&config, version))
	}
	return items, nil
}

func (service *Service) GetSummary(ctx context.Context, subject identity.Subject, configID string) (*ConnectionSummary, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return nil, err
	}
	summary := summaryOf(config, version)
	return &summary, nil
}

func (service *Service) GetManagementDetail(ctx context.Context, subject identity.Subject, configID string) (*ConnectionManagementDetail, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return nil, err
	}
	if !canManage(subject, config) {
		return nil, ErrForbidden
	}
	if service.keyring == nil {
		return nil, ErrInvalid
	}
	payload, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return nil, ErrInvalid
	}
	detail := ConnectionManagementDetail{
		ConnectionSummary:        summaryOf(config, version),
		EndpointConfigured:       strings.TrimSpace(payload.Endpoint) != "",
		AuthenticationConfigured: payload.Authentication.Kind != AuthenticationNone && strings.TrimSpace(payload.Authentication.Secret) != "",
		AuthenticationKind:       payload.Authentication.Kind,
		CustomHeadersConfigured:  len(payload.Headers) > 0,
	}
	return &detail, nil
}

// Probe 不存储失败响应或上游错误。只有当前版本的最小 initialize 成功后，才落库
// 已探测的具体 transport；这使旧成功记录不能让新版本获得可选资格。
func (service *Service) Probe(ctx context.Context, subject identity.Subject, configID string) (*ConnectionSummary, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return nil, err
	}
	if !canManage(subject, config) {
		return nil, ErrForbidden
	}
	if service.policy == nil || service.policy.RequireControlledDialer() != nil {
		return nil, ErrControlledEgressRequired
	}
	if service.keyring == nil || service.prober == nil {
		return nil, ErrProbeFailed
	}
	payload, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return nil, ErrProbeFailed
	}
	result, err := service.prober.Probe(ctx, payload, version.Transport)
	if err != nil {
		if errors.Is(err, ErrProbeRateLimited) {
			return nil, ErrProbeRateLimited
		}
		return nil, ErrProbeFailed
	}
	if err := service.repository.RecordProbeResult(ctx, config.ID, version.Version, result.DetectedTransport, ProbeStatusPassed); err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	version.DetectedTransport = result.DetectedTransport
	version.ProbeStatus = ProbeStatusPassed
	summary := summaryOf(config, version)
	return &summary, nil
}

func (service *Service) SetEnabled(ctx context.Context, subject identity.Subject, configID string, enabled bool) (*ConnectionSummary, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return nil, err
	}
	if !canManage(subject, config) {
		return nil, ErrForbidden
	}
	if enabled {
		if service.policy == nil || service.policy.RequireControlledDialer() != nil {
			return nil, ErrControlledEgressRequired
		}
		if version.ProbeStatus != ProbeStatusPassed || !concreteProbeTransport(version.DetectedTransport) {
			return nil, ErrTaskConnectionUnavailable
		}
	}
	updated, err := service.repository.SetEnabled(ctx, config.ID, enabled)
	if err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	summary := summaryOf(updated, version)
	return &summary, nil
}

// TaskOptions 是未来 Task 4 的只读输入。缺少受控 dialer 时返回空集而非把未受
// 验证配置泄露给浏览器；实际创建前仍必须调用 ValidateTaskConnection。
func (service *Service) TaskOptions(ctx context.Context, subject identity.Subject) ([]TaskConnectionOption, error) {
	if service == nil || service.repository == nil || !validReader(subject) {
		return nil, ErrForbidden
	}
	if service.policy == nil || service.policy.RequireControlledDialer() != nil {
		return []TaskConnectionOption{}, nil
	}
	configs, err := service.repository.ListConfigs(ctx)
	if err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	options := make([]TaskConnectionOption, 0, len(configs))
	for index := range configs {
		config := &configs[index]
		if !canRead(subject, config) || !config.Enabled {
			continue
		}
		version, getErr := service.repository.GetVersion(ctx, config.ID, config.CurrentVersion)
		if getErr != nil {
			return nil, mapServiceRepositoryError(getErr)
		}
		if version.ProbeStatus != ProbeStatusPassed || !concreteProbeTransport(version.DetectedTransport) {
			continue
		}
		options = append(options, TaskConnectionOption{
			ConnectionID:      config.ID,
			ConnectionVersion: version.Version,
			Name:              config.Name,
			Description:       config.Description,
			Scope:             config.Scope,
			Transport:         version.DetectedTransport,
		})
	}
	return options, nil
}

// ValidateTaskConnection 仅执行未来任务创建需要的资格验证；它不创建任务、绑定或
// UoW，避免在 Task 3 越过 Task 4 的持久化职责。
func (service *Service) ValidateTaskConnection(ctx context.Context, subject identity.Subject, configID string) error {
	if service == nil || service.policy == nil || service.policy.RequireControlledDialer() != nil {
		return ErrControlledEgressRequired
	}
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return err
	}
	if !config.Enabled || version.ProbeStatus != ProbeStatusPassed || !concreteProbeTransport(version.DetectedTransport) {
		return ErrTaskConnectionUnavailable
	}
	return nil
}

func (service *Service) visibleCurrentVersion(ctx context.Context, subject identity.Subject, configID string) (*ConnectionConfig, *ConnectionVersion, error) {
	if service == nil || service.repository == nil || !validReader(subject) || strings.TrimSpace(configID) == "" {
		return nil, nil, ErrNotFound
	}
	config, err := service.repository.GetConfig(ctx, configID)
	if err != nil {
		return nil, nil, mapServiceRepositoryError(err)
	}
	if !canRead(subject, config) {
		return nil, nil, ErrNotFound
	}
	version, err := service.repository.GetVersion(ctx, config.ID, config.CurrentVersion)
	if err != nil {
		return nil, nil, mapServiceRepositoryError(err)
	}
	return config, version, nil
}

func summaryOf(config *ConnectionConfig, version *ConnectionVersion) ConnectionSummary {
	return ConnectionSummary{
		ID:                config.ID,
		Name:              config.Name,
		Description:       config.Description,
		Scope:             config.Scope,
		CurrentVersion:    config.CurrentVersion,
		Enabled:           config.Enabled,
		Transport:         version.Transport,
		DetectedTransport: version.DetectedTransport,
		ProbeStatus:       version.ProbeStatus,
	}
}

func canCreate(subject identity.Subject, scope Scope) bool {
	if subject.UserID == "" {
		return false
	}
	switch subject.Role {
	case identity.RoleAdmin:
		return scope == ScopePrivate || scope == ScopeGlobal
	case identity.RoleUser:
		return scope == ScopePrivate
	default:
		return false
	}
}

func validReader(subject identity.Subject) bool {
	switch subject.Role {
	case identity.RoleAdmin, identity.RoleAuditor:
		return true
	case identity.RoleUser:
		return strings.TrimSpace(subject.UserID) != ""
	default:
		return false
	}
}

func canRead(subject identity.Subject, config *ConnectionConfig) bool {
	if config == nil || !validReader(subject) {
		return false
	}
	if subject.Role == identity.RoleAdmin {
		return true
	}
	if config.Scope == ScopeGlobal {
		return true
	}
	return subject.Role == identity.RoleUser && config.Scope == ScopePrivate && subject.UserID == config.OwnerUserID
}

func canManage(subject identity.Subject, config *ConnectionConfig) bool {
	if config == nil {
		return false
	}
	if subject.Role == identity.RoleAdmin {
		return true
	}
	return subject.Role == identity.RoleUser && config.Scope == ScopePrivate && subject.UserID != "" && subject.UserID == config.OwnerUserID
}

func validCreateInput(input CreateConnectionInput) bool {
	if strings.TrimSpace(input.Name) == "" || input.Scope != ScopePrivate && input.Scope != ScopeGlobal || !configurableTransport(input.Transport) || strings.TrimSpace(input.ServerURL) == "" {
		return false
	}
	for _, header := range input.Headers {
		if strings.TrimSpace(header.Name) == "" || strings.ContainsAny(header.Name, "\r\n") {
			return false
		}
	}
	switch input.Authentication.Kind {
	case AuthenticationNone:
		return input.Authentication.Secret == "" && input.Authentication.HeaderName == ""
	case AuthenticationBearer:
		return strings.TrimSpace(input.Authentication.Secret) != "" && input.Authentication.HeaderName == ""
	case AuthenticationAPIKeyHeader:
		return strings.TrimSpace(input.Authentication.Secret) != "" && strings.TrimSpace(input.Authentication.HeaderName) != "" && !strings.ContainsAny(input.Authentication.HeaderName, "\r\n")
	case AuthenticationCustomHeaders:
		return input.Authentication.Secret == "" && input.Authentication.HeaderName == "" && len(input.Headers) > 0
	default:
		return false
	}
}

func configurableTransport(transport Transport) bool {
	return transport == TransportAuto || transport == TransportHTTP || transport == TransportSSE
}

func concreteProbeTransport(transport Transport) bool {
	return transport == TransportHTTP || transport == TransportSSE
}

func cloneHeaders(headers []Header) []Header {
	return append([]Header(nil), headers...)
}

func mapServiceRepositoryError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return ErrInvalid
}

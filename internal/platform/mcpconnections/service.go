package mcpconnections

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/google/uuid"
)

var (
	ErrForbidden                 = errors.New("MCP 连接操作无权访问")
	ErrTaskConnectionUnavailable = errors.New("MCP 连接当前不可用于创建任务")
)

const (
	maxConnectionNameRunes        = 80
	maxConnectionDescriptionRunes = 240
	maxDisplayTokenRunes          = 31
	maxCustomHeaders              = 10
	maxHTTPHeaderNameBytes        = 64
	maxHTTPHeaderValueBytes       = 8 * 1024
)

// ConnectionRepository 是连接服务使用的最小持久化边界。Task 4 的任务 UoW
// 不属于该接口，避免服务层在本任务中越过其事务和绑定职责。
type ConnectionRepository interface {
	Create(context.Context, *ConnectionConfig, *ConnectionVersion) error
	GetConfig(context.Context, string) (*ConnectionConfig, error)
	GetVersion(context.Context, string, int) (*ConnectionVersion, error)
	ListConfigs(context.Context) ([]ConnectionConfig, error)
	StartProbe(context.Context, string, int, string) (*ProbeAttempt, error)
	RecordProbeResult(context.Context, string, int, string, Transport, ProbeStatus) error
	SetEnabled(context.Context, string, int, string, bool) (*ConnectionConfig, error)
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
	payload := connectionPayloadFromInput(input)
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
	if err := service.keyring.SealConnectionPayload(config, version, payload); err != nil {
		return nil, ErrInvalid
	}
	if err := service.repository.Create(ctx, config, version); err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	summary := summaryOf(config, version, &payload)
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
		items = append(items, service.safeSummaryOf(&config, version))
	}
	return items, nil
}

func (service *Service) GetSummary(ctx context.Context, subject identity.Subject, configID string) (*ConnectionSummary, error) {
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return nil, err
	}
	summary := service.safeSummaryOf(config, version)
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
		ConnectionSummary:        summaryOf(config, version, &payload),
		EndpointConfigured:       strings.TrimSpace(payload.Endpoint) != "",
		AuthenticationConfigured: authenticationConfigured(payload),
		AuthenticationKind:       payload.Authentication.Kind,
		CustomHeadersConfigured:  len(payload.Headers) > 0,
	}
	return &detail, nil
}

// Probe 不存储失败响应或上游错误。它先消耗本进程限流配额，再持久化 StartProbe
// token，随后只允许相同 token 的最小 initialize 结果写回；跨 Engine 的迟到
// 成功因此不能覆盖较晚失败或配置变更后的最新状态。
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
	if !configurableTransport(version.Transport) {
		return nil, ErrProbeFailed
	}
	if err := service.prober.reserveAttempt(config.ID); err != nil {
		if errors.Is(err, ErrProbeRateLimited) {
			return nil, ErrProbeRateLimited
		}
		return nil, ErrProbeFailed
	}
	attempt, err := service.repository.StartProbe(ctx, config.ID, version.Version, config.ResourceRevision)
	if err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	if attempt == nil || attempt.ConnectionConfigID != config.ID || attempt.Version != version.Version || strings.TrimSpace(attempt.Token) == "" {
		return nil, ErrProbeFailed
	}
	// StartProbe 已在同一事务中撤销 enabled 并清空旧结论；更新内存副本使成功
	// 回应也不会把开始探测前的 enabled/passed 投影给浏览器。
	config.Enabled = false
	config.ResourceRevision = attempt.Token
	version.DetectedTransport = ""
	version.ProbeStatus = ProbeStatusNotTested
	payload, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return service.recordProbeFailure(ctx, attempt)
	}
	result, err := service.prober.probeReserved(ctx, payload, version.Transport)
	if err != nil {
		return service.recordProbeFailure(ctx, attempt)
	}
	if err := service.repository.RecordProbeResult(ctx, config.ID, version.Version, attempt.Token, result.DetectedTransport, ProbeStatusPassed); err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	version.DetectedTransport = result.DetectedTransport
	version.ProbeStatus = ProbeStatusPassed
	summary := summaryOf(config, version, &payload)
	return &summary, nil
}

// recordProbeFailure 将不带上游详情的失败结论条件写回。仅过期 token 不能写入；
// 此时返回的冲突同样不含端点、Header 或认证材料。
func (service *Service) recordProbeFailure(ctx context.Context, attempt *ProbeAttempt) (*ConnectionSummary, error) {
	if service == nil || service.repository == nil || attempt == nil || strings.TrimSpace(attempt.ConnectionConfigID) == "" || attempt.Version < 1 || strings.TrimSpace(attempt.Token) == "" {
		return nil, ErrProbeFailed
	}
	if err := service.repository.RecordProbeResult(ctx, attempt.ConnectionConfigID, attempt.Version, attempt.Token, "", ProbeStatusFailed); err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	return nil, ErrProbeFailed
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
		if _, err := service.currentPayloadPermitted(ctx, config, version); err != nil {
			return nil, err
		}
	}
	updated, err := service.repository.SetEnabled(ctx, config.ID, version.Version, config.ResourceRevision, enabled)
	if err != nil {
		return nil, mapServiceRepositoryError(err)
	}
	if updated == nil || updated.CurrentVersion != version.Version {
		return nil, ErrConflict
	}
	summary := service.safeSummaryOf(updated, version)
	return &summary, nil
}

// TaskOptions 是未来 Task 4 的只读输入。缺少受控 dialer 时返回空集而非把未受
// 验证配置泄露给浏览器；实际创建前仍必须调用 ValidateTaskConnection。
func (service *Service) TaskOptions(ctx context.Context, subject identity.Subject) ([]TaskConnectionOption, error) {
	if service == nil || service.repository == nil || !canUseForTask(subject) {
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
		payload, permittedErr := service.currentPayloadPermitted(ctx, config, version)
		if permittedErr != nil {
			continue
		}
		name, _, displaySafe := safeConnectionDisplayText(config.Name, config.Description, &payload)
		if !displaySafe {
			continue
		}
		options = append(options, TaskConnectionOption{
			ConnectionID:      config.ID,
			ConnectionVersion: version.Version,
			Name:              name,
			Scope:             config.Scope,
			Transport:         version.DetectedTransport,
		})
	}
	return options, nil
}

// ValidateTaskConnection 仅执行未来任务创建需要的资格验证；它不创建任务、绑定或
// UoW，避免在 Task 3 越过 Task 4 的持久化职责。
func (service *Service) ValidateTaskConnection(ctx context.Context, subject identity.Subject, configID string) error {
	if service == nil || !canUseForTask(subject) {
		return ErrForbidden
	}
	if service.policy == nil || service.policy.RequireControlledDialer() != nil {
		return ErrControlledEgressRequired
	}
	config, version, err := service.visibleCurrentVersion(ctx, subject, configID)
	if err != nil {
		return err
	}
	if !config.Enabled || version.ProbeStatus != ProbeStatusPassed || !concreteProbeTransport(version.DetectedTransport) {
		return ErrTaskConnectionUnavailable
	}
	if _, err := service.currentPayloadPermitted(ctx, config, version); err != nil {
		// 任务创建者只需要知道该连接此刻不可用，不能分辨解密、DNS 或允许集
		// 的内部原因，更不能由错误文本获得 endpoint 或 header 材料。
		return ErrTaskConnectionUnavailable
	}
	return nil
}

// currentPayloadPermitted 将当前版本的加密材料重新解密并依照当前 outbound
// policy 复核，而不是相信历史 probe 的结论。允许集、DNS 解析或材料结构变更后，
// 资格路径必须 fail closed；调用方只得到固定的 unavailable/denied 错误。
func (service *Service) currentPayloadPermitted(ctx context.Context, config *ConnectionConfig, version *ConnectionVersion) (ConnectionPayload, error) {
	if service == nil || service.keyring == nil || service.policy == nil || config == nil || version == nil {
		return ConnectionPayload{}, ErrTaskConnectionUnavailable
	}
	payload, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return ConnectionPayload{}, ErrTaskConnectionUnavailable
	}
	payload, valid := canonicalConnectionPayload(payload)
	if !valid {
		return ConnectionPayload{}, ErrTaskConnectionUnavailable
	}
	if service.policy.ValidateServerURL(ctx, payload.Endpoint) != nil {
		return ConnectionPayload{}, ErrOutboundDenied
	}
	return payload, nil
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

func (service *Service) safeSummaryOf(config *ConnectionConfig, version *ConnectionVersion) ConnectionSummary {
	if service == nil || service.keyring == nil {
		return summaryOf(config, version, nil)
	}
	payload, err := service.keyring.OpenConnectionPayload(config, version)
	if err != nil {
		return summaryOf(config, version, nil)
	}
	return summaryOf(config, version, &payload)
}

func summaryOf(config *ConnectionConfig, version *ConnectionVersion, payload *ConnectionPayload) ConnectionSummary {
	name, description, _ := safeConnectionDisplayText(config.Name, config.Description, payload)
	return ConnectionSummary{
		ID:                config.ID,
		Name:              name,
		Description:       description,
		Scope:             config.Scope,
		CurrentVersion:    config.CurrentVersion,
		Enabled:           config.Enabled,
		Transport:         version.Transport,
		DetectedTransport: version.DetectedTransport,
		ProbeStatus:       version.ProbeStatus,
	}
}

func canCreate(subject identity.Subject, scope Scope) bool {
	if !hasSubjectUserID(subject) {
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
	if !hasSubjectUserID(subject) {
		return false
	}
	switch subject.Role {
	case identity.RoleAdmin, identity.RoleAuditor:
		return true
	case identity.RoleUser:
		return true
	default:
		return false
	}
}

// canUseForTask 与只读审计视图分离：审计员可看经过脱敏的摘要，但不能选择连接或
// 验证任务可用性，以免读权限间接变成任务执行权限。
func canUseForTask(subject identity.Subject) bool {
	if !hasSubjectUserID(subject) {
		return false
	}
	switch subject.Role {
	case identity.RoleAdmin:
		return true
	case identity.RoleUser:
		return true
	default:
		return false
	}
}

func canRead(subject identity.Subject, config *ConnectionConfig) bool {
	if config == nil || !hasSubjectUserID(subject) || !validReader(subject) {
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
	if config == nil || !hasSubjectUserID(subject) {
		return false
	}
	if subject.Role == identity.RoleAdmin {
		return true
	}
	return subject.Role == identity.RoleUser && config.Scope == ScopePrivate && subject.UserID != "" && subject.UserID == config.OwnerUserID
}

func hasSubjectUserID(subject identity.Subject) bool {
	return strings.TrimSpace(subject.UserID) != ""
}

func validCreateInput(input CreateConnectionInput) bool {
	if input.Scope != ScopePrivate && input.Scope != ScopeGlobal || !configurableTransport(input.Transport) || strings.TrimSpace(input.ServerURL) == "" {
		return false
	}
	payload, validPayload := canonicalConnectionPayload(connectionPayloadFromInput(input))
	if !validPayload {
		return false
	}
	if _, _, safe := safeConnectionDisplayText(input.Name, input.Description, &payload); !safe {
		return false
	}
	return true
}

// canonicalConnectionPayload 是所有已解密或尚未加密连接材料进入受控执行路径前
// 的唯一规范化边界。历史版本并不天然可信：名称去空白后仍必须满足和新建连接
// 相同的认证 shape、Header 名/值、大小与去重策略，否则调用方只能 fail closed。
// 该函数只返回内存副本，绝不记录或包装失败的秘密材料。
func canonicalConnectionPayload(payload ConnectionPayload) (ConnectionPayload, bool) {
	canonical := payload
	canonical.Endpoint = strings.TrimSpace(canonical.Endpoint)
	canonical.Authentication.HeaderName = strings.TrimSpace(canonical.Authentication.HeaderName)
	canonical.Headers = normalizedHeaders(canonical.Headers)
	return canonical, validConnectionPayload(canonical)
}

func validConnectionPayload(payload ConnectionPayload) bool {
	if strings.TrimSpace(payload.Endpoint) == "" || !validCustomHeaders(payload.Headers) {
		return false
	}
	authentication := payload.Authentication
	switch authentication.Kind {
	case AuthenticationNone:
		return authentication.Secret == "" && authentication.HeaderName == "" && len(payload.Headers) == 0
	case AuthenticationBearer:
		return strings.TrimSpace(authentication.Secret) != "" && validHTTPHeaderValue(authentication.Secret) && authentication.HeaderName == ""
	case AuthenticationAPIKeyHeader:
		return strings.TrimSpace(authentication.Secret) != "" && validHTTPHeaderValue(authentication.Secret) && validCustomHeaderName(authentication.HeaderName)
	case AuthenticationCustomHeaders:
		return authentication.Secret == "" && authentication.HeaderName == "" && len(payload.Headers) > 0
	default:
		return false
	}
}

func connectionPayloadFromInput(input CreateConnectionInput) ConnectionPayload {
	authentication := input.Authentication
	authentication.HeaderName = strings.TrimSpace(authentication.HeaderName)
	return ConnectionPayload{
		Endpoint:       strings.TrimSpace(input.ServerURL),
		Authentication: authentication,
		Headers:        normalizedHeaders(input.Headers),
	}
}

func authenticationConfigured(payload ConnectionPayload) bool {
	switch payload.Authentication.Kind {
	case AuthenticationBearer, AuthenticationAPIKeyHeader:
		return strings.TrimSpace(payload.Authentication.Secret) != ""
	case AuthenticationCustomHeaders:
		return len(payload.Headers) > 0
	default:
		return false
	}
}

func validCustomHeaders(headers []Header) bool {
	if len(headers) > maxCustomHeaders {
		return false
	}
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		if !validCustomHeaderName(header.Name) || !validHTTPHeaderValue(header.Value) {
			return false
		}
		key := strings.ToLower(header.Name)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validCustomHeaderName(name string) bool {
	if !validHTTPHeaderName(name) {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	// '_' 是 HTTP token 中的合法字符，却会让下游代理的 "X-Forwarded-*"
	// 等规则出现不一致解释。MCP 连接没有必须使用下划线 Header 的协议需求，
	// 因而统一拒绝，避免连字符黑名单被等价变体绕过。
	if strings.Contains(name, "_") {
		return false
	}
	if strings.HasPrefix(name, "mcp-") || strings.HasPrefix(name, "proxy-") ||
		strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "x-host-") ||
		strings.HasPrefix(name, "x-http-host-") || strings.HasPrefix(name, "x-original-") ||
		strings.HasPrefix(name, "x-rewrite-") || strings.HasPrefix(name, "x-envoy-") ||
		strings.HasPrefix(name, "x-accel-") {
		return false
	}
	switch name {
	case "host", "content-length", "transfer-encoding", "connection", "keep-alive", "upgrade", "te", "trailer",
		"content-type", "accept", "cookie", "set-cookie", "cache-control", "last-event-id",
		"forwarded", "via", "x-real-ip", "x-original-url", "x-original-uri", "x-rewrite-url", "x-rewrite-uri",
		"x-host", "x-http-host", "host-override", "http-host-override", "x-host-override", "x-http-host-override",
		"x-http-method-override", "x-http-method", "x-method-override", "x-url-scheme", "x-forwarded-ssl", "x-arr-ssl":
		return false
	default:
		return true
	}
}

func validHTTPHeaderName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxHTTPHeaderNameBytes || strings.ContainsAny(name, "\r\n") {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	return len(value) <= maxHTTPHeaderValueBytes && !strings.ContainsAny(value, "\r\n")
}

func validDisplayText(value string, maximumRunes int, required bool) bool {
	if maximumRunes <= 0 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return !required
	}
	if utf8.RuneCountInString(value) > maximumRunes {
		return false
	}
	if containsSensitiveDisplayMaterial(value) {
		return false
	}
	return true
}

// safeConnectionDisplayText 只在文本本身和已解密的当前版本材料均可验证时投影
// 名称、说明。任一字段或 payload 不安全时两个字段同时清空，避免残余文本成为
// 间接泄露通道。
func safeConnectionDisplayText(name string, description string, payload *ConnectionPayload) (string, string, bool) {
	if payload == nil || !validDisplayText(name, maxConnectionNameRunes, true) || !validDisplayText(description, maxConnectionDescriptionRunes, false) {
		return "", "", false
	}
	if displayTextEchoesConnectionMaterial(name, *payload) || displayTextEchoesConnectionMaterial(description, *payload) {
		return "", "", false
	}
	return strings.TrimSpace(name), strings.TrimSpace(description), true
}

func containsSensitiveDisplayMaterial(value string) bool {
	lower := strings.ToLower(value)
	if strings.ContainsAny(value, "/\\@:?#=&%") || strings.Contains(lower, "git@") || containsHostLikeSegment(value) || containsASCIIHeaderNameSegment(value) {
		return true
	}
	for _, keyword := range []string{
		"token", "cookie", "authorization", "header", "bearer", "api_key", "apikey", "secret", "password", "credential", "endpoint",
		"令牌", "凭据", "密钥", "授权", "请求头", "端点",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return containsTokenLikeSegment(value)
}

func containsTokenLikeSegment(value string) bool {
	length := 0
	flush := func() bool {
		matched := length >= maxDisplayTokenRunes
		length = 0
		return matched
	}
	for _, character := range value {
		switch {
		case character >= 'A' && character <= 'Z':
			length++
		case character >= 'a' && character <= 'z':
			length++
		case character >= '0' && character <= '9':
			length++
		case character == '-' || character == '_':
			length++
		default:
			if flush() {
				return true
			}
		}
	}
	return flush()
}

// looksLikeHost 拒绝 ASCII 域名/IP 标签组合。显示文本只需要是人可读标签，因而不
// 应携带可被误认为出站目标的 host；宁可把类似版本号的 ASCII 片段保守地拒绝。
func looksLikeHost(value string) bool {
	value = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
	if value == "" || strings.ContainsAny(value, " \t") {
		return false
	}
	labels := strings.Split(value, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

// looksLikeASCIIHeaderName 对任意 ASCII 连字符 Header 形式 fail closed。显示标签
// 应使用中文或空格分词的人类描述；不能依赖 Header 名称大小写或常见前缀来判断其
// 是否敏感。
func looksLikeASCIIHeaderName(value string) bool {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "-") || strings.ContainsAny(value, " \t") {
		return false
	}
	for _, character := range value {
		if character != '-' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return false
	}
	return true
}

func containsHostLikeSegment(value string) bool {
	return containsASCIISegment(value, func(character rune) bool {
		return character == '.' || character == '-' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
	}, looksLikeHost)
}

func containsASCIIHeaderNameSegment(value string) bool {
	return containsASCIISegment(value, func(character rune) bool {
		return character == '-' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9'
	}, looksLikeASCIIHeaderName)
}

func containsASCIISegment(value string, permitted func(rune) bool, matches func(string) bool) bool {
	var segment strings.Builder
	flush := func() bool {
		candidate := segment.String()
		segment.Reset()
		return candidate != "" && matches(candidate)
	}
	for _, character := range value {
		if permitted(character) {
			segment.WriteRune(character)
			continue
		}
		if flush() {
			return true
		}
	}
	return flush()
}

// displayTextEchoesConnectionMaterial 阻止管理者把当前连接材料嵌入可投影标签。
// Header 名称与 host 比较按大小写无关处理；秘密和值按原文子串匹配，防止前后附加
// 普通说明文本绕过校验。
func displayTextEchoesConnectionMaterial(value string, payload ConnectionPayload) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if containsDisplayMaterial(value, payload.Endpoint, true) {
		return true
	}
	if parsed, err := parseHTTPSURL(payload.Endpoint); err == nil && containsDisplayMaterial(value, parsed.Hostname(), true) {
		return true
	}
	if containsDisplayMaterial(value, payload.Authentication.HeaderName, true) || containsDisplayMaterial(value, payload.Authentication.Secret, false) {
		return true
	}
	for _, header := range payload.Headers {
		if containsDisplayMaterial(value, header.Name, true) || containsDisplayMaterial(value, header.Value, false) {
			return true
		}
	}
	return false
}

func containsDisplayMaterial(value, material string, caseInsensitive bool) bool {
	material = strings.TrimSpace(material)
	if material == "" {
		return false
	}
	if caseInsensitive {
		return strings.Contains(strings.ToLower(value), strings.ToLower(material))
	}
	return strings.Contains(value, material)
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

func normalizedHeaders(headers []Header) []Header {
	normalized := cloneHeaders(headers)
	for index := range normalized {
		normalized[index].Name = strings.TrimSpace(normalized[index].Name)
	}
	return normalized
}

func mapServiceRepositoryError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, ErrConflict) {
		return ErrConflict
	}
	if errors.Is(err, ErrTaskConnectionUnavailable) {
		return ErrTaskConnectionUnavailable
	}
	return ErrInvalid
}

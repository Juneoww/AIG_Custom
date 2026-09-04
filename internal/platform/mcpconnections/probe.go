package mcpconnections

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrProbeFailed      = errors.New("MCP 连接探测失败")
	ErrProbeRateLimited = errors.New("MCP 连接探测请求过于频繁")
)

const (
	minimumInitializeRequestID       = "mcp-probe"
	minimumInitializeProtocolVersion = "2025-03-26"
	maxProbeSessionIDBytes           = 4 * 1024
)

// ProbeRequest 是探测端口的内部输入，不可直接映射 API DTO。它可携带解密后的
// 连接材料，但 ProbePort 的实现不得记录、返回或包装其中的 URL/认证信息。
type ProbeRequest struct {
	Payload   ConnectionPayload
	Transport Transport
}

// ProbePort 将协议探测与连接服务解耦。未来 Task 5 可以提供受控 gateway
// 实现；Task 3 的服务不接入普通 Agent 的直连路径。
type ProbePort interface {
	Initialize(context.Context, ProbeRequest) error
}

type ProbeClock interface {
	Now() time.Time
}

type systemProbeClock struct{}

func (systemProbeClock) Now() time.Time { return time.Now().UTC() }

type ProbeOptions struct {
	Timeout         time.Duration
	MinimumInterval time.Duration
	Clock           ProbeClock
}

type ProbeResult struct {
	DetectedTransport Transport
}

// ProbeEngine 只发起最小 initialize 握手。它没有工具发现/调用能力，因此探测
// 不能在管理端无意间执行 MCP tool。
type ProbeEngine struct {
	port            ProbePort
	timeout         time.Duration
	minimumInterval time.Duration
	clock           ProbeClock
	mu              sync.Mutex
	lastAttempts    map[string]time.Time
}

func NewProbeEngine(port ProbePort, options ProbeOptions) *ProbeEngine {
	if options.Timeout <= 0 {
		options.Timeout = 10 * time.Second
	}
	if options.MinimumInterval <= 0 {
		options.MinimumInterval = time.Minute
	}
	if options.Clock == nil {
		options.Clock = systemProbeClock{}
	}
	return &ProbeEngine{
		port:            port,
		timeout:         options.Timeout,
		minimumInterval: options.MinimumInterval,
		clock:           options.Clock,
		lastAttempts:    make(map[string]time.Time),
	}
}

func (engine *ProbeEngine) Probe(ctx context.Context, connectionConfigID string, payload ConnectionPayload, selected Transport) (ProbeResult, error) {
	if err := engine.reserveAttempt(connectionConfigID); err != nil {
		return ProbeResult{}, err
	}
	return engine.probeReserved(ctx, payload, selected)
}

// reserveAttempt 仅消耗本进程的速率配额。Service 必须先调用它，再持久化
// StartProbe，确保被本地限流的请求不会把已有 passed 状态重置为 not_tested。
func (engine *ProbeEngine) reserveAttempt(connectionConfigID string) error {
	if engine == nil || engine.port == nil {
		return ErrProbeFailed
	}
	if !engine.allowAttempt(connectionConfigID) {
		return ErrProbeRateLimited
	}
	return nil
}

// probeReserved 只执行已经获得本地速率配额的真实握手。它不再写任何持久化
// 状态；调用方负责用 StartProbe 的 token 条件写回结果。
func (engine *ProbeEngine) probeReserved(ctx context.Context, payload ConnectionPayload, selected Transport) (ProbeResult, error) {
	if engine == nil || engine.port == nil {
		return ProbeResult{}, ErrProbeFailed
	}
	transports := probeTransportOrder(selected)
	if len(transports) == 0 {
		return ProbeResult{}, ErrProbeFailed
	}
	for _, transport := range transports {
		attemptContext, cancel := context.WithTimeout(ctx, engine.timeout)
		err := engine.port.Initialize(attemptContext, ProbeRequest{Payload: payload, Transport: transport})
		cancel()
		if err == nil {
			return ProbeResult{DetectedTransport: transport}, nil
		}
	}
	return ProbeResult{}, ErrProbeFailed
}

func (engine *ProbeEngine) allowAttempt(connectionConfigID string) bool {
	connectionConfigID = strings.TrimSpace(connectionConfigID)
	if connectionConfigID == "" {
		return false
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	now := engine.clock.Now()
	for id, attemptedAt := range engine.lastAttempts {
		if !attemptedAt.IsZero() && now.Sub(attemptedAt) >= engine.minimumInterval {
			delete(engine.lastAttempts, id)
		}
	}
	if attemptedAt, exists := engine.lastAttempts[connectionConfigID]; exists && now.Sub(attemptedAt) < engine.minimumInterval {
		return false
	}
	engine.lastAttempts[connectionConfigID] = now
	return true
}

func probeTransportOrder(selected Transport) []Transport {
	switch selected {
	case TransportAuto:
		// 自动协商刻意只有稳定的两项顺序，不能把 HTTP 失败悄悄降级为任意协议。
		return []Transport{TransportHTTP, TransportSSE}
	case TransportHTTP, TransportSSE:
		return []Transport{selected}
	default:
		return nil
	}
}

type HTTPProbeOptions struct {
	MaxResponseBytes int64
	HTTPClientConfig ControlledHTTPClientConfig
}

type HTTPProbePort struct {
	policy           *OutboundPolicy
	client           *http.Client
	maxResponseBytes int64
}

// validatedProbePayload 只可由 validateProbePayload 构造。它让所有后续 HTTP、
// SSE、notification 与 session-cleanup 路径在类型层面只能消费已经过严格策略
// 校验的内存副本，而不能重新接收任意历史解密 payload 或直接 ProbeRequest。
type validatedProbePayload struct{ ConnectionPayload }

func validateProbePayload(payload ConnectionPayload) (validatedProbePayload, error) {
	canonical, valid := canonicalConnectionPayload(payload)
	if !valid {
		return validatedProbePayload{}, ErrProbeFailed
	}
	return validatedProbePayload{ConnectionPayload: canonical}, nil
}

func NewHTTPProbePort(policy *OutboundPolicy, options HTTPProbeOptions) (*HTTPProbePort, error) {
	if err := policy.RequireControlledDialer(); err != nil {
		return nil, err
	}
	// 探测端口永远由受控策略构造 client。不能接受外部 *http.Client，否则代理、
	// DNS rebinding 防护或 TLS 校验都可能被调用方 transport 绕过。
	client, err := NewControlledHTTPClient(policy, options.HTTPClientConfig)
	if err != nil {
		return nil, err
	}
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = 64 << 10
	}
	return &HTTPProbePort{policy: policy, client: client, maxResponseBytes: options.MaxResponseBytes}, nil
}

func (port *HTTPProbePort) Initialize(ctx context.Context, request ProbeRequest) error {
	if port == nil || port.client == nil || port.policy == nil || port.policy.RequireControlledDialer() != nil {
		return ErrProbeFailed
	}
	if request.Transport != TransportHTTP && request.Transport != TransportSSE {
		return ErrProbeFailed
	}
	payload, err := validateProbePayload(request.Payload)
	if err != nil {
		return ErrProbeFailed
	}
	if err := port.policy.ValidateServerURL(ctx, payload.Endpoint); err != nil {
		return ErrProbeFailed
	}
	switch request.Transport {
	case TransportHTTP:
		return port.initializeStreamableHTTP(ctx, payload)
	case TransportSSE:
		return port.initializeLegacySSE(ctx, payload)
	default:
		return ErrProbeFailed
	}
}

// initializeStreamableHTTP 是 MCP 2025-03-26 的单请求 Streamable HTTP 握手。
// 该协议允许同一次 POST 以 JSON 或 SSE 返回 initialize 响应，因此响应的媒体
// 类型不能被误用来推断 legacy SSE transport。
func (port *HTTPProbePort) initializeStreamableHTTP(ctx context.Context, payload validatedProbePayload) error {
	body, err := json.Marshal(minimumInitializeRequest())
	if err != nil {
		return ErrProbeFailed
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, payload.Endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrProbeFailed
	}
	if err := applyProbeAuthentication(httpRequest, payload); err != nil {
		return ErrProbeFailed
	}
	applyStreamableHTTPHeaders(httpRequest, "")

	response, err := port.client.Do(httpRequest)
	if err != nil || response == nil || response.Body == nil {
		return ErrProbeFailed
	}
	sessionID := usableProbeSessionID(response.Header.Get("Mcp-Session-Id"))
	if sessionID != "" {
		// defer 在通知或解析失败时同样执行。cleanup 不能把 session、URL 或上游错误
		// 写入任何返回值；它只是经相同受控 client 做尽力而为的资源释放。
		defer port.cleanupStreamableSession(ctx, payload, sessionID)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ErrProbeFailed
	}

	valid := false
	switch {
	case hasMediaType(response.Header.Get("Content-Type"), "application/json"):
		responseBody, readErr := readLimitedBody(response.Body, port.maxResponseBytes)
		valid = readErr == nil && validJSONInitializeResponse(responseBody)
	case hasMediaType(response.Header.Get("Content-Type"), "text/event-stream"):
		valid = validSSEInitializeStream(ctx, response.Body, port.maxResponseBytes)
	default:
		valid = false
	}
	// 尽早释放 JSON 或 streamable SSE 响应体，避免 initialized notification 因
	// 上游每 host 连接限制而等待旧流关闭。
	_ = response.Body.Close()
	if !valid {
		return ErrProbeFailed
	}
	return port.sendStreamableInitialized(ctx, payload, sessionID)
}

// initializeLegacySSE 实现旧 SSE transport 的真实生命周期：先打开只读 SSE
// stream，取得同 origin 的 message endpoint，向该 endpoint POST initialize，
// 再从原 stream 等待匹配 response。绝不把一个 POST + Accept:SSE 冒充旧协议。
func (port *HTTPProbePort) initializeLegacySSE(ctx context.Context, payload validatedProbePayload) error {
	streamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, payload.Endpoint, nil)
	if err != nil {
		return ErrProbeFailed
	}
	if err := applyProbeAuthentication(streamRequest, payload); err != nil {
		return ErrProbeFailed
	}
	applyLegacySSEStreamHeaders(streamRequest)
	streamResponse, err := port.client.Do(streamRequest)
	if err != nil || streamResponse == nil || streamResponse.Body == nil {
		return ErrProbeFailed
	}
	defer streamResponse.Body.Close()
	if streamResponse.StatusCode != http.StatusOK || !hasMediaType(streamResponse.Header.Get("Content-Type"), "text/event-stream") {
		return ErrProbeFailed
	}

	stopClosingBody := closeReaderWhenContextDone(ctx, streamResponse.Body)
	defer stopClosingBody()
	stream := newProbeSSEReader(streamResponse.Body, port.maxResponseBytes)
	endpointEvent, err := stream.next(ctx)
	if err != nil || endpointEvent.event != "endpoint" {
		return ErrProbeFailed
	}
	messageEndpoint, err := legacySSEMessageEndpoint(payload.Endpoint, endpointEvent.data)
	if err != nil {
		return ErrProbeFailed
	}
	if err := port.postLegacySSEMessage(ctx, messageEndpoint, payload, minimumInitializeRequest()); err != nil {
		return ErrProbeFailed
	}

	for {
		responseEvent, responseErr := stream.next(ctx)
		if responseErr != nil || responseEvent.event != "message" {
			return ErrProbeFailed
		}
		if validJSONInitializeResponse([]byte(responseEvent.data)) {
			break
		}
		if !validJSONRPCNotification([]byte(responseEvent.data)) {
			return ErrProbeFailed
		}
	}
	return port.postLegacySSEMessage(ctx, messageEndpoint, payload, initializedNotification())
}

func (port *HTTPProbePort) sendStreamableInitialized(ctx context.Context, payload validatedProbePayload, sessionID string) error {
	message, err := json.Marshal(initializedNotification())
	if err != nil {
		return ErrProbeFailed
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, payload.Endpoint, bytes.NewReader(message))
	if err != nil {
		return ErrProbeFailed
	}
	if err := applyProbeAuthentication(request, payload); err != nil {
		return ErrProbeFailed
	}
	applyStreamableHTTPHeaders(request, sessionID)
	response, err := port.client.Do(request)
	if err != nil || response == nil || response.Body == nil {
		return ErrProbeFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return ErrProbeFailed
	}
	return nil
}

func (port *HTTPProbePort) cleanupStreamableSession(ctx context.Context, payload validatedProbePayload, sessionID string) {
	if port == nil || port.client == nil || sessionID == "" {
		return
	}
	cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(cleanupContext, http.MethodDelete, payload.Endpoint, nil)
	if err != nil {
		return
	}
	if applyProbeAuthentication(request, payload) != nil {
		return
	}
	// session ID 来自握手响应，不能由用户配置覆盖；在认证材料之后固定写入。
	request.Header.Set("Mcp-Session-Id", sessionID)
	response, err := port.client.Do(request)
	if err == nil && response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
}

func (port *HTTPProbePort) postLegacySSEMessage(ctx context.Context, target string, payload validatedProbePayload, message map[string]any) error {
	body, err := json.Marshal(message)
	if err != nil {
		return ErrProbeFailed
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return ErrProbeFailed
	}
	if err := applyProbeAuthentication(request, payload); err != nil {
		return ErrProbeFailed
	}
	applyLegacySSEPostHeaders(request)
	response, err := port.client.Do(request)
	if err != nil || response == nil || response.Body == nil {
		return ErrProbeFailed
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return ErrProbeFailed
	}
	return nil
}

func minimumInitializeRequest() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      minimumInitializeRequestID,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": minimumInitializeProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "ai-infra-guard-probe",
				"version": "1",
			},
		},
	}
}

func initializedNotification() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
}

func applyProbeAuthentication(request *http.Request, payload validatedProbePayload) error {
	if request == nil {
		return ErrProbeFailed
	}
	// validatedProbePayload 是私有类型，但同包未来代码仍可手工构造。再次要求
	// canonical payload，避免绕过 Initialize 的入口校验而把不合法 Header 写入请求。
	canonical, valid := canonicalConnectionPayload(payload.ConnectionPayload)
	if !valid || !sameConnectionPayload(canonical, payload.ConnectionPayload) {
		return ErrProbeFailed
	}
	for _, header := range payload.Headers {
		request.Header.Set(header.Name, header.Value)
	}
	switch payload.Authentication.Kind {
	case AuthenticationBearer:
		request.Header.Set("Authorization", "Bearer "+payload.Authentication.Secret)
	case AuthenticationAPIKeyHeader:
		if payload.Authentication.HeaderName != "" {
			request.Header.Set(payload.Authentication.HeaderName, payload.Authentication.Secret)
		}
	}
	return nil
}

func sameConnectionPayload(left, right ConnectionPayload) bool {
	if left.Endpoint != right.Endpoint || left.Authentication != right.Authentication || len(left.Headers) != len(right.Headers) {
		return false
	}
	for index := range left.Headers {
		if left.Headers[index] != right.Headers[index] {
			return false
		}
	}
	return true
}

// applyStreamableHTTPHeaders 必须在全部用户材料之后调用。保存时的 header 策略
// 已拒绝协议和会话名；这里仍以固定值覆盖，作为解密载荷意外绕过写入校验时的第二道
// 边界。2025-03-26 要求 Streamable HTTP POST 同时接受 JSON 与 SSE 响应。
func applyStreamableHTTPHeaders(request *http.Request, sessionID string) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	} else {
		request.Header.Del("Mcp-Session-Id")
	}
}

func applyLegacySSEStreamHeaders(request *http.Request) {
	request.Header.Del("Content-Type")
	request.Header.Del("Mcp-Session-Id")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Cache-Control", "no-cache")
}

func applyLegacySSEPostHeaders(request *http.Request) {
	request.Header.Del("Mcp-Session-Id")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
}

func usableProbeSessionID(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" || len(sessionID) > maxProbeSessionIDBytes || !validHTTPHeaderValue(sessionID) {
		return ""
	}
	return sessionID
}

func readLimitedBody(body io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		return nil, ErrProbeFailed
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrProbeFailed
	}
	return data, nil
}

func hasMediaType(contentType, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, expected)
}

func validJSONInitializeResponse(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return false
	}
	if response.JSONRPC != "2.0" || response.ID != minimumInitializeRequestID || len(response.Result) == 0 || len(response.Error) != 0 {
		return false
	}
	result := bytes.TrimSpace(response.Result)
	if len(result) == 0 || result[0] != '{' {
		return false
	}
	var initializeResult struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      json.RawMessage `json:"serverInfo"`
	}
	if err := json.Unmarshal(result, &initializeResult); err != nil {
		return false
	}
	if initializeResult.ProtocolVersion != minimumInitializeProtocolVersion || !validJSONObject(initializeResult.Capabilities) || !validJSONObject(initializeResult.ServerInfo) {
		return false
	}
	var serverInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(initializeResult.ServerInfo, &serverInfo); err != nil {
		return false
	}
	return strings.TrimSpace(serverInfo.Name) != "" && strings.TrimSpace(serverInfo.Version) != ""
}

func validJSONObject(raw json.RawMessage) bool {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil
}

// validSSEInitializeStream 仅解析首个完整的 data event，并在得到合法 initialize
// 响应后立即返回。SSE 连接本来可以长期保持，因此不能等待 EOF；读取的字节数仍
// 受到上限约束，任何截断、无效 framing 或取消都按失败处理。
func validSSEInitializeStream(ctx context.Context, body io.Reader, limit int64) bool {
	if body == nil || limit <= 0 || ctx == nil || ctx.Err() != nil {
		return false
	}
	stopClosingBody := closeReaderWhenContextDone(ctx, body)
	defer stopClosingBody()
	reader := newProbeSSEReader(body, limit)
	for {
		event, err := reader.next(ctx)
		if err != nil {
			return false
		}
		if validJSONInitializeResponse([]byte(event.data)) {
			return true
		}
		if !validJSONRPCNotification([]byte(event.data)) {
			return false
		}
	}
}

func validJSONRPCNotification(body []byte) bool {
	var notification struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(body), &notification); err != nil || notification.JSONRPC != "2.0" || strings.TrimSpace(notification.Method) == "" {
		return false
	}
	id := bytes.TrimSpace(notification.ID)
	return len(id) == 0 || bytes.Equal(id, []byte("null"))
}

type probeSSEEvent struct {
	event string
	data  string
}

type probeSSEReader struct {
	reader    *bufio.Reader
	limit     int64
	readBytes int64
}

func newProbeSSEReader(body io.Reader, limit int64) *probeSSEReader {
	return &probeSSEReader{reader: bufio.NewReader(io.LimitReader(body, limit+1)), limit: limit}
}

func (reader *probeSSEReader) next(ctx context.Context) (probeSSEEvent, error) {
	if reader == nil || reader.reader == nil || reader.limit <= 0 || ctx == nil || ctx.Err() != nil {
		return probeSSEEvent{}, ErrProbeFailed
	}
	var eventType string
	dataLines := make([]string, 0, 1)
	for {
		if ctx.Err() != nil {
			return probeSSEEvent{}, ErrProbeFailed
		}
		line, err := reader.reader.ReadString('\n')
		reader.readBytes += int64(len(line))
		if reader.readBytes > reader.limit {
			return probeSSEEvent{}, ErrProbeFailed
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			if len(dataLines) > 0 {
				if eventType == "" {
					eventType = "message"
				}
				return probeSSEEvent{event: eventType, data: strings.Join(dataLines, "\n")}, nil
			}
			if err != nil {
				return probeSSEEvent{}, ErrProbeFailed
			}
			continue
		}
		if err != nil {
			// EOF 前未以空行结束的 event 不是完整 SSE framing。
			return probeSSEEvent{}, ErrProbeFailed
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			return probeSSEEvent{}, ErrProbeFailed
		}
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "data":
			dataLines = append(dataLines, value)
		case "event":
			eventType = value
		case "id", "retry":
			// 这些 framing 元数据不影响最小 initialize 验证。
		default:
			return probeSSEEvent{}, ErrProbeFailed
		}
	}
}

func legacySSEMessageEndpoint(baseRaw, eventData string) (string, error) {
	base, err := parseHTTPSURL(baseRaw)
	if err != nil {
		return "", ErrProbeFailed
	}
	eventData = strings.TrimSpace(eventData)
	if eventData == "" || strings.ContainsAny(eventData, "\r\n") {
		return "", ErrProbeFailed
	}
	reference, err := url.Parse(eventData)
	if err != nil || reference == nil || reference.User != nil || reference.Fragment != "" {
		return "", ErrProbeFailed
	}
	target := base.ResolveReference(reference)
	if target == nil || target.Opaque != "" || target.User != nil || target.Fragment != "" || target.Scheme != base.Scheme || canonicalHost(target.Hostname()) != canonicalHost(base.Hostname()) || originPort(target) != originPort(base) {
		return "", ErrProbeFailed
	}
	return target.String(), nil
}

func originPort(target *url.URL) string {
	if target == nil {
		return ""
	}
	if port := target.Port(); port != "" {
		return port
	}
	if strings.EqualFold(target.Scheme, "https") {
		return "443"
	}
	return ""
}

// closeReaderWhenContextDone 让自定义或测试 transport 的流 body 也遵守 request
// context；标准 net/http transport 已会这样处理，但探测端口不能依赖调用方实现。
func closeReaderWhenContextDone(ctx context.Context, body io.Reader) func() {
	closer, ok := body.(io.Closer)
	if !ok {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = closer.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

package mcpconnections

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	ErrProbeFailed      = errors.New("MCP 连接探测失败")
	ErrProbeRateLimited = errors.New("MCP 连接探测请求过于频繁")
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
	lastAttempt     time.Time
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
	}
}

func (engine *ProbeEngine) Probe(ctx context.Context, payload ConnectionPayload, selected Transport) (ProbeResult, error) {
	if engine == nil || engine.port == nil {
		return ProbeResult{}, ErrProbeFailed
	}
	if !engine.allowAttempt() {
		return ProbeResult{}, ErrProbeRateLimited
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

func (engine *ProbeEngine) allowAttempt() bool {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	now := engine.clock.Now()
	if !engine.lastAttempt.IsZero() && now.Sub(engine.lastAttempt) < engine.minimumInterval {
		return false
	}
	engine.lastAttempt = now
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
	if err := port.policy.ValidateServerURL(ctx, request.Payload.Endpoint); err != nil {
		return ErrProbeFailed
	}
	body, err := json.Marshal(minimumInitializeRequest())
	if err != nil {
		return ErrProbeFailed
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, request.Payload.Endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrProbeFailed
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if request.Transport == TransportSSE {
		httpRequest.Header.Set("Accept", "text/event-stream")
	} else {
		httpRequest.Header.Set("Accept", "application/json")
	}
	applyProbeAuthentication(httpRequest, request.Payload)

	response, err := port.client.Do(httpRequest)
	if err != nil || response == nil || response.Body == nil {
		return ErrProbeFailed
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ErrProbeFailed
	}
	responseBody, err := readLimitedBody(response.Body, port.maxResponseBytes)
	if err != nil || !validProbeResponse(request.Transport, response.Header.Get("Content-Type"), responseBody) {
		return ErrProbeFailed
	}
	return nil
}

func minimumInitializeRequest() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      "mcp-probe",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "ai-infra-guard-probe",
				"version": "1",
			},
		},
	}
}

func applyProbeAuthentication(request *http.Request, payload ConnectionPayload) {
	switch payload.Authentication.Kind {
	case AuthenticationBearer:
		request.Header.Set("Authorization", "Bearer "+payload.Authentication.Secret)
	case AuthenticationAPIKeyHeader:
		if payload.Authentication.HeaderName != "" {
			request.Header.Set(payload.Authentication.HeaderName, payload.Authentication.Secret)
		}
	}
	for _, header := range payload.Headers {
		if header.Name != "" {
			request.Header.Set(header.Name, header.Value)
		}
	}
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

func validProbeResponse(transport Transport, contentType string, body []byte) bool {
	switch transport {
	case TransportHTTP:
		return hasMediaType(contentType, "application/json") && validJSONInitializeResponse(body)
	case TransportSSE:
		return hasMediaType(contentType, "text/event-stream") && validSSEInitializeResponse(body)
	default:
		return false
	}
}

func hasMediaType(contentType, expected string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && strings.EqualFold(mediaType, expected)
}

func validJSONInitializeResponse(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		Result  json.RawMessage `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return false
	}
	return response.JSONRPC == "2.0" && len(response.Result) > 0 && len(response.Error) == 0
}

func validSSEInitializeResponse(body []byte) bool {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	dataLines := make([]string, 0, 1)
	for _, line := range lines {
		if line == "" {
			if len(dataLines) > 0 {
				return validJSONInitializeResponse([]byte(strings.Join(dataLines, "\n")))
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			return false
		}
		if strings.HasPrefix(value, " ") {
			value = value[1:]
		}
		switch field {
		case "data":
			dataLines = append(dataLines, value)
		case "event", "id", "retry":
			// 这些是 SSE framing 元数据；initialize 响应仍必须来自 data 字段。
		default:
			return false
		}
	}
	return len(dataLines) > 0 && validJSONInitializeResponse([]byte(strings.Join(dataLines, "\n")))
}

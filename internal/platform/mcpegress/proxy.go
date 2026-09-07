package mcpegress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
)

const (
	// CapabilityHeader is the short-lived, task-bound token sent by the
	// controlled Agent to the internal MCP gateway. It is never forwarded to
	// the configured upstream server.
	CapabilityHeader = "X-AIG-MCP-Capability"

	internalGatewayPathPrefix = "/api/internal/mcp-egress/"

	defaultProxyRequestBytes int64 = 1 << 20
)

var proxyRequestHeaderNames = []string{
	"Accept",
	"Content-Type",
	"MCP-Protocol-Version",
	"Last-Event-ID",
}

var proxyResponseHeaderNames = []string{
	"Content-Type",
	"Mcp-Session-Id",
	"MCP-Protocol-Version",
	"Cache-Control",
}

// ProxyDependencies 只接受服务绑定与请求上限，生产调用方不能替换受控传输。
type ProxyDependencies struct {
	Service      *Service
	MaxBodyBytes int64
}

// Proxy is the Agent-only boundary for service-source MCP traffic. Incoming
// request URL, host, credentials, and arbitrary headers are intentionally not
// used to choose the upstream target.
type Proxy struct {
	service       *Service
	newHTTPClient func(*mcpconnections.OutboundPolicy) (*http.Client, error)
	maxBodyBytes  int64
	sessionsMu    sync.Mutex
	sessions      map[string]*proxySSESession
}

func NewProxy(dependencies ProxyDependencies) *Proxy {
	maxBodyBytes := dependencies.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultProxyRequestBytes
	}
	newHTTPClient := func(policy *mcpconnections.OutboundPolicy) (*http.Client, error) {
		return mcpconnections.NewControlledHTTPClient(policy, mcpconnections.ControlledHTTPClientConfig{
			ConnectTimeout:        5 * time.Second,
			RequestTimeout:        maxCapabilityTTL,
			ResponseHeaderTimeout: 15 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
		})
	}
	return &Proxy{service: dependencies.Service, newHTTPClient: newHTTPClient, maxBodyBytes: maxBodyBytes, sessions: make(map[string]*proxySSESession)}
}

// ServeHTTP forwards a single MCP protocol request for an already-routed task
// ID. The caller must place this handler behind AgentManager.RequireInternalToken;
// this method additionally enforces the task's capability token.
func (proxy *Proxy) ServeHTTP(writer http.ResponseWriter, request *http.Request, taskID string) {
	if proxy == nil || proxy.service == nil || request == nil || strings.TrimSpace(taskID) == "" {
		writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
		return
	}
	sessionID, validPath := proxyRequestPath(request, taskID)
	if !allowedProxyMethod(request.Method) || !validPath {
		writeProxyError(writer, http.StatusBadRequest, "MCP 网关请求无效")
		return
	}
	capability, ok := oneHeaderValue(request.Header, CapabilityHeader)
	if !ok {
		writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
		return
	}
	resolveContext, resolveCancel := context.WithTimeout(request.Context(), 5*time.Second)
	bound, err := proxy.service.resolveServiceProxyTarget(resolveContext, taskID, capability)
	resolveCancel()
	if err != nil {
		writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
		return
	}
	endpoint, err := proxyEndpoint(bound.payload.Endpoint)
	if err != nil {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	ctx, cancel := context.WithDeadline(request.Context(), bound.expiresAt)
	defer cancel()
	stopIO := bindProxyIODeadline(ctx, writer)
	defer stopIO()
	go proxy.watchBinding(ctx, cancel, taskID, capability, bound)
	var session *proxySSESession
	if bound.transport == mcpconnections.TransportSSE {
		if sessionID != "" && request.Method == http.MethodPost {
			session = proxy.findSSESession(sessionID, taskID, capability, bound)
			if session == nil {
				writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
				return
			}
			endpoint = session.endpoint
			stop := context.AfterFunc(session.ctx, cancel)
			defer stop()
		} else if sessionID == "" && request.Method == http.MethodGet {
			sessionID, session, err = proxy.reserveSSESession(taskID, capability, bound, ctx)
			if err != nil {
				writeProxyError(writer, http.StatusTooManyRequests, "MCP 网关会话已达上限")
				return
			}
			defer proxy.removeSSESession(sessionID)
		} else {
			writeProxyError(writer, http.StatusBadRequest, "MCP 网关请求无效")
			return
		}
	} else if sessionID != "" {
		writeProxyError(writer, http.StatusBadRequest, "MCP 网关请求无效")
		return
	} else {
		ids := request.Header.Values("Mcp-Session-Id")
		if len(ids) > 0 {
			if len(ids) != 1 || !validProxySessionID(ids[0]) {
				writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
				return
			}
			sessionID = ids[0]
			session = proxy.findHTTPSession(sessionID, taskID, capability, bound)
			if session == nil {
				writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
				return
			}
			stop := context.AfterFunc(session.ctx, cancel)
			defer stop()
		} else if request.Method != http.MethodPost {
			writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
			return
		}
	}
	if request.ContentLength > proxy.maxBodyBytes {
		writeProxyError(writer, http.StatusRequestEntityTooLarge, "MCP 网关请求过大")
		return
	}
	body := http.MaxBytesReader(writer, request.Body, proxy.maxBodyBytes)
	defer body.Close()
	stopBody := context.AfterFunc(ctx, func() { _ = body.Close() })
	defer stopBody()
	bodyBytes, err := io.ReadAll(body)
	if ctx.Err() != nil {
		writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
		return
	}
	if err != nil {
		writeProxyError(writer, http.StatusRequestEntityTooLarge, "MCP 网关请求过大")
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(ctx, request.Method, endpoint.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	copyProxyHeaders(upstreamRequest.Header, request.Header, proxyRequestHeaderNames)
	if bound.transport == mcpconnections.TransportHTTP && session != nil {
		upstreamRequest.Header.Set("Mcp-Session-Id", session.httpSessionID)
	}
	if !injectBoundCredentials(upstreamRequest.Header, bound.payload) {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	client, err := proxy.newHTTPClient(proxy.service.policy)
	if err != nil || client == nil {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	defer client.CloseIdleConnections()
	checkContext, checkCancel := context.WithTimeout(ctx, 2*time.Second)
	current, checkErr := proxy.service.resolveServiceProxyTarget(checkContext, taskID, capability)
	checkCancel()
	if checkErr != nil || !sameProxyBinding(bound, current) || ctx.Err() != nil {
		writeProxyError(writer, http.StatusForbidden, "MCP 网关访问被拒绝")
		return
	}
	upstreamResponse, err := client.Do(upstreamRequest)
	if err != nil || upstreamResponse == nil {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	defer upstreamResponse.Body.Close()
	if upstreamResponse.StatusCode >= http.StatusMultipleChoices && upstreamResponse.StatusCode < http.StatusBadRequest {
		writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
		return
	}
	if bound.transport == mcpconnections.TransportSSE && request.Method == http.MethodGet {
		proxy.forwardLegacySSE(writer, upstreamResponse, endpoint, sessionID, session)
		return
	}

	createdSession := false
	if bound.transport == mcpconnections.TransportHTTP {
		ids := upstreamResponse.Header.Values("Mcp-Session-Id")
		if len(ids) > 0 {
			if len(ids) != 1 || !validUpstreamHTTPSessionID(ids[0]) || session != nil && ids[0] != session.httpSessionID {
				writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
				return
			}
			if session == nil {
				if upstreamResponse.StatusCode >= http.StatusMultipleChoices {
					writeProxyError(writer, http.StatusBadGateway, "MCP 上游不可用")
					return
				}
				sessionID, session, err = proxy.createHTTPSession(taskID, capability, bound, ids[0])
				if err != nil {
					writeProxyError(writer, http.StatusTooManyRequests, "MCP 网关会话已达上限")
					return
				}
				createdSession = true
			}
			upstreamResponse.Header.Set("Mcp-Session-Id", sessionID)
		}
		if session != nil && (request.Method == http.MethodDelete || upstreamResponse.StatusCode == http.StatusNotFound) {
			defer proxy.removeSSESession(sessionID)
		}
	} else {
		upstreamResponse.Header.Del("Mcp-Session-Id")
	}
	if delivered := proxy.forwardProtocolResponse(writer, upstreamResponse, bound, session, ctx); createdSession && !delivered {
		proxy.removeSSESession(sessionID)
	}
}

// Body.Close 可能等待正在执行的 socket Read；取消时同时推进底层读写截止时间，
// 才能唤醒慢请求体及不读取响应的客户端。Gin 的 Unwrap 由 ResponseController 识别。
func bindProxyIODeadline(ctx context.Context, writer http.ResponseWriter) func() {
	controller := http.NewResponseController(writer)
	deadline, _ := ctx.Deadline()
	_ = controller.SetReadDeadline(deadline)
	_ = controller.SetWriteDeadline(deadline)
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(interrupted)
		now := time.Now()
		_ = controller.SetReadDeadline(now)
		_ = controller.SetWriteDeadline(now)
	})
	return func() {
		// 回调必须在处理器返回前结束，避免 Gin 复用 writer 后仍修改下一请求的期限。
		if !stop() {
			<-interrupted
		}
		if ctx.Err() == nil {
			_ = controller.SetReadDeadline(time.Time{})
			_ = controller.SetWriteDeadline(time.Time{})
		}
	}
}

func proxyRequestPath(request *http.Request, taskID string) (string, bool) {
	if request.URL == nil || request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.RawPath != "" ||
		request.URL.Fragment != "" || request.URL.Opaque != "" || strings.ContainsAny(taskID, "/\\%?#") {
		return "", false
	}
	base := internalGatewayPathPrefix + taskID
	if request.URL.Path == base {
		return "", true
	}
	sessionID, found := strings.CutPrefix(request.URL.Path, base+"/sessions/")
	return sessionID, found && validProxySessionID(sessionID) && request.Method == http.MethodPost
}

// 持续连接每秒复核一次任务、能力轮换和配置状态；验证本身也有独立短超时。
func (proxy *Proxy) watchBinding(ctx context.Context, cancel context.CancelFunc, taskID, capability string, original boundSource) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkContext, checkCancel := context.WithTimeout(ctx, 2*time.Second)
			current, err := proxy.service.resolveServiceProxyTarget(checkContext, taskID, capability)
			checkCancel()
			if err != nil || !sameProxyBinding(original, current) {
				cancel()
				return
			}
		}
	}
}

func sameProxyBinding(left, right boundSource) bool {
	return left.configID == right.configID && left.configVersion == right.configVersion && left.transport == right.transport &&
		left.capabilityRotation == right.capabilityRotation && left.expiresAt.Equal(right.expiresAt) &&
		left.payload.Endpoint == right.payload.Endpoint && left.payload.Authentication == right.payload.Authentication &&
		slices.Equal(left.payload.Headers, right.payload.Headers)
}

func allowedProxyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodDelete:
		return true
	default:
		return false
	}
}

func oneHeaderValue(header http.Header, name string) (string, bool) {
	values := header.Values(name)
	if len(values) != 1 || !validRawCapability(values[0]) {
		return "", false
	}
	return values[0], true
}

func proxyEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint == nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return nil, errors.New("invalid MCP proxy endpoint")
	}
	return endpoint, nil
}

func copyProxyHeaders(destination, source http.Header, allowed []string) {
	for _, name := range allowed {
		for _, value := range source.Values(name) {
			destination.Add(name, value)
		}
	}
}

func injectBoundCredentials(header http.Header, payload mcpconnections.ConnectionPayload) bool {
	return mcpconnections.ApplyRuntimeAuthentication(header, payload) == nil
}

func writeProxyError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_, _ = io.WriteString(writer, `{"message":"`+message+`"}`)
}

type proxyFlushWriter struct{ writer http.ResponseWriter }

func (writer proxyFlushWriter) Write(value []byte) (int, error) {
	count, err := writer.writer.Write(value)
	if flusher, ok := writer.writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return count, err
}

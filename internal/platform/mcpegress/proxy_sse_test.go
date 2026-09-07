package mcpegress

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyProductionDependenciesCannotInjectHTTPClient(t *testing.T) {
	_, exposed := reflect.TypeOf(ProxyDependencies{}).FieldByName("NewHTTPClient")
	assert.False(t, exposed, "arbitrary HTTP clients must not bypass production egress policy")
}

func TestProxySSERewritesEndpointAndRoutesSessionPOST(t *testing.T) {
	const taskID = "sse-roundtrip-task"
	const endpoint = "https://mcp.allowed.example.test/private-stream"
	const messageEndpoint = "https://mcp.allowed.example.test/private-messages?session_id=upstream-opaque-secret"
	service, capability := newSSEProxyFixture(t, taskID, endpoint)
	upstreamRequests := make(chan *http.Request, 8)
	streamClosed := make(chan struct{})
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		upstreamRequests <- request.Clone(request.Context())
		if request.Method == http.MethodGet {
			reader, writer := io.Pipe()
			go func() {
				defer writer.Close()
				_, _ = io.WriteString(writer, "event: endpoint\ndata: /private-messages?session_id=upstream-opaque-secret\n\nevent: message\ndata: {\"result\":\"ready\"}\n\n")
				<-request.Context().Done()
				close(streamClosed)
			}()
			return proxyResponse(request, http.StatusOK, "text/event-stream", reader), nil
		}
		response := proxyResponse(request, http.StatusAccepted, "application/json", io.NopCloser(strings.NewReader(`{}`)))
		response.Header.Set("Mcp-Session-Id", "unexpected-legacy-http-session-secret")
		return response, nil
	}))
	server := newProxyHTTPServer(t, proxy, taskID)
	request, err := http.NewRequest(http.MethodGet, server.URL+internalGatewayPathPrefix+taskID, nil)
	require.NoError(t, err)
	request.Header.Set(CapabilityHeader, capability)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	frame := readProxySSEFrame(t, bufio.NewReader(response.Body))
	assert.NotContains(t, frame, "private-messages")
	assert.NotContains(t, frame, "upstream-opaque-secret")
	assert.NotContains(t, frame, endpoint)
	lines := strings.Split(frame, "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	path := strings.TrimPrefix(lines[1], "data: ")
	require.True(t, strings.HasPrefix(path, internalGatewayPathPrefix+taskID+"/sessions/"), frame)
	getRequest := <-upstreamRequests
	assert.Equal(t, endpoint, getRequest.URL.String())
	assert.Equal(t, "Bearer sse-bound-auth-secret", getRequest.Header.Get("Authorization"))
	posted := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"method":"initialize"}`))
	posted.Header.Set(CapabilityHeader, capability)
	posted.Header.Set("Authorization", "Bearer agent-override")
	posted.Host = "attacker.example.test"
	postResponse := httptest.NewRecorder()
	proxy.ServeHTTP(postResponse, posted, taskID)
	require.Equal(t, http.StatusAccepted, postResponse.Code, postResponse.Body.String())
	assert.Empty(t, postResponse.Header().Get("Mcp-Session-Id"), "legacy SSE responses must not expose raw HTTP session headers")
	postRequest := <-upstreamRequests
	assert.Equal(t, messageEndpoint, postRequest.URL.String())
	assert.Equal(t, "Bearer sse-bound-auth-secret", postRequest.Header.Get("Authorization"))
	assert.Empty(t, postRequest.Header.Get(CapabilityHeader))
	assert.Equal(t, "mcp.allowed.example.test", postRequest.Host)
	_ = response.Body.Close()
	select {
	case <-streamClosed:
	case <-time.After(3 * time.Second):
		t.Fatal("closing the gateway stream did not cancel the upstream")
	}
	closedResponse := httptest.NewRecorder()
	proxy.ServeHTTP(closedResponse, posted.Clone(context.Background()), taskID)
	assert.Equal(t, http.StatusForbidden, closedResponse.Code, "closed sessions cannot make further upstream requests")
}

func TestProxySSERejectsInvalidEndpointsWithoutDisclosure(t *testing.T) {
	for name, stream := range map[string]string{
		"cross origin":     "event: endpoint\ndata: https://attacker.example.test/messages?token=opaque-secret\n\n",
		"http":             "event: endpoint\ndata: http://mcp.allowed.example.test/messages\n\n",
		"userinfo":         "event: endpoint\ndata: https://user:secret@mcp.allowed.example.test/messages\n\n",
		"fragment":         "event: endpoint\ndata: /messages#secret\n\n",
		"encoded control":  "event: endpoint\ndata: /messages%0d%0aInjected?session=secret\n\n",
		"query url":        "event: endpoint\ndata: /messages?url=https%3A%2F%2Fattacker.example.test\n\n",
		"oversize":         "event: endpoint\ndata: /messages?session=" + strings.Repeat("x", 70<<10) + "\n\n",
		"multiline":        "event: endpoint\ndata: /messages\ndata: ?session=secret\n\n",
		"duplicate field":  "event: endpoint\nevent: message\ndata: /messages?session=secret\n\n",
		"missing endpoint": "event: message\ndata: private-upstream-sentinel\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			const taskID = "sse-invalid-task"
			service, capability := newSSEProxyFixture(t, taskID, "https://mcp.allowed.example.test/stream")
			proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				return proxyResponse(request, http.StatusOK, "text/event-stream", io.NopCloser(strings.NewReader(stream))), nil
			}))
			request := httptest.NewRequest(http.MethodGet, internalGatewayPathPrefix+taskID, nil)
			request.Header.Set(CapabilityHeader, capability)
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request, taskID)
			assert.Equal(t, http.StatusBadGateway, response.Code)
			assert.NotContains(t, response.Body.String(), "secret")
			assert.NotContains(t, response.Body.String(), "messages")
			assert.NotContains(t, response.Body.String(), "private-upstream-sentinel")
		})
	}
}

func TestProxySSEDuplicateEndpointClosesWithoutEmittingRawEndpoint(t *testing.T) {
	const taskID = "sse-duplicate-task"
	service, capability := newSSEProxyFixture(t, taskID, "https://mcp.allowed.example.test/stream")
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		return proxyResponse(request, http.StatusOK, "text/event-stream", io.NopCloser(strings.NewReader("event: endpoint\ndata: /messages?session=first-secret\n\nevent: endpoint\ndata: /messages?session=second-secret\n\nevent: message\ndata: must-not-continue\n\n"))), nil
	}))
	request := httptest.NewRequest(http.MethodGet, internalGatewayPathPrefix+taskID, nil)
	request.Header.Set(CapabilityHeader, capability)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, taskID)
	assert.Equal(t, 1, strings.Count(response.Body.String(), "event: endpoint"))
	assert.NotContains(t, response.Body.String(), "secret")
	assert.NotContains(t, response.Body.String(), "must-not-continue")
}

func TestProxySSESessionRejectsOtherTaskAndRotation(t *testing.T) {
	const taskID = "sse-scope-task"
	service, capability := newSSEProxyFixture(t, taskID, "https://mcp.allowed.example.test/stream")
	task := *service.tasks.(*memoryTaskReader).records[taskID]
	task.ID = "other-sse-task"
	service.tasks.(*memoryTaskReader).records[task.ID] = &task
	bindings := service.bindings.(*memoryBindingReader)
	binding := *bindings.bindings[taskID]
	binding.TaskID = task.ID
	bindings.bindings[task.ID] = &binding
	_, err := service.IssueRuntime(context.Background(), task.ID)
	require.NoError(t, err)
	proxy, response, path, _ := openProxySSESession(t, service, taskID, capability)
	for _, candidate := range []struct{ taskID, capability, path string }{
		{task.ID, capability, strings.Replace(path, taskID, task.ID, 1)},
		{taskID, "incorrect-capability", path},
	} {
		request := httptest.NewRequest(http.MethodPost, candidate.path, nil)
		request.Header.Set(CapabilityHeader, candidate.capability)
		result := httptest.NewRecorder()
		proxy.ServeHTTP(result, request, candidate.taskID)
		assert.Equal(t, http.StatusForbidden, result.Code)
	}
	// 同一明文能力值重新签发后，旧会话也不能跨轮换复用。
	_, err = service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, path, nil)
	request.Header.Set(CapabilityHeader, capability)
	result := httptest.NewRecorder()
	proxy.ServeHTTP(result, request, taskID)
	assert.Equal(t, http.StatusForbidden, result.Code)
	_, err = io.ReadAll(response.Body)
	requireProxyStreamClosed(t, err)
}

func TestProxySSELongStreamClosesAtExpiryOrTerminalState(t *testing.T) {
	for _, cause := range []string{"expiry", "terminal", "disabled config", "binding version"} {
		t.Run(cause, func(t *testing.T) {
			const taskID = "sse-lifecycle-task"
			service, _ := newSSEProxyFixture(t, taskID, "https://mcp.allowed.example.test/stream")
			if cause == "expiry" {
				service.capabilityTTL = 200 * time.Millisecond
			}
			reader := &proxyTaskStateReader{source: service.tasks}
			service.tasks = reader
			bindingReader := &proxyBindingStateReader{source: service.bindings}
			service.bindings = bindingReader
			runtime, err := service.IssueRuntime(context.Background(), taskID)
			require.NoError(t, err)
			proxy, response, path, deadline := openProxySSESession(t, service, taskID, runtime.TaskCapability)
			require.False(t, deadline.IsZero(), "upstream request context needs capability deadline")
			switch cause {
			case "terminal":
				reader.terminal.Store(true)
			case "disabled config":
				bindingReader.disabled.Store(true)
			case "binding version":
				bindingReader.versionChanged.Store(true)
			}
			started := time.Now()
			_, err = io.ReadAll(response.Body)
			requireProxyStreamClosed(t, err)
			assert.Less(t, time.Since(started), 3*time.Second)
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set(CapabilityHeader, runtime.TaskCapability)
			result := httptest.NewRecorder()
			proxy.ServeHTTP(result, request, taskID)
			assert.Equal(t, http.StatusForbidden, result.Code)
		})
	}
}

func TestProxySSEBoundsActiveSessionsAndRemovesThem(t *testing.T) {
	const taskID = "sse-limits-task"
	service, capability := newSSEProxyFixture(t, taskID, "https://mcp.allowed.example.test/stream")
	proxy, firstResponse, _, _ := openProxySSESession(t, service, taskID, capability)
	server := newProxyHTTPServer(t, proxy, taskID)
	open := func() *http.Response {
		request, err := http.NewRequest(http.MethodGet, server.URL+internalGatewayPathPrefix+taskID, nil)
		require.NoError(t, err)
		request.Header.Set(CapabilityHeader, capability)
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		t.Cleanup(func() { _ = response.Body.Close() })
		return response
	}
	for index := 1; index < 4; index++ {
		require.Equal(t, http.StatusOK, open().StatusCode)
	}
	assert.Equal(t, http.StatusTooManyRequests, open().StatusCode)
	_ = firstResponse.Body.Close()
	require.Eventually(t, func() bool {
		proxy.sessionsMu.Lock()
		defer proxy.sessionsMu.Unlock()
		return len(proxy.sessions) == 3
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, http.StatusOK, open().StatusCode)
}

func TestProxySSEEndpointOriginValidation(t *testing.T) {
	base, err := proxyEndpoint("https://mcp.allowed.example.test/sse")
	require.NoError(t, err)
	for _, raw := range []string{"/messages?session_id=abc-123", "https://MCP.ALLOWED.EXAMPLE.TEST:443/messages?session=abc", "messages?session_id=abc"} {
		_, err := proxyMessageEndpoint(base, raw)
		assert.NoError(t, err, raw)
	}
	for _, raw := range []string{"https://mcp.allowed.example.test:8443/messages", "//attacker.example.test/messages", "/messages?session=one&session=two", "/messages?session=", "/messages%7f", "/messages#", "/messages?next=https://attacker.example.test", "/%252e%252e/secret"} {
		_, err := proxyMessageEndpoint(base, raw)
		assert.Error(t, err, raw)
	}
}

func TestProxySSERewritesHeartbeatCommentsAndPreservesEventFrames(t *testing.T) {
	const taskID = "sse-heartbeat-task"
	service, capability := newSSEProxyFixture(t, taskID, "https://mcp.allowed.example.test/sse")
	stream := ": secret-upstream-comment\r\n\r\nevent: endpoint\r\ndata: /messages?session=abc\r\n\r\n: sse-bound-auth-secret\r\n\r\nevent: message\rid: protocol-id\rdata: {\"result\":\"ok\"}\r\r"
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		return proxyResponse(request, http.StatusOK, "text/event-stream", io.NopCloser(strings.NewReader(stream))), nil
	}))
	request := httptest.NewRequest(http.MethodGet, internalGatewayPathPrefix+taskID, nil)
	request.Header.Set(CapabilityHeader, capability)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, taskID)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), ": keepalive\n\n")
	assert.Contains(t, response.Body.String(), "id: protocol-id")
	assert.Contains(t, response.Body.String(), `data: {"result":"ok"}`)
	assert.NotContains(t, response.Body.String(), "secret")
}

type proxyTaskStateReader struct {
	source   TaskReader
	terminal atomic.Bool
}

func (reader *proxyTaskStateReader) Get(ctx context.Context, taskID string) (*tasks.Task, error) {
	task, err := reader.source.Get(ctx, taskID)
	if task != nil && reader.terminal.Load() {
		task.Status = tasks.StatusCancelled
	}
	return task, err
}

type proxyBindingStateReader struct {
	source                   BindingReader
	disabled, versionChanged atomic.Bool
}

func (reader *proxyBindingStateReader) GetConfig(ctx context.Context, id string) (*mcpconnections.ConnectionConfig, error) {
	config, err := reader.source.GetConfig(ctx, id)
	if config != nil && reader.disabled.Load() {
		config.Enabled = false
	}
	return config, err
}
func (reader *proxyBindingStateReader) GetVersion(ctx context.Context, id string, version int) (*mcpconnections.ConnectionVersion, error) {
	return reader.source.GetVersion(ctx, id, version)
}
func (reader *proxyBindingStateReader) GetTaskBinding(ctx context.Context, id string) (*mcpconnections.TaskBinding, error) {
	binding, err := reader.source.GetTaskBinding(ctx, id)
	if binding != nil && reader.versionChanged.Load() {
		*binding.ConnectionConfigVersion = 2
	}
	return binding, err
}

func openProxySSESession(t *testing.T, service *Service, taskID, capability string) (*Proxy, *http.Response, string, time.Time) {
	t.Helper()
	deadlines := make(chan time.Time, 8)
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost {
			return proxyResponse(request, http.StatusAccepted, "application/json", io.NopCloser(strings.NewReader(`{}`))), nil
		}
		deadline, _ := request.Context().Deadline()
		deadlines <- deadline
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			_, _ = io.WriteString(writer, "event: endpoint\ndata: /messages?session_id=server-opaque-secret\n\n")
			<-request.Context().Done()
		}()
		return proxyResponse(request, http.StatusOK, "text/event-stream", reader), nil
	}))
	server := newProxyHTTPServer(t, proxy, taskID)
	request, err := http.NewRequest(http.MethodGet, server.URL+internalGatewayPathPrefix+taskID, nil)
	require.NoError(t, err)
	request.Header.Set(CapabilityHeader, capability)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)
	frame := readProxySSEFrame(t, bufio.NewReader(response.Body))
	path := strings.TrimPrefix(strings.Split(frame, "\n")[1], "data: ")
	return proxy, response, path, <-deadlines
}

func newSSEProxyFixture(t *testing.T, taskID, endpoint string) (*Service, string) {
	t.Helper()
	service := newProxyServiceFixture(t, taskID, endpoint, "sse-bound-auth-secret", "sse-task-capability")
	version := service.bindings.(*memoryBindingReader).versions[connectionVersionKey("proxy-service-config", 1)]
	version.Transport = mcpconnections.TransportSSE
	version.DetectedTransport = mcpconnections.TransportSSE
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	return service, runtime.TaskCapability
}

func proxyWithTransport(service *Service, transport http.RoundTripper) *Proxy {
	proxy := NewProxy(ProxyDependencies{Service: service})
	proxy.newHTTPClient = func(*mcpconnections.OutboundPolicy) (*http.Client, error) {
		return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
	}
	return proxy
}

func proxyResponse(request *http.Request, status int, contentType string, body io.ReadCloser) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{contentType}}, Body: body, Request: request}
}

func newProxyHTTPServer(t *testing.T, proxy *Proxy, taskID string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		proxy.ServeHTTP(writer, request, taskID)
	}))
	t.Cleanup(server.Close)
	server.Client().Timeout = 5 * time.Second
	return server
}

func readProxySSEFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var result strings.Builder
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		result.WriteString(line)
		if line == "\n" || line == "\r\n" {
			return result.String()
		}
	}
}

func requireProxyStreamClosed(t *testing.T, err error) {
	t.Helper()
	// 能力撤销会直接终止 socket 读写；已提交响应可能以截断 EOF 结束。
	if err != nil {
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	}
}

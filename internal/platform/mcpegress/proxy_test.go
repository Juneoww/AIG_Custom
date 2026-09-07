package mcpegress

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type proxyRoundTripper func(*http.Request) (*http.Response, error)

type proxyTestDependencies struct {
	Service       *Service
	NewHTTPClient func(*mcpconnections.OutboundPolicy) (*http.Client, error)
}

func newProxyForTest(dependencies proxyTestDependencies) *Proxy {
	proxy := NewProxy(ProxyDependencies{Service: dependencies.Service})
	proxy.newHTTPClient = dependencies.NewHTTPClient
	return proxy
}

func (roundTripper proxyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestProxyForwardsOnlyBoundServiceEndpointAndGatewayCredentials(t *testing.T) {
	const (
		taskID       = "proxy-service-task"
		endpoint     = "https://mcp.allowed.example.test/private-mcp-endpoint"
		capability   = "proxy-capability-sentinel"
		bearerSecret = "proxy-bearer-sentinel"
	)
	service := newProxyServiceFixture(t, taskID, endpoint, bearerSecret, capability)
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)

	var upstreamRequest *http.Request
	proxy := newProxyForTest(proxyTestDependencies{
		Service: service,
		NewHTTPClient: func(*mcpconnections.OutboundPolicy) (*http.Client, error) {
			return &http.Client{Transport: proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				body, readErr := io.ReadAll(request.Body)
				require.NoError(t, readErr)
				upstreamRequest = request.Clone(request.Context())
				upstreamRequest.Body = io.NopCloser(bytes.NewReader(body))
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}, "Mcp-Session-Id": []string{"bound-session"}},
					Body:       io.NopCloser(bytes.NewBufferString(`{"result":"ok"}`)),
					Request:    request,
				}, nil
			})}, nil
		},
	})

	request := httptest.NewRequest(http.MethodPost, "https://platform.internal.example.test/api/internal/mcp-egress/"+taskID, bytes.NewBufferString(`{"method":"initialize"}`))
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cookie", "agent-cookie-sentinel")
	request.Header.Set("X-Internal-Agent-Token", "agent-token-sentinel")
	request.Header.Set("X-Agent-Selected-Target", "https://attacker.example.test")
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, request, taskID)

	require.Equal(t, http.StatusOK, response.Code)
	require.NotNil(t, upstreamRequest)
	assert.Equal(t, endpoint, upstreamRequest.URL.String())
	assert.Equal(t, "Bearer "+bearerSecret, upstreamRequest.Header.Get("Authorization"))
	assert.Empty(t, upstreamRequest.Header.Get("Mcp-Session-Id"), "new sessions are selected only by the bound upstream")
	assert.Equal(t, "application/json", upstreamRequest.Header.Get("Accept"))
	assert.Empty(t, upstreamRequest.Header.Get(CapabilityHeader))
	assert.Empty(t, upstreamRequest.Header.Get("Cookie"))
	assert.Empty(t, upstreamRequest.Header.Get("X-Internal-Agent-Token"))
	assert.Empty(t, upstreamRequest.Header.Get("X-Agent-Selected-Target"))
	assert.Equal(t, `{"method":"initialize"}`, string(mustReadProxyBody(t, upstreamRequest.Body)))
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	assert.True(t, validProxySessionID(response.Header().Get("Mcp-Session-Id")))
	assert.NotEqual(t, "bound-session", response.Header().Get("Mcp-Session-Id"))
	assert.Equal(t, `{"result":"ok"}`, response.Body.String())
}

func TestProxyAppliesSafeCustomHeadersBeforeBoundBearerCredential(t *testing.T) {
	const (
		taskID       = "proxy-bearer-custom-header-task"
		endpoint     = "https://mcp.allowed.example.test/bearer-with-custom"
		capability   = "proxy-bearer-custom-capability"
		bearerSecret = "proxy-bearer-custom-secret"
	)
	service := newProxyServiceFixture(t, taskID, endpoint, bearerSecret, capability)
	bindings := service.bindings.(*memoryBindingReader)
	config := bindings.configs["proxy-service-config"]
	version := bindings.versions[connectionVersionKey(config.ID, 1)]
	require.NoError(t, service.keyring.SealConnectionPayload(config, version, mcpconnections.ConnectionPayload{
		Endpoint: endpoint,
		Authentication: mcpconnections.Authentication{
			Kind: mcpconnections.AuthenticationBearer, Secret: bearerSecret,
		},
		Headers: []mcpconnections.Header{
			{Name: "Authorization", Value: "custom-value-must-be-overridden"},
			{Name: "X-Intranet-Role", Value: "scanner"},
		},
	}))
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	var upstreamRequest *http.Request
	proxy := newProxyForTest(proxyTestDependencies{
		Service: service,
		NewHTTPClient: func(*mcpconnections.OutboundPolicy) (*http.Client, error) {
			return &http.Client{Transport: proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				upstreamRequest = request.Clone(request.Context())
				return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
			})}, nil
		},
	})
	request := httptest.NewRequest(http.MethodPost, "https://platform.internal.example.test/api/internal/mcp-egress/"+taskID, bytes.NewBufferString(`{"method":"initialize"}`))
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, request, taskID)

	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	require.NotNil(t, upstreamRequest)
	assert.Equal(t, "Bearer "+bearerSecret, upstreamRequest.Header.Get("Authorization"))
	assert.Equal(t, "scanner", upstreamRequest.Header.Get("X-Intranet-Role"))
}

func TestProxyRejectsCapabilityWhenBoundVersionLosesProbePass(t *testing.T) {
	const (
		taskID     = "proxy-probe-state-task"
		endpoint   = "https://mcp.allowed.example.test/probe-state"
		capability = "proxy-probe-state-capability"
	)
	service := newProxyServiceFixture(t, taskID, endpoint, "proxy-probe-state-secret", capability)
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)

	bindings := service.bindings.(*memoryBindingReader)
	bindings.versions[connectionVersionKey("proxy-service-config", 1)].ProbeStatus = mcpconnections.ProbeStatusFailed
	upstreamCalled := false
	proxy := newProxyForTest(proxyTestDependencies{
		Service: service,
		NewHTTPClient: func(*mcpconnections.OutboundPolicy) (*http.Client, error) {
			return &http.Client{Transport: proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				upstreamCalled = true
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
			})}, nil
		},
	})
	request := httptest.NewRequest(http.MethodPost, "https://platform.internal.example.test/api/internal/mcp-egress/"+taskID, bytes.NewBufferString(`{"method":"initialize"}`))
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, request, taskID)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.False(t, upstreamCalled)
}

func TestProxyRejectsAgentSelectedGatewayPath(t *testing.T) {
	const (
		taskID     = "proxy-path-task"
		endpoint   = "https://mcp.allowed.example.test/fixed-mcp-endpoint"
		capability = "proxy-path-capability"
	)
	service := newProxyServiceFixture(t, taskID, endpoint, "proxy-path-secret", capability)
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	upstreamCalled := false
	proxy := newProxyForTest(proxyTestDependencies{
		Service: service,
		NewHTTPClient: func(*mcpconnections.OutboundPolicy) (*http.Client, error) {
			return &http.Client{Transport: proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				upstreamCalled = true
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil)), Request: request}, nil
			})}, nil
		},
	})
	request := httptest.NewRequest(http.MethodPost, "https://platform.internal.example.test/api/internal/mcp-egress/"+taskID+"/attacker-selected-path", bytes.NewBufferString(`{"method":"initialize"}`))
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	response := httptest.NewRecorder()

	proxy.ServeHTTP(response, request, taskID)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.False(t, upstreamCalled)
}

func mustReadProxyBody(t *testing.T, body io.ReadCloser) []byte {
	t.Helper()
	value, err := io.ReadAll(body)
	require.NoError(t, err)
	return value
}

func newProxyServiceFixture(t *testing.T, taskID, endpoint, bearerSecret, capability string) *Service {
	t.Helper()
	keyring := testEgressKeyring(t)
	configID := "proxy-service-config"
	config := &mcpconnections.ConnectionConfig{ID: configID, OwnerUserID: "proxy-owner", Scope: mcpconnections.ScopePrivate, Enabled: true}
	version := &mcpconnections.ConnectionVersion{
		ID: "proxy-service-version", ConnectionConfigID: configID, Version: 1,
		Transport: mcpconnections.TransportHTTP, DetectedTransport: mcpconnections.TransportHTTP, ProbeStatus: mcpconnections.ProbeStatusPassed,
	}
	require.NoError(t, keyring.SealConnectionPayload(config, version, mcpconnections.ConnectionPayload{
		Endpoint: endpoint,
		Authentication: mcpconnections.Authentication{
			Kind: mcpconnections.AuthenticationBearer, Secret: bearerSecret,
		},
	}))
	versionValue := version.Version
	return NewService(ServiceDependencies{
		Tasks: &memoryTaskReader{records: map[string]*tasks.Task{
			taskID: {ID: taskID, OwnerUserID: config.OwnerUserID, TaskType: "mcp_scan", Status: tasks.StatusPending},
		}},
		Bindings: &memoryBindingReader{
			bindings: map[string]*mcpconnections.TaskBinding{
				taskID: {ID: "proxy-service-binding", TaskID: taskID, SourceKind: "service", ConnectionConfigID: &configID, ConnectionConfigVersion: &versionValue},
			},
			configs:  map[string]*mcpconnections.ConnectionConfig{configID: config},
			versions: map[string]*mcpconnections.ConnectionVersion{connectionVersionKey(configID, version.Version): version},
		},
		Keyring: keyring, Capabilities: NewMemoryCapabilityRepository(), Policy: testEgressPolicy(t),
		GatewayURL: "https://platform.internal.example.test",
		NewCapability: func() (string, error) {
			return capability, nil
		},
	})
}

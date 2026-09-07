package mcpegress

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyRedactsBoundValuesAcrossChunksAndJSONEscapes(t *testing.T) {
	const taskID = "proxy-redaction-task"
	const endpoint = "https://mcp.allowed.example.test/private-endpoint"
	const secret = "proxy-secret-value"
	for _, contentType := range []string{"application/json", "text/event-stream"} {
		t.Run(contentType, func(t *testing.T) {
			service := newProxyServiceFixture(t, taskID, endpoint, secret, "redaction-capability")
			runtime, err := service.IssueRuntime(context.Background(), taskID)
			require.NoError(t, err)
			payload := `{"jsonrpc":"2.0","id":"preserve-protocol-id-12345678901234567890","result":{"raw":"proxy-secret-value","escaped":"proxy-\u0073ecret-value","url":"https:\/\/mcp.allowed.example.test\/private-endpoint"}}`
			if contentType == "text/event-stream" {
				payload = "id: protocol-event-12345678901234567890\nevent: message\ndata: " + payload + "\n\n"
			}
			proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				response := proxyResponse(request, http.StatusOK, contentType, io.NopCloser(&proxyByteReader{value: []byte(payload)}))
				response.Header.Set("Authorization", "Bearer "+secret)
				response.Header.Set("Set-Cookie", "cookie="+secret)
				response.Header.Set("Content-Length", "9999")
				return response, nil
			}))
			request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, nil)
			request.Header.Set(CapabilityHeader, runtime.TaskCapability)
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request, taskID)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			assert.NotContains(t, response.Body.String(), secret)
			assert.NotContains(t, response.Body.String(), `\u0073ecret`)
			assert.NotContains(t, response.Body.String(), "private-endpoint")
			assert.Contains(t, response.Body.String(), "preserve-protocol-id-12345678901234567890")
			assert.Empty(t, response.Header().Get("Authorization"))
			assert.Empty(t, response.Header().Get("Set-Cookie"))
			assert.Empty(t, response.Header().Get("Content-Length"))
		})
	}
}

func TestProxyRejectsUnknownMalformedAndOversizeResponsesSafely(t *testing.T) {
	for _, test := range []struct{ name, contentType, body string }{
		{"html error", "text/html", "<html>https://mcp.allowed.example.test/private password=secret</html>"},
		{"invalid json", "application/json", `{"error":"secret"`},
		{"multiple json", "application/json", `{} {"error":"secret"}`},
		{"large json", "application/json", `{"data":"` + strings.Repeat("x", 5<<20) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			const taskID = "proxy-malformed-task"
			service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/private", "secret", "malformed-capability")
			runtime, err := service.IssueRuntime(context.Background(), taskID)
			require.NoError(t, err)
			proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				return proxyResponse(request, http.StatusOK, test.contentType, io.NopCloser(strings.NewReader(test.body))), nil
			}))
			request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, nil)
			request.Header.Set(CapabilityHeader, runtime.TaskCapability)
			response := httptest.NewRecorder()
			proxy.ServeHTTP(response, request, taskID)
			assert.Equal(t, http.StatusBadGateway, response.Code)
			assert.Less(t, response.Body.Len(), 1024)
			assert.NotContains(t, response.Body.String(), "secret")
		})
	}
}

func TestProxyControlledClientPinsDialIPKeepsSNIAndBlocksEnvironmentProxyRedirect(t *testing.T) {
	var environmentProxyCalls atomic.Int32
	environmentProxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		environmentProxyCalls.Add(1)
		writer.WriteHeader(http.StatusBadGateway)
	}))
	defer environmentProxy.Close()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, environmentProxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	var upstreamCalls atomic.Int32
	observed := make(chan *http.Request, 2)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upstreamCalls.Add(1)
		observed <- request.Clone(request.Context())
		writer.Header().Set("Location", "https://attacker.example.test/redirect-secret")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer upstream.Close()
	name := upstream.Certificate().DNSNames[0]
	const taskID = "controlled-client-task"
	service := newProxyServiceFixture(t, taskID, "https://"+name+"/bound-mcp", "controlled-secret", "controlled-capability")
	dialed := make(chan string, 2)
	policy, err := mcpconnections.NewOutboundPolicy(mcpconnections.OutboundPolicyConfig{
		AllowedCIDRs: []string{"203.0.113.0/24"}, ControlledDialerAvailable: true,
		Resolver: egressPolicyResolver(func(context.Context, string) ([]net.IPAddr, error) {
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.44")}}, nil
		}),
		Dialer: proxyTestDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
			dialed <- address
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}),
	})
	require.NoError(t, err)
	service.policy = policy
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	proxy := NewProxy(ProxyDependencies{Service: service})
	controlledFactory := proxy.newHTTPClient
	// 测试只增加本地 TLS 证书信任，HTTP 传输、代理与重定向逻辑仍由生产构造器创建。
	proxy.newHTTPClient = func(policy *mcpconnections.OutboundPolicy) (*http.Client, error) {
		client, err := controlledFactory(policy)
		if err != nil {
			return nil, err
		}
		roots := x509.NewCertPool()
		roots.AddCert(upstream.Certificate())
		client.Transport.(*http.Transport).TLSClientConfig.RootCAs = roots
		return client, nil
	}
	request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, strings.NewReader(`{}`))
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, taskID)
	require.Equal(t, http.StatusBadGateway, response.Code)
	assert.Equal(t, int32(1), upstreamCalls.Load())
	assert.Zero(t, environmentProxyCalls.Load())
	assert.Equal(t, "203.0.113.44:443", <-dialed)
	actual := <-observed
	assert.Equal(t, name, actual.TLS.ServerName)
	assert.Equal(t, name, actual.Host)
	assert.Equal(t, "/bound-mcp", actual.URL.Path)
	assert.Equal(t, "Bearer controlled-secret", actual.Header.Get("Authorization"))
	assert.NotContains(t, response.Body.String(), "redirect-secret")
	assert.Empty(t, response.Header().Get("Location"))
}

func TestProxyClosesOwnedIdleTransport(t *testing.T) {
	const taskID = "proxy-idle-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "idle-secret", "idle-capability")
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	transport := &proxyCloseIdleTransport{}
	proxy := proxyWithTransport(service, transport)
	request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, nil)
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	proxy.ServeHTTP(httptest.NewRecorder(), request, taskID)
	assert.Equal(t, int32(1), transport.closed.Load())
}

func TestProxyRejectsChunkedOversizeRequestBeforeUpstream(t *testing.T) {
	const taskID = "proxy-request-size-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "size-secret", "size-capability")
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	var called atomic.Bool
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		called.Store(true)
		return proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{}`))), nil
	}))
	request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	request.ContentLength = -1
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request, taskID)
	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
	assert.False(t, called.Load())
}

func TestProxyRedactsExactNumericSecretsWithoutChangingOtherNumbers(t *testing.T) {
	const taskID = "numeric-secret-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "123456789", "numeric-capability")
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		return proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":7,"error":{"code":-32000,"data":123456789,"other":1234567890}}`))), nil
	}))
	result := proxyHTTPCall(proxy, http.MethodPost, taskID, runtime.TaskCapability, "")
	require.Equal(t, http.StatusOK, result.Code)
	assert.JSONEq(t, `{"jsonrpc":"2.0","id":7,"error":{"code":-32000,"data":"[REDACTED]","other":1234567890}}`, result.Body.String())
}

func TestProxyRedactsExactBooleanAndNullSecrets(t *testing.T) {
	for _, literal := range []string{"true", "false", "null"} {
		t.Run(literal, func(t *testing.T) {
			result, err := sanitizeProxyJSON([]byte(`{"data":`+literal+`,"id":7}`), []string{literal})
			require.NoError(t, err)
			assert.JSONEq(t, `{"data":"[REDACTED]","id":7}`, string(result))
		})
	}
}

func TestProxyHTTPFlushesIdleEventStreamHeadersAndClosesAtExpiry(t *testing.T) {
	const taskID = "http-idle-stream-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "idle-stream-secret", "idle-stream-capability")
	service.capabilityTTL = time.Second
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() { <-request.Context().Done(); _ = writer.Close() }()
		return proxyResponse(request, http.StatusOK, "text/event-stream", reader), nil
	}))
	server := newProxyHTTPServer(t, proxy, taskID)
	request, err := http.NewRequest(http.MethodPost, server.URL+internalGatewayPathPrefix+taskID, nil)
	require.NoError(t, err)
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	started := time.Now()
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	assert.Less(t, time.Since(started), 500*time.Millisecond, "stream headers must not wait for first message")
	assert.Equal(t, http.StatusOK, response.StatusCode)
	_, err = io.ReadAll(response.Body)
	requireProxyStreamClosed(t, err)
}

type proxyByteReader struct{ value []byte }

func (reader *proxyByteReader) Read(buffer []byte) (int, error) {
	if len(reader.value) == 0 {
		return 0, io.EOF
	}
	buffer[0] = reader.value[0]
	reader.value = reader.value[1:]
	return 1, nil
}

type proxyTestDialer func(context.Context, string, string) (net.Conn, error)

func (dialer proxyTestDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return dialer(ctx, network, address)
}

type proxyCloseIdleTransport struct{ closed atomic.Int32 }

func (transport *proxyCloseIdleTransport) CloseIdleConnections() { transport.closed.Add(1) }
func (transport *proxyCloseIdleTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return proxyResponse(request, http.StatusAccepted, "application/json", io.NopCloser(strings.NewReader(`{}`))), nil
}

package mcpconnections

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedProbeClock struct{ now time.Time }

func (clock *fixedProbeClock) Now() time.Time { return clock.now }

func validInitializeResponseJSON() string {
	return `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"test-server","version":"1.0"}}}`
}

func validInitializeResponseSSE() string {
	return "event: message\ndata: " + validInitializeResponseJSON() + "\n\n"
}

type timeoutProbePort struct{}

func (timeoutProbePort) Initialize(ctx context.Context, _ ProbeRequest) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestProbeEngineAutoUsesOnlyStreamableHTTPThenSSE(t *testing.T) {
	port := &scriptedProbePort{errors: map[Transport]error{
		TransportHTTP: errors.New("streamable endpoint did not accept initialize"),
		TransportSSE:  nil,
	}}
	engine := NewProbeEngine(port, ProbeOptions{Timeout: time.Second})

	result, err := engine.Probe(context.Background(), "config-auto-order", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportAuto)
	require.NoError(t, err)
	assert.Equal(t, TransportSSE, result.DetectedTransport)
	assert.Equal(t, []Transport{TransportHTTP, TransportSSE}, port.attempts)
}

func TestProbeEngineFixedTransportNeverFallsBack(t *testing.T) {
	port := &scriptedProbePort{errors: map[Transport]error{
		TransportHTTP: errors.New("fixed transport failed"),
		TransportSSE:  nil,
	}}
	engine := NewProbeEngine(port, ProbeOptions{Timeout: time.Second})

	_, err := engine.Probe(context.Background(), "config-fixed-transport", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.Equal(t, []Transport{TransportHTTP}, port.attempts)
}

func TestProbeEngineBoundsTimeoutAndRateLimitsWithoutLeakingProbeError(t *testing.T) {
	clock := &fixedProbeClock{now: time.Date(2026, 9, 3, 3, 4, 5, 0, time.UTC)}
	engine := NewProbeEngine(timeoutProbePort{}, ProbeOptions{
		Timeout:         10 * time.Millisecond,
		MinimumInterval: time.Minute,
		Clock:           clock,
	})

	_, err := engine.Probe(context.Background(), "config-timeout-rate", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.NotContains(t, err.Error(), "safe.example.test")
	_, err = engine.Probe(context.Background(), "config-timeout-rate", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeRateLimited)
}

func TestProbeEngineAppliesSafeDefaultMinimumInterval(t *testing.T) {
	clock := &fixedProbeClock{now: time.Date(2026, 9, 3, 3, 4, 5, 0, time.UTC)}
	port := &scriptedProbePort{errors: map[Transport]error{TransportHTTP: nil}}
	engine := NewProbeEngine(port, ProbeOptions{Clock: clock})

	_, err := engine.Probe(context.Background(), "config-default-rate", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.NoError(t, err)
	_, err = engine.Probe(context.Background(), "config-default-rate", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeRateLimited, "the default must not permit unlimited probing")
}

func TestProbeEngineRateLimitsByOpaqueConnectionConfigID(t *testing.T) {
	clock := &fixedProbeClock{now: time.Date(2026, 9, 4, 4, 5, 6, 0, time.UTC)}
	port := &scriptedProbePort{errors: map[Transport]error{TransportHTTP: nil}}
	engine := NewProbeEngine(port, ProbeOptions{MinimumInterval: time.Minute, Clock: clock})

	_, err := engine.Probe(context.Background(), "config-opaque-a", ConnectionPayload{Endpoint: "https://one.example.test/mcp"}, TransportHTTP)
	require.NoError(t, err)
	_, err = engine.Probe(context.Background(), "config-opaque-b", ConnectionPayload{Endpoint: "https://one.example.test/mcp"}, TransportHTTP)
	require.NoError(t, err, "another opaque config ID must not be blocked by a different configuration")
	_, err = engine.Probe(context.Background(), "config-opaque-a", ConnectionPayload{Endpoint: "https://two.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeRateLimited, "the same config remains limited even if its endpoint changes")
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newHTTPProbePortWithTestClient(t *testing.T, policy *OutboundPolicy, client *http.Client, maxResponseBytes int64) *HTTPProbePort {
	t.Helper()
	port, err := NewHTTPProbePort(policy, HTTPProbeOptions{MaxResponseBytes: maxResponseBytes})
	require.NoError(t, err)
	// 仅同包测试可覆盖私有 seam，以便断言请求内容；生产构造路径不可注入裸 client。
	port.client = client
	return port
}

func TestHTTPProbePortAlwaysUsesControlledTransport(t *testing.T) {
	policy := testPolicy(t, true)
	port, err := NewHTTPProbePort(policy, HTTPProbeOptions{})
	require.NoError(t, err)
	transport, ok := port.client.Transport.(*http.Transport)
	require.True(t, ok, "the probe must use the controlled HTTP transport")
	assert.Nil(t, transport.Proxy, "the probe must not consult environment proxy settings")
	assert.NotNil(t, transport.DialContext)
	require.NotNil(t, transport.TLSClientConfig)
	assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	require.NotNil(t, port.client.CheckRedirect)
}

func TestHTTPProbePortUsesInitializeThenInitializedWithoutToolsAndBoundsResponseBody(t *testing.T) {
	policy := testPolicy(t, true)
	requests := 0
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, http.MethodPost, request.Method)
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		if requests == 1 {
			assert.Contains(t, string(body), `"method":"initialize"`)
			assert.NotContains(t, string(body), "tools/list")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(validInitializeResponseJSON())),
				Request:    request,
			}, nil
		}
		assert.Equal(t, 2, requests)
		assert.Contains(t, string(body), `"method":"notifications/initialized"`)
		assert.NotContains(t, string(body), "tools/list")
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})}, 256)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
		Transport: TransportHTTP,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, requests)

	tooLarge := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", 129))),
			Request:    request,
		}, nil
	})}, 128)
	err = tooLarge.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp/private-token-value"},
		Transport: TransportHTTP,
	})
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.NotContains(t, err.Error(), "private-token-value")
}

func TestHTTPProbePortProtectsProtocolHeadersAndCredentialPrecedence(t *testing.T) {
	policy := testPolicy(t, true)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "application/json", request.Header.Get("Content-Type"))
		assert.Equal(t, "application/json, text/event-stream", request.Header.Get("Accept"))
		assert.Equal(t, "Bearer managed-bearer-token", request.Header.Get("Authorization"))
		assert.Equal(t, "intranet", request.Header.Get("X-Environment"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"test","version":"1"}}}`)),
			Request:    request,
		}, nil
	})}, 512)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload: ConnectionPayload{
			Endpoint:       "https://safe.example.test/mcp",
			Authentication: Authentication{Kind: AuthenticationBearer, Secret: "managed-bearer-token"},
			Headers: []Header{
				{Name: "Content-Type", Value: "text/plain"},
				{Name: "Accept", Value: "text/plain"},
				{Name: "Authorization", Value: "Bearer custom-token"},
				{Name: "X-Environment", Value: "intranet"},
			},
		},
		Transport: TransportHTTP,
	})
	require.NoError(t, err)
}

func TestHTTPProbePortRequiresMatchingInitializeResponse(t *testing.T) {
	policy := testPolicy(t, true)
	tests := []struct {
		name  string
		body  string
		valid bool
	}{
		{name: "missing id", body: `{"jsonrpc":"2.0","result":{"protocolVersion":"2025-03-26"}}`},
		{name: "wrong id", body: `{"jsonrpc":"2.0","id":"another-request","result":{"protocolVersion":"2025-03-26"}}`},
		{name: "null result", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":null}`},
		{name: "string result", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":"unexpected"}`},
		{name: "array result", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":[]}`},
		{name: "empty result object", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{}}`},
		{name: "missing protocol version", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"capabilities":{},"serverInfo":{"name":"test","version":"1"}}}`},
		{name: "wrong protocol version", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"test","version":"1"}}}`},
		{name: "missing capabilities", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","serverInfo":{"name":"test","version":"1"}}}`},
		{name: "non object capabilities", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":[],"serverInfo":{"name":"test","version":"1"}}}`},
		{name: "missing server info", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":{}}}`},
		{name: "blank server name", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":" ","version":"1"}}}`},
		{name: "blank server version", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":{},"serverInfo":{"name":"test","version":" "}}}`},
		{name: "matching initialize result", body: validInitializeResponseJSON(), valid: true},
	}

	for _, transport := range []Transport{TransportHTTP} {
		for _, test := range tests {
			t.Run(string(transport)+"/"+test.name, func(t *testing.T) {
				contentType := "application/json"
				body := test.body
				if transport == TransportSSE {
					contentType = "text/event-stream"
					body = "event: message\ndata: " + body + "\n\n"
				}
				port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": []string{contentType}},
						Body:       io.NopCloser(strings.NewReader(body)),
						Request:    request,
					}, nil
				})}, 512)

				err := port.Initialize(context.Background(), ProbeRequest{
					Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
					Transport: transport,
				})
				if test.valid {
					require.NoError(t, err)
					return
				}
				require.ErrorIs(t, err, ErrProbeFailed)
			})
		}
	}
}

func TestValidSSEInitializeStreamRequiresStrictInitializeResult(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		valid bool
	}{
		{name: "matching result", body: validInitializeResponseJSON(), valid: true},
		{name: "wrong version", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"test","version":"1"}}}`},
		{name: "missing capability object", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","serverInfo":{"name":"test","version":"1"}}}`},
		{name: "missing server info", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26","capabilities":{}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			valid := validSSEInitializeStream(context.Background(), strings.NewReader("event: message\ndata: "+test.body+"\n\n"), 512)
			assert.Equal(t, test.valid, valid)
		})
	}
}

func TestValidSSEInitializeStreamSkipsNotificationsUntilMatchingResponse(t *testing.T) {
	stream := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" + validInitializeResponseSSE()
	assert.True(t, validSSEInitializeStream(context.Background(), strings.NewReader(stream), 1024))
}

func TestHTTPProbePortRejectsRedirectResponseWithoutFollowingIt(t *testing.T) {
	policy := testPolicy(t, true)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"https://other.example.test/secret"}},
			Body:       io.NopCloser(strings.NewReader("redirect")),
			Request:    request,
		}, nil
	})}, 0)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
		Transport: TransportHTTP,
	})
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.NotContains(t, err.Error(), "other.example.test")
}

func TestHTTPProbePortStreamableHTTPAcceptsEventStreamWithoutLegacyFallback(t *testing.T) {
	policy := testPolicy(t, true)
	accepts := make([]string, 0, 2)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		accepts = append(accepts, request.Header.Get("Accept"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(validInitializeResponseSSE())),
			Request:    request,
		}, nil
	})}, 256)
	engine := NewProbeEngine(port, ProbeOptions{Timeout: time.Second})

	result, err := engine.Probe(context.Background(), "config-streamable-sse", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportAuto)
	require.NoError(t, err)
	assert.Equal(t, TransportHTTP, result.DetectedTransport)
	assert.Equal(t, []string{"application/json, text/event-stream", "application/json, text/event-stream"}, accepts)
}

func TestHTTPProbePortFixedHTTPAcceptsEventStreamResponse(t *testing.T) {
	policy := testPolicy(t, true)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(validInitializeResponseSSE())),
			Request:    request,
		}, nil
	})}, 256)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
		Transport: TransportHTTP,
	})
	require.NoError(t, err)
}

func TestHTTPProbePortFixedSSERejectsJSONResponse(t *testing.T) {
	policy := testPolicy(t, true)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","result":{}}`)),
			Request:    request,
		}, nil
	})}, 256)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
		Transport: TransportSSE,
	})
	require.ErrorIs(t, err, ErrProbeFailed)
}

func TestHTTPProbePortStreamableHTTPNotifiesAndCleansSession(t *testing.T) {
	policy := testPolicy(t, true)
	methods := make([]string, 0, 3)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		switch len(methods) {
		case 1:
			body, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Equal(t, "application/json, text/event-stream", request.Header.Get("Accept"))
			assert.Contains(t, string(body), `"method":"initialize"`)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Mcp-Session-Id": []string{"opaque-session-sentinel"},
			}, Body: io.NopCloser(strings.NewReader(validInitializeResponseJSON())), Request: request}, nil
		case 2:
			body, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Equal(t, "opaque-session-sentinel", request.Header.Get("Mcp-Session-Id"))
			assert.Contains(t, string(body), `"method":"notifications/initialized"`)
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		case 3:
			assert.Equal(t, http.MethodDelete, request.Method)
			assert.Equal(t, "opaque-session-sentinel", request.Header.Get("Mcp-Session-Id"))
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		default:
			t.Fatalf("unexpected probe request %s", request.Method)
			return nil, nil
		}
	})}, 512)

	err := port.Initialize(context.Background(), ProbeRequest{Payload: ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, Transport: TransportHTTP})
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodPost, http.MethodPost, http.MethodDelete}, methods)
}

func TestHTTPProbePortCleansSessionWhenInitializedNotificationFails(t *testing.T) {
	policy := testPolicy(t, true)
	methods := make([]string, 0, 3)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		switch len(methods) {
		case 1:
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
				"Content-Type":   []string{"application/json"},
				"Mcp-Session-Id": []string{"opaque-session-failure-sentinel"},
			}, Body: io.NopCloser(strings.NewReader(validInitializeResponseJSON())), Request: request}, nil
		case 2:
			return &http.Response{StatusCode: http.StatusInternalServerError, Body: io.NopCloser(strings.NewReader("upstream-secret-response")), Request: request}, nil
		case 3:
			assert.Equal(t, http.MethodDelete, request.Method)
			assert.Equal(t, "opaque-session-failure-sentinel", request.Header.Get("Mcp-Session-Id"))
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		default:
			t.Fatalf("unexpected probe request %s", request.Method)
			return nil, nil
		}
	})}, 512)

	err := port.Initialize(context.Background(), ProbeRequest{Payload: ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, Transport: TransportHTTP})
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.NotContains(t, err.Error(), "opaque-session-failure-sentinel")
	assert.NotContains(t, err.Error(), "upstream-secret-response")
	assert.Equal(t, []string{http.MethodPost, http.MethodPost, http.MethodDelete}, methods)
}

func TestHTTPProbePortUsesLegacySSEGETEndpointAndMessageFlow(t *testing.T) {
	policy := testPolicy(t, true)
	methods := make([]string, 0, 3)
	paths := make([]string, 0, 3)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		paths = append(paths, request.URL.RequestURI())
		switch len(methods) {
		case 1:
			assert.Equal(t, http.MethodGet, request.Method)
			assert.Equal(t, "text/event-stream", request.Header.Get("Accept"))
			stream := "event: endpoint\ndata: /legacy/messages?sessionId=opaque-legacy-session\n\n" + validInitializeResponseSSE()
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: request}, nil
		case 2:
			body, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Contains(t, string(body), `"method":"initialize"`)
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		case 3:
			body, readErr := io.ReadAll(request.Body)
			require.NoError(t, readErr)
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Contains(t, string(body), `"method":"notifications/initialized"`)
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		default:
			t.Fatalf("unexpected legacy SSE request %s", request.Method)
			return nil, nil
		}
	})}, 1024)

	err := port.Initialize(context.Background(), ProbeRequest{Payload: ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, Transport: TransportSSE})
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodGet, http.MethodPost, http.MethodPost}, methods)
	assert.Equal(t, []string{"/mcp", "/legacy/messages?sessionId=opaque-legacy-session", "/legacy/messages?sessionId=opaque-legacy-session"}, paths)
}

func TestHTTPProbePortLegacySSEWaitsForMatchingDefaultMessageResponse(t *testing.T) {
	policy := testPolicy(t, true)
	methods := make([]string, 0, 3)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		switch len(methods) {
		case 1:
			stream := "event: endpoint\ndata: /legacy/messages?sessionId=opaque-legacy-session\n\n" +
				"event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" +
				"data: " + validInitializeResponseJSON() + "\n\n"
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: request}, nil
		case 2, 3:
			return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		default:
			t.Fatalf("unexpected legacy request %s", request.Method)
			return nil, nil
		}
	})}, 1024)

	err := port.Initialize(context.Background(), ProbeRequest{Payload: ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, Transport: TransportSSE})
	require.NoError(t, err)
	assert.Equal(t, []string{http.MethodGet, http.MethodPost, http.MethodPost}, methods)
}

func TestHTTPProbePortRejectsCrossOriginLegacySSEEndpoint(t *testing.T) {
	policy := testPolicy(t, true)
	methods := make([]string, 0, 2)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		methods = append(methods, request.Method)
		if request.Method == http.MethodGet {
			stream := "event: endpoint\ndata: https://other.example.test/messages?sessionId=opaque\n\n"
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(validInitializeResponseSSE())), Request: request}, nil
	})}, 512)

	err := port.Initialize(context.Background(), ProbeRequest{Payload: ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, Transport: TransportSSE})
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.Equal(t, []string{http.MethodGet}, methods)
}

func TestProbeStreamableHTTPAcceptsOpenSSEInitializeWithoutWaitingForEOF(t *testing.T) {
	policy := testPolicy(t, true)
	sseReader, sseWriter := io.Pipe()
	holdSSEOpen := make(chan struct{})
	go func() {
		_, _ = io.WriteString(sseWriter, validInitializeResponseSSE())
		<-holdSSEOpen
		_ = sseWriter.Close()
	}()
	defer func() {
		close(holdSSEOpen)
		_ = sseWriter.Close()
	}()

	accepts := make([]string, 0, 2)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		accepts = append(accepts, request.Header.Get("Accept"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       sseReader,
			Request:    request,
		}, nil
	})}, 256)
	engine := NewProbeEngine(port, ProbeOptions{Timeout: time.Second})

	type result struct {
		value ProbeResult
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := engine.Probe(context.Background(), "config-open-streamable-sse", ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportAuto)
		done <- result{value: value, err: err}
	}()

	select {
	case outcome := <-done:
		require.NoError(t, outcome.err)
		assert.Equal(t, TransportHTTP, outcome.value.DetectedTransport)
		assert.Equal(t, []string{"application/json, text/event-stream", "application/json, text/event-stream"}, accepts)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("the SSE initialize event must be accepted without waiting for the stream to close")
	}
}

func TestProbeSSEContextCancellationClosesStreamingBody(t *testing.T) {
	policy := testPolicy(t, true)
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       reader,
			Request:    request,
		}, nil
	})}, 256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	readStarted := make(chan struct{})
	go func() {
		_, _ = io.WriteString(writer, "data: ")
		close(readStarted)
	}()
	done := make(chan error, 1)
	go func() {
		done <- port.Initialize(ctx, ProbeRequest{
			Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
			Transport: TransportSSE,
		})
	}()

	select {
	case <-readStarted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("the streaming probe did not begin reading the event")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrProbeFailed)
		assert.NotContains(t, err.Error(), "safe.example.test")
	case <-time.After(200 * time.Millisecond):
		t.Fatal("the SSE probe must fail closed when its context is cancelled")
	}
}

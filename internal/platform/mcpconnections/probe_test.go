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

	result, err := engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportAuto)
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

	_, err := engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
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

	_, err := engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeFailed)
	assert.NotContains(t, err.Error(), "safe.example.test")
	_, err = engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeRateLimited)
}

func TestProbeEngineAppliesSafeDefaultMinimumInterval(t *testing.T) {
	clock := &fixedProbeClock{now: time.Date(2026, 9, 3, 3, 4, 5, 0, time.UTC)}
	port := &scriptedProbePort{errors: map[Transport]error{TransportHTTP: nil}}
	engine := NewProbeEngine(port, ProbeOptions{Clock: clock})

	_, err := engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.NoError(t, err)
	_, err = engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportHTTP)
	require.ErrorIs(t, err, ErrProbeRateLimited, "the default must not permit unlimited probing")
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

func TestHTTPProbePortSendsOnlyInitializeAndBoundsResponseBody(t *testing.T) {
	policy := testPolicy(t, true)
	requests := 0
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, http.MethodPost, request.Method)
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		assert.Contains(t, string(body), `"method":"initialize"`)
		assert.NotContains(t, string(body), "tools/list")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26"}}`)),
			Request:    request,
		}, nil
	})}, 256)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
		Transport: TransportHTTP,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, requests)

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

func TestHTTPProbePortAutoRetriesSSEWhenHTTPReceivesEventStream(t *testing.T) {
	policy := testPolicy(t, true)
	accepts := make([]string, 0, 2)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		accepts = append(accepts, request.Header.Get("Accept"))
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"result\":{}}\n\n")),
			Request:    request,
		}, nil
	})}, 256)
	engine := NewProbeEngine(port, ProbeOptions{Timeout: time.Second})

	result, err := engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportAuto)
	require.NoError(t, err)
	assert.Equal(t, TransportSSE, result.DetectedTransport)
	assert.Equal(t, []string{"application/json", "text/event-stream"}, accepts)
}

func TestHTTPProbePortFixedHTTPRejectsEventStreamResponse(t *testing.T) {
	policy := testPolicy(t, true)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"result\":{}}\n\n")),
			Request:    request,
		}, nil
	})}, 256)

	err := port.Initialize(context.Background(), ProbeRequest{
		Payload:   ConnectionPayload{Endpoint: "https://safe.example.test/mcp"},
		Transport: TransportHTTP,
	})
	require.ErrorIs(t, err, ErrProbeFailed)
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

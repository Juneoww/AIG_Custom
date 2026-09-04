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
		{name: "missing protocol version", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"serverInfo":{"name":"test"}}}`},
		{name: "blank protocol version", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":" "}}`},
		{name: "matching initialize result", body: `{"jsonrpc":"2.0","id":"mcp-probe","result":{"protocolVersion":"2025-03-26"}}`, valid: true},
	}

	for _, transport := range []Transport{TransportHTTP, TransportSSE} {
		for _, test := range tests {
			t.Run(string(transport)+"/"+test.name, func(t *testing.T) {
				contentType := "application/json"
				body := test.body
				if transport == TransportSSE {
					contentType = "text/event-stream"
					body = "data: " + body + "\n\n"
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
			Body:       io.NopCloser(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"id\":\"mcp-probe\",\"result\":{\"protocolVersion\":\"2025-03-26\"}}\n\n")),
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

func TestProbeAutoAcceptsOpenSSEInitializeWithoutWaitingForEOF(t *testing.T) {
	policy := testPolicy(t, true)
	firstReader, firstWriter := io.Pipe()
	sseReader, sseWriter := io.Pipe()
	holdSSEOpen := make(chan struct{})
	go func() {
		_, _ = io.WriteString(sseWriter, "data: {\"jsonrpc\":\"2.0\",\"id\":\"mcp-probe\",\"result\":{\"protocolVersion\":\"2025-03-26\"}}\n\n")
		<-holdSSEOpen
		_ = sseWriter.Close()
	}()
	defer func() {
		_ = firstWriter.Close()
		close(holdSSEOpen)
		_ = sseWriter.Close()
	}()

	accepts := make([]string, 0, 2)
	port := newHTTPProbePortWithTestClient(t, policy, &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		accepts = append(accepts, request.Header.Get("Accept"))
		if len(accepts) == 1 {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       firstReader,
				Request:    request,
			}, nil
		}
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
		value, err := engine.Probe(context.Background(), ConnectionPayload{Endpoint: "https://safe.example.test/mcp"}, TransportAuto)
		done <- result{value: value, err: err}
	}()

	select {
	case outcome := <-done:
		require.NoError(t, outcome.err)
		assert.Equal(t, TransportSSE, outcome.value.DetectedTransport)
		assert.Equal(t, []string{"application/json", "text/event-stream"}, accepts)
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

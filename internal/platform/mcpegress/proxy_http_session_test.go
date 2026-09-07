package mcpegress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyHTTPMapsSessionsToTaskAndDeletesAfterTermination(t *testing.T) {
	const taskID = "http-session-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "http-session-secret", "http-session-capability")
	task := *service.tasks.(*memoryTaskReader).records[taskID]
	task.ID = "other-http-task"
	service.tasks.(*memoryTaskReader).records[task.ID] = &task
	bindings := service.bindings.(*memoryBindingReader)
	binding := *bindings.bindings[taskID]
	binding.TaskID = task.ID
	bindings.bindings[task.ID] = &binding
	first, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	other, err := service.IssueRuntime(context.Background(), task.ID)
	require.NoError(t, err)
	upstreamCalls := 0
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		upstreamCalls++
		response := proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{"result":"ok"}`)))
		if upstreamCalls == 1 {
			assert.Empty(t, request.Header.Get("Mcp-Session-Id"))
			response.Header.Set("Mcp-Session-Id", "server-private-http-session")
		} else {
			assert.Equal(t, "server-private-http-session", request.Header.Get("Mcp-Session-Id"))
		}
		return response, nil
	}))
	initial := proxyHTTPCall(proxy, http.MethodPost, taskID, first.TaskCapability, "")
	require.Equal(t, http.StatusOK, initial.Code)
	opaque := initial.Header().Get("Mcp-Session-Id")
	require.True(t, validProxySessionID(opaque), opaque)
	assert.NotEqual(t, "server-private-http-session", opaque)
	for _, candidate := range []struct{ taskID, capability, id string }{
		{task.ID, other.TaskCapability, opaque},
		{taskID, first.TaskCapability, "server-private-http-session"},
		{taskID, first.TaskCapability, strings.Repeat("x", 43)},
	} {
		result := proxyHTTPCall(proxy, http.MethodPost, candidate.taskID, candidate.capability, candidate.id)
		assert.Equal(t, http.StatusForbidden, result.Code)
	}
	assert.Equal(t, 1, upstreamCalls)
	assert.Equal(t, http.StatusOK, proxyHTTPCall(proxy, http.MethodGet, taskID, first.TaskCapability, opaque).Code)
	assert.Equal(t, http.StatusOK, proxyHTTPCall(proxy, http.MethodDelete, taskID, first.TaskCapability, opaque).Code)
	assert.Equal(t, http.StatusForbidden, proxyHTTPCall(proxy, http.MethodPost, taskID, first.TaskCapability, opaque).Code)
	assert.Equal(t, 3, upstreamCalls)
}

func TestProxyHTTPRejectsMissingAndDuplicateSessionHeaders(t *testing.T) {
	const taskID = "http-session-header-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "http-header-secret", "http-header-capability")
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	var called atomic.Bool
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		called.Store(true)
		return proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{}`))), nil
	}))
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		assert.Equal(t, http.StatusForbidden, proxyHTTPCall(proxy, method, taskID, runtime.TaskCapability, "").Code)
	}
	request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, nil)
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	request.Header["Mcp-Session-Id"] = []string{"one", "two"}
	result := httptest.NewRecorder()
	proxy.ServeHTTP(result, request, taskID)
	assert.Equal(t, http.StatusForbidden, result.Code)
	assert.False(t, called.Load())
}

func TestProxyHTTPSessionsExpireAndRotate(t *testing.T) {
	for _, cause := range []string{"expiry", "rotation"} {
		t.Run(cause, func(t *testing.T) {
			const taskID = "http-session-lifecycle-task"
			service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "http-lifecycle-secret", "http-lifecycle-capability")
			if cause == "expiry" {
				service.capabilityTTL = 200 * time.Millisecond
			}
			runtime, err := service.IssueRuntime(context.Background(), taskID)
			require.NoError(t, err)
			proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				response := proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{}`)))
				response.Header.Set("Mcp-Session-Id", "upstream-session")
				return response, nil
			}))
			result := proxyHTTPCall(proxy, http.MethodPost, taskID, runtime.TaskCapability, "")
			require.Equal(t, http.StatusOK, result.Code)
			if cause == "rotation" {
				_, err = service.IssueRuntime(context.Background(), taskID)
				require.NoError(t, err)
			}
			require.Eventually(t, func() bool {
				proxy.sessionsMu.Lock()
				defer proxy.sessionsMu.Unlock()
				return len(proxy.sessions) == 0
			}, 3*time.Second, 10*time.Millisecond)
			assert.Equal(t, http.StatusForbidden, proxyHTTPCall(proxy, http.MethodPost, taskID, runtime.TaskCapability, result.Header().Get("Mcp-Session-Id")).Code)
		})
	}
}

func TestProxyBodyReadIsCancelledBeforeExpiredCapabilityCanSendUpstream(t *testing.T) {
	const taskID = "body-expiry-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "body-expiry-secret", "body-expiry-capability")
	service.capabilityTTL = 100 * time.Millisecond
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	var called atomic.Bool
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		called.Store(true)
		return proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{}`))), nil
	}))
	reader, writer := io.Pipe()
	defer writer.Close()
	request := httptest.NewRequest(http.MethodPost, internalGatewayPathPrefix+taskID, reader)
	request.Header.Set(CapabilityHeader, runtime.TaskCapability)
	done := make(chan struct{})
	go func() { defer close(done); proxy.ServeHTTP(httptest.NewRecorder(), request, taskID) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = writer.Close()
		<-done
		t.Fatal("request body read outlived capability expiry")
	}
	assert.False(t, called.Load())
}

func proxyHTTPCall(proxy *Proxy, method, taskID, capability, sessionID string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, internalGatewayPathPrefix+taskID, strings.NewReader(`{}`))
	request.Header.Set(CapabilityHeader, capability)
	if sessionID != "" {
		request.Header.Set("Mcp-Session-Id", sessionID)
	}
	result := httptest.NewRecorder()
	proxy.ServeHTTP(result, request, taskID)
	return result
}

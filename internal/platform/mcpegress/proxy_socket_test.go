package mcpegress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyRealSocketInterruptsIncompletePOSTOnCapabilityExpiryOrRotation(t *testing.T) {
	for _, cause := range []string{"expiry", "rotation"} {
		t.Run(cause, func(t *testing.T) {
			const taskID = "socket-body-task"
			service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "socket-body-secret", "socket-body-capability")
			service.capabilityTTL = 5 * time.Second
			if cause == "expiry" {
				service.capabilityTTL = 200 * time.Millisecond
			}
			runtime, err := service.IssueRuntime(context.Background(), taskID)
			require.NoError(t, err)
			var upstreamCalled atomic.Bool
			proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
				upstreamCalled.Store(true)
				return proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{}`))), nil
			}))
			server, done, bodyRead := newSocketGatewayServer(t, proxy)
			connection, err := net.Dial("tcp", server.Listener.Addr().String())
			require.NoError(t, err)
			defer connection.Close()
			_, err = fmt.Fprintf(connection, "POST %s%s HTTP/1.1\r\nHost: gateway.local\r\nX-Internal-Agent-Token: socket-test-agent\r\n%s: %s\r\nContent-Length: 128\r\n\r\n", internalGatewayPathPrefix, taskID, CapabilityHeader, runtime.TaskCapability)
			require.NoError(t, err)
			select {
			case <-bodyRead:
			case <-time.After(time.Second):
				t.Fatal("real HTTP request never began reading body")
			}
			if cause == "rotation" {
				_, err = service.IssueRuntime(context.Background(), taskID)
				require.NoError(t, err)
			}
			select {
			case <-done:
			case <-time.After(1500 * time.Millisecond):
				_ = connection.Close()
				<-done
				t.Errorf("authenticated incomplete POST remained blocked after capability %s until client closed socket", cause)
			}
			assert.False(t, upstreamCalled.Load(), "incomplete/expired request must never reach upstream")
		})
	}
}

func TestProxyRealSocketInterruptsBlockedResponseWriteAtCapabilityExpiry(t *testing.T) {
	const taskID = "socket-write-task"
	service := newProxyServiceFixture(t, taskID, "https://mcp.allowed.example.test/mcp", "socket-write-secret", "socket-write-capability")
	service.capabilityTTL = 300 * time.Millisecond
	runtime, err := service.IssueRuntime(context.Background(), taskID)
	require.NoError(t, err)
	upstreamCalled := make(chan struct{})
	proxy := proxyWithTransport(service, proxyRoundTripper(func(request *http.Request) (*http.Response, error) {
		close(upstreamCalled)
		return proxyResponse(request, http.StatusOK, "application/json", io.NopCloser(strings.NewReader(`{"result":"`+strings.Repeat("x", 3<<20)+`"}`))), nil
	}))
	server, done, _ := newSocketGatewayServer(t, proxy)
	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.(*net.TCPConn).SetReadBuffer(1024))
	_, err = fmt.Fprintf(connection, "POST %s%s HTTP/1.1\r\nHost: gateway.local\r\nX-Internal-Agent-Token: socket-test-agent\r\n%s: %s\r\nContent-Length: 2\r\n\r\n{}", internalGatewayPathPrefix, taskID, CapabilityHeader, runtime.TaskCapability)
	require.NoError(t, err)
	select {
	case <-upstreamCalled:
	case <-time.After(time.Second):
		t.Fatal("upstream was not called")
	}
	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		_ = connection.Close()
		<-done
		t.Fatal("response write remained blocked after capability expiry until client closed socket")
	}
}

func newSocketGatewayServer(t *testing.T, proxy *Proxy) (*httptest.Server, <-chan struct{}, <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	done := make(chan struct{})
	bodyRead := make(chan struct{})
	router := gin.New()
	NewHandler(proxy).Register(router.Group("/api/internal"), func(ctx *gin.Context) {
		defer close(done)
		if ctx.GetHeader("X-Internal-Agent-Token") != "socket-test-agent" {
			ctx.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		ctx.Request.Body = &proxyObservedSocketBody{ReadCloser: ctx.Request.Body, started: bodyRead}
		ctx.Next()
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.ConnState = func(connection net.Conn, state http.ConnState) {
		if state == http.StateNew {
			_ = connection.(*net.TCPConn).SetWriteBuffer(1024)
		}
	}
	server.Start()
	t.Cleanup(server.Close)
	return server, done, bodyRead
}

type proxyObservedSocketBody struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (body *proxyObservedSocketBody) Read(value []byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	return body.ReadCloser.Read(value)
}

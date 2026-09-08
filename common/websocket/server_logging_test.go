package websocket

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMCPInternalLogsNeverExposeArchiveOrRuntimeCredentials(t *testing.T) {
	for _, shouldPanic := range []bool{false, true} {
		var logs bytes.Buffer
		router := newWebServerRouter(&logs, &logs)
		router.GET("/api/internal/mcp-archives/:sessionID/:archiveID", func(c *gin.Context) {
			if shouldPanic {
				panic("panic-secret-sentinel")
			}
			c.Status(403)
		})
		request := httptest.NewRequest("GET", "/api/internal/mcp-archives/session-secret/archive-secret?token=query-secret", nil)
		request.Header.Set("X-Internal-Agent-Token", "agent-secret")
		request.Header.Set("X-AIG-MCP-Capability", "capability-secret")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if shouldPanic {
			require.Equal(t, 500, response.Code)
		} else {
			require.Equal(t, 403, response.Code)
		}
		for _, secret := range []string{"session-secret", "archive-secret", "query-secret", "agent-secret", "capability-secret", "panic-secret-sentinel"} {
			require.NotContains(t, logs.String(), secret)
		}
		require.Contains(t, logs.String(), "/api/internal/mcp-archives/:sessionID/:archiveID")
	}
}

func TestMCPInternalTrailingSlashCannotLeakThroughGinDebugRedirect(t *testing.T) {
	previousMode, previousWriter := gin.Mode(), gin.DefaultWriter
	var logs bytes.Buffer
	gin.SetMode(gin.DebugMode)
	gin.DefaultWriter = &logs
	t.Cleanup(func() { gin.SetMode(previousMode); gin.DefaultWriter = previousWriter })
	router := newWebServerRouter(&logs, &logs)
	router.GET("/api/internal/mcp-archives/:sessionID/:archiveID", func(c *gin.Context) { c.Status(403) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/internal/mcp-archives/private-session/private-capability/?token=private-query", nil))
	require.Equal(t, 404, response.Code)
	for _, secret := range []string{"private-session", "private-capability", "private-query"} {
		require.NotContains(t, logs.String(), secret)
	}
}

func TestModelProbeProductionRouterLogsNeverExposeCredentials(t *testing.T) {
	for _, shouldPanic := range []bool{false, true} {
		for _, saved := range []bool{false, true} {
			var logs bytes.Buffer
			router := newWebServerRouter(&logs, &logs)
			route := "/api/v1/platform/models/test"
			path := route
			if saved {
				route = "/api/v1/platform/models/:modelID/test"
				path = "/api/v1/platform/models/model-secret-canary/test"
			}
			router.POST(route, func(c *gin.Context) {
				if shouldPanic {
					panic("panic-secret-canary")
				}
				c.Status(400)
			})
			request := httptest.NewRequest("POST", path+"?token=query-secret-canary", strings.NewReader(`{"token":"body-secret-canary"}`))
			request.Header.Set("Authorization", "Bearer header-secret-canary")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if shouldPanic {
				require.Equal(t, 500, response.Code)
			} else {
				require.Equal(t, 400, response.Code)
			}
			for _, secret := range []string{"model-secret-canary", "query-secret-canary", "body-secret-canary", "header-secret-canary", "panic-secret-canary"} {
				require.NotContains(t, logs.String(), secret)
			}
			require.Contains(t, logs.String(), route)
		}
	}
}

func TestModelProbeMalformedRouteLogsRemainRedacted(t *testing.T) {
	previousMode, previousWriter := gin.Mode(), gin.DefaultWriter
	var logs bytes.Buffer
	gin.SetMode(gin.DebugMode)
	gin.DefaultWriter = &logs
	t.Cleanup(func() { gin.SetMode(previousMode); gin.DefaultWriter = previousWriter })
	router := newWebServerRouter(&logs, &logs)
	router.POST("/api/v1/platform/models/:modelID/test", func(c *gin.Context) { c.Status(400) })
	for _, path := range []string{"/api/v1/platform/models/model-secret-canary/test/", "/api/v1/platform/models/model-secret-canary/invalid"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", path+"?token=query-secret-canary", nil))
		require.Equal(t, 404, response.Code)
	}
	require.NotContains(t, logs.String(), "model-secret-canary")
	require.NotContains(t, logs.String(), "query-secret-canary")
}

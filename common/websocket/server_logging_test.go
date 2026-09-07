package websocket

import (
	"bytes"
	"net/http/httptest"
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

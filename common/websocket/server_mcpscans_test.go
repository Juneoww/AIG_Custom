package websocket

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpegress"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpscans"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMCPDedicatedRoutesAreMountedWithoutAnonymousAccess(t *testing.T) {
	router := gin.New()
	module := &mcpServerModule{connections: mcpconnections.NewHandler(nil, nil, nil), scans: mcpscans.NewHandler(nil, nil), gateway: mcpegress.NewHandler(mcpegress.NewProxy(mcpegress.ProxyDependencies{}))}
	module.RegisterPlatform(router.Group("/api/v1/platform"))
	module.RegisterInternal(router.Group("/api/internal"), NewAgentManager("test-internal-token").RequireInternalToken())
	for _, entry := range []struct{ method, path string }{{"GET", "/api/v1/platform/mcp-scans"}, {"POST", "/api/v1/platform/mcp-scans"}, {"GET", "/api/v1/platform/mcp-connection-configs"}, {"GET", "/api/v1/platform/mcp-connection-options"}, {"POST", "/api/internal/mcp-egress/task"}, {"GET", "/api/internal/mcp-archives/session/archive"}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(entry.method, entry.path, nil))
		require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, response.Code, entry.path)
	}
}

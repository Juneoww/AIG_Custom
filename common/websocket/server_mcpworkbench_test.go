package websocket

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	platformmcpworkbench "github.com/Juneoww/AIG_Custom/internal/platform/mcpworkbench"
	platformreports "github.com/Juneoww/AIG_Custom/internal/platform/reports"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPlatformMCPWorkbenchRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	reportService := platformreports.NewService(platformreports.NewMemoryRepository(), nil)
	taskService := platformtasks.NewService(platformtasks.NewMemoryRepository(), nil, nil)
	handler := platformmcpworkbench.NewHandler(platformmcpworkbench.NewService(reportService, taskService))
	registerPlatformMCPWorkbenchRoutes(router.Group("/api/v1/platform"), handler)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/mcp-workbench", nil))
	assert.NotEqual(t, http.StatusNotFound, response.Code)
}

func TestRunWebServerComposesMCPWorkbenchInsideProtectedPlatformGroup(t *testing.T) {
	source, err := os.ReadFile("server.go")
	require.NoError(t, err)
	text := string(source)
	assert.Contains(t, text, "platformmcpworkbench.NewService(reportService, platformTaskService)")
	assert.Contains(t, text, "registerPlatformMCPWorkbenchRoutes(platformGroup")
	assert.Less(t, strings.Index(text, "registerPlatformGovernanceRoutes(platformGroup"), strings.Index(text, "registerPlatformMCPWorkbenchRoutes(platformGroup"))
}

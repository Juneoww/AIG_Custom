package websocket

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	platformdashboard "github.com/Juneoww/AIG_Custom/internal/platform/dashboard"
	platformreports "github.com/Juneoww/AIG_Custom/internal/platform/reports"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterPlatformDashboardRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	reportService := platformreports.NewService(platformreports.NewMemoryRepository(), nil)
	taskService := platformtasks.NewService(platformtasks.NewMemoryRepository(), nil, nil)
	handler := platformdashboard.NewHandler(platformdashboard.NewService(reportService, taskService))
	registerPlatformDashboardRoutes(router.Group("/api/v1/platform"), handler)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/dashboard", nil))
	assert.NotEqual(t, http.StatusNotFound, response.Code)
}

func TestRunWebServerComposesDashboardInsideProtectedPlatformGroup(t *testing.T) {
	source, err := os.ReadFile("server.go")
	require.NoError(t, err)
	text := string(source)
	assert.Contains(t, text, "platformdashboard.NewService(reportService, platformTaskService)")
	assert.Contains(t, text, "registerPlatformDashboardRoutes(platformGroup")
	assert.Less(t, strings.Index(text, "registerPlatformGovernanceRoutes(platformGroup"), strings.Index(text, "registerPlatformDashboardRoutes(platformGroup"))
}

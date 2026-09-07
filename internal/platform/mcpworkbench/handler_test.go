package mcpworkbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandlerReusesPlatformAuthenticationPasswordAndSafeMethodCSRFGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	identityService := identity.NewService(identity.NewMemoryRepository())
	policy := identity.CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf", Secure: true, HTTPOnly: true, SameSite: http.SameSiteLaxMode}
	ctx := context.Background()
	tokens := map[string]string{}
	for _, account := range []struct {
		name       string
		role       identity.Role
		mustChange bool
	}{
		{name: "must-change", role: identity.RoleUser, mustChange: true},
		{name: "user", role: identity.RoleUser},
		{name: "auditor", role: identity.RoleAuditor},
		{name: "admin", role: identity.RoleAdmin},
	} {
		_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: account.name, Password: "secret", Role: account.role, MustChangePassword: account.mustChange})
		require.NoError(t, err)
		login, err := identityService.Authenticate(ctx, account.name, "secret")
		require.NoError(t, err)
		tokens[account.name] = login.Token
	}
	handler := NewHandler(NewService(&recordingReportReader{}, &recordingTaskReader{}))
	router := gin.New()
	group := router.Group("/api/v1/platform")
	group.Use(identity.Authenticate(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	handler.Register(group)

	for _, test := range []struct {
		name, token string
		status      int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{name: "must-change", token: tokens["must-change"], status: http.StatusForbidden},
		{name: "user", token: tokens["user"], status: http.StatusOK},
		{name: "auditor", token: tokens["auditor"], status: http.StatusOK},
		{name: "admin", token: tokens["admin"], status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/platform/mcp-workbench", nil)
			if test.token != "" {
				request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: test.token})
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code, response.Body.String())
		})
	}
}

func TestHandlerReturnsStrictSafeDTOAndFixedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	reportReader := &recordingReportReader{projection: reports.MCPWorkbenchProjection{Completed30d: 3, HighRisk: 2, Highlights: []reports.MCPRiskHighlight{{
		ReportID: "report", TaskID: "task", Severity: "high", Category: "other", Summary: "MCP 安全发现。", CompletedAt: now,
	}}}}
	taskReader := &recordingTaskReader{projection: tasks.MCPWorkbenchProjection{Running: 1, Pending: 2, ActiveTasks: []tasks.MCPWorkbenchTask{{
		TaskID: "12345678-90ab-cdef-1234-567890abcdef", SourceKind: "service", Status: tasks.StatusRunning, UpdatedAt: now,
	}}}}
	service := NewService(reportReader, taskReader)
	service.now = func() time.Time { return now }
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity_subject", identity.Subject{UserID: "alice", Role: identity.RoleUser})
	})
	NewHandler(service).Register(router.Group("/api/v1/platform"))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/mcp-workbench", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var wire map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
	assert.ElementsMatch(t, []string{"metrics", "active_tasks", "recent_risks"}, mapKeys(wire))
	assert.ElementsMatch(t, []string{"running", "pending", "high_risk", "completed_30d"}, mapKeys(wire["metrics"].(map[string]any)))
	assert.ElementsMatch(t, []string{"task_id", "label", "source_kind", "phase", "status", "updated_at"}, mapKeys(wire["active_tasks"].([]any)[0].(map[string]any)))
	assert.ElementsMatch(t, []string{"report_id", "task_id", "severity", "category", "summary", "completed_at"}, mapKeys(wire["recent_risks"].([]any)[0].(map[string]any)))
	for _, forbidden := range []string{"content", "endpoint", "raw_result", "model_id", "headers", "authorization", "attachment", "log"} {
		assert.NotContains(t, response.Body.String(), forbidden)
	}

	reportReader.err = errors.New("postgres private-password")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/mcp-workbench", nil))
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.JSONEq(t, `{"error":"mcp workbench request failed"}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "private-password")
}

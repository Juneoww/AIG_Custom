package dashboard

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
	reportReader := &recordingReportReader{}
	handler := NewHandler(NewService(reportReader, &recordingTaskReader{}))
	router := gin.New()
	group := router.Group("/api/v1/platform")
	group.Use(identity.Authenticate(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	handler.Register(group)

	tests := []struct {
		name, token string
		status      int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{name: "must-change", token: tokens["must-change"], status: http.StatusForbidden},
		{name: "user", token: tokens["user"], status: http.StatusOK},
		{name: "auditor", token: tokens["auditor"], status: http.StatusOK},
		{name: "admin", token: tokens["admin"], status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/platform/dashboard", nil)
			if test.token != "" {
				request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: test.token})
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code, response.Body.String())
		})
	}
}

func TestHandlerReturnsExplicitSafeDTOAndFixedDatabaseError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	reportReader := &recordingReportReader{projection: reports.DashboardProjection{
		SnapshotCount: 1, ScoreSum: 50, MappingVersions: []string{"risk-v2"},
		Risk:      reports.RiskSummary{High: 1},
		Trend:     []reports.DashboardTrendPoint{{Date: now, Completed: 1, ScoreSum: 50, High: 1}},
		Attention: []reports.DashboardAttention{{ReportID: "report", TaskID: "task", TaskType: "mcp_scan", CompletedAt: now, Score: 50, High: 1, ProductName: "AIG"}},
	}}
	service := NewService(reportReader, &recordingTaskReader{})
	service.now = func() time.Time { return now }
	handler := NewHandler(service)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity_subject", identity.Subject{UserID: "alice", Role: identity.RoleUser})
	})
	handler.Register(router.Group("/api/v1/platform"))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/dashboard", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var wire map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
	assert.ElementsMatch(t, []string{"has_data", "security_score", "mapping_versions", "risk", "trend", "recent_tasks", "attention"}, mapKeys(wire))
	attention := wire["attention"].([]any)[0].(map[string]any)
	assert.ElementsMatch(t, []string{"report_id", "task_id", "task_type", "completed_at", "score", "risk", "product_name"}, mapKeys(attention))
	for _, forbidden := range []string{"raw_result", "render_data", "logo", "owner_user_id", "engine_session_id", "dispatch_error", "attachment_refs"} {
		assert.NotContains(t, response.Body.String(), forbidden)
	}

	reportReader.err = errors.New("postgres password=database-secret")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/platform/dashboard", nil))
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.JSONEq(t, `{"error":"dashboard request failed"}`, response.Body.String())
	assert.NotContains(t, response.Body.String(), "database-secret")
}

func mapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

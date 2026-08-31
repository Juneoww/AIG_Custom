package reports

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectedReportsHandlerUsesAuthenticatedRBACAndCSRF(t *testing.T) {
	router, tokens, _ := newReportsHandlerFixture(t)

	anonymous := httptest.NewRequest(http.MethodGet, "/api/v1/platform/reports", nil)
	anonymous.Header.Set("username", "admin")
	anonymous.Header.Set("role", "admin")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, anonymous)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	aliceList := performReportRequest(router, tokens["alice"], http.MethodGet, "/api/v1/platform/reports", nil, false)
	require.Equal(t, http.StatusOK, aliceList.Code, aliceList.Body.String())
	var listed struct {
		Items    []ReportSummary `json:"items"`
		Total    int64           `json:"total"`
		Page     int             `json:"page"`
		PageSize int             `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(aliceList.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 1)
	assert.Equal(t, int64(1), listed.Total)
	assert.Equal(t, 1, listed.Page)
	assert.Equal(t, 20, listed.PageSize)
	assert.Equal(t, "alice-report", listed.Items[0].ID)
	assert.Equal(t, http.StatusNotFound, performReportRequest(router, tokens["alice"], http.MethodGet, "/api/v1/platform/reports/bob-report", nil, false).Code)
	for _, role := range []string{"auditor", "admin"} {
		response = performReportRequest(router, tokens[role], http.MethodGet, "/api/v1/platform/reports", nil, false)
		require.Equal(t, http.StatusOK, response.Code, role)
		assert.Equal(t, http.StatusOK, performReportRequest(router, tokens[role], http.MethodGet, "/api/v1/platform/reports/trends?days=30", nil, false).Code)
	}

	assert.Equal(t, http.StatusForbidden, performReportRequest(router, tokens["alice"], http.MethodPost, "/api/v1/platform/reports/alice-report/exports/pdf", nil, false).Code)
	response = performReportRequest(router, tokens["alice"], http.MethodPost, "/api/v1/platform/reports/alice-report/exports/pdf", nil, true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Equal(t, "application/pdf", response.Header().Get("Content-Type"))
	assert.Equal(t, "%PDF-retry", response.Body.String())
	assert.Equal(t, http.StatusNotFound, performReportRequest(router, tokens["alice"], http.MethodPost, "/api/v1/platform/reports/bob-report/exports/pdf", nil, true).Code)
	assert.Equal(t, http.StatusOK, performReportRequest(router, tokens["auditor"], http.MethodPost, "/api/v1/platform/reports/bob-report/exports/pdf", nil, true).Code)

	assert.Equal(t, http.StatusForbidden, performReportRequest(router, tokens["auditor"], http.MethodPost, "/api/v1/platform/admin/reports/backfill", []byte(`{"task_id":"backfill-task"}`), true).Code)
	assert.Equal(t, http.StatusForbidden, performReportRequest(router, tokens["alice"], http.MethodPost, "/api/v1/platform/admin/reports/backfill", []byte(`{"task_id":"backfill-task"}`), true).Code)
	response = performReportRequest(router, tokens["admin"], http.MethodPost, "/api/v1/platform/admin/reports/backfill", []byte(`{"task_id":"backfill-task"}`), true)
	assert.Contains(t, []int{http.StatusOK, http.StatusCreated}, response.Code)
}

func TestReportsWireResponsesNeverExposeStoredRawRenderOrBrandSecrets(t *testing.T) {
	router, tokens, _ := newReportsHandlerFixture(t)
	for _, path := range []string{"/api/v1/platform/reports", "/api/v1/platform/reports/alice-report"} {
		response := performReportRequest(router, tokens["alice"], http.MethodGet, path, nil, false)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		body := response.Body.String()
		for _, sensitive := range []string{"raw_result", "render_data", `"brand":`, "api-token-sentinel", "C:/private/report-path", "logo-sentinel", "updated_by"} {
			assert.NotContains(t, body, sensitive, path)
		}
	}
}

func TestReportsListEnvelopePaginationCapsAndFiltersOwnerBeforePaging(t *testing.T) {
	router, tokens, _ := newReportsHandlerFixture(t)
	response := performReportRequest(router, tokens["alice"], http.MethodGet, "/api/v1/platform/reports?page=1&page_size=1000", nil, false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var listed struct {
		Items    []ReportSummary `json:"items"`
		Total    int64           `json:"total"`
		Page     int             `json:"page"`
		PageSize int             `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 1)
	assert.Equal(t, int64(1), listed.Total)
	assert.Equal(t, 1, listed.Page)
	assert.Equal(t, 100, listed.PageSize)
	assert.Equal(t, "alice-report", listed.Items[0].ID)
	assert.Equal(t, "企业安全平台", listed.Items[0].BrandProductName)
	for _, query := range []string{"?page=0", "?page=bad", "?page=1001", "?page=9223372036854775807", "?page_size=0", "?page_size=bad"} {
		response = performReportRequest(router, tokens["alice"], http.MethodGet, "/api/v1/platform/reports"+query, nil, false)
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
		assert.JSONEq(t, `{"error":"invalid report"}`, response.Body.String(), query)
	}
}

func TestReportDetailNeverExposesNaturalLanguageOrStructuredCredentials(t *testing.T) {
	router, tokens, repository := newReportsHandlerFixture(t)
	snapshot, sentinels := credentialLeakSnapshot(t)
	require.NoError(t, repository.Create(context.Background(), snapshot))

	response := performReportRequest(router, tokens["alice"], http.MethodGet, "/api/v1/platform/reports/credential-report", nil, false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, sentinel := range sentinels {
		assert.NotContains(t, response.Body.String(), sentinel)
	}
}

func TestReportDetailDropsUntrustedInfrastructurePortScanCombination(t *testing.T) {
	now := time.Now().UTC()
	snapshot, err := BuildSnapshotAt("invalid-port-task", "user-alice", "ai_infra_scan", event(`{"score":100,"results":[]}`), brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF"}, now, now)
	require.NoError(t, err)
	snapshot.ID = "invalid-port-report"
	var render map[string]any
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	render["port_scan_mode"] = "agent-supplied"
	render["port_spec"] = "sentinel-port-spec"
	snapshot.RenderData, err = json.Marshal(render)
	require.NoError(t, err)

	detail, err := detailOf(snapshot)
	require.NoError(t, err)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "agent-supplied")
	assert.NotContains(t, string(encoded), "sentinel-port-spec")
}

func TestReportDetailDropsPortScanFromNonInfrastructureSnapshot(t *testing.T) {
	now := time.Now().UTC()
	snapshot, err := BuildSnapshotAt("non-infra-port-task", "user-alice", "mcp_scan", event(`{"score":100,"results":[]}`), brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF"}, now, now)
	require.NoError(t, err)
	snapshot.ID = "non-infra-port-report"
	var render map[string]any
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	render["task_type"] = "ai_infra_scan"
	render["port_scan_mode"] = "fixed_ai"
	render["port_spec"] = "11434,1337,7000-9000,18789"
	snapshot.RenderData, err = json.Marshal(render)
	require.NoError(t, err)

	detail, err := detailOf(snapshot)
	require.NoError(t, err)
	encoded, err := json.Marshal(detail)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "fixed_ai")
	assert.NotContains(t, string(encoded), "11434,1337,7000-9000,18789")
}

func newReportsHandlerFixture(t *testing.T) (http.Handler, map[string]string, Repository) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	identityService := identity.NewService(identity.NewMemoryRepository())
	tokens := createHandlerUsers(t, identityService)
	repository := NewMemoryRepository()
	putHandlerSnapshot(t, repository, "alice-report", "alice-task", "user-alice")
	putHandlerSnapshot(t, repository, "bob-report", "bob-task", "user-bob")
	source := &backfillSource{task: CompletedTask{TaskID: "backfill-task", OwnerUserID: "user-alice", TaskType: "mcp_scan", RawResult: event(`{"score":100,"results":[]}`), CompletedAt: time.Now().UTC()}}
	service := NewGovernedService(repository, brand.NewService(brand.NewMemoryRepository()), audit.NewService(audit.NewMemoryRepository()), &retryRenderer{}, source)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf"}
	router := gin.New()
	group := router.Group("/api/v1/platform", identity.Authenticate(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	NewHandler(service).Register(group)
	return router, tokens, repository
}

func putHandlerSnapshot(t *testing.T, repository Repository, id, taskID, owner string) {
	t.Helper()
	now := time.Now().UTC()
	snapshot, err := BuildSnapshotAt(taskID, owner, "mcp_scan", event(`{"score":100,"results":[]}`), brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF", Logo: []byte("logo-sentinel"), LogoMIME: "image/png", UpdatedBy: "updated_by"}, now, now)
	require.NoError(t, err)
	snapshot.ID = id
	snapshot.RawResult = json.RawMessage(`{"token":"api-token-sentinel","path":"C:/private/report-path"}`)
	require.NoError(t, repository.Create(context.Background(), snapshot))
}

func createHandlerUsers(t *testing.T, service *identity.Service) map[string]string {
	t.Helper()
	tokens := map[string]string{}
	for _, user := range []struct {
		id, name string
		role     identity.Role
	}{{"user-alice", "alice", identity.RoleUser}, {"user-bob", "bob", identity.RoleUser}, {"user-auditor", "auditor", identity.RoleAuditor}, {"user-admin", "admin", identity.RoleAdmin}} {
		_, err := service.CreateUser(context.Background(), identity.CreateUserInput{ID: user.id, Username: user.name, Password: "secret", Role: user.role})
		require.NoError(t, err)
		login, err := service.Authenticate(context.Background(), user.name, "secret")
		require.NoError(t, err)
		tokens[user.name] = login.Token
	}
	return tokens
}

func performReportRequest(router http.Handler, token, method, path string, body []byte, csrf bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: token})
	if csrf {
		request.AddCookie(&http.Cookie{Name: "aig_csrf", Value: "csrf-token"})
		request.Header.Set("X-CSRF-Token", "csrf-token")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

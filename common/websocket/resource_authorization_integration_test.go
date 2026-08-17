package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	platformadmin "github.com/Juneoww/AIG_Custom/internal/platform/admin"
	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskAndModelHandlersEnforceSubjectRoleMatrix(t *testing.T) {
	tm, cleanup := newTestTaskManager(t)
	defer cleanup()

	for _, username := range []string{"alice", "bob"} {
		require.NoError(t, tm.taskStore.CreateUser(&database.User{
			UserID: username + "-id", Username: username, Email: username + "@example.test", IsActive: true,
		}))
		require.NoError(t, tm.taskStore.CreateSession(&database.Session{
			ID: username + "-task", Username: username, Title: username + " task", TaskType: "scan", Content: "{}", Status: TaskStatusTodo,
		}))
		require.NoError(t, tm.modelStore.CreateModel(&database.Model{
			ModelID: username + "-model", Username: username, ModelName: "model", Token: "secret", BaseURL: "https://example.test/v1",
		}))
	}

	mm := NewModelManager(tm.modelStore)
	identityService := identity.NewService(identity.NewMemoryRepository())
	tokens := make(map[string]string)
	for _, account := range []struct {
		username string
		role     identity.Role
	}{
		{username: "admin", role: identity.RoleAdmin},
		{username: "auditor", role: identity.RoleAuditor},
		{username: "alice", role: identity.RoleUser},
	} {
		_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{
			Username: account.username,
			Password: "test-password",
			Role:     account.role,
		})
		require.NoError(t, err)
		login, err := identityService.Authenticate(context.Background(), account.username, "test-password")
		require.NoError(t, err)
		tokens[account.username] = login.Token
	}
	policy := identity.CookiePolicy{SessionCookieName: "aig_session"}
	request := func(t *testing.T, token, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := gin.New()
		r.Use(identity.Authenticate(identityService, policy))
		r.GET("/tasks/:sessionId", func(c *gin.Context) { HandleGetTaskDetail(c, tm) })
		r.PUT("/tasks/:sessionId", func(c *gin.Context) { HandleUpdateTask(c, tm) })
		r.GET("/models/:modelId", func(c *gin.Context) { HandleGetModelDetail(c, mm) })
		r.PUT("/models/:modelId", func(c *gin.Context) { HandleUpdateModel(c, mm) })
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("username", "bob")
		req.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: token})
		response := httptest.NewRecorder()
		r.ServeHTTP(response, req)
		return response
	}
	assertSuccess := func(t *testing.T, response *httptest.ResponseRecorder) {
		t.Helper()
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var payload struct {
			Status int `json:"status"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		require.Equal(t, 0, payload.Status, response.Body.String())
	}

	t.Run("admin governs resources owned by another user", func(t *testing.T) {
		assertSuccess(t, request(t, tokens["admin"], http.MethodGet, "/tasks/bob-task", ""))
		assertSuccess(t, request(t, tokens["admin"], http.MethodPut, "/tasks/bob-task", `{"title":"admin managed"}`))
		assertSuccess(t, request(t, tokens["admin"], http.MethodGet, "/models/bob-model", ""))
		assertSuccess(t, request(t, tokens["admin"], http.MethodPut, "/models/bob-model", `{"model":{"note":"admin managed"}}`))
	})

	t.Run("auditor reads globally but cannot write", func(t *testing.T) {
		assertSuccess(t, request(t, tokens["auditor"], http.MethodGet, "/tasks/bob-task", ""))
		require.Equal(t, http.StatusForbidden, request(t, tokens["auditor"], http.MethodPut, "/tasks/bob-task", `{"title":"forbidden"}`).Code)
		assertSuccess(t, request(t, tokens["auditor"], http.MethodGet, "/models/bob-model", ""))
		require.Equal(t, http.StatusForbidden, request(t, tokens["auditor"], http.MethodPut, "/models/bob-model", `{"model":{"note":"forbidden"}}`).Code)
	})

	t.Run("user accesses only owned resources and headers cannot widen access", func(t *testing.T) {
		assertSuccess(t, request(t, tokens["alice"], http.MethodGet, "/tasks/alice-task", ""))
		assertSuccess(t, request(t, tokens["alice"], http.MethodPut, "/tasks/alice-task", `{"title":"owner managed"}`))
		assertSuccess(t, request(t, tokens["alice"], http.MethodGet, "/models/alice-model", ""))
		assertSuccess(t, request(t, tokens["alice"], http.MethodPut, "/models/alice-model", `{"model":{"note":"owner managed"}}`))
		require.Equal(t, http.StatusForbidden, request(t, tokens["alice"], http.MethodGet, "/tasks/bob-task", "").Code)
		require.Equal(t, http.StatusForbidden, request(t, tokens["alice"], http.MethodPut, "/tasks/bob-task", `{"title":"forbidden"}`).Code)
		require.Equal(t, http.StatusForbidden, request(t, tokens["alice"], http.MethodGet, "/models/bob-model", "").Code)
		require.Equal(t, http.StatusForbidden, request(t, tokens["alice"], http.MethodPut, "/models/bob-model", `{"model":{"note":"forbidden"}}`).Code)
	})
}

func TestAttachmentRouteOwnerAuditorAndAdminAuditAuthorization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	tokens := make(map[string]string)
	subjects := make(map[string]identity.Subject)
	for _, account := range []struct {
		id, username string
		role         identity.Role
	}{
		{id: "user-owner", username: "owner", role: identity.RoleUser},
		{id: "user-other", username: "other", role: identity.RoleUser},
		{id: "user-auditor", username: "auditor", role: identity.RoleAuditor},
		{id: "user-admin", username: "admin", role: identity.RoleAdmin},
	} {
		_, err := identityService.CreateUser(ctx, identity.CreateUserInput{
			ID: account.id, Username: account.username, Password: "test-password", Role: account.role,
		})
		require.NoError(t, err)
		login, err := identityService.Authenticate(ctx, account.username, "test-password")
		require.NoError(t, err)
		tokens[account.username] = login.Token
		subjects[account.username] = login.Subject
	}

	auditRepository := platformaudit.NewMemoryRepository()
	auditService := platformaudit.NewService(auditRepository)
	repository := platformtasks.NewMemoryRepository()
	attachmentService, err := platformtasks.NewAttachmentService(repository, platformtasks.AttachmentConfig{
		UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8,
	}, auditService)
	require.NoError(t, err)
	attachment, err := attachmentService.Upload(ctx, subjects["owner"], "private.txt", strings.NewReader("private"))
	require.NoError(t, err)
	taskService := platformtasks.NewService(repository, nil, auditService)
	taskService.SetAttachmentService(attachmentService)
	taskHandler := platformtasks.NewHandler(taskService, attachmentService)
	keyring, err := platformmodels.NewKeyring("attachment-route-authorization", bytes.Repeat([]byte{7}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(platformmodels.NewMemoryRepository(), keyring, auditService)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session"}
	router := gin.New()
	registerPlatformGovernanceRoutes(
		router.Group("/api/v1/platform"), identityService, policy,
		platformadmin.NewHandler(identityService, auditService), modelService, taskHandler,
	)

	download := func(username, attachmentID string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/platform/tasks/attachments/"+attachmentID+"/download", nil)
		request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: tokens[username]})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	ownerResponse := download("owner", attachment.ID)
	require.Equal(t, http.StatusOK, ownerResponse.Code)
	assert.Equal(t, "private", ownerResponse.Body.String())
	assert.Equal(t, http.StatusNotFound, download("other", attachment.ID).Code)
	assert.Equal(t, http.StatusNotFound, download("other", "missing-attachment").Code)
	assert.Equal(t, http.StatusForbidden, download("auditor", attachment.ID).Code)
	assert.Equal(t, http.StatusForbidden, download("auditor", "missing-attachment").Code)
	adminResponse := download("admin", attachment.ID)
	require.Equal(t, http.StatusOK, adminResponse.Code)
	assert.Equal(t, "private", adminResponse.Body.String())
	assert.Equal(t, http.StatusNotFound, download("admin", "missing-attachment").Code)

	events, err := auditRepository.List(ctx, platformaudit.Filter{
		Action: platformaudit.ActionAttachmentDownloaded, ResourceID: attachment.ID,
	})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, platformaudit.OutcomePending, events[0].Outcome)
	assert.Equal(t, platformaudit.OutcomeSuccess, events[1].Outcome)
}

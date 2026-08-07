package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
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

package tasks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestMCPGenericBrowserRoutesRequireSpecializedEndpoint(t *testing.T) {
	repository := NewMemoryRepository()
	service := NewService(repository, &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("identity_subject", identity.Subject{UserID: "alice", Role: identity.RoleUser})
	})
	NewHandler(service).Register(router.Group("/tasks"))
	for _, kind := range []string{"mcp_scan", "Mcp-Scan"} {
		id := uuid.NewString()
		_, _, err := repository.CreateOrGet(context.Background(), &Task{ID: id, OwnerUserID: "alice", IdempotencyKey: id, TaskType: kind, Status: StatusPending})
		require.NoError(t, err)
		for _, entry := range []struct{ method, path, body string }{
			{"POST", "/tasks", `{"task_type":"` + kind + `","content":"secret"}`},
			{"GET", "/tasks?task_type=" + kind, ""}, {"GET", "/tasks/" + id, ""}, {"POST", "/tasks/" + id + "/cancel", `{}`},
		} {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(entry.method, entry.path, strings.NewReader(entry.body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "MCP_SPECIALIZED_ENDPOINT_REQUIRED")
			require.NotContains(t, response.Body.String(), "secret")
		}
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/tasks", nil))
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), `"total":0`)
}

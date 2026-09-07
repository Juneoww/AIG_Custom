package mcpscans

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 业务事务已提交后，调度错误不能丢掉受理 ID，否则页面重试可能重复创建任务。
func TestMCPCreateReturnsAcceptedAndStableReplayAfterDispatchFailure(t *testing.T) {
	for _, failure := range []error{errors.New("private dispatch diagnostic"), tasks.ErrSubmitAcknowledgementUnknown, context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			fixture := newUnitOfWorkFixture(t)
			fixture.dispatcher.err = failure
			owner := identity.Subject{UserID: "accepted-owner", Username: "accepted-owner", Role: identity.RoleUser}
			router := gin.New()
			router.Use(func(c *gin.Context) { c.Set("identity_subject", owner) })
			NewHandler(NewService(fixture.workflow, nil, nil, nil, nil), nil).Register(router.Group("/api/v1/platform"))
			var first CreateResult
			for attempt, status := range []int{http.StatusAccepted, http.StatusOK} {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/platform/mcp-scans", strings.NewReader(`{"source_kind":"repository","repository_url":"https://git.allowed.example.test/team/repo.git"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", "accepted-despite-dispatch-failure")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, req)
				require.Equal(t, status, response.Code, response.Body.String())
				require.NotContains(t, response.Body.String(), "private dispatch diagnostic")
				var result CreateResult
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				require.NotEmpty(t, result.TaskID)
				if attempt == 0 {
					first = result
				} else {
					require.Equal(t, first.TaskID, result.TaskID)
					require.Equal(t, "true", response.Header().Get("Idempotent-Replay"))
				}
			}
			require.Equal(t, 1, fixture.tasks.calls)
			require.Equal(t, 1, fixture.bindings.calls)
			require.Equal(t, 1, fixture.dispatcher.calls)
		})
	}
}

func TestMCPHandlerOnlyProjectsMCPTargetsAndRejectsRawFields(t *testing.T) {
	repository := tasks.NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	owner := identity.Subject{UserID: "alice", Username: "alice", Role: identity.RoleUser}
	taskService := tasks.NewService(repository, nil, audits)
	service := NewService(nil, repository, taskService, idempotency.NewService(idempotency.NewMemoryRepository()), audits)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity_subject", owner) })
	NewHandler(service, nil).Register(router.Group("/api/v1/platform"))
	ids := map[string]string{}
	for _, kind := range []string{"mcp_scan", "Mcp-Scan", "ai_infra_scan"} {
		id := uuid.NewString()
		ids[kind] = id
		_, _, err := repository.CreateOrGet(context.Background(), &tasks.Task{ID: id, OwnerUserID: owner.UserID, OwnerUsername: owner.Username, IdempotencyKey: id, TaskType: kind, Status: tasks.StatusPending, Content: "https://private.example.test/secret", Params: json.RawMessage(`{"source_kind":"service","token":"private-credential"}`), CreatedAt: time.Now(), UpdatedAt: time.Now()})
		require.NoError(t, err)
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/v1/platform"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(response, req)
		return response
	}
	list := request(http.MethodGet, "/mcp-scans", "")
	require.Equal(t, 200, list.Code, list.Body.String())
	require.NotContains(t, list.Body.String(), ids["ai_infra_scan"])
	for _, kind := range []string{"mcp_scan", "Mcp-Scan"} {
		detail := request(http.MethodGet, "/mcp-scans/"+ids[kind], "")
		require.Equal(t, 200, detail.Code, detail.Body.String())
		for _, secret := range []string{"private.example.test", "private-credential", "task_type", "params", "content"} {
			require.NotContains(t, detail.Body.String(), secret)
		}
	}
	require.Equal(t, 404, request(http.MethodGet, "/mcp-scans/"+ids["ai_infra_scan"], "").Code)
	for _, body := range []string{`{"source_kind":"service","server_url":"https://private.example.test"}`, `{"source_kind":"repository","source_kind":"service"}`, `{"source_kind":"repository","country_iso_code":"en"}`} {
		invalid := request(http.MethodPost, "/mcp-scans", body)
		require.Equal(t, 400, invalid.Code, invalid.Body.String())
		require.NotContains(t, invalid.Body.String(), "private.example.test")
	}
}

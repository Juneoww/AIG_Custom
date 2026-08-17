package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/knowledge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingAdminAuditRepository struct {
	delegate *audit.MemoryRepository
	failOn   int
	calls    int
}

type failFirstReadyAdminAuditRepository struct {
	delegate   *audit.MemoryRepository
	readyCalls int
}

func (repository *failFirstReadyAdminAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	return repository.delegate.Append(ctx, event)
}

func (repository *failFirstReadyAdminAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *failFirstReadyAdminAuditRepository) ListPage(ctx context.Context, filter audit.PageFilter) ([]audit.Event, int64, error) {
	return repository.delegate.ListPage(ctx, filter)
}

func (repository *failFirstReadyAdminAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	if completion.State == audit.CompletionStateReady {
		repository.readyCalls++
		if repository.readyCalls == 1 {
			return errors.New("injected ready finalize failure")
		}
	}
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *failFirstReadyAdminAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *failFirstReadyAdminAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *failFirstReadyAdminAuditRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}

func (repository *failFirstReadyAdminAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *failFirstReadyAdminAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func (repository *failingAdminAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	repository.calls++
	if repository.calls == repository.failOn {
		return errors.New("injected audit append failure")
	}
	return repository.delegate.Append(ctx, event)
}

func (repository *failingAdminAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *failingAdminAuditRepository) ListPage(ctx context.Context, filter audit.PageFilter) ([]audit.Event, int64, error) {
	return repository.delegate.ListPage(ctx, filter)
}

func (repository *failingAdminAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *failingAdminAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *failingAdminAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *failingAdminAuditRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}

func (repository *failingAdminAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *failingAdminAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func TestAdminUserLifecycleAlwaysWritesAuditEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)

	auditService := audit.NewService(audit.NewMemoryRepository())
	router := gin.New()
	group := router.Group("/admin", identity.Authenticate(identityService, identity.CookiePolicy{}))
	NewHandler(identityService, auditService).Register(group)

	created := performJSON(t, router, login.Token, http.MethodPost, "/admin/users", map[string]any{
		"username": "alice", "password": "temporary-password", "role": "user",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var user UserResponse
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &user))
	assert.NotEmpty(t, user.ID)
	assert.NotContains(t, created.Body.String(), "temporary-password")
	assert.NotContains(t, created.Body.String(), "password_hash")

	role := performJSON(t, router, login.Token, http.MethodPut, "/admin/users/"+user.ID+"/role", map[string]any{"role": "auditor"})
	assert.Equal(t, http.StatusNoContent, role.Code)
	disable := performJSON(t, router, login.Token, http.MethodPut, "/admin/users/"+user.ID+"/active", map[string]any{"active": false})
	assert.Equal(t, http.StatusNoContent, disable.Code)
	reset := performJSON(t, router, login.Token, http.MethodPost, "/admin/users/"+user.ID+"/password-reset", nil)
	assert.Equal(t, http.StatusNoContent, reset.Code)
	assert.Empty(t, reset.Body.String(), "reset token must never be returned by HTTP")

	events, err := auditService.Query(ctx, identity.Subject{Role: identity.RoleAdmin}, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 8)
	assert.Equal(t, []audit.Action{
		audit.ActionAccountCreated, audit.ActionAccountCreated,
		audit.ActionRoleAssigned, audit.ActionRoleAssigned,
		audit.ActionAccountDisabled, audit.ActionAccountDisabled,
		audit.ActionPasswordResetRequested, audit.ActionPasswordResetRequested,
	}, []audit.Action{events[0].Action, events[1].Action, events[2].Action, events[3].Action, events[4].Action, events[5].Action, events[6].Action, events[7].Action})
	for index := 0; index < len(events); index += 2 {
		assert.Equal(t, audit.OutcomePending, events[index].Outcome)
		assert.Equal(t, audit.OutcomeSuccess, events[index+1].Outcome)
		assert.Equal(t, events[index].RequestID, events[index+1].RequestID)
	}
	for _, event := range events {
		assert.Equal(t, "admin", event.ActorUsername)
		assert.Equal(t, user.ID, event.ResourceID)
	}
}

func TestAuditorCannotMutateUsersEvenWhenHandlerIsMountedWithoutRoleMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "auditor", Password: "secret", Role: identity.RoleAuditor})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "auditor", "secret")
	require.NoError(t, err)
	auditService := audit.NewService(audit.NewMemoryRepository())

	router := gin.New()
	group := router.Group("/admin", identity.Authenticate(identityService, identity.CookiePolicy{}))
	NewHandler(identityService, auditService).Register(group)
	response := performJSON(t, router, login.Token, http.MethodPost, "/admin/users", map[string]any{
		"username": "forbidden", "password": "temporary-password", "role": "user",
	})
	assert.Equal(t, http.StatusForbidden, response.Code)
	reconcile := performJSON(t, router, login.Token, http.MethodPost, "/admin/audit-events/reconcile", nil)
	assert.Equal(t, http.StatusForbidden, reconcile.Code)
	events, queryErr := auditService.Query(ctx, identity.Subject{Role: identity.RoleAdmin}, audit.Filter{})
	require.NoError(t, queryErr)
	assert.Empty(t, events)
}

func TestAdminUsersListEnvelopePaginationIsSafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	adminUser, err := identityService.CreateUser(ctx, identity.CreateUserInput{ID: "admin-01", Username: "admin", Password: "admin-secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	for index := 0; index < 24; index++ {
		_, err = identityService.CreateUser(ctx, identity.CreateUserInput{
			ID:       fmt.Sprintf("user-%02d", index),
			Username: fmt.Sprintf("member-%02d", index),
			Password: fmt.Sprintf("private-password-%02d", index),
			Role:     identity.RoleUser,
		})
		require.NoError(t, err)
	}
	login, err := identityService.Authenticate(ctx, adminUser.Username, "admin-secret")
	require.NoError(t, err)
	router := gin.New()
	NewHandler(identityService, audit.NewService(audit.NewMemoryRepository())).Register(
		router.Group("/admin", identity.Authenticate(identityService, identity.CookiePolicy{})),
	)

	response := performJSON(t, router, login.Token, http.MethodGet, "/admin/users?page=2&page_size=10", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Items    []UserResponse `json:"items"`
		Total    int64          `json:"total"`
		Page     int            `json:"page"`
		PageSize int            `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	assert.Equal(t, int64(25), envelope.Total)
	assert.Equal(t, 2, envelope.Page)
	assert.Equal(t, 10, envelope.PageSize)
	require.Len(t, envelope.Items, 10)
	assert.Equal(t, "member-09", envelope.Items[0].Username)
	for _, forbidden := range []string{"password_hash", "token_hash", "reset", "session", "private-password", "admin-secret"} {
		assert.NotContains(t, response.Body.String(), forbidden)
	}

	for _, query := range []string{"?page=0", "?page=bad", "?page=1001", "?page=9223372036854775807", "?page_size=0", "?page_size=bad"} {
		response = performJSON(t, router, login.Token, http.MethodGet, "/admin/users"+query, nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
		assert.JSONEq(t, `{"error":"invalid list request"}`, response.Body.String(), query)
	}
}

func TestAdminAuditListEnvelopePaginationFiltersAndSanitizesMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	adminUser, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, adminUser.Username, "secret")
	require.NoError(t, err)
	auditService := audit.NewService(audit.NewMemoryRepository())
	actor := identity.Subject{UserID: adminUser.ID, Username: adminUser.Username, Role: adminUser.Role}
	for index := 0; index < 23; index++ {
		action := audit.ActionTaskChanged
		if index%2 == 0 {
			action = audit.ActionModelUpdated
		}
		require.NoError(t, auditService.Record(ctx, actor, audit.EventInput{
			Action: action, ResourceType: "model", ResourceID: fmt.Sprintf("model-%02d", index),
			Metadata: map[string]any{
				"safe": index, "api_token": "raw-token-sentinel", "internal_error": "database-error-sentinel",
				"raw_result": "raw-result-sentinel", "config_path": "private-path-sentinel",
				"nested": map[string]any{"password": "raw-password-sentinel"},
			},
		}))
	}
	router := gin.New()
	NewHandler(identityService, auditService).Register(router.Group("/admin", identity.Authenticate(identityService, identity.CookiePolicy{})))

	response := performJSON(t, router, login.Token, http.MethodGet, "/admin/audit-events?action=model.updated&page=2&page_size=5", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Items    []audit.Event `json:"items"`
		Total    int64         `json:"total"`
		Page     int           `json:"page"`
		PageSize int           `json:"page_size"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	assert.Equal(t, int64(12), envelope.Total)
	assert.Equal(t, 2, envelope.Page)
	assert.Equal(t, 5, envelope.PageSize)
	require.Len(t, envelope.Items, 5)
	assert.NotContains(t, response.Body.String(), "raw-token-sentinel")
	assert.NotContains(t, response.Body.String(), "raw-password-sentinel")
	assert.NotContains(t, response.Body.String(), "database-error-sentinel")
	assert.NotContains(t, response.Body.String(), "raw-result-sentinel")
	assert.NotContains(t, response.Body.String(), "private-path-sentinel")
	assert.Contains(t, response.Body.String(), audit.RedactedValue)

	for _, query := range []string{"?page=0", "?page=bad", "?page=1001", "?page=9223372036854775807", "?page_size=0", "?page_size=bad"} {
		response = performJSON(t, router, login.Token, http.MethodGet, "/admin/audit-events"+query, nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
		assert.JSONEq(t, `{"error":"invalid list request"}`, response.Body.String(), query)
	}
}

func TestAdminMutationDoesNotStartWhenDurableAuditIntentFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditService := audit.NewService(&failingAdminAuditRepository{delegate: audit.NewMemoryRepository(), failOn: 1})
	router := gin.New()
	NewHandler(identityService, auditService).Register(router.Group("/admin", identity.Authenticate(identityService, identity.CookiePolicy{})))

	response := performJSON(t, router, login.Token, http.MethodPost, "/admin/users", map[string]any{
		"username": "never-created", "password": "temporary-password", "role": "user",
	})
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	_, err = identityService.Authenticate(ctx, "never-created", "temporary-password")
	assert.ErrorIs(t, err, identity.ErrInvalidCredentials)
}

func TestAdminCreateStaysSuccessfulWhenCompletionAppendFailsAndReconciles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	delegate := audit.NewMemoryRepository()
	auditService := audit.NewService(&failingAdminAuditRepository{delegate: delegate, failOn: 2})
	router := gin.New()
	NewHandler(identityService, auditService).Register(router.Group("/admin", identity.Authenticate(identityService, identity.CookiePolicy{})))

	response := performJSON(t, router, login.Token, http.MethodPost, "/admin/users", map[string]any{
		"username": "durable-user", "password": "temporary-password", "role": "user",
	})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	_, err = identityService.Authenticate(ctx, "durable-user", "temporary-password")
	require.NoError(t, err)
	events, err := delegate.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	pending, err := auditService.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	reconcile := performJSON(t, router, login.Token, http.MethodPost, "/admin/audit-events/reconcile?limit=10", nil)
	require.Equal(t, http.StatusOK, reconcile.Code, reconcile.Body.String())
	var result struct {
		Reconciled int `json:"reconciled"`
	}
	require.NoError(t, json.Unmarshal(reconcile.Body.Bytes(), &result))
	assert.Equal(t, 1, result.Reconciled)
	reconcile = performJSON(t, router, login.Token, http.MethodPost, "/admin/audit-events/reconcile?limit=10", nil)
	require.Equal(t, http.StatusOK, reconcile.Code, reconcile.Body.String())
	require.NoError(t, json.Unmarshal(reconcile.Body.Bytes(), &result))
	assert.Zero(t, result.Reconciled)
	events, err = delegate.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}

func TestAdminFinalizesPreparedKnowledgeCompletionWithoutRepeatingFileMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	type account struct {
		user  *identity.User
		login *identity.LoginResult
	}
	accounts := map[identity.Role]account{}
	for _, role := range []identity.Role{identity.RoleAdmin, identity.RoleAuditor, identity.RoleUser} {
		user, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: string(role), Password: "secret", Role: role})
		require.NoError(t, err)
		login, err := identityService.Authenticate(ctx, user.Username, "secret")
		require.NoError(t, err)
		accounts[role] = account{user: user, login: login}
	}

	delegate := audit.NewMemoryRepository()
	auditRepository := &failFirstReadyAdminAuditRepository{delegate: delegate}
	auditService := audit.NewService(auditRepository)
	knowledgeService := knowledge.NewService(auditService)
	mutations := 0
	adminAccount := accounts[identity.RoleAdmin]
	router := gin.New()
	authenticate := identity.Authenticate(identityService, identity.CookiePolicy{})
	router.POST("/knowledge", authenticate,
		knowledge.NewHandler(knowledgeService).Govern(knowledge.KindFingerprint, knowledge.OperationUpdate, func(c *gin.Context) {
			mutations++
			c.JSON(http.StatusOK, gin.H{"message": "legacy success"})
		}))
	NewHandler(identityService, auditService).Register(router.Group("/admin", authenticate))
	mutation := performJSON(t, router, adminAccount.login.Token, http.MethodPost, "/knowledge", nil)
	require.Equal(t, http.StatusOK, mutation.Code, mutation.Body.String())
	assert.Contains(t, mutation.Body.String(), "legacy success")
	assert.Equal(t, 1, mutations)
	requestID := mutation.Header().Get(knowledge.AuditRequestIDHeader)
	require.NotEmpty(t, requestID)

	list := performJSON(t, router, adminAccount.login.Token, http.MethodGet, "/admin/audit-events?action=knowledge.changed", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var pending AuditListResponse
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &pending))
	require.Len(t, pending.Items, 1)
	assert.Equal(t, requestID, pending.Items[0].RequestID)

	finalizePath := "/admin/audit-events/prepared/" + requestID + "/finalize"
	for _, role := range []identity.Role{identity.RoleAuditor, identity.RoleUser} {
		response := performJSON(t, router, accounts[role].login.Token, http.MethodPost, finalizePath, map[string]any{"outcome": "success"})
		assert.Equal(t, http.StatusForbidden, response.Code)
	}
	missing := performJSON(t, router, adminAccount.login.Token, http.MethodPost, "/admin/audit-events/prepared/missing/finalize", map[string]any{"outcome": "success"})
	assert.Equal(t, http.StatusNotFound, missing.Code)
	invalid := performJSON(t, router, adminAccount.login.Token, http.MethodPost, finalizePath, map[string]any{"outcome": "pending"})
	assert.Equal(t, http.StatusBadRequest, invalid.Code)

	finalized := performJSON(t, router, adminAccount.login.Token, http.MethodPost, finalizePath, map[string]any{
		"outcome": "success", "metadata": map[string]any{"files_updated": 1, "api_token": "must-be-redacted"},
	})
	require.Equal(t, http.StatusNoContent, finalized.Code, finalized.Body.String())
	retried := performJSON(t, router, adminAccount.login.Token, http.MethodPost, finalizePath, map[string]any{"outcome": "success"})
	assert.Equal(t, http.StatusNoContent, retried.Code)
	conflict := performJSON(t, router, adminAccount.login.Token, http.MethodPost, finalizePath, map[string]any{"outcome": "failure"})
	assert.Equal(t, http.StatusConflict, conflict.Code)
	assert.Equal(t, 1, mutations, "recovery finalizes audit state and never repeats the legacy file mutation")

	reconcile := performJSON(t, router, adminAccount.login.Token, http.MethodPost, "/admin/audit-events/reconcile?limit=10", nil)
	require.Equal(t, http.StatusOK, reconcile.Code, reconcile.Body.String())
	events, err := delegate.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, requestID, events[1].RequestID)
	assert.NotContains(t, string(events[1].Metadata), "must-be-redacted")
}

func performJSON(t *testing.T, router http.Handler, sessionToken, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: sessionToken})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingAdminAuditRepository struct {
	delegate *audit.MemoryRepository
	failOn   int
	calls    int
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

func (repository *failingAdminAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *failingAdminAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *failingAdminAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
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

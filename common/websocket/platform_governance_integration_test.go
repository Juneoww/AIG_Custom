package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	platformadmin "github.com/Juneoww/AIG_Custom/internal/platform/admin"
	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type completionAppendFailingAuditRepository struct {
	delegate *audit.MemoryRepository
	failOn   int
	calls    int
}

type completionEnqueueFailingAuditRepository struct {
	db       *gorm.DB
	delegate *audit.GormRepository
	ready    int
}

func (repository *completionEnqueueFailingAuditRepository) TransactionDB() *gorm.DB {
	return repository.db
}

func (repository *completionEnqueueFailingAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	return repository.delegate.Append(ctx, event)
}

func (repository *completionEnqueueFailingAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *completionEnqueueFailingAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	if completion.State == audit.CompletionStateReady {
		repository.ready++
		return errors.New("injected ready completion enqueue failure")
	}
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *completionEnqueueFailingAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *completionEnqueueFailingAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *completionEnqueueFailingAuditRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}

func (repository *completionEnqueueFailingAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *completionEnqueueFailingAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func (repository *completionAppendFailingAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	repository.calls++
	if repository.calls == repository.failOn {
		return errors.New("injected audit append failure")
	}
	return repository.delegate.Append(ctx, event)
}

func (repository *completionAppendFailingAuditRepository) List(ctx context.Context, filter audit.Filter) ([]audit.Event, error) {
	return repository.delegate.List(ctx, filter)
}

func (repository *completionAppendFailingAuditRepository) EnqueueCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.EnqueueCompletion(ctx, completion)
}

func (repository *completionAppendFailingAuditRepository) Completion(ctx context.Context, id string) (*audit.CompletionOutbox, error) {
	return repository.delegate.Completion(ctx, id)
}

func (repository *completionAppendFailingAuditRepository) ListPendingCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListPendingCompletions(ctx, limit)
}
func (repository *completionAppendFailingAuditRepository) ListReadyCompletions(ctx context.Context, limit int) ([]audit.CompletionOutbox, error) {
	return repository.delegate.ListReadyCompletions(ctx, limit)
}

func (repository *completionAppendFailingAuditRepository) UpdateCompletion(ctx context.Context, completion *audit.CompletionOutbox) error {
	return repository.delegate.UpdateCompletion(ctx, completion)
}

func (repository *completionAppendFailingAuditRepository) EventExists(ctx context.Context, id string) (bool, error) {
	return repository.delegate.EventExists(ctx, id)
}

func TestPlatformGovernanceHandlersUsePostgresSubjectPoliciesAndNeverLeakModelToken(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("AIG_TEST_DB_DSN is provided by the isolated PostgreSQL Docker test stack")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	identityRepository := identity.NewGormRepository(db)
	require.NoError(t, identityRepository.Init())
	identityService := identity.NewService(identityRepository)
	auditRepository := audit.NewGormRepository(db)
	require.NoError(t, auditRepository.Init())
	auditService := audit.NewService(auditRepository)
	modelRepository := platformmodels.NewGormRepository(db)
	require.NoError(t, modelRepository.Init())
	keyring, err := platformmodels.NewKeyring("integration", bytes.Repeat([]byte{7}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(modelRepository, keyring, auditService)

	suffix := uuid.NewString()
	ctx := context.Background()
	for _, account := range []struct {
		username string
		role     identity.Role
	}{
		{"admin-" + suffix, identity.RoleAdmin},
		{"alice-" + suffix, identity.RoleUser},
		{"bob-" + suffix, identity.RoleUser},
	} {
		created, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: account.username, Password: "secret", Role: account.role, MustChangePassword: true})
		require.NoError(t, err)
		require.NoError(t, identityService.ChangePassword(ctx, created.ID, "secret", "ready-secret"))
	}
	adminLogin, err := identityService.Authenticate(ctx, "admin-"+suffix, "ready-secret")
	require.NoError(t, err)
	aliceLogin, err := identityService.Authenticate(ctx, "alice-"+suffix, "ready-secret")
	require.NoError(t, err)
	bobLogin, err := identityService.Authenticate(ctx, "bob-"+suffix, "ready-secret")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPlatformGovernanceRoutes(
		router.Group("/api/v1/platform"), identityService, identity.CookiePolicy{},
		platformadmin.NewHandler(identityService, auditService), modelService,
	)

	createdUser := governanceRequest(t, router, adminLogin.Token, http.MethodPost, "/api/v1/platform/admin/users", map[string]any{
		"username": "managed-" + suffix, "password": "temporary-password", "role": "auditor",
	})
	assert.Equal(t, http.StatusCreated, createdUser.Code, createdUser.Body.String())
	forbiddenUser := governanceRequest(t, router, aliceLogin.Token, http.MethodPost, "/api/v1/platform/admin/users", map[string]any{
		"username": "forbidden-" + suffix, "password": "temporary-password", "role": "user",
	})
	assert.Equal(t, http.StatusForbidden, forbiddenUser.Code)

	globalToken := "global-handler-plain-token"
	globalModel := governanceRequest(t, router, adminLogin.Token, http.MethodPost, "/api/v1/platform/models", map[string]any{
		"name": "shared", "provider_model": "gpt-shared", "base_url": "https://models.invalid", "token": globalToken, "scope": "global",
	})
	require.Equal(t, http.StatusCreated, globalModel.Code, globalModel.Body.String())
	assert.NotContains(t, globalModel.Body.String(), globalToken)
	assert.Contains(t, globalModel.Body.String(), platformmodels.MaskedToken)

	privateToken := "private-handler-plain-token"
	privateModel := governanceRequest(t, router, aliceLogin.Token, http.MethodPost, "/api/v1/platform/models", map[string]any{
		"name": "mine", "provider_model": "gpt-private", "base_url": "https://models.invalid", "token": privateToken, "scope": "private",
	})
	require.Equal(t, http.StatusCreated, privateModel.Code, privateModel.Body.String())
	assert.NotContains(t, privateModel.Body.String(), privateToken)
	var privateView platformmodels.View
	require.NoError(t, json.Unmarshal(privateModel.Body.Bytes(), &privateView))

	otherRead := governanceRequest(t, router, bobLogin.Token, http.MethodGet, "/api/v1/platform/models/"+privateView.ID, nil)
	assert.Equal(t, http.StatusForbidden, otherRead.Code)
	auditRead := governanceRequest(t, router, adminLogin.Token, http.MethodGet, "/api/v1/platform/admin/audit-events", nil)
	require.Equal(t, http.StatusOK, auditRead.Code, auditRead.Body.String())
	assert.Contains(t, auditRead.Body.String(), string(audit.ActionAccountCreated))
	assert.Contains(t, auditRead.Body.String(), string(audit.ActionModelCreated))
	assert.NotContains(t, auditRead.Body.String(), globalToken)
	assert.NotContains(t, auditRead.Body.String(), privateToken)
}

func TestPlatformModelCreateKeepsCreatedResponseWhenCompletionAppendFails(t *testing.T) {
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	admin, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	require.NoError(t, identityService.ChangePassword(ctx, admin.ID, "secret", "ready-secret"))
	login, err := identityService.Authenticate(ctx, "admin", "ready-secret")
	require.NoError(t, err)

	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(&completionAppendFailingAuditRepository{delegate: auditRepository, failOn: 2})
	modelRepository := platformmodels.NewMemoryRepository()
	keyring, err := platformmodels.NewKeyring("test", bytes.Repeat([]byte{7}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(modelRepository, keyring, auditService)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPlatformGovernanceRoutes(
		router.Group("/api/v1/platform"), identityService, identity.CookiePolicy{},
		platformadmin.NewHandler(identityService, auditService), modelService,
	)

	response := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/models", map[string]any{
		"name": "durable-global", "provider_model": "gpt-test", "base_url": "https://models.invalid", "token": "plain-token", "scope": "global",
	})
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	models, err := modelRepository.List(ctx)
	require.NoError(t, err)
	require.Len(t, models, 1, "the model mutation must execute exactly once")
	pending, err := auditService.PendingCompletions(ctx, identity.Subject{Role: identity.RoleAdmin}, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)

	reconcile := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/admin/audit-events/reconcile", nil)
	require.Equal(t, http.StatusOK, reconcile.Code, reconcile.Body.String())
	reconcile = governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/admin/audit-events/reconcile", nil)
	require.Equal(t, http.StatusOK, reconcile.Code, reconcile.Body.String())
	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}

func TestPlatformDatabaseMutationsRollbackWhenCompletionOutboxCannotPersist(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("AIG_TEST_DB_DSN is provided by the isolated PostgreSQL Docker test stack")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))

	identityRepository := identity.NewGormRepository(db)
	require.NoError(t, identityRepository.Init())
	identityService := identity.NewService(identityRepository)
	auditRepository := audit.NewGormRepository(db)
	require.NoError(t, auditRepository.Init())
	failingAuditRepository := &completionEnqueueFailingAuditRepository{db: db, delegate: auditRepository}
	auditService := audit.NewService(failingAuditRepository)
	modelRepository := platformmodels.NewGormRepository(db)
	require.NoError(t, modelRepository.Init())
	keyring, err := platformmodels.NewKeyring("integration", bytes.Repeat([]byte{7}, 32), nil)
	require.NoError(t, err)
	modelService := platformmodels.NewService(modelRepository, keyring, auditService)
	var preparedBefore int64
	require.NoError(t, db.Model(&audit.CompletionOutbox{}).Where("state = ?", audit.CompletionStatePrepared).Count(&preparedBefore).Error)

	suffix := uuid.NewString()
	ctx := context.Background()
	admin, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "tx-admin-" + suffix, Password: "secret", Role: identity.RoleAdmin, MustChangePassword: true})
	require.NoError(t, err)
	require.NoError(t, identityService.ChangePassword(ctx, admin.ID, "secret", "ready-secret"))
	login, err := identityService.Authenticate(ctx, admin.Username, "ready-secret")
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	identity.RegisterRoutesWithObserver(router.Group("/api/v1/auth"), identityService, identity.CookiePolicy{}, auditService)
	registerPlatformGovernanceRoutes(
		router.Group("/api/v1/platform"), identityService, identity.CookiePolicy{},
		platformadmin.NewHandler(identityService, auditService), modelService,
	)

	managedUsername := "rolled-back-" + suffix
	userResponse := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/admin/users", map[string]any{
		"username": managedUsername, "password": "temporary-password", "role": "user",
	})
	require.Equal(t, http.StatusInternalServerError, userResponse.Code)
	_, err = identityRepository.UserByUsername(ctx, managedUsername)
	assert.ErrorIs(t, err, identity.ErrNotFound, "user insert and completion outbox must share one transaction")

	resetTarget, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "reset-target-" + suffix, Password: "secret", Role: identity.RoleUser})
	require.NoError(t, err)
	resetResponse := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/admin/users/"+resetTarget.ID+"/password-reset", nil)
	require.Equal(t, http.StatusInternalServerError, resetResponse.Code)
	var resetCount int64
	require.NoError(t, db.Table("identity_password_resets").Where("user_id = ?", resetTarget.ID).Count(&resetCount).Error)
	assert.Zero(t, resetCount, "identity reset insert and completion outbox must share one transaction")

	httpResetTarget, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "http-reset-target-" + suffix, Password: "secret", Role: identity.RoleUser})
	require.NoError(t, err)
	httpResetResponse := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/auth/password-resets/"+httpResetTarget.ID, nil)
	require.Equal(t, http.StatusInternalServerError, httpResetResponse.Code)
	resetCount = 0
	require.NoError(t, db.Table("identity_password_resets").Where("user_id = ?", httpResetTarget.ID).Count(&resetCount).Error)
	assert.Zero(t, resetCount, "HTTP identity reset and completion outbox must share one transaction")

	modelName := "rolled-back-model-" + suffix
	modelResponse := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/models", map[string]any{
		"name": modelName, "provider_model": "gpt-test", "base_url": "https://models.invalid", "token": "plain-token", "scope": "global",
	})
	require.Equal(t, http.StatusInternalServerError, modelResponse.Code)
	storedModels, err := modelRepository.List(ctx)
	require.NoError(t, err)
	for index := range storedModels {
		assert.NotEqual(t, modelName, storedModels[index].Name, "model insert and completion outbox must share one transaction")
	}
	assert.Equal(t, 4, failingAuditRepository.ready, "each prepared intent must reach the transactional ready write")
	var preparedAfter int64
	require.NoError(t, db.Model(&audit.CompletionOutbox{}).Where("state = ?", audit.CompletionStatePrepared).Count(&preparedAfter).Error)
	assert.Equal(t, preparedBefore+4, preparedAfter, "failed ready transactions retain exactly one prepared intent each")
}

func governanceRequest(t *testing.T, router http.Handler, sessionToken, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", "csrf")
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: sessionToken})
	request.AddCookie(&http.Cookie{Name: "aig_csrf", Value: "csrf"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

package websocket

import (
	"bytes"
	"context"
	"encoding/json"
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

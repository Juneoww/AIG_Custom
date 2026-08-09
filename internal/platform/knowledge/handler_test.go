package knowledge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGovernBuffersLegacySuccessUntilAuditCompletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditRepository := audit.NewMemoryRepository()
	recorder := &failOnRecord{delegate: audit.NewService(auditRepository), failOn: 2}
	mutated := false

	router := gin.New()
	router.POST("/knowledge", identity.Authenticate(identityService, identity.CookiePolicy{}),
		NewHandler(NewService(recorder)).Govern(KindFingerprint, OperationUpdate, func(c *gin.Context) {
			mutated = true
			c.JSON(http.StatusOK, gin.H{"message": "legacy success"})
		}))
	request := httptest.NewRequest(http.MethodPost, "/knowledge", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.True(t, mutated)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.NotContains(t, response.Body.String(), "legacy success")
	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
}

func TestGovernAsyncRecordsCompletionOnlyAfterBackgroundResult(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(ctx, identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	auditRepository := audit.NewMemoryRepository()
	auditService := audit.NewService(auditRepository)
	var completion AsyncCompletion

	router := gin.New()
	router.POST("/update", identity.Authenticate(identityService, identity.CookiePolicy{}),
		NewHandler(NewService(auditService)).GovernAsync(KindSystemData, OperationUpdate, func(c *gin.Context) {
			var ok bool
			completion, ok = CurrentAsyncCompletion(c)
			require.True(t, ok)
			c.Status(http.StatusAccepted)
		}))
	request := httptest.NewRequest(http.MethodPost, "/update", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusAccepted, response.Code)

	events, err := auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.ActionKnowledgeChangeRequested, events[0].Action)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)

	require.NoError(t, completion(ctx, true, map[string]any{"files_updated": 3}))
	events, err = auditRepository.List(ctx, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
	assert.Equal(t, events[0].RequestID, events[1].RequestID)
}

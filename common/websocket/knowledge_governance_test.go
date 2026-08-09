package websocket

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformknowledge "github.com/Juneoww/AIG_Custom/internal/platform/knowledge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyKnowledgeWriteHandlerRejectsDirectMountAndOnlyRunsThroughGovernedFacade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	legacy := HandleCreate(func(string) error { called = true; return nil })
	direct := gin.New()
	direct.POST("/knowledge", legacy)
	request := httptest.NewRequest(http.MethodPost, "/knowledge", bytes.NewBufferString(`{"content":"id: demo"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	direct.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.False(t, called)

	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{Username: "admin", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(context.Background(), "admin", "secret")
	require.NoError(t, err)
	auditService := audit.NewService(audit.NewMemoryRepository())
	governed := gin.New()
	governed.POST("/knowledge", identity.Authenticate(identityService, identity.CookiePolicy{}),
		platformknowledge.NewHandler(platformknowledge.NewService(auditService)).Govern(
			platformknowledge.KindMCP, platformknowledge.OperationCreate, legacy,
		))
	request = httptest.NewRequest(http.MethodPost, "/knowledge", bytes.NewBufferString(`{"content":"id: demo"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
	response = httptest.NewRecorder()
	governed.ServeHTTP(response, request)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.True(t, called)
	events, err := auditService.Query(context.Background(), identity.Subject{Role: identity.RoleAdmin}, audit.Filter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, audit.OutcomePending, events[0].Outcome)
	assert.Equal(t, audit.ActionKnowledgeChanged, events[1].Action)
	assert.Equal(t, audit.OutcomeSuccess, events[1].Outcome)
}

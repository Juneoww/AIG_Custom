package websocket

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformknowledge "github.com/Juneoww/AIG_Custom/internal/platform/knowledge"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptCollectionDeleteUsesOpaqueRouteIDThroughGovernedFacade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	identityService := identity.NewService(identity.NewMemoryRepository())
	_, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{Username: "admin-delete", Password: "secret", Role: identity.RoleAdmin})
	require.NoError(t, err)
	login, err := identityService.Authenticate(context.Background(), "admin-delete", "secret")
	require.NoError(t, err)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf", SameSite: http.SameSiteLaxMode}

	deleted := ""
	auditService := audit.NewService(audit.NewMemoryRepository())
	router := gin.New()
	router.DELETE(
		"/api/v1/knowledge/prompt_collections/:id",
		identity.Authenticate(identityService, policy),
		identity.RequireCSRF(policy),
		platformknowledge.NewHandler(platformknowledge.NewService(auditService)).Govern(
			platformknowledge.KindPromptCollection,
			platformknowledge.OperationDelete,
			HandleDelete(func(id string) error { deleted = id; return nil }),
		),
	)
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/knowledge/prompt_collections/prompt-opaque-1", nil)
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: login.Token})
	request.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	request.Header.Set("X-CSRF-Token", "csrf")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "prompt-opaque-1", deleted)
}

func TestAgentConnectFailureUsesFixedSafeMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := agentConnectivityCheck
	t.Cleanup(func() { agentConnectivityCheck = original })

	for _, testCase := range []struct {
		name       string
		check      func(string) (bool, string, error)
		wantStatus int
	}{
		{
			name: "provider failure",
			check: func(string) (bool, string, error) {
				return false, "raw-provider-token-sentinel", nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "runtime failure",
			check: func(string) (bool, string, error) {
				return false, "", errors.New("C:\\private\\provider-token-sentinel")
			},
			wantStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agentConnectivityCheck = testCase.check
			router := gin.New()
			router.POST("/connect", HandleAgentConnect)
			request := httptest.NewRequest(http.MethodPost, "/connect", bytes.NewBufferString(`{"content":"provider: demo"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			require.Equal(t, testCase.wantStatus, response.Code)
			assert.NotContains(t, response.Body.String(), "provider-token-sentinel")
			assert.NotContains(t, response.Body.String(), "C:\\private")
		})
	}
}

func TestAgentPromptFailureUsesFixedSafeMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := agentPromptTestCheck
	t.Cleanup(func() { agentPromptTestCheck = original })

	for _, testCase := range []struct {
		name       string
		check      func(string, string) (ConnectResultUpdate, error)
		wantStatus int
	}{
		{
			name: "provider failure",
			check: func(string, string) (ConnectResultUpdate, error) {
				return ConnectResultUpdate{Content: ConnectResultContent{Message: "raw-provider-token-sentinel"}}, nil
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "runtime failure",
			check: func(string, string) (ConnectResultUpdate, error) {
				return ConnectResultUpdate{}, errors.New("C:\\private\\provider-token-sentinel")
			},
			wantStatus: http.StatusInternalServerError,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agentPromptTestCheck = testCase.check
			router := gin.New()
			router.POST("/prompt-test", HandleAgentPromptTest)
			request := httptest.NewRequest(http.MethodPost, "/prompt-test", bytes.NewBufferString(`{"content":"provider: demo","prompt":"hello"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			require.Equal(t, testCase.wantStatus, response.Code)
			assert.NotContains(t, response.Body.String(), "provider-token-sentinel")
			assert.NotContains(t, response.Body.String(), "C:\\private")
		})
	}
}

func TestAgentPromptSuccessNeverFallsBackToRawProviderPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := agentPromptTestCheck
	t.Cleanup(func() { agentPromptTestCheck = original })
	agentPromptTestCheck = func(string, string) (ConnectResultUpdate, error) {
		return ConnectResultUpdate{Content: ConnectResultContent{
			Success: true,
			Message: "provider-message-token-sentinel",
			ProviderResponse: &ProviderResponse{Raw: map[string]any{
				"path":  "C:\\private\\provider-token-sentinel",
				"token": "provider-token-sentinel",
			}},
		}}, nil
	}
	router := gin.New()
	router.POST("/prompt-test", HandleAgentPromptTest)
	request := httptest.NewRequest(http.MethodPost, "/prompt-test", bytes.NewBufferString(`{"content":"provider: demo","prompt":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.NotContains(t, response.Body.String(), "provider-token-sentinel")
	assert.NotContains(t, response.Body.String(), "provider-message-token-sentinel")
	assert.NotContains(t, response.Body.String(), "C:\\private")
}

func TestAgentPromptSuccessBoundsExplicitOutputByUTF8Bytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := agentPromptTestCheck
	t.Cleanup(func() { agentPromptTestCheck = original })
	output := strings.Repeat("中", 90_000) // 270,000 UTF-8 bytes: deliberately above the browser-safe 256 KiB contract.
	agentPromptTestCheck = func(string, string) (ConnectResultUpdate, error) {
		return ConnectResultUpdate{Content: ConnectResultContent{
			Success:          true,
			ProviderResponse: &ProviderResponse{Output: &output},
		}}, nil
	}
	router := gin.New()
	router.POST("/prompt-test", HandleAgentPromptTest)
	request := httptest.NewRequest(http.MethodPost, "/prompt-test", bytes.NewBufferString(`{"content":"provider: demo","prompt":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "Prompt test completed")
	assert.NotContains(t, response.Body.String(), output)
}

func TestKnowledgeHandlersDoNotReturnOrLogInternalErrorDetails(t *testing.T) {
	rawErrorMessage := regexp.MustCompile(`"message"\s*:\s*[^\n]*err\.Error\(\)`)
	for _, fileName := range []string{"knowledge_api.go", "knowledge2_api.go"} {
		source, err := os.ReadFile(fileName)
		require.NoError(t, err)
		assert.Empty(t, rawErrorMessage.FindAll(source, -1), fileName)
		assert.NotContains(t, string(source), `gologger.Infoln("test_agent_connect", lastLine)`)
		assert.NotContains(t, string(source), `gologger.Infof("prompt test result: %s", lastLine)`)
		assert.NotContains(t, string(source), "log.Error(path, err.Error())")
	}
}

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

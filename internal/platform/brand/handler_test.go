package brand

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectedBrandHandlerUsesAuthenticatedRBACAndCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	identityService := identity.NewService(identity.NewMemoryRepository())
	tokens := createBrandHandlerUsers(t, identityService)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf"}
	router := gin.New()
	group := router.Group("/api/v1/platform", identity.Authenticate(identityService, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	NewHandler(NewGovernedService(NewMemoryRepository(), audit.NewService(audit.NewMemoryRepository()))).Register(group)

	for _, actor := range []string{"alice", "auditor", "admin"} {
		assert.Equal(t, http.StatusOK, performBrandRequest(router, tokens[actor], http.MethodGet, "/api/v1/platform/brand", nil, false).Code)
	}
	body := []byte(fmt.Sprintf(`{"product_name":"企业安全平台","primary_color":"#1677FF","logo":"%s","logo_mime":"image/png"}`, base64.StdEncoding.EncodeToString(testPNG(t))))
	assert.Equal(t, http.StatusForbidden, performBrandRequest(router, tokens["alice"], http.MethodPut, "/api/v1/platform/brand", body, true).Code)
	assert.Equal(t, http.StatusForbidden, performBrandRequest(router, tokens["auditor"], http.MethodPut, "/api/v1/platform/brand", body, true).Code)
	assert.Equal(t, http.StatusForbidden, performBrandRequest(router, tokens["admin"], http.MethodPut, "/api/v1/platform/brand", body, false).Code)
	assert.Equal(t, http.StatusOK, performBrandRequest(router, tokens["admin"], http.MethodPut, "/api/v1/platform/brand", body, true).Code)
}

func createBrandHandlerUsers(t *testing.T, service *identity.Service) map[string]string {
	t.Helper()
	tokens := map[string]string{}
	for _, user := range []struct {
		id, name string
		role     identity.Role
	}{{"alice", "alice", identity.RoleUser}, {"auditor", "auditor", identity.RoleAuditor}, {"admin", "admin", identity.RoleAdmin}} {
		_, err := service.CreateUser(context.Background(), identity.CreateUserInput{ID: user.id, Username: user.name, Password: "secret", Role: user.role})
		require.NoError(t, err)
		login, err := service.Authenticate(context.Background(), user.name, "secret")
		require.NoError(t, err)
		tokens[user.name] = login.Token
	}
	return tokens
}

func performBrandRequest(router http.Handler, token, method, path string, body []byte, csrf bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: token})
	if csrf {
		request.AddCookie(&http.Cookie{Name: "aig_csrf", Value: "csrf-token"})
		request.Header.Set("X-CSRF-Token", "csrf-token")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

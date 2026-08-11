package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Keep representative production route declarations protected. Authentication
// itself is covered by identity middleware tests; this test prevents a future
// server route edit from silently dropping the required guard.
func TestServerRoutesKeepKnowledgeAppAndSystemRBACGuards(t *testing.T) {
	source, err := os.ReadFile("server.go")
	require.NoError(t, err)

	for _, route := range []string{
		"fingerprints.GET(\"\", identity.RequireRole(identity.RoleAdmin, identity.RoleUser, identity.RoleAuditor)",
		"fingerprints.POST(\"\", knowledgeHandler.Govern(platformknowledge.KindFingerprint",
		"tasks.GET(\"/:sessionId\", identity.RequireOwnerOrRole(taskOwner, false)",
		"tasks.PUT(\"/:sessionId\", identity.RequireOwnerOrRole(taskOwner, true)",
		"registerPlatformModelRoutes(models, platformModelService, modelStore)",
		"system.GET(\"/version\", identity.RequireRole(identity.RoleAdmin, identity.RoleAuditor)",
		"system.POST(\"/update-data\", knowledgeHandler.GovernAsync(platformknowledge.KindSystemData",
	} {
		require.Contains(t, string(source), route)
	}
}

func TestRBACHTTPGuardsKnowledgeAndSystemWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	policy := identity.CookiePolicy{
		SessionCookieName: "aig_session",
		CSRFCookieName:    "aig_csrf",
		Secure:            true,
		HTTPOnly:          true,
		SameSite:          http.SameSiteLaxMode,
	}
	service := identity.NewService(identity.NewMemoryRepository())
	ctx := context.Background()
	tokens := map[identity.Role]string{}
	for _, user := range []struct {
		username string
		role     identity.Role
	}{
		{"admin", identity.RoleAdmin},
		{"user", identity.RoleUser},
		{"auditor", identity.RoleAuditor},
	} {
		_, err := service.CreateUser(ctx, identity.CreateUserInput{Username: user.username, Password: "secret", Role: user.role})
		require.NoError(t, err)
		login, err := service.Authenticate(ctx, user.username, "secret")
		require.NoError(t, err)
		tokens[user.role] = login.Token
	}

	r := gin.New()
	registerProtectedWrite := func(path string) *gin.RouterGroup {
		group := r.Group(path)
		group.Use(setupIdentityMiddleware(service, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
		return group
	}
	registerProtectedWrite("/api/v1/knowledge").POST("/fingerprints", identity.RequireRole(identity.RoleAdmin), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	registerProtectedWrite("/api/v1/system").POST("/update-data", identity.RequireRole(identity.RoleAdmin), func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, tc := range []struct {
		name   string
		role   identity.Role
		path   string
		status int
	}{
		{"user cannot write knowledge", identity.RoleUser, "/api/v1/knowledge/fingerprints", http.StatusForbidden},
		{"auditor cannot write knowledge", identity.RoleAuditor, "/api/v1/knowledge/fingerprints", http.StatusForbidden},
		{"admin can write knowledge", identity.RoleAdmin, "/api/v1/knowledge/fingerprints", http.StatusNoContent},
		{"user cannot write system", identity.RoleUser, "/api/v1/system/update-data", http.StatusForbidden},
		{"auditor cannot write system", identity.RoleAuditor, "/api/v1/system/update-data", http.StatusForbidden},
		{"admin can write system", identity.RoleAdmin, "/api/v1/system/update-data", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, tc.path, nil)
			request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: tokens[tc.role]})
			request.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
			request.Header.Set("X-CSRF-Token", "csrf")
			response := httptest.NewRecorder()
			r.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
		})
	}

	t.Run("forged identity headers cannot bypass authentication", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/system/update-data", nil)
		request.Header.Set("username", "admin")
		request.Header.Set("role", string(identity.RoleAdmin))
		request.Header.Set("X-CSRF-Token", "csrf")
		request.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code)
	})
}

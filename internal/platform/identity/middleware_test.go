package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthenticationNeverTrustsUsernameHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := newTestService(t)
	r := gin.New()
	r.GET("/protected", Authenticate(service, CookiePolicy{}), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("username", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestStateChangeRequiresDoubleSubmitCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := newTestService(t)
	ctx := context.Background()
	_, err := service.CreateUser(ctx, CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
	require.NoError(t, err)
	login, err := service.Authenticate(ctx, "alice", "secret")
	require.NoError(t, err)
	policy := CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf", Secure: true, HTTPOnly: true, SameSite: http.SameSiteLaxMode}
	r := gin.New()
	r.POST("/change", Authenticate(service, policy), RequireCSRF(policy), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/change", nil)
	req.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: login.Token})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCookiePolicyAndTrustedProxyTLSRules(t *testing.T) {
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "production", TrustedProxyCIDRs: "10.0.0.0/8"})
	require.NoError(t, err)
	assert.True(t, policy.Secure)
	assert.True(t, policy.HTTPOnly)
	assert.Equal(t, http.SameSiteLaxMode, policy.SameSite)

	trusted := httptest.NewRequest(http.MethodGet, "http://aig.local", nil)
	trusted.RemoteAddr = "10.1.2.3:1234"
	trusted.Header.Set("X-Forwarded-Proto", "https")
	assert.True(t, policy.RequestIsHTTPS(trusted))

	untrusted := httptest.NewRequest(http.MethodGet, "http://aig.local", nil)
	untrusted.RemoteAddr = "192.168.1.3:1234"
	untrusted.Header.Set("X-Forwarded-Proto", "https")
	assert.False(t, policy.RequestIsHTTPS(untrusted))
}

func TestInsecureCookieExceptionOnlyWorksInExplicitTestEnvironment(t *testing.T) {
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
	require.NoError(t, err)
	assert.False(t, policy.Secure)
	_, err = NewCookiePolicy(CookieConfig{AppEnv: "production", AllowInsecureTestCookie: true})
	require.Error(t, err)
}

func TestRoleAndOwnerPolicyMatrix(t *testing.T) {
	admin := Subject{UserID: "admin", Username: "admin", Role: RoleAdmin}
	user := Subject{UserID: "user", Username: "user", Role: RoleUser}
	auditor := Subject{UserID: "auditor", Username: "auditor", Role: RoleAuditor}

	assert.True(t, CanAccessOwnerOrRole(admin, "other", true))
	assert.True(t, CanAccessOwnerOrRole(user, "user", true))
	assert.False(t, CanAccessOwnerOrRole(user, "other", true))
	assert.True(t, CanAccessOwnerOrRole(auditor, "other", false))
	assert.False(t, CanAccessOwnerOrRole(auditor, "other", true))
	assert.True(t, HasAnyRole(admin, RoleAdmin))
	assert.False(t, HasAnyRole(user, RoleAdmin))
}

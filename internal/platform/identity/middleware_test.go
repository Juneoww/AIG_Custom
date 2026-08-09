package identity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
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

type recordingGovernanceObserver struct {
	authentication []AuthenticationEvent
	passwordResets []string
}

type failingPasswordResetObserver struct{}

func (*failingPasswordResetObserver) AuthenticationAttempt(context.Context, AuthenticationEvent) error {
	return nil
}

func (*failingPasswordResetObserver) BeginPasswordReset(context.Context, Subject, string) (GovernanceCompletion, error) {
	return nil, errors.New("injected audit failure")
}

func (observer *recordingGovernanceObserver) AuthenticationAttempt(_ context.Context, event AuthenticationEvent) error {
	observer.authentication = append(observer.authentication, event)
	return nil
}

func (observer *recordingGovernanceObserver) BeginPasswordReset(_ context.Context, _ Subject, userID string) (GovernanceCompletion, error) {
	return func(_ context.Context, success bool) error {
		if success {
			observer.passwordResets = append(observer.passwordResets, userID)
		}
		return nil
	}, nil
}

func TestAuthRoutesNotifyGovernanceObserverForSuccessfulAndFailedLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := newTestService(t)
	_, err := service.CreateUser(context.Background(), CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
	require.NoError(t, err)
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
	require.NoError(t, err)
	observer := &recordingGovernanceObserver{}
	router := gin.New()
	RegisterRoutesWithObserver(router.Group("/auth"), service, policy, observer)

	for _, password := range []string{"secret", "wrong"} {
		body, marshalErr := json.Marshal(map[string]string{"username": "alice", "password": password})
		require.NoError(t, marshalErr)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body)))
	}

	require.Len(t, observer.authentication, 2)
	assert.True(t, observer.authentication[0].Success)
	assert.Equal(t, "alice", observer.authentication[0].Subject.Username)
	assert.False(t, observer.authentication[1].Success)
	assert.Equal(t, "alice", observer.authentication[1].Username)
}

func TestHTTPPasswordResetDoesNotStartWhenAuditWriteAheadFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := NewMemoryRepository()
	service := NewService(repository)
	ctx := context.Background()
	admin, err := service.CreateUser(ctx, CreateUserInput{Username: "admin", Password: "secret", Role: RoleAdmin})
	require.NoError(t, err)
	target, err := service.CreateUser(ctx, CreateUserInput{Username: "target", Password: "secret", Role: RoleUser})
	require.NoError(t, err)
	login, err := service.Authenticate(ctx, admin.Username, "secret")
	require.NoError(t, err)
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
	require.NoError(t, err)
	router := gin.New()
	RegisterRoutesWithObserver(router.Group("/auth"), service, policy, &failingPasswordResetObserver{})

	request := httptest.NewRequest(http.MethodPost, "/auth/password-resets/"+target.ID, nil)
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: login.Token})
	request.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	request.Header.Set("X-CSRF-Token", "csrf")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Empty(t, repository.resets)
}

func TestAuthRoutesRequireHTTPSExceptExplicitTestCookieMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := newTestService(t)
	require.NoError(t, func() error {
		_, err := service.CreateUser(context.Background(), CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
		return err
	}())
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "production", TrustedProxyCIDRs: "10.0.0.0/8"})
	require.NoError(t, err)
	r := gin.New()
	RegisterRoutes(r.Group("/auth"), service, policy)
	body, err := json.Marshal(map[string]string{"username": "alice", "password": "secret"})
	require.NoError(t, err)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body)),
		func() *http.Request {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
			req.RemoteAddr = "192.168.1.2:443"
			req.Header.Set("X-Forwarded-Proto", "https")
			return req
		}(),
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, request)
		assert.Equal(t, http.StatusUpgradeRequired, w.Code)
	}
	trusted := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	trusted.RemoteAddr = "10.2.3.4:443"
	trusted.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, trusted)
	assert.Equal(t, http.StatusOK, w.Code)

	testPolicy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
	require.NoError(t, err)
	testRouter := gin.New()
	RegisterRoutes(testRouter.Group("/auth"), service, testPolicy)
	w = httptest.NewRecorder()
	testRouter.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body)))
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthLifecycleRoutesRotateAndResetWithoutLeakingTokensToUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := newTestService(t)
	ctx := context.Background()
	admin, err := service.CreateUser(ctx, CreateUserInput{Username: "admin", Password: "secret", Role: RoleAdmin})
	require.NoError(t, err)
	user, err := service.CreateUser(ctx, CreateUserInput{Username: "user", Password: "secret", Role: RoleUser})
	require.NoError(t, err)
	adminLogin, err := service.Authenticate(ctx, admin.Username, "secret")
	require.NoError(t, err)
	userLogin, err := service.Authenticate(ctx, user.Username, "secret")
	require.NoError(t, err)
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "production"})
	require.NoError(t, err)
	r := gin.New()
	observer := &recordingGovernanceObserver{}
	RegisterRoutesWithObserver(r.Group("/auth"), service, policy, observer)

	userReset := httptest.NewRequest(http.MethodPost, "/auth/password-resets/"+user.ID, nil)
	userReset.TLS = &tls.ConnectionState{}
	userReset.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: userLogin.Token})
	userReset.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	userReset.Header.Set("X-CSRF-Token", "csrf")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, userReset)
	assert.Equal(t, http.StatusForbidden, w.Code)

	adminReset := httptest.NewRequest(http.MethodPost, "/auth/password-resets/"+user.ID, nil)
	adminReset.TLS = &tls.ConnectionState{}
	adminReset.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: adminLogin.Token})
	adminReset.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	adminReset.Header.Set("X-CSRF-Token", "csrf")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, adminReset)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())
	assert.Equal(t, []string{user.ID}, observer.passwordResets)

	rotate := httptest.NewRequest(http.MethodPost, "/auth/rotate-session", nil)
	rotate.TLS = &tls.ConnectionState{}
	rotate.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: adminLogin.Token})
	rotate.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	rotate.Header.Set("X-CSRF-Token", "csrf")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, rotate)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Body.String())

	resetToken, err := service.CreatePasswordReset(ctx, user.ID)
	require.NoError(t, err)
	confirmBody, err := json.Marshal(map[string]string{"token": resetToken, "temporary_password": "new-temporary-password"})
	require.NoError(t, err)
	insecureConfirm := httptest.NewRequest(http.MethodPost, "/auth/password-resets/confirm", bytes.NewReader(confirmBody))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, insecureConfirm)
	assert.Equal(t, http.StatusUpgradeRequired, w.Code)
	secureConfirm := httptest.NewRequest(http.MethodPost, "/auth/password-resets/confirm", bytes.NewReader(confirmBody))
	secureConfirm.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, secureConfirm)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestRequireRoleGuardsRepresentativeReadAndWriteRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/knowledge", testSubject(RoleAuditor), RequireRole(RoleAdmin, RoleUser, RoleAuditor), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/knowledge", testSubject(RoleAuditor), RequireRole(RoleAdmin), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.PUT("/app/owned", testSubject(RoleUser), RequireOwnerOrRole(func(*gin.Context) string { return "user" }, true), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.DELETE("/system", testSubject(RoleAuditor), RequireRole(RoleAdmin), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, tc := range []struct {
		method, path string
		status       int
	}{{http.MethodGet, "/knowledge", 204}, {http.MethodPost, "/knowledge", 403}, {http.MethodPut, "/app/owned", 204}, {http.MethodDelete, "/system", 403}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		assert.Equal(t, tc.status, w.Code)
	}
}

func testSubject(role Role) gin.HandlerFunc {
	return func(c *gin.Context) { c.Set(subjectContextKey, Subject{UserID: "user", Role: role}); c.Next() }
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
	assert.True(t, CanAccessOwnerOrRole(Subject{UserID: "identity-id", Username: "legacy-owner", Role: RoleUser}, "legacy-owner", true))
	assert.False(t, CanAccessOwnerOrRole(user, "other", true))
	assert.True(t, CanAccessOwnerOrRole(auditor, "other", false))
	assert.False(t, CanAccessOwnerOrRole(auditor, "other", true))
	assert.True(t, HasAnyRole(admin, RoleAdmin))
	assert.False(t, HasAnyRole(user, RoleAdmin))
}

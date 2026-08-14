package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserBootstrapCSRFAndCurrentSubject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, repository := newTestService(t)
	user, err := service.CreateUser(context.Background(), CreateUserInput{
		Username:           "alice",
		Password:           "secret",
		Role:               RoleUser,
		MustChangePassword: true,
	})
	require.NoError(t, err)
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
	require.NoError(t, err)
	router := gin.New()
	RegisterRoutes(router.Group("/auth"), service, policy)

	t.Run("anonymous CSRF bootstrap does not create a session", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))

		require.Equal(t, http.StatusOK, response.Code)
		csrfCookie, ok := responseCookie(response, policy.CSRFCookieName)
		require.True(t, ok)
		assert.False(t, csrfCookie.HttpOnly)
		_, hasSession := responseCookie(response, policy.SessionCookieName)
		assert.False(t, hasSession)
		assert.Empty(t, repository.Sessions())

		var payload map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		assert.Equal(t, map[string]any{"csrf_token": csrfCookie.Value}, payload)
	})

	t.Run("anonymous current subject is rejected", func(t *testing.T) {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/me", nil))
		assert.Equal(t, http.StatusUnauthorized, response.Code)
	})

	t.Run("authenticated current subject exposes only the safe browser model", func(t *testing.T) {
		login, loginErr := service.Authenticate(context.Background(), user.Username, "secret")
		require.NoError(t, loginErr)
		request := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
		request.AddCookie(policy.SessionCookie(login.Token))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		require.Equal(t, http.StatusOK, response.Code)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		assert.Equal(t, map[string]any{
			"id":                   user.ID,
			"username":             user.Username,
			"role":                 string(user.Role),
			"must_change_password": true,
		}, payload)
	})
}

func TestLoginRequiresInitializedCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, repository := newTestService(t)
	_, err := service.CreateUser(context.Background(), CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
	require.NoError(t, err)
	policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
	require.NoError(t, err)
	router := gin.New()
	RegisterRoutes(router.Group("/auth"), service, policy)
	loginBody := []byte(`{"username":"alice","password":"secret"}`)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginBody)))
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, repository.Sessions())

	bootstrap := httptest.NewRecorder()
	router.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))
	require.Equal(t, http.StatusOK, bootstrap.Code)
	initialCSRF, ok := responseCookie(bootstrap, policy.CSRFCookieName)
	require.True(t, ok)

	mismatchRequest := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginBody))
	mismatchRequest.AddCookie(initialCSRF)
	mismatchRequest.Header.Set("X-CSRF-Token", "mismatched-csrf-token")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, mismatchRequest)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, repository.Sessions())

	request := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(loginBody))
	request.AddCookie(initialCSRF)
	request.Header.Set("X-CSRF-Token", initialCSRF.Value)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	sessionCookie, ok := responseCookie(response, policy.SessionCookieName)
	require.True(t, ok)
	assert.True(t, sessionCookie.HttpOnly)
	rotatedCSRF, ok := responseCookie(response, policy.CSRFCookieName)
	require.True(t, ok)
	assert.False(t, rotatedCSRF.HttpOnly)
	assert.NotEqual(t, initialCSRF.Value, rotatedCSRF.Value)
	assert.Len(t, repository.Sessions(), 1)
}

func TestPasswordResetConfirmationRequiresCSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("missing anonymous CSRF is rejected without leaking or consuming the reset token", func(t *testing.T) {
		service, _ := newTestService(t)
		user, err := service.CreateUser(context.Background(), CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
		require.NoError(t, err)
		resetToken, err := service.CreatePasswordReset(context.Background(), user.ID)
		require.NoError(t, err)
		policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
		require.NoError(t, err)
		var accessLog bytes.Buffer
		router := gin.New()
		router.Use(gin.LoggerWithWriter(&accessLog))
		RegisterRoutes(router.Group("/auth"), service, policy)
		body, err := json.Marshal(map[string]string{"token": resetToken, "temporary_password": "new-temporary-password"})
		require.NoError(t, err)

		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/auth/password-resets/confirm", bytes.NewReader(body)))

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.NotContains(t, response.Body.String(), resetToken)
		assert.NotContains(t, accessLog.String(), resetToken)
		assert.NoError(t, service.ResetPassword(context.Background(), resetToken, "still-valid-temporary-password"))
	})

	t.Run("mismatched anonymous CSRF is rejected without consuming the reset token", func(t *testing.T) {
		service, _ := newTestService(t)
		user, err := service.CreateUser(context.Background(), CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
		require.NoError(t, err)
		resetToken, err := service.CreatePasswordReset(context.Background(), user.ID)
		require.NoError(t, err)
		policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
		require.NoError(t, err)
		router := gin.New()
		RegisterRoutes(router.Group("/auth"), service, policy)
		bootstrap := httptest.NewRecorder()
		router.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))
		require.Equal(t, http.StatusOK, bootstrap.Code)
		csrfCookie, ok := responseCookie(bootstrap, policy.CSRFCookieName)
		require.True(t, ok)
		body, err := json.Marshal(map[string]string{"token": resetToken, "temporary_password": "new-temporary-password"})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/auth/password-resets/confirm", bytes.NewReader(body))
		request.AddCookie(csrfCookie)
		request.Header.Set("X-CSRF-Token", "mismatched-csrf-token")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.NoError(t, service.ResetPassword(context.Background(), resetToken, "still-valid-temporary-password"))
	})

	t.Run("matching anonymous CSRF permits confirmation without leaking the reset token", func(t *testing.T) {
		service, _ := newTestService(t)
		user, err := service.CreateUser(context.Background(), CreateUserInput{Username: "alice", Password: "secret", Role: RoleUser})
		require.NoError(t, err)
		resetToken, err := service.CreatePasswordReset(context.Background(), user.ID)
		require.NoError(t, err)
		policy, err := NewCookiePolicy(CookieConfig{AppEnv: "test", AllowInsecureTestCookie: true})
		require.NoError(t, err)
		var accessLog bytes.Buffer
		router := gin.New()
		router.Use(gin.LoggerWithWriter(&accessLog))
		RegisterRoutes(router.Group("/auth"), service, policy)

		bootstrap := httptest.NewRecorder()
		router.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/auth/csrf", nil))
		require.Equal(t, http.StatusOK, bootstrap.Code)
		csrfCookie, ok := responseCookie(bootstrap, policy.CSRFCookieName)
		require.True(t, ok)
		body, err := json.Marshal(map[string]string{"token": resetToken, "temporary_password": "new-temporary-password"})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/auth/password-resets/confirm", bytes.NewReader(body))
		request.AddCookie(csrfCookie)
		request.Header.Set("X-CSRF-Token", csrfCookie.Value)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		assert.Equal(t, http.StatusNoContent, response.Code)
		assert.NotContains(t, response.Body.String(), resetToken)
		assert.NotContains(t, accessLog.String(), resetToken)
	})
}

func responseCookie(response *httptest.ResponseRecorder, name string) (*http.Cookie, bool) {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie, true
		}
	}
	return nil, false
}

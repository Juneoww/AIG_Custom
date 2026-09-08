package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformmodels "github.com/Juneoww/AIG_Custom/internal/platform/models"

	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelProbeRoutesEnforceSessionCSRFAndSafeInput(t *testing.T) {
	ctx := context.Background()
	ids := identity.NewService(identity.NewMemoryRepository())
	user, err := ids.CreateUser(ctx, identity.CreateUserInput{Username: "probe-user", Password: "probe-password", Role: identity.RoleUser})
	require.NoError(t, err)
	login, err := ids.Authenticate(ctx, user.Username, "probe-password")
	require.NoError(t, err)
	auditor, err := ids.CreateUser(ctx, identity.CreateUserInput{Username: "probe-auditor", Password: "probe-password", Role: identity.RoleAuditor})
	require.NoError(t, err)
	auditLogin, err := ids.Authenticate(ctx, auditor.Username, "probe-password")
	require.NoError(t, err)
	temporary, err := ids.CreateUser(ctx, identity.CreateUserInput{Username: "probe-temporary", Password: "probe-password", Role: identity.RoleUser, MustChangePassword: true})
	require.NoError(t, err)
	temporaryLogin, err := ids.Authenticate(ctx, temporary.Username, "probe-password")
	require.NoError(t, err)
	var logs bytes.Buffer
	router := newWebServerRouter(&logs, &logs)
	policy := identity.CookiePolicy{}
	group := router.Group("/api/v1/platform", setupIdentityMiddleware(ids, policy), identity.RequirePasswordChangeCompleted(), identity.RequireCSRF(policy))
	registerGovernanceModelRoutes(group.Group("/models"), platformmodels.NewService(platformmodels.NewMemoryRepository(), nil, nil))
	body := json.RawMessage(`{"provider_model":"m","base_url":"https://api.example/v1","token":"secret-canary"}`)
	for _, path := range []string{"/api/v1/platform/models/test", "/api/v1/platform/models/id/test"} {
		response := governanceRequest(t, router, "", http.MethodPost, path, body)
		require.Equal(t, 401, response.Code)
		response = governanceRequest(t, router, temporaryLogin.Token, http.MethodPost, path, body)
		require.Equal(t, 403, response.Code)
		response = governanceRequest(t, router, auditLogin.Token, http.MethodPost, path, body)
		require.Equal(t, 403, response.Code)
		response = governanceRequest(t, router, login.Token, http.MethodPost, path, json.RawMessage(`{"token":123}`))
		require.Equal(t, 400, response.Code)
		response = governanceRequest(t, router, login.Token, http.MethodPost, path+"?token=secret-canary", body)
		require.Equal(t, 400, response.Code)
		response = governanceRequest(t, router, login.Token, http.MethodPost, path, map[string]string{"token": strings.Repeat("x", 16385)})
		require.Equal(t, 400, response.Code)
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
		request.AddCookie(&http.Cookie{Name: "aig_session", Value: login.Token})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, 403, recorder.Code)
	}
	response := governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/models/test", body)
	require.Equal(t, 500, response.Code)
	require.NotContains(t, response.Body.String(), "secret-canary")
	response = governanceRequest(t, router, login.Token, http.MethodPost, "/api/v1/platform/models/id/test", body)
	require.Equal(t, 404, response.Code)
	require.NotContains(t, logs.String(), "secret-canary")
}

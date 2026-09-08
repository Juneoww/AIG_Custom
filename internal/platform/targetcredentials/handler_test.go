package targetcredentials

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHandlerSecretsStrictBodyAndRevision(t *testing.T) {
	s, _, subject := fixture(t)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity_subject", subject) })
	group := router.Group("/api/v1/platform")
	group.Use(identity.RequireCSRF(identity.CookiePolicy{}))
	NewHandler(s).Register(group)
	call := func(method, path, revision, body string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/platform/target-credentials"+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", revision)
		if csrf {
			req.Header.Set("Cookie", "aig_csrf=test")
			req.Header.Set("X-CSRF-Token", "test")
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		return res
	}
	body := `{"name":"Inference","origin":"https://inference.example.com","auth_type":"bearer","secret":"private-sentinel"}`
	require.Equal(t, 403, call("POST", "", "", body, false).Code)
	created := call("POST", "", "", body, true)
	require.Equal(t, 201, created.Code, created.Body.String())
	require.NotContains(t, created.Body.String(), "private-sentinel")
	var view View
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &view))
	require.Equal(t, 428, call("PUT", "/"+view.ID, "", body, true).Code)
	require.Equal(t, 409, call("PUT", "/"+view.ID, `"2"`, body, true).Code)
	require.Equal(t, 400, call("POST", "", "", `{"name":"x","owner_user_id":"forged"}`, true).Code)
	require.Equal(t, 400, call("POST", "", "", body+body, true).Code)
	listed := call("GET", "", "", "", true)
	require.Equal(t, "no-store", listed.Header().Get("Cache-Control"))
	require.NotContains(t, listed.Body.String(), "private-sentinel")
	httpBody := `{"name":"Internal","origin":"http://inference.internal:8080","auth_type":"bearer","secret":"fictional-http-token","allow_insecure_http":true}`
	httpCreated := call("POST", "", "", httpBody, true)
	require.Equal(t, 201, httpCreated.Code, httpCreated.Body.String())
	require.Contains(t, httpCreated.Body.String(), `"allow_insecure_http":true`)
	require.NotContains(t, httpCreated.Body.String(), "fictional-http-token")
	require.Equal(t, 201, call("POST", "", "", `{"name":"Internal","origin":"http://inference.internal","auth_type":"bearer","secret":"fictional-http-token"}`, true).Code)
	subject.Role = identity.RoleAuditor
	require.Equal(t, 403, call("GET", "", "", "", true).Code)
}

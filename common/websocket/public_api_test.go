package websocket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	platformbrand "github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicBrandRouteAllowsAnonymousAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPublicRoutes(router.Group("/api/v1"), platformbrand.NewService(platformbrand.NewMemoryRepository()))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/brand", nil))
	require.Equal(t, http.StatusOK, response.Code)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	assert.ElementsMatch(t, []string{"product_name", "primary_color", "logo_data_url"}, websocketMapKeys(fields))
}

func TestSafeVersionRouteAllowsAnonymousAccessWithExactWhitelist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerPublicRoutes(router.Group("/api/v1"), platformbrand.NewService(platformbrand.NewMemoryRepository()))

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	require.Equal(t, http.StatusOK, response.Code)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	assert.ElementsMatch(t, []string{"version", "commit", "build_time"}, websocketMapKeys(fields))
}

func TestPublicRoutesKeepVersionAnonymousBesideProtectedSibling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	registerPublicRoutes(v1, platformbrand.NewService(platformbrand.NewMemoryRepository()))

	policy := identity.CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf"}
	protected := v1.Group("/protected")
	protected.Use(
		setupIdentityMiddleware(identity.NewService(identity.NewMemoryRepository()), policy),
		identity.RequirePasswordChangeCompleted(),
		identity.RequireCSRF(policy),
	)
	protected.GET("/probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	publicResponse := httptest.NewRecorder()
	router.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/api/v1/version", nil))
	require.Equal(t, http.StatusOK, publicResponse.Code)

	protectedResponse := httptest.NewRecorder()
	router.ServeHTTP(protectedResponse, httptest.NewRequest(http.MethodGet, "/api/v1/protected/probe", nil))
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, protectedResponse.Code)
}

func websocketMapKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	return keys
}

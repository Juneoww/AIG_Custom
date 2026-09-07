package mcpegress

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandlerRegistersGatewayBehindInternalAgentMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	internal := router.Group("/api/internal")
	handler := NewHandler(NewProxy(ProxyDependencies{}))
	handler.Register(internal, func(context *gin.Context) {
		if context.GetHeader("X-Internal-Agent-Token") != "controlled-agent-token" {
			context.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		context.Next()
	})

	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/internal/mcp-egress/handler-task", nil)
	unauthenticated.Header.Set(CapabilityHeader, "handler-capability")
	unauthenticatedResponse := httptest.NewRecorder()
	router.ServeHTTP(unauthenticatedResponse, unauthenticated)
	assert.Equal(t, http.StatusUnauthorized, unauthenticatedResponse.Code)

	authenticated := httptest.NewRequest(http.MethodPost, "/api/internal/mcp-egress/handler-task", nil)
	authenticated.Header.Set("X-Internal-Agent-Token", "controlled-agent-token")
	authenticated.Header.Set(CapabilityHeader, "handler-capability")
	authenticatedResponse := httptest.NewRecorder()
	router.ServeHTTP(authenticatedResponse, authenticated)
	assert.Equal(t, http.StatusForbidden, authenticatedResponse.Code, "the route reaches the gateway only after internal authentication")

	session := httptest.NewRequest(http.MethodPost, "/api/internal/mcp-egress/handler-task/sessions/opaque-session", nil)
	sessionResponse := httptest.NewRecorder()
	router.ServeHTTP(sessionResponse, session)
	assert.Equal(t, http.StatusUnauthorized, sessionResponse.Code, "legacy SSE message routes require internal Agent authentication")
}

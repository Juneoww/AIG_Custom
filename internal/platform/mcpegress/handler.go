package mcpegress

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Handler provides the HTTP adapter for the internal-only MCP gateway. It has
// no browser identity path: callers must pass AgentManager.RequireInternalToken
// when registering the route.
type Handler struct{ proxy *Proxy }

func NewHandler(proxy *Proxy) *Handler { return &Handler{proxy: proxy} }

// Register mounts only the MCP protocol methods under the supplied internal
// Agent authentication middleware. The URL is intentionally outside browser
// platform routes and uses a task ID only as an opaque capability scope.
func (handler *Handler) Register(group *gin.RouterGroup, requireInternalAgent gin.HandlerFunc) {
	if handler == nil || handler.proxy == nil || group == nil || requireInternalAgent == nil {
		return
	}
	protected := group.Group("")
	protected.Use(requireInternalAgent)
	protected.GET("/mcp-egress/:taskID", handler.serve)
	protected.POST("/mcp-egress/:taskID", handler.serve)
	protected.DELETE("/mcp-egress/:taskID", handler.serve)
	protected.POST("/mcp-egress/:taskID/sessions/:sessionID", handler.serve)
}

func (handler *Handler) serve(context *gin.Context) {
	if handler == nil || handler.proxy == nil {
		context.AbortWithStatus(http.StatusForbidden)
		return
	}
	handler.proxy.ServeHTTP(context.Writer, context.Request, context.Param("taskID"))
}

package websocket

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// newWebServerRouter 对非 MCP 请求保留 Gin 原有访问日志和恢复行为；
// 内部 MCP 通道只记录固定路由模板、状态和耗时，不转储 URL、Header 或 panic 值。
func newWebServerRouter(output, errorOutput io.Writer) *gin.Engine {
	router := gin.New()
	// Gin 的 Debug 重定向日志在中间件之前输出原始路径；API 只接受精确路由。
	router.RedirectTrailingSlash = false
	sensitive := func(c *gin.Context) bool { return strings.HasPrefix(c.Request.URL.Path, "/api/internal/mcp-") }
	router.Use(gin.LoggerWithConfig(gin.LoggerConfig{Output: output, Skip: sensitive}))
	recovery := gin.RecoveryWithWriter(errorOutput)
	router.Use(func(c *gin.Context) {
		if !sensitive(c) {
			recovery(c)
			return
		}
		started := time.Now()
		route := "/api/internal/mcp-[redacted]"
		if strings.HasPrefix(c.Request.URL.Path, "/api/internal/mcp-archives/") {
			route = "/api/internal/mcp-archives/:sessionID/:archiveID"
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/internal/mcp-egress/") {
			route = "/api/internal/mcp-egress/:taskID"
		}
		defer func() {
			if recover() != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
			}
			fmt.Fprintf(output, "[GIN-MCP] %d | %s | %s\n", c.Writer.Status(), time.Since(started), route)
		}()
		c.Next()
	})
	return router
}

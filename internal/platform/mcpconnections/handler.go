package mcpconnections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service     *Service
	idempotency *idempotency.Service
	audits      audit.Recorder
}

func NewHandler(service *Service, replay *idempotency.Service, audits audit.Recorder) *Handler {
	return &Handler{service: service, idempotency: replay, audits: audits}
}

// Register 只能挂在经过身份、强制改密和 CSRF 防护的 platform 分组。
func (handler *Handler) Register(group *gin.RouterGroup) {
	group.GET("/mcp-connection-configs", handler.list)
	group.POST("/mcp-connection-configs", handler.create)
	group.GET("/mcp-connection-configs/:configID", handler.detail)
	group.PATCH("/mcp-connection-configs/:configID", handler.update)
	group.POST("/mcp-connection-configs/:configID/test", handler.test)
	group.GET("/mcp-connection-options", handler.options)
}
func connectionSubject(c *gin.Context, write bool) (identity.Subject, bool) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.AbortWithStatus(http.StatusUnauthorized)
		return subject, false
	}
	if write && !canUseForTask(subject) {
		c.AbortWithStatus(http.StatusForbidden)
		return subject, false
	}
	return subject, true
}
func (handler *Handler) list(c *gin.Context) {
	subject, ok := connectionSubject(c, false)
	if !ok {
		return
	}
	items, err := handler.service.List(c.Request.Context(), subject)
	if err != nil {
		connectionError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (handler *Handler) detail(c *gin.Context) {
	subject, ok := connectionSubject(c, false)
	if !ok {
		return
	}
	detail, err := handler.service.GetEditableDetail(c.Request.Context(), subject, c.Param("configID"))
	if err != nil {
		connectionError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", strconv.Quote(detail.ResourceRevision))
	c.JSON(http.StatusOK, detail)
}
func (handler *Handler) options(c *gin.Context) {
	subject, ok := connectionSubject(c, true)
	if !ok {
		return
	}
	items, err := handler.service.TaskOptions(c.Request.Context(), subject)
	if err != nil {
		connectionError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func connectionBody(c *gin.Context, destination any) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || idempotency.DecodeStrict(raw, destination) != nil {
		return nil, ErrInvalid
	}
	return raw, nil
}
func connectionRevision(c *gin.Context) (string, bool) {
	value, err := strconv.Unquote(c.GetHeader("If-Match"))
	number, numberErr := strconv.ParseUint(value, 10, 64)
	if err != nil || numberErr != nil || number == 0 || strconv.FormatUint(number, 10) != value {
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": "需要当前配置版本", "code": "MCP_REVISION_REQUIRED"})
		return "", false
	}
	return value, true
}
func (handler *Handler) create(c *gin.Context) {
	subject, ok := connectionSubject(c, true)
	if !ok {
		return
	}
	// scope 不在浏览器 DTO 中；个人归属完全由认证主体决定。
	var input struct {
		Name           string         `json:"name"`
		Description    string         `json:"description"`
		ServerURL      string         `json:"server_url"`
		Transport      Transport      `json:"transport"`
		Authentication Authentication `json:"authentication"`
		Headers        []Header       `json:"headers,omitempty"`
	}
	raw, err := connectionBody(c, &input)
	if err != nil {
		connectionError(c, err)
		return
	}
	handler.mutate(c, subject, "", raw, "", false, func(ctx context.Context, resourceID string) (*ConnectionSummary, string, error) {
		summary, err := handler.service.create(ctx, subject, CreateConnectionInput{Name: input.Name, Description: input.Description, Scope: ScopePrivate, ServerURL: input.ServerURL, Transport: input.Transport, Authentication: input.Authentication, Headers: input.Headers}, resourceID)
		return summary, "created", err
	})
}
func (handler *Handler) update(c *gin.Context) {
	subject, ok := connectionSubject(c, true)
	if !ok {
		return
	}
	revision, ok := connectionRevision(c)
	if !ok {
		return
	}
	var patch UpdateConnectionInput
	raw, err := connectionBody(c, &patch)
	if err != nil {
		connectionError(c, err)
		return
	}
	handler.mutate(c, subject, c.Param("configID"), raw, revision, false, func(ctx context.Context, resourceID string) (*ConnectionSummary, string, error) {
		summary, err := handler.service.Update(ctx, subject, c.Param("configID"), revision, patch)
		status := "updated"
		if patch.Enabled != nil {
			status = "disabled"
			if *patch.Enabled {
				status = "enabled"
			}
		}
		return summary, status, err
	})
}
func (handler *Handler) test(c *gin.Context) {
	subject, ok := connectionSubject(c, true)
	if !ok {
		return
	}
	revision, ok := connectionRevision(c)
	if !ok {
		return
	}
	var input struct{}
	raw, err := connectionBody(c, &input)
	if err != nil {
		connectionError(c, err)
		return
	}
	handler.mutate(c, subject, c.Param("configID"), raw, revision, true, nil)
}

// mutate 仅持久化窄响应和载荷哈希。探测先持久化禁用/attempt，再在事务外进行
// 受控网络操作；业务事务不跨越网络等待，完成审计和可重放结果仍原子提交。
func (handler *Handler) mutate(c *gin.Context, subject identity.Subject, id string, raw []byte, revision string, probe bool, apply func(context.Context, string) (*ConnectionSummary, string, error)) {
	scope := idempotency.ScopePrivate
	if id != "" {
		config, _, err := handler.service.visibleCurrentVersion(c.Request.Context(), subject, id)
		if err != nil {
			connectionError(c, err)
			return
		}
		if !canManage(subject, config) {
			connectionError(c, ErrForbidden)
			return
		}
		if config.Scope == ScopeGlobal {
			scope = idempotency.ScopeGlobal
		}
	}
	payload, _ := json.Marshal(struct {
		Body     json.RawMessage `json:"body"`
		Revision string          `json:"revision"`
	}{Body: raw, Revision: revision})
	operation := idempotency.Operation{Scope: scope, Method: c.Request.Method, Path: c.Request.URL.Path, Key: c.GetHeader("Idempotency-Key"), Payload: payload}
	result, err := handler.idempotency.Execute(c.Request.Context(), subject, operation, func(ctx context.Context, claim *idempotency.Claim) error {
		resourceID := id
		if resourceID == "" {
			resourceID = handler.service.newID()
		}
		mutation, err := audit.BeginMutation(ctx, handler.audits, subject, audit.EventInput{Action: audit.Action("mcp_connection.changed"), ResourceType: "mcp_connection", ResourceID: resourceID})
		if err != nil {
			return err
		}
		var summary *ConnectionSummary
		var status string
		var prepared *preparedProbe
		if probe {
			prepared, err = handler.service.prepareProbe(ctx, subject, resourceID, revision)
			if err != nil {
				_ = mutation.Failed(ctx, resourceID, nil)
				return err
			}
		}
		return mutation.Run(ctx, resourceID, nil, func(tx context.Context) error {
			if probe {
				summary, err = handler.service.completePreparedProbe(tx, prepared)
				if err != nil {
					return err
				}
				status = string(prepared.status)
			} else {
				summary, status, err = apply(tx, resourceID)
				if err != nil {
					return err
				}
			}
			response := idempotency.SafeResponse{ID: summary.ID, CurrentVersion: summary.CurrentVersion, ResourceRevision: summary.ResourceRevision, Status: status}
			code := http.StatusOK
			if id == "" {
				code = http.StatusCreated
			}
			return claim.PersistSuccess(tx, code, response)
		})
	})
	if err != nil {
		connectionError(c, err)
		return
	}
	code := result.StatusCode
	if result.Replay {
		code = http.StatusOK
		c.Header("Idempotent-Replay", "true")
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(code, result.Response)
}
func connectionError(c *gin.Context, err error) {
	status, code, message := http.StatusInternalServerError, "MCP_REQUEST_FAILED", "MCP 配置操作失败"
	switch {
	case errors.Is(err, ErrNotFound):
		status, code, message = 404, "MCP_CONNECTION_NOT_FOUND", "连接配置不存在或不可访问"
	case errors.Is(err, ErrForbidden):
		status, code, message = 403, "MCP_FORBIDDEN", "无权执行此操作"
	case errors.Is(err, idempotency.ErrKeyReused):
		status, code, message = 409, "IDEMPOTENCY_KEY_REUSED", "相同幂等键已用于其他请求"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrTaskConnectionUnavailable):
		status, code, message = 409, "MCP_CONNECTION_VERSION_CONFLICT", "连接配置已变更或当前不可用，请刷新后重试"
	case errors.Is(err, ErrProbeRateLimited):
		status, code, message = 429, "MCP_PROBE_RATE_LIMITED", "测试过于频繁，请稍后重试"
	case errors.Is(err, ErrControlledEgressRequired):
		status, code, message = 503, "MCP_EGRESS_UNAVAILABLE", "尚未配置受控出站能力"
	case errors.Is(err, ErrOutboundDenied):
		status, code, message = 400, "MCP_OUTBOUND_DENIED", "服务地址未通过出站策略校验"
	case errors.Is(err, ErrInvalid), errors.Is(err, idempotency.ErrInvalid), errors.Is(err, idempotency.ErrInvalidScope):
		status, code, message = 400, "MCP_INVALID_REQUEST", "MCP 请求参数无效"
	}
	c.JSON(status, gin.H{"error": message, "code": code})
}

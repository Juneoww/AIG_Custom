package mcpscans

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service     *Service
	attachments *AttachmentAdapter
}

func NewHandler(service *Service, attachments *AttachmentAdapter) *Handler {
	return &Handler{service: service, attachments: attachments}
}
func (handler *Handler) Register(group *gin.RouterGroup) {
	group.GET("/mcp-scans", handler.list)
	group.POST("/mcp-scans", handler.create)
	group.GET("/mcp-scans/:taskID", handler.get)
	group.POST("/mcp-scans/:taskID/cancel", handler.cancel)
	if handler.attachments != nil {
		handler.attachments.Register(group)
	}
}
func scanSubject(c *gin.Context, write bool) (identity.Subject, bool) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.AbortWithStatus(401)
		return subject, false
	}
	if write && subject.Role != identity.RoleAdmin && subject.Role != identity.RoleUser {
		c.AbortWithStatus(403)
		return subject, false
	}
	return subject, true
}
func scanBody(c *gin.Context, destination any) ([]byte, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || idempotency.DecodeStrict(raw, destination) != nil {
		return nil, ErrInvalidCreate
	}
	return raw, nil
}
func (handler *Handler) create(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	var request struct {
		SourceKind              SourceKind `json:"source_kind"`
		RepositoryURL           string     `json:"repository_url,omitempty"`
		AttachmentIDs           []string   `json:"attachment_ids,omitempty"`
		ConnectionConfigID      string     `json:"connection_config_id,omitempty"`
		ConnectionConfigVersion int        `json:"connection_config_version,omitempty"`
		AuthorizationConfirmed  bool       `json:"authorization_confirmed,omitempty"`
		ModelID                 string     `json:"model_id,omitempty"`
		Thread                  *int       `json:"thread,omitempty"`
	}
	raw, err := scanBody(c, &request)
	if err != nil {
		scanError(c, err)
		return
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	prohibited := []string{"repository_url", "attachment_ids"}
	if request.SourceKind == SourceKindRepository {
		prohibited = []string{"connection_config_id", "connection_config_version", "authorization_confirmed"}
	}
	for _, key := range prohibited {
		if _, exists := fields[key]; exists {
			scanError(c, ErrInvalidCreate)
			return
		}
	}
	if request.Thread == nil {
		value := 4
		request.Thread = &value
	}
	result, err := handler.service.Create(c.Request.Context(), subject, CreateInput{IdempotencyKey: c.GetHeader("Idempotency-Key"), SourceKind: request.SourceKind, RepositoryURL: request.RepositoryURL, AttachmentIDs: request.AttachmentIDs, ConnectionConfigID: request.ConnectionConfigID, ConnectionConfigVersion: request.ConnectionConfigVersion, AuthorizationConfirmed: request.AuthorizationConfirmed, ModelID: request.ModelID, Thread: request.Thread})
	if err != nil {
		scanError(c, err)
		return
	}
	code := http.StatusAccepted
	if result.Replay {
		code = http.StatusOK
		c.Header("Idempotent-Replay", "true")
	}
	c.JSON(code, result)
}
func (handler *Handler) list(c *gin.Context) {
	subject, ok := scanSubject(c, false)
	if !ok {
		return
	}
	page, size := 1, 20
	for name, target := range map[string]*int{"page": &page, "page_size": &size} {
		if values, exists := c.GetQueryArray(name); exists {
			if len(values) != 1 {
				scanError(c, ErrInvalidCreate)
				return
			}
			value, err := strconv.Atoi(values[0])
			if err != nil {
				scanError(c, ErrInvalidCreate)
				return
			}
			*target = value
		}
	}
	result, err := handler.service.List(c.Request.Context(), subject, page, size, tasks.Status(c.Query("status")))
	if err != nil {
		scanError(c, err)
		return
	}
	c.JSON(200, result)
}
func (handler *Handler) get(c *gin.Context) {
	subject, ok := scanSubject(c, false)
	if !ok {
		return
	}
	result, err := handler.service.Get(c.Request.Context(), subject, c.Param("taskID"))
	if err != nil {
		scanError(c, err)
		return
	}
	c.JSON(200, result)
}
func (handler *Handler) cancel(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	var input struct{}
	if _, err := scanBody(c, &input); err != nil {
		scanError(c, err)
		return
	}
	result, err := handler.service.Cancel(c.Request.Context(), subject, c.Param("taskID"), c.GetHeader("Idempotency-Key"))
	if err != nil {
		scanError(c, err)
		return
	}
	if result.Replay {
		c.Header("Idempotent-Replay", "true")
	}
	c.JSON(200, result)
}
func scanError(c *gin.Context, err error) {
	status, code, message := 500, "MCP_REQUEST_FAILED", "MCP 扫描操作失败"
	switch {
	case errors.Is(err, tasks.ErrNotFound), errors.Is(err, mcpconnections.ErrNotFound):
		status, code, message = 404, "MCP_NOT_FOUND", "资源不存在或不可访问"
	case errors.Is(err, tasks.ErrForbidden), errors.Is(err, mcpconnections.ErrForbidden):
		status, code, message = 403, "MCP_FORBIDDEN", "无权执行此操作"
	case errors.Is(err, idempotency.ErrKeyReused):
		status, code, message = 409, "IDEMPOTENCY_KEY_REUSED", "相同幂等键已用于其他请求"
	case errors.Is(err, mcpconnections.ErrConflict), errors.Is(err, mcpconnections.ErrTaskConnectionUnavailable):
		status, code, message = 409, "MCP_CONNECTION_VERSION_CONFLICT", "连接配置已变更或当前不可用，请刷新后重试"
	case errors.Is(err, mcpconnections.ErrControlledEgressRequired):
		status, code, message = 503, "MCP_EGRESS_UNAVAILABLE", "尚未配置受控出站能力"
	case errors.Is(err, tasks.ErrAttachmentTooLarge):
		status, code, message = 413, "MCP_ATTACHMENT_TOO_LARGE", "附件超过大小限制"
	case errors.Is(err, ErrInvalidCreate), errors.Is(err, tasks.ErrInvalid), errors.Is(err, tasks.ErrAttachmentNotReady), errors.Is(err, tasks.ErrAttachmentSizeMismatch), errors.Is(err, idempotency.ErrInvalid), errors.Is(err, mcpconnections.ErrOutboundDenied):
		status, code, message = 400, "MCP_INVALID_REQUEST", "MCP 请求参数无效"
	}
	c.JSON(status, gin.H{"error": message, "code": code})
}

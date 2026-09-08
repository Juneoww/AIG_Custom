package targetcredentials

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service} }

// Register 仅注册到已包含身份、强制改密和 CSRF 防护的平台分组。
func (h *Handler) Register(group *gin.RouterGroup) {
	g := group.Group("/target-credentials")
	g.Use(identity.RequireRole(identity.RoleAdmin, identity.RoleUser), func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() })
	g.GET("", h.list)
	g.GET("/:id", h.get)
	g.POST("", h.create)
	g.PUT("/:id", h.update)
	g.DELETE("/:id", h.delete)
}
func subjectOf(c *gin.Context) identity.Subject {
	subject, _ := identity.CurrentSubject(c)
	return subject
}
func failure(c *gin.Context, err error) {
	code := http.StatusInternalServerError
	message := "基础设施凭据暂不可用"
	switch {
	case errors.Is(err, ErrInvalid):
		code = http.StatusBadRequest
		message = "请检查凭据类型、HTTP 或 HTTPS 目标地址和认证内容"
	case errors.Is(err, ErrForbidden):
		code = http.StatusForbidden
		message = "无权访问基础设施凭据"
	case errors.Is(err, ErrNotFound):
		code = http.StatusNotFound
		message = "基础设施凭据不存在"
	case errors.Is(err, ErrConflict):
		code = http.StatusConflict
		message = "凭据已更新，请刷新后重试"
	}
	c.JSON(code, gin.H{"error": message})
}
func bodyOf(c *gin.Context) (Input, error) {
	var input Input
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil || idempotency.DecodeStrict(raw, &input) != nil {
		return input, ErrInvalid
	}
	return input, nil
}
func revisionOf(c *gin.Context) (int64, bool) {
	raw, err := strconv.Unquote(c.GetHeader("If-Match"))
	revision, parseErr := strconv.ParseInt(raw, 10, 64)
	if err != nil || parseErr != nil || revision <= 0 || strconv.FormatInt(revision, 10) != raw {
		c.JSON(http.StatusPreconditionRequired, gin.H{"error": "需要当前凭据版本"})
		return 0, false
	}
	return revision, true
}
func sendView(c *gin.Context, status int, v View) {
	c.Header("ETag", strconv.Quote(strconv.FormatInt(v.Revision, 10)))
	c.JSON(status, v)
}
func (h *Handler) list(c *gin.Context) {
	items, err := h.service.List(c.Request.Context(), subjectOf(c))
	if err != nil {
		failure(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *Handler) get(c *gin.Context) {
	v, err := h.service.Get(c.Request.Context(), subjectOf(c), c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	sendView(c, http.StatusOK, v)
}
func (h *Handler) create(c *gin.Context) {
	input, err := bodyOf(c)
	if err != nil {
		failure(c, err)
		return
	}
	v, err := h.service.Create(c.Request.Context(), subjectOf(c), input)
	if err != nil {
		failure(c, err)
		return
	}
	sendView(c, http.StatusCreated, v)
}
func (h *Handler) update(c *gin.Context) {
	revision, ok := revisionOf(c)
	if !ok {
		return
	}
	input, err := bodyOf(c)
	if err != nil {
		failure(c, err)
		return
	}
	v, err := h.service.Update(c.Request.Context(), subjectOf(c), c.Param("id"), revision, input)
	if err != nil {
		failure(c, err)
		return
	}
	sendView(c, http.StatusOK, v)
}
func (h *Handler) delete(c *gin.Context) {
	revision, ok := revisionOf(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), subjectOf(c), c.Param("id"), revision); err != nil {
		failure(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

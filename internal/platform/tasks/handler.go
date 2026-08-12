package tasks

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service     *Service
	attachments *AttachmentService
}

func NewHandler(service *Service, attachments ...*AttachmentService) *Handler {
	handler := &Handler{service: service}
	if len(attachments) > 0 {
		handler.attachments = attachments[0]
	}
	return handler
}

func (handler *Handler) Register(group *gin.RouterGroup) {
	if handler.attachments != nil {
		group.POST("/attachments", handler.uploadAttachment)
		group.POST("/attachments/chunked", handler.beginChunkedAttachment)
		group.POST("/attachments/:attachmentID/chunks", handler.uploadAttachmentChunk)
		group.POST("/attachments/:attachmentID/merge", handler.mergeAttachment)
		group.GET("/attachments/:attachmentID/download", handler.downloadAttachment)
	}
	group.POST("", handler.create)
	group.GET("", handler.list)
	group.GET("/:taskID", handler.get)
	group.GET("/:taskID/result", handler.result)
	group.POST("/:taskID/cancel", handler.cancel)
}

func (handler *Handler) list(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	views, err := handler.service.List(c.Request.Context(), subject)
	switch {
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.JSON(http.StatusOK, views)
	}
}

func (handler *Handler) uploadAttachment(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, handler.attachments.config.MaxFileBytes+(1<<20))
	header, err := c.FormFile("file")
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	source, err := header.Open()
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	defer source.Close()
	view, err := handler.attachments.Upload(c.Request.Context(), subject, header.Filename, source)
	if err != nil {
		respondAttachmentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

func (handler *Handler) beginChunkedAttachment(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	var request struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	view, err := handler.attachments.BeginChunked(c.Request.Context(), subject, request.Filename, request.Size)
	if err != nil {
		respondAttachmentError(c, err)
		return
	}
	c.JSON(http.StatusCreated, view)
}

func (handler *Handler) uploadAttachmentChunk(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, handler.attachments.config.MaxChunkBytes+(1<<20))
	index, err := strconv.Atoi(c.PostForm("chunk_index"))
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	header, err := c.FormFile("chunk")
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	source, err := header.Open()
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	defer source.Close()
	if err := handler.attachments.UploadChunk(c.Request.Context(), subject, c.Param("attachmentID"), index, source); err != nil {
		respondAttachmentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (handler *Handler) mergeAttachment(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	var request struct {
		TotalChunks int   `json:"total_chunks"`
		FileSize    int64 `json:"file_size"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	view, err := handler.attachments.Merge(c.Request.Context(), subject, c.Param("attachmentID"), request.TotalChunks, request.FileSize)
	if err != nil {
		respondAttachmentError(c, err)
		return
	}
	c.JSON(http.StatusOK, view)
}

func (handler *Handler) downloadAttachment(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	file, filename, size, err := handler.attachments.Open(c.Request.Context(), subject, c.Param("attachmentID"))
	if err != nil {
		respondAttachmentError(c, err)
		return
	}
	defer file.Close()
	encoded := url.QueryEscape(filename)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", encoded))
	c.DataFromReader(http.StatusOK, size, "application/octet-stream", file, nil)
}

func respondAttachmentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		c.Status(http.StatusNotFound)
	case errors.Is(err, ErrAttachmentTooLarge):
		c.Status(http.StatusRequestEntityTooLarge)
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrAttachmentSizeMismatch), errors.Is(err, ErrAttachmentNotReady):
		c.Status(http.StatusBadRequest)
	default:
		c.Status(http.StatusInternalServerError)
	}
}

func (handler *Handler) result(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	result, err := handler.service.Result(c.Request.Context(), subject, c.Param("taskID"))
	switch {
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		c.Status(http.StatusNotFound)
	case errors.Is(err, ErrResultNotReady):
		c.Status(http.StatusConflict)
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.Data(http.StatusOK, "application/json", result)
	}
}

func (handler *Handler) create(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	var input CreateInput
	if c.ShouldBindJSON(&input) != nil {
		c.Status(http.StatusBadRequest)
		return
	}
	input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	view, err := handler.service.Create(c.Request.Context(), subject, input)
	switch {
	case errors.Is(err, ErrInvalid):
		c.Status(http.StatusBadRequest)
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, ErrDispatchFailed):
		c.JSON(http.StatusServiceUnavailable, view)
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.JSON(http.StatusAccepted, view)
	}
}

func (handler *Handler) get(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	view, err := handler.service.Get(c.Request.Context(), subject, c.Param("taskID"))
	switch {
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		c.Status(http.StatusNotFound)
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.JSON(http.StatusOK, view)
	}
}

func (handler *Handler) cancel(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	err := handler.service.Cancel(c.Request.Context(), subject, c.Param("taskID"))
	switch {
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		c.Status(http.StatusNotFound)
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.Status(http.StatusNoContent)
	}
}

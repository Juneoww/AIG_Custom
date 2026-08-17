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

const (
	defaultTaskPageSize = 20
	maxTaskPageSize     = 100
	maxTaskPage         = 1000
)

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
	page, pageSize, err := taskPage(c)
	if err != nil {
		respondTaskError(c, ErrInvalid)
		return
	}
	response, err := handler.service.Browse(c.Request.Context(), subject, page, pageSize)
	switch {
	case errors.Is(err, ErrForbidden):
		respondTaskError(c, err)
	case err != nil:
		respondTaskError(c, err)
	default:
		c.JSON(http.StatusOK, response)
	}
}

func taskPage(c *gin.Context) (int, int, error) {
	page, pageSize := 1, defaultTaskPageSize
	if raw := c.Query("page"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxTaskPage {
			return 0, 0, ErrInvalid
		}
		page = value
	}
	if raw := c.Query("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return 0, 0, ErrInvalid
		}
		if value > maxTaskPageSize {
			value = maxTaskPageSize
		}
		pageSize = value
	}
	return page, pageSize, nil
}

func respondTaskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	case errors.Is(err, ErrInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task request"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "task request failed"})
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
	_, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	c.JSON(http.StatusGone, gin.H{"error": "task result route retired"})
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
	detail := taskDetailOfView(view)
	switch {
	case errors.Is(err, ErrInvalid):
		c.Status(http.StatusBadRequest)
	case errors.Is(err, ErrForbidden):
		c.Status(http.StatusForbidden)
	case errors.Is(err, ErrDispatchFailed):
		c.JSON(http.StatusServiceUnavailable, TaskCreateErrorResponse{Error: "task dispatch unavailable", Task: detail})
	case err != nil:
		c.Status(http.StatusInternalServerError)
	default:
		c.JSON(http.StatusAccepted, detail)
	}
}

func (handler *Handler) get(c *gin.Context) {
	subject, ok := identity.CurrentSubject(c)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}
	detail, err := handler.service.BrowserGet(c.Request.Context(), subject, c.Param("taskID"))
	switch {
	case errors.Is(err, ErrForbidden):
		respondTaskError(c, err)
	case errors.Is(err, ErrNotFound):
		respondTaskError(c, err)
	case err != nil:
		respondTaskError(c, err)
	default:
		c.JSON(http.StatusOK, detail)
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

package mcpscans

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/gin-gonic/gin"
)

type AttachmentAdapter struct {
	service *tasks.AttachmentService
	replay  *idempotency.Service
}
type AttachmentResponse struct {
	ID            string                `json:"id"`
	State         tasks.AttachmentState `json:"state"`
	Size          int64                 `json:"size"`
	MaxFileBytes  int64                 `json:"max_file_bytes"`
	MaxChunkBytes int64                 `json:"max_chunk_bytes"`
}

func NewAttachmentAdapter(service *tasks.AttachmentService, replay *idempotency.Service) (*AttachmentAdapter, error) {
	if service == nil || !service.MCPOnly() || replay == nil {
		return nil, ErrInvalidCreate
	}
	return &AttachmentAdapter{service: service, replay: replay}, nil
}
func (adapter *AttachmentAdapter) Register(group *gin.RouterGroup) {
	group.POST("/mcp-scan-attachments", adapter.upload)
	group.POST("/mcp-scan-attachments/chunked", adapter.begin)
	group.POST("/mcp-scan-attachments/:attachmentID/chunks", adapter.chunk)
	group.POST("/mcp-scan-attachments/:attachmentID/merge", adapter.merge)
	group.DELETE("/mcp-scan-attachments/:attachmentID", adapter.abort)
}
func (adapter *AttachmentAdapter) response(id string, state tasks.AttachmentState, size int64) AttachmentResponse {
	return AttachmentResponse{ID: id, State: state, Size: size, MaxFileBytes: adapter.service.MaxFileBytes(), MaxChunkBytes: adapter.service.MaxChunkBytes()}
}
func (adapter *AttachmentAdapter) upload(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, adapter.service.MaxFileBytes()+(1<<20))
	header, err := c.FormFile("file")
	if err != nil {
		scanError(c, ErrInvalidCreate)
		return
	}
	source, err := header.Open()
	if err != nil {
		scanError(c, ErrInvalidCreate)
		return
	}
	defer source.Close()
	view, err := adapter.service.Upload(c.Request.Context(), subject, header.Filename, source)
	if err != nil {
		scanError(c, err)
		return
	}
	c.JSON(201, adapter.response(view.ID, view.State, view.Size))
}
func (adapter *AttachmentAdapter) begin(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	var input struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if _, err := scanBody(c, &input); err != nil {
		scanError(c, err)
		return
	}
	view, err := adapter.service.BeginChunked(c.Request.Context(), subject, input.Filename, input.Size)
	if err != nil {
		scanError(c, err)
		return
	}
	c.JSON(201, adapter.response(view.ID, view.State, view.Size))
}
func (adapter *AttachmentAdapter) chunk(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, adapter.service.MaxChunkBytes()+(1<<20))
	index, err := strconv.Atoi(c.PostForm("chunk_index"))
	if err != nil || index < 0 {
		scanError(c, ErrInvalidCreate)
		return
	}
	header, err := c.FormFile("chunk")
	if err != nil {
		scanError(c, ErrInvalidCreate)
		return
	}
	source, err := header.Open()
	if err != nil {
		scanError(c, ErrInvalidCreate)
		return
	}
	defer source.Close()
	body, err := io.ReadAll(io.LimitReader(source, adapter.service.MaxChunkBytes()+1))
	if err != nil || len(body) == 0 {
		scanError(c, ErrInvalidCreate)
		return
	}
	if int64(len(body)) > adapter.service.MaxChunkBytes() {
		scanError(c, tasks.ErrAttachmentTooLarge)
		return
	}
	digest := sha256.Sum256(body)
	payload, _ := json.Marshal(struct {
		Index  int    `json:"index"`
		Digest string `json:"digest"`
	}{index, hex.EncodeToString(digest[:])})
	id := c.Param("attachmentID")
	result, err := adapter.replay.Execute(c.Request.Context(), subject, idempotency.Operation{Scope: idempotency.ScopePrivate, Method: "POST", Path: c.Request.URL.Path, Key: "chunk-" + strconv.Itoa(index), Payload: payload}, func(ctx context.Context, claim *idempotency.Claim) error {
		return adapter.service.UploadMCPChunk(ctx, subject, id, index, bytes.NewReader(body), func(tx context.Context, size int64) error {
			return claim.PersistSuccess(tx, 204, idempotency.SafeResponse{AttachmentID: id, Size: size, Status: "chunk_uploaded"})
		})
	})
	if err != nil {
		scanError(c, err)
		return
	}
	if result.Replay {
		c.Header("Idempotent-Replay", "true")
	}
	c.Status(204)
}
func (adapter *AttachmentAdapter) merge(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	var input struct {
		TotalChunks int   `json:"total_chunks"`
		FileSize    int64 `json:"file_size"`
	}
	raw, err := scanBody(c, &input)
	if err != nil {
		scanError(c, err)
		return
	}
	id := c.Param("attachmentID")
	result, err := adapter.replay.Execute(c.Request.Context(), subject, idempotency.Operation{Scope: idempotency.ScopePrivate, Method: "POST", Path: c.Request.URL.Path, Key: c.GetHeader("Idempotency-Key"), Payload: raw}, func(ctx context.Context, claim *idempotency.Claim) error {
		_, err := adapter.service.MergeMCP(ctx, subject, id, input.TotalChunks, input.FileSize, func(tx context.Context, size int64) error {
			return claim.PersistSuccess(tx, 200, idempotency.SafeResponse{AttachmentID: id, Status: "ready", Size: size})
		})
		return err
	})
	if err != nil {
		scanError(c, err)
		return
	}
	if result.Replay {
		c.Header("Idempotent-Replay", "true")
	}
	c.JSON(200, adapter.response(result.Response.AttachmentID, tasks.AttachmentStateReady, result.Response.Size))
}
func (adapter *AttachmentAdapter) abort(c *gin.Context) {
	subject, ok := scanSubject(c, true)
	if !ok {
		return
	}
	if err := adapter.service.Abort(c.Request.Context(), subject, c.Param("attachmentID")); err != nil {
		scanError(c, err)
		return
	}
	c.Status(204)
}

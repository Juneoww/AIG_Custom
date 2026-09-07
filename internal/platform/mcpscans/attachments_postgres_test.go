package mcpscans

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/idempotency"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestMCPAttachmentHTTPReplayKeepsCommittedBytes 验证真实 PostgreSQL 事务下，
// HTTP 分片和合并重放不会重复计数，同键不同内容也不会覆盖已经提交的附件。
func TestMCPAttachmentHTTPReplayKeepsCommittedBytes(t *testing.T) {
	db := openMCPScanPostgresDB(t)
	require.NoError(t, database.Migrate(db))
	repository := tasks.NewGormRepository(db)
	replayRepository := idempotency.NewGormRepository(db)
	auditRepository := audit.NewGormRepository(db)
	require.NoError(t, repository.Init())
	require.NoError(t, replayRepository.Init())
	require.NoError(t, auditRepository.Init())
	attachments, err := tasks.NewAttachmentService(repository, tasks.AttachmentConfig{MCPOnly: true, UploadDir: t.TempDir(), MaxFileBytes: 32, MaxChunkBytes: 8}, audit.NewService(auditRepository))
	require.NoError(t, err)
	adapter, err := NewAttachmentAdapter(attachments, idempotency.NewService(replayRepository))
	require.NoError(t, err)
	owner := identity.Subject{UserID: "mcp-http-owner", Username: "mcp-http-owner", Role: identity.RoleUser}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("identity_subject", owner) })
	adapter.Register(router.Group("/api/v1/platform"))
	upload, err := attachments.BeginChunked(context.Background(), owner, "private-source.py", 3)
	require.NoError(t, err)
	path := "/api/v1/platform/mcp-scan-attachments/" + upload.ID
	chunk := func(contents string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		require.NoError(t, writer.WriteField("chunk_index", "0"))
		file, err := writer.CreateFormFile("chunk", "private-source.py")
		require.NoError(t, err)
		_, err = file.Write([]byte(contents))
		require.NoError(t, err)
		require.NoError(t, writer.Close())
		request := httptest.NewRequest(http.MethodPost, path+"/chunks", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	require.Equal(t, 204, chunk("abc").Code)
	replayed := chunk("abc")
	require.Equal(t, 204, replayed.Code, replayed.Body.String())
	require.Equal(t, "true", replayed.Header().Get("Idempotent-Replay"))
	require.Equal(t, 409, chunk("def").Code)
	stored, err := repository.GetAttachment(context.Background(), upload.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, stored.Size)
	merge := func(payload string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path+"/merge", strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "mcp-http-merge")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	payload := `{"total_chunks":1,"file_size":3}`
	first := merge(payload)
	require.Equal(t, 200, first.Code, first.Body.String())
	second := merge(payload)
	require.Equal(t, 200, second.Code, second.Body.String())
	require.Equal(t, first.Body.String(), second.Body.String())
	require.Equal(t, "true", second.Header().Get("Idempotent-Replay"))
	require.Equal(t, 409, merge(`{"total_chunks":2,"file_size":3}`).Code)
	var safe AttachmentResponse
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &safe))
	require.Equal(t, tasks.AttachmentStateReady, safe.State)
	require.NotContains(t, second.Body.String(), "private-source")
	stored, err = repository.GetAttachment(context.Background(), upload.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, stored.Size)
	require.Equal(t, tasks.AttachmentStateReady, stored.State)
}

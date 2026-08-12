package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestRetiredBrowserTaskRoutesUseProductionIdentityPasswordAndCSRFChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	identityService := identity.NewService(identity.NewMemoryRepository())
	user, err := identityService.CreateUser(context.Background(), identity.CreateUserInput{
		ID: "legacy-user-id", Username: "legacy-user", Password: "temporary-secret", Role: identity.RoleUser,
		MustChangePassword: true,
	})
	require.NoError(t, err)
	initialLogin, err := identityService.Authenticate(context.Background(), "legacy-user", "temporary-secret")
	require.NoError(t, err)
	policy := identity.CookiePolicy{SessionCookieName: "aig_session", CSRFCookieName: "aig_csrf"}

	router := gin.New()
	app := router.Group("/api/v1/app")
	app.Use(
		setupIdentityMiddleware(identityService, policy),
		identity.RequirePasswordChangeCompleted(),
		identity.RequireCSRF(policy),
	)
	registerRetiredBrowserTaskRoutes(app)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/uploadFile", nil)
	request.Header.Set("username", "legacy-user")
	request.Header.Set("role", "admin")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	request = httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/uploadFile", nil)
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: initialLogin.Token})
	request.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	request.Header.Set("X-CSRF-Token", "csrf")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code, "must-change sessions are rejected before the retired handler")

	require.NoError(t, identityService.ChangePassword(context.Background(), user.ID, "temporary-secret", "ready-secret"))
	readyLogin, err := identityService.Authenticate(context.Background(), "legacy-user", "ready-secret")
	require.NoError(t, err)

	request = httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/uploadFile", nil)
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: readyLogin.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code, "mutating retired routes retain production CSRF protection")

	request = httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/uploadFile", nil)
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: readyLogin.Token})
	request.AddCookie(&http.Cookie{Name: policy.CSRFCookieName, Value: "csrf"})
	request.Header.Set("X-CSRF-Token", "csrf")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusGone, response.Code)

	request = httptest.NewRequest(http.MethodGet, "/api/v1/app/taskapi/status/browser-engine-id", nil)
	request.AddCookie(&http.Cookie{Name: policy.SessionCookieName, Value: readyLogin.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusGone, response.Code)
}

func TestInternalAgentMiddlewareRejectsBrowserIdentityAndAcceptsOnlyToken(t *testing.T) {
	manager := NewAgentManager(testInternalAgentToken)
	router := gin.New()
	router.GET("/internal", manager.RequireInternalToken(), func(context *gin.Context) {
		context.Status(http.StatusNoContent)
	})

	tests := []struct {
		name   string
		header http.Header
		want   int
	}{
		{name: "missing", header: http.Header{}, want: http.StatusUnauthorized},
		{name: "wrong", header: http.Header{InternalAgentTokenHeader: []string{"wrong"}}, want: http.StatusForbidden},
		{name: "browser headers", header: http.Header{"username": []string{"admin"}, "role": []string{"admin"}, "Cookie": []string{"aig_session=forged"}}, want: http.StatusUnauthorized},
		{name: "internal token", header: http.Header{InternalAgentTokenHeader: []string{testInternalAgentToken}}, want: http.StatusNoContent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/internal", nil)
			request.Header = test.header
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			assert.Equal(t, test.want, response.Code)
		})
	}
}

func TestInternalTaskDownloadRequiresExactSessionAttachment(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	uploadDir := t.TempDir()
	taskManager.fileConfig = &FileUploadConfig{UploadDir: uploadDir}
	require.NoError(t, os.WriteFile(filepath.Join(uploadDir, "allowed.txt"), []byte("private"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(uploadDir, "other.txt"), []byte("other"), 0o600))
	attachments, err := json.Marshal([]string{"allowed.txt"})
	require.NoError(t, err)
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: "download-membership-task", Username: "legacy-agent-owner", TaskType: "mcp_scan",
		Content: "scan", Attachments: datatypes.JSON(attachments), Status: TaskStatusDoing, Share: false,
	}))

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	err = taskManager.DownloadFile("download-membership-task", "other.txt", "", context, "download-test")
	assert.EqualError(t, err, "文件不存在于此任务中")
	err = taskManager.DownloadFile("download-membership-task", "../allowed.txt", "", context, "download-test")
	assert.Error(t, err)

	response := httptest.NewRecorder()
	context, _ = gin.CreateTestContext(response)
	require.NoError(t, taskManager.DownloadFile("download-membership-task", "allowed.txt", "", context, "download-test"))
	assert.Equal(t, "private", response.Body.String())
}

func TestInternalTaskDownloadHandlerDoesNotTrustUsernameHeaders(t *testing.T) {
	taskManager, cleanup := newTestTaskManager(t)
	defer cleanup()
	uploadDir := t.TempDir()
	taskManager.fileConfig = &FileUploadConfig{UploadDir: uploadDir}
	require.NoError(t, os.WriteFile(filepath.Join(uploadDir, "private.txt"), []byte("private"), 0o600))
	attachments, err := json.Marshal([]string{"private.txt"})
	require.NoError(t, err)
	require.NoError(t, taskManager.taskStore.CreateSession(&database.Session{
		ID: "internal-download-handler", Username: "legacy-agent-owner", TaskType: "mcp_scan",
		Content: "scan", Attachments: datatypes.JSON(attachments), Status: TaskStatusDoing, Share: false,
	}))

	manager := NewAgentManager(testInternalAgentToken)
	router := gin.New()
	router.POST("/api/v1/app/tasks/:sessionId/downloadFile", manager.RequireInternalToken(), func(context *gin.Context) {
		HandleInternalTaskDownload(context, taskManager)
	})
	body := bytes.NewBufferString(`{"fileUrl":"private.txt"}`)
	forged := httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/internal-download-handler/downloadFile", body)
	forged.Header.Set("Content-Type", "application/json")
	forged.Header.Set("username", "legacy-agent-owner")
	forged.Header.Set("role", "admin")
	forgedResponse := httptest.NewRecorder()
	router.ServeHTTP(forgedResponse, forged)
	assert.Equal(t, http.StatusUnauthorized, forgedResponse.Code)

	body = bytes.NewBufferString(`{"fileUrl":"private.txt"}`)
	internal := httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/internal-download-handler/downloadFile", body)
	internal.Header.Set("Content-Type", "application/json")
	internal.Header.Set(InternalAgentTokenHeader, testInternalAgentToken)
	internalResponse := httptest.NewRecorder()
	router.ServeHTTP(internalResponse, internal)
	assert.Equal(t, http.StatusOK, internalResponse.Code, internalResponse.Body.String())
	assert.Equal(t, "private", internalResponse.Body.String())
}

func TestInternalTaskUploadRequiresTokenAndExactRunningPlatformTask(t *testing.T) {
	repository := platformtasks.NewMemoryRepository()
	now := time.Now().UTC()
	platformTask := &platformtasks.Task{
		ID: "platform-upload-task", OwnerUserID: "upload-owner", OwnerUsername: "alice",
		IdempotencyKey: "upload-idempotency", EngineSessionID: "platform-upload-task",
		TaskType: "ai_infra_scan", Content: "scan", Params: json.RawMessage(`{}`),
		AttachmentRefs: json.RawMessage(`[]`), Status: platformtasks.StatusRunning,
		CreatedAt: now, UpdatedAt: now,
	}
	_, created, err := repository.CreateOrGet(context.Background(), platformTask)
	require.NoError(t, err)
	require.True(t, created)
	auditRepository := platformaudit.NewMemoryRepository()
	uploadDir := t.TempDir()
	attachments, err := platformtasks.NewAttachmentService(repository, platformtasks.AttachmentConfig{
		UploadDir: uploadDir, MaxFileBytes: 16, MaxChunkBytes: 8,
	}, platformaudit.NewService(auditRepository))
	require.NoError(t, err)

	manager := NewAgentManager(testInternalAgentToken)
	router := gin.New()
	router.POST("/api/v1/app/tasks/:sessionId/uploadFile", manager.RequireInternalToken(), func(context *gin.Context) {
		HandleInternalTaskUpload(context, attachments)
	})
	newUpload := func(sessionID string) *http.Request {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, partErr := writer.CreateFormFile("file", "result.json")
		require.NoError(t, partErr)
		_, partErr = part.Write([]byte("private"))
		require.NoError(t, partErr)
		require.NoError(t, writer.Close())
		request := httptest.NewRequest(http.MethodPost, "/api/v1/app/tasks/"+sessionID+"/uploadFile", &body)
		request.Header.Set("Content-Type", writer.FormDataContentType())
		return request
	}

	forged := newUpload(platformTask.EngineSessionID)
	forged.Header.Set("username", "alice")
	forged.Header.Set("role", "admin")
	forgedResponse := httptest.NewRecorder()
	router.ServeHTTP(forgedResponse, forged)
	assert.Equal(t, http.StatusUnauthorized, forgedResponse.Code)
	events, err := auditRepository.List(context.Background(), platformaudit.Filter{})
	require.NoError(t, err)
	assert.Empty(t, events)

	internal := newUpload(platformTask.EngineSessionID)
	internal.Header.Set(InternalAgentTokenHeader, testInternalAgentToken)
	internalResponse := httptest.NewRecorder()
	router.ServeHTTP(internalResponse, internal)
	require.Equal(t, http.StatusOK, internalResponse.Code, internalResponse.Body.String())
	assert.NotContains(t, internalResponse.Body.String(), uploadDir)
	assert.NotContains(t, strings.ToLower(internalResponse.Body.String()), "storage")
	assert.Contains(t, internalResponse.Body.String(), "/api/v1/platform/tasks/attachments/")

	unknown := newUpload("unknown-platform-task")
	unknown.Header.Set(InternalAgentTokenHeader, testInternalAgentToken)
	unknownResponse := httptest.NewRecorder()
	router.ServeHTTP(unknownResponse, unknown)
	assert.Equal(t, http.StatusNotFound, unknownResponse.Code)

	require.NoError(t, repository.UpdateStatus(context.Background(), platformTask.ID, platformtasks.StatusSucceeded, time.Now().UTC()))
	late := newUpload(platformTask.EngineSessionID)
	late.Header.Set(InternalAgentTokenHeader, testInternalAgentToken)
	lateResponse := httptest.NewRecorder()
	router.ServeHTTP(lateResponse, late)
	assert.Equal(t, http.StatusForbidden, lateResponse.Code)
}

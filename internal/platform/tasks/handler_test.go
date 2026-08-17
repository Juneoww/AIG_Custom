package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectedTaskHandlerRejectsAnonymousAndForgedIdentityHeaders(t *testing.T) {
	router, _, _ := newTaskHandlerFixture(t)

	request := httptest.NewRequest(http.MethodPost, "/tasks", bytes.NewBufferString(`{"task_type":"mcp_scan","content":"scan"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "header-forgery")
	request.Header.Set("username", "admin")
	request.Header.Set("role", "admin")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestProtectedTaskHandlerUsesCookieSubjectAndOwnerUserID(t *testing.T) {
	router, tokens, engine := newTaskHandlerFixture(t)

	created := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "owner-key", map[string]any{
		"task_type": "mcp_scan", "content": "scan", "username": "mallory",
	})
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	var task View
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &task))
	assert.Equal(t, "user-alice", task.OwnerUserID)
	assert.Equal(t, "alice", task.OwnerUsername)
	assert.Equal(t, int64(1), engine.submits.Load())

	forbidden := performTaskJSON(t, router, tokens["bob"], http.MethodGet, "/tasks/"+task.ID, "", nil)
	assert.Equal(t, http.StatusForbidden, forbidden.Code)
	auditorRead := performTaskJSON(t, router, tokens["auditor"], http.MethodGet, "/tasks/"+task.ID, "", nil)
	assert.Equal(t, http.StatusOK, auditorRead.Code)
	auditorCancel := performTaskJSON(t, router, tokens["auditor"], http.MethodPost, "/tasks/"+task.ID+"/cancel", "", nil)
	assert.Equal(t, http.StatusForbidden, auditorCancel.Code)
	adminCancel := performTaskJSON(t, router, tokens["admin"], http.MethodPost, "/tasks/"+task.ID+"/cancel", "", nil)
	assert.Equal(t, http.StatusNoContent, adminCancel.Code)
}

func TestProtectedTaskListRejectsHeadersAndAppliesOwnerRBAC(t *testing.T) {
	router, tokens, _ := newTaskHandlerFixture(t)
	aliceCreated := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "list-alice", map[string]any{"task_type": "mcp_scan", "content": "scan"})
	bobCreated := performTaskJSON(t, router, tokens["bob"], http.MethodPost, "/tasks", "list-bob", map[string]any{"task_type": "mcp_scan", "content": "scan"})
	require.Equal(t, http.StatusAccepted, aliceCreated.Code)
	require.Equal(t, http.StatusAccepted, bobCreated.Code)

	forged := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	forged.Header.Set("username", "admin")
	forged.Header.Set("role", "admin")
	forgedResponse := httptest.NewRecorder()
	router.ServeHTTP(forgedResponse, forged)
	assert.Equal(t, http.StatusUnauthorized, forgedResponse.Code)

	for actor, expected := range map[string]int{"alice": 1, "bob": 1, "auditor": 2, "admin": 2} {
		response := performTaskJSON(t, router, tokens[actor], http.MethodGet, "/tasks", "", nil)
		require.Equal(t, http.StatusOK, response.Code, actor+": "+response.Body.String())
		var tasks TaskListResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &tasks))
		assert.Len(t, tasks.Items, expected)
		assert.Equal(t, expected, tasks.Total)
	}
}

func TestProtectedTaskHandlerRequiresIdempotencyKey(t *testing.T) {
	router, tokens, _ := newTaskHandlerFixture(t)
	response := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "", map[string]any{
		"task_type": "mcp_scan", "content": "scan",
	})
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestAttachmentHandlerReturnsOnlyOpaqueMetadataAndEnforcesOwnerDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	for _, input := range []identity.CreateUserInput{
		{ID: "user-alice", Username: "alice", Password: "secret", Role: identity.RoleUser},
		{ID: "user-bob", Username: "bob", Password: "secret", Role: identity.RoleUser},
	} {
		_, err := identityService.CreateUser(ctx, input)
		require.NoError(t, err)
	}
	alice, err := identityService.Authenticate(ctx, "alice", "secret")
	require.NoError(t, err)
	bob, err := identityService.Authenticate(ctx, "bob", "secret")
	require.NoError(t, err)
	repository := NewMemoryRepository()
	attachmentService, err := NewAttachmentService(repository, AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 16, MaxChunkBytes: 8}, audit.NewService(audit.NewMemoryRepository()))
	require.NoError(t, err)
	taskService := NewService(repository, &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	taskService.SetAttachmentService(attachmentService)
	router := gin.New()
	NewHandler(taskService, attachmentService).Register(router.Group("/tasks", identity.Authenticate(identityService, identity.CookiePolicy{})))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "private.txt")
	require.NoError(t, err)
	_, err = io.WriteString(part, "private")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/tasks/attachments", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: alice.Token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "storage")
	assert.NotContains(t, response.Body.String(), attachmentService.config.UploadDir)
	var attachment AttachmentView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &attachment))

	request = httptest.NewRequest(http.MethodGet, "/tasks/attachments/"+attachment.ID+"/download", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: bob.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func newTaskHandlerFixture(t *testing.T) (http.Handler, map[string]string, *recordingEngine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	users := []identity.CreateUserInput{
		{ID: "user-alice", Username: "alice", Password: "secret", Role: identity.RoleUser},
		{ID: "user-bob", Username: "bob", Password: "secret", Role: identity.RoleUser},
		{ID: "user-auditor", Username: "auditor", Password: "secret", Role: identity.RoleAuditor},
		{ID: "user-admin", Username: "admin", Password: "secret", Role: identity.RoleAdmin},
	}
	tokens := map[string]string{}
	for _, input := range users {
		_, err := identityService.CreateUser(ctx, input)
		require.NoError(t, err)
		login, err := identityService.Authenticate(ctx, input.Username, input.Password)
		require.NoError(t, err)
		tokens[input.Username] = login.Token
	}
	engine := &recordingEngine{}
	service := NewService(NewMemoryRepository(), engine, audit.NewService(audit.NewMemoryRepository()))
	router := gin.New()
	group := router.Group("/tasks", identity.Authenticate(identityService, identity.CookiePolicy{}))
	NewHandler(service).Register(group)
	return router, tokens, engine
}

func performTaskJSON(t *testing.T, router http.Handler, token, method, path, idempotencyKey string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type toggledAttachmentAuditRepository struct {
	*audit.MemoryRepository
	failErr error
}

type taskCreateWireEngine struct {
	recordingEngine
	sessionID string
}

func (engine *taskCreateWireEngine) SubmitTask(ctx context.Context, task EngineTask) (string, error) {
	_, err := engine.recordingEngine.SubmitTask(ctx, task)
	if err != nil {
		return "", err
	}
	return engine.sessionID, nil
}

func (repository *toggledAttachmentAuditRepository) Append(ctx context.Context, event *audit.Event) error {
	if repository.failErr != nil {
		return repository.failErr
	}
	return repository.MemoryRepository.Append(ctx, event)
}

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

func TestProtectedTaskHandlerUsesCookieSubjectAndSafeOwner(t *testing.T) {
	router, tokens, engine := newTaskHandlerFixture(t)

	created := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "owner-key", map[string]any{
		"task_type": "mcp_scan", "content": "scan", "username": "mallory",
	})
	require.Equal(t, http.StatusAccepted, created.Code, created.Body.String())
	var task TaskDetail
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &task))
	assert.Equal(t, "alice", task.Owner)
	assert.Equal(t, int64(1), engine.submits.Load())

	forbidden := performTaskJSON(t, router, tokens["bob"], http.MethodGet, "/tasks/"+task.ID, "", nil)
	assert.Equal(t, http.StatusNotFound, forbidden.Code)
	forbiddenCancel := performTaskJSON(t, router, tokens["bob"], http.MethodPost, "/tasks/"+task.ID+"/cancel", "", nil)
	assert.Equal(t, http.StatusNotFound, forbiddenCancel.Code)
	auditorRead := performTaskJSON(t, router, tokens["auditor"], http.MethodGet, "/tasks/"+task.ID, "", nil)
	assert.Equal(t, http.StatusOK, auditorRead.Code)
	auditorCancel := performTaskJSON(t, router, tokens["auditor"], http.MethodPost, "/tasks/"+task.ID+"/cancel", "", nil)
	assert.Equal(t, http.StatusForbidden, auditorCancel.Code)
	adminCancel := performTaskJSON(t, router, tokens["admin"], http.MethodPost, "/tasks/"+task.ID+"/cancel", "", nil)
	assert.Equal(t, http.StatusNoContent, adminCancel.Code)
}

func TestTaskCreateAcceptedResponseUsesSafeDetailWire(t *testing.T) {
	engine := &taskCreateWireEngine{sessionID: "engine-session-sentinel"}
	router, tokens := newTaskHandlerFixtureWithEngine(t, engine)

	response := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "safe-create-accepted", map[string]any{
		"task_type": "mcp_scan", "content": "content-sentinel", "country_iso_code": "zh",
		"params": map[string]any{"thread": 7},
	})
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())

	assertSafeTaskCreateDetail(t, response.Body.Bytes(), map[string]any{
		"task_type":     "mcp_scan",
		"input_summary": map[string]any{"language": "zh", "thread": float64(7)},
	}, "user-alice", "content-sentinel", "engine-session-sentinel")
	assert.Zero(t, engine.statusReads.Load(), "rendering the response must not read the engine")
}

func TestTaskCreateAcceptedAIInfraResponseUsesSafeModelIDDetailWire(t *testing.T) {
	engine := &taskCreateWireEngine{sessionID: "engine-session-sentinel"}
	router, tokens := newTaskHandlerFixtureWithEngine(t, engine)

	response := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "safe-ai-infra-model-create", map[string]any{
		"task_type": "ai_infra_scan", "content": "first-target\nsecond-target", "country_iso_code": "zh",
		"params": map[string]any{"model_id": "model-opaque-1", "timeout": 300, "port_scan_mode": "fixed_ai"},
	})
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())

	assertSafeTaskCreateDetail(t, response.Body.Bytes(), map[string]any{
		"task_type": "ai_infra_scan",
		"input_summary": map[string]any{
			"language": "zh", "model_id": "model-opaque-1", "timeout": float64(300), "target_count": float64(2), "port_scan_mode": "fixed_ai",
		},
	}, "user-alice", "first-target", "second-target", "engine-session-sentinel")
	assert.Zero(t, engine.statusReads.Load(), "rendering the response must not read the engine")
}

func TestTaskCreateRejectsOversizedJSONWithFixedBadRequest(t *testing.T) {
	router, tokens, engine := newTaskHandlerFixture(t)
	payload := `{"task_type":"mcp_scan","content":"` + strings.Repeat("x", 300<<10) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "oversized-json")
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: tokens["alice"]})
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.JSONEq(t, `{"error":"invalid task request"}`, response.Body.String())
	assert.Zero(t, engine.submits.Load())
}

func TestTaskCreateDispatchFailureUsesFixedErrorAndSafeTaskWire(t *testing.T) {
	engine := &taskCreateWireEngine{}
	engine.err = NewTransientDispatchError(errors.New("dispatch-error-sentinel"))
	router, tokens := newTaskHandlerFixtureWithEngine(t, engine)

	response := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "safe-create-unavailable", map[string]any{
		"task_type": "mcp_scan", "content": "failure-content-sentinel",
		"params": map[string]any{"thread": 4},
	})
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())

	var wire map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
	require.ElementsMatch(t, []string{"error", "task"}, mapKeys(wire))
	assert.Equal(t, "task dispatch unavailable", wire["error"])
	task, ok := wire["task"].(map[string]any)
	require.True(t, ok)
	encoded, err := json.Marshal(task)
	require.NoError(t, err)
	assertSafeTaskCreateDetail(t, encoded, map[string]any{
		"task_type":     "mcp_scan",
		"input_summary": map[string]any{"thread": float64(4)},
	}, "user-alice", "failure-content-sentinel", "dispatch-error-sentinel")
	assert.Zero(t, engine.statusReads.Load(), "rendering the response must not read the engine")
}

func TestTaskCreateMapsUnavailableAttachmentsToSafeBadRequest(t *testing.T) {
	engine := &recordingEngine{}
	router, tokens, attachments := newTaskHandlerFixtureWithAttachments(t, engine)
	notReady, err := attachments.BeginChunked(context.Background(), identity.Subject{
		UserID: "user-alice", Username: "alice", Role: identity.RoleUser,
	}, "private-path-state-sentinel.txt", 7)
	require.NoError(t, err)

	for name, attachmentID := range map[string]string{
		"missing":   "missing-attachment-sentinel",
		"not ready": notReady.ID,
	} {
		t.Run(name, func(t *testing.T) {
			response := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "attachment-"+strings.ReplaceAll(name, " ", "-"), map[string]any{
				"task_type": "mcp_scan", "content": "safe scan", "attachment_ids": []string{attachmentID},
			})
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.JSONEq(t, `{"error":"attachment unavailable"}`, response.Body.String())
			for _, forbidden := range []string{attachmentID, "private-path-state-sentinel.txt", attachments.config.UploadDir, string(AttachmentStateUploading)} {
				assert.NotContains(t, response.Body.String(), forbidden)
			}
		})
	}
	assert.Zero(t, engine.submits.Load(), "unavailable attachments must fail before engine submission")
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
		assert.Equal(t, int64(expected), tasks.Total)
	}
}

func TestProtectedTaskHandlerRequiresIdempotencyKey(t *testing.T) {
	router, tokens, _ := newTaskHandlerFixture(t)
	response := performTaskJSON(t, router, tokens["alice"], http.MethodPost, "/tasks", "", map[string]any{
		"task_type": "mcp_scan", "content": "scan",
	})
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.JSONEq(t, `{"error":"invalid task request"}`, response.Body.String())
}

func TestTaskCreateMalformedJSONReturnsSafeErrorWithoutSideEffects(t *testing.T) {
	router, tokens, engine := newTaskHandlerFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"task_type":"mcp_scan","content":"parse-sentinel"`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "malformed-json")
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: tokens["alice"]})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	assert.JSONEq(t, `{"error":"invalid task request"}`, response.Body.String())
	for _, internal := range []string{"parse-sentinel", "unexpected", "EOF", "syntax", "offset"} {
		assert.NotContains(t, response.Body.String(), internal)
	}
	assert.Zero(t, engine.submits.Load(), "malformed JSON must fail before engine submission")
	assert.Zero(t, engine.statusReads.Load(), "malformed JSON must not read engine status")
	assert.Zero(t, engine.resultReads.Load(), "malformed JSON must not read engine results")

	listed := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks", "", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var tasks TaskListResponse
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &tasks))
	assert.Empty(t, tasks.Items)
	assert.Zero(t, tasks.Total, "malformed JSON must not persist a task")
}

func TestAttachmentHandlerReturnsOnlyOpaqueMetadataAndEnforcesOwnerDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	identityService := identity.NewService(identity.NewMemoryRepository())
	for _, input := range []identity.CreateUserInput{
		{ID: "user-alice", Username: "alice", Password: "secret", Role: identity.RoleUser},
		{ID: "user-bob", Username: "bob", Password: "secret", Role: identity.RoleUser},
		{ID: "user-auditor", Username: "auditor", Password: "secret", Role: identity.RoleAuditor},
		{ID: "user-admin", Username: "admin", Password: "secret", Role: identity.RoleAdmin},
	} {
		_, err := identityService.CreateUser(ctx, input)
		require.NoError(t, err)
	}
	alice, err := identityService.Authenticate(ctx, "alice", "secret")
	require.NoError(t, err)
	bob, err := identityService.Authenticate(ctx, "bob", "secret")
	require.NoError(t, err)
	auditor, err := identityService.Authenticate(ctx, "auditor", "secret")
	require.NoError(t, err)
	admin, err := identityService.Authenticate(ctx, "admin", "secret")
	require.NoError(t, err)
	repository := NewMemoryRepository()
	auditRepository := &toggledAttachmentAuditRepository{MemoryRepository: audit.NewMemoryRepository()}
	auditService := audit.NewService(auditRepository)
	attachmentService, err := NewAttachmentService(repository, AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 16, MaxChunkBytes: 8}, auditService)
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
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: alice.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "private", response.Body.String())

	request = httptest.NewRequest(http.MethodGet, "/tasks/attachments/"+attachment.ID+"/download", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: bob.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNotFound, response.Code)

	for _, attachmentID := range []string{attachment.ID, "missing-attachment"} {
		request = httptest.NewRequest(http.MethodGet, "/tasks/attachments/"+attachmentID+"/download", nil)
		request.AddCookie(&http.Cookie{Name: "aig_session", Value: auditor.Token})
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assert.Equal(t, http.StatusForbidden, response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/tasks/attachments/"+attachment.ID+"/download", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: admin.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "private", response.Body.String())
	events, err := auditRepository.List(ctx, audit.Filter{Action: audit.ActionAttachmentDownloadAuthorized, ResourceID: attachment.ID})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, audit.OutcomeSuccess, events[0].Outcome)

	auditRepository.failErr = errors.New("injected attachment audit append failure")
	request = httptest.NewRequest(http.MethodGet, "/tasks/attachments/"+attachment.ID+"/download", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: admin.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Empty(t, response.Body.String())

	auditRepository.failErr = nil
	attachmentService.openFile = func(string) (*os.File, error) { return nil, os.ErrPermission }
	request = httptest.NewRequest(http.MethodGet, "/tasks/attachments/"+attachment.ID+"/download", nil)
	request.AddCookie(&http.Cookie{Name: "aig_session", Value: admin.Token})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Empty(t, response.Body.String())
	assert.NotContains(t, response.Body.String(), attachmentService.config.UploadDir)
}

func TestAttachmentAbortRouteKeepsOwnerAndRoleBoundary(t *testing.T) {
	router, tokens, attachments := newTaskHandlerFixtureWithAttachments(t, &recordingEngine{})
	owner := identity.Subject{UserID: "user-alice", Username: "alice", Role: identity.RoleUser}
	view, err := attachments.BeginChunked(context.Background(), owner, "partial.txt", 7)
	require.NoError(t, err)

	other := performTaskJSON(t, router, tokens["bob"], http.MethodDelete, "/tasks/attachments/"+view.ID, "", nil)
	assert.Equal(t, http.StatusNotFound, other.Code)
	auditor := performTaskJSON(t, router, tokens["auditor"], http.MethodDelete, "/tasks/attachments/"+view.ID, "", nil)
	assert.Equal(t, http.StatusForbidden, auditor.Code)
	ownerResponse := performTaskJSON(t, router, tokens["alice"], http.MethodDelete, "/tasks/attachments/"+view.ID, "", nil)
	assert.Equal(t, http.StatusNoContent, ownerResponse.Code)
}

func newTaskHandlerFixture(t *testing.T) (http.Handler, map[string]string, *recordingEngine) {
	t.Helper()
	engine := &recordingEngine{}
	router, tokens := newTaskHandlerFixtureWithEngine(t, engine)
	return router, tokens, engine
}

func newTaskHandlerFixtureWithEngine(t *testing.T, engine EngineAdapter) (http.Handler, map[string]string) {
	router, tokens, _ := newTaskHandlerFixtureWithOptions(t, engine, false)
	return router, tokens
}

func newTaskHandlerFixtureWithAttachments(t *testing.T, engine EngineAdapter) (http.Handler, map[string]string, *AttachmentService) {
	return newTaskHandlerFixtureWithOptions(t, engine, true)
}

func newTaskHandlerFixtureWithOptions(t *testing.T, engine EngineAdapter, withAttachments bool) (http.Handler, map[string]string, *AttachmentService) {
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
	repository := NewMemoryRepository()
	auditService := audit.NewService(audit.NewMemoryRepository())
	service := NewService(repository, engine, auditService)
	var attachments *AttachmentService
	if withAttachments {
		var err error
		attachments, err = NewAttachmentService(repository, AttachmentConfig{
			UploadDir: t.TempDir(), MaxFileBytes: 16, MaxChunkBytes: 8,
		}, auditService)
		require.NoError(t, err)
		service.SetAttachmentService(attachments)
	}
	router := gin.New()
	group := router.Group("/tasks", identity.Authenticate(identityService, identity.CookiePolicy{}))
	NewHandler(service, attachments).Register(group)
	return router, tokens, attachments
}

func assertSafeTaskCreateDetail(t *testing.T, encoded []byte, expected map[string]any, sentinels ...string) {
	t.Helper()
	var task map[string]any
	require.NoError(t, json.Unmarshal(encoded, &task))
	require.ElementsMatch(t, []string{"id", "owner", "task_type", "status", "created_at", "updated_at", "input_summary"}, mapKeys(task))
	assert.Equal(t, "alice", task["owner"])
	assert.Equal(t, expected["task_type"], task["task_type"])
	assert.Equal(t, expected["input_summary"], task["input_summary"])
	for _, forbidden := range []string{"owner_user_id", "owner_username", "content", "params", "attachment_ids", "engine_session_id", "dispatch_error", "dispatch_attempts", "country_iso_code"} {
		assert.NotContains(t, task, forbidden)
	}
	body := string(encoded)
	for _, sentinel := range sentinels {
		assert.NotContains(t, body, sentinel)
	}
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

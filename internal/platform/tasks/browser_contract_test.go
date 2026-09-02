package tasks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type taskQueryCaptureLogger struct {
	logger.Interface
	statements []string
}

func (capture *taskQueryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	statement, _ := fc()
	capture.statements = append(capture.statements, strings.ToLower(statement))
	capture.Interface.Trace(ctx, begin, func() (string, int64) { return statement, 0 }, err)
}

func TestTaskBrowserListReturnsSafePagedOwnerScopedContract(t *testing.T) {
	router, tokens, repository, _ := newTaskBrowserFixture(t)
	base := time.Date(2026, 8, 17, 8, 0, 0, 0, time.UTC)
	for index := range 24 {
		ownerID, ownerName := "user-alice", "alice"
		if index%3 == 0 {
			ownerID, ownerName = "user-bob", "bob"
		}
		putBrowserTask(t, repository, Task{
			ID: fmt.Sprintf("task-%02d", index), OwnerUserID: ownerID, OwnerUsername: ownerName,
			IdempotencyKey: fmt.Sprintf("key-%02d", index), EngineSessionID: "engine-secret",
			TaskType: "mcp_scan", Content: "private-content", Params: json.RawMessage(`{"token":"params-secret"}`),
			AttachmentRefs: json.RawMessage(`["attachment-secret"]`), Status: StatusRunning,
			DispatchError: "dispatch-secret", DispatchClaimToken: "claim-secret",
			CreatedAt: base.Add(time.Duration(index/2) * time.Minute), UpdatedAt: base.Add(time.Duration(index) * time.Minute),
		})
	}

	response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks?page=1&page_size=5", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var listed TaskListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	assert.Equal(t, int64(16), listed.Total)
	assert.Equal(t, 1, listed.Page)
	assert.Equal(t, 5, listed.PageSize)
	require.Len(t, listed.Items, 5)
	assert.Equal(t, []string{"task-23", "task-22", "task-20", "task-19", "task-17"}, taskSummaryIDs(listed.Items))

	var wire map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &wire))
	items := wire["items"].([]any)
	require.NotEmpty(t, items)
	assert.ElementsMatch(t, []string{"id", "owner", "task_type", "status", "created_at", "updated_at"}, mapKeys(items[0].(map[string]any)))
	for _, secret := range []string{"user-alice", "engine-secret", "private-content", "params-secret", "attachment-secret", "dispatch-secret", "claim-secret"} {
		assert.NotContains(t, response.Body.String(), secret)
	}

	global := performTaskJSON(t, router, tokens["auditor"], http.MethodGet, "/tasks?page=2&page_size=20", "", nil)
	require.Equal(t, http.StatusOK, global.Code, global.Body.String())
	require.NoError(t, json.Unmarshal(global.Body.Bytes(), &listed))
	assert.Equal(t, int64(24), listed.Total)
	assert.Equal(t, 2, listed.Page)
	assert.Len(t, listed.Items, 4)
}

func TestTaskBrowserPaginationDefaultsCapsAndRejectsInvalidValues(t *testing.T) {
	router, tokens, repository, _ := newTaskBrowserFixture(t)
	now := time.Now().UTC()
	for index := range 105 {
		putBrowserTask(t, repository, Task{
			ID: fmt.Sprintf("page-%03d", index), OwnerUserID: "user-alice", OwnerUsername: "alice",
			IdempotencyKey: fmt.Sprintf("page-key-%03d", index), TaskType: "mcp_scan", Status: StatusPending,
			Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now,
		})
	}

	response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var listed TaskListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	assert.Equal(t, 20, listed.PageSize)
	assert.Len(t, listed.Items, 20)

	response = performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks?page=1&page_size=1000", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	assert.Equal(t, 100, listed.PageSize)
	assert.Len(t, listed.Items, 100)
	response = performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks?page=1000", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	assert.Equal(t, 1000, listed.Page)

	for _, query := range []string{"?page=0", "?page=bad", "?page=1001", "?page=9223372036854775807", "?page_size=0", "?page_size=bad"} {
		response = performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks"+query, "", nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
		assert.JSONEq(t, `{"error":"invalid task request"}`, response.Body.String(), query)
	}
}

func TestTaskBrowserListFiltersStatusAndTaskTypeBeforePaging(t *testing.T) {
	router, tokens, repository, _ := newTaskBrowserFixture(t)
	now := time.Now().UTC()
	fixtures := []Task{
		{ID: "alice-canonical", OwnerUserID: "user-alice", OwnerUsername: "alice", TaskType: "mcp_scan", Status: StatusRunning},
		{ID: "alice-alias", OwnerUserID: "user-alice", OwnerUsername: "alice", TaskType: "Mcp-Scan", Status: StatusRunning},
		{ID: "alice-pending", OwnerUserID: "user-alice", OwnerUsername: "alice", TaskType: "mcp_scan", Status: StatusPending},
		{ID: "alice-agent", OwnerUserID: "user-alice", OwnerUsername: "alice", TaskType: "agent_scan", Status: StatusRunning},
		{ID: "bob-running", OwnerUserID: "user-bob", OwnerUsername: "bob", TaskType: "mcp_scan", Status: StatusRunning},
	}
	for index := range fixtures {
		fixtures[index].IdempotencyKey = fixtures[index].ID
		fixtures[index].Params = json.RawMessage(`{}`)
		fixtures[index].AttachmentRefs = json.RawMessage(`[]`)
		fixtures[index].CreatedAt = now.Add(time.Duration(index) * time.Minute)
		fixtures[index].UpdatedAt = fixtures[index].CreatedAt
		putBrowserTask(t, repository, fixtures[index])
	}

	response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks?status=running&task_type=mcp_scan&page=1&page_size=1", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var listed TaskListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	assert.Equal(t, int64(2), listed.Total)
	require.Len(t, listed.Items, 1)
	assert.Equal(t, "alice-alias", listed.Items[0].ID)

	global := performTaskJSON(t, router, tokens["auditor"], http.MethodGet, "/tasks?status=running&task_type=mcp_scan&page_size=20", "", nil)
	require.Equal(t, http.StatusOK, global.Code, global.Body.String())
	require.NoError(t, json.Unmarshal(global.Body.Bytes(), &listed))
	assert.Equal(t, int64(3), listed.Total)
	assert.Equal(t, []string{"bob-running", "alice-alias", "alice-canonical"}, taskSummaryIDs(listed.Items))
}

func TestTaskBrowserListRejectsInvalidExactFilters(t *testing.T) {
	router, tokens, _, _ := newTaskBrowserFixture(t)
	for _, query := range []string{
		"?status=done",
		"?status=RUNNING",
		"?task_type=unknown",
		"?task_type=Mcp-Scan",
		"?task_type=not_registered",
	} {
		response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks"+query, "", nil)
		assert.Equal(t, http.StatusBadRequest, response.Code, query)
		assert.JSONEq(t, `{"error":"invalid task request"}`, response.Body.String(), query)
	}
}

func TestTaskBrowserServiceRejectsPageBeyondMaximum(t *testing.T) {
	service := NewService(NewMemoryRepository(), &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	subject := identity.Subject{UserID: "user-alice", Username: "alice", Role: identity.RoleUser}
	response, err := service.Browse(context.Background(), subject, 1000, 20, TaskListFilters{})
	require.NoError(t, err)
	assert.Equal(t, 1000, response.Page)
	_, err = service.Browse(context.Background(), subject, 1001, 20, TaskListFilters{})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = service.Browse(context.Background(), subject, 1, 20, TaskListFilters{Status: "done"})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = service.Browse(context.Background(), subject, 1, 20, TaskListFilters{TaskType: "unknown"})
	require.ErrorIs(t, err, ErrInvalid)
}

func TestTaskBrowserTotalUsesInt64Contract(t *testing.T) {
	var responseTotal int64 = (TaskListResponse{}).Total
	_, repositoryTotal, err := NewMemoryRepository().ListBrowser(context.Background(), TaskListQuery{})
	require.NoError(t, err)
	var checkedRepositoryTotal int64 = repositoryTotal
	assert.Zero(t, responseTotal)
	assert.Zero(t, checkedRepositoryTotal)
}

func TestTaskBrowserCanonicalizesOnlyRegisteredTaskTypeAliases(t *testing.T) {
	router, tokens, repository, _ := newTaskBrowserFixture(t)
	now := time.Now().UTC()
	aliases := []struct {
		id       string
		stored   string
		expected string
	}{
		{id: "mcp-snake", stored: "mcp_scan", expected: "mcp_scan"},
		{id: "mcp-agent", stored: "Mcp-Scan", expected: "mcp_scan"},
		{id: "infra-snake", stored: "ai_infra_scan", expected: "ai_infra_scan"},
		{id: "infra-agent", stored: "AI-Infra-Scan", expected: "ai_infra_scan"},
		{id: "redteam-snake", stored: "model_redteam_report", expected: "model_redteam_report"},
		{id: "redteam-agent", stored: "Model-Redteam-Report", expected: "model_redteam_report"},
		{id: "agent-snake", stored: "agent_scan", expected: "agent_scan"},
		{id: "agent-native", stored: "Agent-Scan", expected: "agent_scan"},
		{id: "unregistered", stored: "Model-Jailbreak", expected: "unknown"},
		{id: "wrong-case", stored: "mCp-ScAn", expected: "unknown"},
		{id: "unsafe-type", stored: "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890", expected: "unknown"},
	}
	for _, alias := range aliases {
		putBrowserTask(t, repository, Task{
			ID: alias.id, OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: alias.id,
			TaskType: alias.stored, Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
			Status: StatusPending, CreatedAt: now, UpdatedAt: now,
		})
	}

	response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks?page_size=100", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var listed TaskListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	actual := make(map[string]string, len(listed.Items))
	for _, item := range listed.Items {
		actual[item.ID] = item.TaskType
	}
	for _, alias := range aliases {
		assert.Equal(t, alias.expected, actual[alias.id], alias.id)
	}
	assert.NotContains(t, response.Body.String(), "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890")
}

func TestTaskBrowserDetailUsesTaskTypeWhitelistAndDropsUnsafeFields(t *testing.T) {
	router, tokens, repository, _ := newTaskBrowserFixture(t)
	now := time.Now().UTC()
	putBrowserTask(t, repository, Task{
		ID: "agent-detail", OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: "agent-detail",
		EngineSessionID: "engine-detail-secret", TaskType: "agent_scan", Content: "prompt-secret",
		Params:         json.RawMessage(`{"agent_id":"display-agent","agent_data":"yaml-secret","eval_model":{"token":"token-secret"},"unexpected":"drop-secret"}`),
		AttachmentRefs: json.RawMessage(`["attachment-secret"]`), CountryIsoCode: "zh", Status: StatusRunning,
		DispatchError: "dispatch-secret", DispatchClaimToken: "claim-secret", CreatedAt: now, UpdatedAt: now,
	})
	putBrowserTask(t, repository, Task{
		ID: "unknown-detail", OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: "unknown-detail",
		TaskType: "future_unsafe_task", Content: "unknown-content-secret", Params: json.RawMessage(`{"label":"unknown-param-secret"}`),
		AttachmentRefs: json.RawMessage(`[]`), Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	})
	putBrowserTask(t, repository, Task{
		ID: "sensitive-display-value", OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: "sensitive-display-value",
		TaskType: "agent_scan", Params: json.RawMessage(`{"agent_id":"sk-browser-sensitive-value"}`),
		AttachmentRefs: json.RawMessage(`[]`), CountryIsoCode: "Bearer language-secret", Status: StatusPending,
		CreatedAt: now, UpdatedAt: now,
	})
	putBrowserTask(t, repository, Task{
		ID: "redteam-unsafe-values", OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: "redteam-unsafe-values",
		TaskType: "Model-Redteam-Report", CountryIsoCode: "zh_CN", Status: StatusPending,
		Params: json.RawMessage(`{
			"dataset":{"dataFile":["eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhIn0.signature","ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890","AKIAIOSFODNN7EXAMPLE","e7W9qL2mN8vR4xC6bH1kP5sT3yU0iO9a"],"numPrompts":25},
			"techniques":["https://example.invalid/?token=hidden","dGhpcy1pcy1oaWdoLWVudHJvcHktZnJlZS10ZXh0"]
		}`),
		AttachmentRefs: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now,
	})

	response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks/agent-detail", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var detail TaskDetail
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &detail))
	assert.Equal(t, "zh", detail.InputSummary.Language)
	assert.NotContains(t, response.Body.String(), "agent_id")
	for _, secret := range []string{"user-alice", "engine-detail-secret", "prompt-secret", "yaml-secret", "token-secret", "drop-secret", "attachment-secret", "dispatch-secret", "claim-secret"} {
		assert.NotContains(t, response.Body.String(), secret)
	}

	response = performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks/unknown-detail", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "unknown-content-secret")
	assert.NotContains(t, response.Body.String(), "unknown-param-secret")
	var unknownWire map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &unknownWire))
	assert.Empty(t, unknownWire["input_summary"].(map[string]any))

	response = performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks/sensitive-display-value", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "sk-browser-sensitive-value")
	assert.NotContains(t, response.Body.String(), "language-secret")

	response = performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks/redteam-unsafe-values", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var redteamWire map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &redteamWire))
	inputSummary := redteamWire["input_summary"].(map[string]any)
	assert.ElementsMatch(t, []string{"language", "num_prompts"}, mapKeys(inputSummary))
	assert.Equal(t, "zh", inputSummary["language"])
	assert.Equal(t, float64(25), inputSummary["num_prompts"])
	for _, unsafe := range []string{
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhIn0.signature",
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890",
		"AKIAIOSFODNN7EXAMPLE",
		"e7W9qL2mN8vR4xC6bH1kP5sT3yU0iO9a",
		"https://example.invalid/?token=hidden",
		"dGhpcy1pcy1oaWdoLWVudHJvcHktZnJlZS10ZXh0",
	} {
		assert.NotContains(t, response.Body.String(), unsafe)
	}
}

func TestTaskDetailMCPBrowserSourceSummaryOnlyProjectsSafeEnum(t *testing.T) {
	for _, test := range []struct {
		name       string
		params     json.RawMessage
		wantSource string
		wantThread bool
	}{
		{name: "repository", params: json.RawMessage(`{"source_kind":"repository","model_id":"private-model","thread":7}`), wantSource: "repository", wantThread: true},
		{name: "service", params: json.RawMessage(`{"source_kind":"service","authorization_confirmed":true,"model_id":"private-model","thread":7}`), wantSource: "service", wantThread: true},
		{name: "legacy missing source", params: json.RawMessage(`{"model_id":"private-model","thread":7}`), wantSource: "legacy_unknown", wantThread: true},
		{name: "unknown source", params: json.RawMessage(`{"source_kind":"untrusted","thread":7}`), wantSource: "legacy_unknown", wantThread: true},
		{name: "malformed params", params: json.RawMessage(`{`), wantSource: "legacy_unknown"},
	} {
		t.Run(test.name, func(t *testing.T) {
			detail := taskDetailOf(&Task{
				ID: "mcp-browser-" + strings.ReplaceAll(test.name, " ", "-"), OwnerUsername: "alice", TaskType: "mcp_scan",
				Content: "https://user:private-password@mcp.example.test/rpc?token=private-token", Params: test.params,
				CountryIsoCode: "zh", Status: StatusRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
			})
			encoded, err := json.Marshal(detail)
			require.NoError(t, err)
			var wire map[string]any
			require.NoError(t, json.Unmarshal(encoded, &wire))
			summary := wire["input_summary"].(map[string]any)
			assert.Equal(t, test.wantSource, summary["source_kind"])
			if test.wantThread {
				assert.Equal(t, float64(7), summary["thread"])
			} else {
				assert.NotContains(t, summary, "thread")
			}
			assert.Equal(t, "zh", summary["language"])
			for _, secret := range []string{"private-password", "private-token", "private-model", "authorization_confirmed", "https://user:"} {
				assert.NotContains(t, string(encoded), secret)
			}
		})
	}
}

func TestTaskAndViewDetailProjectionStayEquivalentAndSafe(t *testing.T) {
	now := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	task := &Task{
		ID: "projection-task", OwnerUserID: "owner-id-sentinel", OwnerUsername: "alice",
		EngineSessionID: "engine-session-sentinel", TaskType: "AI-Infra-Scan",
		Content: "https://target.invalid\n\nhttps://second.invalid", Params: json.RawMessage(`{"timeout":45,"secret_label":"params-sentinel"}`),
		AttachmentRefs: json.RawMessage(`["attachment-sentinel"]`), CountryIsoCode: "en", Status: StatusRunning,
		DispatchError: "dispatch-error-sentinel", CreatedAt: now, UpdatedAt: now.Add(time.Minute),
	}

	fromTask := taskDetailOf(task)
	fromView := taskDetailOfView(viewOf(task))
	assert.Equal(t, fromTask, fromView)
	encoded, err := json.Marshal(fromTask)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(encoded, &wire))
	assert.ElementsMatch(t, []string{"id", "owner", "task_type", "status", "created_at", "updated_at", "input_summary"}, mapKeys(wire))
	for _, forbidden := range []string{"owner-id-sentinel", "engine-session-sentinel", "target.invalid", "params-sentinel", "attachment-sentinel", "dispatch-error-sentinel"} {
		assert.NotContains(t, string(encoded), forbidden)
	}
}

func TestTaskBrowserGormRepositoryFiltersOwnerBeforePagingAndCountsFilteredTotal(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_tasks").Error)
	repository := NewGormRepository(db)
	base := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	for index := range 20 {
		putBrowserTask(t, repository, Task{
			ID: fmt.Sprintf("bob-%02d", index), OwnerUserID: "user-bob", OwnerUsername: "bob", IdempotencyKey: fmt.Sprintf("bob-%02d", index),
			EngineSessionID: fmt.Sprintf("engine-bob-%02d", index),
			TaskType:        "mcp_scan", Status: StatusPending, Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
			CreatedAt: base.Add(time.Duration(index+10) * time.Minute), UpdatedAt: base,
		})
	}
	for index := range 2 {
		putBrowserTask(t, repository, Task{
			ID: fmt.Sprintf("alice-%02d", index), OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: fmt.Sprintf("alice-%02d", index),
			EngineSessionID: fmt.Sprintf("engine-alice-%02d", index),
			TaskType:        "mcp_scan", Status: StatusPending, Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`),
			CreatedAt: base.Add(time.Duration(index) * time.Minute), UpdatedAt: base,
		})
	}
	putBrowserTask(t, repository, Task{
		ID: "alice-filtered-out", OwnerUserID: "user-alice", OwnerUsername: "alice", IdempotencyKey: "alice-filtered-out",
		EngineSessionID: "engine-filtered-out", TaskType: "agent_scan", Status: StatusRunning,
		Params: json.RawMessage(`{}`), AttachmentRefs: json.RawMessage(`[]`), CreatedAt: base.Add(30 * time.Minute), UpdatedAt: base,
	})

	capture := &taskQueryCaptureLogger{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = capture
	service := NewService(repository, &recordingEngine{}, audit.NewService(audit.NewMemoryRepository()))
	listed, err := service.Browse(
		context.Background(),
		identity.Subject{UserID: "user-alice", Username: "alice", Role: identity.RoleUser},
		1,
		1,
		TaskListFilters{Status: StatusPending, TaskType: "mcp_scan"},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(2), listed.Total)
	require.Len(t, listed.Items, 1)
	assert.Equal(t, "alice-01", listed.Items[0].ID)
	require.NotEmpty(t, capture.statements)
	listSQL := capture.statements[len(capture.statements)-1]
	projection := strings.SplitN(listSQL, " from ", 2)[0]
	for _, column := range []string{"id", "owner_username", "task_type", "status", "created_at", "updated_at"} {
		assert.Contains(t, projection, column)
	}
	for _, forbidden := range []string{"*", "owner_user_id", "content", "params", "attachment_refs", "engine_session_id", "dispatch_error", "dispatch_claim_token", "dispatch_lease_until"} {
		assert.NotContains(t, projection, forbidden)
	}

	_, err = repository.GetBrowser(context.Background(), "bob-19", "user-alice")
	require.ErrorIs(t, err, ErrNotFound)
	visible, err := repository.GetBrowser(context.Background(), "alice-01", "user-alice")
	require.NoError(t, err)
	assert.Equal(t, "alice-01", visible.ID)

	_, err = service.BrowserGet(context.Background(), identity.Subject{UserID: "user-alice", Username: "alice", Role: identity.RoleUser}, "bob-19")
	require.ErrorIs(t, err, ErrNotFound)
}

func newTaskBrowserFixture(t *testing.T) (http.Handler, map[string]string, *MemoryRepository, *recordingEngine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	identityService := identity.NewService(identity.NewMemoryRepository())
	tokens := map[string]string{}
	for _, user := range []identity.CreateUserInput{
		{ID: "user-alice", Username: "alice", Password: "secret", Role: identity.RoleUser},
		{ID: "user-bob", Username: "bob", Password: "secret", Role: identity.RoleUser},
		{ID: "user-auditor", Username: "auditor", Password: "secret", Role: identity.RoleAuditor},
		{ID: "user-admin", Username: "admin", Password: "secret", Role: identity.RoleAdmin},
	} {
		_, err := identityService.CreateUser(context.Background(), user)
		require.NoError(t, err)
		login, err := identityService.Authenticate(context.Background(), user.Username, user.Password)
		require.NoError(t, err)
		tokens[user.Username] = login.Token
	}
	repository := NewMemoryRepository()
	engine := &recordingEngine{}
	service := NewService(repository, engine, audit.NewService(audit.NewMemoryRepository()))
	router := gin.New()
	NewHandler(service).Register(router.Group("/tasks", identity.Authenticate(identityService, identity.CookiePolicy{})))
	return router, tokens, repository, engine
}

func putBrowserTask(t *testing.T, repository Repository, task Task) {
	t.Helper()
	_, created, err := repository.CreateOrGet(context.Background(), &task)
	require.NoError(t, err)
	require.True(t, created)
}

func taskSummaryIDs(items []TaskSummary) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func mapKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	return keys
}

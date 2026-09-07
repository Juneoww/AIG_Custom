package tasks

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func skillsZIP(t *testing.T, definition string) []byte {
	t.Helper()
	var contents bytes.Buffer
	writer := zip.NewWriter(&contents)
	file, err := writer.Create("example/SKILL.md")
	require.NoError(t, err)
	_, err = io.WriteString(file, definition)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return contents.Bytes()
}

func newSkillsFixture(t *testing.T) (*Service, *AttachmentService, *MemoryRepository, *recordingEngine, identity.Subject) {
	t.Helper()
	repository := NewMemoryRepository()
	audits := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 21 << 20, MaxChunkBytes: 1 << 20}, audits)
	require.NoError(t, err)
	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	service := NewService(repository, engine, audits)
	service.SetAttachmentService(attachments)
	return service, attachments, repository, engine, identity.Subject{UserID: "user-alice", Username: "alice", Role: identity.RoleUser}
}

func uploadSkill(t *testing.T, attachments *AttachmentService, owner identity.Subject, filename string) AttachmentView {
	t.Helper()
	file, err := attachments.Upload(context.Background(), owner, filename, bytes.NewReader(skillsZIP(t, "---\nname: example\ndescription: Read supplied text.\n---\nInspect text without running it.\n")))
	require.NoError(t, err)
	return file
}

func TestSkillsCreatePersistsIdentityAndReplaysAttachedPackage(t *testing.T) {
	service, attachments, repository, engine, owner := newSkillsFixture(t)
	file := uploadSkill(t, attachments, owner, "Example.ZIP")
	input := CreateInput{IdempotencyKey: "skills-create", TaskType: "skills_scan", AttachmentIDs: []string{file.ID}, Params: json.RawMessage(`{"model_id":"model-example"}`), Remark: "  private business remark  ", CountryIsoCode: "zh_CN"}
	created, err := service.Create(context.Background(), owner, input)
	require.NoError(t, err)
	assert.Equal(t, "skills_scan", created.TaskType)
	assert.Equal(t, "private business remark", created.Remark)
	assert.Equal(t, StatusRunning, created.Status)
	stored, err := repository.Get(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, stored.TargetCount)
	attachment, err := repository.GetAttachment(context.Background(), file.ID)
	require.NoError(t, err)
	assert.Equal(t, AttachmentStateAttached, attachment.State)
	assert.Equal(t, "skills_scan", engine.last.TaskType)
	assert.Empty(t, engine.last.Content)
	assert.Equal(t, []string{attachment.StorageName}, engine.last.Attachments)
	assert.JSONEq(t, `{"model_id":"model-example"}`, string(engine.last.Params))
	second, err := service.Create(context.Background(), owner, input)
	require.NoError(t, err)
	assert.Equal(t, created.ID, second.ID)
	assert.Equal(t, int64(1), engine.submits.Load())
	input.Remark = "different remark"
	_, err = service.Create(context.Background(), owner, input)
	require.ErrorIs(t, err, ErrInvalid)
	detail, err := service.BrowserGet(context.Background(), owner, created.ID)
	require.NoError(t, err)
	wire, err := json.Marshal(detail.InputSummary)
	require.NoError(t, err)
	assert.JSONEq(t, `{"language":"zh","model_id":"model-example","scan_mode":"static"}`, string(wire))
	assert.Equal(t, "private business remark", detail.Remark)
}

func TestSkillsCreateRejectsInvalidContractBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{"missing model", func(in *CreateInput) { in.Params = json.RawMessage(`{}`) }},
		{"empty model", func(in *CreateInput) { in.Params = json.RawMessage(`{"model_id":""}`) }},
		{"raw credential", func(in *CreateInput) { in.Params = json.RawMessage(`{"model_id":"model-example","token":"secret"}`) }},
		{"thread", func(in *CreateInput) { in.Params = json.RawMessage(`{"model_id":"model-example","thread":1}`) }},
		{"mode override", func(in *CreateInput) {
			in.Params = json.RawMessage(`{"model_id":"model-example","scan_mode":"dynamic"}`)
		}},
		{"no package", func(in *CreateInput) { in.AttachmentIDs = nil }},
		{"two packages", func(in *CreateInput) { in.AttachmentIDs = append(in.AttachmentIDs, "another-id") }},
		{"content", func(in *CreateInput) { in.Content = "https://example.invalid/repo" }},
		{"whitespace content", func(in *CreateInput) { in.Content = " " }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, attachments, repository, engine, owner := newSkillsFixture(t)
			file := uploadSkill(t, attachments, owner, "skill.zip")
			input := CreateInput{IdempotencyKey: "invalid", TaskType: "skills_scan", Params: json.RawMessage(`{"model_id":"model-example"}`), AttachmentIDs: []string{file.ID}}
			tc.mutate(&input)
			_, err := service.Create(context.Background(), owner, input)
			require.ErrorIs(t, err, ErrInvalid)
			rows, err := repository.List(context.Background())
			require.NoError(t, err)
			assert.Empty(t, rows)
			assert.Zero(t, engine.submits.Load())
			attachment, err := repository.GetAttachment(context.Background(), file.ID)
			require.NoError(t, err)
			assert.Equal(t, AttachmentStateReady, attachment.State)
		})
	}
}

func TestSkillsCreateValidatesPackageAndGovernance(t *testing.T) {
	for _, mode := range []string{"bad zip", "bad metadata", "wrong extension", "owner", "not ready", "model denied", "storage mismatch", "storage symlink"} {
		t.Run(mode, func(t *testing.T) {
			service, attachments, repository, engine, owner := newSkillsFixture(t)
			filename := "skill.zip"
			if mode == "wrong extension" {
				filename = "skill.txt"
			}
			file := uploadSkill(t, attachments, owner, filename)
			attachment, err := repository.GetAttachment(context.Background(), file.ID)
			require.NoError(t, err)
			storage, err := attachments.storagePath(attachment.StorageName)
			require.NoError(t, err)
			switch mode {
			case "bad zip":
				require.NoError(t, os.WriteFile(storage, bytes.Repeat([]byte("x"), int(attachment.Size)), 0600))
			case "bad metadata":
				invalid, uploadErr := attachments.Upload(context.Background(), owner, "bad.zip", bytes.NewReader(skillsZIP(t, "hello")))
				require.NoError(t, uploadErr)
				file = invalid
			case "owner":
				owner = identity.Subject{UserID: "user-bob", Username: "bob", Role: identity.RoleUser}
			case "not ready":
				require.NoError(t, repository.BindReadyAttachments(context.Background(), owner.UserID, []string{file.ID}, time.Now()))
			case "model denied":
				service.engine = &rejectingReferenceEngine{}
			case "storage mismatch":
				require.NoError(t, os.WriteFile(storage, []byte("short"), 0600))
			case "storage symlink":
				target := filepath.Join(t.TempDir(), "source.zip")
				data, readErr := os.ReadFile(storage)
				require.NoError(t, readErr)
				require.NoError(t, os.WriteFile(target, data, 0600))
				require.NoError(t, os.Remove(storage))
				if linkErr := os.Symlink(target, storage); linkErr != nil {
					t.Skip("symlink unavailable")
				}
			}
			_, err = service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "invalid-file", TaskType: "skills_scan", Params: json.RawMessage(`{"model_id":"model-example"}`), AttachmentIDs: []string{file.ID}})
			require.Error(t, err)
			assert.Zero(t, engine.submits.Load())
			rows, err := repository.List(context.Background())
			require.NoError(t, err)
			assert.Empty(t, rows)
		})
	}
}

func TestSkillsBrowserFiltersBeforePaginationAndKeepsOwnersIsolated(t *testing.T) {
	router, tokens, repository, _ := newTaskBrowserFixture(t)
	for i, kind := range []string{"skills_scan", "Skills-Scan", "mcp_scan", "skills_scan"} {
		ownerID, username := "user-alice", "alice"
		if i == 3 {
			ownerID, username = "user-bob", "bob"
		}
		id := "skill-" + strings.Repeat("x", i+1)
		putBrowserTask(t, repository, Task{ID: id, IdempotencyKey: id, OwnerUserID: ownerID, OwnerUsername: username, TaskType: kind, Status: StatusRunning, Params: json.RawMessage(`{"model_id":"model-example","token":"secret"}`), Content: "raw-secret", Remark: "remark-secret", AttachmentRefs: json.RawMessage(`[]`), CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}
	response := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks?task_type=skills_scan&page_size=1&page=2", "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var list TaskListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	assert.Equal(t, int64(2), list.Total)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "skills_scan", list.Items[0].TaskType)
	assert.NotContains(t, response.Body.String(), "secret")
	global := performTaskJSON(t, router, tokens["auditor"], http.MethodGet, "/tasks?task_type=skills_scan", "", nil)
	require.Equal(t, http.StatusOK, global.Code)
	require.NoError(t, json.Unmarshal(global.Body.Bytes(), &list))
	assert.Equal(t, int64(3), list.Total)
	foreign := performTaskJSON(t, router, tokens["alice"], http.MethodGet, "/tasks/skill-xxxx", "", nil)
	assert.Equal(t, http.StatusNotFound, foreign.Code)
	readonly := performTaskJSON(t, router, tokens["auditor"], http.MethodPost, "/tasks/skill-x/cancel", "", nil)
	assert.Equal(t, http.StatusForbidden, readonly.Code)
}

func TestSkillsTrustedCompletionCreatesSeparateImmutableReport(t *testing.T) {
	service, attachments, _, engine, owner := newSkillsFixture(t)
	file := uploadSkill(t, attachments, owner, "skill.zip")
	snapshots := reports.NewMemoryRepository()
	service.SetReportSnapshotService(reports.NewService(snapshots, brand.NewService(brand.NewMemoryRepository())))
	created, err := service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "skills-report", TaskType: "skills_scan", Params: json.RawMessage(`{"model_id":"model-example"}`), AttachmentIDs: []string{file.ID}, Remark: "private-business-remark"})
	require.NoError(t, err)
	setEngineResult(engine, created.EngineSessionID, json.RawMessage(`{"id":"skills-result","type":"resultUpdate","timestamp":1,"result":{"score":75,"results":[{"title":"External transfer","desc":"scripts/send.py:2 reads and sends data","level":"high","risk_type":"Data Exfiltration","suggestion":"Remove the transfer"}]}}`))
	require.NoError(t, service.RecordEngineEvent(context.Background(), created.EngineSessionID, EngineStateSucceeded, ""))
	require.NoError(t, service.RecordEngineEvent(context.Background(), created.EngineSessionID, EngineStateSucceeded, ""))
	snapshot, err := snapshots.GetByTaskID(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "skills_scan", snapshot.TaskType)
	assert.Equal(t, 1, snapshot.Risk.High)
	assert.Equal(t, int64(1), engine.resultReads.Load())
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private-business-remark")
	require.NoError(t, service.Cancel(context.Background(), owner, created.ID))
	detail, err := service.BrowserGet(context.Background(), owner, created.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, detail.Status)
}

func TestSkillsGormPersistsAndFiltersBeforePagination(t *testing.T) {
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "Skills PostgreSQL regression requires the isolated test database")
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	require.NoError(t, db.Exec("DELETE FROM platform_tasks").Error)
	repository := NewGormRepository(db)
	audits := audit.NewService(audit.NewMemoryRepository())
	attachments, err := NewAttachmentService(repository, AttachmentConfig{UploadDir: t.TempDir(), MaxFileBytes: 21 << 20, MaxChunkBytes: 1 << 20}, audits)
	require.NoError(t, err)
	owner := identity.Subject{UserID: "skills-gorm-owner", Username: "alice", Role: identity.RoleUser}
	engine := &recordingEngine{}
	service := NewService(repository, engine, audits)
	service.SetAttachmentService(attachments)
	for index := 0; index < 3; index++ {
		file := uploadSkill(t, attachments, owner, "skill.zip")
		input := CreateInput{IdempotencyKey: "skills-gorm-" + strings.Repeat("x", index+1), TaskType: "skills_scan", Params: json.RawMessage(`{"model_id":"model-example"}`), AttachmentIDs: []string{file.ID}}
		created, createErr := service.Create(context.Background(), owner, input)
		require.NoError(t, createErr)
		replayed, replayErr := service.Create(context.Background(), owner, input)
		require.NoError(t, replayErr)
		assert.Equal(t, created.ID, replayed.ID)
	}
	_, err = service.Create(context.Background(), owner, CreateInput{IdempotencyKey: "mcp-other", TaskType: "mcp_scan", Content: "https://example.com/repository.git", Params: json.RawMessage(`{"source_kind":"repository"}`)})
	require.NoError(t, err)
	list, err := service.Browse(context.Background(), owner, 2, 2, TaskListFilters{TaskType: "skills_scan"})
	require.NoError(t, err)
	assert.Equal(t, int64(3), list.Total)
	require.Len(t, list.Items, 1)
	assert.Equal(t, "skills_scan", list.Items[0].TaskType)
	detail, err := service.BrowserGet(context.Background(), owner, list.Items[0].ID)
	require.NoError(t, err)
	assert.Equal(t, "static", detail.InputSummary.ScanMode)
}

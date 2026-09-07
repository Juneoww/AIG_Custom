package websocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	platformaudit "github.com/Juneoww/AIG_Custom/internal/platform/audit"
	platformbrand "github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpconnections"
	"github.com/Juneoww/AIG_Custom/internal/platform/mcpegress"
	platformreports "github.com/Juneoww/AIG_Custom/internal/platform/reports"
	platformtasks "github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTaskManagerReconcilesDurableResultAfterInitialPlatformSnapshotFailure(t *testing.T) {
	ctx := context.Background()
	db := openPlatformRecoveryPostgresDB(t)
	taskStore := database.NewTaskStore(db)
	require.NoError(t, taskStore.Init())
	modelStore := database.NewModelStore(db)
	require.NoError(t, modelStore.Init())
	manager := NewTaskManager(NewAgentManager(), taskStore, modelStore, nil, NewSSEManager())

	taskRepository := platformtasks.NewGormRepository(db)
	auditRepository := platformaudit.NewGormRepository(db)
	reportRepository := platformreports.NewGormRepository(db)
	brandRepository := platformbrand.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, reportRepository.Init())
	require.NoError(t, brandRepository.Init())

	brands := platformbrand.NewService(brandRepository)
	reports := platformreports.NewService(reportRepository, brands)
	failedService := platformtasks.NewService(taskRepository, manager, platformaudit.NewService(auditRepository))
	failedService.SetReportSnapshotService(&persistFailingSnapshotter{delegate: reports})
	manager.SetPlatformTaskEventSink(failedService)

	const sessionID = "durable-result-recovery"
	raw := json.RawMessage(`{"id":"durable-event","type":"resultUpdate","timestamp":1,"result":{"score":73,"results":[{"level":"high"},{"level":"medium"}]}}`)
	require.NoError(t, taskStore.CreateSession(&database.Session{
		ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan", Status: TaskStatusDoing,
		AssignedAgent: "agent-recovery", Share: false,
	}))
	nowTask := platformtasks.Task{
		ID: sessionID, OwnerUserID: "alice-id", OwnerUsername: "alice", IdempotencyKey: sessionID,
		EngineSessionID: sessionID, TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`),
		AttachmentRefs: json.RawMessage(`[]`), Status: platformtasks.StatusRunning,
	}
	_, created, err := taskRepository.CreateOrGet(ctx, &nowTask)
	require.NoError(t, err)
	require.True(t, created)

	// Recovery exercises the real binding-based redactor. A missing redactor
	// must now fail closed instead of persisting an unfiltered success result.
	keyring, err := mcpconnections.NewKeyring("recovery-test-key", bytes.Repeat([]byte{7}, 32), nil)
	require.NoError(t, err)
	bindings := mcpconnections.NewGormRepository(db)
	bound := &mcpconnections.TaskBinding{ID: "recovery-binding", TaskID: sessionID, SourceKind: "repository"}
	require.NoError(t, keyring.SealRepositorySource(bound, mcpconnections.BindingEncryptionContext{
		OwnerUserID: "alice-id", Scope: mcpconnections.ScopePrivate, Version: 1,
	}, mcpconnections.RepositorySourceSnapshot{RepositoryURL: "https://git.example.test/recovery/source.git"}))
	require.NoError(t, bindings.CreateTaskBinding(ctx, bound))
	manager.SetMCPEventRedactor(mcpegress.NewService(mcpegress.ServiceDependencies{
		Tasks: taskRepository, Bindings: bindings, Keyring: keyring,
	}))

	assert.True(t, manager.HandleAgentEvent("agent-recovery", sessionID, WSMsgTypeResultUpdate, raw))
	legacy, err := taskStore.GetSession(sessionID)
	require.NoError(t, err)
	assert.Equal(t, TaskStatusDone, legacy.Status)
	require.NotNil(t, legacy.CompletedAt)
	platformTask, err := taskRepository.Get(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, platformtasks.StatusRunning, platformTask.Status)
	_, err = reportRepository.GetByTaskID(ctx, sessionID)
	assert.ErrorIs(t, err, platformreports.ErrNotFound)
	assert.False(t, manager.HandleAgentEvent("agent-recovery", sessionID, WSMsgTypeResultUpdate, raw), "legacy terminal state rejects duplicate Agent events")

	// Rebuild the platform service and its durable repositories to model a restart.
	restartedTaskRepository := platformtasks.NewGormRepository(db)
	restartedAuditRepository := platformaudit.NewGormRepository(db)
	restartedReportRepository := platformreports.NewGormRepository(db)
	require.NoError(t, restartedTaskRepository.Init())
	require.NoError(t, restartedAuditRepository.Init())
	require.NoError(t, restartedReportRepository.Init())
	restarted := platformtasks.NewService(restartedTaskRepository, manager, platformaudit.NewService(restartedAuditRepository))
	restarted.SetReportSnapshotService(platformreports.NewService(restartedReportRepository, brands))

	require.NoError(t, restarted.ReconcileCompletedEngineResults(ctx))
	platformTask, err = restartedTaskRepository.Get(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, platformtasks.StatusSucceeded, platformTask.Status)
	snapshot, err := restartedReportRepository.GetByTaskID(ctx, sessionID)
	require.NoError(t, err)
	assert.True(t, time.UnixMilli(*legacy.CompletedAt).UTC().Equal(snapshot.CompletedAt), "restart recovery must preserve the trusted legacy completion time")
	assert.JSONEq(t, string(raw), string(snapshot.RawResult))
	assert.Equal(t, "risk-v2", snapshot.Risk.MappingVersion)
	assert.Equal(t, 1, snapshot.Risk.High)
	assert.Equal(t, 1, snapshot.Risk.Medium)
	assert.Equal(t, 73, snapshot.Risk.Score)
	messages, err := taskStore.GetSessionEventsByType(sessionID, WSMsgTypeResultUpdate)
	require.NoError(t, err)
	assert.Len(t, messages, 1, "recovery must not append another legacy result event")

	require.NoError(t, restarted.ReconcileCompletedEngineResults(ctx))
	messages, err = taskStore.GetSessionEventsByType(sessionID, WSMsgTypeResultUpdate)
	require.NoError(t, err)
	assert.Len(t, messages, 1)
}

func TestTaskManagerConcurrentRecoveryIsIdempotentForOneDurableResult(t *testing.T) {
	ctx := context.Background()
	db := openPlatformRecoveryPostgresDB(t)
	taskStore := database.NewTaskStore(db)
	require.NoError(t, taskStore.Init())
	modelStore := database.NewModelStore(db)
	require.NoError(t, modelStore.Init())
	manager := NewTaskManager(NewAgentManager(), taskStore, modelStore, nil, NewSSEManager())

	taskRepository := platformtasks.NewGormRepository(db)
	auditRepository := platformaudit.NewGormRepository(db)
	reportRepository := platformreports.NewGormRepository(db)
	brandRepository := platformbrand.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, reportRepository.Init())
	require.NoError(t, brandRepository.Init())

	const sessionID = "concurrent-durable-result"
	raw := json.RawMessage(`{"id":"concurrent-event","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[{"level":"low"}]}}`)
	completedAt := time.Date(2026, 8, 12, 13, 0, 0, 0, time.UTC).UnixMilli()
	require.NoError(t, taskStore.CreateSession(&database.Session{
		ID: sessionID, Username: "alice", TaskType: "mcp_scan", Content: "scan", Status: TaskStatusDone,
		AssignedAgent: "agent-concurrent", CompletedAt: &completedAt, Share: false,
	}))
	require.NoError(t, taskStore.StoreEvent("concurrent-result", sessionID, WSMsgTypeResultUpdate, raw, 1))
	_, created, err := taskRepository.CreateOrGet(ctx, &platformtasks.Task{
		ID: sessionID, OwnerUserID: "alice-id", OwnerUsername: "alice", IdempotencyKey: sessionID,
		EngineSessionID: sessionID, TaskType: "mcp_scan", Content: "scan", Params: json.RawMessage(`{}`),
		AttachmentRefs: json.RawMessage(`[]`), Status: platformtasks.StatusRunning,
	})
	require.NoError(t, err)
	require.True(t, created)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	newService := func() *platformtasks.Service {
		service := platformtasks.NewService(platformtasks.NewGormRepository(db), manager, platformaudit.NewService(platformaudit.NewGormRepository(db)))
		return service
	}
	barrier := &recoveryBarrierSnapshotter{
		delegate: platformreports.NewService(platformreports.NewGormRepository(db), platformbrand.NewService(platformbrand.NewGormRepository(db))),
		release:  make(chan struct{}),
	}
	first, second := newService(), newService()
	first.SetReportSnapshotService(barrier)
	second.SetReportSnapshotService(barrier)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, service := range []*platformtasks.Service{first, second} {
		wait.Add(1)
		go func(service *platformtasks.Service) {
			defer wait.Done()
			results <- service.ReconcileCompletedEngineResults(ctx)
		}(service)
	}
	wait.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result, "racing recovery workers must converge rather than report a duplicate snapshot failure")
	}
	stored, err := taskRepository.Get(ctx, sessionID)
	require.NoError(t, err)
	assert.Equal(t, platformtasks.StatusSucceeded, stored.Status)
	var count int64
	require.NoError(t, db.Table("report_snapshots").Where("task_id = ?", sessionID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
	events, err := platformaudit.NewService(auditRepository).Query(ctx, identity.Subject{Role: identity.RoleAuditor}, platformaudit.Filter{
		Action: platformaudit.ActionTaskChanged, ResourceType: "task", ResourceID: sessionID, Limit: 20,
	})
	require.NoError(t, err)
	successes, failures := 0, 0
	for _, event := range events {
		if event.Outcome == platformaudit.OutcomeSuccess {
			successes++
		}
		if event.Outcome == platformaudit.OutcomeFailure {
			failures++
		}
	}
	assert.Equal(t, 1, successes, "concurrent recovery must emit one successful completion")
	assert.Zero(t, failures, "a CAS loser that observes the same terminal success must not emit a false failure")
}

type recoveryBarrierSnapshotter struct {
	delegate interface {
		Prepare(context.Context, platformreports.CompletedTask) (*platformreports.Snapshot, error)
		Persist(context.Context, *platformreports.Snapshot) error
	}
	mu      sync.Mutex
	arrived int
	release chan struct{}
}

func (snapshotter *recoveryBarrierSnapshotter) Prepare(ctx context.Context, task platformreports.CompletedTask) (*platformreports.Snapshot, error) {
	snapshotter.mu.Lock()
	snapshotter.arrived++
	if snapshotter.arrived == 2 {
		close(snapshotter.release)
	}
	snapshotter.mu.Unlock()
	select {
	case <-snapshotter.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return snapshotter.delegate.Prepare(ctx, task)
}

func (snapshotter *recoveryBarrierSnapshotter) Persist(ctx context.Context, snapshot *platformreports.Snapshot) error {
	return snapshotter.delegate.Persist(ctx, snapshot)
}

type persistFailingSnapshotter struct {
	delegate interface {
		Prepare(context.Context, platformreports.CompletedTask) (*platformreports.Snapshot, error)
		Persist(context.Context, *platformreports.Snapshot) error
	}
}

func (snapshotter *persistFailingSnapshotter) Prepare(ctx context.Context, task platformreports.CompletedTask) (*platformreports.Snapshot, error) {
	return snapshotter.delegate.Prepare(ctx, task)
}

func (snapshotter *persistFailingSnapshotter) Persist(ctx context.Context, snapshot *platformreports.Snapshot) error {
	if err := snapshotter.delegate.Persist(ctx, snapshot); err != nil {
		return err
	}
	return errors.New("snapshot persistence sentinel")
}

func openPlatformRecoveryPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "platform_recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, database.Migrate(db))
	return db
}

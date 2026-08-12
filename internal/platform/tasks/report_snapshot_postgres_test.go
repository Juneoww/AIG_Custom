package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Juneoww/AIG_Custom/internal/platform/audit"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestTrustedSuccessRollsBackTaskAndSnapshotWhenSnapshotPersistenceFails(t *testing.T) {
	ctx := context.Background()
	db := openTaskSnapshotPostgresDB(t)
	taskRepository := NewGormRepository(db)
	auditRepository := audit.NewGormRepository(db)
	reportRepository := reports.NewGormRepository(db)
	require.NoError(t, taskRepository.Init())
	require.NoError(t, auditRepository.Init())
	require.NoError(t, reportRepository.Init())

	engine := &recordingEngine{results: map[string]json.RawMessage{}}
	service := NewService(taskRepository, engine, audit.NewService(auditRepository))
	brands := brand.NewService(brand.NewMemoryRepository())
	_, err := brands.Update(ctx, identity.Subject{UserID: "admin", Role: identity.RoleAdmin}, brand.Config{ProductName: "v1", PrimaryColor: "#1677FF", LogoMIME: "image/png"})
	require.NoError(t, err)
	delegate := reports.NewService(reportRepository, brands)
	service.SetReportSnapshotService(&transactionalSnapshotter{delegate: delegate, persistErr: errors.New("persist sentinel")})
	owner := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}
	task := createRunningTask(t, service, owner, "postgres-snapshot-rollback")
	setEngineResult(engine, task.EngineSessionID, json.RawMessage(`{"id":"event-pg","type":"resultUpdate","timestamp":1,"result":{"score":90,"results":[{"level":"high"}]}}`))

	err = service.RecordEngineEvent(ctx, task.EngineSessionID, EngineStateSucceeded, "")
	require.Error(t, err)
	fresh, err := taskRepository.Get(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusRunning, fresh.Status)
	_, err = reportRepository.GetByTaskID(ctx, task.ID)
	assert.ErrorIs(t, err, reports.ErrNotFound)

	service.SetReportSnapshotService(delegate)
	require.NoError(t, service.RecordEngineEvent(ctx, task.EngineSessionID, EngineStateSucceeded, ""))
	fresh, err = taskRepository.Get(ctx, task.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusSucceeded, fresh.Status)
	_, err = reportRepository.GetByTaskID(ctx, task.ID)
	require.NoError(t, err)
}

type snapshotService interface {
	Prepare(context.Context, reports.CompletedTask) (*reports.Snapshot, error)
	Persist(context.Context, *reports.Snapshot) error
}

type transactionalSnapshotter struct {
	delegate   snapshotService
	persistErr error
}

func (snapshotter *transactionalSnapshotter) Prepare(ctx context.Context, task reports.CompletedTask) (*reports.Snapshot, error) {
	return snapshotter.delegate.Prepare(ctx, task)
}

func (snapshotter *transactionalSnapshotter) Persist(ctx context.Context, snapshot *reports.Snapshot) error {
	if err := snapshotter.delegate.Persist(ctx, snapshot); err != nil {
		return err
	}
	return snapshotter.persistErr
}

func openTaskSnapshotPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "task_snapshot_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

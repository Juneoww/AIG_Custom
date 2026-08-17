package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/txcontext"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type queryCaptureLogger struct {
	logger.Interface
	statements []string
}

func (capture *queryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	statement, _ := fc()
	capture.statements = append(capture.statements, strings.ToLower(statement))
	capture.Interface.Trace(ctx, begin, func() (string, int64) { return statement, 0 }, err)
}

func TestGormRepositoryRequiresMigrationWithoutDDL(t *testing.T) {
	db := openReportsTestDB(t)
	repository := NewGormRepository(db)

	err := repository.Init()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aig migrate")
	assert.False(t, db.Migrator().HasTable("report_snapshots"))
}

func TestGormRepositoryPersistsOneImmutableSnapshotPerTask(t *testing.T) {
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Init())

	completedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	first := &Snapshot{
		ID: "reports-gorm-1", TaskID: "reports-task-1", OwnerUserID: "alice", TaskType: "mcp_scan",
		CompletedAt: completedAt, CreatedAt: completedAt, RawResult: json.RawMessage(`{"findings":[{"severity":"high"}]}`),
		RenderData: json.RawMessage(`{"title":"report"}`), Risk: RiskSummary{MappingVersion: "risk-v1", High: 1, Score: 85},
		Brand: brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF", Logo: []byte("logo-v1"), LogoMIME: "image/png"},
	}
	require.NoError(t, repository.Create(context.Background(), first))
	first.RawResult[2] = 'X'
	first.Brand.Logo[0] = 'X'

	stored, err := repository.GetByTaskID(context.Background(), first.TaskID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"findings":[{"severity":"high"}]}`, string(stored.RawResult))
	assert.Equal(t, []byte("logo-v1"), stored.Brand.Logo)
	duplicate := *first
	duplicate.ID = "reports-gorm-2"
	duplicate.RawResult = append(json.RawMessage(nil), stored.RawResult...)
	duplicate.RenderData = append(json.RawMessage(nil), stored.RenderData...)
	duplicate.Brand = stored.Brand
	require.True(t, errors.Is(repository.Create(context.Background(), &duplicate), ErrSnapshotExists))
	var count int64
	require.NoError(t, db.Table("report_snapshots").Where("task_id = ?", first.TaskID).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	trend, err := repository.Trend(context.Background(), TrendQuery{Now: completedAt.Add(time.Hour), Days: 1, OwnerUserID: "alice"})
	require.NoError(t, err)
	require.Len(t, trend, 1)
	assert.Equal(t, 1, trend[0].High)
}

func TestGormRepositoryDuplicateCreateLeavesTransactionReadable(t *testing.T) {
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Init())

	first := reportSnapshotFixture("duplicate-readable-1", "duplicate-readable-task")
	require.NoError(t, repository.Create(context.Background(), first))
	duplicate := *first
	duplicate.ID = "duplicate-readable-2"

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		transactionContext := txcontext.WithGorm(context.Background(), tx)
		if err := repository.Create(transactionContext, &duplicate); !errors.Is(err, ErrSnapshotExists) {
			return err
		}
		_, err := repository.GetByTaskID(transactionContext, first.TaskID)
		return err
	}))
}

func TestGormRepositoryListAndTrendProjectOnlySafeRequiredColumns(t *testing.T) {
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	require.NoError(t, repository.Create(context.Background(), reportSnapshotFixture("projection-report", "projection-task")))

	capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = capture
	_, err := repository.List(context.Background(), ListQuery{OwnerUserID: "alice", Limit: 20})
	require.NoError(t, err)
	listSQL := capture.statements[len(capture.statements)-1]
	assert.NotContains(t, listSQL, "select *")
	assert.Contains(t, listSQL, "risk_summary")
	assert.Contains(t, listSQL, "product_name")
	assert.NotContains(t, listSQL, "raw_result")
	assert.NotContains(t, listSQL, "render_data")

	capture.statements = nil
	_, err = repository.Trend(context.Background(), TrendQuery{OwnerUserID: "alice", Days: 30, Now: time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)})
	require.NoError(t, err)
	trendSQL := capture.statements[len(capture.statements)-1]
	assert.Contains(t, trendSQL, "risk_summary")
	assert.Contains(t, trendSQL, "completed_at")
	assert.NotContains(t, trendSQL, "raw_result")
	assert.NotContains(t, trendSQL, "render_data")
	assert.NotContains(t, trendSQL, "brand_snapshot")
}

func TestGormReportPaginationUsesOwnerCountStableOrderAndSafeProjection(t *testing.T) {
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	repository := NewGormRepository(db)
	for _, fixture := range []struct{ id, owner string }{
		{id: "report-a", owner: "alice"}, {id: "report-b", owner: "alice"},
		{id: "report-c", owner: "alice"}, {id: "report-z", owner: "bob"},
	} {
		snapshot := reportSnapshotFixture(fixture.id, "task-"+fixture.id)
		snapshot.OwnerUserID = fixture.owner
		snapshot.RawResult = json.RawMessage(`{"raw_result":"raw-sentinel"}`)
		snapshot.RenderData = json.RawMessage(`{"content":"render-sentinel"}`)
		snapshot.Brand.Logo = []byte("logo-sentinel")
		require.NoError(t, repository.Create(context.Background(), snapshot))
	}

	capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = capture
	items, total, err := repository.ListPage(context.Background(), ListQuery{OwnerUserID: "alice", Limit: 1, Offset: 1})
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	require.Len(t, items, 1)
	assert.Equal(t, "report-b", items[0].ID)
	assert.Empty(t, items[0].RawResult)
	assert.Empty(t, items[0].RenderData)
	assert.Empty(t, items[0].Brand.Logo)

	require.GreaterOrEqual(t, len(capture.statements), 2)
	countSQL := capture.statements[len(capture.statements)-2]
	listSQL := capture.statements[len(capture.statements)-1]
	assert.Contains(t, countSQL, "owner_user_id = 'alice'")
	assert.Contains(t, listSQL, "owner_user_id = 'alice'")
	assert.Contains(t, listSQL, "order by created_at desc, id desc")
	assert.Contains(t, listSQL, "limit 1")
	assert.Contains(t, listSQL, "offset 1")
	assert.NotContains(t, listSQL, "raw_result")
	assert.NotContains(t, listSQL, "render_data")
	assert.NotContains(t, listSQL, "logo")
}

func reportSnapshotFixture(id, taskID string) *Snapshot {
	completedAt := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	return &Snapshot{
		ID: id, TaskID: taskID, OwnerUserID: "alice", TaskType: "mcp_scan",
		CompletedAt: completedAt, CreatedAt: completedAt, RawResult: json.RawMessage(`{"id":"event","type":"resultUpdate","timestamp":1,"result":{"score":100,"results":[]}}`),
		RenderData: json.RawMessage(`{"title":"report"}`), Risk: RiskSummary{MappingVersion: "risk-v2", Score: 100},
		Brand: brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF", LogoMIME: "image/png"},
	}
}

func openReportsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "reports_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, adminDB.Exec(fmt.Sprintf("CREATE SCHEMA %s", schema)).Error)
	t.Cleanup(func() { require.NoError(t, adminDB.Exec(fmt.Sprintf("DROP SCHEMA %s CASCADE", schema)).Error) })

	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	return db
}

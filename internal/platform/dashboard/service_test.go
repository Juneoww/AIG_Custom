package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/internal/platform/reports"
	"github.com/Juneoww/AIG_Custom/internal/platform/tasks"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type recordingReportReader struct {
	projection reports.DashboardProjection
	err        error
	from       time.Time
	to         time.Time
	subject    identity.Subject
	limit      int
}

func (reader *recordingReportReader) Dashboard(_ context.Context, subject identity.Subject, from, to time.Time, attentionLimit int) (reports.DashboardProjection, error) {
	reader.subject = subject
	reader.from = from
	reader.to = to
	reader.limit = attentionLimit
	return reader.projection, reader.err
}

type recordingTaskReader struct {
	items   []tasks.TaskSummary
	err     error
	subject identity.Subject
	limit   int
}

func (reader *recordingTaskReader) Recent(_ context.Context, subject identity.Subject, limit int) ([]tasks.TaskSummary, error) {
	reader.subject = subject
	reader.limit = limit
	return append([]tasks.TaskSummary(nil), reader.items...), reader.err
}

func TestServiceUsesThirtyWholeUTCDaysAndBuildsStrictEmptyState(t *testing.T) {
	reportsReader := &recordingReportReader{}
	tasksReader := &recordingTaskReader{items: []tasks.TaskSummary{{ID: "recent", TaskType: "mcp_scan", Status: tasks.StatusRunning}}}
	service := NewService(reportsReader, tasksReader)
	service.now = func() time.Time { return time.Date(2026, 8, 17, 23, 45, 0, 0, time.FixedZone("CST", 8*60*60)) }
	subject := identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser}

	view, err := service.Get(context.Background(), subject)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), reportsReader.from)
	assert.Equal(t, time.Date(2026, 8, 18, 0, 0, 0, 0, time.UTC), reportsReader.to)
	assert.Equal(t, 5, reportsReader.limit)
	assert.Equal(t, subject, reportsReader.subject)
	assert.Equal(t, subject, tasksReader.subject)
	assert.Equal(t, 5, tasksReader.limit)
	assert.False(t, view.HasData)
	assert.Nil(t, view.SecurityScore)
	assert.Equal(t, reports.RiskSummary{}, view.Risk)
	assert.Empty(t, view.MappingVersions)
	assert.Empty(t, view.Attention)
	assert.Equal(t, tasksReader.items, view.RecentTasks)
	require.Len(t, view.Trend, 30)
	assert.Equal(t, time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC), view.Trend[0].Date)
	assert.Equal(t, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), view.Trend[29].Date)
	for _, point := range view.Trend {
		assert.Zero(t, point.Completed)
		assert.Nil(t, point.SecurityScore)
		assert.Zero(t, point.High)
		assert.Zero(t, point.Medium)
		assert.Zero(t, point.Low)
	}
}

func TestServiceAggregatesOnlyFrozenProjectionAndRoundsScores(t *testing.T) {
	firstDay := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)
	reportsReader := &recordingReportReader{projection: reports.DashboardProjection{
		SnapshotCount: 2,
		ScoreSum:      139,
		MappingVersions: []string{
			"risk-v2", "risk-v1", "risk-v2",
		},
		Risk: reports.RiskSummary{High: 2, Medium: 3, Low: 4},
		Trend: []reports.DashboardTrendPoint{
			{Date: firstDay, Completed: 1, ScoreSum: 80, High: 2, Medium: 1},
			{Date: lastDay, Completed: 1, ScoreSum: 59, Medium: 2, Low: 4},
		},
		Attention: []reports.DashboardAttention{
			{ReportID: "report-high", TaskID: "task-high", TaskType: "mcp_scan", CompletedAt: firstDay.Add(time.Hour), Score: 80, High: 2, Medium: 1, ProductName: "AIG"},
			{ReportID: "report-low-score", TaskID: "task-low", TaskType: "agent_scan", CompletedAt: lastDay.Add(time.Hour), Score: 59, Medium: 2, Low: 4, ProductName: "AIG"},
		},
	}}
	service := NewService(reportsReader, &recordingTaskReader{})
	service.now = func() time.Time { return lastDay.Add(12 * time.Hour) }

	view, err := service.Get(context.Background(), identity.Subject{Role: identity.RoleAuditor})
	require.NoError(t, err)
	require.NotNil(t, view.SecurityScore)
	assert.Equal(t, 70, *view.SecurityScore)
	assert.True(t, view.HasData)
	assert.Equal(t, []string{"risk-v1", "risk-v2"}, view.MappingVersions)
	assert.Equal(t, reports.RiskSummary{High: 2, Medium: 3, Low: 4, Score: 70}, view.Risk)
	require.Len(t, view.Trend, 30)
	require.NotNil(t, view.Trend[0].SecurityScore)
	assert.Equal(t, 80, *view.Trend[0].SecurityScore)
	assert.Nil(t, view.Trend[1].SecurityScore)
	require.NotNil(t, view.Trend[29].SecurityScore)
	assert.Equal(t, 59, *view.Trend[29].SecurityScore)
	require.Len(t, view.Attention, 2)
	assert.Equal(t, "report-high", view.Attention[0].ReportID)
	assert.Equal(t, reports.RiskSummary{High: 2, Medium: 1, Score: 80}, view.Attention[0].Risk)
	assert.Equal(t, "AIG", view.Attention[0].ProductName)
}

func TestServiceKeepsRoleScopeInsideReportsAndTasksServices(t *testing.T) {
	ctx := context.Background()
	reportRepository := reports.NewMemoryRepository()
	taskRepository := tasks.NewMemoryRepository()
	reportService := reports.NewService(reportRepository, nil)
	taskService := tasks.NewService(taskRepository, nil, nil)
	service := NewService(reportService, taskService)
	service.now = func() time.Time { return time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC) }
	for _, fixture := range []struct {
		id, owner, username string
		score               int
	}{
		{id: "alice", owner: "alice-id", username: "alice", score: 40},
		{id: "bob", owner: "bob-id", username: "bob", score: 100},
	} {
		now := service.now()
		require.NoError(t, reportRepository.Create(ctx, &reports.Snapshot{
			ID: "report-" + fixture.id, TaskID: "task-" + fixture.id, OwnerUserID: fixture.owner, TaskType: "mcp_scan",
			CompletedAt: now, CreatedAt: now, RawResult: json.RawMessage(`{"safe":true}`), RenderData: json.RawMessage(`{"safe":true}`),
			Risk: reports.RiskSummary{MappingVersion: "risk-v2", Score: fixture.score},
		}))
		_, _, err := taskRepository.CreateOrGet(ctx, &tasks.Task{
			ID: "task-" + fixture.id, OwnerUserID: fixture.owner, OwnerUsername: fixture.username,
			IdempotencyKey: fixture.id, TaskType: "mcp_scan", Status: tasks.StatusSucceeded, CreatedAt: now, UpdatedAt: now,
		})
		require.NoError(t, err)
	}
	now := service.now()
	require.NoError(t, reportRepository.Create(ctx, &reports.Snapshot{
		ID: "report-invalid", TaskID: "task-invalid", OwnerUserID: "alice-id", TaskType: "mcp_scan",
		CompletedAt: now, CreatedAt: now, RawResult: json.RawMessage(`{"safe":true}`), RenderData: json.RawMessage(`{"safe":true}`),
		Risk: reports.RiskSummary{Score: 101, High: 99},
	}))
	for _, fixture := range []struct {
		id     string
		status tasks.Status
	}{
		{id: "failed", status: tasks.StatusEngineFailed},
		{id: "cancelled", status: tasks.StatusCancelled},
		{id: "running", status: tasks.StatusRunning},
		{id: "unknown", status: tasks.StatusDispatchUnknown},
	} {
		require.NoError(t, reportRepository.Create(ctx, &reports.Snapshot{
			ID: "report-" + fixture.id, TaskID: "task-" + fixture.id, OwnerUserID: "alice-id", TaskType: "mcp_scan",
			CompletedAt: now, CreatedAt: now, RawResult: json.RawMessage(`{"safe":true}`), RenderData: json.RawMessage(`{"safe":true}`),
			Risk: reports.RiskSummary{MappingVersion: "risk-v2", Score: 0, High: 50},
		}))
		_, _, err := taskRepository.CreateOrGet(ctx, &tasks.Task{
			ID: "task-" + fixture.id, OwnerUserID: "alice-id", OwnerUsername: "alice", IdempotencyKey: fixture.id,
			TaskType: "mcp_scan", Status: fixture.status, CreatedAt: now, UpdatedAt: now,
		})
		require.NoError(t, err)
	}

	userView, err := service.Get(ctx, identity.Subject{UserID: "alice-id", Username: "alice", Role: identity.RoleUser})
	require.NoError(t, err)
	require.NotNil(t, userView.SecurityScore)
	assert.Equal(t, 40, *userView.SecurityScore)
	require.Len(t, userView.RecentTasks, 5)
	assert.Contains(t, taskSummaryIDs(userView.RecentTasks), "task-alice")
	assert.Contains(t, taskSummaryIDs(userView.RecentTasks), "task-failed")

	auditorView, err := service.Get(ctx, identity.Subject{Role: identity.RoleAuditor})
	require.NoError(t, err)
	require.NotNil(t, auditorView.SecurityScore)
	assert.Equal(t, 70, *auditorView.SecurityScore)
	require.Len(t, auditorView.RecentTasks, 5)
	assert.Contains(t, taskSummaryIDs(auditorView.RecentTasks), "task-bob")

	adminView, err := service.Get(ctx, identity.Subject{Role: identity.RoleAdmin})
	require.NoError(t, err)
	assert.Equal(t, auditorView.SecurityScore, adminView.SecurityScore)
}

type dashboardQueryCaptureLogger struct {
	logger.Interface
	statements []string
}

func (capture *dashboardQueryCaptureLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	statement, _ := fc()
	capture.statements = append(capture.statements, strings.ToLower(statement))
	capture.Interface.Trace(ctx, begin, func() (string, int64) { return statement, 0 }, err)
}

func TestRecentTasksUsesOwnerFilteredUpdatedOrderLimitedSafeSQLProjection(t *testing.T) {
	db := openDashboardTestDB(t)
	require.NoError(t, database.Migrate(db))
	repository := tasks.NewGormRepository(db)
	base := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	for index := range 7 {
		owner, username := "alice-id", "alice"
		if index == 6 {
			owner, username = "bob-id", "bob"
		}
		task := &tasks.Task{
			ID: fmt.Sprintf("task-%d", index), OwnerUserID: owner, OwnerUsername: username,
			IdempotencyKey: fmt.Sprintf("key-%d", index), EngineSessionID: fmt.Sprintf("engine-%d", index),
			TaskType: "mcp_scan", Content: "raw-secret", Params: json.RawMessage(`{"token":"secret"}`),
			AttachmentRefs: json.RawMessage(`["attachment-secret"]`), DispatchError: "dispatch-secret", Status: tasks.StatusRunning,
			CreatedAt: base, UpdatedAt: base.Add(time.Duration(index/2) * time.Hour),
		}
		_, _, err := repository.CreateOrGet(context.Background(), task)
		require.NoError(t, err)
	}
	capture := &dashboardQueryCaptureLogger{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = capture
	service := tasks.NewService(repository, nil, nil)

	items, err := service.Recent(context.Background(), identity.Subject{UserID: "alice-id", Role: identity.RoleUser}, 5)
	require.NoError(t, err)
	require.Len(t, items, 5)
	assert.Equal(t, []string{"task-5", "task-4", "task-3", "task-2", "task-1"}, taskSummaryIDs(items))
	require.Len(t, capture.statements, 1)
	statement := capture.statements[0]
	projection := strings.SplitN(statement, " from ", 2)[0]
	assert.Contains(t, statement, "owner_user_id = 'alice-id'")
	assert.Contains(t, statement, "order by updated_at desc, id desc")
	assert.Contains(t, statement, "limit 5")
	for _, column := range []string{"id", "owner_username", "task_type", "status", "created_at", "updated_at"} {
		assert.Contains(t, projection, column)
	}
	for _, forbidden := range []string{"*", "owner_user_id", "content", "params", "attachment_refs", "engine_session_id", "dispatch_error", "dispatch_claim_token", "dispatch_lease_until"} {
		assert.NotContains(t, projection, forbidden)
	}
}

func taskSummaryIDs(items []tasks.TaskSummary) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func openDashboardTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AIG_TEST_DB_DSN")
	require.NotEmpty(t, dsn, "AIG_TEST_DB_DSN must point to isolated PostgreSQL")
	adminDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	schema := "dashboard_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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

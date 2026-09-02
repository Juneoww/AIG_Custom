package reports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/Juneoww/AIG_Custom/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type mcpWorkbenchBarrierLogger struct {
	logger.Interface
	firstBatch chan struct{}
	resume     chan struct{}
	once       sync.Once
}

func (barrier *mcpWorkbenchBarrierLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	statement, rows := fc()
	wait := false
	lowerStatement := strings.ToLower(statement)
	if strings.Contains(lowerStatement, "report_snapshots") && strings.Contains(lowerStatement, "reports.task_type") && strings.Contains(lowerStatement, "limit 64") {
		barrier.once.Do(func() {
			close(barrier.firstBatch)
			wait = true
		})
	}
	if wait {
		<-barrier.resume
	}
	barrier.Interface.Trace(ctx, begin, func() (string, int64) { return statement, rows }, err)
}

func TestMCPWorkbenchProjectionScopesUTCDayWindowSortsAndCapsWithoutRawResult(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 17, 23, 45, 0, 0, time.FixedZone("CST", 8*60*60))
	lower := utcDay(now).AddDate(0, 0, -29)
	upper := utcDay(now).AddDate(0, 0, 1)
	repository := NewMemoryRepository()
	service := NewService(repository, nil)

	fixtures := []*Snapshot{
		mcpWorkbenchSnapshot(t, "high-new", "task-high-new", "alice", "Mcp-Scan", upper.Add(-time.Hour), RiskSummary{High: 1}, []TechnicalFinding{{Title: "High newest", Category: "dangerous_tool", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "high-tie-a", "task-high-tie-a", "alice", "mcp_scan", upper.Add(-2*time.Hour), RiskSummary{High: 3}, []TechnicalFinding{{Title: "High tie a", Category: "command_file", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "high-tie-b", "task-high-tie-b", "alice", "mcp_scan", upper.Add(-2*time.Hour), RiskSummary{High: 2}, []TechnicalFinding{{Title: "High tie b", Category: "tool_poisoning", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "medium-new", "task-medium-new", "alice", "mcp_scan", upper.Add(-30*time.Minute), RiskSummary{Medium: 1}, []TechnicalFinding{{Title: "Medium newest", Category: "authorization", Severity: "medium"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "low-new", "task-low-new", "alice", "mcp_scan", upper.Add(-15*time.Minute), RiskSummary{Low: 1}, []TechnicalFinding{{Title: "Low newest", Category: "data_leakage", Severity: "low"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "low-old", "task-low-old", "alice", "mcp_scan", lower, RiskSummary{Low: 1}, []TechnicalFinding{{Title: "Low oldest", Category: "other", Severity: "low"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "before-window", "task-before-window", "alice", "mcp_scan", lower.Add(-time.Nanosecond), RiskSummary{High: 100}, []TechnicalFinding{{Title: "Before", Category: "other", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "upper-bound", "task-upper-bound", "alice", "mcp_scan", upper, RiskSummary{High: 100}, []TechnicalFinding{{Title: "Upper", Category: "other", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "bob", "task-bob", "bob", "mcp_scan", upper.Add(-time.Hour), RiskSummary{High: 100}, []TechnicalFinding{{Title: "Bob", Category: "other", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "agent", "task-agent", "alice", "agent_scan", upper.Add(-time.Hour), RiskSummary{High: 100}, []TechnicalFinding{{Title: "Agent", Category: "other", Severity: "high"}}, "report-render-v2"),
	}
	for _, snapshot := range fixtures {
		require.NoError(t, repository.Create(ctx, snapshot))
	}

	projection, err := service.MCPWorkbench(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, now)
	require.NoError(t, err)
	assert.Equal(t, 6, projection.Completed30d)
	assert.Equal(t, 6, projection.HighRisk)
	require.Len(t, projection.Highlights, 5)
	assert.Equal(t, []string{"high-new", "high-tie-b", "high-tie-a", "medium-new", "low-new"}, mcpHighlightReportIDs(projection.Highlights))
	for _, item := range projection.Highlights {
		assert.Equal(t, "alice", ownerForMCPWorkbenchFixture(item.ReportID))
		assert.GreaterOrEqual(t, item.CompletedAt.UTC(), lower)
		assert.True(t, item.CompletedAt.UTC().Before(upper))
	}

	encoded, err := json.Marshal(projection)
	require.NoError(t, err)
	for _, secret := range []string{"raw-result-never-read-sentinel", "before-window", "upper-bound", "Bob", "Agent", "render_data", "raw_result"} {
		assert.NotContains(t, string(encoded), secret)
	}
	assertMCPWorkbenchWireShape(t, encoded)

	global, err := service.MCPWorkbench(ctx, identity.Subject{Role: identity.RoleAuditor}, now)
	require.NoError(t, err)
	assert.Equal(t, 7, global.Completed30d)
	assert.Equal(t, 106, global.HighRisk)
	assert.Contains(t, mcpHighlightReportIDs(global.Highlights), "bob")
	admin, err := service.MCPWorkbench(ctx, identity.Subject{Role: identity.RoleAdmin}, now)
	require.NoError(t, err)
	assert.Equal(t, global, admin)

	_, err = service.MCPWorkbench(ctx, identity.Subject{}, now)
	assert.ErrorIs(t, err, ErrForbidden)
}

func TestMCPWorkbenchProjectionCountsOnlyReportsCompletedInsideWindow(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	lower, upper := mcpWorkbenchWindow(now)
	repository := NewMemoryRepository()
	inside := mcpWorkbenchSnapshot(t, "completed-inside", "task-inside", "alice", "mcp_scan", lower, RiskSummary{}, nil, "report-render-v2")
	inside.CreatedAt = lower.AddDate(-1, 0, 0)
	outside := mcpWorkbenchSnapshot(t, "completed-outside", "task-outside", "alice", "mcp_scan", lower.Add(-time.Nanosecond), RiskSummary{}, nil, "report-render-v2")
	outside.CreatedAt = upper.Add(time.Hour)
	for _, snapshot := range []*Snapshot{inside, outside} {
		require.NoError(t, repository.Create(ctx, snapshot))
	}

	projection, err := NewService(repository, nil).MCPWorkbench(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, now)
	require.NoError(t, err)
	assert.Equal(t, 1, projection.Completed30d)
	assert.Zero(t, projection.HighRisk)
	assert.Empty(t, projection.Highlights)
}

func TestMCPWorkbenchProjectionUsesRiskOnlyFallbackForLegacyAndBadRenderData(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	service := NewService(repository, nil)
	completed := now.Add(-time.Hour)

	legacy := mcpWorkbenchSnapshot(t, "legacy", "task-legacy", "alice", "mcp_scan", completed, RiskSummary{High: 2, Medium: 3, Low: 4}, []TechnicalFinding{{
		Title: "legacy-title-sentinel", Evidence: "legacy-evidence-sentinel", Impact: "legacy-impact-sentinel",
	}}, "report-render-v1")
	badRender := mcpWorkbenchSnapshot(t, "bad-render", "task-bad-render", "alice", "mcp_scan", completed.Add(time.Minute), RiskSummary{High: 1}, nil, "report-render-v2")
	badRenderData, err := json.Marshal(map[string]any{
		"render_version": "report-render-v2", "task_id": badRender.TaskID, "task_type": badRender.TaskType,
		"completed_at": badRender.CompletedAt, "risk": badRender.Risk,
		"technical_findings": "not-an-array", "private": "bad-render-sentinel",
	})
	require.NoError(t, err)
	badRender.RenderData = badRenderData
	invalidCategory := mcpWorkbenchSnapshot(t, "invalid-category", "task-invalid-category", "alice", "mcp_scan", completed.Add(2*time.Minute), RiskSummary{High: 1}, []TechnicalFinding{{
		Title: "invalid-category-title-sentinel", Category: "untrusted-category-sentinel", Severity: "high",
	}}, "report-render-v2")
	noRisk := mcpWorkbenchSnapshot(t, "legacy-no-risk", "task-legacy-no-risk", "alice", "mcp_scan", completed.Add(3*time.Minute), RiskSummary{}, []TechnicalFinding{{
		Title: "no-risk-title-sentinel",
	}}, "report-render-v1")
	for _, snapshot := range []*Snapshot{legacy, badRender, invalidCategory, noRisk} {
		require.NoError(t, repository.Create(ctx, snapshot))
	}

	projection, err := service.MCPWorkbench(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, now)
	require.NoError(t, err)
	assert.Equal(t, 4, projection.HighRisk)
	require.Len(t, projection.Highlights, 5)
	assert.NotContains(t, mcpHighlightReportIDs(projection.Highlights), "legacy-no-risk")

	legacyBySeverity := map[string]MCPRiskHighlight{}
	for _, item := range projection.Highlights {
		if item.ReportID == "legacy" {
			legacyBySeverity[item.Severity] = item
		}
		assert.Equal(t, "other", item.Category)
		assert.LessOrEqual(t, len([]rune(item.Summary)), 160)
	}
	require.Len(t, legacyBySeverity, 3)
	assert.Equal(t, "high", legacyBySeverity["high"].Severity)
	assert.Equal(t, "medium", legacyBySeverity["medium"].Severity)
	assert.Equal(t, "low", legacyBySeverity["low"].Severity)

	encoded, err := json.Marshal(projection)
	require.NoError(t, err)
	for _, secret := range []string{
		"raw-result-never-read-sentinel", "legacy-title-sentinel", "legacy-evidence-sentinel", "legacy-impact-sentinel",
		"bad-render-sentinel", "invalid-category-title-sentinel", "untrusted-category-sentinel", "no-risk-title-sentinel",
	} {
		assert.NotContains(t, string(encoded), secret)
	}
	assertMCPWorkbenchWireShape(t, encoded)
}

func TestBuildSnapshotDoesNotSetMCPCategoryOrSeverityForOtherTaskTypes(t *testing.T) {
	render, _ := renderForTechnicalTest(t, "Agent-Scan", `{"schema_version":"agent-security-report@1","score":50,"results":[{"id":"ASI-001","type":"tool_misuse","title":"Agent finding","description":"safe","level":"high","owasp":"ASI02"}]}`)
	require.Len(t, render.TechnicalFindings, 1)
	assert.Empty(t, render.TechnicalFindings[0].Category)
	assert.Empty(t, render.TechnicalFindings[0].Severity)
	encoded, err := json.Marshal(render.TechnicalFindings[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"category"`)
	assert.NotContains(t, string(encoded), `"severity"`)
}

func TestMCPWorkbenchProjectionDoesNotExposeFindingTitlesOrEndpointParameters(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	snapshot := mcpWorkbenchSnapshot(t, "private-title", "task-private-title", "alice", "mcp_scan", now, RiskSummary{High: 4}, []TechnicalFinding{
		{Title: `Unsafe https://private-mcp.example.test/internal?token=private-query GET /private-route risk_type=private-raw-type`, Category: "command_file", Severity: "high"},
		{Title: `Scheme endpoint: mcp://private-host.internal:8443/admin`, Category: "command_file", Severity: "high"},
		{Title: `Bare endpoint private-host.internal:9443/private`, Category: "command_file", Severity: "high"},
		{Title: `Log {"endpoint":"private-json-host.internal:9444/private","token":"private-token"} endpoint=private-log-host.internal:9445/private log_param=log-param`, Category: "command_file", Severity: "high"},
	}, "report-render-v2")
	require.NoError(t, repository.Create(ctx, snapshot))

	projection, err := NewService(repository, nil).MCPWorkbench(ctx, identity.Subject{UserID: "alice", Role: identity.RoleUser}, now)
	require.NoError(t, err)
	require.Len(t, projection.Highlights, 4)
	for _, highlight := range projection.Highlights {
		assert.Equal(t, "MCP 命令或文件访问风险发现（高风险）。", highlight.Summary)
	}
	encoded, err := json.Marshal(projection)
	require.NoError(t, err)
	for _, privateValue := range []string{
		"private-mcp.example.test", "/internal", "/private-route", "private-query", "private-raw-type",
		"mcp://", "private-host.internal", ":8443/admin", ":9443/private", "private-json-host.internal", ":9444/private",
		"private-token", "private-log-host.internal", ":9445/private", "log-param",
	} {
		assert.NotContains(t, string(encoded), privateValue)
	}
}

func TestGormMCPWorkbenchProjectionMatchesMemoryAndNeverReadsRawResult(t *testing.T) {
	ctx := context.Background()
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	gormRepository := NewGormRepository(db)
	memoryRepository := NewMemoryRepository()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	lower := utcDay(now).AddDate(0, 0, -29)
	fixtures := []*Snapshot{
		mcpWorkbenchSnapshot(t, "gorm-alias", "task-gorm-alias", "alice", "Mcp-Scan", now, RiskSummary{High: 2}, []TechnicalFinding{{Title: "Alias high", Category: "dangerous_tool", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "gorm-legacy", "task-gorm-legacy", "alice", "mcp_scan", now.Add(-time.Minute), RiskSummary{High: 1, Medium: 1}, []TechnicalFinding{{Title: "legacy-title-sentinel"}}, "report-render-v1"),
		mcpWorkbenchSnapshot(t, "gorm-bob", "task-gorm-bob", "bob", "mcp_scan", now, RiskSummary{High: 9}, []TechnicalFinding{{Title: "Bob", Category: "other", Severity: "high"}}, "report-render-v2"),
		mcpWorkbenchSnapshot(t, "gorm-old", "task-gorm-old", "alice", "mcp_scan", lower.Add(-time.Nanosecond), RiskSummary{High: 99}, []TechnicalFinding{{Title: "Old", Category: "other", Severity: "high"}}, "report-render-v2"),
	}
	for _, snapshot := range fixtures {
		require.NoError(t, memoryRepository.Create(ctx, snapshot))
		require.NoError(t, gormRepository.Create(ctx, snapshot))
	}

	query := MCPWorkbenchQuery{OwnerUserID: "alice", Now: now}
	want, err := memoryRepository.MCPWorkbench(ctx, query)
	require.NoError(t, err)
	capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = capture
	got, err := gormRepository.MCPWorkbench(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	require.Len(t, capture.statements, 1)
	statement := capture.statements[0]
	assert.Contains(t, statement, "reports.task_type in")
	assert.Contains(t, statement, "reports.owner_user_id = 'alice'")
	assert.Contains(t, statement, "reports.completed_at >= '2026-07-19 00:00:00'")
	assert.Contains(t, statement, "reports.completed_at < '2026-08-18 00:00:00'")
	assert.Contains(t, statement, "risk_summary")
	assert.Contains(t, statement, "render_data")
	assert.NotContains(t, statement, "raw_result")
	assert.NotContains(t, string(mustJSON(t, got)), "raw-result-never-read-sentinel")
}

func TestGormMCPWorkbenchBatchesHighCardinalityProjection(t *testing.T) {
	const reportCount = 66
	const expectedBatchSize = 64
	ctx := context.Background()
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	gormRepository := NewGormRepository(db)
	memoryRepository := NewMemoryRepository()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	aliases := mcpTaskTypeAliases()
	require.Equal(t, []string{"Mcp-Scan", "mcp_scan"}, aliases)
	for _, alias := range aliases {
		kind, ok := riskTaskType(alias)
		require.True(t, ok, alias)
		assert.Equal(t, "mcp", kind)
	}

	for index := 0; index < reportCount; index++ {
		taskType := aliases[index%len(aliases)]
		snapshot := mcpWorkbenchSnapshot(t,
			fmt.Sprintf("batch-%03d", index),
			fmt.Sprintf("task-batch-%03d", index),
			"alice",
			taskType,
			now,
			RiskSummary{High: 1},
			[]TechnicalFinding{{Title: "scanner-controlled-title", Category: "command_file", Severity: "high"}},
			"report-render-v2",
		)
		require.NoError(t, memoryRepository.Create(ctx, snapshot))
		require.NoError(t, gormRepository.Create(ctx, snapshot))
	}

	query := MCPWorkbenchQuery{OwnerUserID: "alice", Now: now}
	want, err := memoryRepository.MCPWorkbench(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, reportCount, want.HighRisk)
	require.Len(t, want.Highlights, 5)
	assert.Equal(t, []string{"batch-065", "batch-064", "batch-063", "batch-062", "batch-061"}, mcpHighlightReportIDs(want.Highlights))

	capture := &queryCaptureLogger{Interface: logger.Default.LogMode(logger.Silent)}
	db.Config.Logger = capture
	got, err := gormRepository.MCPWorkbench(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, reportCount, got.HighRisk)
	require.Len(t, got.Highlights, 5)
	require.GreaterOrEqual(t, len(capture.statements), 2, "high-cardinality projection must use bounded batches")
	for _, statement := range capture.statements {
		assert.Contains(t, statement, fmt.Sprintf("limit %d", expectedBatchSize))
		assert.Contains(t, statement, "reports.task_type in")
		assert.NotContains(t, statement, "raw_result")
	}
}

func TestGormMCPWorkbenchUsesOneReadOnlyRepeatableReadSnapshot(t *testing.T) {
	const existingReportCount = 65
	ctx := context.Background()
	db := openReportsTestDB(t)
	require.NoError(t, database.Migrate(db))
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	for index := 0; index < existingReportCount; index++ {
		snapshot := mcpWorkbenchSnapshot(t,
			fmt.Sprintf("mcp-tx-%03d", index),
			fmt.Sprintf("task-mcp-tx-%03d", index),
			"alice",
			mcpTaskTypeAliases()[index%len(mcpTaskTypeAliases())],
			now,
			RiskSummary{High: 1},
			[]TechnicalFinding{{Category: "command_file", Severity: "high"}},
			"report-render-v2",
		)
		require.NoError(t, NewGormRepository(db).Create(ctx, snapshot))
	}

	barrier := &mcpWorkbenchBarrierLogger{
		Interface: logger.Default.LogMode(logger.Silent), firstBatch: make(chan struct{}), resume: make(chan struct{}),
	}
	defer func() {
		select {
		case <-barrier.resume:
		default:
			close(barrier.resume)
		}
	}()
	readDB := db.Session(&gorm.Session{Logger: barrier})
	var settings struct {
		isolation string
		readOnly  string
		err       error
	}
	var settingsOnce sync.Once
	captureSettings := func(queryDB *gorm.DB) {
		settingsOnce.Do(func() {
			settings.err = queryDB.Statement.ConnPool.QueryRowContext(queryDB.Statement.Context, "SHOW transaction_isolation").Scan(&settings.isolation)
			if settings.err == nil {
				settings.err = queryDB.Statement.ConnPool.QueryRowContext(queryDB.Statement.Context, "SHOW transaction_read_only").Scan(&settings.readOnly)
			}
		})
	}
	require.NoError(t, readDB.Callback().Query().Before("gorm:query").Register("test:mcp_workbench_transaction_settings", captureSettings))

	type workbenchResult struct {
		projection MCPWorkbenchProjection
		err        error
	}
	result := make(chan workbenchResult, 1)
	query := MCPWorkbenchQuery{OwnerUserID: "alice", Now: now}
	go func() {
		projection, err := NewGormRepository(readDB).MCPWorkbench(ctx, query)
		result <- workbenchResult{projection: projection, err: err}
	}()
	select {
	case <-barrier.firstBatch:
	case completed := <-result:
		require.NoError(t, completed.err)
		t.Fatal("MCP workbench completed before first-batch barrier")
	}

	concurrent := mcpWorkbenchSnapshot(t,
		"mcp-tx-999", "task-mcp-tx-999", "alice", "mcp_scan", now,
		RiskSummary{High: 1}, []TechnicalFinding{{Category: "command_file", Severity: "high"}}, "report-render-v2",
	)
	writerDB := db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, NewGormRepository(writerDB).Create(ctx, concurrent))
	close(barrier.resume)

	completed := <-result
	require.NoError(t, completed.err)
	require.NoError(t, settings.err)
	assert.Equal(t, "repeatable read", settings.isolation)
	assert.Equal(t, "on", settings.readOnly)
	assert.Equal(t, existingReportCount, completed.projection.HighRisk)
	assert.NotContains(t, mcpHighlightReportIDs(completed.projection.Highlights), concurrent.ID)

	next, err := NewGormRepository(writerDB).MCPWorkbench(ctx, query)
	require.NoError(t, err)
	assert.Equal(t, existingReportCount+1, next.HighRisk)
	assert.Contains(t, mcpHighlightReportIDs(next.Highlights), concurrent.ID)
}

func mcpWorkbenchSnapshot(t *testing.T, reportID, taskID, owner, taskType string, completedAt time.Time, risk RiskSummary, findings []TechnicalFinding, renderVersion string) *Snapshot {
	t.Helper()
	renderData, err := json.Marshal(RenderModel{
		RenderVersion:     renderVersion,
		TaskID:            taskID,
		TaskType:          taskType,
		CompletedAt:       completedAt.UTC(),
		Risk:              risk,
		TechnicalFindings: findings,
	})
	require.NoError(t, err)
	return &Snapshot{
		ID: reportID, TaskID: taskID, OwnerUserID: owner, TaskType: taskType,
		CompletedAt: completedAt.UTC(), CreatedAt: completedAt.UTC(),
		RawResult: json.RawMessage(`{"raw":"raw-result-never-read-sentinel"}`),
		Risk:      risk, RenderData: renderData,
	}
}

func mcpHighlightReportIDs(items []MCPRiskHighlight) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ReportID)
	}
	return ids
}

func ownerForMCPWorkbenchFixture(reportID string) string {
	if reportID == "bob" {
		return "bob"
	}
	return "alice"
}

func assertMCPWorkbenchWireShape(t *testing.T, encoded []byte) {
	t.Helper()
	var projection map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &projection))
	assert.ElementsMatch(t, []string{"completed_30d", "high_risk", "highlights"}, mapJSONKeys(projection))
	var highlights []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(projection["highlights"], &highlights))
	for _, item := range highlights {
		assert.ElementsMatch(t, []string{"report_id", "task_id", "severity", "category", "summary", "completed_at"}, mapJSONKeys(item))
	}
}

func mapJSONKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

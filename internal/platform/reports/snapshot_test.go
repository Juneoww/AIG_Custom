package reports

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/common/portscan"
	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/Juneoww/AIG_Custom/internal/platform/identity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryRepositorySnapshotsAreImmutableAndUniquePerTask(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	candidate := reportSnapshotForTest(t, "snapshot-1", "task-1", "alice", time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC))
	candidate.Brand.Logo = []byte("logo-v1")
	require.NoError(t, repository.Create(ctx, candidate))
	candidate.RawResult[2] = 'X'
	candidate.RenderData[2] = 'X'
	candidate.Brand.Logo[0] = 'X'

	stored, err := repository.Get(ctx, candidate.ID)
	require.NoError(t, err)
	assert.JSONEq(t, string(event(`{"score":100,"results":[]}`)), string(stored.RawResult))
	assert.JSONEq(t, string(reportSnapshotForTest(t, "expected", "task-1", "alice", candidate.CompletedAt).RenderData), string(stored.RenderData))
	assert.Equal(t, []byte("logo-v1"), stored.Brand.Logo)
	stored.RawResult[2] = 'Y'
	stored.RenderData[2] = 'Y'
	stored.Brand.Logo[0] = 'Y'

	again, err := repository.GetByTaskID(ctx, candidate.TaskID)
	require.NoError(t, err)
	assert.JSONEq(t, string(event(`{"score":100,"results":[]}`)), string(again.RawResult))
	assert.JSONEq(t, string(reportSnapshotForTest(t, "expected-again", "task-1", "alice", candidate.CompletedAt).RenderData), string(again.RenderData))
	assert.Equal(t, []byte("logo-v1"), again.Brand.Logo)
	duplicate := reportSnapshotForTest(t, "snapshot-2", candidate.TaskID, "alice", candidate.CompletedAt)
	require.True(t, errors.Is(repository.Create(ctx, duplicate), ErrSnapshotExists))

	unchanged, err := repository.Get(ctx, candidate.ID)
	require.NoError(t, err)
	assert.Equal(t, "snapshot-1", unchanged.ID)
}

func TestMemoryRepositoryTrendFiltersOwnerAndTimeWindow(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	now := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	for _, snapshot := range []*Snapshot{
		reportSnapshotForTest(t, "old", "old-task", "alice", now.AddDate(0, 0, -31)),
		reportSnapshotForTest(t, "other", "other-task", "bob", now.AddDate(0, 0, -1)),
		reportSnapshotForTest(t, "first", "first-task", "alice", now.AddDate(0, 0, -2)),
		reportSnapshotForTest(t, "last", "last-task", "alice", now.AddDate(0, 0, -1)),
	} {
		switch snapshot.ID {
		case "old", "other":
			snapshot.Risk.High = 9
		case "first":
			snapshot.Risk.High, snapshot.Risk.Medium = 1, 2
		case "last":
			snapshot.Risk.Low = 3
		}
		require.NoError(t, repository.Create(ctx, snapshot))
	}

	trend, err := repository.Trend(ctx, TrendQuery{Now: now, Days: 30, OwnerUserID: "alice"})
	require.NoError(t, err)
	require.Len(t, trend, 30)
	assert.Equal(t, 1, trend[27].Completed)
	assert.Equal(t, 1, trend[27].High)
	assert.Equal(t, 2, trend[27].Medium)
	assert.Equal(t, 3, trend[28].Low)

	global, err := repository.Trend(ctx, TrendQuery{Now: now, Days: 30})
	require.NoError(t, err)
	require.Len(t, global, 30)
	assert.Equal(t, 9, global[28].High)
	assert.Equal(t, 3, global[28].Low)
}

func TestMemoryRepositoryTrendUsesInclusiveUTCNaturalDayBuckets(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	now := time.Date(2026, 8, 12, 18, 0, 0, 0, time.UTC)
	for _, snapshot := range []*Snapshot{
		reportSnapshotForTest(t, "inside", "inside-task", "alice", now.AddDate(0, 0, -29)),
		reportSnapshotForTest(t, "outside", "outside-task", "alice", now.AddDate(0, 0, -30)),
	} {
		require.NoError(t, repository.Create(ctx, snapshot))
	}

	trend, err := repository.Trend(ctx, TrendQuery{Now: now, Days: 30, OwnerUserID: "alice"})
	require.NoError(t, err)
	require.Len(t, trend, 30)
	assert.Equal(t, time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC), trend[0].Date)
	assert.Equal(t, 1, trend[0].Completed)
	assert.Equal(t, time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC), trend[29].Date)
}

func TestBuildSnapshotSeparatesEngineCompletionFromReportGenerationAndPersistsRenderModel(t *testing.T) {
	completedAt := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	generatedAt := completedAt.Add(3 * time.Hour)
	raw := json.RawMessage(event(`{"score":73,"results":[{"level":"high"},{"level":"low"}]}`))
	snapshot, err := BuildSnapshotAt("task-render", "alice", "mcp_scan", raw, brand.Config{ProductName: "企业安全平台", PrimaryColor: "#1677FF", Watermark: "内部"}, completedAt, generatedAt)
	require.NoError(t, err)
	assert.Equal(t, completedAt, snapshot.CompletedAt)
	assert.Equal(t, generatedAt, snapshot.CreatedAt)
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	assert.Equal(t, "report-render-v2", render.RenderVersion)
	assert.Equal(t, generatedAt, render.GeneratedAt)
	assert.Equal(t, completedAt, render.CompletedAt)
	assert.Equal(t, snapshot.Risk, render.Risk)
	assert.Contains(t, render.ScoreExplanation, "risk-v2")
	require.Len(t, render.RiskTrend, 30)
	assert.Equal(t, generatedAt.Truncate(24*time.Hour), render.RiskTrend[29].Date)
	assert.Equal(t, 1, render.RiskTrend[29].Completed)
	assert.Equal(t, snapshot.Risk.High, render.RiskTrend[29].High)
	assert.NotEmpty(t, render.Recommendations)
	raw[2] = 'X'
	assert.NotContains(t, string(snapshot.RenderData), "X")
}

func TestBuildSnapshotRetainsBrandHistory(t *testing.T) {
	ctx := context.Background()
	brands := brand.NewService(brand.NewMemoryRepository())
	admin := identity.Subject{Role: identity.RoleAdmin}
	brandV1, err := brands.Update(ctx, admin, brand.Config{ProductName: "v1", PrimaryColor: "#1677FF"})
	require.NoError(t, err)
	first, err := BuildSnapshot("task-1", "alice", "mcp_scan", json.RawMessage(event(`{"score":100,"results":[]}`)), brandV1, time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	brandV2, err := brands.Update(ctx, admin, brand.Config{ProductName: "v2", PrimaryColor: "#1677FF"})
	require.NoError(t, err)
	second, err := BuildSnapshot("task-2", "alice", "mcp_scan", json.RawMessage(event(`{"score":100,"results":[]}`)), brandV2, time.Date(2026, 8, 12, 1, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "v1", first.Brand.ProductName)
	assert.Equal(t, "v2", second.Brand.ProductName)
}

func TestPreparePersistsOnlyWhitelistedInfrastructurePortScanMetadata(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryRepository(), brand.NewService(brand.NewMemoryRepository()))
	completedAt := time.Date(2026, 8, 12, 8, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name     string
		taskType string
		mode     portscan.Mode
		spec     string
		wantMode string
		wantSpec string
	}{
		{name: "fixed", taskType: "ai_infra_scan", mode: portscan.FixedAI, spec: portscan.FixedAIPortSpec, wantMode: "fixed_ai", wantSpec: portscan.FixedAIPortSpec},
		{name: "full", taskType: "ai_infra_scan", mode: portscan.FullTCP, spec: portscan.FullTCPPortSpec, wantMode: "full_tcp", wantSpec: portscan.FullTCPPortSpec},
		{name: "mismatched spec is omitted", taskType: "ai_infra_scan", mode: portscan.FixedAI, spec: portscan.FullTCPPortSpec},
		{name: "unknown mode is omitted", taskType: "ai_infra_scan", mode: "agent-supplied", spec: "sentinel-port-spec"},
		{name: "non infrastructure task is omitted", taskType: "mcp_scan", mode: portscan.FixedAI, spec: portscan.FixedAIPortSpec},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot, err := service.Prepare(ctx, CompletedTask{
				TaskID: "report-" + testCase.name, OwnerUserID: "alice", TaskType: testCase.taskType,
				RawResult: event(`{"score":100,"results":[]}`), CompletedAt: completedAt,
				PortScanMode: testCase.mode, PortSpec: testCase.spec,
			})
			require.NoError(t, err)
			var render RenderModel
			require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
			assert.Equal(t, testCase.wantMode, render.PortScanMode)
			assert.Equal(t, testCase.wantSpec, render.PortSpec)
		})
	}
}

func reportSnapshotForTest(t *testing.T, id, taskID, owner string, completedAt time.Time) *Snapshot {
	t.Helper()
	snapshot, err := BuildSnapshotAt(taskID, owner, "mcp_scan", event(`{"score":100,"results":[]}`), brand.Config{ProductName: "v1", PrimaryColor: "#1677FF"}, completedAt, completedAt)
	require.NoError(t, err)
	snapshot.ID = id
	return snapshot
}

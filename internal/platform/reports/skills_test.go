package reports

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillsRiskAcceptsPlatformAndEngineTypes(t *testing.T) {
	raw := event(`{"score":61,"results":[{"level":"high"},{"level":"中危"},{"level":"low"}]}`)
	for _, taskType := range []string{"skills_scan", "Skills-Scan"} {
		t.Run(taskType, func(t *testing.T) {
			risk, err := MapRisk(taskType, raw)
			require.NoError(t, err)
			assert.Equal(t, RiskSummary{MappingVersion: "risk-v2", Score: 61, High: 1, Medium: 1, Low: 1}, risk)
		})
	}
}

func TestSkillsRiskRejectsMalformedResults(t *testing.T) {
	for _, result := range []string{
		`{"score":101,"results":[]}`,
		`{"score":100,"results":null}`,
		`{"score":100}`,
		`{"score":70,"results":[{}]}`,
		`{"score":70,"results":[{"level":" "}]}`,
	} {
		t.Run(result, func(t *testing.T) {
			_, err := MapRisk("skills_scan", event(result))
			assert.ErrorIs(t, err, ErrInvalidFindings)
		})
	}
}

func TestSkillsSnapshotPreservesIdentityAndTechnicalFindings(t *testing.T) {
	ctx := context.Background()
	repository := NewMemoryRepository()
	completedAt := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	raw := event(`{"score":72,"results":[{"title":"越权读取","description":"Skill 要求读取授权范围外的数据。","risk_type":"越权指令","level":"high","suggestion":"将读取范围限制为用户授权目录。"}]}`)
	for _, taskType := range []string{"mcp_scan", "skills_scan"} {
		snapshot, err := BuildSnapshotAt("task-"+taskType, "alice", taskType, raw, brand.Config{ProductName: "安全平台"}, completedAt, completedAt)
		require.NoError(t, err)
		require.NoError(t, repository.Create(ctx, snapshot))

		stored, err := repository.GetByTaskID(ctx, "task-"+taskType)
		require.NoError(t, err)
		assert.Equal(t, taskType, stored.TaskType)
		var render RenderModel
		require.NoError(t, json.Unmarshal(stored.RenderData, &render))
		assert.Equal(t, taskType, render.TaskType)
		require.Len(t, render.TechnicalFindings, 1)
		assert.Equal(t, "越权读取", render.TechnicalFindings[0].Title)
		assert.Equal(t, "Skill 要求读取授权范围外的数据。", render.TechnicalFindings[0].Evidence)
		assert.Equal(t, "将读取范围限制为用户授权目录。", render.TechnicalFindings[0].Remediation)
		assert.Equal(t, RiskSummary{MappingVersion: "risk-v2", Score: 72, High: 1}, render.Risk)
	}

	snapshots, err := repository.List(ctx, ListQuery{OwnerUserID: "alice"})
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	assert.ElementsMatch(t, []string{"mcp_scan", "skills_scan"}, []string{snapshots[0].TaskType, snapshots[1].TaskType})
}

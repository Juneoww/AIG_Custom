package reports

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/stretchr/testify/require"
)

func TestMCPBasicReportExplicitlyLimitsCoverageWithoutRawText(t *testing.T) {
	raw := event(`{"analysis_mode":"basic","readme":"private-code-and-credentials","score":100,"results":[]}`)
	snapshot, err := BuildSnapshot("task", "owner", "mcp_scan", raw, brand.Config{}, time.Now())
	require.NoError(t, err)
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	require.Contains(t, render.Coverage, "基础检查（未使用模型）")
	require.Contains(t, render.Coverage, "未执行服务工具")
	require.Contains(t, render.Conclusion, "不代表已完成全面安全评估")
	require.NotContains(t, string(snapshot.RenderData), "private-code-and-credentials")
}

func TestMCPAnalysisLabelNeverTrustsArbitraryMetadata(t *testing.T) {
	for _, mode := range []string{"model_assisted", "private-mode-sentinel", ""} {
		payload, err := json.Marshal(map[string]any{"analysis_mode": mode, "score": 100, "results": []any{}})
		require.NoError(t, err)
		snapshot, err := BuildSnapshot("task", "owner", "mcp_scan", event(string(payload)), brand.Config{}, time.Now())
		require.NoError(t, err)
		require.NotContains(t, string(snapshot.RenderData), "private-mode-sentinel")
		require.NotContains(t, string(snapshot.RenderData), "基础检查（未使用模型）")
	}
}

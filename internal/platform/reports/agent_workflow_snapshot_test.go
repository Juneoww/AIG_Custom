package reports

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/stretchr/testify/require"
)

// TestAgentWorkflowSnapshotSupportsPythonOWASP 验证真实 Python 数组格式及历史字符串都能生成安全快照。
func TestAgentWorkflowSnapshotSupportsPythonOWASP(t *testing.T) {
	for _, testCase := range []struct{ name, categories, want string }{
		{"legacy_string", `"ASI03"`, "ASI03"},
		{"python_array", `["ASI03"]`, "ASI03"},
		{"multiple_categories", `["ASI03","ASI09"]`, "ASI03, ASI09"},
		{"empty_array", `[]`, ""},
		{"empty_string", `""`, ""},
		{"legacy_null", `null`, ""},
		{"omitted", ``, ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot, err := BuildSnapshot("task", "owner", "Agent-Scan", event(agentWorkflowReportWithOWASP(testCase.categories)), brand.Config{}, time.Now())
			require.NoError(t, err)
			require.Equal(t, 1, snapshot.Risk.High)
			var render RenderModel
			require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
			require.Len(t, render.TechnicalFindings, 1)
			if testCase.want != "" {
				require.Contains(t, render.TechnicalFindings[0].Evidence, "OWASP: "+testCase.want)
			} else {
				require.NotContains(t, render.TechnicalFindings[0].Evidence, "OWASP:")
			}
			require.NotContains(t, string(snapshot.RenderData), `"conversation"`)
			require.NotContains(t, string(snapshot.RenderData), "private prompt")
			require.NotContains(t, string(snapshot.RenderData), "private response")
		})
	}
}

func TestAgentWorkflowSnapshotRejectsMalformedOWASP(t *testing.T) {
	for _, testCase := range []struct{ name, categories string }{
		{"object", `{"id":"ASI03"}`},
		{"number", `3`},
		{"boolean", `true`},
		{"number_element", `["ASI03",3]`},
		{"boolean_element", `["ASI03",false]`},
		{"null_element", `["ASI03",null]`},
		{"object_element", `["ASI03",{"id":"ASI09"}]`},
		{"nested_array", `["ASI03",["ASI09"]]`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot, err := BuildSnapshot("task", "owner", "agent_scan", event(agentWorkflowReportWithOWASP(testCase.categories)), brand.Config{}, time.Now())
			require.ErrorIs(t, err, ErrInvalidSnapshot)
			require.Nil(t, snapshot)
		})
	}
}

func TestAgentWorkflowSnapshotRedactsOWASPCategories(t *testing.T) {
	categories, err := json.Marshal([]string{"ASI03", "token=agent-owasp-private", "Bearer agent-owasp-bearer", strings.Repeat("分类", 200)})
	require.NoError(t, err)
	snapshot, err := BuildSnapshot("task", "owner", "agent_scan", event(agentWorkflowReportWithOWASP(string(categories))), brand.Config{}, time.Now())
	require.NoError(t, err)
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	require.Len(t, render.TechnicalFindings, 1)
	evidence := render.TechnicalFindings[0].Evidence
	require.Contains(t, evidence, "OWASP: ASI03")
	require.Contains(t, evidence, "[REDACTED]")
	require.LessOrEqual(t, len(evidence), maxFindingTextBytes)
	for _, secret := range []string{"agent-owasp-private", "agent-owasp-bearer", "private prompt", "private response"} {
		require.NotContains(t, string(snapshot.RenderData), secret)
	}
}

func agentWorkflowReportWithOWASP(categories string) string {
	field := ""
	if categories != "" {
		field = `,"owasp":` + categories
	}
	return fmt.Sprintf(`{"schema_version":"agent-security-report@1","score":85,"risk_type":"high","total_tests":2,"vulnerable_tests":1,"results":[{"id":"f-001","type":"identity_abuse","title":"Ownership missing","description":"Restricted account record returned","level":"High"%s,"suggestion":"Check ownership","conversation":[{"prompt":"private prompt","response":"private response"}]}]}`, field)
}

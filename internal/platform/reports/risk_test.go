package reports

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapRiskV2MapsOnlyKnownEventSchemasAndTaskAliases(t *testing.T) {
	tests := []struct {
		name, taskType string
		raw            []byte
		want           RiskSummary
	}{
		{"ai", "AI-Infra-Scan", event(`{"score":73,"results":[{"vulnerabilities":[{"severity":"critical"},{"severity":"high"},{"severity":"严重"},{"severity":"高危"},{"severity":"medium"},{"severity":"中危"},{"severity":"low"},{"severity":"info"},{"severity":"unknown"},{"severity":"低危"}]}]}`), RiskSummary{MappingVersion: "risk-v2", High: 4, Medium: 2, Low: 4, Score: 73}},
		{"ai snake alias", "ai_infra_scan", event(`{"score":73,"results":[{"vulnerabilities":[{"severity":"critical"},{"severity":"high"},{"severity":"严重"},{"severity":"高危"},{"severity":"medium"},{"severity":"中危"},{"severity":"low"},{"severity":"info"},{"severity":"unknown"},{"severity":"低危"}]}]}`), RiskSummary{MappingVersion: "risk-v2", High: 4, Medium: 2, Low: 4, Score: 73}},
		{"mcp", "Mcp-Scan", event(`{"score":66,"results":[{"level":"high","risk_type":"low"},{"level":"medium"},{"level":"low"},{"level":"unknown"}]}`), RiskSummary{MappingVersion: "risk-v2", High: 1, Medium: 1, Low: 2, Score: 66}},
		{"agent", "agent_scan", event(`{"score":48,"schema_version":"agent-security-report@1","results":[{"level":"高危"},{"level":"中危"},{"level":"info"}]}`), RiskSummary{MappingVersion: "risk-v2", High: 1, Medium: 1, Low: 1, Score: 48}},
		{"prompt", "Model-Redteam-Report", event(`{"msgType":"json","status":"ok","content":[{"total":10,"jailbreak":3,"score":70,"results":[{"ignored":true}]},{"total":5,"jailbreak":2,"score":60,"results":[{}]}]}`), RiskSummary{MappingVersion: "risk-v2", High: 5, Score: 66}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			risk, err := MapRisk(testCase.taskType, testCase.raw)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, risk)
		})
	}
}

func TestMapRiskV2AIProductionResultAllowsOmittedVulnerabilities(t *testing.T) {
	risk, err := MapRisk("AI-Infra-Scan", event(`{
		"total":1,
		"score":93,
		"results":[{
			"target_url":"http://safe-target.test",
			"status_code":200,
			"title":"healthy",
			"fingerprint":"safe-service"
		}]
	}`))

	require.NoError(t, err)
	assert.Equal(t, RiskSummary{MappingVersion: "risk-v2", Score: 93}, risk)
}

func TestMapRiskV2AgentRequiresProductionSchemaVersion(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		result string
	}{
		{"missing", `{"score":88,"results":[{"level":"low"}]}`},
		{"wrong", `{"score":88,"schema_version":"1.0","results":[{"level":"low"}]}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := MapRisk("Agent-Scan", event(testCase.result))
			assert.ErrorIs(t, err, ErrInvalidFindings)
		})
	}
}

func TestMapRiskV2RejectsUntrustedOrMalformedShapes(t *testing.T) {
	valid := event(`{"score":50,"results":[{"level":"low"}]}`)
	for _, testCase := range []struct {
		name, taskType string
		raw            []byte
	}{
		{"root string", "mcp_scan", []byte(`"safe"`)},
		{"root array", "mcp_scan", []byte(`[]`)},
		{"data wrapper", "mcp_scan", event(`{"data":{"score":50,"results":[{"level":"low"}]}}`)},
		{"root findings", "mcp_scan", []byte(`{"findings":[{"severity":"high"}]}`)},
		{"unknown task", "Model-Jailbreak", valid},
		{"unknown spelling", "MCP-Scan", valid},
		{"score above range", "mcp_scan", event(`{"score":101,"results":[{"level":"low"}]}`)},
		{"score below range", "mcp_scan", event(`{"score":-1,"results":[{"level":"low"}]}`)},
		{"missing event type", "mcp_scan", []byte(`{"id":"e","timestamp":1,"result":{"score":50,"results":[{"level":"low"}]}}`)},
		{"wrong event type", "mcp_scan", []byte(`{"id":"e","type":"statusUpdate","timestamp":1,"result":{"score":50,"results":[{"level":"low"}]}}`)},
		{"non-numeric event timestamp", "mcp_scan", []byte(`{"id":"e","type":"resultUpdate","timestamp":"1","result":{"score":50,"results":[{"level":"low"}]}}`)},
		{"missing result", "mcp_scan", []byte(`{"id":"e","type":"resultUpdate","timestamp":1}`)},
		{"missing level", "mcp_scan", event(`{"score":50,"results":[{}]}`)},
		{"prompt jailbreak above total", "model_redteam_report", event(`{"msgType":"json","content":[{"total":2,"jailbreak":3,"results":[]}],"status":"ok"}`)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := MapRisk(testCase.taskType, testCase.raw)
			assert.True(t, errors.Is(err, ErrInvalidFindings))
		})
	}
}

func event(result string) []byte {
	return []byte(`{"id":"event-1","type":"resultUpdate","timestamp":1,"result":` + result + `}`)
}

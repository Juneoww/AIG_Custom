package reports

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Juneoww/AIG_Custom/internal/platform/brand"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSnapshotMapsProductionTechnicalFindingsWithoutRawSecrets(t *testing.T) {
	tests := []struct {
		name     string
		taskType string
		result   string
		want     func(*testing.T, RenderModel)
		secrets  []string
	}{
		{
			name: "ai infra vulnerability whitelist", taskType: "AI-Infra-Scan",
			result: `{
				"score":73,
				"results":[
					{"target_url":"https://healthy.example","fingerprint":"safe-service"},
					{"target_url":"https://alice:url-pass@example.test/admin?token=query-secret#fragment","title":"remote-title-secret","reason":"reason-secret","screenshot":"screenshot-secret","fingerprint":"nginx","vulnerabilities":[{
						"name":"SQL injection","cve":"CVE-2026-0001","summary":"SQL injection summary","details":"May expose protected records.","cvss":"9.8","severity":"high","security_advise":"Upgrade and retest.",
						"references":["https://advisories.example/CVE-2026-0001?token=reference-secret","javascript:alert(1)"]
					}]}
				]
			}`,
			want: func(t *testing.T, render RenderModel) {
				require.Len(t, render.TechnicalFindings, 1)
				finding := render.TechnicalFindings[0]
				assert.Contains(t, finding.Title, "CVE-2026-0001")
				assert.Contains(t, finding.Evidence, "https://example.test/admin")
				assert.Contains(t, finding.Evidence, "nginx")
				assert.Contains(t, finding.Evidence, "9.8")
				assert.Contains(t, finding.Evidence, "https://advisories.example/CVE-2026-0001")
				assert.Contains(t, finding.Impact, "protected records")
				assert.Contains(t, finding.Remediation, "Upgrade")
			},
			secrets: []string{"url-pass", "query-secret", "reference-secret", "remote-title-secret", "reason-secret", "screenshot-secret", "javascript:"},
		},
		{
			name: "mcp model text is redacted", taskType: "Mcp-Scan",
			result: `{"score":60,"readme":"readme-secret","results":[{
				"title":"Unsafe command execution","description":"Bearer mcp-bearer found at C:\\Users\\alice\\private\\config.json","risk_type":"command_injection","level":"high","suggestion":"Rotate api_key=mcp-api-key and restrict commands"
			}]}`,
			want: func(t *testing.T, render RenderModel) {
				require.Len(t, render.TechnicalFindings, 1)
				finding := render.TechnicalFindings[0]
				assert.Contains(t, finding.Title, "Unsafe command execution")
				assert.Contains(t, finding.Evidence, "[REDACTED]")
				assert.Contains(t, finding.Evidence, "[USER_PATH]")
				assert.Equal(t, "command_file", finding.Category)
				assert.Equal(t, "high", finding.Severity)
				assert.NotContains(t, finding.Impact, "command_injection")
				assert.Contains(t, finding.Remediation, "[REDACTED]")
			},
			secrets: []string{"mcp-bearer", "mcp-api-key", `C:\Users\alice`, "readme-secret"},
		},
		{
			name: "agent conversation is excluded", taskType: "Agent-Scan",
			result: `{"schema_version":"agent-security-report@1","score":55,"agent_name":"agent-secret","results":[{
				"id":"ASI-001","type":"tool_misuse","title":"Unsafe tool use","description":"Password=agent-password was exposed","level":"medium","owasp":"ASI02","suggestion":"Apply least privilege","conversation":[{"prompt":"conversation-prompt-secret","response":"conversation-response-secret"}]
			}]}`,
			want: func(t *testing.T, render RenderModel) {
				require.Len(t, render.TechnicalFindings, 1)
				finding := render.TechnicalFindings[0]
				assert.Contains(t, finding.Title, "Unsafe tool use")
				assert.Contains(t, finding.Evidence, "ASI-001")
				assert.Contains(t, finding.Evidence, "ASI02")
				assert.Contains(t, finding.Evidence, "[REDACTED]")
				assert.Contains(t, finding.Impact, "tool_misuse")
				assert.Contains(t, finding.Remediation, "least privilege")
			},
			secrets: []string{"agent-password", "conversation-prompt-secret", "conversation-response-secret", "agent-secret"},
		},
		{
			name: "prompt only exposes jailbreak classification", taskType: "Model-Redteam-Report",
			result: `{"msgType":"json","status":"ok","content":[{"modelName":"model-secret","total":4,"jailbreak":1,"results":[
				{"status":"Jailbreak","vulnerability":"Prompt Injection","attackMethod":"Roleplay","reason":"Bearer prompt-bearer bypassed safeguards","originalInput":"original-secret","input":"input-secret","output":"output-secret","error":"error-secret","attachment":"C:\\Users\\alice\\artifact-secret.json"},
				{"status":"Safe","vulnerability":"Safe sentinel","reason":"safe-reason-secret"},
				{"status":"Exception","vulnerability":"Exception sentinel","error":"exception-secret"},
				{"status":"SimulationFailed","vulnerability":"Failure sentinel","output":"simulation-secret"}
			]}]}`,
			want: func(t *testing.T, render RenderModel) {
				require.Len(t, render.TechnicalFindings, 1)
				finding := render.TechnicalFindings[0]
				assert.Contains(t, finding.Title, "Prompt Injection")
				assert.Contains(t, finding.Title, "Roleplay")
				assert.Contains(t, finding.Evidence, "[REDACTED]")
				assert.Contains(t, finding.Impact, "越狱")
				assert.NotEmpty(t, finding.Remediation)
			},
			secrets: []string{"prompt-bearer", "original-secret", "input-secret", "output-secret", "error-secret", "artifact-secret", "model-secret", "Safe sentinel", "Exception sentinel", "Failure sentinel"},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			render, encoded := renderForTechnicalTest(t, testCase.taskType, testCase.result)
			testCase.want(t, render)
			assert.Equal(t, "report-render-v2", render.RenderVersion)
			for _, secret := range testCase.secrets {
				assert.NotContains(t, encoded, secret)
			}
		})
	}
}

func TestBuildSnapshotRedactsNaturalLanguageAndStructuredCredentialPatterns(t *testing.T) {
	snapshot, sentinels := credentialLeakSnapshot(t)
	encoded := string(snapshot.RenderData)
	assert.Contains(t, encoded, "[REDACTED]")
	for _, sentinel := range sentinels {
		assert.NotContains(t, encoded, sentinel)
	}
}

func TestBuildSnapshotTechnicalFindingsUseStableSeverityOrderAndSafetyBudgets(t *testing.T) {
	results := make([]map[string]any, 0, 51)
	for index := 0; index < 51; index++ {
		level := "low"
		if index%3 == 1 {
			level = "high"
		} else if index%3 == 2 {
			level = "medium"
		}
		results = append(results, map[string]any{
			"title":       fmt.Sprintf("%s-%02d-%s", level, index, strings.Repeat("题", 240)),
			"description": strings.Repeat("evidence ", 400),
			"risk_type":   "test",
			"level":       level,
			"suggestion":  strings.Repeat("remediation ", 200),
		})
	}
	payload, err := json.Marshal(map[string]any{"score": 50, "results": results})
	require.NoError(t, err)
	render, _ := renderForTechnicalTest(t, "Mcp-Scan", string(payload))

	require.Len(t, render.TechnicalFindings, 50)
	assert.Contains(t, render.Coverage, "50/51")
	assert.True(t, strings.HasPrefix(render.TechnicalFindings[0].Title, "high-01"))
	assert.True(t, strings.HasPrefix(render.TechnicalFindings[16].Title, "high-49"))
	assert.True(t, strings.HasPrefix(render.TechnicalFindings[17].Title, "medium-02"))
	for _, finding := range render.TechnicalFindings {
		assert.LessOrEqual(t, len([]rune(finding.Title)), 200)
		assert.LessOrEqual(t, len([]rune(finding.Evidence)), 2000)
		assert.LessOrEqual(t, len([]rune(finding.Impact)), 1000)
		assert.LessOrEqual(t, len([]rune(finding.Remediation)), 1000)
	}
}

func TestBuildSnapshotMapsMCPFindingCategoryAndSeverityWithoutRawRiskType(t *testing.T) {
	const unknownRiskType = "future-private-risk-type-sentinel"
	results := []map[string]any{
		{"title": "Dangerous tool", "risk_type": "dangerous_tool", "level": "critical"},
		{"title": "Command execution", "risk_type": "MCP05 Command Injection & Execution", "level": "high"},
		{"title": "Authorization", "risk_type": "MCP07 Insufficient Auth & Authz", "level": "medium"},
		{"title": "Leakage", "risk_type": "MCP01 Token Mismanagement & Secret Exposure", "level": "low"},
		{"title": "Poisoning", "risk_type": "MCP03 Tool Poisoning", "level": "high"},
		{"title": "Skill mismatch", "risk_type": "skill mismatch", "level": "medium"},
		{"title": "Unknown: " + unknownRiskType, "risk_type": unknownRiskType, "level": "unexpected"},
		{"title": "Near miss", "risk_type": "MCP030 private extension", "level": "low"},
	}
	payload, err := json.Marshal(map[string]any{"score": 50, "results": results})
	require.NoError(t, err)

	render, encoded := renderForTechnicalTest(t, "Mcp-Scan", string(payload))
	require.Len(t, render.TechnicalFindings, len(results))
	want := map[string]struct{ category, severity string }{
		"Dangerous tool":                {"dangerous_tool", "high"},
		"Command execution":             {"command_file", "high"},
		"Authorization":                 {"authorization", "medium"},
		"Leakage":                       {"data_leakage", "low"},
		"Poisoning":                     {"tool_poisoning", "high"},
		"[REDACTED_RISK_TYPE]":          {"skill_mismatch", "medium"},
		"Unknown: [REDACTED_RISK_TYPE]": {"other", "low"},
		"Near miss":                     {"other", "low"},
	}
	for _, finding := range render.TechnicalFindings {
		expected, ok := want[finding.Title]
		require.True(t, ok, finding.Title)
		assert.Equal(t, expected.category, finding.Category)
		assert.Equal(t, expected.severity, finding.Severity)
	}
	assert.NotContains(t, encoded, unknownRiskType)
}

func renderForTechnicalTest(t *testing.T, taskType, result string) (RenderModel, string) {
	t.Helper()
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	snapshot, err := BuildSnapshotAt("technical-task", "alice", taskType, event(result), brand.Config{ProductName: "AIG", PrimaryColor: "#1677FF"}, now, now)
	require.NoError(t, err)
	var render RenderModel
	require.NoError(t, json.Unmarshal(snapshot.RenderData, &render))
	return render, string(snapshot.RenderData)
}

func credentialLeakSnapshot(t *testing.T) (*Snapshot, []string) {
	t.Helper()
	sentinels := []string{
		"hunter2-natural-secret",
		"token-natural-secret",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJhbGljZSJ9.signatureSentinel12345",
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcd",
		"AKIAIOSFODNN7EXAMPLE",
	}
	payload := map[string]any{
		"score": 50,
		"results": []map[string]any{{
			"title":       "Credential exposure",
			"description": fmt.Sprintf("password is %s; token was %s; observed %s", sentinels[0], sentinels[1], sentinels[2]),
			"risk_type":   "credential_exposure",
			"level":       "high",
			"suggestion":  fmt.Sprintf("Revoke %s and rotate %s", sentinels[3], sentinels[4]),
		}},
	}
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	snapshot, err := BuildSnapshotAt("credential-task", "user-alice", "Mcp-Scan", event(string(encoded)), brand.Config{ProductName: "AIG", PrimaryColor: "#1677FF"}, now, now)
	require.NoError(t, err)
	snapshot.ID = "credential-report"
	return snapshot, sentinels
}

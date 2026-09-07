package tasks

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
)

var safeAgentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,127}$`)

// validNewAgentInput 只约束新任务；历史幂等确认在调用前已经完成。
func validNewAgentInput(input CreateInput) bool {
	return strings.TrimSpace(input.Content) != "" && utf8.ValidString(input.Content) && len(input.AttachmentIDs) == 0
}

// safeAgentReferences 仅投影完整标准引用合同，不回显历史内联配置或疑似凭据。
func safeAgentReferences(raw json.RawMessage) (string, string) {
	var params agentTaskParams
	if !validTaskParams("agent_scan", raw) || !decodeExactJSON(raw, &params) {
		return "", ""
	}
	agentID, modelID := params.AgentID, params.EvalModelID
	if !safeAgentIDPattern.MatchString(agentID) || strings.Contains(agentID, "..") || suspiciousReference(agentID) {
		agentID = ""
	}
	if safeModelID(modelID) == "" || suspiciousReference(modelID) {
		modelID = ""
	}
	return agentID, modelID
}

func suspiciousReference(value string) bool {
	lower := strings.ToLower(value)
	for _, prefix := range []string{"sk-", "sk_", "ghp_", "github_pat_", "akia", "asia", "eyj", "bearer ", "token ", "password"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

package reports

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

const mappingVersion = "risk-v2"

var ErrInvalidFindings = errors.New("扫描结果格式无效")

type RiskSummary struct {
	MappingVersion string `json:"mapping_version"`
	High           int    `json:"high"`
	Medium         int    `json:"medium"`
	Low            int    `json:"low"`
	Score          int    `json:"score"`
}

func MapRisk(taskType string, raw []byte) (RiskSummary, error) {
	kind, ok := riskTaskType(taskType)
	if !ok {
		return RiskSummary{}, ErrInvalidFindings
	}
	result, err := eventResult(raw)
	if err != nil {
		return RiskSummary{}, ErrInvalidFindings
	}
	switch kind {
	case "ai":
		return mapAI(result)
	case "mcp":
		return mapLevel(result)
	case "agent":
		return mapAgent(result)
	case "prompt":
		return mapPrompt(result)
	default:
		return RiskSummary{}, ErrInvalidFindings
	}
}

func riskTaskType(taskType string) (string, bool) {
	switch taskType {
	case "AI-Infra-Scan", "ai_infra_scan":
		return "ai", true
	case "Mcp-Scan", "mcp_scan":
		return "mcp", true
	case "Agent-Scan", "agent_scan":
		return "agent", true
	case "Model-Redteam-Report", "model_redteam_report":
		return "prompt", true
	default:
		return "", false
	}
}

func eventResult(raw []byte) (json.RawMessage, error) {
	var envelope struct {
		ID        string          `json:"id"`
		Type      string          `json:"type"`
		Timestamp json.RawMessage `json:"timestamp"`
		Result    json.RawMessage `json:"result"`
	}
	if !json.Valid(raw) || json.Unmarshal(raw, &envelope) != nil || envelope.ID == "" || envelope.Type != "resultUpdate" || !isJSONNumber(envelope.Timestamp) || len(envelope.Result) == 0 || !isJSONObject(envelope.Result) {
		return nil, ErrInvalidFindings
	}
	return envelope.Result, nil
}

func mapAI(raw json.RawMessage) (RiskSummary, error) {
	var payload struct {
		Score   json.RawMessage `json:"score"`
		Results []struct {
			Vulnerabilities []struct {
				Severity string `json:"severity"`
			} `json:"vulnerabilities"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Results == nil {
		return RiskSummary{}, ErrInvalidFindings
	}
	score, err := validScore(payload.Score)
	if err != nil {
		return RiskSummary{}, ErrInvalidFindings
	}
	summary := RiskSummary{MappingVersion: mappingVersion, Score: score}
	for _, result := range payload.Results {
		for _, vulnerability := range result.Vulnerabilities {
			if vulnerability.Severity == "" {
				return RiskSummary{}, ErrInvalidFindings
			}
			countSeverity(&summary, vulnerability.Severity)
		}
	}
	return summary, nil
}

func mapAgent(raw json.RawMessage) (RiskSummary, error) {
	var payload struct {
		SchemaVersion string `json:"schema_version"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.SchemaVersion != "agent-security-report@1" {
		return RiskSummary{}, ErrInvalidFindings
	}
	return mapLevel(raw)
}

func mapLevel(raw json.RawMessage) (RiskSummary, error) {
	var payload struct {
		Score   json.RawMessage `json:"score"`
		Results []struct {
			Level *string `json:"level"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Results == nil {
		return RiskSummary{}, ErrInvalidFindings
	}
	score, err := validScore(payload.Score)
	if err != nil {
		return RiskSummary{}, ErrInvalidFindings
	}
	summary := RiskSummary{MappingVersion: mappingVersion, Score: score}
	for _, result := range payload.Results {
		if result.Level == nil || strings.TrimSpace(*result.Level) == "" {
			return RiskSummary{}, ErrInvalidFindings
		}
		countSeverity(&summary, *result.Level)
	}
	return summary, nil
}

func mapPrompt(raw json.RawMessage) (RiskSummary, error) {
	var payload struct {
		MsgType string `json:"msgType"`
		Status  string `json:"status"`
		Content []struct {
			Total     *int            `json:"total"`
			Jailbreak *int            `json:"jailbreak"`
			Results   json.RawMessage `json:"results"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.MsgType != "json" || payload.Status == "" || payload.Content == nil {
		return RiskSummary{}, ErrInvalidFindings
	}
	summary := RiskSummary{MappingVersion: mappingVersion}
	total := 0
	for _, content := range payload.Content {
		if content.Total == nil || content.Jailbreak == nil || *content.Total < 0 || *content.Jailbreak < 0 || *content.Jailbreak > *content.Total || !isJSONArray(content.Results) {
			return RiskSummary{}, ErrInvalidFindings
		}
		total += *content.Total
		summary.High += *content.Jailbreak
	}
	if total == 0 {
		summary.Score = 100
		return summary, nil
	}
	summary.Score = (100 * (total - summary.High)) / total
	return summary, nil
}

func validScore(raw json.RawMessage) (int, error) {
	var number float64
	if len(raw) == 0 || json.Unmarshal(raw, &number) != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number < 0 || number > 100 {
		return 0, ErrInvalidFindings
	}
	return int(number), nil
}

func countSeverity(summary *RiskSummary, severity string) {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "high", "严重", "高危":
		summary.High++
	case "medium", "中危":
		summary.Medium++
	default:
		summary.Low++
	}
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func isJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']'
}

func isJSONNumber(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	_, ok := value.(json.Number)
	return ok
}

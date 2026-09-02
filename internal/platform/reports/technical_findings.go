package reports

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxTechnicalFindings = 50
	maxFindingTitleRunes = 200
	maxFindingTextRunes  = 1000
	maxImpactRunes       = 512
	maxRemediationRunes  = 512
	maxFindingTitleBytes = 512
	maxFindingTextBytes  = 1000
	maxImpactBytes       = 512
	maxRemediationBytes  = 512
)

var (
	bearerPattern        = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]+`)
	credentialPattern    = regexp.MustCompile(`(?i)\b(api[_ -]?key|password|secret|token|cookie|authorization)\s*[:=]\s*[^\s,;]+`)
	credentialCuePattern = regexp.MustCompile(`(?i)\b(api[_ -]?key|password|passwd|pwd|secret|token|cookie|authorization|access[_ -]?key|client[_ -]?secret)\s+(?:is|was|are|were|equals?|value(?:\s+is)?)\s+["']?[^\s,;]+`)
	jwtPattern           = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\b`)
	githubTokenPattern   = regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,255}|github_pat_[A-Za-z0-9_]{20,255})\b`)
	awsAccessKeyPattern  = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
	keyPattern           = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`)
	privateKeyPattern    = regexp.MustCompile(`(?s)-----BEGIN [^-\r\n]{0,64}PRIVATE KEY-----.*?-----END [^-\r\n]{0,64}PRIVATE KEY-----`)
	windowsPathPattern   = regexp.MustCompile(`[A-Za-z]:\\[^\s]+`)
	unixUserPathPattern  = regexp.MustCompile(`(?:/home|/Users|/root)/[^\s]+`)
	httpURLPattern       = regexp.MustCompile(`https?://[^\s<>"']+`)
)

type technicalFindingCandidate struct {
	TechnicalFinding
	severity int
	order    int
}

func technicalFindingsOf(taskType string, raw []byte) ([]TechnicalFinding, int, error) {
	kind, ok := riskTaskType(taskType)
	if !ok {
		return nil, 0, ErrInvalidFindings
	}
	result, err := eventResult(raw)
	if err != nil {
		return nil, 0, ErrInvalidFindings
	}
	var candidates []technicalFindingCandidate
	switch kind {
	case "ai":
		candidates, err = aiTechnicalFindings(result)
	case "mcp":
		candidates, err = mcpTechnicalFindings(result)
	case "agent":
		candidates, err = agentTechnicalFindings(result)
	case "prompt":
		candidates, err = promptTechnicalFindings(result)
	default:
		err = ErrInvalidFindings
	}
	if err != nil {
		return nil, 0, ErrInvalidFindings
	}
	total := len(candidates)
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].severity == candidates[right].severity {
			return candidates[left].order < candidates[right].order
		}
		return candidates[left].severity < candidates[right].severity
	})
	if len(candidates) > maxTechnicalFindings {
		candidates = candidates[:maxTechnicalFindings]
	}
	findings := make([]TechnicalFinding, 0, len(candidates))
	for _, candidate := range candidates {
		findings = append(findings, candidate.TechnicalFinding)
	}
	return findings, total, nil
}

func aiTechnicalFindings(raw json.RawMessage) ([]technicalFindingCandidate, error) {
	var payload struct {
		Results []struct {
			TargetURL       string `json:"target_url"`
			Fingerprint     string `json:"fingerprint"`
			Vulnerabilities []struct {
				Name           string   `json:"name"`
				CVE            string   `json:"cve"`
				Summary        string   `json:"summary"`
				Details        string   `json:"details"`
				CVSS           string   `json:"cvss"`
				Severity       string   `json:"severity"`
				SecurityAdvice string   `json:"security_advise"`
				References     []string `json:"references"`
			} `json:"vulnerabilities"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Results == nil {
		return nil, ErrInvalidFindings
	}
	candidates := make([]technicalFindingCandidate, 0)
	for _, result := range payload.Results {
		for _, vulnerability := range result.Vulnerabilities {
			titleParts := compactNonEmpty(vulnerability.CVE, firstNonEmpty(vulnerability.Summary, vulnerability.Name))
			title := safeFindingText(strings.Join(titleParts, " - "), "AI 基础设施安全发现", maxFindingTitleRunes, maxFindingTitleBytes)
			evidence := make([]string, 0, 7)
			if target := safeHTTPURL(result.TargetURL); target != "" {
				evidence = append(evidence, "目标: "+target)
			}
			if fingerprint := safeFindingText(result.Fingerprint, "", 128, 256); fingerprint != "" {
				evidence = append(evidence, "指纹: "+fingerprint)
			}
			if cvss := safeFindingText(vulnerability.CVSS, "", 32, 64); cvss != "" {
				evidence = append(evidence, "CVSS: "+cvss)
			}
			if severity := safeFindingText(vulnerability.Severity, "", 32, 64); severity != "" {
				evidence = append(evidence, "级别: "+severity)
			}
			for _, reference := range vulnerability.References {
				if len(evidence) >= 7 {
					break
				}
				if safe := safeHTTPURL(reference); safe != "" {
					evidence = append(evidence, "参考: "+safe)
				}
			}
			candidates = append(candidates, technicalFindingCandidate{
				TechnicalFinding: TechnicalFinding{
					Title:       title,
					Evidence:    safeFindingText(strings.Join(evidence, "; "), "引擎确认了基础设施风险。", maxFindingTextRunes, maxFindingTextBytes),
					Impact:      safeFindingText(firstNonEmpty(vulnerability.Details, vulnerability.Summary), "该风险可能影响目标服务的机密性、完整性或可用性。", maxImpactRunes, maxImpactBytes),
					Remediation: safeFindingText(vulnerability.SecurityAdvice, "升级受影响组件，限制暴露面并在修复后复测。", maxRemediationRunes, maxRemediationBytes),
				},
				severity: findingSeverity(vulnerability.Severity), order: len(candidates),
			})
		}
	}
	return candidates, nil
}

func mcpTechnicalFindings(raw json.RawMessage) ([]technicalFindingCandidate, error) {
	var payload struct {
		Results []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			RiskType    string `json:"risk_type"`
			Level       string `json:"level"`
			Suggestion  string `json:"suggestion"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Results == nil {
		return nil, ErrInvalidFindings
	}
	candidates := make([]technicalFindingCandidate, 0, len(payload.Results))
	for _, finding := range payload.Results {
		category := mcpFindingCategory(finding.RiskType)
		severity := mcpFindingSeverity(finding.Level)
		candidates = append(candidates, technicalFindingCandidate{
			TechnicalFinding: TechnicalFinding{
				Title:       safeMCPFindingTitle(finding.Title, finding.RiskType),
				Evidence:    safeFindingText(finding.Description, "MCP 扫描器确认了该风险。", maxFindingTextRunes, maxFindingTextBytes),
				Impact:      mcpFindingImpact(category),
				Remediation: safeFindingText(finding.Suggestion, "限制受影响能力，修复后复测。", maxRemediationRunes, maxRemediationBytes),
				Category:    category,
				Severity:    severity,
			},
			severity: findingSeverity(finding.Level), order: len(candidates),
		})
	}
	return candidates, nil
}

// safeMCPFindingTitle keeps a scanner-supplied title useful while preventing
// the original scanner risk_type from being persisted into a display field.
func safeMCPFindingTitle(value, riskType string) string {
	title := safeFindingText(value, "MCP 安全发现", maxFindingTitleRunes, maxFindingTitleBytes)
	cleanRiskType := safeFindingText(riskType, "", maxFindingTitleRunes, maxFindingTitleBytes)
	if cleanRiskType != "" {
		title = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(cleanRiskType)).ReplaceAllString(title, "[REDACTED_RISK_TYPE]")
	}
	return safeFindingText(title, "MCP 安全发现", maxFindingTitleRunes, maxFindingTitleBytes)
}

func mcpFindingCategory(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "dangerous_tool", "command_file", "authorization", "data_leakage", "tool_poisoning", "skill_mismatch":
		return normalized
	}
	switch {
	case containsMCPRisk(normalized, "mcp03") || strings.Contains(normalized, "tool poisoning") || strings.Contains(normalized, "tool poison") || strings.Contains(normalized, "工具投毒") || strings.Contains(normalized, "prompt injection") || strings.Contains(normalized, "提示注入"):
		return "tool_poisoning"
	case strings.Contains(normalized, "skill mismatch") || strings.Contains(normalized, "skill-mismatch") || strings.Contains(normalized, "技能不匹配"):
		return "skill_mismatch"
	case containsMCPRisk(normalized, "mcp05") || strings.Contains(normalized, "command") || strings.Contains(normalized, "shell") || strings.Contains(normalized, "rce") || strings.Contains(normalized, "code execution") || strings.Contains(normalized, "file access") || strings.Contains(normalized, "file read") || strings.Contains(normalized, "file write") || strings.Contains(normalized, "path traversal") || strings.Contains(normalized, "文件") || strings.Contains(normalized, "命令"):
		return "command_file"
	case containsMCPRisk(normalized, "mcp02") || containsMCPRisk(normalized, "mcp07") || strings.Contains(normalized, "authorization") || strings.Contains(normalized, "authentication") || strings.Contains(normalized, "authz") || strings.Contains(normalized, "authn") || strings.Contains(normalized, "privilege") || strings.Contains(normalized, "scope creep") || strings.Contains(normalized, "越权") || strings.Contains(normalized, "授权") || strings.Contains(normalized, "认证") || strings.Contains(normalized, "权限"):
		return "authorization"
	case containsMCPRisk(normalized, "mcp01") || containsMCPRisk(normalized, "mcp10") || strings.Contains(normalized, "data leak") || strings.Contains(normalized, "data exfiltration") || strings.Contains(normalized, "secret exposure") || strings.Contains(normalized, "token mismanagement") || strings.Contains(normalized, "credential") || strings.Contains(normalized, "information disclosure") || strings.Contains(normalized, "sensitive data") || strings.Contains(normalized, "泄露") || strings.Contains(normalized, "凭据") || strings.Contains(normalized, "密钥"):
		return "data_leakage"
	case containsMCPRisk(normalized, "mcp04") || containsMCPRisk(normalized, "mcp09") || strings.Contains(normalized, "dangerous tool") || strings.Contains(normalized, "malicious tool") || strings.Contains(normalized, "shadow mcp") || strings.Contains(normalized, "tool shadow") || strings.Contains(normalized, "supply chain") || strings.Contains(normalized, "rug pull") || strings.Contains(normalized, "name confusion") || strings.Contains(normalized, "危险工具"):
		return "dangerous_tool"
	default:
		return "other"
	}
}

func containsMCPRisk(value, identifier string) bool {
	for offset := 0; offset < len(value); {
		index := strings.Index(value[offset:], identifier)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(identifier)
		before, _ := utf8.DecodeLastRuneInString(value[:index])
		after, _ := utf8.DecodeRuneInString(value[end:])
		beforeBoundary := index == 0 || !mcpRiskIdentifierRune(before)
		afterBoundary := end == len(value) || !mcpRiskIdentifierRune(after)
		if beforeBoundary && afterBoundary {
			return true
		}
		offset = end
	}
	return false
}

func mcpRiskIdentifierRune(value rune) bool {
	return unicode.IsLetter(value) || unicode.IsNumber(value)
}

func mcpFindingSeverity(value string) string {
	switch findingSeverity(value) {
	case 0:
		return "high"
	case 1:
		return "medium"
	default:
		return "low"
	}
}

func mcpFindingImpact(category string) string {
	switch category {
	case "dangerous_tool":
		return "该工具的安全行为或来源需要复核。"
	case "command_file":
		return "该风险可能影响命令执行或文件访问边界。"
	case "authorization":
		return "该风险可能导致权限或授权边界失效。"
	case "data_leakage":
		return "该风险可能导致敏感数据或凭据暴露。"
	case "tool_poisoning":
		return "该风险可能通过工具元数据或输出影响调用行为。"
	case "skill_mismatch":
		return "该风险可能导致技能声明与实际能力不一致。"
	default:
		return "MCP 扫描器确认了需进一步复核的风险。"
	}
}

// sanitizeMCPRenderTechnicalFindings projects MCP findings into fixed server
// summaries before they cross the browser report-detail boundary. Stored MCP
// render data may contain scanner descriptions, target URLs, or repository
// references, so it must not be returned verbatim even to an authorized user.
func sanitizeMCPRenderTechnicalFindings(taskType string, render *RenderModel) {
	if render == nil || !isMCPTaskType(taskType) {
		return
	}
	for index := range render.TechnicalFindings {
		finding := render.TechnicalFindings[index]
		category := mcpFindingCategory(finding.Category)
		severity := safeMCPDetailSeverity(finding.Severity)
		title := "MCP 安全风险发现。"
		if severity != "" {
			title = mcpWorkbenchFindingSummary(category, severity)
		}
		render.TechnicalFindings[index] = TechnicalFinding{
			Title:       title,
			Evidence:    "MCP 扫描器确认了该类风险；原始目标与扫描参数未在报告详情中展示。",
			Impact:      mcpFindingImpact(category),
			Remediation: "限制相关工具能力与权限边界，修复后重新执行授权扫描。",
			Category:    category,
			Severity:    severity,
		}
	}
}

func safeMCPDetailSeverity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "high", "严重", "高危":
		return "high"
	case "medium", "中危":
		return "medium"
	case "low", "低危", "info", "informational":
		return "low"
	default:
		return ""
	}
}

func agentTechnicalFindings(raw json.RawMessage) ([]technicalFindingCandidate, error) {
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		Results       []struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Level       string `json:"level"`
			OWASP       string `json:"owasp"`
			Suggestion  string `json:"suggestion"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.SchemaVersion != "agent-security-report@1" || payload.Results == nil {
		return nil, ErrInvalidFindings
	}
	candidates := make([]technicalFindingCandidate, 0, len(payload.Results))
	for _, finding := range payload.Results {
		identifier := safeFindingText(finding.ID, "", 128, 256)
		category := safeFindingText(finding.Type, "未分类", 128, 256)
		owasp := safeFindingText(finding.OWASP, "", 128, 256)
		evidenceParts := compactNonEmpty(
			safeFindingText(finding.Description, "", maxFindingTextRunes, maxFindingTextBytes),
			labelIfPresent("发现编号", identifier), labelIfPresent("分类", category), labelIfPresent("OWASP", owasp),
		)
		candidates = append(candidates, technicalFindingCandidate{
			TechnicalFinding: TechnicalFinding{
				Title:       safeFindingText(finding.Title, "Agent 安全发现", maxFindingTitleRunes, maxFindingTitleBytes),
				Evidence:    safeFindingText(strings.Join(evidenceParts, "; "), "Agent 扫描器确认了该风险。", maxFindingTextRunes, maxFindingTextBytes),
				Impact:      safeFindingText(fmt.Sprintf("%s 类风险（%s）可能影响 Agent 工作流的安全边界。", category, safeFindingText(finding.Level, "未知", 32, 64)), "该风险可能影响 Agent 工作流。", maxImpactRunes, maxImpactBytes),
				Remediation: safeFindingText(finding.Suggestion, "限制 Agent 权限与工具边界，修复后复测。", maxRemediationRunes, maxRemediationBytes),
			},
			severity: findingSeverity(finding.Level), order: len(candidates),
		})
	}
	return candidates, nil
}

func promptTechnicalFindings(raw json.RawMessage) ([]technicalFindingCandidate, error) {
	var payload struct {
		MsgType string `json:"msgType"`
		Status  string `json:"status"`
		Content []struct {
			Results []struct {
				Status        string `json:"status"`
				Vulnerability string `json:"vulnerability"`
				AttackMethod  string `json:"attackMethod"`
				Reason        string `json:"reason"`
			} `json:"results"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.MsgType != "json" || payload.Status == "" || payload.Content == nil {
		return nil, ErrInvalidFindings
	}
	candidates := make([]technicalFindingCandidate, 0)
	for _, report := range payload.Content {
		for _, result := range report.Results {
			if result.Status != "Jailbreak" {
				continue
			}
			title := strings.Join(compactNonEmpty(result.Vulnerability, result.AttackMethod), " / ")
			candidates = append(candidates, technicalFindingCandidate{
				TechnicalFinding: TechnicalFinding{
					Title:       safeFindingText(title, "模型越狱发现", maxFindingTitleRunes, maxFindingTitleBytes),
					Evidence:    safeFindingText(result.Reason, "引擎判定该用例为 Jailbreak。", maxFindingTextRunes, maxFindingTextBytes),
					Impact:      "该越狱用例表明模型安全策略可被特定攻击方法绕过。",
					Remediation: "加强对应漏洞类别的输入防护、策略约束与拒答测试，并在修复后复测。",
				},
				severity: 0, order: len(candidates),
			})
		}
	}
	return candidates, nil
}

func safeFindingText(value, fallback string, maxRunes, maxBytes int) string {
	value = stripUnsafeFindingText(value)
	if value == "" {
		value = stripUnsafeFindingText(fallback)
	}
	return truncateFindingText(value, maxRunes, maxBytes)
}

func stripUnsafeFindingText(value string) string {
	var cleaned strings.Builder
	for _, current := range value {
		if unicode.IsControl(current) || unicode.Is(unicode.Cf, current) ||
			current >= '\u202A' && current <= '\u202E' || current >= '\u2066' && current <= '\u2069' {
			cleaned.WriteByte(' ')
			continue
		}
		cleaned.WriteRune(current)
	}
	value = privateKeyPattern.ReplaceAllString(cleaned.String(), "[REDACTED]")
	value = bearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	value = credentialPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = credentialCuePattern.ReplaceAllString(value, "$1 [REDACTED]")
	value = jwtPattern.ReplaceAllString(value, "[REDACTED]")
	value = githubTokenPattern.ReplaceAllString(value, "[REDACTED]")
	value = awsAccessKeyPattern.ReplaceAllString(value, "[REDACTED]")
	value = keyPattern.ReplaceAllString(value, "[REDACTED]")
	value = windowsPathPattern.ReplaceAllString(value, "[USER_PATH]")
	value = unixUserPathPattern.ReplaceAllString(value, "[USER_PATH]")
	value = httpURLPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if sanitized := safeHTTPURL(candidate); sanitized != "" {
			return sanitized
		}
		return "[REDACTED_URL]"
	})
	return html.EscapeString(strings.Join(strings.Fields(value), " "))
}

func safeHTTPURL(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func truncateFindingText(value string, maxRunes, maxBytes int) string {
	if maxRunes <= 0 || maxBytes <= 0 {
		return ""
	}
	result := make([]rune, 0, minInt(utf8.RuneCountInString(value), maxRunes))
	bytesUsed := 0
	for _, current := range value {
		width := utf8.RuneLen(current)
		if len(result) == maxRunes || bytesUsed+width > maxBytes {
			break
		}
		result = append(result, current)
		bytesUsed += width
	}
	return strings.TrimSpace(string(result))
}

func findingSeverity(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "high", "严重", "高危":
		return 0
	case "medium", "中危":
		return 1
	default:
		return 2
	}
}

func technicalCoverage(shown, total int) string {
	if total == 0 {
		return "未发现可安全展示的技术明细；风险计数基于完整引擎结果。"
	}
	return fmt.Sprintf("展示 %d/%d 条技术发现；风险计数基于完整引擎结果。", shown, total)
}

func compactNonEmpty(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func labelIfPresent(label, value string) string {
	if value == "" {
		return ""
	}
	return label + ": " + value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

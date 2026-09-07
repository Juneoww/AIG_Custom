package reports

import "encoding/json"

// applyMCPAnalysisCoverage 仅识别固定运行模式，绝不把原始 readme 或工具描述
// 带入浏览器/PDF；基础规则参考分也不得被解释为全面安全结论。
func applyMCPAnalysisCoverage(render *RenderModel, taskType string, raw json.RawMessage) {
	if !isMCPTaskType(taskType) {
		return
	}
	result, err := eventResult(raw)
	if err != nil {
		return
	}
	var mode struct {
		AnalysisMode string `json:"analysis_mode"`
	}
	if json.Unmarshal(result, &mode) != nil || mode.AnalysisMode != "basic" {
		return
	}
	render.Coverage = "基础检查（未使用模型）：仅包含只读代码规则或 MCP 协议与工具元信息检查；未执行服务工具，未进行模型语义分析和漏洞利用验证。" + render.Coverage
	render.ScoreExplanation = "基础规则参考分：仅反映本次已检查规则及待复核线索；高分不代表完整安全评估或安全认证。"
	render.Conclusion = "本报告不代表已完成全面安全评估；请人工复核规则线索，并按需选择模型开展进一步分析。"
}

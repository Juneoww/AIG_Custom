"""功能：严格校验私有 MCP 模型辅助阶段及最终复核，防止损坏输出伪装成功。
实现：有界完整 XML 解析与三次格式重试；不改变独立 Skills 或旧版 Agent。
输入：模型阶段文本。输出：完整漏洞列表或明确空声明；失败仅抛固定安全异常。
依赖：标准库 XML 与现有 BaseAgent；仅供受治理私有 MCP 运行时使用。
"""

import xml.etree.ElementTree as ET

from agent.base_agent import BaseAgent
from utils.prompt_manager import prompt_manager

MCP_REVIEW_FORMAT = """仅返回完整 XML，不加代码围栏或解释；每条漏洞一个 <vuln>，所有字段必填且非空：
<vuln><title>标题</title><desc>基于已检查证据的 Markdown 描述，含位置、风险条件、影响和验证限制</desc><risk_type>风险类型</risk_type><level>Critical 或 High 或 Medium 或 Low</level><suggestion>具体修复建议</suggestion></vuln>
字段中的 & 和 < 必须转义为 &amp; 和 &lt;，或将文本放入 CDATA。不得添加其他标签、重复字段或 XML 声明。
只有完成分析并明确没有发现时才返回唯一的 <empty/>。不得与漏洞混合；不得将格式失败或缺失证据解释为无风险。
"""


def _bounded_text(content):
    """拒绝空值、非文本、异常 Unicode 和超过协议上限的阶段结果。"""
    try:
        return isinstance(content, str) and bool(content.strip()) and len(content.encode("utf-8")) <= 1 << 20
    except UnicodeError:
        return False


def parse_mcp_review(content: str) -> list[dict[str, str]]:
    """任一坏块使整份复核失败；兼容 MCP 已有的唯一 <empty> 空声明。"""
    invalid = "MCP review output invalid"
    if not _bounded_text(content):
        raise ValueError(invalid)
    text = content.strip()
    if text in {"<empty>", "<empty/>", "<empty />", "<empty></empty>"}:
        return []
    if "<!" in text.replace("<![CDATA[", "") or "<?" in text:
        raise ValueError(invalid)
    try:
        root = ET.fromstring("<report>" + text + "</report>")
    except (ET.ParseError, ValueError):
        raise ValueError(invalid) from None
    if not len(root) or (root.text or "").strip():
        raise ValueError(invalid)
    required = {"title", "desc", "risk_type", "level", "suggestion"}
    levels = {"critical": "Critical", "high": "High", "medium": "Medium", "low": "Low",
              "严重": "Critical", "高危": "High", "中危": "Medium", "低危": "Low"}
    results = []
    for block in root:
        if block.tag != "vuln" or block.attrib or (block.text or "").strip() or (block.tail or "").strip():
            raise ValueError(invalid)
        if len(block) != len(required) or {field.tag for field in block} != required:
            raise ValueError(invalid)
        fields = {}
        for field in block:
            if field.attrib or len(field) or (field.tail or "").strip() or not (field.text or "").strip():
                raise ValueError(invalid)
            fields[field.tag] = field.text.strip()
        level = levels.get(fields["level"].lower())
        if level is None:
            raise ValueError(invalid)
        results.append({"title": fields["title"], "description": fields["desc"],
                        "risk_type": fields["risk_type"], "level": level, "suggestion": fields["suggestion"]})
    return results


def is_mcp_review_output(content: str) -> bool:
    try:
        parse_mcp_review(content)
        return True
    except ValueError:
        return False


def validate_mcp_stage_output(content, check=None):
    """流水线和格式重试共用失败关闭条件，不回传模型的坏输出。"""
    if not _bounded_text(content) or (check is not None and not check(content)):
        raise ValueError("MCP stage output invalid")


class PrivateMCPBaseAgent(BaseAgent):
    async def _format_final_output(self) -> str:
        # 保留原 system 及固定工具边界；重试时不记录或回传损坏的模型输出。
        history = [dict(item) for item in self.history]
        history.append({"role": "user", "content": prompt_manager.format_prompt(
            "format_report", output_format=self.output_format)})
        for _ in range(3):
            output = self.llm.chat(history)
            try:
                validate_mcp_stage_output(output, self.output_check_fn)
                return output
            except ValueError:
                history.append({"role": "user", "content": "上次结果为空或格式无效。重新输出完整阶段报告，不得将格式错误解释为无发现。"})
        raise ValueError("MCP stage output invalid")

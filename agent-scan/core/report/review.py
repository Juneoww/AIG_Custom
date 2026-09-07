"""功能：校验最终复核输出，阻止空文本或截断漏洞被解释为安全结论。
实现：解析完整 XML 片段并检查完成标记、零发现标记及漏洞证据字段。
输入：最终 reviewer 文本；输出：合法漏洞块数量，否则抛出 RuntimeError。
"""

import re
import xml.etree.ElementTree as ET


def validate_review_output(text: str) -> int:
    """仅接受完整、有明确终态且结构有效的复核结果。"""
    def invalid(reason):
        raise RuntimeError(f"Invalid final review: {reason}")

    if not isinstance(text, str) or not text.strip():
        invalid("empty output")
    try:
        # 包装根节点使真实 DTD/XML 声明非法；保留 PI 节点供结构校验拒绝。
        # CDATA 内相同字面量只是证据文本，不应按声明拒绝。
        root = ET.fromstring(f"<review>{text}</review>", parser=ET.XMLParser(target=ET.TreeBuilder(insert_pis=True)))
    except ET.ParseError as exc:
        raise RuntimeError("Invalid final review: malformed or truncated XML") from exc
    elements = list(root)
    if root.text and root.text.strip():
        invalid("unexpected text outside review fields")
    for element in elements:
        if element.tail and element.tail.strip():
            invalid("unexpected text after review fields")
        if element.tag not in {"vuln", "no_findings", "review_complete"} or element.attrib:
            invalid("unexpected review field")
    complete = root.findall("review_complete")
    if len(complete) != 1 or complete[0] is not elements[-1] or list(complete[0]) or (complete[0].text or "").strip() != "true":
        invalid("missing final completion marker")
    findings = root.findall("vuln")
    no_findings = root.findall("no_findings")
    if not findings:
        if len(no_findings) != 1 or list(no_findings[0]) or (no_findings[0].text or "").strip() != "true":
            invalid("zero findings requires an explicit no_findings marker")
        return 0
    if no_findings:
        invalid("findings contradict the no_findings marker")
    for finding in findings:
        required = {"id", "title", "desc", "risk_type", "level", "suggestion", "conversation"}
        if {field.tag for field in finding} != required or len(list(finding)) != len(required):
            invalid("missing or duplicate vulnerability fields")
        for name in required - {"conversation"}:
            field = finding.find(name)
            if field.attrib or list(field) or not (field.text or "").strip():
                invalid(f"empty or malformed vulnerability {name}")
        if finding.findtext("level").strip().lower() not in {"critical", "high", "medium", "low"}:
            invalid("unknown severity")
        if not re.match(r"^ASI(?:0[1-9]|10)(?:\s*:|\s|$)", finding.findtext("risk_type").strip(), re.IGNORECASE):
            invalid("unknown OWASP ASI category")
        conversation = finding.find("conversation")
        turns = list(conversation)
        if not turns or conversation.attrib:
            invalid("missing conversation evidence")
        for turn in turns:
            if turn.tag != "turn" or turn.attrib or len(list(turn)) != 2 or {field.tag for field in turn} != {"prompt", "response"}:
                invalid("malformed conversation turn")
            for field in turn:
                if field.attrib or list(field) or not (field.text or "").strip():
                    invalid("incomplete conversation evidence")
    return len(findings)

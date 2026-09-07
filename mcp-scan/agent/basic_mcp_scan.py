"""功能：提供未选择模型时的 MCP 基础检查；不构造模型或执行目标代码。
实现：仓库只读模式匹配；服务仅初始化并列出工具元数据，报告固定待复核线索。
输入：受控本地根目录或私有网关配置。输出：安全结果对象；异常只使用固定消息。
边界：最多一万文件、单文件 16 MiB、合计 64 MiB；不运行代码或调用远程工具。
"""

import json
import os
import re
import stat
import time
from pathlib import Path

from utils.project_analyzer import calc_mcp_score

MAX_FILES = 10000
MAX_FILE_BYTES = 16 << 20
MAX_TOTAL_BYTES = 64 << 20
MAX_FINDINGS = 256
MAX_TOOLS = 1000
MAX_METADATA_BYTES = 1 << 20
SAFE_ERROR = "MCP basic scan failed"
EXTENSIONS = {".py", ".js", ".ts", ".jsx", ".tsx", ".mjs", ".cjs", ".sh", ".bash", ".yaml", ".yml", ".json", ".toml", ".ini", ".conf"}
RULES = (
    ("dynamic-execution", re.compile(r"\b(?:eval|exec)\s*\("), "动态代码执行调用", "核对动态执行参数是否受不可信输入影响，并优先移除动态执行。"),
    ("shell-execution", re.compile(r"\b(?:subprocess\.[A-Za-z_]+\s*\([^\n]*\bshell\s*=\s*True|os\.system\s*\(|child_process\.exec\s*\()"), "Shell 执行调用", "核对命令构造与输入来源，使用固定程序及独立参数并关闭 Shell。"),
    ("unsafe-deserialization", re.compile(r"\b(?:pickle\.loads?|yaml\.unsafe_load)\s*\("), "危险反序列化调用", "核对数据是否可信，并使用不允许任意对象构造的解析方式。"),
)
POLLUTED_DESCRIPTION = re.compile(r"ignore\s+(?:all\s+)?(?:previous|prior|system)\s+instructions|忽略(?:所有|之前|先前|系统).{0,12}指令|(?:reveal|disclose|exfiltrate)\s+(?:the\s+)?(?:secrets?|tokens?|credentials?)", re.I)


def _fail():
    raise ValueError(SAFE_ERROR) from None


def _finding(rule, title, location, suggestion):
    return {"title": "待复核线索：" + title, "description": location + "。命中基础规则，仅为待人工复核线索，未经利用验证。",
            "risk_type": rule, "level": "Low", "suggestion": suggestion}


def _result(started, findings, coverage):
    return {"readme": "基础检查（未使用模型）。" + coverage + "\n仅执行所列明确规则，结果是待复核线索；非全面安全评估，未发现线索不代表目标安全。基础规则参考分仅反映本次规则线索，不代表完整安全评估或认证。未使用默认模型或任何模型环境配置。",
            "score": calc_mcp_score(findings), "language": "zh", "start_time": started, "end_time": time.time(), "results": findings,
            "llm": "", "analysis_mode": "basic"}


def _linked(path):
    return path.is_symlink() or (hasattr(path, "is_junction") and path.is_junction())


def scan_repository(repo):
    """只读遍历受控源文件，不返回源文本、文件名称或完整路径。"""
    started = time.time()
    try:
        selected = Path(repo)
        if _linked(selected):
            _fail()
        root = selected.resolve(strict=True)
        if not root.is_dir():
            _fail()
        files = []
        entries = 0
        for directory, dirs, names in os.walk(root, followlinks=False, onerror=lambda _: _fail()):
            dirs.sort()
            for name in sorted([*dirs, *names]):
                path = Path(directory) / name
                entries += 1
                if entries > MAX_FILES or _linked(path):
                    _fail()
                path.resolve(strict=True).relative_to(root)
                mode = path.stat(follow_symlinks=False).st_mode
                if not stat.S_ISDIR(mode) and not stat.S_ISREG(mode):
                    _fail()
                if stat.S_ISREG(mode):
                    files.append(path)
        findings, total, inspected, skipped = [], 0, 0, 0
        for index, path in enumerate(files, 1):
            before = path.stat(follow_symlinks=False)
            size = before.st_size
            if size > MAX_FILE_BYTES:
                _fail()
            total += size
            if total > MAX_TOTAL_BYTES:
                _fail()
            if path.suffix.lower() not in EXTENSIONS:
                skipped += 1
                continue
            if _linked(path):
                _fail()
            path.resolve(strict=True).relative_to(root)
            descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_BINARY", 0) | getattr(os, "O_NOFOLLOW", 0))
            with os.fdopen(descriptor, "rb") as stream:
                opened = os.fstat(stream.fileno())
                if not stat.S_ISREG(opened.st_mode) or (before.st_dev, before.st_ino) != (opened.st_dev, opened.st_ino):
                    _fail()
                content = stream.read(MAX_FILE_BYTES + 1)
            if len(content) != size or len(content) > MAX_FILE_BYTES:
                _fail()
            if b"\0" in content:
                skipped += 1
                continue
            try:
                text = content.decode("utf-8-sig", errors="strict")
            except UnicodeError:
                skipped += 1
                continue
            inspected += 1
            for line_number, line in enumerate(text.splitlines(), 1):
                if line.lstrip().startswith(("#", "//", "*")):
                    continue
                for rule, pattern, title, advice in RULES:
                    if pattern.search(line):
                        findings.append(_finding(rule, title, f"源文件序号 {index}，第 {line_number} 行", advice))
                        if len(findings) > MAX_FINDINGS:
                            _fail()
        if inspected == 0:
            _fail()
        coverage = f"已检查 {inspected} 个 UTF-8 源码或配置文件，跳过 {skipped} 个不支持的格式或编码。检查动态执行、Shell 调用和危险反序列化模式；未运行仓库代码、安装依赖、调用 Shell 或访问服务端。"
        return _result(started, findings, coverage)
    except Exception:
        _fail()


async def scan_service(runtime):
    """仅获取工具声明；不调用工具、提示或资源，不重试其他传输。"""
    from utils.mcp_tools import MCPTools
    from utils.runtime_config import validate_runtime_config
    started = time.time()
    try:
        runtime = validate_runtime_config(runtime)
        if "model" in runtime or "mcp_proxy_url" not in runtime:
            _fail()
        manager = MCPTools(url=runtime["mcp_proxy_url"], transport=runtime["effective_transport"], task_capability=runtime["task_capability"])
        async with manager._session() as session:
            result = await session.list_tools()
        tools = result.tools
        if not isinstance(tools, list) or len(tools) > MAX_TOOLS or getattr(result, "nextCursor", None):
            _fail()
        findings, metadata_bytes = [], 0
        for index, tool in enumerate(tools, 1):
            description = tool.description or ""
            if not isinstance(description, str):
                _fail()
            metadata_bytes += len(description.encode("utf-8"))
            if metadata_bytes > MAX_METADATA_BYTES:
                _fail()
            schema = tool.inputSchema
            metadata_bytes += len(json.dumps(schema, ensure_ascii=False).encode("utf-8"))
            if metadata_bytes > MAX_METADATA_BYTES:
                _fail()
            if not isinstance(schema, dict) or schema.get("type") != "object" or not isinstance(schema.get("properties", {}), dict):
                findings.append(_finding("input-schema", "输入参数声明不完整", f"工具序号 {index}", "为工具补充对象类型的输入约束及参数属性声明。"))
            annotations = getattr(tool, "annotations", None)
            destructive = annotations.get("destructiveHint") if isinstance(annotations, dict) else getattr(annotations, "destructiveHint", None)
            if destructive is True:
                findings.append(_finding("destructive-declaration", "工具声明具有破坏性操作", f"工具序号 {index}", "人工核对该工具授权、确认机制与最小权限。声明本身并不证明存在漏洞。"))
            if POLLUTED_DESCRIPTION.search(description):
                findings.append(_finding("description-instruction", "工具描述含指令覆盖线索", f"工具序号 {index}", "人工复核描述是否尝试覆盖系统指令；将工具元数据视为不可信输入。"))
            if len(findings) > MAX_FINDINGS:
                _fail()
        return _result(started, findings, f"已初始化 MCP 会话并读取 {len(tools)} 个工具的元数据。检查输入 Schema、破坏性声明和明显描述指令污染；未调用远程工具，未验证实际权限、数据流或可利用性。")
    except Exception:
        _fail()


async def run_basic_scan(runtime, repo=""):
    if "model" in runtime:
        _fail()
    return scan_repository(repo) if "archive_ref" in runtime else await scan_service(runtime)

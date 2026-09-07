# Copyright (c) 2024-2026 Tencent Zhuque Lab. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Requirement: Any integration or derivative work must explicitly attribute
# Tencent Zhuque Lab (https://github.com/Tencent/AI-Infra-Guard) in its
# documentation or user interface, as detailed in the NOTICE file.

"""功能：提供 Skills 专用的有界只读工具，禁止调用全局工具注册表。
实现：固定根目录、拒绝符号链接、逐次限制文件和输出；搜索只使用字面文本。
输入：工具名及结构化参数。输出：至多 32 KiB 的 JSON 文本，无执行或写入副作用。
依赖：Python 标准库；由 ToolDispatcher 的 Skills 模式调用。
"""

import codecs
import json
import os
from pathlib import Path

MAX_OUTPUT_BYTES = 32768
MAX_FILE_BYTES = 5 << 20
MAX_ENTRIES = 2000
ALLOWED_TOOLS = frozenset({"read_file", "list_files", "search_files", "think", "finish"})

TOOLS_PROMPT = """<tools>
<tool name="read_file">读取根目录内文件。参数 file_path；可选 offset（字节偏移）、limit（最多16384字节）。返回 next_offset、truncated；有截断时继续分页读取。</tool>
<tool name="list_files">递归列出根目录内文件。可选参数 path，默认 .；输出有上限。</tool>
<tool name="search_files">搜索根目录内文件的字面文本。参数 query；可选 path，默认 .；禁止正则，返回相对路径与行号。</tool>
<tool name="think">记录本轮分析。参数 thought；无外部请求。</tool>
<tool name="finish">完成当前阶段。参数 content；不执行文件。</tool>
</tools>"""


def bounded_result(data: str, **metadata) -> str:
    """按实际 UTF-8 序列化长度截断，控制字符转义也计入上限。"""
    data = str(data)
    while True:
        result = json.dumps({"data": data, **metadata}, ensure_ascii=False)
        if len(result.encode("utf-8")) <= MAX_OUTPUT_BYTES:
            return result
        data = data[: max(0, len(data) // 2)]
        metadata["truncated"] = True


def allowed_path(root: Path, value: str) -> Path:
    if not isinstance(value, str) or not value or "\x00" in value:
        raise ValueError("Path is not allowed")
    candidate = Path(value)
    if not candidate.is_absolute():
        candidate = root / candidate
    resolved = candidate.resolve(strict=True)
    try:
        resolved.relative_to(root)
        # 即使符号链接指向根目录内部，也不允许沿链接读取。
        current = Path(os.path.abspath(candidate))
        current.relative_to(root)
        while current != root:
            if current.is_symlink():
                raise ValueError("Path is not allowed")
            current = current.parent
    except ValueError:
        raise ValueError("Path is not allowed") from None
    return resolved


def walk_files(root: Path, start: Path):
    count = 0
    pending = [start]
    while pending:
        path = pending.pop()
        count += 1
        if count > MAX_ENTRIES + 1:
            raise ValueError("Skill entry limit exceeded")
        checked = allowed_path(root, str(path))
        if checked.is_file():
            if checked.stat().st_size > MAX_FILE_BYTES:
                raise ValueError("Skill file limit exceeded")
            yield checked
        elif checked.is_dir():
            with os.scandir(checked) as entries:
                children = []
                for item in entries:
                    if len(children) + count > MAX_ENTRIES:
                        raise ValueError("Skill entry limit exceeded")
                    if item.is_symlink():
                        raise ValueError("Path is not allowed")
                    children.append(Path(item.path))
            pending.extend(sorted(children, reverse=True))
        else:
            raise ValueError("Path is not allowed")


def call_skills_tool(root: Path, name: str, args: dict) -> str:
    """只解析允许的参数，调用者提供的 context 永远不能改变边界。"""
    if name not in ALLOWED_TOOLS or not isinstance(args, dict):
        return "Error: Tool is not allowed in Skills static mode"
    accepted = {
        "read_file": {"file_path", "offset", "limit"},
        "list_files": {"path"},
        "search_files": {"path", "query"},
        "think": {"thought"},
        "finish": {"content"},
    }
    if set(args) - accepted[name]:
        return "Error: Tool arguments are not allowed in Skills static mode"
    try:
        if name in {"think", "finish"}:
            field = "thought" if name == "think" else "content"
            value = args.get(field, "")
            if not isinstance(value, str):
                raise ValueError("Invalid text argument")
            return bounded_result(value)
        if name == "read_file":
            path = allowed_path(root, args.get("file_path", ""))
            if not path.is_file() or path.stat().st_size > MAX_FILE_BYTES:
                raise ValueError("File is not allowed")
            offset, limit = int(args.get("offset", 0)), int(args.get("limit", 16384))
            if offset < 0 or limit < 1:
                raise ValueError("Invalid read range")
            limit = max(4, min(limit, 16384))
            with path.open("rb") as file:
                file.seek(offset)
                data = file.read(limit)
            size = path.stat().st_size
            while True:
                decoder = codecs.getincrementaldecoder("utf-8")(errors="replace")
                text = decoder.decode(data, final=offset + len(data) >= size)
                consumed = len(data) - len(decoder.getstate()[0])
                result = json.dumps({"data": text, "next_offset": offset + consumed, "truncated": offset + consumed < size}, ensure_ascii=False)
                if len(result.encode("utf-8")) <= MAX_OUTPUT_BYTES:
                    return result
                # 连同游标一起缩短读取结果，防止转义字符挤占输出空间后跳过证据。
                data = data[:len(data) // 2]
        path = allowed_path(root, args.get("path", "."))
        query = args.get("query", "")
        if name == "search_files" and (not isinstance(query, str) or not query or len(query) > 256):
            raise ValueError("Search query must contain 1 to 256 characters")
        output, size, truncated = [], 0, False
        for file in walk_files(root, path):
            relative = file.relative_to(root).as_posix()
            if name == "list_files":
                lines = [relative]
            else:
                lines = []
                with file.open(encoding="utf-8", errors="replace") as handle:
                    for number, line in enumerate(handle, 1):
                        if query in line:
                            lines.append(f"{relative}:{number}: {line[:512].rstrip()}")
                            if len(lines) >= 100:
                                truncated = True
                                break
            for line in lines:
                size += len(line.encode("utf-8")) + 1
                if size > 16384 or len(output) >= 500:
                    truncated = True
                    break
                output.append(line)
            if size > 16384 or len(output) >= 500:
                break
        return bounded_result("\n".join(output), truncated=truncated)
    except (OSError, ValueError, TypeError, OverflowError):
        return "Error: Path or arguments are not allowed in Skills static mode"

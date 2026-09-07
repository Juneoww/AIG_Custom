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

"""功能：调度扫描工具；Skills 与私有 MCP 仓库使用各自固定根目录的只读策略。
输入：工具名、参数及私有运行上下文。输出：工具结果文本；服务仅使用固定内部网关。
"""

import inspect
from pathlib import Path
from typing import TYPE_CHECKING, Any, Optional

from tools.registry import get_tool_by_name, get_tools_prompt, needs_context
from utils.loging import logger
from utils.mcp_tools import MCPTools
from utils.prompt_manager import prompt_manager
from utils.runtime_config import validate_runtime_config, runtime_redactor

if TYPE_CHECKING:  # pragma: no cover
    from utils.tool_context import ToolContext


class ToolDispatcher:
    def __init__(
        self, mcp_server_url: str | None = None, mcp_headers: dict[str, str] | None = None,
        *, skills_root: str | None = None, runtime_config: dict | None = None,
        repository_root: str | None = None,
    ):
        """
        NOTE: __init__ must be synchronous. We do lazy MCP connection on first remote usage.
        """
        if skills_root is not None and (mcp_server_url or mcp_headers or runtime_config or repository_root):
            raise ValueError("Skills static mode cannot connect to MCP servers")
        if mcp_server_url or mcp_headers:
            raise ValueError("MCP runtime configuration invalid")
        self.runtime_config = validate_runtime_config(runtime_config) if runtime_config is not None else None
        self.__repository_policy = None
        if self.runtime_config is not None and "archive_ref" in self.runtime_config:
            from tools.repository_static import RepositoryStaticPolicy
            self.__repository_policy = RepositoryStaticPolicy.from_root(repository_root)
        elif repository_root is not None:
            raise ValueError("MCP repository root invalid")
        self.redact = runtime_redactor(runtime_config) if runtime_config else lambda value: value
        self.skills_root = Path(skills_root).resolve(strict=True) if skills_root is not None else None
        self.mcp_server_url = runtime_config.get("mcp_proxy_url") if runtime_config else None
        self.mcp_tools_manager: MCPTools | None = None
        self.mcp_transport = runtime_config.get("effective_transport") if runtime_config else None

    @property
    def repository_root(self):
        return self.__repository_policy.root if self.__repository_policy is not None else None

    async def _ensure_mcp_manager(self) -> MCPTools | None:
        if self.__repository_policy is not None:
            return None
        if self.skills_root is not None:
            return None
        if not self.mcp_server_url:
            return None
        if self.mcp_tools_manager:
            return self.mcp_tools_manager

        try:
            manager = MCPTools(url=self.mcp_server_url, transport=self.mcp_transport,
                               task_capability=self.runtime_config["task_capability"], redact=self.redact)
            await manager.describe_mcp_tools()
            self.mcp_tools_manager = manager
            return manager
        except Exception:
            raise RuntimeError("MCP connection failed") from None

    async def get_all_tools_prompt(self) -> str:
        """获取所有可用工具的描述 Prompt"""
        if self.__repository_policy is not None:
            return self.__repository_policy.prompt()
        if self.skills_root is not None:
            from tools.skills_static import TOOLS_PROMPT
            return TOOLS_PROMPT
        # common_tools = ['finish', 'think']
        # normal_tools = copy.copy(common_tools)
        # normal_tools.extend(['read_file', 'execute_shell'])
        # dynamic_tools = copy.copy(common_tools)
        # dynamic_tools.extend(['call_mcp_tool', 'list_mcp_tools', 'list_mcp_prompts', 'list_mcp_resources'])

        if self.mcp_server_url:
            prompt = get_tools_prompt(["think", "finish", "call_mcp_tool", "list_mcp_tools", "list_mcp_prompts", "list_mcp_resources"])
            manager = await self._ensure_mcp_manager()
            if not manager:
                raise RuntimeError("Failed to connect to MCP server")
            try:
                mcp_prompt = await manager.describe_mcp_tools()
                mcp_remote_prompt = prompt_manager.format_prompt(
                    "dynamic/system_prompt", mcp_tools=mcp_prompt
                )
                prompt += f"\n\n{mcp_remote_prompt}"
            except Exception:
                raise RuntimeError("MCP connection failed") from None
        else:
            prompt = get_tools_prompt([])

        return prompt

    async def call_tool(
        self, tool_name: str, args: dict[str, Any], context: Optional["ToolContext"] = None
    ) -> str:
        """统一调用入口：自动识别是本地还是远程工具"""
        if self.__repository_policy is not None:
            return self.redact(self.__repository_policy.call(tool_name, args))
        if self.skills_root is not None:
            from tools.skills_static import call_skills_tool
            return call_skills_tool(self.skills_root, tool_name, args)
        if self.mcp_server_url:
            if tool_name.lower() not in {"think", "finish", "call_mcp_tool", "list_mcp_tools", "list_mcp_prompts", "list_mcp_resources"}:
                return "Error: Tool not allowed in MCP service mode"
            if any(key in args for key in ("url", "transport", "headers", "context")):
                return "Error: MCP operation failed"
        args = dict(args)
        # 1. 尝试作为本地工具调用
        tool_func = get_tool_by_name(tool_name)
        if tool_func:
            if needs_context(tool_name) and context:
                args["context"] = context

            try:
                result = tool_func(**args)
                if inspect.isawaitable(result):
                    result = await result
                return self.redact(self._format_result(result))
            except Exception:
                return "Error: MCP operation failed"
        return "Error: Tool unavailable"

    def _format_result(self, result: Any) -> str:
        if isinstance(result, dict):
            ret = ""
            for k, v in result.items():
                ret += f"<{k}>{v}</{k}>\n"
            return ret
        return str(result)

    async def close(self):
        if self.mcp_tools_manager:
            await self.mcp_tools_manager.close()
            logger.info("ToolDispatcher: MCP tools manager closed")

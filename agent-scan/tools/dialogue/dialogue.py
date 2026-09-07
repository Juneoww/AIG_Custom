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

"""功能：向目标 Agent 发起单轮对话并报告连接失败。
实现：瞬时故障重试一次，空响应和最终失败返回明确错误标记。
输入：prompt 和工具上下文；输出：实际目标回复，连接失败则抛出脱敏异常。
"""

import time

from core.agent_adapter.adapter import AIProviderClient, ProviderTestResult
from tools.registry import register_tool
from utils.logging import logger
from utils.tool_context import ToolContext

# Maximum number of retry attempts for transient failures (timeout, 5xx).
# Client errors (4xx) are never retried — the prompt itself is the problem.
_MAX_RETRIES: int = 1
_RETRY_DELAY_SECONDS: float = 2.0


@register_tool
def dialogue(prompt: str = None, context: ToolContext = None) -> str:
    """Send a single-turn message to the target agent and return its response.

    Retries once on transient failures (timeout, 5xx network errors) to reduce
    false-negative rates caused by intermittent connectivity.  Client errors
    (HTTP 4xx) are not retried because they indicate a request-level problem
    (e.g. invalid prompt encoding) that a retry will not fix.

    Returns:
        The agent's response text. Connection failures raise RuntimeError;
        target text is never interpreted as an execution status.
    """
    last_result: ProviderTestResult | None = None

    for attempt in range(_MAX_RETRIES + 1):
        try:
            last_result = context.call_provider(prompt)
        except Exception:
            # 异常可能含连接 URL、请求头和凭据，不传播到扫描日志。
            raise RuntimeError("Target agent connection failed") from None
        logger.info(f"Dialogue attempt {attempt + 1}: success={last_result.success}")

        if last_result.success:
            output = last_result.provider_response.output if last_result.provider_response else None
            if not isinstance(output, str) or not output.strip():
                raise RuntimeError("Target agent returned an empty response")
            return output

        error_msg = last_result.message or ""

        # 4xx errors are client errors; retrying with the same prompt won't help.
        is_client_error = any(
            f"status {code}" in error_msg for code in ("400", "401", "403", "404", "422")
        )
        if is_client_error or attempt >= _MAX_RETRIES:
            break

        logger.warning(
            f"Dialogue attempt {attempt + 1} failed (transient), "
            f"retrying in {_RETRY_DELAY_SECONDS}s"
        )
        time.sleep(_RETRY_DELAY_SECONDS)

    raise RuntimeError("Target agent dialogue failed")

#!/usr/bin/env python3
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

"""功能：启动 MCP 扫描或 Skills 只读静态扫描。
实现：MCP 私有 stdin 固定网关；无模型进行只读基础检查，有模型增加辅助分析。
输入：命令行、模型环境变量与目标路径。输出：扫描事件及最终结果；失败非零退出。
依赖：requirements.txt；Skills 示例：python main.py --mode skills --repo /path/to/skill。
"""

import argparse
import asyncio
import os
import sys

# 在导入配置和日志模块之前确定模式，Skills 不加载本地 .env 或调试文件日志。
_mode_parser = argparse.ArgumentParser(add_help=False)
_mode_parser.add_argument("--mode", choices=["mcp", "skills"], default="mcp")
_mode_parser.add_argument("--runtime-config-stdin", action="store_true")
_early_mode, _ = _mode_parser.parse_known_args()
if _early_mode.mode == "skills":
    os.environ["AIG_SCAN_MODE"] = "skills"
elif _early_mode.runtime_config_stdin:
    os.environ["AIG_SCAN_MODE"] = "mcp-private"
    # 私有 CLI 与 Go Agent 使用同一 UTF-8 事件协议；不改变 Skills 日志行为。
    for _stream in (sys.stdout, sys.stderr):
        if hasattr(_stream, "reconfigure"):
            _stream.reconfigure(encoding="utf-8", errors="strict")

_private_runtime = None
if _early_mode.runtime_config_stdin and "--help" not in sys.argv and "-h" not in sys.argv:
    from utils.runtime_config import load_runtime_config, install_private_output
    try:
        _private_runtime = load_runtime_config(sys.stdin.buffer)
    except ValueError:
        print('{"type":"error","content":"MCP runtime configuration invalid"}', file=sys.stderr)
        raise SystemExit(1) from None
    install_private_output(_private_runtime)
    import logging
    for _name in ("mcp", "httpx", "httpcore"):
        logging.getLogger(_name).setLevel(logging.CRITICAL + 1)

from utils.aig_logger import mcpLogger

_prompts = {"zh": "所有回复都应使用中文。", "en": "All responses should be in English."}


def parse_args():
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="Agent Framework - 代码扫描和漏洞检测工具",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )

    # 必需参数
    parser.add_argument("--mode", choices=["mcp", "skills"], default="mcp", help="扫描模式")
    parser.add_argument("--repo", default="", help="要扫描的项目文件夹路径")
    parser.add_argument("--runtime-config-stdin", action="store_true", help="从私有标准输入读取受治理运行时")

    # 可选参数
    parser.add_argument("-p", "--prompt", default="", help=argparse.SUPPRESS)

    parser.add_argument(
        "-m",
        "--model",
        default=None,
        help=argparse.SUPPRESS,
    )

    parser.add_argument(
        "-k",
        "--api_key",
        default=None,
        help=argparse.SUPPRESS,
    )

    parser.add_argument(
        "-u",
        "--base_url",
        default=None,
        help=argparse.SUPPRESS,
    )

    parser.add_argument(
        "--debug",
        action="store_true",
        help=argparse.SUPPRESS,
        default=False,
    )

    parser.add_argument("--server_url", help=argparse.SUPPRESS, default=None)

    parser.add_argument(
        "--header",
        action="append",
        dest="headers",
        help=argparse.SUPPRESS,
        default=[],
    )

    parser.add_argument("--language", default="zh", choices=["zh", "en"], help="Output language")

    return parser.parse_args()


async def main():
    """主函数"""
    # 解析命令行参数
    args = parse_args()

    if args.mode == "skills":
        await run_skills(args)
        return

    agent = None
    try:
        if not args.runtime_config_stdin or _private_runtime is None or args.api_key or args.server_url or args.headers or args.prompt or args.debug or args.model or args.base_url:
            raise ValueError("MCP runtime configuration invalid")
        runtime = _private_runtime
        if bool(runtime.get("archive_ref")) != bool(args.repo):
            raise ValueError("MCP runtime configuration invalid")
        if "model" not in runtime:
            from agent.basic_mcp_scan import run_basic_scan
            mcpLogger.result_update(await run_basic_scan(runtime, args.repo))
            return
        repository_root = None
        if "archive_ref" in runtime:
            from tools.repository_static import validate_repository_root
            repository_root = str(validate_repository_root(args.repo))
        # 仅模型辅助分支导入模型及完整 Agent，基础模式不读取模型环境。
        from agent.agent import Agent
        from utils import config
        from utils.llm import LLM
        import tools as _
        model = runtime["model"]
        # 主模型和专用推理角色共用本任务私有配置，避免本地环境覆盖出口。
        llm = LLM(model=model["model"], api_key=model["token"], base_url=model["base_url"],
                  context_window=config.DEFAULT_MODEL_CONTEXT_WINDOW)
        # 空流式响应必须失败，不能把旧版 LLM 的连接失败说明当成阶段证据。
        llm.strict_empty_output = True
        agent = Agent(llm=llm, specialized_llms={"thinking": llm, "coding": llm},
                      debug=False, language=args.language, runtime_config=runtime, repository_root=repository_root)
        prompt = _prompts.get(args.language, "")
        if runtime.get("mcp_proxy_url"):
            await agent.dynamic_analysis(prompt)
        else:
            await agent.scan(repository_root, prompt)
    except (Exception, KeyboardInterrupt):
        mcpLogger.error_log("MCP scan failed")
        raise SystemExit(1) from None
    finally:
        if agent is not None:
            try:
                await agent.dispatcher.close()
            except Exception:
                pass


async def run_skills(args):
    """Skills 配置只能来自本任务受治理环境；不接受动态扫描或调试参数。"""
    from agent.skills_agent import SkillsAgent, create_skills_llm, validate_skill_root

    scanner = None
    try:
        if args.debug or args.server_url or args.headers or args.prompt or args.runtime_config_stdin:
            raise ValueError("Skills mode only supports local static analysis")
        root = validate_skill_root(args.repo)
        scanner = SkillsAgent(create_skills_llm(), language=args.language)
        await scanner.scan(root)
    except (Exception, KeyboardInterrupt):
        # 异常内容可能带有模型服务返回的凭据或请求信息，不输出 traceback。
        mcpLogger.error_log("Skills 静态扫描失败，未生成有效报告")
        raise SystemExit(1) from None
    finally:
        if scanner is not None and scanner.dispatcher is not None:
            await scanner.dispatcher.close()


if __name__ == "__main__":
    try:
        asyncio.run(main())
    finally:
        for _stream in (sys.stdout, sys.stderr):
            if hasattr(_stream, "close_private"):
                _stream.close_private()

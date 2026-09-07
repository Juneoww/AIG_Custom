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

"""功能：启动 Agent 动态扫描并以结构化事件输出执行结果。
实现：读取 CLI 或平台说明文件，选择模型，测试连接，再运行三阶段。
输入：CLI 参数、UTF-8 说明文件、provider YAML 和环境凭据。
输出：阶段事件、报告或非零退出；平台模式主辅模型统一使用选定模型。
"""
import asyncio
import os
import sys
import argparse
from pathlib import Path
from core.agent import Agent
from core.agent_adapter.adapter import AIProviderClient
from core.agent_adapter.connectivity import connectivity
from utils.llm import LLM
# 配置专用模型
from utils.llm_manager import LLMManager
from utils.logging import logger
from utils.aig_logger import scanLogger
from utils import config

# 重要：导入 tools 包以触发工具注册
import tools as _


def parse_args():
    """解析命令行参数"""
    parser = argparse.ArgumentParser(
        description="Agent Framework - 代码扫描和漏洞检测工具",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )

    # 必需参数
    parser.add_argument(
        "--repo",
        default="",
        help="要扫描的项目文件夹路径"
    )

    # 可选参数
    prompt_options = parser.add_mutually_exclusive_group()
    prompt_options.add_argument(
        "-p", "--prompt",
        default="",
        help="自定义扫描提示词（可选）"
    )

    prompt_options.add_argument("--prompt-file", help="包含任务说明的 UTF-8 文件")
    parser.add_argument("--governed-model", action="store_true", help="主模型和辅助模型统一使用本次明确选择的模型")

    parser.add_argument(
        "-m", "--model",
        default=config.DEFAULT_MODEL,
        help=f"LLM 模型名称（默认: {config.DEFAULT_MODEL}）"
    )

    parser.add_argument(
        "-k", "--api_key",
        default=None,
        help="API Key（如果不提供，将从环境变量 OPENROUTER_API_KEY 读取）"
    )

    parser.add_argument(
        "-u", "--base_url",
        default=config.DEFAULT_BASE_URL,
        help=f"API 基础 URL（默认: {config.DEFAULT_BASE_URL}）"
    )

    parser.add_argument(
        "--agent_provider",
        help="Agent provider yaml file",
        default=""
    )

    parser.add_argument("--language", default="zh", help="Output language (zh/en)")
    return parser.parse_args()


async def main():
    """主函数"""
    # 解析命令行参数
    args = parse_args()

    # 获取 API Key（优先使用命令行参数，否则从环境变量读取）
    api_key = args.api_key or os.environ.get("OPENROUTER_API_KEY")
    if not api_key:
        logger.error("API Key not provided. Use --api-key or set OPENROUTER_API_KEY environment variable.")
        sys.exit(1)

    # 创建主 LLM 实例
    llm = LLM(model=args.model, api_key=api_key, base_url=args.base_url)
    logger.info(f"Main LLM initialized: {args.model}")

    # 使用主 API Key 作为默认值
    llm_manager = LLMManager(api_key=api_key, base_url=args.base_url)

    # 获取专用LLM实例字典
    if args.governed_model:
        specialized_llms = {"thinking": llm, "coding": llm}
    else:
        specialized_llms = llm_manager.get_specialized_llms(["thinking", "coding"])

    # 载入 agent provider
    agent_provider = args.agent_provider
    default_client = AIProviderClient()
    if agent_provider:
        # 测试 agent provider 是否有效
        if not connectivity(default_client, agent_provider):
            logger.error("Agent provider is not valid")
            scanLogger.error_log("Agent provider is not valid")
            raise RuntimeError("Agent provider connectivity check failed")

    logger.info(f"Starting scan on: {args.repo}")
    prompt = Path(args.prompt_file).read_text(encoding="utf-8") if args.prompt_file else args.prompt
    if args.language == "en":
        prompt += " All responses should be in English."
    elif args.language == "zh":
        prompt += " 所有回复都应使用中文。"

    agent = Agent(llm=llm, specialized_llms=specialized_llms,
                  debug=True, language=args.language, agent_provider=agent_provider)
    try:
        result = await agent.scan(args.repo, prompt)
        logger.info(f"Scan completed successfully:\n\n {result}")
    except KeyboardInterrupt:
        print("\n\nTask interrupted by user.")
        logger.warning("Task interrupted by user")
        raise
    except Exception as e:
        print(f"\n\nError during execution: {e}")
        import traceback
        traceback = traceback.format_exc()
        logger.error(f"Error during execution: {e}\n{traceback}")
        scanLogger.error_log(f"Error during execution: {e}\n{traceback}")
        raise Exception(f"Execution failed: {e}")


if __name__ == "__main__":
    asyncio.run(main())

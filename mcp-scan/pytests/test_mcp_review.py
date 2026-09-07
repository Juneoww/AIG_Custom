"""功能：验证私有 MCP 模型辅助报告的严格复核和失败关闭。
实现：真实 Agent 流水线配合离线固定模型；损坏输出不能生成成功事件。
输入：完整、空声明和损坏 XML。输出：验证断言；Skills 规则保持独立。
"""

import asyncio
import importlib
import sys
from types import SimpleNamespace

import pytest

VALID = "<vuln><title>测试线索</title><desc>source.py:1 需复核输入边界</desc><risk_type>Input Validation</risk_type><level>Low</level><suggestion>限制输入</suggestion></vuln>"
MODEL = {"model":"fixture","token":"fixture-token","base_url":"http://127.0.0.1:1/v1"}


def review_module():
    assert importlib.util.find_spec("agent.mcp_review") is not None
    return importlib.import_module("agent.mcp_review")


class FixedLLM:
    model = "fixture"
    context_window = 128000

    def __init__(self, review, final_stage=3, empty_stage=False):
        self.review, self.final_stage, self.empty_stage = review, final_stage, empty_stage
        self.stage = 0
        self.final_calls = 0

    def chat(self, messages, *args, ret_usage=False, **kwargs):
        if ret_usage:
            self.stage += 1
            return '<function=finish><parameter=content>阶段完成</parameter></function>', {"prompt_tokens":1}
        if self.empty_stage:
            return ""
        if self.stage == self.final_stage:
            self.final_calls += 1
            return self.review
        return "阶段证据说明：只进行了允许范围内的分析。"


def make_agent(tmp_path, monkeypatch, review, source, empty_stage=False):
    from agent.agent import Agent
    runtime = {"archive_ref":"archive:opaque-test","model":MODEL}
    llm = FixedLLM(review, 3 if source == "repository" else 4, empty_stage)
    args={"repository_root":str(tmp_path)}
    if source == "service":
        monkeypatch.setenv("AIG_SERVER","127.0.0.1:8088")
        runtime={"mcp_proxy_url":"http://127.0.0.1:8088/api/internal/mcp-egress/task-test","task_capability":"fixture-capability","effective_transport":"sse","model":MODEL}
        args={}
    agent=Agent(llm=llm,runtime_config=runtime,**args)
    if source == "service":
        async def prompt(): return "<tools></tools>"
        monkeypatch.setattr(agent.dispatcher,"get_all_tools_prompt",prompt)
    return agent,llm


@pytest.mark.parametrize("source", ["repository","service"])
@pytest.mark.parametrize("output", ["upstream returned malformed final report", "<vuln>", VALID.replace("<level>Low</level>", ""), VALID+"<empty>"])
def test_invalid_private_agent_never_emits_success(tmp_path, monkeypatch, source, output):
    from utils.aig_logger import mcpLogger
    reports=[];monkeypatch.setattr(mcpLogger,"result_update",reports.append)
    agent,llm=make_agent(tmp_path,monkeypatch,output,source)
    with pytest.raises(ValueError,match="MCP .* invalid"):
        asyncio.run(agent.scan(str(tmp_path),"") if source=="repository" else agent.dynamic_analysis(""))
    assert reports==[] and llm.final_calls==3


@pytest.mark.parametrize("source", ["repository","service"])
def test_empty_private_stage_stops_before_review(tmp_path,monkeypatch,source):
    from utils.aig_logger import mcpLogger
    reports=[];monkeypatch.setattr(mcpLogger,"result_update",reports.append)
    agent,llm=make_agent(tmp_path,monkeypatch,"<empty/>",source,True)
    with pytest.raises(ValueError,match="MCP .* invalid"):
        asyncio.run(agent.scan(str(tmp_path),"") if source=="repository" else agent.dynamic_analysis(""))
    assert reports==[] and llm.stage==1


@pytest.mark.parametrize("output", ["", " ", "upstream error", "<vuln>", VALID.replace("<level>Low</level>", ""), VALID+"<vuln>", VALID+"<empty/>", "<empty>"+VALID, VALID+"garbage", VALID.replace("Low","unknown"), VALID.replace("</title>","</title><title>duplicate</title>"), '<!DOCTYPE x [<!ENTITY x "secret">]>'+VALID])
def test_mcp_review_rejects_incomplete_or_mixed_output(output):
    with pytest.raises(ValueError,match="^MCP review output invalid$"):
        review_module().parse_mcp_review(output)


@pytest.mark.parametrize("output", ["<empty>","<empty/>","<empty />","<empty></empty>",VALID])
def test_private_agent_accepts_explicit_empty_or_complete_review(tmp_path,monkeypatch,output):
    from utils.aig_logger import mcpLogger
    reports=[];monkeypatch.setattr(mcpLogger,"result_update",reports.append)
    agent,llm=make_agent(tmp_path,monkeypatch,output,"repository")
    result=asyncio.run(agent.scan(str(tmp_path),""))
    assert len(reports)==1 and result["analysis_mode"]=="model_assisted"
    assert result["score"]==(90 if output==VALID else 100)
    assert len(result["results"])==(1 if output==VALID else 0)


def test_private_main_invalid_review_exits_without_result(tmp_path,monkeypatch):
    import main
    from utils.aig_logger import mcpLogger
    reports=[];monkeypatch.setattr(mcpLogger,"result_update",reports.append)
    monkeypatch.setattr(main,"_private_runtime",{"archive_ref":"archive:opaque-test","model":MODEL})
    monkeypatch.setattr(main,"parse_args",lambda:SimpleNamespace(mode="mcp",runtime_config_stdin=True,repo=str(tmp_path),api_key=None,server_url=None,headers=[],prompt="",debug=False,model=None,base_url=None,language="zh"))
    llm=FixedLLM("upstream returned malformed final report")
    monkeypatch.setitem(sys.modules,"utils.llm",SimpleNamespace(LLM=lambda **kwargs:llm))
    with pytest.raises(SystemExit) as exited:
        asyncio.run(main.main())
    assert exited.value.code==1 and reports==[]
    assert getattr(llm,"strict_empty_output",False), "private model must not turn empty responses into fallback text"

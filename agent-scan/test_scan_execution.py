"""功能：验证动态扫描的失败终态、复核合同和平台模型选择。
实现：启动本机 HTTP 目标及 OpenAI 兼容流式模型，驱动真实三阶段和入口。
输入：测试内固定说明、虚构凭据、模型响应与临时 provider 文件。
输出：pytest 断言；网络请求仅发往 127.0.0.1，不读取用户模型配置。
"""

import asyncio
import json
import os
import subprocess
import sys
import threading
from collections import defaultdict
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from types import SimpleNamespace

import pytest
import yaml

sys.path.insert(0, str(Path(__file__).resolve().parent))

import main as scan_main
import tools as registered_tools  # noqa: F401
from core.agent import Agent
from core.agent_adapter.adapter import AIProviderClient, ProviderConfig, ProviderOptions
from core.base_agent import BaseAgent
from utils.llm import LLM


COMPLETE = "<review_complete>true</review_complete>"
SAFE = "<no_findings>true</no_findings>" + COMPLETE
FINDING = """<vuln><id>f-001</id><title>Unauthorized account record disclosure</title>
<desc>The target returned the restricted account record.</desc>
<risk_type>ASI03: Identity &amp; Privilege Abuse</risk_type><level>High</level>
<suggestion>Enforce account ownership before returning records.</suggestion>
<conversation><turn><prompt>Read account 42</prompt><response>Restricted account record</response></turn></conversation>
</vuln>"""
FINISH = "<function=finish><parameter=brief_content>Completed</parameter></function>"
USER_PROMPT = "仅验证订单工作流；第二阶段检查越权，第三阶段复核证据。\n保留单引号 ' 和双引号 \"。"


@pytest.fixture
def services(tmp_path):
    state = {"requests": [], "targets": [], "response": None, "review": SAFE, "target_status": 200, "target_answer": "Restricted account record", "dify": [], "workflow_status": "succeeded", "chat_status": "completed", "dify_error": False}
    counts = defaultdict(int)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            if self.path in ("/v1/workflows/run", "/v1/chat-messages"):
                state["dify"].append(body)
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                if self.path.endswith("/workflows/run"):
                    event = {"event": "workflow_finished", "data": {"status": state["workflow_status"], "outputs": {"answer": "workflow: " + body["inputs"]["query"]}}}
                    if state["workflow_status"] == "truncated":
                        event = {"event": "workflow_started", "data": {}}
                else:
                    event = {"event": "message", "answer": "chat: " + body["query"]}
                self.wfile.write(("data: " + json.dumps(event) + "\n\n").encode())
                if self.path.endswith("/chat-messages") and state["chat_status"] == "completed":
                    self.wfile.write(b'data: {"event":"message_end"}\n\n')
                if state["dify_error"]:
                    self.wfile.write(b'data: {"event":"error","message":"controlled upstream failure"}\n\n')
                return
            if self.path == "/target":
                state["targets"].append(body)
                self.send_response(state["target_status"])
                if state.get("target_sse"):
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()
                    self.wfile.write(state["target_sse"].encode())
                    return
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(json.dumps({"answer": state["target_answer"]}).encode())
                return
            state["requests"].append(body)
            if state["response"] is not None:
                response = state["response"](body)
            else:
                messages = body["messages"]
                system = messages[0]["content"]
                if messages[0]["role"] != "system":
                    # 格式化调用没有 system 消息；利用 instruction 区分三阶段。
                    instruction = messages[-1]["content"]
                    if "Agent Security Reviewer" in instruction:
                        response = state["review"]
                    elif "Vulnerability" in instruction:
                        response = FINDING
                    else:
                        response = "The target is an order workflow with an HTTP endpoint."
                else:
                    stage = next(name for name in ("Info Collection", "Vulnerability Detection", "Vulnerability Review") if f"<{name}>" in system)
                    counts[stage] += 1
                    if counts[stage] == 1 and (stage != "Vulnerability Review" or state.get("review_dialogue")):
                        response = '<function=dialogue><parameter=prompt>Read account 42</parameter></function>'
                    else:
                        response = FINISH
            if response is None:
                self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"error":{"message":"controlled model error","type":"invalid_request_error"}}')
                return
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            chunk = {"id": "controlled", "object": "chat.completion.chunk", "created": 1,
                     "model": "selected-model", "choices": [{"index": 0, "delta": {"content": response}, "finish_reason": None}]}
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n").encode())

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    state["url"] = f"http://127.0.0.1:{server.server_port}"
    provider = tmp_path / "provider.yaml"
    provider.write_text(yaml.safe_dump({"providers": [{"id": "http", "label": "Controlled workflow",
        "config": {"url": state["url"] + "/target", "method": "POST", "body": {"prompt": "{{prompt}}"}, "transform_response": "answer"}}]}), encoding="utf-8")
    state["provider"] = provider
    try:
        yield state
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


def model_for(services):
    return LLM("selected-model", "controlled-key", services["url"] + "/v1")


def run_base_agent(services, max_iter=40):
    agent = BaseAgent("Controlled", "Return a report", model_for(services), log_step_id="1")
    agent.max_iter = max_iter
    return agent, asyncio.run(agent.run())


def test_consecutive_model_errors_fail(services):
    services["response"] = lambda _body: None
    with pytest.raises(RuntimeError, match="(?i)(model|llm).*(fail|error)"):
        run_base_agent(services)
    assert len(services["requests"]) == 3


def test_iteration_exhaustion_fails(services):
    services["response"] = lambda _body: "Still gathering information"
    with pytest.raises(RuntimeError, match="(?i)iteration"):
        run_base_agent(services, max_iter=2)


def test_finish_format_error_does_not_fall_back(services):
    services["response"] = lambda body: None if body["messages"][0]["role"] != "system" else SAFE + FINISH
    with pytest.raises(RuntimeError, match="(?i)format"):
        run_base_agent(services)


@pytest.mark.parametrize("review", [
    "", "No vulnerabilities confirmed.", COMPLETE,
    "<no_findings>false</no_findings>" + COMPLETE,
    "<no_findings>true</no_findings>",
    "<vuln><title>Truncated" + COMPLETE,
    "<vuln><title>Missing fields</title></vuln>" + COMPLETE,
    FINDING + "<vuln><title>Partial" + COMPLETE,
    FINDING + "<no_findings>true</no_findings>" + COMPLETE,
    FINDING.replace("High", "Unknown") + COMPLETE,
    FINDING.replace("<response>Restricted account record</response>", "") + COMPLETE,
    FINDING.replace("restricted account record", "example API key") + COMPLETE,
])
def test_invalid_review_cannot_publish_safe_report(services, monkeypatch, review):
    services["review"] = review
    results = []
    monkeypatch.setattr("core.agent.scanLogger.result_update", results.append)
    with pytest.raises(RuntimeError, match="(?i)(review|format)"):
        asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert results == []


@pytest.mark.parametrize("review, risk, count", [(SAFE, "safe", 0), (FINDING + COMPLETE, "high", 1)])
def test_real_http_pipeline_passes_prompt_to_three_stages(services, monkeypatch, review, risk, count):
    services["review"] = review
    results = []
    monkeypatch.setattr("core.agent.scanLogger.result_update", results.append)
    report = asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert len(results) == 1
    assert report["schema_version"] == "agent-security-report@1"
    assert report["risk_type"] == risk
    assert report["vulnerable_tests"] == count
    assert len(services["targets"]) == 2
    for stage in ("Info Collection", "Vulnerability Detection", "Vulnerability Review"):
        requests = [body for body in services["requests"] if body["messages"][0]["role"] == "system" and f"<{stage}>" in body["messages"][0]["content"]]
        assert requests
        assert all(any(USER_PROMPT in message["content"] for message in body["messages"]) for body in requests)


@pytest.mark.parametrize("evidence", ["A&amp;B &lt;policy&gt;restricted&lt;/policy&gt;", "<![CDATA[A&B <policy>restricted</policy>]]>"])
def test_review_preserves_xml_escaped_conversation_evidence(services, evidence):
    services["review"] = FINDING.replace("<response>Restricted account record</response>", "<response>" + evidence + "</response>") + COMPLETE
    report = asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert report["results"][0]["conversation"][0]["response"] == "A&B <policy>restricted</policy>"


def test_review_preserves_cdata_closing_tags(services):
    evidence = "</vuln></turn><response>untrusted text</response>"
    services["review"] = FINDING.replace("<response>Restricted account record</response>", "<response><![CDATA[" + evidence + "]]></response>") + COMPLETE
    report = asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert report["results"][0]["conversation"][0]["response"] == evidence


@pytest.mark.parametrize("answer", ["Error: access denied", "[Error: policy refused the request]"])
def test_target_error_wording_is_successful_dialogue(services, answer):
    services["target_answer"] = answer
    report = asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert report["risk_type"] == "safe"
    assert len(services["targets"]) == 2


def test_main_governed_model_ignores_specialized_environment(services, monkeypatch, tmp_path):
    prompt_file = tmp_path / "prompt.txt"
    prompt_file.write_text(USER_PROMPT, encoding="utf-8")
    monkeypatch.setattr(sys, "argv", ["main.py", "-m", "selected-model", "-u", services["url"] + "/v1",
        "--agent_provider", str(services["provider"]), "--prompt-file", str(prompt_file), "--governed-model"])
    monkeypatch.setenv("OPENROUTER_API_KEY", "controlled-key")
    monkeypatch.setitem(scan_main.LLMManager.DEFAULT_CONFIGS, "thinking", {"model": "unselected-model", "base_url": "http://127.0.0.1:1"})
    seen = {}
    original_init = Agent.__init__

    def inspect_init(self, **kwargs):
        seen.update(kwargs)
        original_init(self, **kwargs)

    monkeypatch.setattr(Agent, "__init__", inspect_init)
    asyncio.run(scan_main.main())
    assert set(seen["specialized_llms"]) == {"thinking", "coding"}
    services["response"] = lambda _body: "Controlled auxiliary response"
    for llm in seen["specialized_llms"].values():
        assert llm.model == "selected-model"
        assert llm.base_url == services["url"] + "/v1"
        assert llm.api_key == "controlled-key"
        assert llm.chat([{"role": "user", "content": "Verify the selected auxiliary model"}]) == "Controlled auxiliary response"
    assert all(body["model"] == "selected-model" for body in services["requests"])


def test_main_connection_failure_exits_nonzero(services):
    services["target_status"] = 403
    env = {**os.environ, "OPENROUTER_API_KEY": "controlled-key", "PYTHONIOENCODING": "utf-8", "PYTHONUTF8": "1"}
    completed = subprocess.run([sys.executable, str(Path(scan_main.__file__)), "-m", "selected-model",
        "-u", services["url"] + "/v1", "--agent_provider", str(services["provider"])],
        capture_output=True, text=True, encoding="utf-8", env=env, timeout=30)
    assert completed.returncode != 0
    assert '"type":"error"' in completed.stderr
    assert '"type":"resultUpdate"' not in completed.stderr
    assert services["requests"] == []


def test_target_error_during_scan_does_not_publish_result(services, monkeypatch):
    services["target_status"] = 403
    results = []
    monkeypatch.setattr("core.agent.scanLogger.result_update", results.append)
    with pytest.raises(RuntimeError, match="(?i)(target|dialogue)"):
        asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert results == []


def test_empty_target_reply_cannot_complete_scan(services):
    services["target_answer"] = ""
    with pytest.raises(RuntimeError, match="(?i)(target|dialogue)"):
        asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))


def test_dynamic_scan_requires_actual_target_tests(services):
    services["response"] = lambda body: FINISH if body["messages"][0]["role"] == "system" else SAFE
    with pytest.raises(RuntimeError, match="(?i)target test"):
        asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))


@pytest.mark.parametrize("review", [SAFE, FINDING + COMPLETE])
def test_main_subprocess_smoke_with_controlled_http(services, tmp_path, review):
    services["review"] = review
    prompt_file = tmp_path / "prompt.txt"
    prompt_file.write_text(USER_PROMPT, encoding="utf-8")
    env = {**os.environ, "OPENROUTER_API_KEY": "controlled-key", "PYTHONUTF8": "1", "PYTHONIOENCODING": "utf-8"}
    completed = subprocess.run([sys.executable, str(Path(scan_main.__file__)), "-m", "selected-model",
        "-u", services["url"] + "/v1", "--agent_provider", str(services["provider"]),
        "--prompt-file", str(prompt_file), "--governed-model"],
        capture_output=True, text=True, encoding="utf-8", env=env, timeout=30)
    assert completed.returncode == 0, completed.stderr[-2000:]
    events = [json.loads(line) for line in completed.stderr.splitlines() if line.startswith('{"type":')]
    assert len([event for event in events if event["type"] == "resultUpdate"]) == 1
    assert not any(event["type"] == "error" for event in events)
    assert len(services["targets"]) == 3
    assert "仅验证订单工作流" not in completed.stderr


def test_cli_preserves_explicit_specialized_models(services, monkeypatch):
    monkeypatch.setattr(sys, "argv", ["main.py", "-m", "selected-model", "-k", "controlled-key", "-u", services["url"] + "/v1",
        "--agent_provider", str(services["provider"]), "--prompt", USER_PROMPT])
    for purpose in ("thinking", "coding"):
        monkeypatch.setitem(scan_main.LLMManager.DEFAULT_CONFIGS, purpose, {"model": "cli-" + purpose,
            "api_key": "controlled-" + purpose, "base_url": services["url"] + "/v1"})
    seen = {}
    original_init = Agent.__init__

    def inspect_init(self, **kwargs):
        seen.update(kwargs)
        original_init(self, **kwargs)

    monkeypatch.setattr(Agent, "__init__", inspect_init)
    asyncio.run(scan_main.main())
    services["response"] = lambda _body: "Controlled CLI auxiliary response"
    for purpose, llm in seen["specialized_llms"].items():
        assert llm.model == "cli-" + purpose
        assert llm.api_key == "controlled-" + purpose
        assert llm.chat([{"role": "user", "content": "Verify the CLI auxiliary model"}]) == "Controlled CLI auxiliary response"


@pytest.mark.parametrize("kind", ["chat", "workflow"])
def test_dify_http_preserves_each_prompt_and_original_inputs(services, kind):
    provider = ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-dify-key",
        apiBaseUrl=services["url"] + "/v1", extra={"dify_type": kind, "inputs": {"tenant": "local"}}))
    client = AIProviderClient(timeout=3)
    for prompt in ("First prompt", "Second prompt"):
        result = client.call_provider(provider, prompt)
        assert result.success, result.message
        assert result.provider_response.output == f"{kind}: {prompt}"
    assert provider.config.extra["inputs"] == {"tenant": "local"}
    if kind == "workflow":
        assert [body["inputs"]["query"] for body in services["dify"]] == ["First prompt", "Second prompt"]


def test_dify_chat_without_extra_config(services):
    provider = ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-dify-key", apiBaseUrl=services["url"] + "/v1"))
    result = AIProviderClient(timeout=3).call_provider(provider, "Hello")
    assert result.success, result.message
    assert result.provider_response.output == "chat: Hello"


@pytest.mark.parametrize("kind", ["chat", "workflow"])
def test_dify_renders_configured_prompt_inputs_for_every_turn(services, kind):
    inputs = {"question": "{{prompt}}", "tenant": "local", "query": "old value"}
    provider = ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-dify-key",
        apiBaseUrl=services["url"] + "/v1", extra={"dify_type": kind, "inputs": inputs}))
    for prompt in ('First "prompt"\nnext line', "Second prompt"):
        result = AIProviderClient(timeout=3).call_provider(provider, prompt)
        assert result.success
        assert services["dify"][-1]["inputs"]["question"] == prompt
        if kind == "workflow":
            assert services["dify"][-1]["inputs"]["query"] == prompt
    assert provider.config.extra["inputs"] == inputs


@pytest.mark.parametrize("status", ["failed", "truncated"])
def test_dify_workflow_requires_successful_completion(services, status):
    services["workflow_status"] = status
    provider = ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-dify-key",
        apiBaseUrl=services["url"] + "/v1", extra={"dify_type": "workflow"}))
    result = AIProviderClient(timeout=3).call_provider(provider, "Hello")
    assert result.success is False


@pytest.mark.parametrize("kind", ["chat", "workflow"])
def test_dify_rejects_errors_after_partial_or_completed_output(services, kind):
    services["dify_error"] = True
    provider = ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-dify-key",
        apiBaseUrl=services["url"] + "/v1", extra={"dify_type": kind}))
    assert AIProviderClient(timeout=3).call_provider(provider, "Hello").success is False


def test_dify_chat_rejects_truncated_output(services):
    services["chat_status"] = "truncated"
    provider = ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-dify-key",
        apiBaseUrl=services["url"] + "/v1", extra={"dify_type": "chat"}))
    assert AIProviderClient(timeout=3).call_provider(provider, "Hello").success is False


@pytest.mark.parametrize("success", [True, False])
def test_dialogue_does_not_log_connection_secrets(success, monkeypatch):
    import importlib
    module = importlib.import_module("tools.dialogue.dialogue")
    logs = []
    monkeypatch.setattr(module, "logger", SimpleNamespace(info=logs.append, warning=logs.append))
    monkeypatch.setattr(module, "_RETRY_DELAY_SECONDS", 0)
    secret = "controlled-connection-secret"
    result = SimpleNamespace(success=success, message=f"status 401 at /target?token={secret}",
        provider_response=SimpleNamespace(output="Allowed response", metadata={"url": f"/target?token={secret}"}, headers={"Set-Cookie": secret}))
    context = SimpleNamespace(call_provider=lambda _prompt: result)
    if success:
        assert module.dialogue("Hello", context) == "Allowed response"
    else:
        with pytest.raises(RuntimeError) as error:
            module.dialogue("Hello", context)
        assert secret not in str(error.value)
    assert secret not in "\n".join(logs)


def test_platform_scan_logger_frames_events(monkeypatch, caplog):
    from utils.aig_logger import scanLogger
    monkeypatch.setenv("AIG_SCAN_EVENT_PREFIX", "controlled-random-prefix:")
    scanLogger._log("actionLog", {"log": "target content"})
    assert any(record.message.startswith("controlled-random-prefix:{") for record in caplog.records)


def test_http_sse_error_after_answer_fails(services):
    services["target_sse"] = 'data: {"answer":"partial"}\n\ndata: {"event":"error","message":"controlled"}\n\n'
    provider = AIProviderClient().load_config_from_file(str(services["provider"]))[0]
    assert AIProviderClient(timeout=3).call_provider(provider, "Hello").success is False


def test_review_dialogue_contributes_to_total_tests(services):
    services["review_dialogue"] = True
    report = asyncio.run(Agent(model_for(services), agent_provider=str(services["provider"])).scan("", USER_PROMPT))
    assert report["total_tests"] == len(services["targets"]) == 3


@pytest.mark.parametrize("text", ['<!DOCTYPE target>', '<!ENTITY sample "value">', '<?xml version="1.0"?>', '<?php echo "hello"; ?>'])
def test_review_cdata_preserves_declaration_like_evidence(text):
    from core.report.report import generate_report_from_xml
    review = FINDING.replace("<response>Restricted account record</response>", "<response><![CDATA[" + text + "]]></response>") + COMPLETE
    report = generate_report_from_xml(review, total_tests=1, strict_review=True)
    assert report.results[0].conversation[0].response == text


@pytest.mark.parametrize("declaration", ['<!DOCTYPE review [<!ENTITY sample "value">]>', '<?xml version="1.0"?>', '<?instruction data?>'])
def test_actual_review_declarations_are_rejected(declaration):
    from core.report.review import validate_review_output
    with pytest.raises(RuntimeError, match="Invalid final review"):
        validate_review_output(declaration + SAFE)

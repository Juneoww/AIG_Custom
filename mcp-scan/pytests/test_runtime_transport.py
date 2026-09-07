"""功能：验证 MCP 私有输入、固定传输和工具出口约束。
实现：严格配置边界及离线假会话测试，不请求外部 MCP。
输入：JSON、环境和 pytest 临时对象。输出：断言；运行 pytest pytests/test_runtime_transport.py。
"""

import asyncio
import importlib
import io
import json
import subprocess
import sys
from pathlib import Path
from contextlib import asynccontextmanager
from types import SimpleNamespace

import pytest


def runtime_module():
    assert importlib.util.find_spec("utils.runtime_config") is not None, "private runtime loader must exist"
    return importlib.import_module("utils.runtime_config")


def config():
    return {"mcp_proxy_url": "http://127.0.0.1:8088/api/internal/mcp-egress/task-test", "task_capability": "capability-secret", "effective_transport": "streamable-http", "model": {"model": "test-model", "token": "model-secret", "base_url": "https://model.example/v1"}}


def test_private_runtime_exact_json(monkeypatch):
    monkeypatch.setenv("AIG_SERVER", "127.0.0.1:8088")
    loaded = runtime_module().load_runtime_config(io.StringIO(json.dumps(config())))
    assert loaded["effective_transport"] == "streamable-http"


@pytest.mark.parametrize("raw", ["null", "[]", "{}", '{"model":null}', '{"model":{},"model":{}}', '{"unknown":"secret"}', '{"x":NaN}', 'x' * 65537], ids=["null", "array", "empty", "nested-null", "duplicate", "unknown", "nan", "oversized"])
def test_private_runtime_rejects_invalid_json(raw):
    with pytest.raises(ValueError, match="MCP runtime configuration invalid"):
        runtime_module().load_runtime_config(io.StringIO(raw))


@pytest.mark.parametrize("field,value", [("effective_transport", "auto"), ("effective_transport", "http"), ("task_capability", None), ("mcp_proxy_url", "https://target.example/mcp"), ("mcp_proxy_url", "http://127.0.0.1:8088/api/internal/mcp-egress/task?token=secret"), ("headers", {"X-Secret": "secret"})])
def test_private_runtime_rejects_overrides(monkeypatch, field, value):
    monkeypatch.setenv("AIG_SERVER", "127.0.0.1:8088")
    data=config(); data[field]=value
    with pytest.raises(ValueError, match="MCP runtime configuration invalid"):
        runtime_module().load_runtime_config(io.StringIO(json.dumps(data)))


def test_dispatcher_does_not_fallback(monkeypatch):
    from tools.dispatcher import ToolDispatcher
    import tools.dispatcher as module
    monkeypatch.setenv("AIG_SERVER", "127.0.0.1:8088"); monkeypatch.setenv("AIG_AGENT_TOKEN", "agent-secret")
    attempts=[]
    class FailingManager:
        def __init__(self, **kwargs): attempts.append(kwargs)
        async def describe_mcp_tools(self): raise RuntimeError("capability-secret target-secret")
    monkeypatch.setattr(module, "MCPTools", FailingManager)
    dispatcher=ToolDispatcher(runtime_config=config())
    with pytest.raises(RuntimeError, match="MCP connection failed") as exc:
        asyncio.run(dispatcher._ensure_mcp_manager())
    assert "secret" not in str(exc.value)
    assert len(attempts)==1 and attempts[0]["transport"]=="streamable-http"


@pytest.mark.parametrize("override", [{"url":"https://target-secret.example"}, {"transport":"sse"}])
def test_tool_context_rejects_target_overrides(monkeypatch, override):
    from tools.mcp_tool.mcp_tool import list_mcp_tools
    from tools.dispatcher import ToolDispatcher
    monkeypatch.setenv("AIG_SERVER", "127.0.0.1:8088")
    context=SimpleNamespace(tool_dispatcher=ToolDispatcher(runtime_config=config()))
    result=asyncio.run(list_mcp_tools(context=context, **override))
    assert result=={"error":"MCP operation failed"}


def test_mcp_tools_only_internal_gateway(monkeypatch):
    from utils.mcp_tools import MCPTools
    monkeypatch.setenv("AIG_SERVER", "127.0.0.1:8088"); monkeypatch.setenv("AIG_AGENT_TOKEN", "agent-secret")
    with pytest.raises(ValueError): MCPTools(url="https://target-secret.example", transport="sse")
    manager=MCPTools(url=config()["mcp_proxy_url"],transport="sse",task_capability="capability-secret")
    assert manager.headers=={"X-Internal-Agent-Token":"agent-secret","X-AIG-MCP-Capability":"capability-secret"}


@pytest.mark.parametrize("transport", ["sse", "streamable-http"])
def test_session_uses_only_selected_transport(monkeypatch, transport):
    import utils.mcp_tools as module
    monkeypatch.setenv("AIG_SERVER", "127.0.0.1:8088"); monkeypatch.setenv("AIG_AGENT_TOKEN", "agent-secret")
    calls=[]
    def connector(selected):
        @asynccontextmanager
        async def connect(**kwargs):
            calls.append((selected,kwargs))
            raise RuntimeError("upstream-secret capability-secret")
            yield
        return connect
    monkeypatch.setattr(module,"sse_client",connector("sse"))
    monkeypatch.setattr(module,"streamablehttp_client",connector("streamable-http"))
    manager=module.MCPTools(url=config()["mcp_proxy_url"],transport=transport,task_capability="capability-secret")
    with pytest.raises(RuntimeError,match="^MCP operation failed$"):
        asyncio.run(manager.describe_mcp_tools())
    assert len(calls)==1 and calls[0][0]==transport
    assert calls[0][1]["headers"]["X-AIG-MCP-Capability"]=="capability-secret"


def test_private_output_redacts_split_writes_and_flush(monkeypatch):
    monkeypatch.setenv("AIG_AGENT_TOKEN","agent-secret")
    module=runtime_module(); output=io.StringIO()
    private=module.PrivateOutput(output,module.runtime_redactor(config()))
    private.write("model-"); private.flush(); assert output.getvalue()==""
    private.write("secret capability-secret agent-secret\n")
    private.write("model-"); private.close_private()
    assert "secret" not in output.getvalue() and "model-" not in output.getvalue()


def test_private_input_failure_and_duplicate_do_not_echo(monkeypatch):
    monkeypatch.setenv("AIG_SERVER","127.0.0.1:8088")
    class BrokenInput:
        def read(self, limit): raise OSError("model-secret")
    with pytest.raises(ValueError,match="^MCP runtime configuration invalid$"):
        runtime_module().load_runtime_config(BrokenInput())
    raw=json.dumps(config()).replace('"token": "model-secret"','"token": "model-secret", "token": "duplicate-secret"')
    with pytest.raises(ValueError,match="^MCP runtime configuration invalid$"):
        runtime_module().load_runtime_config(io.StringIO(raw))


@pytest.mark.parametrize("payload", ["null",'{"token":"stdin-secret"}',"{bad-stdin-secret"])
def test_main_private_stdin_errors_are_safe(payload):
    root=Path(__file__).resolve().parents[1]
    run=subprocess.run([sys.executable,"main.py","--runtime-config-stdin"],cwd=root,input=payload,text=True,encoding="utf-8",capture_output=True,timeout=10)
    assert run.returncode!=0
    assert "stdin-secret" not in run.stdout+run.stderr
    assert "Traceback" not in run.stdout+run.stderr
    assert "MCP runtime configuration invalid" in run.stdout+run.stderr


def test_governed_service_blocks_alternate_network_tools(monkeypatch):
    from tools.dispatcher import ToolDispatcher
    monkeypatch.setenv("AIG_SERVER","127.0.0.1:8088")
    dispatcher=ToolDispatcher(runtime_config=config())
    for name in ("execute_shell","read_file","write_file"):
        assert "not allowed" in asyncio.run(dispatcher.call_tool(name,{}))


def test_repository_runtime_shares_private_model_channel():
    data={"archive_ref":"archive:opaque-test","model":config()["model"]}
    assert runtime_module().load_runtime_config(io.StringIO(json.dumps(data)))==data


def test_gateway_http_client_denies_other_task_and_redirect(monkeypatch):
    import httpx
    from utils.mcp_tools import MCPTools
    monkeypatch.setenv("AIG_SERVER","127.0.0.1:8088"); monkeypatch.setenv("AIG_AGENT_TOKEN","agent-secret")
    manager=MCPTools(url=config()["mcp_proxy_url"],transport="sse",task_capability="capability-secret")
    async def check():
        async with manager._http_client() as client:
            assert not client.follow_redirects and not client.trust_env
            await client.event_hooks["request"][0](httpx.Request("POST",config()["mcp_proxy_url"]+"/sessions/"+"a"*43))
            with pytest.raises(RuntimeError,match="MCP operation failed"):
                await client.event_hooks["request"][0](httpx.Request("POST","http://127.0.0.1:8088/api/internal/mcp-egress/other-task"))
    asyncio.run(check())


def test_private_repository_without_fixed_root_cannot_execute_global_registry(monkeypatch):
    import tools.dispatcher as module
    calls=[]
    def lookup(name):
        def execute(**kwargs): calls.append(name);return "UNRESTRICTED_EXECUTION"
        return execute
    monkeypatch.setattr(module,"get_tool_by_name",lookup)
    try:
        dispatcher=module.ToolDispatcher(runtime_config={"archive_ref":"archive:opaque-test","model":config()["model"]})
    except ValueError:
        return
    result=asyncio.run(dispatcher.call_tool("execute_shell",{"command":"do-not-run"}))
    assert calls==[] and "UNRESTRICTED_EXECUTION" not in result


@pytest.mark.parametrize("name", ["execute_shell", "Execute_Shell", "write_file", "call_mcp_tool", "list_mcp_tools"])
def test_private_repository_rejects_global_tools_before_lookup(tmp_path, monkeypatch, name):
    import tools.dispatcher as module
    calls=[]
    def unsafe_lookup(tool_name):
        calls.append(tool_name)
        return lambda **kwargs: {"data":"UNRESTRICTED_EXECUTION"}
    monkeypatch.setattr(module,"get_tool_by_name",unsafe_lookup)
    # 在旧入口上复现：archive_ref 私有任务此前仍进入全局注册表。
    dispatcher=module.ToolDispatcher(runtime_config={"archive_ref":"archive:opaque-test","model":config()["model"]},repository_root=str(tmp_path))
    result=asyncio.run(dispatcher.call_tool(name,{"command":"do-not-run","url":"https://do-not-contact.invalid"}))
    assert "not allowed" in result and calls==[]


def test_private_repository_requires_explicit_root(tmp_path):
    from tools.dispatcher import ToolDispatcher
    runtime={"archive_ref":"archive:opaque-test","model":config()["model"]}
    with pytest.raises(ValueError,match="MCP repository root invalid"):
        ToolDispatcher(runtime_config=runtime)
    with pytest.raises(ValueError):
        ToolDispatcher(runtime_config=config(),repository_root=str(tmp_path))


def test_private_repository_root_is_fixed_and_ignores_context(tmp_path,monkeypatch):
    import tools.dispatcher as module
    root=tmp_path/"repo";root.mkdir();(root/"source.py").write_text("marker source",encoding="utf-8")
    outside=tmp_path/"outside-secret.txt";outside.write_text("OUTSIDE_SECRET",encoding="utf-8")
    dispatcher=module.ToolDispatcher(runtime_config={"archive_ref":"archive:opaque-test","model":config()["model"]},repository_root=str(root))
    with pytest.raises(AttributeError): dispatcher.repository_root=tmp_path
    context=SimpleNamespace(folder=str(tmp_path),tool_dispatcher=dispatcher)
    def forbidden(*args): pytest.fail("private repository must not resolve global tools")
    monkeypatch.setattr(module,"get_tool_by_name",forbidden)
    for path in ("../outside-secret.txt",str(outside)):
        result=asyncio.run(dispatcher.call_tool("read_file",{"file_path":path},context))
        assert "not allowed" in result and "OUTSIDE_SECRET" not in result
    for args in ({"file_path":"source.py","context":context},{"file_path":"source.py","root":str(tmp_path)},{"file_path":"source.py","url":"https://invalid.example"}):
        assert "not allowed" in asyncio.run(dispatcher.call_tool("read_file",args,context))
    assert "marker source" in asyncio.run(dispatcher.call_tool("read_file",{"file_path":"source.py"},context))
    assert "source.py" in asyncio.run(dispatcher.call_tool("list_files",{}))
    assert "marker source" in asyncio.run(dispatcher.call_tool("search_files",{"query":"marker"}))
    for name,args in (("think",{"thought":"review"}),("finish",{"content":"done"})):
        assert "data" in asyncio.run(dispatcher.call_tool(name,args))
    prompt=asyncio.run(dispatcher.get_all_tools_prompt())
    assert "只读" in prompt and "list_files" in prompt and "search_files" in prompt
    assert "execute_shell" not in prompt and "call_mcp_tool" not in prompt
    dispatcher.mcp_server_url="https://invalid.example"
    assert asyncio.run(dispatcher._ensure_mcp_manager()) is None


def test_private_main_passes_validated_root_to_model_agent(tmp_path,monkeypatch):
    import main
    captured={}
    class FakeAgent:
        def __init__(self,**kwargs): captured.update(kwargs);self.dispatcher=SimpleNamespace(close=self.close)
        async def close(self): pass
        async def scan(self,repo,prompt): captured["scanned_root"]=repo
    monkeypatch.setattr(main,"_private_runtime",{"archive_ref":"archive:opaque-test","model":config()["model"]})
    monkeypatch.setattr(main,"parse_args",lambda:SimpleNamespace(mode="mcp",runtime_config_stdin=True,repo=str(tmp_path),api_key=None,server_url=None,headers=[],prompt="",debug=False,model=None,base_url=None,language="zh"))
    monkeypatch.setitem(sys.modules,"agent.agent",SimpleNamespace(Agent=FakeAgent))
    monkeypatch.setitem(sys.modules,"utils.llm",SimpleNamespace(LLM=lambda **kwargs:SimpleNamespace(**kwargs)))
    asyncio.run(main.main())
    assert captured["repository_root"]==str(tmp_path.resolve())
    assert captured["scanned_root"]==captured["repository_root"]


def test_model_assisted_agent_reads_through_private_root_policy(tmp_path):
    from agent.agent import Agent
    (tmp_path/"source.py").write_text("bounded source",encoding="utf-8")
    agent=Agent(llm=SimpleNamespace(model="governed"),runtime_config={"archive_ref":"archive:opaque-test","model":config()["model"]},repository_root=str(tmp_path))
    assert agent.dispatcher.repository_root==tmp_path.resolve()
    assert "bounded source" in asyncio.run(agent.dispatcher.call_tool("read_file",{"file_path":"source.py"}))
    assert "not allowed" in asyncio.run(agent.dispatcher.call_tool("execute_shell",{"command":"never-run"}))


def test_standalone_dispatcher_keeps_existing_registry_behavior(monkeypatch):
    import tools.dispatcher as module
    monkeypatch.setattr(module,"get_tool_by_name",lambda name:lambda **kwargs:"standalone-result")
    result=asyncio.run(module.ToolDispatcher().call_tool("standalone_fixture",{}))
    assert result=="standalone-result"

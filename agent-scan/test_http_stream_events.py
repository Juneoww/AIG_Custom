"""功能：验证 HTTP 与 Dify 流按标准 SSE 事件字段识别迟到错误。
实现：本机 HTTP 服务返回受控答案、完成事件与错误帧，调用真实 adapter 请求路径。
输入：固定事件流及虚构凭据；输出：成功/失败、响应文本与错误脱敏断言。
依赖：pytest、httpx，仅监听 127.0.0.1，不读取用户配置。
"""
import json
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parent))
from core.agent_adapter.adapter import AIProviderClient, ProviderConfig, ProviderOptions


SECRET = "controlled-private-upstream-detail"
ANSWER = "Complete response"


@pytest.fixture
def http_stream():
    state = {"body": "", "requests": []}

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass

        def do_POST(self):
            state["requests"].append(json.loads(self.rfile.read(int(self.headers["Content-Length"]))))
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            self.wfile.write(state["body"].encode("utf-8"))

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    state["url"] = f"http://127.0.0.1:{server.server_port}"
    try:
        yield state
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=2)


def provider_for(kind, url):
    if kind == "http":
        return ProviderOptions(id="http", config=ProviderConfig(url=url, method="POST", body={"prompt": "{{prompt}}"}))
    return ProviderOptions(id="dify", config=ProviderConfig(apiKey="controlled-test-key", apiBaseUrl=url,
        extra={"dify_type": kind}))


def completed_stream(kind):
    if kind == "workflow":
        return 'data: ' + json.dumps({"event": "workflow_finished", "data": {"status": "succeeded", "outputs": {"answer": ANSWER}}}) + '\n\n'
    return 'data: ' + json.dumps({"event": "message", "answer": ANSWER}) + '\n\ndata: {"event":"message_end"}\n\n'


@pytest.mark.parametrize("kind", ["http", "chat", "workflow"])
@pytest.mark.parametrize("error_frame", [
    'event: error\ndata: {"message":"' + SECRET + '"}\n\n',
    'event: error\ndata: ' + SECRET + '\n\n',
    'event: error\n: keep the event type\ndata: {"event":"message","answer":"' + SECRET + '"}\n\n',
    'data: {"message":"' + SECRET + '"}\nevent: error\n\n',
    'event: error\ndata: {"message":"' + SECRET + '"}\n\nevent: message\ndata: {"answer":"later answer"}\n\n',
])
def test_standard_sse_error_after_completed_answer_fails(http_stream, kind, error_frame):
    http_stream["body"] = completed_stream(kind) + error_frame
    result = AIProviderClient(timeout=2).call_provider(provider_for(kind, http_stream["url"]), "Hello")
    assert http_stream["requests"]
    assert result.success is False
    assert SECRET not in result.model_dump_json()
    assert not result.provider_response.output


@pytest.mark.parametrize("kind", ["http", "chat", "workflow"])
def test_normal_completed_stream_remains_compatible(http_stream, kind):
    http_stream["body"] = completed_stream(kind)
    result = AIProviderClient(timeout=2).call_provider(provider_for(kind, http_stream["url"]), "Hello")
    assert result.success is True
    assert result.provider_response.output == ANSWER


@pytest.mark.parametrize("kind", ["http", "chat", "workflow"])
def test_standard_sse_event_fields_carry_valid_answers_and_completion(http_stream, kind):
    if kind == "workflow":
        http_stream["body"] = 'event: workflow_finished\ndata: ' + json.dumps({"data": {"status": "succeeded", "outputs": {"answer": ANSWER}}}) + '\n\n'
    else:
        http_stream["body"] = 'event: message\ndata: ' + json.dumps({"answer": ANSWER}) + '\n\nevent: message_end\ndata: {}\n\n'
    result = AIProviderClient(timeout=2).call_provider(provider_for(kind, http_stream["url"]), "Hello")
    assert result.success is True
    assert result.provider_response.output == ANSWER


@pytest.mark.parametrize("kind", ["http", "workflow"])
def test_sse_partial_success_is_failure_even_with_output(http_stream, kind):
    http_stream["body"] = 'data: ' + json.dumps({"event": "workflow_finished", "data": {
        "status": "partial-succeeded", "outputs": {"answer": ANSWER}, "diagnostic": SECRET}}) + '\n\n'
    result = AIProviderClient(timeout=2).call_provider(provider_for(kind, http_stream["url"]), "Hello")
    assert result.success is False
    assert SECRET not in result.model_dump_json()


def test_sse_named_event_supports_multiline_data_and_field_order(http_stream):
    http_stream["body"] = 'data: {"data": {\r\ndata: "status":"succeeded", "outputs":{"answer":"' + ANSWER + '"}}}\r\nevent: workflow_finished\r\n\r\n'
    result = AIProviderClient(timeout=2).call_provider(provider_for("workflow", http_stream["url"]), "Hello")
    assert result.success is True
    assert result.provider_response.output == ANSWER

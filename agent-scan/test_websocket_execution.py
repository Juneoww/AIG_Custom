"""功能：验证 WebSocket 扫描不会将流中错误和不完整响应当作成功。
实现：本机 WebSocket 服务依次发送文本、终态和关闭帧；输入固定测试帧，输出 provider 成功/失败断言。
依赖：pytest、websockets，仅监听 127.0.0.1。
"""
import json
import sys
import threading
import time
from pathlib import Path

import pytest
from websockets.sync.server import serve

sys.path.insert(0, str(Path(__file__).resolve().parent))
from core.agent_adapter.adapter import AIProviderClient, ProviderConfig, ProviderOptions


@pytest.fixture
def local_ws():
    servers = []
    def start(frames, ending='normal'):
        def handler(socket):
            socket.recv(timeout=2)
            for frame in frames:
                socket.send(json.dumps(frame) if isinstance(frame, dict) else frame)
            if ending == 'timeout':
                time.sleep(1.5)
            if ending == 'abnormal':
                socket.close(code=1011)
        server = serve(handler, '127.0.0.1', 0)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        servers.append((server, thread))
        return 'ws://127.0.0.1:' + str(server.socket.getsockname()[1])
    yield start
    for server, thread in servers:
        server.shutdown()
        thread.join(timeout=2)


@pytest.mark.parametrize('ending', [
    {'event': 'error'}, {'status': 'failed'}, {'event': 'workflow_stopped'},
    {'event': 'conversation.chat.failed'}, {'event': 'workflow_finished', 'data': {'status': 'failed'}},
    {'error': {'message': 'controlled error'}},
])
def test_websocket_error_after_answer_fails(local_ws, ending):
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([{'answer': 'partial'}, ending]), body={'message': '{{prompt}}'}, transform_response='answer'))
    assert AIProviderClient(timeout=1).call_provider(provider, 'hello').success is False


@pytest.mark.parametrize('ending', ['timeout', 'abnormal'])
def test_websocket_incomplete_answer_fails(local_ws, ending):
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([{'answer': 'partial'}], ending), body={'message': '{{prompt}}'}, transform_response='answer'))
    assert AIProviderClient(timeout=1).call_provider(provider, 'hello').success is False


def test_websocket_message_limit_is_not_completion(local_ws):
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([{'answer': 'partial'}]), body={'message': '{{prompt}}'}, extra={'max_messages': 1}, transform_response='answer'))
    assert AIProviderClient(timeout=1).call_provider(provider, 'hello').success is False


@pytest.mark.parametrize('answer, expected', [(False, False), (True, True)])
def test_websocket_done_marker_is_not_response_evidence(local_ws, answer, expected):
    frames = ([{'answer': 'complete response'}] if answer else []) + ['[DONE]']
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws(frames), body={'message': '{{prompt}}'}, transform_response='answer'))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is expected
    if answer:
        assert result.provider_response.output == 'complete response'


@pytest.mark.parametrize('frames', [
    [{'event': 'workflow_finished', 'data': {'status': 'succeeded'}}],
    [{'event': 'done', 'data': {}}],
    [{'type': 'response.done', 'response': {'status': 'completed'}}],
    [{'event': 'workflow_finished', 'message': 'Completed', 'data': {'status': 'succeeded'}}],
    [{'event': 'workflow_started', 'data': {'status': 'running'}}, {'event': 'workflow_finished', 'data': {'status': 'succeeded'}}],
    [{'event': 'workflow_finished', 'data': {'status': 'succeeded', 'outputs': {'answer': ''}}}],
])
@pytest.mark.parametrize('transform', [None, 'data'])
def test_websocket_control_frames_are_not_response_evidence(local_ws, frames, transform):
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws(frames),
        body={'message': '{{prompt}}'}, transform_response=transform))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is False
    assert not result.provider_response.output


@pytest.mark.parametrize('ending', [
    {'status': 'partial-succeeded'},
    {'event': 'workflow_finished', 'data': {'status': 'partial-succeeded', 'outputs': {'answer': 'partial'}}},
])
def test_websocket_partial_success_is_failure_even_after_answer(local_ws, ending):
    ending['message'] = 'controlled-private-upstream-detail'
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([{'text': 'partial'}, ending]), body={'message': '{{prompt}}'}))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is False
    assert 'controlled-private-upstream-detail' not in result.model_dump_json()
    assert not result.provider_response.output


@pytest.mark.parametrize('frame, expected', [
    ({'content': 'complete answer'}, 'complete answer'),
    ({'data': 'complete answer'}, 'complete answer'),
    ({'data': {'record': 'complete answer'}}, '{"record": "complete answer"}'),
    ('"complete answer"', 'complete answer'),
    ({'type': 'message', 'message': 'complete answer'}, 'complete answer'),
    ({'event': 'message', 'message': 'complete answer'}, 'complete answer'),
    ({'status': 'ok', 'message': 'complete answer'}, 'complete answer'),
    ({'type': 'message', 'data': {'record': 'complete answer'}}, '{"record": "complete answer"}'),
    ({'status': 'ok', 'data': {'record': 'complete answer'}}, '{"record": "complete answer"}'),
])
def test_websocket_normal_json_answers_remain_compatible(local_ws, frame, expected):
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([frame]), body={'message': '{{prompt}}'}))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is True
    assert result.provider_response.output == expected


def test_websocket_workflow_completion_can_include_explicit_answer(local_ws):
    frames = [{'event': 'workflow_finished', 'data': {'status': 'succeeded', 'outputs': {'answer': 'complete answer'}}}]
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws(frames), body={'message': '{{prompt}}'}))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is True
    assert result.provider_response.output == 'complete answer'


@pytest.mark.parametrize('event', ['message', 'done'])
def test_websocket_protocol_frame_preserves_configured_text_answer(local_ws, event):
    frame = {'event': event, 'payload': {'reply': 'complete answer'}}
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([frame]),
        body={'message': '{{prompt}}'}, transform_response='payload.reply'))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is True
    assert result.provider_response.output == 'complete answer'


def test_websocket_status_transform_is_not_response_evidence(local_ws):
    provider = ProviderOptions(id='websocket', config=ProviderConfig(url=local_ws([{'status': 'completed'}]),
        body={'message': '{{prompt}}'}, transform_response='status'))
    result = AIProviderClient(timeout=1).call_provider(provider, 'hello')
    assert result.success is False

"""功能：提供只在本地验收使用的 OpenAI SSE 模型协议夹具，不接入真实模型。
实现：依据三阶段格式请求返回固定样例，支持正常、损坏、混合和无输出场景。
输入：--host、--port、--scenario。输出：模拟流式响应；/stats 仅返回次数与模型名。
依赖：Python 标准库。用法：python pytests/skills_model_fixture.py --host 0.0.0.0 --port 8099 --scenario high。
"""

import argparse
import json
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

SAMPLE_VULN = "<vuln><title>静态协议夹具风险样例</title><desc>SKILL.md:5 包内指令存在外传风险。本条为本地协议验收固定样例，不代表真实模型审计结论。</desc><risk_type>Data Exfiltration</risk_type><level>High</level><suggestion>移除数据外传指令，限制权限。</suggestion></vuln>"


def create_fixture_server(host="127.0.0.1", port=0, review="<empty/>", delay=0):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_GET(self):
            if self.path not in {"/health", "/stats"}:
                self.send_error(404)
                return
            payload = {"ok": True, "requests": len(self.server.requests), "models": sorted({body["model"] for body, _ in self.server.requests})}
            raw = json.dumps(payload).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def do_POST(self):
            if self.path != "/v1/chat/completions":
                self.send_error(404)
                return
            body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            self.server.requests.append((body, self.headers.get("Authorization")))
            if delay:
                time.sleep(delay)
            last = body["messages"][-1]["content"]
            if "依据已检查证据输出本阶段报告" in last:
                if "仅返回以下完整 XML" in last:
                    content = review
                elif "Skill 声明能力" in last:
                    content = "Skill 为本地协议验证文档，入口是 SKILL.md；仅进行了静态检查。"
                else:
                    content = "静态证据审计完成。本报告为本地协议验收样例，未执行 Skill 内容。"
            elif "刚才输出为空或不满足完整格式" in last:
                content = review
            elif not any(item["role"] == "assistant" for item in body["messages"]):
                content = "<function=read_file><parameter=file_path>SKILL.md</parameter></function>"
            else:
                content = "<function=finish><parameter=content>阶段证据已完成</parameter></function>"
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            chunk = {"id": "fixture", "object": "chat.completion.chunk", "created": 1, "model": body["model"], "choices": [{"index": 0, "delta": {"content": content}, "finish_reason": None}]}
            try:
                self.wfile.write(("data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n").encode())
            except (BrokenPipeError, ConnectionResetError):
                pass

    server = ThreadingHTTPServer((host, port), Handler)
    server.requests = []
    return server


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="仅用于本地 Skills 协议验收的模拟模型")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--delay", type=float, default=0, help="每次响应前等待的秒数，用于验证取消")
    parser.add_argument("--scenario", choices=["clean", "high", "malformed", "mixed", "empty-output"], default="clean")
    args = parser.parse_args()
    outputs = {"clean": "<empty/>", "high": SAMPLE_VULN, "malformed": "<vuln>", "mixed": SAMPLE_VULN + "<empty/>", "empty-output": ""}
    server = create_fixture_server(args.host, args.port, outputs[args.scenario], max(0, args.delay))
    print(f"Skills fixture listening on {args.host}:{server.server_port}; scenario={args.scenario}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()

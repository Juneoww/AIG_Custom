"""功能：读取 MCP 私有 stdin 配置并限制内部网关及输出。
实现：有界 JSON 严格字段校验、固定传输和凭据脱敏；不打印输入或底层异常。
输入：stdin、AIG_SERVER 与 Agent 凭据环境。输出：内存配置和安全输出包装。
"""

import json
import os
import re
import sys
from urllib.parse import urlsplit

MAX_RUNTIME_BYTES = 65536
OPAQUE_ID = re.compile(r"[A-Za-z0-9_-]{1,160}\Z")
SAFE_ERROR = "MCP runtime configuration invalid"


class RuntimeConfigError(ValueError):
    pass


def _reject():
    raise RuntimeConfigError(SAFE_ERROR) from None


def _object(pairs):
    result = {}
    for key, value in pairs:
        if key in result or value is None:
            _reject()
        result[key] = value
    return result


def _string(value, limit=8192):
    return isinstance(value, str) and bool(value) and len(value) <= limit and not any(ord(c) < 32 for c in value)


def validate_gateway(url):
    if not _string(url):
        _reject()
    server = os.environ.get("AIG_SERVER", "")
    if not server:
        _reject()
    if "://" not in server:
        server = "http://" + server
    try:
        trusted, parsed = urlsplit(server), urlsplit(url)
        prefix = "/api/internal/mcp-egress/"
        if (trusted.scheme not in {"http", "https"} or not trusted.hostname or trusted.username or trusted.path not in {"", "/"} or trusted.query or trusted.fragment or parsed.scheme != trusted.scheme or parsed.netloc != trusted.netloc or parsed.username or parsed.query or parsed.fragment or "?" in url or "#" in url or not parsed.path.startswith(prefix) or not OPAQUE_ID.fullmatch(parsed.path[len(prefix):])):
            _reject()
    except (ValueError, TypeError):
        _reject()
    return url


def validate_runtime_config(data):
    if not isinstance(data, dict):
        _reject()
    service = {"mcp_proxy_url", "task_capability", "effective_transport"}
    repository = {"archive_ref"}
    fields = set(data) - {"model"}
    if fields not in (service, repository):
        _reject()
    if "model" in data:
        model = data["model"]
        if not isinstance(model, dict) or set(model) != {"model", "token", "base_url"} or not all(_string(v) for v in model.values()):
            _reject()
        try:
            base = urlsplit(model["base_url"])
            if base.scheme not in {"http", "https"} or not base.hostname or base.username:
                _reject()
        except (ValueError, TypeError):
            _reject()
    if fields == service:
        validate_gateway(data["mcp_proxy_url"])
        if data["effective_transport"] not in ("streamable-http", "sse") or not _string(data["task_capability"]):
            _reject()
    else:
        ref = data["archive_ref"]
        if not _string(ref) or not ref.startswith("archive:") or not OPAQUE_ID.fullmatch(ref[8:]):
            _reject()
    return data


def load_runtime_config(stream):
    try:
        raw = stream.read(MAX_RUNTIME_BYTES + 1)
        if isinstance(raw, bytes):
            if len(raw) > MAX_RUNTIME_BYTES:
                _reject()
            raw = raw.decode("utf-8", errors="strict")
        if not isinstance(raw, str) or len(raw.encode("utf-8")) > MAX_RUNTIME_BYTES:
            _reject()
        data = json.loads(raw, object_pairs_hook=_object, parse_constant=lambda _: _reject())
        return validate_runtime_config(data)
    except Exception:
        _reject()


def runtime_redactor(data):
    model = data.get("model", {})
    secrets = [data.get("mcp_proxy_url"), data.get("task_capability"), data.get("archive_ref"), model.get("token"), model.get("base_url"), os.environ.get("AIG_AGENT_TOKEN")]
    variants = set()
    for secret in secrets:
        if secret:
            variants.update((secret, json.dumps(secret, ensure_ascii=True)[1:-1], json.dumps(secret, ensure_ascii=False)[1:-1]))
    ordered = sorted(variants, key=len, reverse=True)
    def redact(value):
        for secret in ordered:
            value = value.replace(secret, "[REDACTED]")
        return value
    return redact


class PrivateOutput:
    """按行缓冲，避免秘密被拆成多次 write 后绕过脱敏。"""
    def __init__(self, stream, redact):
        self.stream, self.redact, self.pending = stream, redact, ""
    def write(self, value):
        self.pending += value
        while "\n" in self.pending:
            line, self.pending = self.pending.split("\n", 1)
            self.stream.write(self.redact(line) + "\n")
        if len(self.pending) > MAX_RUNTIME_BYTES:
            self.pending = "[MCP output withheld]"
        return len(value)
    def flush(self):
        # 不输出未完成行；后续 write 可能补齐秘密，退出时只保留安全占位。
        self.stream.flush()
    def close_private(self):
        if self.pending:
            self.stream.write("[MCP partial output withheld]\n")
            self.pending = ""
        self.stream.flush()
    def isatty(self):
        return False
    @property
    def encoding(self):
        return getattr(self.stream, "encoding", "utf-8")


def install_private_output(data):
    redact = runtime_redactor(data)
    sys.stdout = PrivateOutput(sys.stdout, redact)
    sys.stderr = PrivateOutput(sys.stderr, redact)
    return redact

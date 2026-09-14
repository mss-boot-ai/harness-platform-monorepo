#!/opt/harness/deepseek-venv/bin/python
"""ACP stable-v1 stdio adapter for the official DeepSeek Harness Python SDK."""

from __future__ import annotations

import json
import os
import sys
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Iterator


MAX_LINE_BYTES = 64 * 1024
MAX_CHUNKS = 128
STDOUT_LOCK = threading.Lock()


def _write(value: dict[str, Any]) -> None:
    encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if not encoded or len(encoded) > MAX_LINE_BYTES:
        raise RuntimeError("ACP output exceeded the bounded line size")
    with STDOUT_LOCK:
        sys.stdout.buffer.write(encoded + b"\n")
        sys.stdout.buffer.flush()


def _read() -> dict[str, Any] | None:
    line = sys.stdin.buffer.readline(MAX_LINE_BYTES + 1)
    if not line:
        return None
    if len(line) > MAX_LINE_BYTES or not line.endswith(b"\n"):
        raise RuntimeError("ACP input exceeded the bounded line size")
    value = json.loads(line)
    if not isinstance(value, dict) or value.get("jsonrpc") != "2.0":
        raise RuntimeError("invalid ACP JSON-RPC envelope")
    return value


def _request(value: dict[str, Any], method: str) -> tuple[Any, dict[str, Any]]:
    if value.get("method") != method or "id" not in value or value.get("id") is None:
        raise RuntimeError(f"expected ACP {method} request")
    params = value.get("params")
    if not isinstance(params, dict):
        raise RuntimeError(f"ACP {method} params are required")
    return value["id"], params


def _prompt_text(params: dict[str, Any], session_id: str) -> str:
    if params.get("sessionId") != session_id:
        raise RuntimeError("ACP prompt session binding is invalid")
    blocks = params.get("prompt")
    if not isinstance(blocks, list) or not blocks:
        raise RuntimeError("ACP prompt must contain content")
    text: list[str] = []
    for block in blocks:
        if not isinstance(block, dict) or block.get("type") != "text" or not isinstance(block.get("text"), str):
            raise RuntimeError("the DeepSeek adapter currently accepts text ACP content only")
        if block["text"]:
            text.append(block["text"])
    prompt = "\n".join(text)
    if not prompt:
        raise RuntimeError("ACP prompt text is empty")
    return prompt


def _utf8_chunks(value: str, limit: int = 48 * 1024) -> Iterator[str]:
    current: list[str] = []
    size = 0
    for character in value:
        encoded_size = len(character.encode("utf-8"))
        if current and size + encoded_size > limit:
            yield "".join(current)
            current = []
            size = 0
        current.append(character)
        size += encoded_size
    if current:
        yield "".join(current)


def _positive_environment_int(name: str, default: int, maximum: int) -> int:
    raw = os.environ.get(name, "").strip()
    if not raw:
        return default
    value = int(raw)
    if value <= 0 or value > maximum:
        raise RuntimeError(f"{name} is outside the supported range")
    return value


def _configuration(cwd: Path) -> Any:
    from deepseek_harness import DeepSeekHarnessConfig

    base_url = os.environ.get("MSS_HARNESS_API_BASE_URL", "").strip().rstrip("/")
    api_key = os.environ.get("MSS_HARNESS_API_KEY", "").strip()
    model = os.environ.get("MSS_HARNESS_MODEL", "grok-workbuddy").strip()
    if not base_url.startswith("https://") or not api_key or not model:
        raise RuntimeError("DeepSeek Harness runtime configuration is unavailable")
    session_root = Path(
        os.environ.get(
            "MSS_HARNESS_SESSION_ROOT",
            str(Path.home() / ".local" / "state" / "harness-aba" / "deepseek"),
        )
    ).expanduser()
    session_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    return DeepSeekHarnessConfig(
        provider="deepseek-official",
        model=model,
        max_tokens=_positive_environment_int("MSS_HARNESS_MAX_TOKENS", 2048, 32768),
        cwd=str(cwd),
        session_root=str(session_root.resolve()),
        request_timeout_seconds=float(
            _positive_environment_int("MSS_HARNESS_REQUEST_TIMEOUT_SECONDS", 50, 55)
        ),
        base_url=base_url,
        api_key=api_key,
    )

def _error(request_id: Any, message: str, **data: Any) -> dict[str, Any]:
    return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32001, "message": message, "data": data}}


class TurnStream:
    """Coalesce real SDK deltas into bounded ACP notifications."""
    def __init__(self, session_id: str) -> None:
        self.session_id = session_id
        self.pending = ""
        self.published = ""
        self.count = 0
        self.events = 0
        self.last_flush = time.monotonic()

    def flush(self, force: bool = False) -> None:
        if not self.pending or (not force and len(self.pending) < 512 and time.monotonic() - self.last_flush < 0.25):
            return
        for chunk in _utf8_chunks(self.pending):
            self.count += 1
            if self.count > 1024 or len(self.published.encode("utf-8")) + len(chunk.encode("utf-8")) > 1024 * 1024:
                raise RuntimeError("bounded runtime output exceeded")
            _write({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": self.session_id,
                "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": chunk}}}})
            self.published += chunk
        self.pending = ""
        self.last_flush = time.monotonic()

    def notification(self, notification: Any) -> None:
        self.events += 1
        if self.events > 8192:
            raise RuntimeError("bounded runtime event count exceeded")
        payload = getattr(notification, "payload", None)
        if getattr(notification, "method", None) != "session.event" or not isinstance(payload, dict) or payload.get("sessionId") != self.session_id:
            return
        event = payload.get("event")
        if not isinstance(event, dict) or event.get("type") != "assistant/chunk":
            return
        data = event.get("data")
        chunk = data.get("chunk") if isinstance(data, dict) else None
        if isinstance(chunk, dict) and chunk.get("type") == "text-delta" and isinstance(chunk.get("text"), str):
            self.pending += chunk["text"]
            self.flush()

    def finish(self, final: str) -> None:
        self.flush(True)
        if final and not self.published.endswith(final):
            self.pending = final
            self.flush(True)


def _provider_failure(result: Any, request_id: Any) -> dict[str, Any]:
    data: dict[str, Any] = {"executionState": "failed"}
    for event in reversed(getattr(result, "events", []) or []):
        if not isinstance(event, dict) or event.get("type") != "turn/end":
            continue
        details = event.get("data")
        reason = details.get("reason") if isinstance(details, dict) else None
        error = reason.get("error") if isinstance(reason, dict) else None
        if isinstance(error, dict):
            status = error.get("status")
            if isinstance(status, int) and not isinstance(status, bool) and 400 <= status <= 599:
                data["status"] = status
            delay = error.get("providerRetryAfterMs")
            if isinstance(delay, int) and not isinstance(delay, bool) and delay >= 0:
                data["retryAfterMs"] = min(delay, 300_000)
        break
    return _error(request_id, "PROVIDER_TIMEOUT" if data.get("status") == 504 else "PROVIDER_ERROR", **data)


class RuntimeTurns:
    def __init__(self, harness: Any, session_id: str) -> None:
        self.harness = harness
        self.session_id = session_id
        self.lock = threading.Lock()
        self.busy = False
        self.stopped = False

    def submit(self, request_id: Any, prompt: str) -> None:
        with self.lock:
            if self.stopped:
                _write(_error(request_id, "RUNTIME_UNAVAILABLE", executionState="rejected", runtimeStopped=True))
                return
            if self.busy:
                _write(_error(request_id, "TURN_IN_PROGRESS", executionState="rejected"))
                return
            self.busy = True
        threading.Thread(target=self.execute, args=(request_id, prompt), daemon=True).start()

    def execute(self, request_id: Any, prompt: str) -> None:
        try:
            stream = TurnStream(self.session_id)
            result = self.harness.run(prompt, session_id=self.session_id, on_notification=stream.notification)
            stream.finish(str(getattr(result, "final_response", "")))
            reason = getattr(result, "finish_reason", None)
            if reason == "completed":
                response = {"jsonrpc": "2.0", "id": request_id, "result": {"stopReason": "end_turn"}}
            elif reason in ("aborted", "interrupted"):
                response = {"jsonrpc": "2.0", "id": request_id, "result": {"stopReason": "cancelled"}}
            elif reason in ("error", "blocked", "max-tokens"):
                response = _provider_failure(result, request_id)
            else:
                raise RuntimeError("runtime terminal result is unavailable")
        except Exception:
            self.stopped = True
            try:
                self.harness.close()
            except Exception:
                pass
            response = _error(request_id, "RUNTIME_RESULT_UNKNOWN", executionState="unknown", runtimeStopped=True)
        with self.lock:
            self.busy = False
            try:
                _write(response)
            except (BrokenPipeError, OSError):
                pass


def run() -> int:
    from deepseek_harness import DeepSeekHarness

    initialize = _read()
    if initialize is None:
        return 0
    initialize_id, initialize_params = _request(initialize, "initialize")
    if initialize_params.get("protocolVersion") != 1:
        raise RuntimeError("only ACP stable protocol v1 is supported")
    _write(
        {
            "jsonrpc": "2.0",
            "id": initialize_id,
            "result": {
                "protocolVersion": 1,
                "agentInfo": {"name": "deepseek-harness", "title": "DeepSeek Harness", "version": "0.1.1rc1"},
                "_meta": {"mss": {"turnCancellation": False}},
                "agentCapabilities": {
                    "loadSession": False,
                    "promptCapabilities": {
                        "image": False,
                        "audio": False,
                        "embeddedContext": False,
                    },
                    "mcpCapabilities": {"http": False, "sse": False},
                    "sessionCapabilities": {},
                    "auth": {},
                },
                "authMethods": [],
            },
        }
    )

    create = _read()
    if create is None:
        raise RuntimeError("ACP session/new request is required")
    create_id, create_params = _request(create, "session/new")
    cwd_value = create_params.get("cwd")
    if not isinstance(cwd_value, str):
        raise RuntimeError("ACP session/new cwd is required")
    cwd = Path(cwd_value).resolve(strict=True)
    if cwd != Path.cwd().resolve(strict=True) or not cwd.is_dir():
        raise RuntimeError("ACP workspace does not match the local runtime policy")
    if create_params.get("mcpServers", []) != []:
        raise RuntimeError("remote MCP server injection is not supported")
    with DeepSeekHarness(_configuration(cwd)) as harness:
        session_id = "harness-" + uuid.uuid4().hex
        _write({"jsonrpc": "2.0", "id": create_id, "result": {"sessionId": session_id}})
        turns = RuntimeTurns(harness, session_id)
        while True:
            message = _read()
            if message is None:
                return 0
            if message.get("method") != "session/prompt":
                if "id" in message:
                    _write({"jsonrpc": "2.0", "id": message["id"], "error": {"code": -32601, "message": "UNSUPPORTED_SESSION_METHOD"}})
                continue
            request_id, params = _request(message, "session/prompt")
            try:
                prompt = _prompt_text(params, session_id)
            except (ValueError, RuntimeError):
                _write(_error(request_id, "INVALID_PROMPT", executionState="rejected"))
                continue
            turns.submit(request_id, prompt)


def main() -> int:
    try:
        return run()
    except Exception as error:
        # Never include prompts, model output, credentials, or arbitrary exception text.
        print(f"deepseek-acp-adapter: {type(error).__name__}", file=sys.stderr, flush=True)
        return 70


if __name__ == "__main__":
    raise SystemExit(main())

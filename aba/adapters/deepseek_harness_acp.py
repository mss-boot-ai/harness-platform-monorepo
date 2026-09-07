#!/opt/harness/deepseek-venv/bin/python
"""ACP stable-v1 stdio adapter for the official DeepSeek Harness Python SDK."""

from __future__ import annotations

import json
import os
import sys
import uuid
from pathlib import Path
from typing import Any, Iterator


MAX_LINE_BYTES = 64 * 1024
MAX_CHUNKS = 128


def _write(value: dict[str, Any]) -> None:
    encoded = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if not encoded or len(encoded) > MAX_LINE_BYTES:
        raise RuntimeError("ACP output exceeded the bounded line size")
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
        while True:
            message = _read()
            if message is None:
                return 0
            request_id, params = _request(message, "session/prompt")
            prompt = _prompt_text(params, session_id)
            result = harness.run(prompt, session_id=session_id)
            if getattr(result, "finish_reason", None) != "completed":
                raise RuntimeError("DeepSeek Harness did not complete the dispatched turn")
            final_response = str(getattr(result, "final_response", ""))
            chunks = list(_utf8_chunks(final_response))
            if len(chunks) > MAX_CHUNKS:
                raise RuntimeError("DeepSeek Harness response exceeded the bounded chunk count")
            for chunk in chunks:
                _write(
                    {
                        "jsonrpc": "2.0",
                        "method": "session/update",
                        "params": {
                            "sessionId": session_id,
                            "update": {
                                "sessionUpdate": "agent_message_chunk",
                                "content": {"type": "text", "text": chunk},
                            },
                        },
                    }
                )
            _write(
                {
                    "jsonrpc": "2.0",
                    "id": request_id,
                    "result": {"stopReason": "end_turn"},
                }
            )


def main() -> int:
    try:
        return run()
    except Exception as error:
        # Never include prompts, model output, credentials, or arbitrary exception text.
        print(f"deepseek-acp-adapter: {type(error).__name__}", file=sys.stderr, flush=True)
        return 70


if __name__ == "__main__":
    raise SystemExit(main())

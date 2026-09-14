"""Explicit real-provider acceptance. Writes only into its own temporary workspace.

Run using the pinned Codex venv and its locally authorized model-provider env.
Only bounded counts/statuses are printed; no keys, provider bodies or source text.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import queue
import signal
import subprocess
import sys
import tempfile
import threading
import time


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--adapter", required=True)
    parser.add_argument("--controls", action="store_true")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="codex-acceptance-", dir=os.environ.get("HARNESS_CODEX_TEST_ROOT")) as directory:
        workspace = Path(directory)
        (workspace / "README.md").write_text("Acceptance fixture: PROJECT_FILE_MARKER_2718\n", encoding="utf-8")
        process = subprocess.Popen([sys.executable, args.adapter], cwd=workspace, stdin=subprocess.PIPE,
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, encoding="utf-8", start_new_session=True)
        messages = queue.Queue(maxsize=1024)
        def reader():
            try:
                for line in process.stdout:
                    messages.put_nowait(json.loads(line))
            finally:
                messages.put(None)
        threading.Thread(target=reader, daemon=True).start()
        def send(value):
            process.stdin.write(json.dumps({"jsonrpc": "2.0", **value}) + "\n")
            process.stdin.flush()
        def receive(request_id, *, allow=False, cancel=False):
            deadline = time.monotonic() + 180
            chunks, tools, approvals, text = 0, 0, 0, ""
            cancelled = False
            started = time.monotonic()
            while time.monotonic() < deadline:
                if cancel and not cancelled and time.monotonic() - started > 2:
                    send({"method": "session/cancel", "params": {"sessionId": session_id}})
                    cancelled = True
                try:
                    value = messages.get(timeout=0.2)
                except queue.Empty:
                    continue
                if value is None:
                    raise RuntimeError("adapter transport ended")
                if value.get("method") == "session/request_permission":
                    approvals += 1
                    choices = value["params"]["options"]
                    selected = next((x for x in choices if allow and x["kind"] == "allow_once"), choices[-1])
                    send({"id": value["id"], "result": {"outcome": {"outcome": "selected", "optionId": selected["optionId"]}}})
                elif value.get("method") == "session/update":
                    update = value["params"]["update"]
                    if update["sessionUpdate"] == "agent_message_chunk":
                        chunks += 1
                        text += update["content"]["text"]
                    if update["sessionUpdate"] == "tool_call":
                        tools += 1
                elif value.get("id") == request_id:
                    result = {"request": request_id, "chunks": chunks, "tools": tools, "approvals": approvals,
                        "status": value.get("result", {}).get("stopReason", "ok" if "result" in value else value.get("error", {}).get("message", "invalid"))}
                    print(json.dumps(result), flush=True)
                    if "error" in value:
                        raise RuntimeError("ACP request failed")
                    return value["result"], text, tools, approvals
            raise TimeoutError("acceptance deadline")
        try:
            send({"id": "initialize", "method": "initialize", "params": {"protocolVersion": 1, "clientCapabilities": {}}})
            receive("initialize")
            send({"id": "new", "method": "session/new", "params": {"cwd": directory, "mcpServers": []}})
            created, _, _, _ = receive("new")
            session_id = created["sessionId"]
            def prompt(name, text, **options):
                send({"id": name, "method": "session/prompt", "params": {"sessionId": session_id, "prompt": [{"type": "text", "text": text}]}})
                return receive(name, **options)
            _, text, _, _ = prompt("context-first", "Remember this test code: CONTEXT_MARKER_3141. Reply with just that code; do not use tools.")
            assert "CONTEXT_MARKER_3141" in text
            _, text, _, _ = prompt("context-followup", "What was the exact test code I just gave you? Reply only with that code; do not use tools.")
            assert "CONTEXT_MARKER_3141" in text
            _, text, tools, _ = prompt("project-read", "Read the README.md file in the current workspace with a real file tool and return its acceptance marker. Do not modify files.")
            assert "PROJECT_FILE_MARKER_2718" in text and tools > 0
            if args.controls:
                effort = next(x for x in created["configOptions"] if x["id"] == "effort")
                send({"id": "config", "method": "session/set_config_option", "params": {"sessionId": session_id,
                    "configId": "effort", "value": effort["options"][0]["value"]}})
                receive("config")
                _, _, _, count = prompt("approval-reject", "Use apply_patch to create approval-probe.txt containing EXACT_APPROVAL_MARKER. Request approval for this single edit, no shell fallback. If denied, stop.")
                assert count > 0 and not (workspace / "approval-probe.txt").exists()
                _, _, _, count = prompt("approval-allow", "Try the same apply_patch creation of approval-probe.txt with EXACT_APPROVAL_MARKER once more. Request approval; no shell fallback.", allow=True)
                assert count > 0 and "EXACT_APPROVAL_MARKER" in (workspace / "approval-probe.txt").read_text()
                result, _, _, _ = prompt("cancel", "Run a shell command sleep 30 and then report completion.", cancel=True)
                assert result["stopReason"] == "cancelled"
                _, text, _, _ = prompt("after-cancel", "Reply only with CONTINUED_AFTER_CANCEL, without tools.")
                assert "CONTINUED_AFTER_CANCEL" in text
            print(json.dumps({"verified": "real-provider", "context": True, "fileRead": True, "controls": args.controls}), flush=True)
            return 0
        finally:
            process.stdin.close()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:
        print(json.dumps({"verificationFailure": type(error).__name__}), flush=True)
        raise SystemExit(1)

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
import importlib.util
import threading
from types import SimpleNamespace
from unittest.mock import patch
from pathlib import Path


ADAPTER = Path(__file__).resolve().parents[1] / "deepseek_harness_acp.py"


class DeepSeekHarnessACPAdapterTest(unittest.TestCase):
    def load_adapter(self):
        spec = importlib.util.spec_from_file_location("tested_deepseek_adapter", ADAPTER)
        assert spec is not None and spec.loader is not None
        adapter = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(adapter)
        return adapter

    def test_provider_failure_is_safe_and_the_same_runtime_can_continue(self) -> None:
        adapter = self.load_adapter()
        packets = []
        completed = threading.Event()

        class SDK:
            calls = 0
            def run(self, _prompt, *, session_id, on_notification):
                self.calls += 1
                if self.calls == 1:
                    return SimpleNamespace(finish_reason="error", final_response="", events=[{
                        "type": "turn/end", "data": {"reason": {"kind": "error", "error": {
                            "status": 504, "providerRetryAfterMs": 120000, "message": "PRIVATE_PROVIDER_ERROR_CANARY"}}}}])
                return SimpleNamespace(finish_reason="completed", final_response="continued", events=[])

        def write(packet):
            packets.append(packet)
            if "id" in packet:
                completed.set()

        sdk = SDK()
        with patch.object(adapter, "_write", write):
            turns = adapter.RuntimeTurns(sdk, "session")
            turns.submit("first", "hello")
            self.assertTrue(completed.wait(2))
            self.assertEqual(packets[-1]["error"]["data"]["status"], 504)
            self.assertEqual(packets[-1]["error"]["data"]["executionState"], "failed")
            completed.clear()
            turns.submit("second", "continue")
            self.assertTrue(completed.wait(2))
            self.assertEqual(packets[-1]["result"]["stopReason"], "end_turn")
        self.assertEqual(sdk.calls, 2)
        self.assertNotIn("PRIVATE_PROVIDER_ERROR_CANARY", json.dumps(packets))

    def test_streams_actual_deltas_before_completion_without_duplicate_final_text(self) -> None:
        adapter = self.load_adapter()
        packets = []
        started = threading.Event()
        release = threading.Event()
        completed = threading.Event()
        prefix = "streamed " * 70

        class SDK:
            def run(self, _prompt, *, session_id, on_notification):
                def emit(text):
                    on_notification(SimpleNamespace(method="session.event", payload={"sessionId": session_id, "event": {
                        "type": "assistant/chunk", "data": {"chunk": {"type": "text-delta", "text": text}}}}))
                emit(prefix)
                started.set()
                if not release.wait(2):
                    raise TimeoutError("test release was not signalled")
                emit("final")
                return SimpleNamespace(finish_reason="completed", final_response=prefix + "final", events=[])

        def write(packet):
            packets.append(packet)
            if "id" in packet:
                completed.set()

        with patch.object(adapter, "_write", write):
            turns = adapter.RuntimeTurns(SDK(), "session")
            turns.submit("request", "hello")
            self.assertTrue(started.wait(2))
            self.assertTrue(any(packet.get("method") == "session/update" for packet in packets))
            self.assertFalse(completed.is_set())
            release.set()
            self.assertTrue(completed.wait(2))
        self.assertEqual("".join(packet["params"]["update"]["content"]["text"] for packet in packets if "method" in packet), prefix + "final")

    def test_unknown_runtime_failure_is_not_restarted_or_replayed(self) -> None:
        adapter = self.load_adapter()
        packets = []
        completed = threading.Event()

        class SDK:
            calls = 0
            closed = False
            def run(self, *_args, **_kwargs):
                self.calls += 1
                raise TimeoutError("PRIVATE_TRANSPORT_ERROR_CANARY")
            def close(self):
                self.closed = True

        def write(packet):
            packets.append(packet)
            completed.set()

        sdk = SDK()
        with patch.object(adapter, "_write", write):
            turns = adapter.RuntimeTurns(sdk, "session")
            turns.submit("first", "hello")
            self.assertTrue(completed.wait(2))
            self.assertEqual(packets[-1]["error"]["data"]["executionState"], "unknown")
            turns.submit("second", "do not replay")
            self.assertTrue(packets[-1]["error"]["data"]["runtimeStopped"])
        self.assertEqual(sdk.calls, 1)
        self.assertTrue(sdk.closed)
        self.assertNotIn("PRIVATE_TRANSPORT_ERROR_CANARY", json.dumps(packets))

    def test_bridges_two_acp_prompts_through_one_sdk_instance(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module = root / "deepseek_harness"
            module.mkdir()
            (module / "__init__.py").write_text(
                textwrap.dedent(
                    """
                    from types import SimpleNamespace

                    class DeepSeekHarnessConfig:
                        def __init__(self, **values):
                            self.values = values

                    class DeepSeekHarness:
                        def __init__(self, config):
                            self.config = config
                            self.turn = 0
                        def __enter__(self):
                            return self
                        def __exit__(self, *_args):
                            return None
                        def run(self, prompt, *, session_id=None, on_notification=None):
                            self.turn += 1
                            return SimpleNamespace(
                                finish_reason="completed",
                                final_response=f"turn-{self.turn}:{prompt}",
                                session_id=session_id,
                            )
                    """
                ),
                encoding="utf-8",
            )
            workspace = root / "workspace"
            workspace.mkdir()
            environment = {
                "HOME": str(root / "home"),
                "MSS_HARNESS_API_BASE_URL": "https://gateway.example/v1",
                "MSS_HARNESS_API_KEY": "test-only-key",
                "MSS_HARNESS_MODEL": "test-model",
                "PATH": os.environ.get("PATH", ""),
                "PYTHONPATH": str(root),
            }
            process = subprocess.Popen(
                [sys.executable, str(ADAPTER)],
                cwd=workspace,
                env=environment,
                stdin=subprocess.PIPE,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
            )
            assert process.stdin is not None
            assert process.stdout is not None
            requests = [
                {
                    "jsonrpc": "2.0",
                    "id": "initialize",
                    "method": "initialize",
                    "params": {"protocolVersion": 1, "clientCapabilities": {}},
                },
                {
                    "jsonrpc": "2.0",
                    "id": "new",
                    "method": "session/new",
                    "params": {"cwd": str(workspace), "mcpServers": []},
                },
            ]
            for request in requests:
                process.stdin.write(json.dumps(request) + "\n")
                process.stdin.flush()
                response = json.loads(process.stdout.readline())
                self.assertEqual(response["id"], request["id"])
            session_id = response["result"]["sessionId"]

            for turn, prompt in enumerate(("hello", "again"), start=1):
                process.stdin.write(
                    json.dumps(
                        {
                            "jsonrpc": "2.0",
                            "id": f"prompt-{turn}",
                            "method": "session/prompt",
                            "params": {
                                "sessionId": session_id,
                                "prompt": [{"type": "text", "text": prompt}],
                            },
                        }
                    )
                    + "\n"
                )
                process.stdin.flush()
                update = json.loads(process.stdout.readline())
                completed = json.loads(process.stdout.readline())
                self.assertEqual(update["params"]["sessionId"], session_id)
                self.assertEqual(update["params"]["update"]["content"]["text"], f"turn-{turn}:{prompt}")
                self.assertEqual(completed, {"jsonrpc": "2.0", "id": f"prompt-{turn}", "result": {"stopReason": "end_turn"}})

            process.stdin.close()
            self.assertEqual(process.wait(timeout=5), 0)
            assert process.stderr is not None
            self.assertEqual(process.stderr.read(), "")
            process.stdout.close()
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()

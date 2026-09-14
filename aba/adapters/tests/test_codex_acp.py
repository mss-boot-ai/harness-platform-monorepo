from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import threading
import time
import unittest
from unittest.mock import patch


class CodexAdapterTest(unittest.TestCase):
    def setUp(self):
        spec = importlib.util.spec_from_file_location("tested_codex_adapter", Path(__file__).resolve().parents[1] / "codex_acp.py")
        self.adapter = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.adapter)
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.packets = []
        writer = patch.object(self.adapter, "emit", self.packets.append)
        writer.start()
        self.addCleanup(writer.stop)
        self.rpc = []

        class Server:
            closed = False
            def send(inner, value):
                self.rpc.append(value)
            def request(inner, method, params):
                self.rpc.append({"method": method, "params": params})
                return {}
            def close(inner):
                inner.closed = True

        runtime = self.adapter.CodexACP.__new__(self.adapter.CodexACP)
        self.runtime = runtime
        runtime.server = Server()
        runtime.workspace = Path(self.directory.name)
        runtime.session_id, runtime.thread_id, runtime.turn_id, runtime.request_id = "session", "thread", "turn", "prompt"
        runtime.materialized = True
        runtime.cancel_requested = runtime.interrupt_sent = runtime.stopped = False
        runtime.pending_permissions, runtime.file_changes = {}, {}
        runtime.tool_owners = {}
        runtime.lock = threading.RLock()
        runtime.stream, runtime.stream_bytes = "", 0
        runtime.last_flush = time.monotonic()
        runtime.mode, runtime.model, runtime.effort = "read-only", "test-model", "low"
        runtime.models = {"test-model": {"supportedReasoningEfforts": [{"reasoningEffort": "low"}, {"reasoningEffort": "high"}]}}

    def event(self, method, **params):
        self.runtime.event({"method": method, "params": {"threadId": "thread", "turnId": "turn", **params}})

    def test_only_exact_isolated_relay_can_use_local_http(self):
        env = {"HARNESS_CODEX_API_BASE_URL": "http://127.0.0.1:39121/v1",
            "HARNESS_CODEX_API_KEY": "local-isolated-provider",
            "HARNESS_CODEX_PROVIDER_TRANSPORT": "local-isolated-v1"}
        with patch.dict(self.adapter.os.environ, env, clear=True):
            self.assertEqual(self.adapter.provider_base(), env["HARNESS_CODEX_API_BASE_URL"])
        for url in ["http://127.0.0.1:18082/v1", "http://localhost:39121/v1", "http://127.0.0.1:39121/admin", "http://127.0.0.1:39121/v1?x=1"]:
            with patch.dict(self.adapter.os.environ, {**env, "HARNESS_CODEX_API_BASE_URL": url}, clear=True):
                with self.assertRaises(ValueError):
                    self.adapter.provider_base()
        with patch.dict(self.adapter.os.environ, {**env, "HARNESS_CODEX_PROVIDER_TRANSPORT": ""}, clear=True):
            with self.assertRaises(ValueError):
                self.adapter.provider_base()

    def test_direct_provider_keeps_https_requirement(self):
        with patch.dict(self.adapter.os.environ, {"HARNESS_CODEX_API_BASE_URL": "https://provider.example/v1", "HARNESS_CODEX_API_KEY": "synthetic-test-key"}, clear=True):
            self.assertEqual(self.adapter.provider_base(), "https://provider.example/v1")

    def test_stream_preserves_large_deltas_and_real_terminal(self):
        text = "中文 stream " * 2500
        self.event("item/agentMessage/delta", delta=text)
        self.assertFalse(any("id" in x for x in self.packets))
        self.event("turn/completed", turn={"id": "turn", "status": "completed"})
        combined = "".join(x["params"]["update"]["content"]["text"] for x in self.packets if "method" in x)
        self.assertEqual(combined, text)
        self.assertEqual(self.packets[-1]["result"]["stopReason"], "end_turn")

    def test_late_tool_completion_keeps_original_runtime_owner(self):
        self.event("item/started", item={"id": "tool-a", "type": "commandExecution", "status": "inProgress"})
        self.event("turn/completed", turn={"id": "turn", "status": "interrupted"})
        self.runtime.turn_id, self.runtime.request_id = "turn-b", "prompt-b"
        self.event("item/completed", item={"id": "tool-a", "type": "commandExecution", "status": "completed", "aggregatedOutput": "late output"})
        self.assertEqual(self.packets[-1]["params"]["update"]["sessionUpdate"], "tool_call_update")
        self.assertEqual(self.runtime.tool_owners["tool-a"]["turn"], "turn")
        self.assertEqual(self.runtime.request_id, "prompt-b")
        self.runtime.request_id = None
        self.event("item/completed", item={"id": "tool-a", "type": "commandExecution", "status": "completed"})
        self.assertEqual(self.packets[-1]["params"]["update"]["toolCallId"], "tool-a")

    def test_unowned_late_tool_does_not_become_current_turn_output(self):
        self.runtime.turn_id, self.runtime.request_id = "turn-b", "prompt-b"
        self.event("item/completed", item={"id": "unknown", "type": "commandExecution", "status": "completed"})
        self.assertEqual(self.packets[-1]["params"]["update"]["code"], "UNOWNED_TOOL_EVENT")
        self.assertEqual(self.runtime.tool_owners, {})

    def test_approval_contains_exact_diff_is_one_time_and_scoped(self):
        changes = [{"path": str(self.runtime.workspace / "probe.txt"), "kind": {"type": "add"}, "diff": "+approval test"}]
        self.event("item/started", item={"id": "file", "type": "fileChange", "changes": changes})
        self.runtime.event({"id": 12, "method": "item/fileChange/requestApproval", "params": {"threadId": "thread", "turnId": "turn", "itemId": "file"}})
        approval = self.packets[-1]
        self.assertEqual(approval["params"]["toolCall"]["rawInput"]["changes"], changes)
        self.assertEqual(approval["params"]["options"][0]["kind"], "allow_once")
        reply = {"id": approval["id"], "result": {"outcome": {"outcome": "selected", "optionId": "allow"}}}
        self.runtime.decide(reply)
        self.runtime.decide(reply)
        self.assertEqual(self.rpc, [{"id": 12, "result": {"decision": "accept"}}])

    def test_missing_diff_and_outside_workspace_never_enable_approval(self):
        self.runtime.file_changes["outside"] = [{"path": "/etc/harness/private", "kind": {"type": "add"}, "diff": "+test"}]
        for item_id in ("missing", "outside"):
            self.runtime.event({"id": item_id, "method": "item/fileChange/requestApproval", "params": {"threadId": "thread", "turnId": "turn", "itemId": item_id}})
            self.assertEqual([x["kind"] for x in self.packets[-1]["params"]["options"]], ["reject_once"])

    def test_cancellation_waits_for_terminal_and_deduplicates_interrupt(self):
        for _ in range(20):
            self.runtime.interrupt()
        self.assertTrue(self.runtime.cancel_requested)
        self.assertFalse(any("result" in packet for packet in self.packets))
        self.event("turn/completed", turn={"id": "turn", "status": "interrupted"})
        self.assertEqual(self.packets[-1]["result"]["stopReason"], "cancelled")
        self.assertFalse(self.runtime.stopped)

    def test_known_failure_can_continue_but_transport_failure_cannot(self):
        self.event("turn/completed", turn={"id": "turn", "status": "failed", "error": {"message": "PRIVATE_PROVIDER_CANARY"}})
        self.assertEqual(self.packets[-1]["error"]["data"]["executionState"], "failed")
        self.assertFalse(self.runtime.stopped)
        self.runtime.request_id = "next"
        self.runtime.transport_failed()
        self.assertTrue(self.packets[-1]["error"]["data"]["runtimeStopped"])
        self.assertTrue(self.runtime.server.closed)
        self.assertNotIn("PRIVATE_PROVIDER_CANARY", str(self.packets))

    def test_configuration_timeout_fences_runtime_without_claiming_rollback(self):
        self.runtime.request_id = None
        def fail(*args):
            raise TimeoutError("PRIVATE_CONFIG_CANARY")
        self.runtime.server.request = fail
        self.runtime.configure("config", "effort", "high")
        self.assertTrue(self.runtime.stopped)
        self.assertEqual(self.packets[-1]["error"]["data"]["executionState"], "unknown")

    def test_configuration_unloads_idle_thread_before_acknowledging_effective_value(self):
        self.runtime.request_id = None
        calls = []
        def request(method, params):
            calls.append(method)
            if method == "thread/unsubscribe":
                return {"status": "unsubscribed"}
            return {"thread": {"id": "thread"}, "model": "test-model", "modelProvider": "harness", "cwd": str(self.runtime.workspace),
                "reasoningEffort": "high", "approvalPolicy": "on-request", "sandbox": {"type": "readOnly"}}
        self.runtime.server.request = request
        self.runtime.configure("config", "effort", "high")
        self.assertEqual(calls, ["thread/unsubscribe", "thread/resume"])
        self.assertEqual(self.packets[-1]["result"]["configOptions"][1]["currentValue"], "high")

    def test_unknown_server_requests_fail_closed_without_hanging(self):
        self.runtime.event({"id": 9, "method": "item/tool/requestUserInput", "params": {"threadId": "thread"}})
        self.assertEqual(self.rpc[0]["error"]["code"], -32601)


if __name__ == "__main__":
    unittest.main()

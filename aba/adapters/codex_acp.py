#!/opt/harness/codex-venv/bin/python
"""ACP v1 adapter for the pinned Codex App Server, with non-blocking approvals."""
from __future__ import annotations

import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import threading
import time
import uuid
from typing import Any
from urllib.parse import urlsplit

MAX_ACP_BYTES = 64 * 1024
MAX_RPC_BYTES = 1024 * 1024
OUT_LOCK = threading.Lock()


def emit(value: dict[str, Any]) -> None:
    data = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode()
    if len(data) > MAX_ACP_BYTES:
        raise ValueError("ACP output limit")
    with OUT_LOCK:
        sys.stdout.buffer.write(data + b"\n")
        sys.stdout.buffer.flush()


def failure(request_id: Any, code: str, unknown: bool = False) -> None:
    emit({"jsonrpc": "2.0", "id": request_id, "error": {"code": -32001, "message": code,
        "data": {"executionState": "unknown" if unknown else "failed", "runtimeStopped": unknown}}})


class RpcRejected(Exception):
    pass


class AppServer:
    """The reader never waits for a human decision or writes ACP output."""
    def __init__(self, command: list[str], cwd: Path, env: dict[str, str]) -> None:
        self.process = subprocess.Popen(command, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, text=True, encoding="utf-8", bufsize=1)
        self.lock = threading.Lock()
        self.pending: dict[str, queue.Queue[Any]] = {}
        self.events: queue.Queue[dict[str, Any]] = queue.Queue(maxsize=256)
        self.writer = threading.Lock()
        self.closed = False
        threading.Thread(target=self.read, daemon=True).start()

    def send(self, message: dict[str, Any]) -> None:
        encoded = json.dumps(message, separators=(",", ":"))
        if len(encoded.encode()) > MAX_RPC_BYTES or self.closed:
            raise RuntimeError("App Server transport unavailable")
        with self.writer:
            assert self.process.stdin is not None
            self.process.stdin.write(encoded + "\n")
            self.process.stdin.flush()

    def request(self, method: str, params: dict[str, Any]) -> dict[str, Any]:
        request_id = "harness-" + uuid.uuid4().hex
        waiter: queue.Queue[Any] = queue.Queue(maxsize=1)
        with self.lock:
            self.pending[request_id] = waiter
        try:
            self.send({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params})
            result = waiter.get(timeout=20)
            if isinstance(result, BaseException):
                raise result
            if "error" in result:
                raise RpcRejected("App Server rejected request")
            value = result.get("result")
            if not isinstance(value, dict):
                raise ValueError("App Server response is invalid")
            return value
        finally:
            with self.lock:
                self.pending.pop(request_id, None)

    def read(self) -> None:
        try:
            assert self.process.stdout is not None
            while True:
                line = self.process.stdout.readline(MAX_RPC_BYTES + 1)
                if not line or len(line.encode()) > MAX_RPC_BYTES or not line.endswith("\n"):
                    raise RuntimeError("App Server output ended")
                value = json.loads(line)
                if not isinstance(value, dict):
                    raise ValueError("App Server envelope is invalid")
                if "method" in value:
                    self.events.put_nowait(value)
                else:
                    with self.lock:
                        waiter = self.pending.get(str(value.get("id")))
                    if waiter is not None:
                        waiter.put_nowait(value)
        except Exception:
            with self.lock:
                waiters = list(self.pending.values())
            for waiter in waiters:
                try:
                    waiter.put_nowait(RuntimeError("App Server transport closed"))
                except queue.Full:
                    pass
            self.close()

    def close(self) -> None:
        self.closed = True
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=2)


def within(path: str, root: Path) -> bool:
    try:
        Path(path).resolve().relative_to(root)
        return True
    except (ValueError, OSError):
        return False


class CodexACP:
    def __init__(self, workspace: Path) -> None:
        from codex_cli_bin import bundled_codex_path, bundled_path_dir

        self.workspace = workspace
        base = os.environ.get("MSS_HARNESS_API_BASE_URL", "").strip().rstrip("/")
        parsed = urlsplit(base)
        if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or not os.environ.get("MSS_HARNESS_API_KEY"):
            raise ValueError("Model provider configuration is unavailable")
        self.model = os.environ.get("HARNESS_CODEX_MODEL", "gpt-5.6-luna").strip()
        allowed = os.environ.get("HARNESS_CODEX_MODELS", self.model).split(",")
        self.allowed = {value.strip() for value in allowed if value.strip()}
        if not self.allowed or len(self.allowed) > 16 or self.model not in self.allowed:
            raise ValueError("Local model allow-list is invalid")
        self.mode = "read-only"
        self.effort = "low"
        self.session_id = "codex-" + uuid.uuid4().hex
        self.thread_id = ""
        self.turn_id: str | None = None
        self.request_id: Any = None
        self.cancel_requested = False
        self.stopped = False
        self.pending_permissions: dict[str, dict[str, Any]] = {}
        self.file_changes: dict[str, list[dict[str, Any]]] = {}
        self.interrupt_sent = False
        self.stream = ""
        self.stream_bytes = 0
        self.last_flush = time.monotonic()
        self.lock = threading.RLock()
        overrides = {
            "model_provider": "harness", "model_providers.harness.name": "Harness model gateway",
            "model_providers.harness.base_url": base, "model_providers.harness.env_key": "MSS_HARNESS_API_KEY",
            "model_providers.harness.wire_api": "responses", "shell_environment_policy.ignore_default_excludes": False,
            "web_search": "disabled",
        }
        command = [str(bundled_codex_path())]
        for key, value in overrides.items():
            command.extend(["--config", key + "=" + json.dumps(value)])
        command.extend(["app-server", "--listen", "stdio://"])
        env = dict(os.environ)
        path_dir = bundled_path_dir()
        if path_dir is not None:
            env["PATH"] = str(path_dir) + os.pathsep + env.get("PATH", "")
        self.server = AppServer(command, workspace, env)
        try:
            self.server.request("initialize", {"clientInfo": {"name": "harness_acp", "title": "Harness ACP", "version": "0.1.0"}, "capabilities": {"experimentalApi": True}})
            self.server.send({"method": "initialized", "params": {}})
            listed = self.server.request("model/list", {"limit": 100, "includeHidden": False})
            self.models = {item["model"]: item for item in listed.get("data", []) if isinstance(item, dict) and item.get("model") in self.allowed}
            if self.model not in self.models:
                raise ValueError("Configured model is not announced by the pinned runtime")
            efforts = self.efforts(self.model)
            self.effort = self.models[self.model].get("defaultReasoningEffort")
            if self.effort not in efforts:
                self.effort = efforts[0]
            result = self.server.request("thread/start", self.thread_options())
            thread = result.get("thread")
            if not isinstance(thread, dict) or not isinstance(thread.get("id"), str):
                raise ValueError("Thread creation response is invalid")
            self.thread_id = thread["id"]
            threading.Thread(target=self.events, daemon=True).start()
        except Exception:
            self.server.close()
            raise

    def efforts(self, model: str) -> list[str]:
        values = [item.get("reasoningEffort") for item in self.models[model].get("supportedReasoningEfforts", []) if isinstance(item, dict)]
        values = [value for value in values if isinstance(value, str)]
        if not values:
            raise ValueError("Reasoning capabilities are unavailable")
        return values

    def sandbox(self) -> dict[str, Any]:
        if self.mode == "read-only":
            return {"type": "readOnly", "networkAccess": False}
        return {"type": "workspaceWrite", "writableRoots": [str(self.workspace)], "networkAccess": False,
            "excludeTmpdirEnvVar": True, "excludeSlashTmp": True}

    def thread_options(self) -> dict[str, Any]:
        return {"model": self.model, "modelProvider": "harness", "cwd": str(self.workspace), "sandbox": self.mode,
            "approvalPolicy": "on-request", "approvalsReviewer": "user", "config": {"model_reasoning_effort": self.effort}}

    def options(self) -> list[dict[str, Any]]:
        return [
            {"id": "model", "name": "模型", "category": "model", "type": "select", "currentValue": self.model,
                "options": [{"value": name, "name": value.get("displayName", name)} for name, value in self.models.items()]},
            {"id": "effort", "name": "推理强度", "category": "thought_level", "type": "select", "currentValue": self.effort,
                "options": [{"value": value, "name": value} for value in self.efforts(self.model)]},
            {"id": "access", "name": "项目权限", "category": "mode", "type": "select", "currentValue": self.mode,
                "options": [{"value": "read-only", "name": "只读，写入时审批"}, {"value": "workspace-write", "name": "项目内可写"}]},
        ]

    def update(self, value: dict[str, Any]) -> None:
        emit({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": self.session_id, "update": value}})

    def configure(self, request_id: Any, config_id: str, value: str) -> None:
        with self.lock:
            if self.request_id is not None or self.stopped:
                failure(request_id, "TURN_IN_PROGRESS")
                return
            original = self.model, self.effort, self.mode
            if config_id == "model" and value in self.models:
                self.model = value
                self.effort = self.models[value].get("defaultReasoningEffort", self.efforts(value)[0])
            elif config_id == "effort" and value in self.efforts(self.model):
                self.effort = value
            elif config_id == "access" and value in ("read-only", "workspace-write"):
                self.mode = value
            else:
                failure(request_id, "UNSUPPORTED_CONFIGURATION")
                return
            try:
                result = self.server.request("thread/resume", {"threadId": self.thread_id, **self.thread_options()})
                if (result.get("thread", {}).get("id") != self.thread_id or result.get("model") != self.model
                    or result.get("modelProvider") != "harness" or result.get("cwd") != str(self.workspace)
                    or result.get("reasoningEffort") != self.effort or result.get("approvalPolicy") != "on-request"
                    or result.get("sandbox", {}).get("type") != ("readOnly" if self.mode == "read-only" else "workspaceWrite")):
                    raise ValueError("Configuration acknowledgement is invalid")
                emit({"jsonrpc": "2.0", "id": request_id, "result": {"configOptions": self.options()}})
            except RpcRejected:
                self.model, self.effort, self.mode = original
                failure(request_id, "CONFIGURATION_REJECTED")
            except Exception:
                self.stopped = True
                self.server.close()
                failure(request_id, "CONFIGURATION_UNCONFIRMED", True)

    def prompt(self, request_id: Any, blocks: list[Any]) -> None:
        if not isinstance(request_id, (str, int)) or isinstance(request_id, bool) or not isinstance(blocks, list) or not blocks or len(blocks) > 64 or any(not isinstance(item, dict) or item.get("type") != "text" or not isinstance(item.get("text"), str) for item in blocks):
            failure(request_id, "INVALID_PROMPT")
            return
        with self.lock:
            if self.request_id is not None or self.stopped:
                failure(request_id, "TURN_IN_PROGRESS" if not self.stopped else "RUNTIME_UNAVAILABLE", self.stopped)
                return
            self.request_id = request_id
            self.turn_id = None
            self.cancel_requested = False
            self.interrupt_sent = False
            self.stream = ""
            self.stream_bytes = 0
            self.file_changes.clear()
        threading.Thread(target=self.start_turn, args=(request_id, blocks), daemon=True).start()

    def start_turn(self, request_id: Any, blocks: list[Any]) -> None:
        try:
            result = self.server.request("turn/start", {"threadId": self.thread_id, "input": blocks, "cwd": str(self.workspace),
                "model": self.model, "effort": self.effort, "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandboxPolicy": self.sandbox()})
            turn = result.get("turn", {})
            with self.lock:
                if self.request_id != request_id:
                    return
                if not isinstance(turn.get("id"), str) or self.turn_id not in (None, turn["id"]):
                    raise ValueError("Turn binding is invalid")
                self.turn_id = turn["id"]
                cancelled = self.cancel_requested
            if cancelled:
                self.interrupt()
        except RpcRejected:
            with self.lock:
                if self.request_id != request_id:
                    return
                unknown = self.turn_id is not None
                self.stopped = unknown
                self.request_id = None
                failure(request_id, "TURN_START_REJECTED", unknown)
                if unknown:
                    self.server.close()
        except Exception:
            with self.lock:
                if self.request_id != request_id:
                    return
                self.stopped = True
                self.request_id = None
                self.server.close()
                failure(request_id, "RUNTIME_RESULT_UNKNOWN", True)

    def interrupt(self) -> None:
        with self.lock:
            if self.request_id is None or self.stopped:
                return
            self.cancel_requested = True
            turn_id = self.turn_id
            send = turn_id is not None and not self.interrupt_sent
            self.interrupt_sent = self.interrupt_sent or send
        if send:
            def request() -> None:
                try:
                    self.server.request("turn/interrupt", {"threadId": self.thread_id, "turnId": turn_id})
                except Exception:
                    pass  # Only turn/completed confirms cancellation to HC.
            threading.Thread(target=request, daemon=True).start()

    def approval(self, message: dict[str, Any]) -> None:
        params = message.get("params", {})
        method = message["method"]
        with self.lock:
            if params.get("threadId") != self.thread_id or params.get("turnId") != self.turn_id or self.request_id is None or len(self.pending_permissions) >= 16:
                self.server.send({"id": message.get("id"), "result": {"decision": "cancel"}})
                return
            grant = params.get("grantRoot") or str(self.workspace)
            changes = self.file_changes.get(str(params.get("itemId")), [])
            safe_write = (method == "item/fileChange/requestApproval" and isinstance(grant, str) and within(grant, self.workspace)
                and bool(changes) and all(isinstance(change.get("path"), str) and within(change["path"], self.workspace)
                    and (not change.get("kind", {}).get("move_path") or within(change["kind"]["move_path"], self.workspace)) for change in changes))
            permission_id = "permission-" + uuid.uuid4().hex
            self.pending_permissions[permission_id] = {"requestId": message["id"], "turnId": self.turn_id, "allow": safe_write, "changes": changes}
            options = [{"optionId": "reject", "name": "拒绝此次", "kind": "reject_once"}]
            if safe_write:
                options.insert(0, {"optionId": "allow", "name": "允许此次项目修改", "kind": "allow_once"})
            detail = {key: params[key] for key in ("command", "cwd", "reason", "grantRoot", "itemId") if key in params}
            if changes:
                detail["changes"] = changes
            if len(json.dumps(detail).encode()) > 24 * 1024:
                detail = {"reason": "Request details exceeded the display limit; approval is disabled."}
                options = [options[-1]]
                self.pending_permissions[permission_id]["allow"] = False
            emit({"jsonrpc": "2.0", "id": permission_id, "method": "session/request_permission", "params": {
                "sessionId": self.session_id, "toolCall": {"toolCallId": str(params.get("itemId", permission_id)), "title": "Codex 请求修改项目" if safe_write else "Codex 请求扩展执行权限",
                    "kind": "edit" if safe_write else "execute", "status": "pending", "rawInput": detail}, "options": options}})

    def decide(self, message: dict[str, Any]) -> None:
        with self.lock:
            pending = self.pending_permissions.pop(str(message.get("id")), None)
            if pending is None or pending["turnId"] != self.turn_id:
                return
            outcome = message.get("result", {}).get("outcome", {})
            allow = pending["allow"] and outcome.get("outcome") == "selected" and outcome.get("optionId") == "allow"
            # Recheck local path resolution immediately before granting an exact edit.
            allow = allow and all(within(change["path"], self.workspace)
                and (not change.get("kind", {}).get("move_path") or within(change["kind"]["move_path"], self.workspace)) for change in pending["changes"])
            self.server.send({"id": pending["requestId"], "result": {"decision": "accept" if allow else "decline"}})

    def flush(self) -> None:
        if self.stream:
            self.update({"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": self.stream}})
            self.stream = ""
            self.last_flush = time.monotonic()

    def transport_failed(self) -> None:
        with self.lock:
            self.stopped = True
            request_id = self.request_id
            self.request_id = None
            self.pending_permissions.clear()
            self.server.close()
            if request_id is not None:
                failure(request_id, "RUNTIME_RESULT_UNKNOWN", True)

    def events(self) -> None:
        while True:
            if self.server.closed:
                self.transport_failed()
                return
            try:
                message = self.server.events.get(timeout=0.25)
            except queue.Empty:
                with self.lock:
                    self.flush()
                continue
            try:
                self.event(message)
            except Exception:
                self.transport_failed()
                return

    def event(self, message: dict[str, Any]) -> None:
        method = message.get("method")
        if method == "_transport/closed":
            raise RuntimeError("Runtime transport closed")
        params = message.get("params", {})
        if "id" in message and method not in ("item/fileChange/requestApproval", "item/commandExecution/requestApproval"):
            self.server.send({"id": message["id"], "error": {"code": -32601, "message": "UNSUPPORTED_RUNTIME_REQUEST"}})
            return
        if not isinstance(params, dict) or params.get("threadId") != self.thread_id:
            if "id" in message:
                self.server.send({"id": message["id"], "result": {"decision": "cancel"}})
            return
        with self.lock:
            if method == "turn/started" and self.request_id is not None:
                turn_id = params.get("turn", {}).get("id")
                if isinstance(turn_id, str) and self.turn_id in (None, turn_id):
                    self.turn_id = turn_id
                return
            if "id" in message and isinstance(method, str) and method.endswith("/requestApproval"):
                self.approval(message)
                return
            if method == "serverRequest/resolved":
                for key, pending in list(self.pending_permissions.items()):
                    if pending["requestId"] == params.get("requestId"):
                        self.pending_permissions.pop(key, None)
                return
            if self.request_id is None:
                return
            event_turn = params.get("turnId") or params.get("turn", {}).get("id")
            if event_turn != self.turn_id:
                return
            if method == "item/agentMessage/delta" and isinstance(params.get("delta"), str):
                delta = params["delta"]
                self.stream_bytes += len(delta.encode())
                if self.stream_bytes > 1024 * 1024:
                    raise ValueError("Turn output limit")
                for offset in range(0, len(delta), 4000):
                    self.stream += delta[offset:offset + 4000]
                    if len(self.stream) >= 512 or time.monotonic() - self.last_flush >= 0.25:
                        self.flush()
            elif method in ("item/started", "item/completed"):
                item = params.get("item", {})
                kind = item.get("type")
                if kind in ("commandExecution", "fileChange", "mcpToolCall", "webSearch"):
                    self.flush()
                    if kind == "fileChange":
                        changes = item.get("changes", [])
                        if isinstance(changes, list) and len(changes) <= 64 and len(json.dumps(changes).encode()) <= 20 * 1024 and len(self.file_changes) < 64:
                            self.file_changes[str(item.get("id"))] = changes
                    self.update({"sessionUpdate": "tool_call" if method == "item/started" else "tool_call_update", "toolCallId": str(item.get("id", "")),
                        "title": str(item.get("command") or item.get("tool") or kind)[:512], "kind": "edit" if kind == "fileChange" else "execute",
                        "status": {"inProgress": "in_progress", "completed": "completed", "failed": "failed", "declined": "failed"}.get(item.get("status"), "pending"),
                        "rawInput": {"command": str(item.get("command", ""))[:4096]},
                        "content": [{"type": "content", "content": {"type": "text", "text": str(item.get("aggregatedOutput", ""))[-8192:]}}]})
            elif method == "turn/plan/updated":
                self.update({"sessionUpdate": "plan", "entries": [{"content": str(item.get("step", ""))[:2048], "status": str(item.get("status", "pending")), "priority": "medium"} for item in params.get("plan", [])[:64]]})
            elif method == "thread/tokenUsage/updated":
                usage = params.get("tokenUsage", {})
                used = usage.get("last", {}).get("totalTokens")
                size = usage.get("modelContextWindow")
                if isinstance(used, int) and isinstance(size, int) and size > 0:
                    self.update({"sessionUpdate": "usage_update", "used": used, "size": size})
            elif method == "turn/completed":
                self.flush()
                turn = params.get("turn", {})
                request_id = self.request_id
                self.request_id = None
                self.pending_permissions.clear()
                if turn.get("status") == "completed":
                    emit({"jsonrpc": "2.0", "id": request_id, "result": {"stopReason": "end_turn"}})
                elif turn.get("status") == "interrupted":
                    emit({"jsonrpc": "2.0", "id": request_id, "result": {"stopReason": "cancelled"}})
                else:
                    failure(request_id, "RUNTIME_TURN_FAILED")


def read() -> dict[str, Any] | None:
    line = sys.stdin.buffer.readline(MAX_ACP_BYTES + 1)
    if not line:
        return None
    if len(line) > MAX_ACP_BYTES or not line.endswith(b"\n"):
        raise ValueError("ACP input limit")
    value = json.loads(line)
    if not isinstance(value, dict) or value.get("jsonrpc") != "2.0":
        raise ValueError("ACP envelope is invalid")
    return value


def main() -> int:
    runtime = None
    try:
        first = read()
        if first is None or first.get("method") != "initialize" or first.get("params", {}).get("protocolVersion") != 1:
            raise ValueError("ACP initialization required")
        emit({"jsonrpc": "2.0", "id": first["id"], "result": {"protocolVersion": 1,
            "agentInfo": {"name": "codex", "title": "Codex", "version": "0.147.0"}, "_meta": {"mss": {"turnCancellation": True}},
            "agentCapabilities": {"loadSession": False, "promptCapabilities": {}, "mcpCapabilities": {}}, "authMethods": []}})
        create = read()
        if create is None or create.get("method") != "session/new":
            raise ValueError("ACP session creation required")
        params = create.get("params", {})
        workspace = Path(params.get("cwd", "")).resolve(strict=True)
        if workspace != Path.cwd().resolve() or params.get("mcpServers", []) != []:
            raise ValueError("Local workspace binding is invalid")
        runtime = CodexACP(workspace)
        emit({"jsonrpc": "2.0", "id": create["id"], "result": {"sessionId": runtime.session_id, "configOptions": runtime.options()}})
        while True:
            message = read()
            if message is None:
                return 0
            method = message.get("method")
            if method is None:
                runtime.decide(message)
                continue
            params = message.get("params", {})
            if params.get("sessionId") != runtime.session_id:
                raise ValueError("ACP session binding changed")
            if method == "session/prompt":
                runtime.prompt(message["id"], params.get("prompt", []))
            elif method == "session/cancel":
                runtime.interrupt()
            elif method == "session/set_config_option":
                runtime.configure(message["id"], params.get("configId", ""), params.get("value", ""))
            elif "id" in message:
                emit({"jsonrpc": "2.0", "id": message["id"], "error": {"code": -32601, "message": "UNSUPPORTED_SESSION_METHOD"}})
    except Exception as error:
        print("codex-acp-adapter: " + type(error).__name__, file=sys.stderr, flush=True)
        return 70
    finally:
        if runtime is not None:
            runtime.server.close()


if __name__ == "__main__":
    raise SystemExit(main())

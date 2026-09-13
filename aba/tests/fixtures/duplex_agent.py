#!/usr/bin/env python3
"""Deterministic ACP transport fixture. Never calls a model or executes tools."""
import json
import sys

SESSION = "fixture-session"
OPTIONS = [{"id": "model", "name": "Model", "category": "model", "type": "select",
            "currentValue": "small", "options": [{"value": "small", "name": "Small"},
                                                   {"value": "large", "name": "Large"}]}]
pending = None


def emit(value):
    print(json.dumps(value, separators=(",", ":")), flush=True)


def reply(message, result):
    emit({"jsonrpc": "2.0", "id": message["id"], "result": result})


def finish(reason="end_turn"):
    global pending
    if pending is not None:
        reply(pending, {"stopReason": reason})
        pending = None


for line in sys.stdin:
    value = json.loads(line)
    method = value.get("method")
    if method == "initialize":
        reply(value, {"protocolVersion": 1, "agentCapabilities": {}, "authMethods": []})
    elif method == "session/new":
        reply(value, {"sessionId": SESSION, "configOptions": OPTIONS})
    elif method == "session/set_config_option":
        OPTIONS[0]["currentValue"] = value["params"]["value"]
        reply(value, {"configOptions": OPTIONS})
    elif method == "session/prompt":
        pending = value
        text = value["params"]["prompt"][0]["text"]
        emit({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": SESSION,
              "update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "early chunk"}}}})
        if text == "permission":
            emit({"jsonrpc": "2.0", "id": 77, "method": "session/request_permission",
                  "params": {"sessionId": SESSION, "toolCall": {"toolCallId": "tool-1", "title": "Fixture only", "kind": "read"},
                             "options": [{"optionId": "allow", "name": "Allow once", "kind": "allow_once"},
                                         {"optionId": "deny", "name": "Deny", "kind": "reject_once"}]}})
        elif text == "crash":
            sys.exit(70)
        elif text != "wait":
            finish()
    elif method == "session/cancel":
        finish("cancelled")
    elif method is None and value.get("id") == 77:
        outcome = value["result"]["outcome"]
        finish("cancelled" if outcome["outcome"] == "cancelled" or outcome.get("optionId") == "deny" else "end_turn")
    else:
        emit({"jsonrpc": "2.0", "id": value.get("id"), "error": {"code": -32601, "message": "unexpected fixture request"}})

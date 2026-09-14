#!/usr/bin/python3
"""Deterministic same-UID isolation fixture. No model, account, or real secret use."""
import json
import os
from pathlib import Path
import signal
import socket
import sys
import time


def inaccessible(path):
    try:
        descriptor = os.open(path, os.O_RDONLY)
        os.close(descriptor)
        return False
    except OSError:
        return True


def cannot_connect(family, address):
    try:
        with socket.socket(family, socket.SOCK_STREAM) as connection:
            connection.settimeout(0.3)
            connection.connect(address)
        return False
    except OSError:
        return True


def probe():
    canary, port, abstract, host_pid_ns = sys.argv[1:]
    facts = {
        "same_existing_uid": os.getuid() != 0,
        "host_state_hidden": inaccessible(canary),
        "host_state_via_proc_hidden": inaccessible("/proc/self/root" + canary),
        "endpoint_key_path_hidden": inaccessible("/var/lib/harness-aba/identity.json"),
        "no_system_bus": inaccessible("/run/dbus/system_bus_socket"),
        "no_cgroup_control": inaccessible("/sys/fs/cgroup/cgroup.procs"),
        "private_pid_namespace": os.readlink("/proc/self/ns/pid") != host_pid_ns,
        "host_loopback_denied": cannot_connect(socket.AF_INET, ("127.0.0.1", int(port))),
        "host_private_network_denied": cannot_connect(socket.AF_INET, ("172.16.0.42", int(port))),
        "host_abstract_socket_denied": cannot_connect(socket.AF_UNIX, "\0" + abstract),
        "isolated_home": os.environ.get("HOME") == "/home/runtime",
    }
    inherited = []
    for descriptor in Path("/proc/self/fd").iterdir():
        try:
            inherited.append(os.readlink(descriptor))
        except OSError:
            pass
    facts["no_host_state_descriptors"] = not any("/var/lib/harness" in value for value in inherited)
    Path("isolation-result.json").write_text(json.dumps(facts, sort_keys=True))
    if not all(facts.values()):
        raise RuntimeError("isolation fixture failed")
    # A background tool that changed process group and ignores TERM must not retain
    # workspace ownership after the parent ABA has certified whole-scope cleanup.
    child = os.fork()
    if child == 0:
        os.setsid()
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        for fd in [0, 1, 2]:
            os.close(fd)
        tick = 0
        while True:
            Path("isolation-heartbeat").write_text(str(tick))
            tick += 1
            time.sleep(0.05)
    deadline = time.monotonic() + 2
    while not Path("isolation-heartbeat").exists():
        if time.monotonic() >= deadline:
            raise RuntimeError("fixture child did not start")
        time.sleep(0.01)


for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method")
    if method == "initialize":
        result = {"protocolVersion": 1}
    elif method == "session/new":
        probe()
        result = {"sessionId": "isolated-fixture-session"}
    else:
        result = {}
    print(json.dumps({"jsonrpc": "2.0", "id": request.get("id"), "result": result}), flush=True)

#!/usr/bin/python3
"""Opt-in dev-242 isolated unit check; reuses existing identities, never changes live ABA.

Run as the already-authorized root deployment identity with --aba <candidate binary>
--source-sha <its full SHA>. Files and the retired test registry are retained as evidence.
This does NOT certify provider egress, arbitrary workspace sockets, or durable Host history.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import pwd
import re
import shutil
import socket
import subprocess
import time

parser = argparse.ArgumentParser()
parser.add_argument("--aba", required=True, type=Path)
parser.add_argument("--source-sha", required=True)
args = parser.parse_args()
if os.geteuid() != 0 or not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
    raise SystemExit("requires authorized deployment identity and full source SHA")
user = pwd.getpwnam("harness-aba")  # Lookup only: never creates an account.
accounts = [(entry.pw_name, entry.pw_uid, entry.pw_gid) for entry in pwd.getpwall()]
suffix = args.source_sha[:12]
unit = "harness-isolation-probe-" + suffix
base = Path("/opt/harness/isolation-probes") / suffix
state = Path("/var/lib") / unit
workspace = Path("/srv/harness-workspaces") / unit
for path in [base, state, workspace]:
    if path.exists():
        raise SystemExit("test target already exists; preserve it and use a new checkpoint")
base.mkdir(parents=True, mode=0o755)
for path in [state, workspace]:
    path.mkdir(mode=0o700)
    os.chown(path, user.pw_uid, user.pw_gid)
binary = base / "aba"
shutil.copyfile(args.aba, binary)
binary.chmod(0o755)
fixture = base / "isolation_agent.py"
shutil.copyfile(Path(__file__).resolve().parents[2] / "aba/tests/fixtures/isolation_agent.py", fixture)
fixture.chmod(0o644)
canary = state / "synthetic-host-secret"
canary.write_text("SYNTHETIC_ISOLATION_SENTINEL")
canary.chmod(0o600)
os.chown(canary, user.pw_uid, user.pw_gid)
subprocess.run(["runuser", "-u", user.pw_name, "--", str(binary), "scope-init", "--directory", str(state)], check=True)

tcp = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
tcp.bind(("0.0.0.0", 0))
tcp.listen(2)
abstract = "harness-isolation-" + suffix
unix = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
unix.bind("\0" + abstract)
unix.listen(2)
config = base / "probe.toml"
config.write_text(f'''schema_version = 1
[platform]
url = "http://127.0.0.1:18082"
[isolation]
state_directory = {json.dumps(str(state))}
cgroup_root = "/sys/fs/cgroup/system.slice/{unit}.service"
runtime_roots = [{json.dumps(str(base))}]
network = "none"
[[runtime]]
id = "fixture"
display_name = "Isolated fixture"
command = {json.dumps(str(Path('/usr/bin/python3').resolve()))}
args = {json.dumps([str(fixture), str(canary), str(tcp.getsockname()[1]), abstract, os.readlink('/proc/self/ns/pid')])}
env_allow = ["HOME", "PATH"]
max_sessions = 1
[[workspace]]
id = "fixture"
display_name = "Isolated scratch workspace"
path = {json.dumps(str(workspace))}
allowed_runtimes = ["fixture"]
''')
config.chmod(0o644)
properties = ["Delegate=yes", "ProtectControlGroups=no", "NoNewPrivileges=yes",
    "ProtectSystem=strict", "ProtectHome=yes", "PrivateTmp=yes", "PrivateDevices=yes",
    "CapabilityBoundingSet=", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
    "KillMode=control-group", "MemoryMax=4G", "TasksMax=256", "LimitCORE=0", "UMask=0077",
    f"ReadWritePaths={state} {workspace} /sys/fs/cgroup/system.slice/{unit}.service"]
command = ["systemd-run", "--quiet", "--wait", "--pipe", "--unit=" + unit,
    "--service-type=exec", "--uid=" + user.pw_name, "--gid=" + user.pw_name]
command += ["--property=" + value for value in properties]
command += [str(binary), "runtime", "probe", "--config", str(config), "--runtime", "fixture",
    "--workspace", "fixture", "--insecure-loopback-development"]
try:
    result = subprocess.run(command, capture_output=True, text=True, timeout=45)
    (base / "unit-output.txt").write_text(result.stdout + result.stderr)
    if result.returncode != 0:
        raise RuntimeError("isolated unit probe failed; retained unit-output.txt")
    facts = json.loads((workspace / "isolation-result.json").read_text())
    registry = json.loads((state / "registry.json").read_text())
    facts["whole_scope_cleanup_recorded"] = len(registry["records"]) == 1 and all(
        item["closed"] for item in registry["records"].values())
    before = (workspace / "isolation-heartbeat").read_text()
    time.sleep(0.3)
    facts["background_tool_stopped"] = (workspace / "isolation-heartbeat").read_text() == before
    facts["accounts_unchanged"] = accounts == [(entry.pw_name, entry.pw_uid, entry.pw_gid) for entry in pwd.getpwall()]
    facts["active_aba_untouched"] = subprocess.run(["systemctl", "is-active", "--quiet", "harness-aba.service"]).returncode == 0
    facts["probe_succeeded"] = "ACP runtime probe succeeded." in result.stdout
    report = {"source_sha": args.source_sha, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "unit": unit, "facts": facts, "scope": "deterministic kernel containment only; provider egress and complete Host remain open"}
    (base / "verification.json").write_text(json.dumps(report, indent=2))
    print(json.dumps(report, indent=2))
    if not all(facts.values()):
        raise RuntimeError("containment assertion failed")
finally:
    tcp.close()
    unix.close()

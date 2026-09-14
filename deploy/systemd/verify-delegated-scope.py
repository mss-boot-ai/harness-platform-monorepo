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
parser.add_argument("--provider", action="store_true", help="Also establish the fixed provider path while running hostile fixtures")
parser.add_argument("--codex", action="store_true", help="Exercise the existing real Codex adapter and provider")
args = parser.parse_args()
if os.geteuid() != 0 or not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
    raise SystemExit("requires authorized deployment identity and full source SHA")
user = pwd.getpwnam("harness-aba")  # Lookup only: never creates an account.
accounts = [(entry.pw_name, entry.pw_uid, entry.pw_gid) for entry in pwd.getpwall()]
live_identity = subprocess.check_output(["systemctl", "show", "harness-aba.service", "-p", "MainPID", "-p", "InvocationID"], text=True)
suffix = args.source_sha[:12]
suffix += "-codex" if args.codex else "-egress" if args.provider else ""
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
pathname = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
pathname.bind(str(workspace / "host-control-sentinel.sock"))
os.chown(workspace / "host-control-sentinel.sock", user.pw_uid, user.pw_gid)
os.chmod(workspace / "host-control-sentinel.sock", 0o777)
pathname.listen(2)
datagram = socket.socket(socket.AF_UNIX, socket.SOCK_DGRAM)
datagram.bind(str(workspace / "host-datagram-sentinel.sock"))
os.chown(workspace / "host-datagram-sentinel.sock", user.pw_uid, user.pw_gid)
os.chmod(workspace / "host-datagram-sentinel.sock", 0o777)
for address in [("127.0.0.1", tcp.getsockname()[1]), ("172.16.0.42", tcp.getsockname()[1])]:
    with socket.create_connection(address, timeout=2) as connection:
        accepted, _ = tcp.accept()
        accepted.close()
for server, address in [(unix, "\0" + abstract), (pathname, str(workspace / "host-control-sentinel.sock"))]:
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.connect(address)
        accepted, _ = server.accept()
        accepted.close()
config = base / "probe.toml"
runtime_roots = [str(base)]
runtime_command = str(Path('/usr/bin/python3').resolve())
runtime_args = [str(fixture), str(canary), str(tcp.getsockname()[1]), abstract, os.readlink('/proc/self/ns/pid'), str(user.pw_uid)]
allowed_env = ["HOME", "PATH"]
if args.codex:
    runtime_roots += ["/opt/harness/codex-venv"]
    candidate_adapter = base / "codex-acp"
    shutil.copyfile(Path(__file__).resolve().parents[2] / "aba/adapters/codex_acp.py", candidate_adapter)
    candidate_adapter.chmod(0o755)
    runtime_command = str(candidate_adapter)
    runtime_args = []
    (workspace / "scope-model-probe.txt").write_text("HARNESS_SCOPE_FILE_OK")
if args.codex or args.provider:
    allowed_env += ["HARNESS_CODEX_API_BASE_URL", "HARNESS_CODEX_API_KEY", "HARNESS_CODEX_MODEL", "HARNESS_CODEX_MODELS"]
config.write_text(f'''schema_version = 1
[platform]
url = "http://127.0.0.1:18082"
[isolation]
state_directory = {json.dumps(str(state))}
cgroup_root = "/sys/fs/cgroup/system.slice/{unit}.service"
runtime_roots = {json.dumps(runtime_roots)}
network = {json.dumps("codex_provider" if args.codex or args.provider else "none")}
[[runtime]]
id = "fixture"
display_name = "Isolated fixture"
command = {json.dumps(runtime_command)}
args = {json.dumps(runtime_args)}
env_allow = {json.dumps(allowed_env)}
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
if args.codex or args.provider:
    properties += ["EnvironmentFile=/etc/harness-aba/codex.env"]
command = ["systemd-run", "--quiet", "--wait", "--pipe", "--unit=" + unit,
    "--service-type=exec", "--uid=" + user.pw_name, "--gid=" + user.pw_name]
command += ["--property=" + value for value in properties]
command += [str(binary), "runtime", "probe", "--config", str(config), "--runtime", "fixture",
    "--workspace", "fixture", "--insecure-loopback-development"]
if args.codex:
    command += ["--exercise"]
try:
    result = subprocess.run(command, capture_output=True, text=True, timeout=240 if args.codex else 45)
    (base / "unit-output.txt").write_text(result.stdout + result.stderr)
    if result.returncode != 0:
        raise RuntimeError("isolated unit probe failed; retained unit-output.txt")
    facts = {"real_provider_and_file_tool": "Isolated provider reply and read-only workspace tool confirmed." in result.stdout} if args.codex else json.loads((workspace / "isolation-result.json").read_text())
    expected = {"real_provider_and_file_tool"} if args.codex else {
        "same_existing_uid", "host_state_hidden", "host_state_via_proc_hidden", "endpoint_key_path_hidden",
        "no_system_bus", "no_cgroup_control", "private_pid_namespace", "host_loopback_denied",
        "host_private_network_denied", "host_abstract_socket_denied", "workspace_control_socket_denied",
        "provider_socket_hidden_from_runtime", "upstream_credential_not_in_runtime_env",
        "isolated_home", "datagram_pair_cannot_reach_host", "no_host_state_descriptors"}
    if set(facts) != expected or any(type(value) is not bool for value in facts.values()):
        raise RuntimeError("fixture omitted or changed a required assertion")
    registry = json.loads((state / "registry.json").read_text())
    facts["whole_scope_cleanup_recorded"] = len(registry["records"]) == 1 and all(
        item["closed"] for item in registry["records"].values())
    if not args.codex:
        before = (workspace / "isolation-heartbeat").read_text()
        time.sleep(0.3)
        facts["background_tool_stopped"] = (workspace / "isolation-heartbeat").read_text() == before
    facts["accounts_unchanged"] = accounts == [(entry.pw_name, entry.pw_uid, entry.pw_gid) for entry in pwd.getpwall()]
    facts["active_aba_untouched"] = (subprocess.run(["systemctl", "is-active", "--quiet", "harness-aba.service"]).returncode == 0
        and live_identity == subprocess.check_output(["systemctl", "show", "harness-aba.service", "-p", "MainPID", "-p", "InvocationID"], text=True))
    facts["probe_succeeded"] = "ACP runtime probe succeeded." in result.stdout
    report = {"source_sha": args.source_sha, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "unit": unit, "facts": facts, "scope": "isolated real Codex reply and read-only tool" if args.codex else "deterministic kernel containment; fixed provider path enabled" if args.provider else "deterministic kernel containment, no network",
        "completion": "partial H1 evidence; approval/cancellation/restart matrix and complete Host remain open"}
    (base / "verification.json").write_text(json.dumps(report, indent=2))
    print(json.dumps(report, indent=2))
    if not all(facts.values()):
        raise RuntimeError("containment assertion failed")
finally:
    tcp.close()
    unix.close()
    pathname.close()
    datagram.close()

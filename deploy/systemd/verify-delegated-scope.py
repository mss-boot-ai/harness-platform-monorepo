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
import selectors
import subprocess
import time

parser = argparse.ArgumentParser()
parser.add_argument("--aba", required=True, type=Path)
parser.add_argument("--source-sha", required=True)
parser.add_argument("--provider", action="store_true", help="Also establish the fixed provider path while running hostile fixtures")
parser.add_argument("--codex", action="store_true", help="Exercise the existing real Codex adapter and provider")
parser.add_argument("--crash", action="store_true", help="Kill and restart only this synthetic Host unit, then reconcile recorded scopes")
parser.add_argument("--parallel", action="store_true", help="Verify two independent runtime/provider scopes")
parser.add_argument("--retirement", action="store_true", help="Recover the post-close-commit/pre-removal cut in a new process")
args = parser.parse_args()
if os.geteuid() != 0 or not re.fullmatch(r"[0-9a-f]{40}", args.source_sha):
    raise SystemExit("requires authorized deployment identity and full source SHA")
if args.crash and args.codex:
    raise SystemExit("crash injection uses the deterministic fixture, never an unbounded live model task")
if args.crash and args.parallel:
    raise SystemExit("crash and peer-survival probes are separate cases")
if args.retirement and (args.crash or args.parallel or args.codex):
    raise SystemExit("retirement is a separate non-executing case")
user = pwd.getpwnam("harness-aba")  # Lookup only: never creates an account.
accounts = [(entry.pw_name, entry.pw_uid, entry.pw_gid) for entry in pwd.getpwall()]
live_identity = subprocess.check_output(["systemctl", "show", "harness-aba.service", "-p", "MainPID", "-p", "InvocationID"], text=True)
suffix = args.source_sha[:12]
suffix += "-codex" if args.codex else "-egress" if args.provider else ""
suffix += "-crash" if args.crash else ""
suffix += "-p" if args.parallel else ""
suffix += "-r" if args.retirement else ""
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
peer_workspace = Path(str(workspace) + "-peer")
if args.parallel:
    peer_workspace.mkdir(mode=0o700)
    os.chown(peer_workspace, user.pw_uid, user.pw_gid)
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
seqpacket = socket.socket(socket.AF_UNIX, socket.SOCK_SEQPACKET)
seqpacket.bind(str(workspace / "host-seqpacket-sentinel.sock"))
os.chown(workspace / "host-seqpacket-sentinel.sock", user.pw_uid, user.pw_gid)
os.chmod(workspace / "host-seqpacket-sentinel.sock", 0o777)
seqpacket.listen(2)
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
max_sessions = {2 if args.parallel else 1}
[[workspace]]
id = "fixture"
display_name = "Isolated scratch workspace"
path = {json.dumps(str(workspace))}
allowed_runtimes = ["fixture"]
''')
config.chmod(0o644)
if args.parallel:
    with config.open("a") as config_file:
        config_file.write(f'''\n[[workspace]]
id = "peer"
display_name = "Independent peer workspace"
path = {json.dumps(str(peer_workspace))}
allowed_runtimes = ["fixture"]
''')
properties = ["Delegate=yes", "ProtectControlGroups=no", "NoNewPrivileges=yes",
    "ProtectSystem=strict", "ProtectHome=yes", "PrivateTmp=yes", "PrivateDevices=yes",
    "CapabilityBoundingSet=", "RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK",
    "KillMode=control-group", "MemoryMax=4G", "TasksMax=512", "LimitCORE=0", "LimitNOFILE=8192", "UMask=0077",
    f"ReadWritePaths={state} {workspace}{' ' + str(peer_workspace) if args.parallel else ''} /sys/fs/cgroup/system.slice/{unit}.service"]
if args.codex or args.provider:
    properties += ["EnvironmentFile=/etc/harness-aba/codex.env"]
command = ["systemd-run", "--quiet", "--wait", "--pipe", "--unit=" + unit,
    "--service-type=exec", "--uid=" + user.pw_name, "--gid=" + user.pw_name]
command += ["--property=" + value for value in properties]
command += [str(binary), "runtime", "probe", "--config", str(config), "--runtime", "fixture",
    "--workspace", "fixture", "--insecure-loopback-development"]
if args.codex:
    command += ["--exercise"]
if args.crash:
    command += ["--hold-for-crash"]
if args.parallel:
    command += ["--parallel"]
if args.retirement:
    command = command[:command.index(str(binary))] + [str(binary), "scope-probe-retirement", "--config", str(config), "--insecure-loopback-development"]
try:
    crash_confirmed = False
    moved_unit = None
    if args.crash:
        process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        selector = selectors.DefaultSelector()
        selector.register(process.stdout, selectors.EVENT_READ)
        if not selector.select(35):
            subprocess.run(["systemctl", "stop", unit + ".service"], timeout=15, check=False)
            raise RuntimeError("synthetic Host failed to arm before the deadline")
        line = process.stdout.readline()
        selector.close()
        if line.strip() != "Synthetic scope probe armed for Host-death injection.":
            raise RuntimeError("unexpected synthetic Host startup result")
        before = json.loads((state / "registry.json").read_text())
        if len(before["records"]) != 1 or any(item["closed"] for item in before["records"].values()):
            raise RuntimeError("crash fixture has no live durable scope claim")
        subprocess.run(["systemctl", "kill", "--kill-who=main", "--signal=KILL", unit + ".service"], check=True, timeout=10)
        old_output, old_error = process.communicate(timeout=20)
        (base / "crashed-unit-output.txt").write_text(line + old_output + old_error)
        if process.returncode == 0:
            raise RuntimeError("Host crash was not observed")
        subprocess.run(["systemctl", "reset-failed", unit + ".service"], check=True, timeout=10)
        at_binary = command.index(str(binary))
        recovery = command[:at_binary] + [str(binary), "scope-reconcile", "--config", str(config), "--insecure-loopback-development"]
        # Same registry under another valid delegated root must not certify absence there.
        moved_unit = unit + "-m"
        moved_config = base / "moved.toml"
        moved_config.write_text(config.read_text().replace(f"/{unit}.service", f"/{moved_unit}.service"))
        moved = [part.replace("--unit=" + unit, "--unit=" + moved_unit)
            .replace(f"/{unit}.service", f"/{moved_unit}.service")
            if part != str(config) else str(moved_config) for part in recovery]
        retained = (state / "registry.json").read_bytes()
        rejected = subprocess.run(moved, capture_output=True, text=True, timeout=30)
        (base / "relocation-rejection.txt").write_text(rejected.stdout + rejected.stderr)
        if rejected.returncode == 0 or retained != (state / "registry.json").read_bytes():
            raise RuntimeError("different delegation was allowed to modify or close the original scope")
        result = subprocess.run(recovery, capture_output=True, text=True, timeout=30)
        crash_confirmed = result.returncode == 0 and "Recorded process scopes reconciled; no runtime was started." in result.stdout
    else:
        result = subprocess.run(command, capture_output=True, text=True, timeout=480 if args.codex else 45)
    (base / "unit-output.txt").write_text(result.stdout + result.stderr)
    if result.returncode != 0:
        raise RuntimeError("isolated unit probe failed; retained unit-output.txt")
    facts = {"interrupted_retirement_recovered": "Interrupted closed-scope retirement completed in a fresh process without runtime execution." in result.stdout} if args.retirement else {"real_provider_and_file_tool": "Isolated provider reply and read-only workspace tool confirmed." in result.stdout} if args.codex else json.loads((workspace / "isolation-result.json").read_text())
    if args.codex:
        facts.update({
            "approval_deny_allow": "Isolated file approval denial and approval confirmed." in result.stdout,
            "configuration_confirmed": "Isolated effective configuration change confirmed." in result.stdout,
            "actual_cancel_and_continue": "Actual sleep tool start, turn cancellation and continuation confirmed." in result.stdout,
        })
    expected = {"interrupted_retirement_recovered"} if args.retirement else {"real_provider_and_file_tool", "approval_deny_allow", "configuration_confirmed", "actual_cancel_and_continue"} if args.codex else {
        "same_existing_uid", "host_state_hidden", "host_state_via_proc_hidden", "endpoint_key_path_hidden",
        "no_system_bus", "no_cgroup_control", "private_pid_namespace", "host_loopback_denied",
        "host_private_network_denied", "host_abstract_socket_denied", "workspace_control_socket_denied",
        "provider_socket_hidden_from_runtime", "upstream_credential_not_in_runtime_env",
        "isolated_home", "datagram_pair_cannot_reach_host", "stream_pairs_stay_private", "seqpacket_pairs_stay_private",
        "local_relay_policy_matches_mode", "no_host_state_descriptors"}
    if not args.codex and not args.retirement:
        expected.add("socketpair_domain_type_protocol_allowlist")
    if set(facts) != expected or any(type(value) is not bool for value in facts.values()):
        raise RuntimeError("fixture omitted or changed a required assertion")
    registry = json.loads((state / "registry.json").read_text())
    facts["whole_scope_cleanup_recorded"] = len(registry["records"]) == (2 if args.parallel else 1) and all(
        item["closed"] for item in registry["records"].values())
    if not args.crash:
        facts["no_runtime_resource_rejection"] = all(item.get("resource_faults") == {
            "pids_max": 0, "memory_oom": 0, "memory_oom_kill": 0} for item in registry["records"].values())
    if not args.codex and not args.retirement:
        before = (workspace / "isolation-heartbeat").read_text()
        time.sleep(0.3)
        facts["background_tool_stopped"] = (workspace / "isolation-heartbeat").read_text() == before
    facts["accounts_unchanged"] = accounts == [(entry.pw_name, entry.pw_uid, entry.pw_gid) for entry in pwd.getpwall()]
    facts["active_aba_untouched"] = (subprocess.run(["systemctl", "is-active", "--quiet", "harness-aba.service"]).returncode == 0
        and live_identity == subprocess.check_output(["systemctl", "show", "harness-aba.service", "-p", "MainPID", "-p", "InvocationID"], text=True))
    facts["probe_succeeded"] = facts["interrupted_retirement_recovered"] if args.retirement else crash_confirmed if args.crash else "ACP runtime probe succeeded." in result.stdout
    if args.crash:
        facts["actual_host_crash_reconciled_without_runtime_restart"] = crash_confirmed
        facts["relocated_delegation_rejected_without_state_change"] = True
    if args.parallel:
        facts["peer_survived_first_scope_cleanup"] = "Independent peer runtime and relay remained usable after the first scope closed." in result.stdout
    report = {"source_sha": args.source_sha, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "unit": unit, "facts": facts, "scope": "isolated real Codex reply and read-only tool" if args.codex else "deterministic kernel containment; fixed provider path enabled" if args.provider else "deterministic kernel containment, no network",
        "completion": "partial H1 evidence; approval/cancellation/restart matrix and complete Host remain open"}
    (base / "verification.json").write_text(json.dumps(report, indent=2))
    print(json.dumps(report, indent=2))
    if not all(facts.values()):
        raise RuntimeError("containment assertion failed")
finally:
    if moved_unit:
        subprocess.run(["systemctl", "stop", moved_unit + ".service"], capture_output=True, timeout=15, check=False)
    loaded = subprocess.run(["systemctl", "show", "--property=LoadState", "--value", unit + ".service"],
        capture_output=True, text=True, timeout=10)
    if loaded.returncode == 0 and loaded.stdout.strip() != "not-found":
        subprocess.run(["systemctl", "stop", unit + ".service"], capture_output=True, timeout=15, check=False)
    tcp.close()
    unix.close()
    pathname.close()
    datagram.close()
    seqpacket.close()

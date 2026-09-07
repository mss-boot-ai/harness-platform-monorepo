# acp-brige-agent (ABA)

ABA is the lightweight local ACP bridge and security boundary for Harness Platform.

The MVP implementation includes:

- a Rust 1.88.0 workspace;
- an exact dependency on official `agent-client-protocol` 2.0.0 with default features disabled;
- build information that exposes AWP, ACP SDK, and pinned Platform baselines;
- strict local TOML configuration for Platform URL, limits, Runtime Profiles, and Workspaces;
- failure-closed rejection of unknown fields, insecure Platform URLs, remote/relative commands, unknown runtime grants, and unimplemented symlink following;
- the fixed 148-byte AWP v1 Canonical AAD encoder and offset-level tests;
- an explicit loopback-only development KeyStore with separate P-256 signing/KEM keys, 0700 directory and 0600 file enforcement, symlink rejection, and public-only inspection.
- ABA enrollment, DPoP refresh, one-time Ticket, trust pinning and signed WSS READY;
- local Runtime/Workspace policy and a process-group ACP supervisor;
- RFC 9180 Key Package, AES-GCM encrypted ACP frames and signed ACK/Error frames;
- a bounded, atomically replaced relay Journal with an exclusive process lease;
- Full-Jitter reconnect that retains active Agent processes, Session keys and replay state;
- `LOCAL_DISPATCH_UNCERTAIN` fail-closed handling and signed CloseTunnel cleanup.

The development file KeyStore is explicitly loopback-only and is not a production OS
Keyring substitute. Process restart does not restore active in-memory Session keys; the
durable Journal detects uncertain dispatch, while the HC closes unrecoverable Sessions.

## Commands

```bash
cargo run -- version --json
cargo run -- config validate --config ./aba.toml
cargo run -- runtime probe --config ./.aba-dev/local.toml --runtime test-agent --workspace harness-platform --insecure-loopback-development
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s adapters/tests -p 'test_*.py'
cargo run -- identity init --store ./.aba-dev/identity.json --platform http://127.0.0.1:8082 --insecure-dev-keystore --json
cargo run -- identity inspect --store ./.aba-dev/identity.json --platform http://127.0.0.1:8082 --insecure-dev-keystore --json
cargo run --bin aba -- run --config ./.aba-dev/local.toml --store ./.aba-dev/identity.json --insecure-dev-keystore
```

## Example configuration

```toml
schema_version = 1

[platform]
url = "https://platform.example.com"

[limits]
max_sessions = 8
max_packet_bytes = 1048576
max_inflight_per_channel = 1024
journal_max_bytes = 67108864

[[runtime]]
id = "codex-acp"
display_name = "Codex ACP"
command = "/usr/local/bin/codex-acp"
args = ["--acp"]
env_allow = ["HOME", "PATH"]
max_sessions = 2

[[workspace]]
id = "mss-boot-admin"
display_name = "mss-boot-admin"
path = "/home/user/workspace/mss-boot-admin"
allowed_runtimes = ["codex-acp"]
follow_symlinks = false
```

Secrets, endpoint private keys, tokens, tickets, and session keys never belong in this file.

`adapters/deepseek_harness_acp.py` is a bounded ACP stable-v1 stdio adapter for the official
DeepSeek Harness Python SDK. It keeps one SDK instance/session alive for the ABA child-process
lifetime, accepts text prompts only, and emits protocol JSON on stdout. Deploy it with a
dedicated virtual environment and inject the existing model gateway settings through the
ABA runtime environment allow-list; never copy an API key into TOML or command arguments.

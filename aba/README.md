# acp-brige-agent (ABA)

ABA is the lightweight local ACP bridge and security boundary for Harness Platform.

This foundation checkpoint implements only:

- a Rust 1.88.0 workspace;
- an exact dependency on official `agent-client-protocol` 2.0.0 with default features disabled;
- build information that exposes AWP, ACP SDK, and pinned Platform baselines;
- strict local TOML configuration for Platform URL, limits, Runtime Profiles, and Workspaces;
- failure-closed rejection of unknown fields, insecure Platform URLs, remote/relative commands, unknown runtime grants, and unimplemented symlink following;
- the fixed 148-byte AWP v1 Canonical AAD encoder and offset-level tests.

It does **not** yet implement Enrollment, endpoint keys, DPoP, WSS, encryption, process supervision, Journal, or ACP proxying. A pushed foundation commit is not a working remote agent.

## Commands

```bash
cargo run -- version --json
cargo run -- config validate --config ./aba.toml
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

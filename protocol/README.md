# Protocol

This directory is the single source of truth for the ABA Wire Protocol (AWP), generated Rust/Go/TypeScript bindings, compatibility baselines, and cross-language golden vectors.

Current foundation:

```text
AWP wire:              v1.0
WebSocket subprotocol: mss.awp.v1
ACP payload target:    stable ACP v1
Schema:                 proto/mss/awp/v1/wire.proto
Shared constants:       constants/awp-v1.json
Canonical AAD length:   148 bytes
```

Rules:

- AWP is versioned independently from ACP.
- ACP JSON-RPC payload bytes and batch boundaries remain opaque to Platform.
- Schema, canonical AAD, crypto transcript, generated code, and vectors evolve in the same checkpoint.
- Security signatures never depend on ordinary protobuf serialization order.
- A released field number is not reused.
- Unknown critical flags, unknown major versions and unknown security control types fail closed.
- OpenTunnel carries only local profile identifiers and authorization metadata; it never carries command, args, cwd, environment values, scripts, binaries or arbitrary paths.

Validation:

```bash
make protocol-check
# or
./scripts/check-protocol.sh
```

The check requires `python3` and `protoc`. Generated bindings and golden vectors will be added in later foundation checkpoints; the absence of generated code must not be described as protocol interoperability.

See `docs/architecture/PROTOCOL.md` before changing anything here.

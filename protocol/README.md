# Protocol

This directory is the single source of truth for the ABA Wire Protocol (AWP), generated Rust/Go/TypeScript bindings, compatibility baselines, and cross-language golden vectors.

Current foundation:

```text
AWP wire:              v1.0
WebSocket subprotocol: mss.awp.v1
ACP payload target:    stable ACP v1
Schema:                 proto/mss/awp/v1/wire.proto
Shared constants:       constants/awp-v1.json
Identity/DPoP vector:   testdata/v1/suite-0001-jwk-es256-dpop.json
Wire binding vector:    testdata/v1/wire-server-challenge.json
Go binding:             ../platform/internal/harness/protocol/awpv1/wire.pb.go
TypeScript binding:     ../hc/packages/core/src/generated/mss/awp/v1/wire_pb.ts
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

The check requires `python3` and `protoc`. Go bindings are generated with `protoc 35.0` and `protoc-gen-go 1.36.12`; TypeScript bindings use `protoc-gen-es 2.14.1`. Shared ServerChallenge bytes are decoded and deterministically re-encoded by both generated bindings. The Suite 0001 fixtures are explicitly test-only. HPKE/AEAD vectors remain later checkpoints, so these vectors alone are not complete protocol interoperability.

See `docs/architecture/PROTOCOL.md` before changing anything here.

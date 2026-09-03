# Protocol

This directory is the single source of truth for the ABA Wire Protocol (AWP), generated Rust/Go/TypeScript bindings, compatibility baselines, and cross-language golden vectors.

Rules:

- AWP is versioned independently from ACP.
- Initial AWP is `v1`; initial ACP payload compatibility is stable ACP v1.
- ACP JSON-RPC payload bytes and batch boundaries remain opaque to Platform.
- Schema, canonical AAD, crypto transcript, generated code, and vectors evolve in the same checkpoint.
- Security signatures never depend on ordinary protobuf serialization order.
- A released field number is not reused.

See `docs/architecture/PROTOCOL.md` before changing anything here.

# C2/C3 cross-language integration gate

- Date: 2026-09-14.
- Baseline: `fc2465735d1ea23bfad1012ee9b4d29667c11064`.
- Status at authorship: implemented test; not yet verified.

The Remote Integration workflow builds the actual Rust ABA executable, starts it with an ephemeral allowlisted Python ACP fixture and exercises a real Gateway listener and SQLite store with an independent Go cryptographic HC client. It tests HPKE establishment, runtime descriptor/config roundtrip, first update before completion, ping while a turn is active, cancel without closing the session, original permission rejection, HC disconnect/reconnect to the same process, immutable frame replay and absence of a private prompt canary in stored transport records/metadata and ABA diagnostics. Tests require explicit executable paths so ordinary unit runs cannot pretend to execute missing binaries. The dedicated CI job supplies those paths and runs with the Go race detector.

The deterministic fixture does not call a real model or execute workspace tools. Enrollment/user login, browser UI, long offline periods, host restart recovery, new-endpoint sharing and production deployment remain separate gates. No customer credentials or real data are used.

The C3 local storage candidate was validated by Remote Checkpoint run `34791127361`: 52 HC tests in 16 files, typecheck/lint/build, 33 Rust unit tests, four duplex integrations, one supervisor integration and Clippy. Its exact tree `d39f16b82bf5fb11aa5d95846d1b45b9dae0e607` was integrated into `fc2465735d1ea23bfad1012ee9b4d29667c11064`. The new integration gate does not inherit that success without executing.

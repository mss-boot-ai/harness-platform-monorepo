# HC durable conversation integration — verified slice

- Recorded: 2026-09-14 20:10 +08:00.
- Repository: `mss-boot-ai/harness-platform-monorepo`.
- Branch: `design/device-fabric-foundation`; [Draft PR #3](https://github.com/mss-boot-ai/harness-platform-monorepo/pull/3).
- Starting source: `717a85351beed69834ef810c5cf9aaeb09dc78d7`.
- Verified implementation: `a0dad3fdbfb9a15b701f4973c48b8d2158072442`, `fix(hc): retain failed drafts independently across conversation switches`.
- Actual PR merge checkout tested by Actions: `7b9071dfc6d818c41e050a7d36ae5fcfa37d0435`. This is not a merged PR.
- Status: Verified for the production HC same-installation conversation slice described below. This report is a subsequent documentation change; it does not claim that every Remote delivery gate is complete.

## Result and scope

The production HC application uses a stable endpoint coordinator and durable per-conversation controllers. New chat keeps existing sessions alive. Conversation selection is immediate and versioned, while restore selection, history, drafts, key envelopes, pending operations and exact outbound frames use encrypted local storage. A failed draft save remains associated with its original conversation and is available for copying in read-only recovery.

Web Locks ownership precedes identity/credential mutations and connection setup. Recovery checks current endpoint/session authorization and ABA fingerprints. Outbound sequences are reserved before encryption, complete packets are saved before transport, and incoming effects are saved before ACK. Reconnection replays original bytes. Expected transport replacement is recoverable; integrity conflicts remain quarantined across reconstruction. Backpressure exposes a delivery/reconnection state instead of leaving an unsent request silently waiting on a live socket.

## Actual validation

Local tools: Node `24.20.0` through nvm, pnpm `10.34.5`, Go `1.26.6` through gvm, Rust `1.88.0`.

After the relevant commits were pushed:

```text
pnpm --dir hc lint
pnpm --dir hc typecheck
pnpm --dir hc test
pnpm --dir hc build
node --test hc/web/e2e/websocket-tap.test.mjs
cargo +1.88.0 build --locked --bin aba
HARNESS_TEST_ABA_BINARY=<built-aba> HARNESS_TEST_ACP_FIXTURE=<fixture> \
  go test -count=1 -race -v -timeout=120s \
  -run '^TestRemoteActualABAGatewayDuplex$' ./internal/harness/gateway
```

Final local HC result: lint/typecheck/build passed; **102 tests in 23 files passed**, plus **2 WebSocket frame-tap tests**. The actual ABA/Gateway race integration passed; its most recent local run was on `07dd21205057b4ae8732438a60cc0c8d502c1b16`, before the final UI-only draft change. Actions reran the actual-binary test against the verified final source.

All **12 PR checks** passed for the verified implementation:

- [Harness Platform CI — 34841287378](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34841287378): documentation, Thin Host import, TimescaleDB, container images, protocol, Rust and HC checks.
- [HC Chat UI — 34841287431](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34841287431): production App ownership/handoff and disconnected UI, plus explicitly synthetic component smoke coverage, IME, copy, drafts, explicit close, new chat, scrolling and mobile layout.
- [Remote Integration — 34841287375](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34841287375): actual-binary Go-client integration and the production HC browser acceptance job.
- [Remote Checkpoint — 34841287352](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34841287352): reproducible source and runtime/HC checks.

## Authenticated production-browser acceptance

The browser job serves the production bundle, runs the actual Admin/Gateway/ABA binaries, initializes a private temporary database through the imported Admin migration command, creates a temporary test account, and uses normal login, HC registration and ABA enrollment approval. It does not inject React state or bypass production authentication. Playwright is pinned to `1.55.0`.

The Agent is the **deterministic ACP subprocess**, not a live model. The [retained sanitized report](2026-09-14-hc-remote-browser.json) records eight passing scenario groups:

1. Two live conversations on distinct allowed workspaces; separate model settings, drafts, events and permission state; rapid selection/typing; refresh during approval; approve once; cancel and continue.
2. Refresh after a complete packet is persisted but dropped before forwarding: original bytes, one execution.
3. Refresh after sending but before ACK delivery: original bytes, one execution.
4. Refresh after ACK persistence but before the result: no new prompt or duplicate execution.
5. A second same-profile tab makes no endpoint mutations or connection; after the owner exits it restores both histories and continues the same endpoint.
6. Prompt canary absent from Platform DB/WAL/Admin/Gateway output and ordinary localStorage empty.
7. Closing B leaves A active; revocation rejects refresh without automatic re-registration or a replacement task.
8. Missing Web Locks fails closed before any login, refresh, ticket or connection.

The job also checked 390 px and 320 px overflow. Desktop and mobile screenshots were downloaded and visually inspected; no page overflow or input obstruction was observed. [Browser artifact 10346337302](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34841287375/artifacts/10346337302) contains the original report and screenshots. Original report SHA-256: `2567b67563b53955407c6dd1d9f188ea6f257caa9d2c6a05371019983a99ab90`.

## Built-in browser observations

The built-in browser separately exercised the production application on source `e378791fb67ebbc35677133fea76cda1dafa6988`: entering a draft, opening/closing settings, confirming the unchanged draft, observing a second tab's waiting state and absent settings actions, then closing the owner and observing the follower acquire the connection controls. The isolated production bundle also showed supported secure storage and the normal login entrypoint. Local test services were stopped and private temporary stack state was removed.

On `a0dad3fdbfb9a15b701f4973c48b8d2158072442`, a synthetic component fixture was used to confirm that explicit close leaves the entered draft visible in a read-only textarea and exposes a copy action. The authenticated full-recovery scenarios above were run by the Actions browser job, not claimed as an additional authenticated built-in-browser run.

## Failures, repairs and retests

| Source / run | Observed failure | Repair / evidence |
| --- | --- | --- |
| `c2c6b49e293a5a1e5675e072a31f09caf6519493` | Two lint warnings and one overly specific rejection-message assertion | `2211e8b9aafe9f23d62306e224ab33b3d636e00f`; local 87 tests and all 11 then-existing PR checks passed |
| `e378791fb67ebbc35677133fea76cda1dafa6988`, UI run `34834364545` | Ownership assertion also counted Vite's reload socket | `a3745721dc9c674c4bfccad6521d8018e335390f`; all 11 checks passed |
| `3eabd9085cb6e1690838e085be6c247341de9149`, browser run `34836025871` | Pre-reload B-draft assertion failed during asynchronous selection | `0ea873d49d69ff28bdd5b9efcc83b12087581e28` makes selection immediate/versioned; 90 runtime tests passed, but one new test annotation failed typecheck |
| `0ea873d49d69ff28bdd5b9efcc83b12087581e28` | Test callback return-type annotation | Corrected in `fe7997e724e5cf22105fe3df36a48dafe9eed702`; full local checks and 93 tests passed |
| Independent review of `3eabd90` | Stale authorization response, lost held delivery, unsent backpressure and fault-lifetime gaps | Focused repairs `fe7997e724e5cf22105fe3df36a48dafe9eed702`, `3cdae17113990d6e9e7261520cc32fb43a1a280f`, `07dd21205057b4ae8732438a60cc0c8d502c1b16`; deferred-race, exact replay, transport replacement and persisted-quarantine tests added |
| Browser run `34839096247` | Navigation completed but `networkidle` timed out with application polling | `07dd21205057b4ae8732438a60cc0c8d502c1b16` uses DOM loading followed by explicit application-state assertions |
| `07dd212`, browser run `34840416354` | Both conversations/drafts restored, but test refreshed before selection save confirmation and expected the unsaved choice | `a0dad3fdbfb9a15b701f4973c48b8d2158072442` waits for affirmative save confirmation, retains the equality assertions, and isolates failed draft edits per conversation; final 12 checks passed |

No failing result was relabeled as passed. Codex with ChatGPT provided planning and independent source review; execution and the commit/push/test sequence remained local-agent responsibilities.

## Remaining scope

This verifies the HC portion of the C3 slice, not all of C3 or R05–R07. Authoritative execution-host/workspace fencing, durable Host/Agent restart recovery, independently authorized devices and writer leases, real-model acceptance of this increment, resources, PWA notifications, production deployment, capacity/long-running faults and the final cross-device journey remain unfinished. Some corruption, quota and key-loss combinations have unit coverage rather than a complete browser fault matrix. PR #3 remains Draft; no merge or deployment occurred.

# C3 production HC conversation integration

- Date: 2026-09-14.
- Branch: `design/device-fabric-foundation`, PR #3.
- Starting source: `717a85351beed69834ef810c5cf9aaeb09dc78d7`.
- State: Verified HC same-installation slice on `a0dad3fdbfb9a15b701f4973c48b8d2158072442`; broader Remote gates remain unfinished.

Current evidence: [final validation report](../roadmap/verification/2026-09-14-hc-remote-conversations.md). All 12 PR checks passed, including authenticated production-browser recovery against actual Admin/Gateway/ABA and a deterministic ACP subprocess. The sections below retain the historical authored/failure/repair states rather than rewriting them as early successes.

The endpoint coordinator keeps one durable controller per conversation, routes bounded incoming packets by their untrusted identifiers before core signature/binding verification, and serializes connection-control messages across conversations. Connection replacement fences stale continuations and drains the old controller queues before exposing a replacement transport.

Encrypted workspace metadata records the selected conversation, an unsent compose draft and the original pending session-creation identity. Ambiguous creation retains that identity and draft for explicit reconciliation. Session authorization and current ABA fingerprints are checked before recovery. A client-side workspace conflict guard is conservative product behavior, not authoritative host fencing.

Endpoint credential access requires ownership and coalesces refresh operations. `UNCERTAIN` state cannot replay outbound operations; legacy sessions retain their basic prompt path. The next checkpoint connects these components to App and replaces the existing in-memory SessionSetup implementation.

Validation is pending for this source checkpoint. Full tests follow commit/push; no prior SHA's success is attributed to these changes. Independent-device attachments, persistent Host restart recovery, authoritative workspace fencing, live-model acceptance, resources and deployment remain outside this slice and unfinished.

## Coordination checkpoint validation

Source `c2c6b49e293a5a1e5675e072a31f09caf6519493` was committed and pushed before validation. Local Node 24.20.0 / pnpm 10.34.5 typecheck and production build passed. Of 86 tests, 85 passed and one assertion expected a different rejection message; the no-replay behavior itself held. Lint found two unnecessary iterable spreads. The next repair removes those warnings, asserts refusal without depending on rejection ordering, and adds a regression for simultaneous local workspace writers before the first persistence finishes. Full validation of the repair remains pending until its own push.

Repair `2211e8b9aafe9f23d62306e224ab33b3d636e00f` was pushed before the local lint/typecheck, 87 tests and production build all passed. This evidence covers the coordination source, not the following React integration.

The repair's 11 PR checks also passed: Harness Platform CI `34833290078`, HC Chat UI `34833290056`, Remote Integration `34833290045`, and Remote Checkpoint `34833290149`. The actual-binary integration still uses its deterministic ACP fixture and a Go HC test client.

## Production React integration checkpoint

App now acquires exclusive installation ownership before bootstrapping identity or refreshing credentials. A stable EndpointAccess and ConversationManager outlive selection changes and settings dialogs. SessionSetup delegates crypto, cursors, delivery and storage to the controllers; new chat changes selection without closing prior sessions. Each conversation retains its own draft, runtime controls and incoming events. Explicit close remains a confirmed, target-specific action. The UI exposes read-only recovery failures and ambiguous creation reconciliation. Draft updates capture their destination and edit version; shutdown fences transport and drains already accepted writes before releasing ownership.

This source is authored, awaiting its own pushed-checkpoint tests and browser evidence. No deployment or live-model acceptance has occurred.

Source `e378791fb67ebbc35677133fea76cda1dafa6988` passed local lint/typecheck, 87 tests and production build. Ten PR checks passed, including actual Gateway/ABA integration; HC Chat UI run `34834364545` failed because the new ownership assertion counted Vite's development-reload socket as an endpoint connection. The repair restricts that assertion to Gateway sockets while retaining the zero endpoint-mutation checks. Built-in-browser inspection of the production app independently confirmed draft retention across settings, a waiting second tab without settings actions, and acquisition after the original tab closed. Authenticated conversation restoration still requires the isolated-stack browser gate.

## Isolated production-browser acceptance checkpoint

The new acceptance launcher creates a private temporary SQLite database and test account through the imported Admin application's migration command. It runs the actual Admin/Gateway/ABA binaries, enrolls ABA through the normal approval API, and serves the production HC bundle through a loopback proxy. The browser logs in and registers its own non-exportable endpoint keys through the ordinary application flow. No production authentication shortcut or key-export hook is added.

A test-only WebSocket frame tap preserves the original handshake, subprotocol, and bytes while dropping selected encrypted messages or ACKs. The scenario exercises two live conversations on distinct local workspaces, isolated drafts/configuration, refresh during a pending approval, cancellation/continuation, three delivery boundaries, tab handoff, revocation and unsupported Web Locks. Opt-in deterministic ACP accounting records only request IDs and prompt digests in temporary workspace files. Sanitized reports/screenshots exclude private state; the private test directory is removed on shutdown.

These scenarios are authored and must pass after this checkpoint is pushed. Browser tests with the deterministic ACP subprocess do not constitute real-model or independent-device acceptance. Storage corruption/key-loss/quota combinations still rely partly on the controller/store unit matrix; authoritative Host fencing and restart recovery remain unfinished.

## Independent review: selection repair

The authenticated browser run `34836025871` reached normal login/enrollment, two active ACP sessions and a pending permission request, then failed before reload at the B-draft assertion. It tested merge checkout `2072811f93700e0af5cc029a5a2627eb9d40a745` for source `3eabd9085cb6e1690838e085be6c247341de9149`. This is a selection/draft binding failure, not evidence of refresh-related loss.

The repair separates immediately visible selection from asynchronously persisted restore selection. Navigation bypasses the operation mutex, late selection writes cannot replace newer choices, and a delayed creation response cannot steal a later navigation choice. Draft input is temporarily disabled only while the initial remote session is being created. The browser test retains both draft equality assertions and adds rapid A/B/A clicks with immediate typing. Deferred-persistence unit cases cover the same boundary and failure visibility. Validation follows this repair's own commit/push.

Selection source `0ea873d49d69ff28bdd5b9efcc83b12087581e28` passed 90 runtime tests and the production build; its typecheck rejected one test callback's inferred return type. The explicit `() => void` annotation is included in the reconciliation checkpoint; a runtime pass did not override that failed typecheck.

## Independent review: reconciliation repair

Each list request now captures its subject controllers and their local creation-grant revisions. Its result can revoke or update only those observed grants; a newer creation is checked by a subsequent fresh request. This is not a permanent union of authorization sets. Known temporarily unauthorized messages remain in the bounded buffer without ACK, and reauthorization flushes that buffer plus requests recovery from durable cursors. A failed overall authorization request disconnects transport so reconnection performs fresh checks and recovery. Tests hold a stale response across creation, retain a pending final frame during an authorization gap, and verify fingerprint changes still remove access. The key-package routing regression isolates routing from the separately tested core HPKE verification.

Source `fe7997e724e5cf22105fe3df36a48dafe9eed702` passed local lint/typecheck, all 93 tests and production build after push. Its full browser/CI result is tracked separately.

## Independent review: backpressure repair

A full socket buffer or synchronous socket-send failure now detaches transport and exposes a recoverable delivery state. The already persisted packet remains in the outbox; a verified replacement connection uses existing exact-byte replay. The UI distinguishes a saved request awaiting delivery from a confirmed delivery awaiting execution. The regression asserts no first transmission under backpressure, unchanged packet bytes/sequence after recovery, and no CloseSession side effect.

Source `3cdae17113990d6e9e7261520cc32fb43a1a280f` passed local lint/typecheck, all 94 tests and production build after push.

## Independent review: fault lifetime repair

Expected connection replacement/shutdown is now a typed transport interruption. A verified receive or resume operation interrupted at its control-send boundary retains valid durable state without creating a permanent integrity fault. Failed control encoding or sending retires that connection rather than skipping a consumed sequence on a live connection. Delayed control-signature tests verify that an old connection sends nothing and the replacement starts its own sequence at one.

Integrity/binding/conflict failures are quarantined within the serialized conversation queue. Their bounded safe status is stored in the encrypted snapshot, remains sticky across later observations, and prevents replay/new encryption after reconstruction. If saving the status fails, the current view remains fail-closed and CAS never overwrites a newer record. The complete outbox is validated before any operation is replayed. Regressions cover a verified conflicting duplicate, reload, CAS failure, and a malformed later outbox entry.

The repaired browser run for `fe7997e724e5cf22105fe3df36a48dafe9eed702` passed the original pre-reload draft assertion but workflow `34839096247` timed out waiting for `networkidle` after navigation had completed. The acceptance test now waits for DOM loading and its explicit permission/conversation state assertions; it does not extend the timeout or remove those assertions. Reports distinguish source HEAD from the actual PR merge checkout, and failures retain a screenshot plus safe status notices for diagnosis.

Source `07dd21205057b4ae8732438a60cc0c8d502c1b16` passed 99 local tests, two fault-tap tests, lint/typecheck/build and actual ABA/Gateway race integration. Browser workflow `34840416354` restored both conversations, their drafts and B's pending permission, but the screenshot showed A selected: the test refreshed immediately after visible navigation without awaiting selection's save confirmation. The assertion now waits for the affirmative saved-state signal before requiring B to be selected after reload.

Final draft review also replaces the single optimistic edit slot with bounded per-conversation entries. A failed A save cannot be discarded by editing or successfully saving B. Stale completions cannot clear newer edits, and read-only recovery displays retained drafts with an explicit copy action. These in-memory unsaved edits are not falsely described as durable; their failure notice tells the user to keep the page open.

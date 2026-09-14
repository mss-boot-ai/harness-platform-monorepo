# C3 production HC conversation integration

- Date: 2026-09-14.
- Branch: `design/device-fabric-foundation`, PR #3.
- Starting source: `717a85351beed69834ef810c5cf9aaeb09dc78d7`.
- State: coordination source locally tested; production React integration authored; browser acceptance pending.

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

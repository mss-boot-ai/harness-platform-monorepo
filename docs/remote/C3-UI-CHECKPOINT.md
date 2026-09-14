# C3 production HC conversation integration

- Date: 2026-09-14.
- Branch: `design/device-fabric-foundation`, PR #3.
- Starting source: `717a85351beed69834ef810c5cf9aaeb09dc78d7`.
- State: coordination source authored; production React integration and browser acceptance pending.

The endpoint coordinator keeps one durable controller per conversation, routes bounded incoming packets by their untrusted identifiers before core signature/binding verification, and serializes connection-control messages across conversations. Connection replacement fences stale continuations and drains the old controller queues before exposing a replacement transport.

Encrypted workspace metadata records the selected conversation, an unsent compose draft and the original pending session-creation identity. Ambiguous creation retains that identity and draft for explicit reconciliation. Session authorization and current ABA fingerprints are checked before recovery. A client-side workspace conflict guard is conservative product behavior, not authoritative host fencing.

Endpoint credential access requires ownership and coalesces refresh operations. `UNCERTAIN` state cannot replay outbound operations; legacy sessions retain their basic prompt path. The next checkpoint connects these components to App and replaces the existing in-memory SessionSetup implementation.

Validation is pending for this source checkpoint. Full tests follow commit/push; no prior SHA's success is attributed to these changes. Independent-device attachments, persistent Host restart recovery, authoritative workspace fencing, live-model acceptance, resources and deployment remain outside this slice and unfinished.

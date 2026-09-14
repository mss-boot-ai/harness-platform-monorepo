# C3b: conversation persistence and refresh contract

- 2026-09-14, implementation plan under ADR-0007; not a verified feature yet.
- The existing Remote design remains the product target. This slice integrates the previously verified encrypted vault into actual HC lifecycles.

## Invariants

A Conversation owns its messages, runtime configuration, draft, pending turn, protocol cursors and local key envelope; it is not the currently selected React component. New chat must not close an existing session. All conversations share one authenticated endpoint transport and one connection-level control sequence allocator, but never share directional AEAD sequence spaces.

Persist an outbound sequence reservation before encrypting. Persist the exact signed frame and the matching pending UI/operation state before socket.send. Retry only the original bytes after a reconnect. An interrupted reservation without a saved frame requires explicit recovery/rekey rather than regenerating ciphertext with the same nonce. Signed ABA ACK allows outbox removal but does not declare the tool completed. Persist incoming message effects and receive cursor before ACK; a replay cannot append a second message or repeat approval.

Store session material, conversations, titles, drafts, pending requests and outbox as ciphertext in the existing per-browser vault, bound to the HC endpoint and its key fingerprints. Do not store endpoint private keys, token or raw content in localStorage. On refresh, validate the binding and current server session/credential state before resuming. Missing keys, revoked identity, expired key generation or corrupt storage must produce a clear recovery/readonly state, not a new anonymous session or automatic redispatch.

A Web Lock holds the browser installation's transport ownership before token refresh/WSS setup. A second tab must not rotate credentials or allocate nonce sequences concurrently. An unavailable Web Locks API is a visible unsupported-write condition, not permission to silently share one live endpoint. This is same-installation tab coordination; independent-device attachments and their controller leases are a separate milestone.

## Implementation and tests

Keep AWP v1 wire fields and crypto inputs unchanged. Isolate an encrypted repository, a bounded per-conversation controller and a React view. Route packets by session before signature/decryption; an untrusted routing hint is not authority. Keep a shared serialized connection-control sender, independent per-conversation event processing and bounded ingress queues. Store only a bounded set of histories and return capacity errors before side effects.

Test encrypted round-trip, wrong endpoint/key binding, tamper/lost key, reservation interruption, exact-byte replay, stale CAS writes, duplicate inbound results, out-of-order ACKs, another session's events, multiple live conversations, independent drafts/config, refresh during a running turn, and tab contention. A real-browser test against actual Gateway/ABA binaries is required in addition to storage/component tests. A deterministic ACP fixture must remain labeled as such; no claim of live-model or independent-device acceptance.

## Investigation of the resumed integration gate

The DPoP correction passed its three ordinary regression tests and the actual-binary scenario locally, including 12 repeated runs with GOMAXPROCS=2. CI reached key establishment but occasionally failed activation. A second test-client defect was found: acknowledged_at_ms was sampled before controlMessage separately sampled created_at_ms; the production contract requires exact equality. Serialize a cloned ACK with the single captured outer timestamp and add a deterministic regression. Do not loosen the production signature binding or extend the readiness timeout.

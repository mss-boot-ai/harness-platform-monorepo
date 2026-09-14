# C3b encrypted snapshot checkpoint

- Date: 2026-09-14.
- Parent: `2e9bbeb123a77be1d4037e29b45aa6bdc712a9a3`.
- Scope: saved source, not yet integrated into the React conversation lifecycle.

ConversationStore defines endpoint/key-bound encrypted snapshots containing session material, messages, runtime configuration, drafts, pending turns, exact outbound packets and decimal 64-bit cursors. An interrupted sequence reservation blocks further encryption instead of reusing a nonce. Snapshots use the existing non-exportable browser AES key and compare-and-set revisions. Different conversations remain independently addressable; active or unconfirmed work cannot be deleted as history.

The core gains a peer/channel/signature-bound ABA ACK reader. Future outbox cleanup must verify it instead of trusting an unverified routing hint. Tests cover ciphertext at rest, restored keys, incorrect identities, mismatched ABA fingerprints, reservations, CAS, independent drafts, and forged ACKs.

This is not completion of multi-device Remote: the actual controller, React lifecycle, Web Locks ownership and browser-to-actual-ABA refresh tests are the next integration step. Long-lived host recovery, independent-device attachments, control leases, live-model acceptance and resources remain open gates.

The prior integration repair was verified by Remote Checkpoint run 34803934875: actual ABA binary and Go Gateway, deterministic ACP fixture, five consecutive full scenario runs plus proof/timestamp/diagnostic regressions, Rust tests/Clippy and 52 HC tests. It was explicitly promoted to source in `2e9bbeb123a77be1d4037e29b45aa6bdc712a9a3`; normal CI is rerun for that commit. The earlier committed-source integration failure is not relabeled as a pass.

Only minimal parse and diff checks precede this checkpoint. Full lint/type/test/build follows the saved commit. No deployment or automatic merge.

# Actual-binary integration repair

Run `34791957572` built both real components and started the opt-in test. It failed before enrollment because the test used a Go TempDir child without explicitly enforcing ABA's canonical private-directory requirement. The repair canonicalizes that directory and sets mode 0700; production key/journal permission checks remain unchanged. This is a fixture repair, not a passing integration result.

The reviewed candidate pipeline now runs the real-binary Go integration with race detection before publishing immutable Git objects. Normal Remote Integration still runs against actual committed source, independently. The candidate is not a delivered implementation until an explicit source commit is created and its normal CI passes.

The checkpoint format optionally accepts bounded gzip-encoded source patches to avoid textual transport corruption. Decompressed bytes are still bounded, archived as a readable reviewed.patch, checked by git apply, restricted to source roots, compiled/tested, and verified by exact tree/blob identities. This workflow never updates refs or merges a PR. No application secrets are exported.

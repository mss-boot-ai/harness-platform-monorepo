# C3a: encrypted local content and recovery primitives

- Date: 2026-09-14.
- Source parent: `5302d2c886434d19ceef3f2359b2c76f2d2f473b`.
- State: authored, not a completed Remote history feature.

Static review found that the original IndexedDB inbox stored verified decrypted ACP bytes as a plaintext field. This change replaces new inbox content with AES-256-GCM ciphertext protected by a browser-generated, non-exportable CryptoKey. The normal readiness probe migrates remaining legacy plaintext in bounded batches; a failed migration prevents a misleading supported/ready result. Metadata binding covers the session/direction/sequence identity, authenticated source content hash and message ID. Bounded inbox page reads support subsequent replay into an encrypted view snapshot.

A separate local content vault supports encrypted record reads, bounded storage and compare-and-set revisions. Crypto operations are outside IndexedDB transactions; the revision check and write remain in one readwrite transaction. Concurrent first writers cannot replace each other's content key, old snapshots cannot overwrite newer revisions, and a missing decryption key never falls back to plaintext or silently replaces the lost key.

This is software storage protection, not a claim of resistance to malicious same-origin JavaScript or an unlocked compromised browser profile. It does not export endpoint private keys, upload plaintext, share HC keys, or give another logged-in endpoint access to historical content. Server-side opaque transport remains unchanged.

Authored tests cover ciphertext inspection, non-exportable keys, scope/tamper failures, concurrent creation, stale CAS writes/deletes, lost keys, encrypted inbox reads and legacy migration. They must run after the source checkpoint is pushed. Full session snapshot restore, durable outbound reservation/packet replay, tab ownership and multiple active conversation integration are the next C3b work, not implied by a passing storage unit test.

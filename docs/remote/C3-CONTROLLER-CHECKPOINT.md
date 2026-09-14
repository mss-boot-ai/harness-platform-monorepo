# C3c durable conversation controller

Continuation: the production HC integration is now verified as a bounded same-installation slice. See [the 2026-09-14 report](../roadmap/verification/2026-09-14-hc-remote-conversations.md); the original checkpoint below records its earlier, not-yet-integrated state.

- 2026-09-14; parent `ce76fb1c3b2d16e17803ba4298ec723110aaf2d2`.
- Source checkpoint: not yet connected to the production React entrypoint.

A per-conversation controller now owns pending turns, config changes, permission responses, drafts, exact outbound frames and received event projection. A new AEAD sequence is reserved durably before encryption; the complete original frame and operation state are saved before transport. Restoring a pending turn only replays original bytes in the existing safe replay window. A signed peer ACK clears transport backlog without declaring execution complete. Incoming semantic effects/cursors are persisted before receipt, including duplicate rejection and missing-range resume.

The local vault explicitly requires strict IndexedDB transaction durability for key/cursor commits. A same-installation Web Locks owner never steals another tab's live endpoint; unsupported or rejected ownership must not initiate token rotation or a transport connection. This is not an independent-device control lease, persistent host recovery or rollback-resistant hardware storage.

Tests use actual WebCrypto and existing AWP frame encoding with deterministic local ACP events. Cases cover interrupted persistence, refresh replay, duplicate input, forged ACK bounds, config confirmation, permission/cancel identity, stale packets, session confusion and tab contention. No live model is used. The following slice connects these controllers to the page, then exercises refresh with real Gateway/ABA binaries.

Previous source checkpoint `ce76fb1` passed all four remote CI workflows and local Node 24 locked lint/typecheck, 60 tests in 18 files and the production HC build. This new checkpoint receives full tests only after saving it; previous passing results are not copied to it.

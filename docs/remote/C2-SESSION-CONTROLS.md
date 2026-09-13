# C2: runtime-backed HC session controls

- Date: 2026-09-14.
- Source baseline: `62d344b22c639fa0c06c634284f1a2be89fd3694`.
- State: authored, pending source commit and full verification.

C1 is now applied source, not a pending integration patch. The source baseline passed Harness Platform CI 34789503486, Remote Checkpoint 34789503479 and HC Chat UI 34789503507. That proves the existing Rust and UI regression suites; it is not the final real multi-endpoint acceptance gate.

C2 connects the encrypted HC stream to real runtime initialization/session descriptors, effective config values, request-bound permission options, true turn cancellation, plan/tool activity and source-labelled context usage. No model names or reasoning levels are fabricated in production. Unsupported boolean/future config types are not made editable; select/group and legacy model/mode interfaces are validated. Configuration remains unchanged in the UI until an authoritative reply/update. Legacy setters trigger an execution-side re-read.

The application capability `remote-session-v1` is negotiated in the existing OpenTunnel requested-capability list. AWP field numbers, direction, AAD, crypto suite and original frame replay remain unchanged. New HC requires the matching Platform/ABA support for new sessions; an old session without this capability keeps its basic prompt behavior and does not receive unknown Remote methods. This is not silent fallback to weaker security.

Cancellation is a notification without a request ID. The original prompt's terminal response confirms cancellation; the UI retains the session and can submit another turn. The legacy DeepSeek adapter explicitly reports no turn-cancellation support until its native adapter implements it. Ending a session remains a distinct action.

Permission decisions contain only the original request ID and an offered option (or cancellation). The full inert action JSON is available in the trusted approval UI; truncated actions cannot be approved. Duplicate, wrong-session, wrong-option and closed requests are rejected; a model does not acquire an approval tool. Local UI timeouts never grant permission.

C2 does not yet claim durable history, multiple attachments, host crash recovery, a real model provider, deployment or completion of R05-R15. Those remain independent gates in DELIVERY. New reducer/renderer tests are authored; encrypted Gateway and live browser acceptance must be recorded separately from synthetic component fixtures.

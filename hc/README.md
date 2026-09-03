# HC

HC is the collective name for the user-facing H clients: Web, WeChat Mini Program, and future native or desktop applications.

Each HC installation is an independent endpoint with a locally generated signing key, KEM key, HEC, DPoP-bound credential, and revocation state. Shared protocol, crypto, identity, and session state machines will live under `hc/packages/`; platform-specific storage and lifecycle adapters will live under `hc/web/`, `hc/miniapp/`, and later `hc/native/`.

No HC implementation may fall back to plaintext storage for endpoint private keys, refresh credentials, or session root keys.

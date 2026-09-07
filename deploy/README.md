# Local MVP deployment

The verified local topology is:

```text
HC H5 :8001 -> Admin API :8080
             -> Gateway  :8082 -> ABA -> local ACP test-agent
```

Prerequisites are Go 1.26.6, Rust 1.88.0, Node 24, Corepack and pnpm 10.34.5.
Initialize the Thin Host database once with `cd platform && mss setup`; no default
administrator password is provided. Then enroll an ABA identity as documented in
`aba/README.md`, and create ignored files `aba/.aba-dev/identity.json` and
`aba/.aba-dev/local.toml`.

Build the deterministic Stable-v1 ACP fixture and point the local runtime profile at
its absolute path:

```bash
cd aba
cargo build --locked --bin test-agent
```

The runtime command should be `<repository>/aba/target/debug/test-agent`; the workspace
path should be the absolute repository path. Start the complete local topology from the
repository root:

```bash
./deploy/run-local-mvp.sh
```

Open `http://localhost:8001/` in the built-in browser. The launcher refuses to invent
Platform or ABA state and never accepts passwords, tokens, tickets, or private keys on
its command line. Override only the two local file locations when necessary:

```bash
HARNESS_ABA_CONFIG=/absolute/aba.toml \
HARNESS_ABA_STORE=/absolute/identity.json \
./deploy/run-local-mvp.sh
```

This is the local MVP launcher, not a production deployment. Production still requires
an external database, managed secret/key storage, a provisioned Gateway signer, TLS,
and an init migration job. ABA remains outbound-only and requires no ingress service.

The target-specific Kubernetes development overlay for Platform, TimescaleDB, Gateway,
Admin Web and HC Web is documented in
[`kubernetes/dev-242/README.md`](kubernetes/dev-242/README.md). It preserves ABA's
loopback-only development KeyStore by using a host-local Gateway bridge; it is not a
production KMS/OS-Keyring deployment.

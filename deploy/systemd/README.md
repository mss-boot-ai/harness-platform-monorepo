# ABA host service

These units keep the development ABA directly on the 242 host while Platform and Gateway
run in Kubernetes. `harness-gateway-port-forward` binds only `127.0.0.1:18082`; this is the
same origin configured as Gateway `NativeExternalOrigin`, so the loopback-only development
KeyStore contract remains intact.

Do not install the example blindly. First inventory the host and prove the discovered
DeepSeek executable is an ACP stable-v1 NDJSON stdio agent. Resolve every symlink with
`readlink -f`, run the initialize/new-session probe, and update `aba.toml` with the real
executable, fixed arguments, environment allow-list and canonical workspace. If DeepSeek is
an HTTP service or ordinary CLI, install a reviewed ACP adapter at the configured absolute
path before starting ABA.

The intended target layout is:

```text
/opt/harness/bin/acp-brige-agent
/opt/harness/bin/harness-gateway-port-forward
/opt/harness/bin/deepseek-acp
/opt/harness/deepseek-venv
/etc/harness-aba/aba.toml
/etc/harness-aba/runtime.env              # required, root-owned mode 0600
/etc/harness-aba/port-forward.env         # generated, root-owned mode 0600
/var/lib/harness-aba/identity.json         # generated/enrolled as harness-aba
/srv/harness-workspaces/harness-platform
```

Create the dedicated system account and directories, copy the release ABA binary under its
canonical product name, install the wrapper and units, then validate before enabling:

```bash
python3 -c 'import sys; assert sys.version_info >= (3, 10), sys.version'
useradd --system --home-dir /var/lib/harness-aba --shell /usr/sbin/nologin harness-aba
useradd --system --home-dir /run/harness-gateway-port-forward --shell /usr/sbin/nologin harness-port-forward
install -d -m 0755 /opt/harness/bin /srv/harness-workspaces
install -d -o harness-aba -g harness-aba -m 0750 /srv/harness-workspaces/harness-platform
install -d -o harness-aba -g harness-aba -m 0700 /var/lib/harness-aba
install -d -o root -g harness-aba -m 0750 /etc/harness-aba
cargo +1.88.0 build --locked --release --manifest-path aba/Cargo.toml --bin aba
install -m 0755 aba/target/release/aba /opt/harness/bin/acp-brige-agent
python3 -m venv /opt/harness/deepseek-venv
/opt/harness/deepseek-venv/bin/pip --disable-pip-version-check install \
  --requirement aba/adapters/requirements-deepseek.txt
install -m 0755 aba/adapters/deepseek_harness_acp.py /opt/harness/bin/deepseek-acp
sudo -u harness-aba /opt/harness/deepseek-venv/bin/python -c \
  'import importlib.metadata as m; assert m.version("deepseek-harness-sdk") == "0.1.1rc1"; assert m.version("deepseek-harness-runtime-bin") == "0.1.1rc1"'
install -m 0755 deploy/systemd/harness-gateway-port-forward /opt/harness/bin/
install -m 0755 deploy/systemd/provision-port-forward-kubeconfig.sh /opt/harness/bin/
install -m 0644 deploy/systemd/harness-gateway-port-forward.service /etc/systemd/system/
install -m 0644 deploy/systemd/harness-aba.service /etc/systemd/system/
install -m 0644 deploy/systemd/harness-aba-runtime-probe.service /etc/systemd/system/
install -o root -g harness-aba -m 0640 deploy/systemd/aba.toml.example /etc/harness-aba/aba.toml
systemd-analyze verify /etc/systemd/system/harness-gateway-port-forward.service \
  /etc/systemd/system/harness-aba.service \
  /etc/systemd/system/harness-aba-runtime-probe.service
HARNESS_KUBE_CONTEXT='<inventory result>' /opt/harness/bin/provision-port-forward-kubeconfig.sh
systemctl daemon-reload
systemctl enable --now harness-gateway-port-forward.service
```

Create the required `/etc/harness-aba/runtime.env` as a root-owned, mode-0600 file containing only the
existing deployment values for `MSS_HARNESS_API_BASE_URL`, `MSS_HARNESS_API_KEY` and
`MSS_HARNESS_MODEL`, plus these bounded local settings:

```text
MSS_HARNESS_MAX_TOKENS=2048
MSS_HARNESS_REQUEST_TIMEOUT_SECONDS=50
MSS_HARNESS_SESSION_ROOT=/var/lib/harness-aba/deepseek-sessions
```

Do not source or print the existing `.env`; parse only those three exact keys and publish the
new file atomically. The systemd service reads it before dropping privileges, while ABA's
`env_clear` passes only the names explicitly listed in `aba.toml` to the adapter.

Initialize identity and complete enrollment as `harness-aba` before enabling the ABA unit.
The user code must be approved through the Platform Admin UI. Never copy a development
identity from another machine.

```bash
systemctl start harness-aba-runtime-probe.service
systemctl --no-pager --full status harness-aba-runtime-probe.service
sudo -u harness-aba /opt/harness/bin/acp-brige-agent identity init \
  --store /var/lib/harness-aba/identity.json \
  --platform http://127.0.0.1:18082 \
  --insecure-dev-keystore --json
sudo -u harness-aba /opt/harness/bin/acp-brige-agent enroll \
  --store /var/lib/harness-aba/identity.json \
  --platform http://127.0.0.1:18082 \
  --name 'DeepSeek Harness 242' \
  --insecure-dev-keystore
```

Only after enrollment and the runtime probe succeed:

```bash
systemctl enable --now harness-aba.service
systemctl --no-pager --full status harness-gateway-port-forward.service harness-aba.service
```

The service sandbox grants write access only to ABA state and the selected Harness workspace.
Bind or copy the intended test workspace there rather than weakening `ProtectSystem` or
`ProtectHome` for the whole host.

This MVP still starts the reviewed DeepSeek adapter as a child of ABA under the same service
identity. Use it only for this controlled development deployment: it is not the production
isolation boundary for an untrusted local ACP runtime. A separate runtime UID plus a filesystem
sandbox remains required before production exposure.

The Kubernetes overlay runs Gateway as the stable `harness-gateway-0` StatefulSet Pod and
creates a dedicated ServiceAccount whose RBAC can only read and port-forward that exact Pod. The
provisioning script writes its root-owned, group-readable kubeconfig to a non-home path and
creates the required root-owned `port-forward.env`:

```text
KUBECONFIG=/etc/harness-gateway-port-forward/kubeconfig
HARNESS_KUBE_CONTEXT=harness-dev-port-forward
```

The port-forward service uses a private runtime directory as `HOME`; do not point it at
`/root/.kube/config`, which is intentionally hidden by `ProtectHome=true`.

The Kubernetes Secret backing this development-only credential is intentionally long-lived so
the supervised bridge survives restarts. Rotate it by deleting
`secret/harness-aba-port-forward-token`, reapplying `20-aba-port-forward-rbac.yaml`, rerunning the
provisioning script, and restarting `harness-gateway-port-forward.service`.

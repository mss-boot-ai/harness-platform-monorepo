# Development deployment on 167.17.68.242

This overlay deploys a single-Gateway Harness Platform development environment:

```text
platform-dev.mss-boot-io.top -> Admin Web + Platform API
hc-dev.mss-boot-io.top       -> HC Web + same-origin Platform API/Gateway
                                           |
                                           +-> TimescaleDB (PostgreSQL 16)
242 host ABA -> 127.0.0.1:18082 -> kubectl port-forward -> Gateway
```

The Gateway remains one replica because its active connection directory is process-local.
Its signing state is on a dedicated PVC. Losing that PVC changes the pinned trust root and
will make existing endpoints reject the Gateway. TimescaleDB is used as PostgreSQL storage;
the extension is installed and verified, while MVP tables remain ordinary tables because
their security uniqueness contracts are not compatible with time partitioning yet.
The database NetworkPolicy admits only the API, Gateway and migration Pods in this
namespace; effective enforcement is verified against the target CNI during rollout.

## Cluster inventory gate

Before changing the cluster, record the current Kubernetes context, node architecture,
default StorageClass, IngressClass, cert-manager API, image runtime and existing workload
names. This target profile requires the verified ingress-nginx controller. It renders the
IngressClass and ACME account email into a namespaced Let's Encrypt Issuer; neither is guessed
or embedded as a personal value in source control.

## Images

Build all four images from one exact pushed Git commit. Example image names are shown below;
use registry-qualified references for a multi-node cluster, or import the images into the
single node's Kubernetes container runtime before deployment.

```bash
harness_sha="$(git rev-parse HEAD)"
docker build -f platform/Dockerfile -t "harness-platform-api:${harness_sha}" platform
docker build -f platform/Dockerfile.web -t "harness-platform-web:${harness_sha}" platform
docker build -f platform/Dockerfile.gateway -t "harness-gateway:${harness_sha}" platform
docker build -f hc/Dockerfile -t "harness-hc-web:${harness_sha}" hc
```

For a single-node K3s installation backed by containerd, one supported import pattern is:

```bash
image_archive="$(mktemp /tmp/harness-images.XXXXXX.tar)"
docker save -o "${image_archive}" \
  "harness-platform-api:${harness_sha}" \
  "harness-platform-web:${harness_sha}" \
  "harness-gateway:${harness_sha}" \
  "harness-hc-web:${harness_sha}"
k3s ctr images import "${image_archive}"
rm -f "${image_archive}"
```

Use that pattern only after the inventory proves K3s/containerd is the active runtime.

## Secrets, migration and rollout

Run as an administrator on the target host. The bootstrap script creates random database and
Admin signing material, stores a recoverable copy under a root-only directory, and sends no
secret value to stdout. It never injects the initial Admin password into a long-running Pod.

```bash
export HARNESS_KUBE_CONTEXT='<inventory result>'
./deploy/kubernetes/dev-242/bootstrap-secrets.sh

export HARNESS_PLATFORM_IMAGE="harness-platform-api:${harness_sha}"
export HARNESS_PLATFORM_WEB_IMAGE="harness-platform-web:${harness_sha}"
export HARNESS_GATEWAY_IMAGE="harness-gateway:${harness_sha}"
export HARNESS_HC_IMAGE="harness-hc-web:${harness_sha}"
export HARNESS_SOURCE_SHA="${harness_sha}"
export HARNESS_STORAGE_CLASS='<inventory result>'
export HARNESS_INGRESS_CLASS='<inventory result>'
export HARNESS_ACME_EMAIL='<deployment account email>'
./deploy/kubernetes/dev-242/deploy.sh
```

`deploy.sh` waits for TimescaleDB, proves the `timescaledb` extension exists, runs the
idempotent Admin/Harness migration Job, deletes the cluster copy of the one-use bootstrap
password, and only then rolls out Platform, Gateway and both web clients. The root-only
recovery file remains at `${HARNESS_SECRET_STATE_DIR:-/var/lib/harness-deploy/dev-242}`. It
also waits for cert-manager to issue the shared two-host certificate before returning.
The script refuses to continue if a legacy `deployment/harness-gateway` exists, because it
must never run alongside the stable single-Pod Gateway StatefulSet.

After DNS and certificate issuance are ready:

```bash
./deploy/kubernetes/dev-242/verify.sh
```

Browser acceptance must be performed in the built-in browser. It must cover both public
origins, Admin login, HC registration, ABA enrollment approval, WSS READY and one encrypted
ACP prompt/response; health endpoints alone are not an MVP acceptance result.

## DNS

Create both records as DNS-only A records until HTTP-01 certificate issuance has completed:

```text
platform-dev.mss-boot-io.top  A  167.17.68.242
hc-dev.mss-boot-io.top        A  167.17.68.242
```

No public Gateway hostname is required. The browser reaches Gateway under the HC origin,
while the host-installed development ABA uses the loopback-only bridge on port 18082.

## ABA and DeepSeek gate

Do not point the development file KeyStore at a public hostname. On the 242 host, keep a
supervised `kubectl port-forward` bound only to `127.0.0.1:18082` and configure ABA with
`platform.url = "http://127.0.0.1:18082"`. This must exactly match
`HARNESS_GATEWAY_NATIVE_EXTERNAL_ORIGIN`.

Before adding the DeepSeek runtime profile, prove the discovered executable is a fresh-process
ACP stable-v1 NDJSON stdio Agent. It must support `initialize`, `session/new` and
`session/prompt`, keep stdout protocol-only, finish initialize/new within 10 seconds and a
prompt within 60 seconds, and use canonical non-symlink executable/workspace paths. An HTTP
API, daemon or ordinary chat CLI needs a separate ACP stdio adapter and cannot be declared
working without that adapter's real handshake test.

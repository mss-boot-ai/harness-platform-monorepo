#!/usr/bin/env bash
set -Eeuo pipefail

manifest_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
harness_namespace="harness-dev"
harness_context="${HARNESS_KUBE_CONTEXT:-}"
harness_kubectl="${KUBECTL:-kubectl}"
harness_platform_image="${HARNESS_PLATFORM_IMAGE:-}"
harness_platform_web_image="${HARNESS_PLATFORM_WEB_IMAGE:-}"
harness_gateway_image="${HARNESS_GATEWAY_IMAGE:-}"
harness_hc_image="${HARNESS_HC_IMAGE:-}"
harness_source_sha="${HARNESS_SOURCE_SHA:-}"
harness_storage_class="${HARNESS_STORAGE_CLASS:-}"
harness_ingress_class="${HARNESS_INGRESS_CLASS:-}"
harness_acme_email="${HARNESS_ACME_EMAIL:-}"
harness_public_ipv4="167.17.68.242"

for harness_command in "${harness_kubectl}" curl getent; do
  command -v "${harness_command}" >/dev/null 2>&1 || {
    echo "error: required command is unavailable: ${harness_command}" >&2
    exit 2
  }
done
if [[ ! "${harness_storage_class}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]]; then
  echo "error: HARNESS_STORAGE_CLASS must be the inventory-confirmed StorageClass" >&2
  exit 2
fi
if [[ ! "${harness_ingress_class}" =~ ^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$ ]]; then
  echo "error: HARNESS_INGRESS_CLASS must be the inventory-confirmed IngressClass" >&2
  exit 2
fi
if [[ "${harness_ingress_class}" != "nginx" ]]; then
  echo "error: the dev-242 TLS/timeout profile requires the verified nginx IngressClass" >&2
  exit 2
fi
if [[ ! "${harness_acme_email}" =~ ^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$ ]]; then
  echo "error: HARNESS_ACME_EMAIL must be a valid ACME account email" >&2
  exit 2
fi

harness_validate_image() {
  local harness_name="$1"
  local harness_value="$2"
  if [[ ! "${harness_value}" =~ ^[A-Za-z0-9][A-Za-z0-9._/@:-]+$ ]]; then
    echo "error: ${harness_name} must be an explicit container image reference" >&2
    exit 2
  fi
}

harness_validate_image HARNESS_PLATFORM_IMAGE "${harness_platform_image}"
harness_validate_image HARNESS_PLATFORM_WEB_IMAGE "${harness_platform_web_image}"
harness_validate_image HARNESS_GATEWAY_IMAGE "${harness_gateway_image}"
harness_validate_image HARNESS_HC_IMAGE "${harness_hc_image}"
if [[ ! "${harness_source_sha}" =~ ^[0-9a-f]{40}$ ]]; then
  echo "error: HARNESS_SOURCE_SHA must be the full pushed Git commit SHA" >&2
  exit 2
fi
for harness_image in \
  "${harness_platform_image}" \
  "${harness_platform_web_image}" \
  "${harness_gateway_image}" \
  "${harness_hc_image}"; do
  if [[ ! "${harness_image}" =~ :${harness_source_sha}(@sha256:[0-9a-f]{64})?$ ]]; then
    echo "error: every application image must use HARNESS_SOURCE_SHA as its immutable tag" >&2
    exit 2
  fi
done

harness_kube_args=()
if [[ -z "${harness_context}" ]]; then
  echo "error: HARNESS_KUBE_CONTEXT must be the inventory-confirmed target context" >&2
  exit 2
fi
harness_kube_args+=(--context "${harness_context}")

"${harness_kubectl}" "${harness_kube_args[@]}" get storageclass "${harness_storage_class}" >/dev/null
if [[ "$("${harness_kubectl}" "${harness_kube_args[@]}" get storageclass "${harness_storage_class}" -o jsonpath='{.provisioner}')" != "openebs.io/local" ]]; then
  echo "error: the dev-242 profile requires the inventory-confirmed OpenEBS LocalPV provisioner" >&2
  exit 2
fi
if [[ "$("${harness_kubectl}" "${harness_kube_args[@]}" get storageclass "${harness_storage_class}" -o jsonpath='{.volumeBindingMode}')" != "WaitForFirstConsumer" ]]; then
  echo "error: the dev-242 OpenEBS LocalPV StorageClass must use WaitForFirstConsumer" >&2
  exit 2
fi
"${harness_kubectl}" "${harness_kube_args[@]}" get ingressclass "${harness_ingress_class}" >/dev/null
if [[ "$("${harness_kubectl}" "${harness_kube_args[@]}" get ingressclass "${harness_ingress_class}" -o jsonpath='{.spec.controller}')" != "k8s.io/ingress-nginx" ]]; then
  echo "error: HARNESS_INGRESS_CLASS is not controlled by ingress-nginx" >&2
  exit 2
fi
if ! "${harness_kubectl}" "${harness_kube_args[@]}" api-resources --api-group=cert-manager.io -o name \
  | grep -Fxq issuers.cert-manager.io; then
  echo "error: cert-manager Issuer API is unavailable" >&2
  exit 2
fi

for harness_host in platform-dev.mss-boot-io.top hc-dev.mss-boot-io.top; do
  harness_addresses="$(getent ahostsv4 "${harness_host}" | awk '{print $1}' | LC_ALL=C sort -u)"
  if ! grep -Fxq "${harness_public_ipv4}" <<<"${harness_addresses}"; then
    echo "error: ${harness_host} does not resolve to ${harness_public_ipv4}" >&2
    exit 2
  fi
  curl --silent --show-error --connect-timeout 5 --max-time 15 \
    --output /dev/null "http://${harness_host}/"
done

for harness_secret in harness-database harness-platform-runtime; do
  "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    get secret "${harness_secret}" >/dev/null
done
if "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  get deployment harness-gateway >/dev/null 2>&1; then
  echo "error: legacy deployment/harness-gateway must be removed before the StatefulSet rollout" >&2
  exit 2
fi
harness_timescaledb_rendered=""
harness_apps_rendered=""
harness_ingress_rendered=""
harness_issuer_rendered=""
harness_migration_rendered=""
harness_migration_preflight_rendered=""
harness_configmap_rendered=""
harness_cleanup_rendered() {
  if [[ -n "${harness_apps_rendered}" ]]; then
    unlink "${harness_apps_rendered}" 2>/dev/null || true
  fi
  if [[ -n "${harness_timescaledb_rendered}" ]]; then
    unlink "${harness_timescaledb_rendered}" 2>/dev/null || true
  fi
  if [[ -n "${harness_ingress_rendered}" ]]; then
    unlink "${harness_ingress_rendered}" 2>/dev/null || true
  fi
  if [[ -n "${harness_issuer_rendered}" ]]; then
    unlink "${harness_issuer_rendered}" 2>/dev/null || true
  fi
  if [[ -n "${harness_migration_rendered}" ]]; then
    unlink "${harness_migration_rendered}" 2>/dev/null || true
  fi
  if [[ -n "${harness_migration_preflight_rendered}" ]]; then
    unlink "${harness_migration_preflight_rendered}" 2>/dev/null || true
  fi
  if [[ -n "${harness_configmap_rendered}" ]]; then
    unlink "${harness_configmap_rendered}" 2>/dev/null || true
  fi
}
trap harness_cleanup_rendered EXIT

harness_timescaledb_rendered="$(mktemp /tmp/harness-timescaledb.XXXXXX.yaml)"
sed "s|storageClassName: HARNESS_STORAGE_CLASS$|storageClassName: ${harness_storage_class}|" \
  "${manifest_dir}/10-timescaledb.yaml" >"${harness_timescaledb_rendered}"
if grep -Fq 'HARNESS_STORAGE_CLASS' "${harness_timescaledb_rendered}"; then
  echo "error: TimescaleDB StorageClass placeholder was not rendered" >&2
  exit 2
fi
harness_issuer_rendered="$(mktemp /tmp/harness-issuer.XXXXXX.yaml)"
sed \
  -e "s|email: HARNESS_ACME_EMAIL$|email: ${harness_acme_email}|" \
  -e "s|ingressClassName: HARNESS_INGRESS_CLASS$|ingressClassName: ${harness_ingress_class}|" \
  "${manifest_dir}/05-issuer.yaml" >"${harness_issuer_rendered}"
if grep -Eq 'HARNESS_(ACME_EMAIL|INGRESS_CLASS)' "${harness_issuer_rendered}"; then
  echo "error: ACME Issuer placeholders were not rendered" >&2
  exit 2
fi

harness_apps_rendered="$(mktemp /tmp/harness-apps.XXXXXX.yaml)"
for harness_placeholder in \
  harness-platform-api:dev \
  harness-platform-web:dev \
  harness-gateway:dev \
  harness-hc-web:dev; do
  if [[ "$(grep -Fc "image: ${harness_placeholder}" "${manifest_dir}/40-apps.yaml")" != "1" ]]; then
    echo "error: expected exactly one ${harness_placeholder} image placeholder" >&2
    exit 2
  fi
done
sed \
  -e "s|image: harness-platform-api:dev$|image: ${harness_platform_image}|" \
  -e "s|image: harness-platform-web:dev$|image: ${harness_platform_web_image}|" \
  -e "s|image: harness-gateway:dev$|image: ${harness_gateway_image}|" \
  -e "s|image: harness-hc-web:dev$|image: ${harness_hc_image}|" \
  -e "s|storageClassName: HARNESS_STORAGE_CLASS$|storageClassName: ${harness_storage_class}|" \
  "${manifest_dir}/40-apps.yaml" >"${harness_apps_rendered}"
if grep -Eq 'image: harness-(platform-api|platform-web|gateway|hc-web):dev$|HARNESS_STORAGE_CLASS' "${harness_apps_rendered}"; then
  echo "error: one or more application placeholders were not rendered" >&2
  exit 2
fi

harness_ingress_rendered="$(mktemp /tmp/harness-ingress.XXXXXX.yaml)"
sed "s|ingressClassName: HARNESS_INGRESS_CLASS$|ingressClassName: ${harness_ingress_class}|" \
  "${manifest_dir}/50-ingress.yaml" >"${harness_ingress_rendered}"
if grep -Fq 'HARNESS_INGRESS_CLASS' "${harness_ingress_rendered}"; then
  echo "error: IngressClass placeholder was not rendered" >&2
  exit 2
fi

harness_migration_rendered="$(mktemp /tmp/harness-migration.XXXXXX.yaml)"
"${harness_kubectl}" "${harness_kube_args[@]}" set image \
  -f "${manifest_dir}/30-platform-migrate.yaml" \
  platform-migrate="${harness_platform_image}" --local -o yaml >"${harness_migration_rendered}"
harness_migration_preflight_rendered="$(mktemp /tmp/harness-migration-preflight.XXXXXX.yaml)"
sed 's|name: harness-platform-migrate$|name: harness-platform-migrate-preflight|' \
  "${harness_migration_rendered}" >"${harness_migration_preflight_rendered}"
harness_configmap_rendered="$(mktemp /tmp/harness-configmap.XXXXXX.yaml)"
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  create configmap harness-platform-config \
  --from-file=application.yml="${manifest_dir}/application.yml" \
  --dry-run=client -o yaml >"${harness_configmap_rendered}"

"${harness_kubectl}" "${harness_kube_args[@]}" apply --dry-run=server \
  -f "${manifest_dir}/00-namespace.yaml" \
  -f "${harness_issuer_rendered}" \
  -f "${harness_timescaledb_rendered}" \
  -f "${manifest_dir}/15-network-policy.yaml" \
  -f "${manifest_dir}/20-aba-port-forward-rbac.yaml" \
  -f "${harness_configmap_rendered}" \
  -f "${harness_migration_preflight_rendered}" \
  -f "${harness_apps_rendered}" \
  -f "${harness_ingress_rendered}" >/dev/null

"${harness_kubectl}" "${harness_kube_args[@]}" apply \
  -f "${manifest_dir}/00-namespace.yaml" \
  -f "${harness_issuer_rendered}" \
  -f "${harness_timescaledb_rendered}" \
  -f "${manifest_dir}/15-network-policy.yaml" \
  -f "${manifest_dir}/20-aba-port-forward-rbac.yaml"
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  wait --for=condition=Ready issuer/harness-dev-letsencrypt-production --timeout=3m
"${harness_kubectl}" "${harness_kube_args[@]}" apply -f "${harness_configmap_rendered}"
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  rollout status statefulset/harness-timescaledb --timeout=10m

"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec statefulset/harness-timescaledb -- \
  psql -U harness -d harness -v ON_ERROR_STOP=1 -c \
  "CREATE EXTENSION IF NOT EXISTS timescaledb" >/dev/null
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec statefulset/harness-timescaledb -- \
  psql -U harness -d harness -v ON_ERROR_STOP=1 -Atc \
  "SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'" >/dev/null

"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  delete job harness-platform-migrate --ignore-not-found --wait=true
"${harness_kubectl}" "${harness_kube_args[@]}" apply -f "${harness_migration_rendered}"
if ! "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  wait --for=condition=complete job/harness-platform-migrate --timeout=10m; then
  "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    logs job/harness-platform-migrate --tail=200 || true
  exit 1
fi

"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  delete secret harness-platform-bootstrap --ignore-not-found

"${harness_kubectl}" "${harness_kube_args[@]}" apply -f "${harness_apps_rendered}"

"${harness_kubectl}" "${harness_kube_args[@]}" apply -f "${harness_ingress_rendered}"

for harness_deployment in harness-platform-api harness-platform-web harness-hc-web; do
  "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    rollout status "deployment/${harness_deployment}" --timeout=10m
done
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  rollout status statefulset/harness-gateway --timeout=10m

"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  wait --for=create certificate/harness-dev-mss-boot-io-tls --timeout=2m
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  wait --for=condition=Ready certificate/harness-dev-mss-boot-io-tls --timeout=10m

"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec deployment/harness-hc-web -- wget -qO- http://harness-platform-api:8080/healthz >/dev/null
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec deployment/harness-hc-web -- wget -qO- http://harness-gateway:8082/gateway/v1/ready >/dev/null

echo "Harness development workloads and the public TLS certificate are ready."

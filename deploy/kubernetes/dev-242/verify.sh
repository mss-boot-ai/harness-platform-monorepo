#!/usr/bin/env bash
set -Eeuo pipefail

harness_namespace="harness-dev"
harness_context="${HARNESS_KUBE_CONTEXT:-}"
harness_kubectl="${KUBECTL:-kubectl}"
harness_kube_args=()
command -v "${harness_kubectl}" >/dev/null 2>&1 || {
  echo "error: kubectl is unavailable: ${harness_kubectl}" >&2
  exit 2
}
command -v curl >/dev/null 2>&1 || {
  echo "error: curl is unavailable" >&2
  exit 2
}
if [[ -z "${harness_context}" ]]; then
  echo "error: HARNESS_KUBE_CONTEXT must be the inventory-confirmed target context" >&2
  exit 2
fi
harness_kube_args+=(--context "${harness_context}")

"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" get \
  statefulset,deployment,pod,service,ingress,pvc
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec statefulset/harness-timescaledb -- \
  psql -U harness -d harness -v ON_ERROR_STOP=1 -Atc \
  "SELECT current_database(), extversion FROM pg_extension WHERE extname = 'timescaledb'"
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec deployment/harness-hc-web -- wget -qO- http://harness-platform-api:8080/readyz
"${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
  exec deployment/harness-hc-web -- wget -qO- http://harness-gateway:8082/gateway/v1/ready

for harness_url in \
  https://platform-dev.mss-boot-io.top/ \
  https://hc-dev.mss-boot-io.top/ \
  https://hc-dev.mss-boot-io.top/gateway/v1/ready; do
  curl --fail --show-error --silent --location --max-time 15 "${harness_url}" >/dev/null
  echo "ok ${harness_url}"
done

for harness_host in platform-dev.mss-boot-io.top hc-dev.mss-boot-io.top; do
  harness_redirect="$(curl --show-error --silent --max-time 15 --output /dev/null \
    --write-out '%{http_code} %{redirect_url}' "http://${harness_host}/")"
  [[ "${harness_redirect}" =~ ^30(1|2|7|8)\ https://${harness_host}/ ]] || {
    echo "error: ${harness_host} does not redirect HTTP to its exact HTTPS origin" >&2
    exit 1
  }
done

harness_platform_headers="$(curl --fail --show-error --silent --head --max-time 15 https://platform-dev.mss-boot-io.top/)"
harness_hc_headers="$(curl --fail --show-error --silent --head --max-time 15 https://hc-dev.mss-boot-io.top/)"
grep -Fiq 'content-security-policy:' <<<"${harness_platform_headers}"
grep -Fiq 'strict-transport-security:' <<<"${harness_platform_headers}"
grep -Fiq "require-trusted-types-for 'script'" <<<"${harness_hc_headers}"
grep -Fiq 'strict-transport-security:' <<<"${harness_hc_headers}"

#!/usr/bin/env bash
set -Eeuo pipefail

harness_namespace="harness-dev"
harness_service_account="harness-aba-port-forward"
harness_token_secret="harness-aba-port-forward-token"
harness_admin_context="${HARNESS_KUBE_CONTEXT:-}"
harness_kubectl="${KUBECTL:-kubectl}"
harness_config_dir="${HARNESS_PORT_FORWARD_CONFIG_DIR:-/etc/harness-gateway-port-forward}"
harness_aba_config_dir="${HARNESS_ABA_CONFIG_DIR:-/etc/harness-aba}"
harness_kubeconfig="${harness_config_dir}/kubeconfig"
harness_environment_file="${harness_aba_config_dir}/port-forward.env"
harness_runtime_context="harness-dev-port-forward"
harness_port_forward_group="${HARNESS_PORT_FORWARD_GROUP:-harness-port-forward}"

command -v "${harness_kubectl}" >/dev/null 2>&1 || {
  echo "error: kubectl is unavailable: ${harness_kubectl}" >&2
  exit 2
}
command -v base64 >/dev/null 2>&1 || {
  echo "error: base64 is unavailable" >&2
  exit 2
}
getent group "${harness_port_forward_group}" >/dev/null 2>&1 || {
  echo "error: port-forward system group is unavailable: ${harness_port_forward_group}" >&2
  exit 2
}
if [[ -z "${harness_admin_context}" ]]; then
  echo "error: HARNESS_KUBE_CONTEXT must be the inventory-confirmed administrator context" >&2
  exit 2
fi
for harness_directory in "${harness_config_dir}" "${harness_aba_config_dir}"; do
  if [[ -L "${harness_directory}" ]]; then
    echo "error: configuration directory must not be a symlink: ${harness_directory}" >&2
    exit 1
  fi
done
if [[ ! -d "${harness_aba_config_dir}" ]]; then
  echo "error: create the ABA configuration directory before provisioning its port-forward environment" >&2
  exit 1
fi

install -d -o root -g "${harness_port_forward_group}" -m 0750 "${harness_config_dir}"
umask 077

harness_server="$("${harness_kubectl}" --context "${harness_admin_context}" config view \
  --raw --minify -o jsonpath='{.clusters[0].cluster.server}')"
if [[ ! "${harness_server}" =~ ^https://[^[:space:]]+$ ]]; then
  echo "error: the selected context has an invalid Kubernetes API server" >&2
  exit 1
fi

harness_token_base64=""
harness_ca_base64=""
for _ in {1..60}; do
  harness_token_base64="$("${harness_kubectl}" --context "${harness_admin_context}" \
    -n "${harness_namespace}" get secret "${harness_token_secret}" \
    -o jsonpath='{.data.token}' 2>/dev/null || true)"
  harness_ca_base64="$("${harness_kubectl}" --context "${harness_admin_context}" \
    -n "${harness_namespace}" get secret "${harness_token_secret}" \
    -o jsonpath='{.data.ca\.crt}' 2>/dev/null || true)"
  if [[ -n "${harness_token_base64}" && -n "${harness_ca_base64}" ]]; then
    break
  fi
  sleep 1
done
printf '%s' "${harness_ca_base64}" | base64 --decode >/dev/null 2>&1 || {
  echo "error: the ServiceAccount token Secret has no valid cluster CA" >&2
  exit 1
}
harness_token="$(printf '%s' "${harness_token_base64}" | base64 --decode)"
if [[ ! "${harness_token}" =~ ^[A-Za-z0-9._-]+$ ]]; then
  echo "error: the ServiceAccount token Secret was not populated safely" >&2
  exit 1
fi

harness_kubeconfig_temporary="$(mktemp "${harness_config_dir}/.kubeconfig.XXXXXX")"
harness_environment_temporary="$(mktemp "${harness_aba_config_dir}/.port-forward.XXXXXX")"
harness_cleanup() {
  unlink "${harness_kubeconfig_temporary}" "${harness_environment_temporary}" 2>/dev/null || true
}
trap harness_cleanup EXIT
chmod 0600 "${harness_kubeconfig_temporary}" "${harness_environment_temporary}"
printf '%s\n' \
  'apiVersion: v1' \
  'kind: Config' \
  'clusters:' \
  '- name: harness-dev' \
  '  cluster:' \
  "    certificate-authority-data: ${harness_ca_base64}" \
  "    server: ${harness_server}" \
  'contexts:' \
  "- name: ${harness_runtime_context}" \
  '  context:' \
  '    cluster: harness-dev' \
  "    namespace: ${harness_namespace}" \
  "    user: ${harness_service_account}" \
  "current-context: ${harness_runtime_context}" \
  'users:' \
  "- name: ${harness_service_account}" \
  '  user:' \
  "    token: ${harness_token}" \
  >"${harness_kubeconfig_temporary}"
printf '%s\n' \
  "KUBECONFIG=${harness_kubeconfig}" \
  "HARNESS_KUBE_CONTEXT=${harness_runtime_context}" \
  >"${harness_environment_temporary}"

KUBECONFIG="${harness_kubeconfig_temporary}" "${harness_kubectl}" \
  --context "${harness_runtime_context}" -n "${harness_namespace}" \
  get pod harness-gateway-0 >/dev/null
chown root:"${harness_port_forward_group}" "${harness_kubeconfig_temporary}"
chmod 0640 "${harness_kubeconfig_temporary}"
mv -fT "${harness_kubeconfig_temporary}" "${harness_kubeconfig}"
mv -fT "${harness_environment_temporary}" "${harness_environment_file}"
trap - EXIT

echo "Least-privilege ABA port-forward kubeconfig provisioned without printing its token."

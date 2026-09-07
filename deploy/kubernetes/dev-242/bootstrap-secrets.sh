#!/usr/bin/env bash
set -Eeuo pipefail

manifest_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
harness_context="${HARNESS_KUBE_CONTEXT:-}"
harness_state_dir="${HARNESS_SECRET_STATE_DIR:-/var/lib/harness-deploy/dev-242}"
harness_namespace="harness-dev"
harness_kubectl="${KUBECTL:-kubectl}"

command -v "${harness_kubectl}" >/dev/null 2>&1 || {
  echo "error: kubectl is unavailable: ${harness_kubectl}" >&2
  exit 2
}
command -v openssl >/dev/null 2>&1 || {
  echo "error: openssl is unavailable" >&2
  exit 2
}

harness_kube_args=()
if [[ -z "${harness_context}" ]]; then
  echo "error: HARNESS_KUBE_CONTEXT must be the inventory-confirmed target context" >&2
  exit 2
fi
harness_kube_args+=(--context "${harness_context}")

"${harness_kubectl}" "${harness_kube_args[@]}" apply -f "${manifest_dir}/00-namespace.yaml"
if [[ -L "${harness_state_dir}" ]]; then
  echo "error: secret state directory must not be a symlink" >&2
  exit 1
fi
install -d -m 0700 "${harness_state_dir}"
if [[ "$(stat -c '%u' "${harness_state_dir}")" != "$(id -u)" || \
  "$(stat -c '%a' "${harness_state_dir}")" != "700" ]]; then
  echo "error: secret state directory owner or mode is unsafe" >&2
  exit 1
fi
umask 077

harness_require_recoverable_state() {
  local harness_secret="$1"
  local harness_file="$2"
  if [[ -f "${harness_file}" ]]; then
    return 0
  fi
  if "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    get secret "${harness_secret}" >/dev/null 2>&1; then
    echo "error: ${harness_secret} exists but its root-only source ${harness_file} is missing" >&2
    echo "error: refusing to rotate live credentials implicitly" >&2
    exit 1
  fi
}

harness_random() {
  openssl rand -base64 48 | tr '/+' '_-' | tr -d '=\n'
}

harness_write_state() {
  local harness_target="$1"
  shift
  local harness_temporary
  harness_temporary="$(mktemp "${harness_state_dir}/.state.XXXXXX")"
  chmod 0600 "${harness_temporary}"
  printf '%s\n' "$@" >"${harness_temporary}"
  if ! ln "${harness_temporary}" "${harness_target}"; then
    unlink "${harness_temporary}" 2>/dev/null || true
    echo "error: secret state appeared concurrently: ${harness_target}" >&2
    exit 1
  fi
  unlink "${harness_temporary}"
  sync -f "${harness_target}"
}

harness_validate_state() {
  local harness_file="$1"
  shift
  if [[ ! -f "${harness_file}" || -L "${harness_file}" ]]; then
    echo "error: secret state must be a regular non-symlink file: ${harness_file}" >&2
    exit 1
  fi
  if [[ "$(stat -c '%u' "${harness_file}")" != "$(id -u)" || \
    "$(stat -c '%a' "${harness_file}")" != "600" ]]; then
    echo "error: secret state owner or mode is unsafe: ${harness_file}" >&2
    exit 1
  fi
  if [[ ! -s "${harness_file}" || "$(tail -c 1 "${harness_file}" | wc -l)" != "1" ]]; then
    echo "error: secret state is empty or lacks an atomic final newline: ${harness_file}" >&2
    exit 1
  fi
  local harness_line
  while IFS= read -r harness_line || [[ -n "${harness_line}" ]]; do
    [[ "${harness_line}" =~ ^[A-Z][A-Z0-9_]*=.+$ ]] || {
      echo "error: secret state has an invalid or empty entry: ${harness_file}" >&2
      exit 1
    }
  done <"${harness_file}"
  local harness_actual_keys harness_expected_keys
  harness_actual_keys="$(cut -d= -f1 "${harness_file}" | LC_ALL=C sort | tr '\n' ' ')"
  harness_expected_keys="$(printf '%s\n' "$@" | LC_ALL=C sort | tr '\n' ' ')"
  if [[ "${harness_actual_keys}" != "${harness_expected_keys}" ]]; then
    echo "error: secret state key set is invalid: ${harness_file}" >&2
    exit 1
  fi
}

harness_database_state="${harness_state_dir}/database.env"
harness_runtime_state="${harness_state_dir}/platform-runtime.env"
harness_bootstrap_state="${harness_state_dir}/admin-bootstrap.env"
harness_require_recoverable_state harness-database "${harness_database_state}"
harness_require_recoverable_state harness-platform-runtime "${harness_runtime_state}"
harness_require_recoverable_state harness-platform-bootstrap "${harness_bootstrap_state}"
if [[ ! -f "${harness_database_state}" ]] && \
  "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    get pvc data-harness-timescaledb-0 >/dev/null 2>&1; then
  echo "error: the TimescaleDB PVC exists but recoverable database credentials are missing" >&2
  exit 1
fi
if [[ ! -f "${harness_bootstrap_state}" ]] && \
  "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    get pvc data-harness-timescaledb-0 >/dev/null 2>&1; then
  echo "error: the TimescaleDB PVC exists but the recoverable Admin bootstrap password is missing" >&2
  echo "error: refusing to create a replacement password that may not match the existing Admin" >&2
  exit 1
fi

if [[ ! -f "${harness_database_state}" ]]; then
  harness_database_password="$(harness_random)"
  harness_write_state "${harness_database_state}" \
    "POSTGRES_PASSWORD=${harness_database_password}" \
    "HARNESS_DATABASE_DSN=host=harness-timescaledb port=5432 user=harness dbname=harness password=${harness_database_password} sslmode=disable TimeZone=UTC"
fi

if [[ ! -f "${harness_runtime_state}" ]]; then
  harness_write_state "${harness_runtime_state}" \
    "HARNESS_ADMIN_AUTH_KEY=$(harness_random)" \
    "HARNESS_ADMIN_IDENTITY_KEY=$(harness_random)"
fi

if [[ ! -f "${harness_bootstrap_state}" ]]; then
  harness_write_state "${harness_bootstrap_state}" \
    "MSS_ADMIN_INITIAL_PASSWORD=A1$(harness_random)"
fi

harness_validate_state "${harness_database_state}" HARNESS_DATABASE_DSN POSTGRES_PASSWORD
harness_validate_state "${harness_runtime_state}" HARNESS_ADMIN_AUTH_KEY HARNESS_ADMIN_IDENTITY_KEY
harness_validate_state "${harness_bootstrap_state}" MSS_ADMIN_INITIAL_PASSWORD

harness_apply_secret() {
  local harness_name="$1"
  local harness_file="$2"
  "${harness_kubectl}" "${harness_kube_args[@]}" -n "${harness_namespace}" \
    create secret generic "${harness_name}" \
    --from-env-file="${harness_file}" \
    --dry-run=client -o yaml |
    "${harness_kubectl}" "${harness_kube_args[@]}" apply -f - >/dev/null
}

harness_apply_secret harness-database "${harness_database_state}"
harness_apply_secret harness-platform-runtime "${harness_runtime_state}"
harness_apply_secret harness-platform-bootstrap "${harness_bootstrap_state}"

echo "Harness development secrets are provisioned in namespace ${harness_namespace}."
echo "The recoverable root-only source is ${harness_state_dir}; no secret value was printed."

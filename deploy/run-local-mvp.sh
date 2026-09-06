#!/usr/bin/env bash
set -Eeuo pipefail

harness_repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
harness_aba_config="${HARNESS_ABA_CONFIG:-${harness_repo_root}/aba/.aba-dev/local.toml}"
harness_aba_store="${HARNESS_ABA_STORE:-${harness_repo_root}/aba/.aba-dev/identity.json}"
harness_pids=()

for harness_command in go cargo node corepack curl setsid; do
  command -v "${harness_command}" >/dev/null 2>&1 || {
    echo "error: required command is unavailable: ${harness_command}" >&2
    exit 2
  }
done

harness_node_major="$(node -p 'process.versions.node.split(".")[0]')"
[[ "${harness_node_major}" == "24" ]] || {
  echo "error: Node 24 is required (found Node ${harness_node_major})" >&2
  exit 2
}

[[ -f "${harness_repo_root}/platform/mss-boot-admin-local.db" ]] || {
  echo "error: initialize Platform first with 'cd platform && mss setup'" >&2
  exit 2
}
[[ -f "${harness_aba_config}" && -f "${harness_aba_store}" ]] || {
  echo "error: ABA local config/identity is unavailable; follow deploy/README.md" >&2
  exit 2
}

for harness_url in \
  http://127.0.0.1:8080/healthz \
  http://127.0.0.1:8082/gateway/v1/health \
  http://127.0.0.1:8001/; do
  if curl --connect-timeout 0.2 --fail --silent "${harness_url}" >/dev/null 2>&1; then
    echo "error: a local MVP service is already using ${harness_url}" >&2
    exit 2
  fi
done

harness_cleanup() {
  local harness_pid
  for harness_pid in "${harness_pids[@]}"; do
    kill -- "-${harness_pid}" 2>/dev/null || true
  done
  for harness_pid in "${harness_pids[@]}"; do
    wait "${harness_pid}" 2>/dev/null || true
  done
}
trap harness_cleanup EXIT INT TERM

setsid bash -c 'cd "$1" && exec env CONFIG_PROVIDER=fs GOWORK=off go run ./cmd/server server' \
  harness-platform-backend "${harness_repo_root}/platform" &
harness_pids+=("$!")

setsid bash -c 'cd "$1" && exec env \
  HARNESS_GATEWAY_ALLOWED_ORIGIN=http://localhost:8001 \
  HARNESS_GATEWAY_EXTERNAL_ORIGIN=http://localhost:8001 \
  HARNESS_GATEWAY_NATIVE_EXTERNAL_ORIGIN=http://127.0.0.1:8082 \
  go run ./cmd/harness-gateway' harness-platform-gateway "${harness_repo_root}/platform" &
harness_pids+=("$!")

setsid bash -c 'cd "$1" && exec corepack pnpm@10.34.5 --filter @harness/hc-web exec vite --host 127.0.0.1 --port 8001' \
  harness-hc-web "${harness_repo_root}/hc" &
harness_pids+=("$!")

harness_wait_ready() {
  local harness_url="$1"
  local harness_attempt
  for harness_attempt in {1..120}; do
    if curl --fail --silent --show-error "${harness_url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "error: local service did not become ready: ${harness_url}" >&2
  return 1
}

harness_wait_ready http://127.0.0.1:8080/healthz
harness_wait_ready http://127.0.0.1:8082/gateway/v1/health
harness_wait_ready http://127.0.0.1:8001/

setsid bash -c 'cd "$1" && exec cargo run --locked --bin aba -- run \
  --config "$2" --store "$3" --insecure-dev-keystore' \
  harness-aba "${harness_repo_root}/aba" "${harness_aba_config}" "${harness_aba_store}" &
harness_pids+=("$!")

echo "Harness MVP local services are ready: http://localhost:8001/"
echo "Press Ctrl-C to stop all four processes."
wait -n "${harness_pids[@]}"

#!/usr/bin/env bash
set -Eeuo pipefail

readonly UPSTREAM_TAG="v1.3.7"
readonly TAG_OBJECT_SHA="41c6517950f7f5f642418f5d4a49386e9c200b15"
readonly SOURCE_COMMIT_SHA="77b53d41092741eac62fa6418c0bdbf87413c7cd"
readonly SOURCE_TREE_SHA="32ffcde4e8ca2f41a8c15c373217d06c1b35d9c0"
readonly GO_MOD_BLOB="0395953a2915b452d366bfe8e0b777d084eb3694"
readonly LICENSE_BLOB="62ff2994787f18495f68803bed2b87d69c4449ca"
readonly AUTH_BLOB="cd5a6ed0d9ef41dff92651fa5b578c031b57d55c"
readonly WS_API_BLOB="1095302d91dee10fcefaabbedda46be285fc7255"
readonly TASK_SERVER_BLOB="3504aa49dc3abb15963111347d8c374a9a0f9ff1"

mode="${1:---lock-only}"
case "${mode}" in
  --lock-only|--source) ;;
  *)
    echo "usage: $0 [--lock-only|--source]" >&2
    exit 2
    ;;
esac

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"
lock_file="platform/.upstream/mss-boot-admin.lock.yaml"

[[ -f "${lock_file}" ]] || { echo "error: missing ${lock_file}" >&2; exit 3; }
for expected in \
  "  tag: ${UPSTREAM_TAG}" \
  "  tag_object_sha: ${TAG_OBJECT_SHA}" \
  "  source_commit_sha: ${SOURCE_COMMIT_SHA}" \
  "  source_tree_sha: ${SOURCE_TREE_SHA}" \
  "  go_version: 1.26.6" \
  "  license: MIT"; do
  grep -Fqx "${expected}" "${lock_file}" || {
    echo "error: lock mismatch: ${expected}" >&2
    exit 4
  }
done

if [[ "${mode}" == "--lock-only" ]]; then
  echo "Platform upstream lock is internally consistent."
  exit 0
fi

check_blob() {
  local path="$1"
  local expected="$2"
  [[ -f "${path}" ]] || { echo "error: missing imported file ${path}" >&2; exit 5; }
  local actual
  actual="$(git hash-object "${path}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "error: imported source differs at ${path}: expected ${expected}, got ${actual}" >&2
    exit 6
  fi
}

check_blob "platform/go.mod" "${GO_MOD_BLOB}"
check_blob "platform/LICENSE" "${LICENSE_BLOB}"
check_blob "platform/admin/middleware/auth.go" "${AUTH_BLOB}"
check_blob "platform/admin/apis/ws.go" "${WS_API_BLOB}"
check_blob "platform/mss-boot/core/server/task/server.go" "${TASK_SERVER_BLOB}"

if ! grep -Fqx "go 1.26.6" platform/go.mod; then
  echo "error: platform/go.mod does not declare Go 1.26.6" >&2
  exit 7
fi
if ! grep -Fq "MIT License" platform/LICENSE; then
  echo "error: imported Platform license is not the expected MIT text" >&2
  exit 7
fi

manifest="platform/.upstream/import-manifest.txt"
[[ -s "${manifest}" ]] || { echo "error: missing non-empty import manifest" >&2; exit 8; }

# Rebuild a tree from the imported source without .upstream to prove the initial import.
tmp_index="$(mktemp)"
rm -f "${tmp_index}"
cleanup() { rm -f "${tmp_index}"; }
trap cleanup EXIT

while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  GIT_INDEX_FILE="${tmp_index}" git update-index --add --cacheinfo \
    "$(git ls-files -s -- "platform/${path}" | awk 'NR == 1 {print $1}')" \
    "$(git hash-object "platform/${path}")" \
    "${path}" >/dev/null
done < <(find platform -type f ! -path 'platform/.upstream/*' -printf '%P\n' | LC_ALL=C sort)

actual_tree="$(GIT_INDEX_FILE="${tmp_index}" git write-tree)"
if [[ "${actual_tree}" != "${SOURCE_TREE_SHA}" ]]; then
  echo "error: imported Platform tree differs from the locked source tree" >&2
  echo "expected: ${SOURCE_TREE_SHA}" >&2
  echo "actual:   ${actual_tree}" >&2
  exit 9
fi

echo "Platform source exactly matches ${UPSTREAM_TAG} (${SOURCE_COMMIT_SHA})."

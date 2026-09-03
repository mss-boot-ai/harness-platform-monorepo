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

command -v python3 >/dev/null 2>&1 || {
  echo "error: python3 is required for exact import-manifest verification" >&2
  exit 2
}

check_blob() {
  local path="$1"
  local expected="$2"
  [[ -f "${path}" ]] || { echo "error: missing imported file ${path}" >&2; exit 5; }
  local actual
  actual="$(git hash-object --no-filters "${path}")"
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

python3 - "${manifest}" <<'PY'
from __future__ import annotations

import hashlib
import os
import stat
import sys
from pathlib import Path

manifest_path = Path(sys.argv[1])
platform_root = Path("platform")

expected: dict[str, tuple[str, str, str]] = {}
for line_number, raw_line in enumerate(manifest_path.read_text(encoding="utf-8").splitlines(), start=1):
    try:
        metadata, path = raw_line.split("\t", 1)
        mode, object_type, object_sha = metadata.split(" ", 2)
    except ValueError as exc:
        raise SystemExit(f"error: malformed import manifest line {line_number}: {exc}") from exc
    if not path or path.startswith("/") or ".." in Path(path).parts:
        raise SystemExit(f"error: unsafe path in import manifest line {line_number}: {path!r}")
    if object_type != "blob":
        raise SystemExit(
            f"error: unsupported upstream object type {object_type!r} at {path}; "
            "gitlinks require an explicit import design"
        )
    if path in expected:
        raise SystemExit(f"error: duplicate path in import manifest: {path}")
    expected[path] = (mode, object_type, object_sha)

actual_paths: set[str] = set()
for root, directory_names, file_names in os.walk(platform_root, topdown=True, followlinks=False):
    root_path = Path(root)
    if root_path == platform_root:
        directory_names[:] = [name for name in directory_names if name != ".upstream"]
    for name in file_names:
        path = root_path / name
        actual_paths.add(path.relative_to(platform_root).as_posix())
    # os.walk lists symlinked directories in directory_names. Treat each as a leaf blob.
    retained_directories: list[str] = []
    for name in directory_names:
        path = root_path / name
        if path.is_symlink():
            actual_paths.add(path.relative_to(platform_root).as_posix())
        else:
            retained_directories.append(name)
    directory_names[:] = retained_directories

expected_paths = set(expected)
missing = sorted(expected_paths - actual_paths)
extra = sorted(actual_paths - expected_paths)
if missing or extra:
    if missing:
        print("error: imported Platform is missing paths:", file=sys.stderr)
        for path in missing[:20]:
            print(f"  {path}", file=sys.stderr)
    if extra:
        print("error: imported Platform has unexpected paths:", file=sys.stderr)
        for path in extra[:20]:
            print(f"  {path}", file=sys.stderr)
    raise SystemExit(9)


def git_blob_sha(data: bytes) -> str:
    header = f"blob {len(data)}\0".encode("ascii")
    return hashlib.sha1(header + data).hexdigest()  # noqa: S324 - Git object identity is SHA-1 by design.


for relative_path, (expected_mode, _, expected_sha) in sorted(expected.items()):
    path = platform_root / relative_path
    file_stat = path.lstat()
    if stat.S_ISLNK(file_stat.st_mode):
        actual_mode = "120000"
        data = os.fsencode(os.readlink(path))
    elif stat.S_ISREG(file_stat.st_mode):
        actual_mode = "100755" if file_stat.st_mode & 0o111 else "100644"
        data = path.read_bytes()
    else:
        raise SystemExit(f"error: unsupported imported file type at {relative_path}")

    actual_sha = git_blob_sha(data)
    if actual_mode != expected_mode:
        raise SystemExit(
            f"error: mode mismatch at {relative_path}: expected {expected_mode}, got {actual_mode}"
        )
    if actual_sha != expected_sha:
        raise SystemExit(
            f"error: blob mismatch at {relative_path}: expected {expected_sha}, got {actual_sha}"
        )

print(f"Verified {len(expected)} imported paths against the locked manifest.")
PY

echo "Platform source exactly matches ${UPSTREAM_TAG} (${SOURCE_COMMIT_SHA}, tree ${SOURCE_TREE_SHA})."

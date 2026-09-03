#!/usr/bin/env bash
set -Eeuo pipefail

readonly UPSTREAM_URL="https://github.com/mss-boot-io/mss-boot-admin.git"
readonly UPSTREAM_TAG="v1.3.7"
readonly TAG_OBJECT_SHA="41c6517950f7f5f642418f5d4a49386e9c200b15"
readonly SOURCE_COMMIT_SHA="77b53d41092741eac62fa6418c0bdbf87413c7cd"
readonly SOURCE_TREE_SHA="32ffcde4e8ca2f41a8c15c373217d06c1b35d9c0"

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

if [[ -n "$(git status --porcelain)" ]]; then
  echo "error: the worktree must be clean before importing Platform upstream" >&2
  exit 2
fi

if [[ "$(git branch --show-current)" == "main" ]]; then
  echo "error: do not import Platform upstream directly on main" >&2
  exit 2
fi

lock_file="platform/.upstream/mss-boot-admin.lock.yaml"
for expected in \
  "  tag: ${UPSTREAM_TAG}" \
  "  tag_object_sha: ${TAG_OBJECT_SHA}" \
  "  source_commit_sha: ${SOURCE_COMMIT_SHA}" \
  "  source_tree_sha: ${SOURCE_TREE_SHA}"; do
  if ! grep -Fqx "${expected}" "${lock_file}"; then
    echo "error: lock file does not contain expected line: ${expected}" >&2
    exit 3
  fi
done

remote_tag_object="$(git ls-remote "${UPSTREAM_URL}" "refs/tags/${UPSTREAM_TAG}" | awk 'NR == 1 {print $1}')"
remote_peeled_commit="$(git ls-remote "${UPSTREAM_URL}" "refs/tags/${UPSTREAM_TAG}^{}" | awk 'NR == 1 {print $1}')"

if [[ "${remote_tag_object}" != "${TAG_OBJECT_SHA}" ]]; then
  echo "error: upstream tag object drift: expected ${TAG_OBJECT_SHA}, got ${remote_tag_object:-missing}" >&2
  exit 4
fi
if [[ "${remote_peeled_commit}" != "${SOURCE_COMMIT_SHA}" ]]; then
  echo "error: upstream peeled tag drift: expected ${SOURCE_COMMIT_SHA}, got ${remote_peeled_commit:-missing}" >&2
  exit 4
fi

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf "${tmp_dir}"
}
trap cleanup EXIT

git -C "${tmp_dir}" init -q
git -C "${tmp_dir}" remote add origin "${UPSTREAM_URL}"
git -C "${tmp_dir}" fetch -q --depth=1 origin "${SOURCE_COMMIT_SHA}"

fetched_commit="$(git -C "${tmp_dir}" rev-parse FETCH_HEAD^{commit})"
fetched_tree="$(git -C "${tmp_dir}" rev-parse FETCH_HEAD^{tree})"
if [[ "${fetched_commit}" != "${SOURCE_COMMIT_SHA}" || "${fetched_tree}" != "${SOURCE_TREE_SHA}" ]]; then
  echo "error: fetched source identity does not match the lock" >&2
  exit 5
fi

mkdir -p platform/.upstream
find platform -mindepth 1 -maxdepth 1 ! -name .upstream -exec rm -rf -- {} +
git -C "${tmp_dir}" archive --format=tar FETCH_HEAD | tar -xf - -C platform

manifest_tmp="${tmp_dir}/import-manifest.txt"
git -C "${tmp_dir}" ls-tree -r --full-tree "${SOURCE_TREE_SHA}" \
  | awk '{print $3 "  " substr($0, index($0, $4))}' \
  > "${manifest_tmp}"
mv "${manifest_tmp}" platform/.upstream/import-manifest.txt

./scripts/verify-platform-upstream.sh --source

echo "Imported mss-boot-admin ${UPSTREAM_TAG} (${SOURCE_COMMIT_SHA})."
echo "Review the tree, then commit it alone before any ACP modifications."

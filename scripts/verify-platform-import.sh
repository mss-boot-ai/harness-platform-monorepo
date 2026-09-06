#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
platform="${root}/platform"

required=(
  "${platform}/.mss/project.yaml"
  "${platform}/.mss/lock.yaml"
  "${platform}/.mss/blueprint-manifest.json"
  "${platform}/go.mod"
  "${platform}/cmd/server/main.go"
  "${platform}/internal/modules/registry.go"
  "${platform}/internal/modules/custom/modules.go"
  "${platform}/web/package.json"
  "${platform}/web/src/business/routes.config.ts"
  "${platform}/web/src/business/route-registrations.ts"
)

for path in "${required[@]}"; do
  test -s "${path}" || { echo "missing Thin Host file: ${path#${root}/}" >&2; exit 1; }
done

for forbidden in admin mss-boot templates; do
  if [[ -e "${platform}/${forbidden}" ]]; then
    echo "vendored Foundation path is forbidden: platform/${forbidden}" >&2
    exit 1
  fi
done

if ! grep -Eq 'github\.com/mss-boot-io/mss-boot-admin/admin[[:space:]]+v1\.3\.7' "${platform}/go.mod"; then
  echo "platform/go.mod must import Admin v1.3.7" >&2
  exit 1
fi
if grep -Eq '^[[:space:]]*replace[[:space:]]+.*mss-boot-admin|=>[[:space:]]*(\.\.?/|/)' "${platform}/go.mod"; then
  echo "local Foundation replace is forbidden" >&2
  exit 1
fi

python3 - "${platform}/web/package.json" <<'PY'
import json
import sys
from pathlib import Path

package = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
actual = package.get("dependencies", {}).get("@mss-boot-io/admin-web")
if actual != "1.3.7":
    raise SystemExit(f"Admin Web must be exactly 1.3.7, got {actual!r}")
PY

if ! grep -Eq 'kind:[[:space:]]*thin-host' "${platform}/.mss/project.yaml"; then
  echo "platform must declare repositoryLayout.kind: thin-host" >&2
  exit 1
fi

if grep -RIl --exclude-dir=.git --exclude='*.sum' --exclude='*.lock' \
  'package business defines the narrow, explicit extension boundary' "${platform}" | grep -q .; then
  echo "Foundation business source appears copied into Thin Host" >&2
  exit 1
fi

echo "Platform Thin Host import contract verified"

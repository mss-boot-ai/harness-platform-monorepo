#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

required_files=(
  AGENT.md
  AGENTS.md
  docs/README.md
  docs/product/PRD.md
  docs/architecture/ARCHITECTURE.md
  docs/architecture/SECURITY.md
  docs/architecture/PROTOCOL.md
  docs/architecture/PLATFORM.md
  docs/architecture/ABA.md
  docs/architecture/HC.md
  docs/roadmap/IMPLEMENTATION.md
  docs/roadmap/VERIFICATION.md
  docs/memory/project-memory.md
  docs/memory/decisions.md
  docs/memory/work-log.md
  docs/adr/README.md
  docs/references.md
)

for path in "${required_files[@]}"; do
  [[ -s "${path}" ]] || { echo "error: missing or empty required document: ${path}" >&2; exit 2; }
done

required_baselines=(
  "v1.3.7"
  "77b53d41092741eac62fa6418c0bdbf87413c7cd"
  "acp-brige-agent"
  "Opaque Mode"
  "148"
)
for value in "${required_baselines[@]}"; do
  grep -Fq "${value}" docs/memory/project-memory.md || {
    echo "error: project memory is missing baseline value: ${value}" >&2
    exit 3
  }
done

if command -v markdownlint-cli2 >/dev/null 2>&1; then
  markdownlint-cli2 "**/*.md"
else
  echo "notice: markdownlint-cli2 is unavailable; structural checks only" >&2
fi

if command -v lychee >/dev/null 2>&1; then
  lychee --no-progress --offline "**/*.md"
else
  echo "notice: lychee is unavailable; link checks skipped" >&2
fi

echo "Documentation structural checks passed."

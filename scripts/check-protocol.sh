#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

proto="protocol/proto/mss/awp/v1/wire.proto"
constants="protocol/constants/awp-v1.json"

[[ -s "${proto}" ]] || { echo "error: missing ${proto}" >&2; exit 2; }
[[ -s "${constants}" ]] || { echo "error: missing ${constants}" >&2; exit 2; }

python3 - "${constants}" <<'PY'
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))
assert data["wire"] == {
    "major": 1,
    "minor": 0,
    "websocket_subprotocol": "mss.awp.v1",
}
assert data["crypto"]["suite_id"] == 1
assert data["crypto"]["suite_name"] == "MSS-AWP-SUITE-0001"
assert data["crypto"]["aad_v1_length"] == 148
assert data["crypto"]["id_length"] == 16
assert data["crypto"]["nonce_length"] == 12
assert data["crypto"]["signature_length"] == 64
assert data["limits"]["max_wire_packet_bytes"] == 1_048_576
assert data["limits"]["max_ack_ranges"] == 32
assert data["limits"]["max_resume_cursors"] == 256
print("AWP v1 constants are internally consistent.")
PY

if grep -Eq '^[[:space:]]*(string|bytes|repeated[[:space:]]+string)[[:space:]]+(command|args|cwd|env)[[:space:]]*=' "${proto}"; then
  echo "error: AWP control schema must not expose remote command/args/cwd/env fields" >&2
  exit 3
fi

if ! command -v protoc >/dev/null 2>&1; then
  echo "error: protoc is required to validate ${proto}" >&2
  exit 4
fi

tmp_descriptor="$(mktemp)"
cleanup() { rm -f "${tmp_descriptor}"; }
trap cleanup EXIT

protoc \
  --proto_path=protocol/proto \
  --include_imports \
  --descriptor_set_out="${tmp_descriptor}" \
  "${proto}"

[[ -s "${tmp_descriptor}" ]] || {
  echo "error: protoc produced an empty descriptor set" >&2
  exit 5
}

echo "AWP v1 protobuf schema validation passed."

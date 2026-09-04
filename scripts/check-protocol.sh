#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

proto="protocol/proto/mss/awp/v1/wire.proto"
constants="protocol/constants/awp-v1.json"
suite_vector="protocol/testdata/v1/suite-0001-jwk-es256-dpop.json"
hpke_vector="protocol/testdata/v1/suite-0001-hpke-session.json"
wire_vector="protocol/testdata/v1/wire-server-challenge.json"
go_binding="platform/internal/harness/protocol/awpv1/wire.pb.go"
ts_binding="hc/packages/core/src/generated/mss/awp/v1/wire_pb.ts"

[[ -s "${proto}" ]] || { echo "error: missing ${proto}" >&2; exit 2; }
[[ -s "${constants}" ]] || { echo "error: missing ${constants}" >&2; exit 2; }
[[ -s "${suite_vector}" ]] || { echo "error: missing ${suite_vector}" >&2; exit 2; }
[[ -s "${hpke_vector}" ]] || { echo "error: missing ${hpke_vector}" >&2; exit 2; }
[[ -s "${wire_vector}" ]] || { echo "error: missing ${wire_vector}" >&2; exit 2; }
[[ -s "${go_binding}" ]] || { echo "error: missing ${go_binding}" >&2; exit 2; }
[[ -s "${ts_binding}" ]] || { echo "error: missing ${ts_binding}" >&2; exit 2; }

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
assert data["crypto"]["hpke_kem_id"] == 0x0010
assert data["crypto"]["hpke_kdf_id"] == 0x0001
assert data["crypto"]["hpke_aead_id"] == 0x0002
assert data["crypto"]["hpke_enc_length"] == 65
assert data["crypto"]["aad_v1_length"] == 148
assert data["crypto"]["id_length"] == 16
assert data["crypto"]["nonce_length"] == 12
assert data["crypto"]["signature_length"] == 64
assert data["limits"]["max_wire_packet_bytes"] == 1_048_576
assert data["limits"]["max_ack_ranges"] == 32
assert data["limits"]["max_resume_cursors"] == 256
print("AWP v1 constants are internally consistent.")
PY

python3 - "${wire_vector}" <<'PY'
import base64
import hashlib
import json
import sys
from pathlib import Path

data = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
assert data["fixtureUse"].startswith("TEST ONLY")
packet = base64.b64decode(data["wirePacketBase64"] + "=" * (-len(data["wirePacketBase64"]) % 4))
assert len(packet) == 149
assert hashlib.sha256(packet).hexdigest() == data["wirePacketSha256"]
assert data["wireMajor"] == 1 and data["wireMinor"] == 0
assert data["connectionGeneration"] > 0
assert data["trustManifestRevision"] > 0
assert data["credentialStatusRevision"] > 0
print("AWP generated binding packet vector is internally consistent.")
PY

grep -Fq 'protoc-gen-go v1.36.12' "${go_binding}"
grep -Fq 'package awpv1' "${go_binding}"
grep -Fq 'protoc-gen-es v2.14.1' "${ts_binding}"
grep -Fq 'mss.awp.v1.WirePacket' "${ts_binding}"

python3 - "${suite_vector}" <<'PY'
import base64
import hashlib
import json
import sys
from pathlib import Path

path = Path(sys.argv[1])
data = json.loads(path.read_text(encoding="utf-8"))

def decode(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))

assert data["suiteId"] == 1
assert data["fixtureUse"].startswith("TEST ONLY")
public_jwk = data["publicJwk"]
assert set(public_jwk) == {"crv", "kty", "x", "y"}
assert public_jwk["crv"] == "P-256" and public_jwk["kty"] == "EC"
assert len(decode(public_jwk["x"])) == 32
assert len(decode(public_jwk["y"])) == 32
canonical = json.dumps(public_jwk, separators=(",", ":"), sort_keys=True).encode()
assert base64.urlsafe_b64encode(hashlib.sha256(canonical).digest()).rstrip(b"=").decode() == data["jkt"]

signature = data["signature"]
message = signature["messageUtf8"].encode()
assert base64.urlsafe_b64encode(hashlib.sha256(message).digest()).rstrip(b"=").decode() == signature["messageSha256Base64Url"]
assert len(decode(signature["p1363Base64Url"])) == 64
assert len(decode(signature["highSP1363Base64Url"])) == 64

dpop = data["dpop"]
assert base64.urlsafe_b64encode(hashlib.sha256(dpop["accessToken"].encode()).digest()).rstrip(b"=").decode() == dpop["ath"]
assert dpop["claims"]["ath"] == dpop["ath"]
assert dpop["header"]["alg"] == "ES256"
assert dpop["header"]["typ"] == "dpop+jwt"
assert dpop["header"]["jwk"] == public_jwk
assert dpop["signingInput"] == dpop["protectedBase64Url"] + "." + dpop["payloadBase64Url"]
assert dpop["proof"] == dpop["signingInput"] + "." + dpop["signatureP1363Base64Url"]
assert len(decode(dpop["signatureP1363Base64Url"])) == 64
print("AWP Suite 0001 shared identity and DPoP vector is internally consistent.")
PY

python3 - "${hpke_vector}" <<'PY'
import base64
import hashlib
import json
import sys
from pathlib import Path

data = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))

def decode(value: str) -> bytes:
    return base64.urlsafe_b64decode(value + "=" * (-len(value) % 4))

assert data["fixtureUse"].startswith("TEST ONLY")
assert data["suiteId"] == 1
assert data["hpke"] == {"kemId": 0x0010, "kdfId": 0x0001, "aeadId": 0x0002}
assert len(decode(data["recipient"]["privateD"])) == 32
assert len(decode(data["infoBase64Url"])) == 84
assert len(decode(data["plaintextBase64Url"])) == 141
assert len(decode(data["encBase64Url"])) == 65
assert len(decode(data["ciphertextBase64Url"])) == 157
assert len(decode(data["directionKeys"]["hcToAbaBase64Url"])) == 32
assert len(decode(data["directionKeys"]["abaToHcBase64Url"])) == 32
info = decode(data["infoBase64Url"])
assert decode(data["contextHashBase64Url"]) == hashlib.sha256(info).digest()
assert decode(data["nonces"]["hcToAbaSequence1Base64Url"]) == decode(data["material"]["hcToAbaNoncePrefixBase64Url"]) + b"\x00" * 7 + b"\x01"
assert decode(data["nonces"]["abaToHcSequence1Base64Url"]) == decode(data["material"]["abaToHcNoncePrefixBase64Url"]) + b"\x00" * 7 + b"\x01"
print("AWP Suite 0001 HPKE and session KDF vector is internally consistent.")
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

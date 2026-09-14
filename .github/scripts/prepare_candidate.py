"""Verify a reviewed source patch as immutable Git objects; never update refs.

Only the owner-authorized Remote branch may run this workflow. Archives include
tracked source/public build dependencies, never credentials or runtime data.
"""
from __future__ import annotations

import base64
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import urllib.request

PATCH = Path(".github/checkpoints/remote.patch")
ROOTS = {"aba", "hc", "platform", "protocol", "docs", "scripts"}
REPOSITORY = "mss-boot-ai/harness-platform-monorepo"
LIMIT = 2 * 1024 * 1024


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], text=True).strip()


def output() -> Path:
    directory = Path(os.environ["RUNNER_TEMP"]) / "runtime-review"
    directory.mkdir(parents=True, exist_ok=True)
    return directory


def prepare() -> None:
    encoded = PATCH.read_bytes()
    if not encoded or len(encoded) > LIMIT:
        raise ValueError("invalid encoded patch bound")
    if encoded.startswith(b"MSS-REVIEWED-PATCH-GZIP-V1\n"):
        compressed = base64.b64decode(b"".join(encoded.split(b"\n", 1)[1].splitlines()), validate=True)
        with gzip.GzipFile(fileobj=io.BytesIO(compressed)) as stream:
            raw = stream.read(LIMIT + 1)
    else:
        raw = encoded
    if not raw or len(raw) > LIMIT:
        raise ValueError("invalid decoded patch bound")
    (output() / "reviewed.patch").write_bytes(raw)
    stats = subprocess.check_output(["git", "apply", "--numstat", "-"], input=raw).decode("utf-8")
    for row in stats.splitlines():
        name = row.split("\t", 2)[-1]
        path = Path(name)
        if path.is_absolute() or ".." in path.parts or not path.parts or path.parts[0] not in ROOTS:
            raise ValueError("patch outside reviewed source roots")
    subprocess.run(["git", "apply", "--check", "--index", "-"], input=raw, check=True)
    subprocess.run(["git", "apply", "--index", "-"], input=raw, check=True)
    subprocess.run(["cargo", "+1.88.0", "fmt", "--all"], cwd="aba", check=True)
    subprocess.run(["git", "add", "-u", "--", "aba"], check=True)
    subprocess.run(["git", "rm", "--", str(PATCH)], check=True)
    paths = git("diff", "--cached", "--name-only", "-z").split("\0")
    metadata = {"source_sha": git("rev-parse", "HEAD"), "base_tree": git("rev-parse", "HEAD^{tree}"),
                "candidate_tree": git("write-tree"), "patch_sha256": hashlib.sha256(raw).hexdigest(),
                "paths": [name for name in paths if name]}
    (output() / "candidate-local.json").write_text(json.dumps(metadata, indent=2) + "\n")
    with (output() / "formatted.patch").open("wb") as stream:
        subprocess.run(["git", "diff", "--cached", "--binary", "HEAD"], stdout=stream, check=True)
    with (output() / "candidate-source.tar").open("wb") as stream:
        subprocess.run(["git", "archive", "--format=tar", "--prefix=harness/", metadata["candidate_tree"]], stdout=stream, check=True)


def post(endpoint: str, payload: dict) -> dict:
    request = urllib.request.Request(
        f"https://api.github.com/repos/{REPOSITORY}/{endpoint}", data=json.dumps(payload).encode(), method="POST",
        headers={"Authorization": f"Bearer {os.environ['GH_TOKEN']}", "Accept": "application/vnd.github+json",
                 "Content-Type": "application/json", "X-GitHub-Api-Version": "2022-11-28"})
    with urllib.request.urlopen(request, timeout=30) as response:
        return json.load(response)


def publish() -> None:
    if os.environ.get("GITHUB_REPOSITORY") != REPOSITORY:
        raise ValueError("repository binding mismatch")
    metadata = json.loads((output() / "candidate-local.json").read_text())
    if metadata["source_sha"] != git("rev-parse", "HEAD") or metadata["candidate_tree"] != git("write-tree"):
        raise ValueError("candidate changed after verification")
    entries = []
    for name in metadata["paths"]:
        indexed = git("ls-files", "--stage", "--", name)
        if not indexed:
            entries.append({"path": name, "mode": "100644", "type": "blob", "sha": None})
            continue
        mode, expected, _ = indexed.split(" ", 2)
        if mode not in {"100644", "100755"}:
            raise ValueError("candidate contains a non-regular entry")
        data = Path(name).read_bytes()
        if len(data) > LIMIT:
            raise ValueError("candidate file too large")
        value = post("git/blobs", {"content": base64.b64encode(data).decode(), "encoding": "base64"})
        if value["sha"] != expected:
            raise ValueError("blob identity mismatch")
        entries.append({"path": name, "mode": mode, "type": "blob", "sha": value["sha"]})
    value = post("git/trees", {"base_tree": metadata["base_tree"], "tree": entries})
    if value["sha"] != metadata["candidate_tree"]:
        raise ValueError("tree identity mismatch")
    metadata["tree_uploaded"] = True
    (output() / "candidate.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print(f"Verified candidate tree: {value['sha']}; no branch or PR was changed.")


if __name__ == "__main__":
    if sys.argv[1:] == ["prepare"]:
        prepare()
    elif sys.argv[1:] == ["publish"]:
        publish()
    else:
        raise SystemExit("expected prepare or publish")

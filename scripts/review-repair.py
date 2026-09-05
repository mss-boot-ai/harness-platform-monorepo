#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import textwrap
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BRANCH = os.environ.get("REPAIR_BRANCH", "codex/bootstrap-harness-platform-foundation")
BASE = os.environ.get("EXPECTED_REPAIR_BASE", "f843b4a515394332d7f9e3201de6899e58d8faff")
REPO = "mss-boot-ai/harness-platform-monorepo"
REPORT = ROOT / "review-repair-report.json"


def run(*args: str, cwd: Path = ROOT, timeout: int = 1200, check: bool = True) -> str:
    result = subprocess.run(
        list(args), cwd=cwd, text=True, stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT, timeout=timeout,
    )
    if check and result.returncode != 0:
        raise RuntimeError(f"command failed ({result.returncode}): {' '.join(args)}\n{result.stdout}")
    return result.stdout


def replace_once(path: Path, old: str, new: str) -> None:
    value = path.read_text()
    if old not in value:
        raise RuntimeError(f"expected source fragment not found in {path}: {old[:120]!r}")
    path.write_text(value.replace(old, new, 1))


def function_span(source: str, marker: str) -> tuple[int, int]:
    start = source.find(marker)
    if start < 0:
        raise RuntimeError(f"function marker not found: {marker}")
    brace = source.find("{", start)
    if brace < 0:
        raise RuntimeError(f"function opening brace not found: {marker}")
    depth = 0
    quote: str | None = None
    escaped = False
    index = brace
    while index < len(source):
        char = source[index]
        if quote is not None:
            if quote == "`":
                if char == "`":
                    quote = None
            elif escaped:
                escaped = False
            elif char == "\\":
                escaped = True
            elif char == quote:
                quote = None
            index += 1
            continue
        if source.startswith("//", index):
            newline = source.find("\n", index)
            index = len(source) if newline < 0 else newline + 1
            continue
        if source.startswith("/*", index):
            end = source.find("*/", index + 2)
            if end < 0:
                raise RuntimeError("unterminated block comment")
            index = end + 2
            continue
        if char in ('"', "'", "`"):
            quote = char
        elif char == "{":
            depth += 1
        elif char == "}":
            depth -= 1
            if depth == 0:
                return start, index + 1
        index += 1
    raise RuntimeError(f"function end not found: {marker}")


def replace_function(path: Path, marker: str, replacement: str) -> None:
    source = path.read_text()
    start, end = function_span(source, marker)
    path.write_text(source[:start] + replacement.rstrip() + "\n" + source[end:])


def git_head() -> str:
    return run("git", "rev-parse", "HEAD").strip()


def wait_ci(sha: str) -> list[dict[str, object]]:
    deadline = time.time() + 1200
    observed: list[dict[str, object]] = []
    while time.time() < deadline:
        payload = json.loads(run(
            "gh", "api",
            f"repos/{REPO}/actions/runs?head_sha={sha}&per_page=30",
            timeout=60,
        ))
        runs = [
            item for item in payload.get("workflow_runs", [])
            if item.get("head_sha") == sha and item.get("name") == "Harness Platform CI"
        ]
        by_event = {item.get("event"): item for item in runs}
        if "push" in by_event and "pull_request" in by_event:
            selected = [by_event["push"], by_event["pull_request"]]
            if all(item.get("status") == "completed" for item in selected):
                observed = [{
                    "id": item["id"], "event": item["event"],
                    "status": item["status"], "conclusion": item.get("conclusion"),
                    "url": item["html_url"],
                } for item in selected]
                if not all(item.get("conclusion") == "success" for item in selected):
                    raise RuntimeError(f"CI failed for {sha}: {observed}")
                return observed
        time.sleep(10)
    raise RuntimeError(f"timed out waiting for push/PR CI for {sha}: {observed}")


report: dict[str, object] = {"base": BASE, "branch": BRANCH, "checkpoints": []}


def checkpoint(message: str, paths: list[str], commands: list[tuple[Path, list[str]]]) -> str:
    for cwd, command in commands:
        run(*command, cwd=cwd)
    run("git", "diff", "--check")
    run("git", "add", "--", *paths)
    if not run("git", "diff", "--cached", "--name-only").strip():
        raise RuntimeError(f"checkpoint has no staged changes: {message}")
    run("git", "commit", "-m", message)
    sha = git_head()
    run("git", "push", "origin", f"HEAD:{BRANCH}", timeout=180)
    ci = wait_ci(sha)
    report["checkpoints"].append({"sha": sha, "message": message, "ci": ci})
    REPORT.write_text(json.dumps(report, indent=2))
    return sha


def gofmt(paths: list[str]) -> None:
    run("gofmt", "-w", *paths, cwd=ROOT / "platform")


def stage_schema() -> None:
    repository = ROOT / "platform/internal/harness/store/repository.go"
    source = repository.read_text()
    marker = "\treturn nil\n}"
    start, end = function_span(source, "func VerifySchema(")
    function = source[start:end]
    if "verifyBaseSecurityUniqueIndexes" not in function:
        if marker not in function:
            raise RuntimeError("VerifySchema return marker changed")
        function = function.replace(
            marker,
            "\tif err := verifyBaseSecurityUniqueIndexes(db); err != nil {\n"
            "\t\treturn err\n\t}\n\treturn nil\n}",
            1,
        )
        repository.write_text(source[:start] + function + source[end:])

    contracts = ROOT / "platform/internal/harness/store/base_schema_contracts.go"
    contracts.write_text(textwrap.dedent('''\
        package store

        import (
        \t"fmt"
        \t"slices"

        \t"gorm.io/gorm"
        )

        type baseSecurityUniqueIndexContract struct {
        \tmodel   any
        \tname    string
        \tcolumns []string
        }

        var baseSecurityUniqueIndexContracts = []baseSecurityUniqueIndexContract{
        \t{model: new(endpointRow), name: "ux_harness_endpoint_owner_sign", columns: []string{"owner_user_id", "signing_jkt"}},
        \t{model: new(endpointRow), name: "ux_harness_endpoint_owner_kem", columns: []string{"owner_user_id", "kem_jkt"}},
        \t{model: new(frameRow), name: "ux_harness_frame_sequence", columns: []string{"session_id", "key_generation", "sender_endpoint_id", "sequence"}},
        }

        func verifyBaseSecurityUniqueIndexes(db *gorm.DB) error {
        \tfor _, contract := range baseSecurityUniqueIndexContracts {
        \t\tindexes, err := db.Migrator().GetIndexes(contract.model)
        \t\tif err != nil {
        \t\t\treturn fmt.Errorf("inspect Harness index %s: %w", contract.name, err)
        \t\t}
        \t\tfound := false
        \t\tfor _, index := range indexes {
        \t\t\tif index.Name() != contract.name {
        \t\t\t\tcontinue
        \t\t\t}
        \t\t\tfound = true
        \t\t\tunique, known := index.Unique()
        \t\t\tif !known || !unique {
        \t\t\t\treturn fmt.Errorf("Harness index %s must be unique", contract.name)
        \t\t\t}
        \t\t\tif columns := index.Columns(); !slices.Equal(columns, contract.columns) {
        \t\t\t\treturn fmt.Errorf("Harness index %s columns are %v; expected %v", contract.name, columns, contract.columns)
        \t\t\t}
        \t\t\tbreak
        \t\t}
        \t\tif !found {
        \t\t\treturn fmt.Errorf("Harness index %s is unavailable", contract.name)
        \t\t}
        \t}
        \treturn nil
        }
        '''))
    tests = ROOT / "platform/internal/harness/store/base_schema_contracts_test.go"
    tests.write_text(textwrap.dedent('''\
        package store

        import (
        \t"strings"
        \t"testing"
        )

        func TestVerifySchemaAcceptsSecurityUniqueIndexContracts(t *testing.T) {
        \tpersistence := newTestStore(t)
        \tif err := VerifySchema(persistence.db); err != nil {
        \t\tt.Fatalf("VerifySchema: %v", err)
        \t}
        }

        func TestVerifySchemaRejectsMalformedSecurityUniqueIndexes(t *testing.T) {
        \ttests := []struct {
        \t\tname, index, replacement string
        \t}{
        \t\t{"frame non-unique", "ux_harness_frame_sequence", "CREATE INDEX ux_harness_frame_sequence ON harness_frames(session_id, key_generation, sender_endpoint_id, sequence)"},
        \t\t{"frame missing column", "ux_harness_frame_sequence", "CREATE UNIQUE INDEX ux_harness_frame_sequence ON harness_frames(session_id, key_generation, sender_endpoint_id)"},
        \t\t{"frame wrong order", "ux_harness_frame_sequence", "CREATE UNIQUE INDEX ux_harness_frame_sequence ON harness_frames(session_id, sender_endpoint_id, key_generation, sequence)"},
        \t\t{"frame wrong column", "ux_harness_frame_sequence", "CREATE UNIQUE INDEX ux_harness_frame_sequence ON harness_frames(session_id, key_generation, receiver_endpoint_id, sequence)"},
        \t\t{"signing JKT non-unique", "ux_harness_endpoint_owner_sign", "CREATE INDEX ux_harness_endpoint_owner_sign ON harness_endpoints(owner_user_id, signing_jkt)"},
        \t\t{"KEM JKT wrong order", "ux_harness_endpoint_owner_kem", "CREATE UNIQUE INDEX ux_harness_endpoint_owner_kem ON harness_endpoints(kem_jkt, owner_user_id)"},
        \t}
        \tfor _, test := range tests {
        \t\tt.Run(test.name, func(t *testing.T) {
        \t\t\tpersistence := newTestStore(t)
        \t\t\tif err := persistence.db.Exec("DROP INDEX " + test.index).Error; err != nil {
        \t\t\t\tt.Fatalf("drop index: %v", err)
        \t\t\t}
        \t\t\tif err := persistence.db.Exec(test.replacement).Error; err != nil {
        \t\t\t\tt.Fatalf("create malformed index: %v", err)
        \t\t\t}
        \t\t\terr := VerifySchema(persistence.db)
        \t\t\tif err == nil || !strings.Contains(err.Error(), test.index) {
        \t\t\t\tt.Fatalf("VerifySchema error = %v", err)
        \t\t\t}
        \t\t})
        \t}
        }
        '''))
    gofmt([
        "internal/harness/store/repository.go",
        "internal/harness/store/base_schema_contracts.go",
        "internal/harness/store/base_schema_contracts_test.go",
    ])
    checkpoint(
        "fix(store): verify security critical unique index contracts",
        [
            "platform/internal/harness/store/repository.go",
            "platform/internal/harness/store/base_schema_contracts.go",
            "platform/internal/harness/store/base_schema_contracts_test.go",
        ],
        [
            (ROOT / "platform", ["go", "test", "-count=1", "./internal/harness/store", "-run", "TestVerifySchema"]),
            (ROOT / "platform", ["go", "test", "-count=1", "./..."]),
            (ROOT / "platform", ["go", "vet", "./..."]),
        ],
    )


def main() -> None:
    run("git", "config", "user.name", "harness-review-repair[bot]")
    run("git", "config", "user.email", "harness-review-repair[bot]@users.noreply.github.com")
    run("git", "merge-base", "--is-ancestor", BASE, "HEAD")
    changed = set(filter(None, run("git", "diff", "--name-only", f"{BASE}..HEAD").splitlines()))
    allowed = {".github/workflows/review-repair.yml", "scripts/review-repair.py"}
    unexpected = changed - allowed
    if unexpected:
        raise RuntimeError(f"unknown work exists after expected base {BASE}: {sorted(unexpected)}")
    if run("git", "status", "--porcelain").strip():
        raise RuntimeError("repair worker started from a dirty checkout")
    stage_schema()
    report["status"] = "schema checkpoint complete"
    report["head"] = git_head()
    REPORT.write_text(json.dumps(report, indent=2))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        report["status"] = "failed"
        report["error"] = str(error)
        REPORT.write_text(json.dumps(report, indent=2))
        print(json.dumps(report, indent=2), file=sys.stderr)
        raise

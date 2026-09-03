# Platform Upstream Provenance

Platform is based on one exact mss-boot-admin source tree. The canonical lock is `mss-boot-admin.lock.yaml`.

Current baseline:

```text
repository:    mss-boot-io/mss-boot-admin
tag:           v1.3.7
tag object:    41c6517950f7f5f642418f5d4a49386e9c200b15
source commit: 77b53d41092741eac62fa6418c0bdbf87413c7cd
source tree:   32ffcde4e8ca2f41a8c15c373217d06c1b35d9c0
Go:            1.26.6
```

Workflow:

1. Run `scripts/import-mss-boot-admin.sh` from a clean topic branch.
2. Review the imported tree and generated `import-manifest.txt`.
3. Commit the import alone; do not mix ACP modifications into that commit.
4. Change `import.status` to `complete` and set `import_commit_sha` in a following provenance checkpoint once the import commit is known.
5. Run `scripts/verify-platform-upstream.sh --source` before modifying imported upstream files.
6. Future Platform changes are normal commits whose ancestry preserves the exact import.

The import script deletes the temporary `platform/README.md` scaffold because the upstream repository has its own root README. It preserves only this `.upstream` directory while replacing the rest of `platform/`.

Never fetch or import upstream `main`, `latest`, or an unpeeled tag. An upstream change requires a new ADR and lock update.

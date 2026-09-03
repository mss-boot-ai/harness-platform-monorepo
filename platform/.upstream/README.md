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

Import status:

```text
status:                  complete
isolated import commit:  bf0f73d5d9480288fd615045e4bdca72ead22012
import manifest blob:    a979e59dd8fe5a9314860c4faf10c2ef05f6ec60
verification workflow:   33775161473
workflow conclusion:     success
verified at UTC:         2026-09-03T15:52:14Z
```

The import workflow performed the following against a clean topic-branch checkout:

1. verified the lock file;
2. resolved the annotated `v1.3.7` tag object and peeled commit;
3. fetched the exact source commit;
4. checked the exact source tree SHA;
5. archived the locked tree into `platform/` while preserving `.upstream`;
6. generated `import-manifest.txt` from `git ls-tree`;
7. verified every imported path, Git blob identity, file type and executable mode;
8. committed and pushed the imported source as one isolated commit.

The one-time write-capable workflow was removed immediately after the successful import. The reusable scripts remain for future controlled imports and provenance checks.

Future Platform modifications are expected to make the working `platform/` tree differ from the pristine upstream tree. The exact initial import remains provable from Git ancestry, the isolated import commit, this lock and the manifest. Do not rewrite that history.

Never fetch or import upstream `main`, `latest`, or an unpeeled tag. An upstream change requires a new ADR and lock update, followed by migration and verification evidence.

# Harness Platform MVP 验证计划

- **状态**：Accepted
- **修订日期**：2026-09-04
- **产品契约**：`docs/product/MVP-PRD.md`

## 1. 证据原则

- 只把实际执行并通过的命令写为“通过”；
- 每份报告绑定完整远端 Commit SHA；
- 代码 push、Workflow 存在和测试通过是三种不同状态；
- 测试后工作树改变时，结果不能归因于旧 SHA；
- 失败输出脱敏保存，修复形成新提交后重新测试；
- 环境缺失、Skip、交叉编译和 Mock 不等于真机/真实服务验证。

## 2. 持续集成

每次 push：

### Documentation

- 文档入口、术语、Accepted/Superseded ADR；
- 禁止再次描述 vendored Platform 为当前架构；
- PRD/架构/实施/验证互相链接。

### Platform

- Thin Host import 合同；
- Go 1.26.6；
- `go test -count=1 ./...`；
- `go vet ./...`；
- Race Detector 对 Gateway/Store 关键包；
- Migration SQLite 集成；
- Admin Web pnpm 10.34.5 install/lint/test/build；
- `mss verify --all` 和生成物漂移。

### Protocol

- Protobuf compile；
- Canonical 148-byte AAD offsets；
- shared testdata schema；
- Go/Rust/TS Golden Vector；
- unknown Major/Suite/Critical Flag rejection。

### ABA

- locked metadata；
- fmt；
- Clippy `-D warnings`；
- unit/integration tests；
- config, key store, process, journal and connector tests。

### HC

- exact pnpm lock；
- lint；
- strict typecheck；
- unit tests；
- WebCrypto/Node shared vectors；
- browser bundle build。

## 3. Platform Test Matrix

### Domain

- every allowed state transition；
- every invalid transition；
- revoked is terminal；
- Generation/Sequence monotonic；
- idempotency same request/same result；
- same key/different payload conflict；
- current owner/tenant authorization。

### Repository

- Migration from empty DB；
- repeated migration no duplicate side effect；
- unique Endpoint JKT；
- atomic Enrollment Consume；
- ticket concurrent single consume；
- frame concurrent insert；
- monotonic ACK CAS；
- revoke + connection/outbox transaction intent；
- retention batch upper bound。

### HTTP

- reject unknown JSON fields；
- request size limits；
- stable code/status；
- cache-control no-store for credentials；
- DPoP malformed/expired/replayed/mismatched；
- cross-owner/cross-endpoint denial；
- Idempotency-Key behavior。

### WSS

- expired/replayed/mismatched Ticket；
- READY only after Challenge；
- binary only；
- one active generation fences old connection；
- queue/byte limits；
- slow consumer；
- invalid Packet close code；
- Resume original Frame。

## 4. Cross-language Crypto Matrix

Shared inputs and expected outputs:

- P-256 public/private JWK；
- RFC 7638 JKT；
- P1363 low-S signature；
- DPoP proof canonical claims；
- HPKE Base Key Package；
- direction HKDF keys；
- 148-byte AAD；
- AES-GCM nonce/ciphertext/tag；
- frame signature；
- tampered variants；
- Generation 2 rotation variant。

Requirement: Go, Rust and TypeScript each independently read and verify the same files. No implementation may regenerate expected outputs during the assertion.

## 5. ABA Matrix

- strict TOML unknown field and duplicate ID rejection；
- no remote executable/path/env fields；
- key generation and secure persistence；
- endpoint credential load/rotation；
- DPoP nonce/retry；
- Ticket and WSS challenge；
- Runtime/Workspace allowed and denied；
- symlink escape；
- process group cleanup；
- ACP complete-frame proxy；
- sequence crash before/after reservation；
- inbound Journal state transitions；
- `DISPATCH_STARTED` crash → `UNCERTAIN`；
- bounded journal/queue；
- revoked endpoint stops reconnect loop。

## 6. HC Matrix

- non-extractable browser key where supported；
- storage adapter never serializes private key to JSON；
- registration proof；
- DPoP and Ticket；
- key package issuer/recipient/context checks；
- frame encrypt/decrypt/sign/verify；
- duplicate/gap handling；
- reconnect and page refresh；
- permission response；
- revoked and uncertain UI state；
- multi-tab leader safety smoke test。

## 7. End-to-End Scenarios

### E2E-001 Happy Path

ABA Enroll → Human Approve → HC Register → both READY → Session Create → Key Package → ACP Prompt/Response → Close。

### E2E-002 HC Network Recovery

Drop HC after Platform stores ABA response but before HC ACK; reconnect with new Ticket; receive exact original Frame once; ACK cursor advances。

### E2E-003 Conflict

Send same `(session,generation,sender,sequence)` with different ciphertext; Gateway rejects, records security conflict and closes affected Session。

### E2E-004 ABA Dispatch Crash

Crash at `DISPATCH_STARTED`; restart; record is `UNCERTAIN`; test Agent does not receive an automatic duplicate request。

### E2E-005 Revocation

Revoke HC while online; current WSS closes; refresh/ticket/new WSS fail; existing history limitations are clearly documented。

### E2E-006 Opaque Canary

Use unique Canary in Prompt, Tool parameter and Response. Scan Platform database, Redis if enabled, Admin/Gateway logs, Audit, error responses and CI artifacts. Exact and encoded plaintext variants must be absent。

### E2E-007 Local Policy

Try unknown Runtime, unknown Workspace, path escape and Platform-supplied command/env; ABA rejects all without starting a process。

## 8. Failure Injection

Inject failure around:

- Enrollment approve/consume commit；
- ticket consume；
- frame DB commit；
- route enqueue；
- sequence reservation；
- AEAD seal；
- journal receive；
- ACP stdin write；
- response journal；
- ACK commit；
- revoke kick。

Invariants: no Nonce reuse, no silent ACK before durable state, no unbounded retry, no automatic repeat of uncertain side effects。

## 9. Capacity Smoke

At minimum record：

- 1,000 idle WSS connections or an explicitly scaled CI equivalent with extrapolation marked；
- 100 active Session or scaled equivalent；
- 4/16/64 KiB frame latency；
- maximum 1 MiB acceptance and 1 MiB+1 rejection；
- slow receiver isolation；
- memory, goroutine/task, FD and queue bounds；
- reconnect storm backoff。

Scaled CI is not production load proof. Full target can remain pre-production evidence if clearly stated in PR。

## 10. Thin Host Upgrade Evidence

Using official `mss v1.3.7`：

1. verify installed binary identity；
2. run read-only upgrade plan against current `platform/`；
3. ensure no conflict and all handwritten seams are preserve；
4. if apply is needed, commit/push generated change before full verification；
5. run `mss doctor --strict` and `mss verify --all`；
6. repeat read-only plan and require no-op；
7. verify no Foundation source/local replace/tarball。

## 11. Security/Secret Scan

Reject：

- PEM/private JWK/seed phrases；
- production Token/Ticket/Cookie；
- plaintext SRK/derived key；
- committed runtime DB/journal；
- ACP Canary in Platform artifacts；
- request/response body dumps；
- arbitrary command execution endpoint；
- unbounded queue/channel construction without documented hard limit。

## 12. Final Report

`docs/roadmap/verification/<date>-mvp-final.md` must include：

```text
branch
final commit SHA
main base SHA
PR number/URL
all checkpoint SHAs
CI run IDs and conclusions
commands and toolchain versions
E2E scenarios and evidence
failure/fix/retest mapping
Opaque Canary result
capacity result
Thin Host upgrade result
known limitations / not verified
```

Only after this report is pushed and the corresponding final CI is green may the PR be described as an MVP implementation PR。

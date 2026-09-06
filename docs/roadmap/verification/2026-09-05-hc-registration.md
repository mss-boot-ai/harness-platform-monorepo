# HC H5 注册与 M2 身份基础验证记录

- **状态**：Verified for this slice
- **日期**：2026-09-05
- **分支**：`codex/bootstrap-harness-platform-foundation`
- **验证 SHA**：`6239ad3ba45ff82018d233e878c119a2f01443a1`
- **范围**：HC H5 本地密钥、Suite 0001 JWK/ES256/DPoP 基础、Human-bound HC 注册与凭据持久化

## 已实现

- HC pnpm workspace、React/Vite H5 和真实 HC CI Job；
- WebCrypto P-256 E-SIG/E-KEM，不可导出私钥；
- IndexedDB `CryptoKey` 结构化克隆、重载和可用性探测；
- RFC 7638 JKT、64-byte P1363 low-S 签名和 Token `ath`；
- Go、Rust、TypeScript 共同消费 Suite 0001 固定测试向量；
- HC/ABA DPoP proof 生成与 Go verifier；
- `htu/htm/iat/jti/ath/nonce/JKT` 校验和有界 replay cache；
- ADR-0005：HC 注册复用 `/admin/api` Browser Session/CSRF/Origin，Gateway 不接受 Admin Cookie；
- 一次性 HC Registration Challenge、Access/Refresh Credential、增量 Migration 和原子消费；
- H5 同源登录、Challenge、固定二进制 Transcript 签名、注册和内存 Access Token；
- Refresh Credential 仅通过 HttpOnly、Strict SameSite、refresh-only Path Cookie 返回。

## 当前 SHA 远端证据

- push Foundation CI `33894174661`：`success`；
- PR Foundation CI `33894180563`：`success`；
- documentation、platform-import、protocol、aba-rust、hc-typescript 均实际执行并成功；
- HC Job 已执行 pnpm frozen install、lint、strict typecheck、15 tests 和 build，不再是缺少 workspace 的 skip。

## 本地命令证据

在本阶段各精确远端检查点实际执行并通过：

```text
platform: go test -count=1 ./...
platform: go vet ./...
platform: go test -race -count=1 ./internal/modules/harness ./internal/harness/registration ./internal/harness/store
aba:      cargo +1.88.0 fmt --all -- --check
aba:      cargo +1.88.0 clippy --locked --workspace --all-targets --all-features -- -D warnings
aba:      cargo +1.88.0 test --locked --workspace --all-targets
hc:       pnpm lint
hc:       pnpm typecheck
hc:       pnpm test
hc:       pnpm build
```

最近结果：ABA 15 tests；HC 6 test files / 15 tests；Go 全量、vet 和关键包 Race Detector 均成功。

## 内置浏览器真实验证

本地使用官方 Thin Host Backend `127.0.0.1:8080`，为复用上游已允许的可信 Origin，暂时停止 Admin Web 并让 HC H5 使用 `127.0.0.1:8001`。实际完成：

1. 浏览器探测得到 `web-software`；
2. 生成不可导出 Signing/KEM Key；
3. 使用本地开发 Admin 账号建立 HttpOnly Browser Session；
4. 请求 HC Challenge；
5. 浏览器本地签名注册 Transcript；
6. 注册返回 Endpoint `051e4bc61a…`，页面显示 `REGISTERED`；
7. 页面明确显示 Access Token 仅驻留内存，Gateway 尚未接通；
8. 浏览器控制台无 warning/error。

只读数据库核对：最新 Challenge 状态 `CONSUMED`；Access/Refresh `token_hash` 都是 64 位 hex、状态 `ACTIVE`。密码、Cookie、Token 原值、私钥和本地数据库均未提交。

## 失败—修复链

- `0af88555...` 的首次本地 HC Test 因 Vitest 5 未启用 globals 在收集阶段失败；`88e221ca...` 修复后 7 tests 和 CI 通过。
- `b8087ca4...` 的主 CI 全绿，但一次性 Cargo lock 生成 workflow Run `33890207853` 因已有 `Cargo.lock` 失败；`1f0c05e5...` 将其改为只读 locked 验证，Run `33890525374` 成功。
- `1d9f89ab...` 的本地 DPoP 测试发现 verifier 注入时钟与 replay cache 系统时钟不一致；`0e038709...` 统一事务时钟后全量、Race 和 CI 通过。
- `c8d7dccb...` 的本地全量发现 `CreateM1Schema` 被补丁错误接入 M2 Verify；`4df928e4...` 恢复正确顺序后全量、Race 和 CI 通过。
- `e854c68f...` 的本地全量发现授权矩阵仍硬编码 12 条；`a491c49e...` 更新为精确 14 条后全量、Race 和 CI 通过。
- 本地 Backend 首次重启因两条新增 Migration 未应用而被 Readiness 拒绝；使用官方 `mss setup` 运行 `go run -mod=readonly ./cmd/server migrate --config-provider fs` 后健康启动，没有手工改 Ledger 或表。

## 边界

本切片证明 HC 可安全建立 Human + Endpoint 注册身份，不代表 Gateway/WSS 或 ACP Session 已完成。Refresh rotation、持久 DPoP replay/nonce、Ticket/WSS Challenge、HEC/Trust Manifest、HPKE、ABA Connector 和 Opaque Relay 仍未实现。

# Gateway DPoP、Ticket 与 WSS Challenge 验证记录

- **状态**：Verified for this slice
- **日期**：2026-09-05
- **分支**：`codex/bootstrap-harness-platform-foundation`
- **验证 SHA**：`e5e405e0659548f16f725082d23543adc8c95755`
- **范围**：Endpoint Token/Refresh、共享 DPoP 状态、一次性 Ticket、Trust Manifest、二进制 WSS Challenge/READY、HC H5

## 交付

- 数据库共享 DPoP Replay、Endpoint Nonce Hash/CAS、Access Credential 查询和增量 Migration；
- 10 分钟 Access + 24 小时 HttpOnly Refresh Family 原子轮换；
- 有效 DPoP 下旧 Refresh 重用吊销整条 Access/Refresh Family；
- 独立 `cmd/harness-gateway`，Admin Cookie 不作为 Gateway 身份；
- 严格 Origin CORS、nonce challenge、`htu/htm/iat/jti/ath/JKT` 校验和 30 秒单次 Ticket；
- Protoc 35.0、protoc-gen-go 1.36.12、Protobuf-ES 2.14.1 生成绑定；
- Go/TS 对同一 149-byte ServerChallenge fixture 解码并确定性重编码；
- Root-signed Trust Manifest、独立 Online Signer、ServerChallenge/ChallengeResponse/ConnectionReady 固定 Transcript；
- WSS 只接受 `mss.awp.v1` + `mss.ticket.<opaque>`，只回显 `mss.awp.v1`，READY 前只接受 ChallengeResponse；
- Connection Generation 使用数据库原子 upsert/returning 持久递增；
- Root/Online P-256 Signer 保存于显式本地安全状态：目录 0700、文件 0600、拒绝 symlink/宽权限；
- H5 在 IndexedDB 固定首次 Root JKT，并拒绝 Root 替换与 Manifest Revision 回退；
- H5 Refresh → DPoP Ticket → Manifest Verify → 双向签名 Challenge → ConnectionReady。

## 当前 SHA 的 CI

- push Foundation CI `33901119145`：`success`；
- PR Foundation CI `33901125467`：`success`；
- documentation、platform-import、protocol、aba-rust、hc-typescript 全部成功。

## 本地验证

```text
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./internal/harness/gateway ./internal/harness/store
pnpm lint
pnpm typecheck
pnpm test                    # 7 files / 17 tests
pnpm build
./scripts/check-protocol.sh  # official protoc 35.0
mss verify --all
mss upgrade admin v1.3.7 --format json
```

结果：全部通过；Thin Host upgrade 为 dry-run、`conflicts: []`，业务 `go.mod`、模块、页面、路由和 locale 均 preserve。

## 内置浏览器证据

本地进程：Backend `127.0.0.1:8080`、HC H5 `localhost:8001`、Gateway `127.0.0.1:8082`，H5 通过同源 Vite proxy 访问 Admin/Gateway。

实际流程：

1. H5 刷新后使用 HttpOnly Refresh Cookie + 本地私钥恢复 Endpoint `dbf6d482bb…`；
2. 使用轮换后的内存 Access Token 发起 Ticket；
3. 第一次请求收到 `DPoP-Nonce`，第二次新 proof 成功；
4. H5 验证 Root-signed Manifest 与 Online Key；
5. WSS 选择 `mss.awp.v1`，Ticket 原值不在 URL；
6. H5 验证 ServerChallenge 签名并发送 Endpoint 签名的 ChallengeResponse；
7. H5 验证 ConnectionReady 签名，页面显示 `CONNECTION READY`、Generation 2 和 Root 指纹；
8. 页面把下一阶段投影为“创建 ACP Session”，浏览器控制台无 warning/error。
9. 应用 Connection Migration 后首次连接为 Generation 1；重启 Gateway 后同一 Endpoint 为 Generation 2，证明计数未随进程回退。
10. 启用持久 Signer 后 Manifest Revision 从 1 增到 2，H5 Root JKT 保持 `UJT3InRUsX…`，Connection Generation 从 3 增到 4；控制台无错误。

## 失败—修复

- `cbd34f6...` 的本地 Ticket 集成因 Nonce Hash 错把随机 bytes 而不是 base64url claim 文本作为输入失败；`2f303999...` 分离 Ticket/Nonce Hash 语义后全量与 CI 通过。
- `8edb154...` 的首次 Go/TS Wire 测试同时拒绝被手工抄漏 6 bytes 的 fixture；`7f714759...` 用当前生成类型重建 149-byte/固定 SHA-256 fixture 后两端通过。
- `2969ba4...` 的 WSS 集成只在 Ticket 重放期望值失败：实现统一返回 401 避免状态探针，`032e231...` 修正测试为安全契约后 Go 全量/Race 和 CI 通过。
- 浏览器首次 READY 后侧栏仍显示未接通；`766a55c...` 修正 H5 状态投影并重新完成真实 WSS。

## 仍未完成

- 本地 Signer 已权限受限持久化；生产 KMS/HSM Adapter 和 Root/Online 轮换仍未实现；
- Connection Generation 已持久化；Fencing 的活动连接目录与跨实例 Kick 尚未实现；
- READY 后业务 Packet 当前明确关闭连接并返回 relay 未启用，不会静默 ACK；
- ABA Enrollment/Connector、Session API、HPKE/AEAD、ACP Relay/Journal 和完整 E2E 尚未实现。

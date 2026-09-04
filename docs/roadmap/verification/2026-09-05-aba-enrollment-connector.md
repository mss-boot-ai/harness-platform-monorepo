# ABA Enrollment 与 Gateway Connector 验证记录

- **状态**：Verified for this slice
- **日期**：2026-09-05
- **分支**：`codex/bootstrap-harness-platform-foundation`
- **验证 SHA**：`fa9fe7abb26ded4aee12b8690fe3cdde2407eb6d`
- **范围**：ABA 开发 KeyStore、Device Enrollment、原生 Refresh、DPoP Ticket、Trust Pin、Protobuf WSS Challenge/READY、HC/ABA 共存

## 交付

- ABA 显式 `--insecure-dev-keystore`，只允许 loopback Platform；E-SIG/E-KEM 使用独立 P-256 私钥；
- 开发 KeyStore 目录 0700、文件 0600，拒绝 symlink、宽权限和重复初始化，CLI inspect 只显示公钥指纹；
- ABA Device Authorization 风格 Start/Poll/Consume，Start 与 Consume 使用不同固定 Transcript 和 E-SIG 持有证明；
- Platform 原子消费已审批 Enrollment，并一次创建 ABA Endpoint、Access、Refresh 与 Audit；
- 未归属的 Pending Enrollment 可由已登录用户看到，但审批仍必须提交对应 User Code；
- Gateway 原生 Refresh 只接受 `Authorization: Refresh <opaque>` + DPoP，HC Web 仍只接受 HttpOnly Cookie；两种传输不可互换；
- ABA Ticket 绑定原生客户端语义且 WebSocket 请求不得携带浏览器 Origin；HC Ticket 仍精确绑定可信浏览器 Origin；
- Browser External Origin 与 Native External Origin 独立配置，使 H5 Vite 同源代理和 ABA 直连可在同一 Gateway 进程共存；
- Rust 使用锁定的 Prost 与 vendored protoc 从权威 `wire.proto` 生成类型，并消费共享 ServerChallenge fixture；
- ABA 验证 Root-signed Trust Manifest、Root JKT、到期时间、Revision、Online Key，并持久固定首次 Root；拒绝 Root 替换、Revision 回退和同 Revision Online Key 漂移；
- ABA 执行 Refresh → DPoP Ticket → 无 Origin WSS → 验证 ServerChallenge → 签名 ChallengeResponse → 验证 ConnectionReady；CLI 不输出 Token、Ticket或私钥。

## 提交与远端 CI

本切片的主要提交：

| SHA | 内容 |
| --- | --- |
| `1c3c9b42d3dae97903ec61287fa17895ea350655` | loopback-only ABA 开发 KeyStore |
| `5455177a81344a3f51fc591ae5ded4bf17e64fe9` | 原子消费 ABA Enrollment |
| `993c5b5f913871060a48b7a35fb68a6e5fa96694` | Gateway ABA Enrollment HTTP 流程 |
| `8f517aac8d33a80593be2eacd64447b9df2b6cd1` | ABA Enrollment Client |
| `fe08d99e8755159ef8a1ae7860431e1000ad6765` | 修复未归属 Enrollment 的审批可见性 |
| `b5ca753c167d783f1e535deef6450105a220d474` | ABA Enrollment HTTP 集成回归 |
| `ed8cc63815fe7190af6db5cefe8a83ada7bb3cdc` | 区分 HC/ABA Refresh 与 Ticket Origin |
| `f0532ffb50e4f8192f8f1354855115b3d9e13e28` | Rust ABA 完整认证握手 Connector |
| `205f808cd64150ff3c6481507c4e37528ea21c41` | 修复共享无 padding Wire fixture 解码 |
| `fa9fe7abb26ded4aee12b8690fe3cdde2407eb6d` | 分离 Browser/Native External Origin |

当前验证 SHA：

- push Foundation CI `33906015241`：`success`；
- PR Foundation CI `33906021011`：`success`；
- documentation、platform-import、protocol、aba-rust、hc-typescript 五个 Job 均成功；
- 额外锁文件检查 `33905626893`：`success`。

前序精确检查点 `b5ca753...` 的 push/PR CI `33904061894`/`33904067087` 均成功；`ed8cc63...` 的 push/PR CI `33904938640`/`33904942864` 均成功。

## 本地静态与测试证据

实际执行并通过：

```text
platform: go test ./...
aba:      cargo fmt --check
aba:      cargo clippy --all-targets --all-features -- -D warnings
aba:      cargo test --all-targets --all-features   # 21 passed
aba:      cargo metadata --locked --format-version 1 --no-deps
```

Go Gateway 测试覆盖：HC Cookie Refresh、ABA Header Refresh、传输类型隔离、DPoP nonce、原生/浏览器 Ticket Origin、错误 Origin 不消费 ABA Ticket、单次消费和完整签名 Challenge。Rust 测试覆盖 KeyStore 权限、凭据轮换、Trust 回退、无 `ath` Refresh proof、共享 Protobuf fixture 和既有密码学/AAD 向量。

## 真实本地流程

本地使用 Backend `:8080`、HC H5 `localhost:8001`、Gateway `127.0.0.1:8082`。Gateway 的 Browser External Origin 为 `http://localhost:8001`，Native External Origin 为 `http://127.0.0.1:8082`。

ABA Enrollment 第一次请求等待 300 秒后超时；用户在超时后审批，客户端没有伪造成功。第二次新 Enrollment 在内置浏览器 Admin 页面按 User Code 审批后，ABA 原子消费成功，创建 Endpoint `3f32d5624086cc6adaf7c5c4499820f2`。Admin Endpoints 页面显示类型 ABA、状态 ACTIVE、Platform `aba`、Version `0.1.0`。

随后在真实本地数据库和持久 Gateway Signer 上完成：

1. ABA 使用本地 Refresh 轮换得到新 Access/Refresh，并在写入安全状态后申请 Ticket；
2. ABA 验证 Trust Manifest 与 ServerChallenge，签名 ChallengeResponse，验证 ConnectionReady；
3. 首次 ABA READY 的 Connection Generation 为 1；在最终双 Origin 配置下再次连接为 2；
4. 内置浏览器 HC H5 同时恢复 Endpoint `dbf6d482bb…` 并到达 READY，Connection Generation 为 6；
5. 两端显示同一持久 Root JKT `UJT3InRUsX…`，ABA 连接完成后 H5 仍保持 READY；
6. `aba/.aba-dev` 与 `identity.json` 分别为 0700/0600，状态只有版本、私钥标量、轮换凭据和公开 Trust Pin 字段；Token 原值没有打印或写入文档；
7. `platform/.mss/run/private` 与 `gateway-trust.json` 分别为 0700/0600。

## 失败—修复

- 第一次真实 Enrollment 在 300 秒超时后才审批，按失败记录保留；第二次使用全新 Enrollment 后成功。
- 初次 Admin 审批页看不到未归属 ABA Enrollment；`fe08d99...` 允许可信用户列出 Pending 项，但仍用 User Code 绑定审批。
- Rust 全量测试首次把无 padding 的共享 Base64 fixture 交给要求 padding 的解码器；`205f808...` 修复后 21 个测试和 CI 通过。
- 单一 External Origin 无法同时满足 H5 Vite proxy 的 DPoP HTU 与 ABA 直连 HTU；`fa9fe7a...` 分离 Browser/Native Origin 后，同一 Gateway 下两端均到达 READY。
- `f0532ff...` 与 `205f808...` 的部分 CI 因后续检查点过快 push 被 GitHub `cancel-in-progress` 取消；没有把取消描述成成功。最终 `fa9fe7a...` 的 push/PR CI 均完整成功。

## 边界与下一阶段

- `aba connect` 当前完成一次认证握手后主动正常关闭，用于验证 Connector；常驻事件循环、Heartbeat、重连退避和业务 Control/Encrypted Packet 尚未实现；
- Gateway 尚无活动 Connection Directory、旧连接 Kick 和跨实例 Fencing；
- READY 后业务 Packet 仍明确失败关闭；Session Control、HPKE/KDF/AEAD、ACP Test Agent、Relay/ACK/Journal/Resume 和在线吊销闭环属于后续 M3/M4；
- 当前 Signer/KeyStore 是明确的本地开发实现，不是生产 KMS/HSM 或系统 Keychain 证明。

因此本记录只把 ABA Enrollment 与认证 Connector 标记为已验证，不把整个 M2、M3 或完整 MVP 标记为完成。

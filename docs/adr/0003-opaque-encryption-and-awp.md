# ADR-0003：默认 Opaque 加密与独立 AWP

- **状态**：Accepted
- **日期**：2026-09-03
- **决策所有者**：Harness Platform 项目
- **取代**：无
- **被取代**：无

## 背景

HC 与 ABA 之间传递的 ACP JSON-RPC 可能包含 Prompt、源代码、Diff、终端输出、工具参数和权限请求。若只使用 TLS 到 Platform，再由 Platform 明文中继，Platform、反向代理后的业务进程和数据库会成为高价值明文集中点。

同时 ACP 本身主要定义 Agent/Client JSON-RPC 语义，并不提供本项目需要的 Endpoint 注册、证书、Token 持有证明、WebSocket Ticket、连接 Fencing、密钥分发、ACK、离线重放和本地执行白名单。

## 决定

### 默认 Opaque Mode

ACP JSON-RPC 在 HC 与 ABA 之间端到端应用层加密。Platform 默认只能处理路由、顺序、ACK、重放、配额和审计元数据，不能获得 SRK 或 ACP 明文。

### 独立 AWP

定义版本化 ABA Wire Protocol（AWP），在不改变 ACP Payload 的前提下提供：

- Endpoint Credential、DPoP、Ticket 和 WebSocket Challenge。
- Binary Protobuf Packet。
- Session、Channel、Direction、Sequence、Generation 和 Key ID。
- HPKE Key Package。
- AEAD Encrypted Frame 和 Endpoint Signature。
- ACK、Resume、Replay、Backpressure、Error Code 和状态机。

AWP v1 保留完整 ACP Transport Frame：一个单独 JSON-RPC 值或一个完整 Batch Array。

## 密钥决定

- 每个 Endpoint 本地生成独立 Signing Key 和 KEM Key。
- Platform 签发 AEC/HEC，但不生成或保存 Endpoint 私钥。
- ABA 为每个 ACP Session/Generation 生成 SRK。
- ABA 使用每个 HC 的 KEM 公钥独立生成 HPKE Key Package。
- 从 SRK 派生每个 HC、每个方向、每个 Generation 的 Direction Key。

首发 Crypto Suite：

```text
MSS-AWP-SUITE-0001
SHA-256
ECDSA P-256 / ES256 (P1363 Low-S)
RFC 7638 JWK Thumbprint
HPKE DHKEM(P-256, HKDF-SHA256) + AES-256-GCM
Session AES-256-GCM
```

AWP v1 Canonical AAD 使用协议文档定义的固定 148 字节编码。Nonce 为独立随机 4 字节前缀与 64-bit Sequence 的组合。

TLS 1.3 仍然强制使用；应用层加密不替代 TLS。

## 选项

### 选项 A：只使用 TLS，Platform 明文中继

拒绝作为默认模式。实现简单，但 Platform 可读取全部敏感内容，数据库/日志泄露影响巨大。

### 选项 B：Platform 生成共享证书和私钥给所有端点

拒绝。端点可互相冒充、无法独立吊销，任意一个私钥泄露影响全部用户端点，也不能称为真正 E2EE。

### 选项 C：每个消息直接使用 Endpoint 公钥加密

拒绝作为数据面。高频公钥加密开销大，难以支持双向流式、轮换和离线重放。采用 HPKE 只封装 Session Key，数据面使用 AEAD。

### 选项 D：复用 ACP JSON-RPC 添加自定义加密字段

拒绝。会污染 ACP Schema、破坏与官方 SDK 的透明兼容，并迫使 Platform 理解 ACP Method。

### 选项 E：独立 AWP + Opaque Session

接受。把身份、加密和可靠性放在 ACP Transport 外层，ACP Payload 保持原样。

## 理由

- Platform 默认不成为 ACP 明文托管方。
- Endpoint 可独立吊销和轮换。
- HPKE 适合向指定 HC 封装 Session Key，AEAD 适合高频流式数据。
- 独立 AWP 允许 Platform 进行路由和可靠性处理而不解析 ACP。
- 固定 Canonical AAD 和 Golden Vector 能确保 Rust、Go、TypeScript 互操作。
- P-256/AES-GCM 首发优先覆盖 Rust、浏览器 WebCrypto 和小程序可移植性。

## 后果

正面：

- 数据库、缓存和 Platform 业务进程默认无法读取 ACP 内容。
- ACP 新 Method/扩展字段通常无需 Platform 更新。
- 密文可以安全离线存储和重放。
- 安全边界、算法和状态机可独立测试。

成本：

- 三端密码学和协议互操作复杂。
- Platform 无法默认提供明文搜索、服务端摘要和内容审核。
- 新 HC 加入 Session 需要 ABA 为其生成 Key Package。
- 历史恢复需要用户持有恢复密钥或仍有授权端点。
- Platform 仍能看到时间、大小、Endpoint 和 Session 路由元数据。

## Managed Mode

未来可提供用户显式开启的 Managed Mode：Platform 通过独立 KMS Grant 和专用 Worker 受控解封 Session Key，用于企业审计或搜索。

该模式：

- 默认关闭。
- 必须二次认证和明确 UI 提示。
- 每次解密产生审计。
- 普通 API/数据库不能直接拿明文 Key。
- 不能宣传为端到端加密。

## 安全与隐私影响

- Access/Refresh Token 仍使用 DPoP 绑定，避免 Token 被窃后直接重放。
- WebSocket 使用单次 Ticket，并在升级后再次验证 Endpoint 私钥。
- Frame Header 进入 AAD，Ciphertext 与 AAD 摘要由 Endpoint 签名。
- Sequence/Nonce 不能回退；状态损坏时轮换新 Generation。
- Platform 日志、Trace、指标和错误禁止记录 ACP 明文或 Key Material。
- Opaque 不隐藏流量元数据；文档和用户界面需要诚实说明。

## 可靠性影响

Platform 提供至少一次密文 Frame 交付，Endpoint 去重。ACK 只表示接收端已安全持久接收，不表示本地 ACP 副作用完成。

ABA 在 `DISPATCH_STARTED` 后崩溃且无法证明执行结果时进入 `UNCERTAIN`，不自动重发高风险操作。

## 兼容与迁移

- AWP Major 与 ACP Version 独立。
- AWP v1 发布后破坏 AAD、Frame 或状态语义的变更进入新 Major。
- 增加新 Crypto Suite 通过明确 Suite ID 和能力协商，不能替换 Suite 0001 的含义。
- ACP draft v2 后续支持必须独立 Feature/ADR，不影响稳定 ACP v1 Payload。
- Managed Mode 不改变 Opaque Session 的默认和既有密钥边界。

## 验证

- Rust/Go/TypeScript 对同一 148 字节 AAD、HKDF、HPKE、AES-GCM 和 ES256 Golden Vector 结果一致。
- Platform 数据库和日志不能搜索到测试 Prompt/ACP Method。
- 只有目标 HC KEM Key 能解封 Key Package。
- 篡改 Header、Ciphertext、Signature、Generation 或 Recipient 均失败。
- Token 被窃但无 Endpoint Signing Key 时 API/WSS 失败。
- Ticket 重放、Challenge 重放、Sequence 重放和 Manifest 回滚失败。
- ACP Request/Response/Notification/Batch 字节和语义不被 Platform 改写。
- 吊销 HC 后无法获取新 Generation，也不能读取吊销后 Frame。

## 参考

- `docs/architecture/SECURITY.md`
- `docs/architecture/PROTOCOL.md`
- `docs/roadmap/VERIFICATION.md`
- RFC 5869、7638、9180、9449、8446

# Harness Platform 项目记忆

- **状态**：Canonical project memory
- **最后结构化更新**：2026-09-03
- **用途**：供新会话、AI Agent 和开发者快速恢复项目事实；具体提交与验证记录见 `work-log.md`。

## 1. 项目身份

```text
repository: mss-boot-ai/harness-platform-monorepo
project:    Harness Platform
visibility: private at repository initialization
main:       design and long-term memory baseline
```

项目用于构建一个可从 Web、微信小程序和后续原生客户端安全控制本地 ACP 编程 Agent 的平台。

## 2. 已确定名称

- **Platform**：基于 mss-boot-admin 的后台控制面、身份中心、证书中心、加密中继、持久化和运营平台。
- **ABA**：`acp-brige-agent` 的简称；Rust 编写的轻量本地 ACP 桥接与安全边界。
- **HC**：H 端客户端统称，包括 Web、微信小程序、原生 App 和桌面端。
- **AWP**：ABA Wire Protocol，HC/Platform/ABA 之间的版本化外层协议。

重要：当前正式名称的拼写是 `acp-brige-agent`，不是 `acp-bridge-agent`。除非通过 ADR 改名，代码、包、制品和文档继续使用已确认拼写。

## 3. Platform 固定基线

```text
upstream repository: mss-boot-io/mss-boot-admin
release tag:         v1.3.7
annotated tag object: 41c6517950f7f5f642418f5d4a49386e9c200b15
peeled source commit: 77b53d41092741eac62fa6418c0bdbf87413c7cd
Go version:           1.26.6
license:              MIT
```

Platform 不跟随上游 `main`，不使用 `latest` 或浮动分支。升级必须通过 ADR、精确提交、迁移、验证和回滚流程。

当前首选引入方式：把精确上游源树以可追溯的 vendored/subtree-style 方式放入 monorepo 的 `platform/`，同时保留上游 License 和 `.upstream` 锁文件。实际首次导入完成与验证状态必须查看工作日志，不能仅根据设计推断。

## 4. ACP 与 ABA 基线

```text
ACP wire compatibility: stable ACP v1
ABA implementation:     Rust
official SDK:            agentclientprotocol/rust-sdk 2.0.x
version lock:            exact Cargo.lock
ACP draft v2:            disabled by default, experimental only
```

官方 Rust SDK 2.0 的 SDK API 有破坏性变化，但稳定 ACP v1 Wire Schema 保持不变。ABA 必须保持 ACP Request、Response、Notification 和 Batch 的语义与边界。

## 5. 核心架构决定

```text
HC <— TLS 1.3 + encrypted AWP —> Platform <— TLS 1.3 + encrypted AWP —> ABA
                                                                        │
                                                                        └— local ACP v1 transport —> Agent
```

- ABA 默认只主动出站，不开放公网或局域网入站端口。
- Platform 承担全部重控制面：用户、租户、RBAC、Endpoint、证书、轮换、路由、密文存储、ACK/Replay、审计、通知和任务。
- ABA 只保留本地不可外移职责：端点私钥、本地 Runtime/Workspace 白名单、ACP 子进程、加解密、协议桥接和最小 Journal。
- HC 同时承担用户界面和端到端加密端点。
- Platform 的 ACP Gateway 与 mss-boot-admin 现有通知 WebSocket Hub 分离。

## 6. 身份模型

每个 ABA 和每个 HC 安装实例都是独立 Endpoint：

```text
Endpoint Signing Key  -> DPoP, WS challenge, frame/control signatures
Endpoint KEM Key      -> HPKE key package
Endpoint Credential   -> AEC or HEC, signed by Platform/UCA
Endpoint Status       -> pending/active/suspended/revoked/expired/compromised
```

- 两套 Endpoint Key 都在端点本地生成。
- Platform 不生成、不接收、不保存端点私钥。
- 不同 Endpoint 不共享私钥或证书。
- 用户身份、Endpoint 身份、当前权限和资源策略必须同时成立。

有效权限：

```text
Current User RBAC
∩ Endpoint Credential Scopes
∩ Access Token Scopes
∩ Endpoint Status Policy
∩ Session ACL
∩ Runtime/Workspace Authorization
```

ABA Token、HC Token 和普通 Admin User Session 不能互换。

## 7. Enrollment 与连接

### ABA

- 使用类似 Device Authorization 的短期 Device Code/User Code 流程。
- 用户在 Platform/可信 HC 审批并核对 Endpoint/公钥指纹。
- Platform 只保存 Code 哈希。
- 凭据领取还需要证明 ABA Signing Key/KEM Key 持有。

### HC

- 先完成 Platform Human Session。
- 本地生成独立 Signing/KEM Key。
- 对一次性 Challenge 签名，获得 HEC。

### WebSocket

- 先通过带 DPoP 的 HTTPS 获取短期单次 Ticket。
- Ticket 通过 `Sec-WebSocket-Protocol` 携带，不把长期 Token 放在 URL。
- Ticket 绑定 Human Session（HC）、Endpoint、Credential、Signing JKT、Origin/Context 和 Purpose。
- WSS Upgrade 后必须完成 Endpoint 私钥首帧 Challenge，才进入 READY。
- 新连接用 Connection Generation/Fencing 使旧连接失效。

## 8. 加密模型

默认模式：**Opaque Mode**。

- ABA 为每个 ACP Session/Generation 生成 SRK。
- ABA 使用每个 HC 的 KEM 公钥，通过 HPKE 分别封装 Key Package。
- Platform 保存并转发 Key Package，但不能解封 SRK。
- 双向 Session Key 通过 HKDF 做 Session、Generation、Endpoint 和方向域分离。
- ACP JSON-RPC 原始 UTF-8 字节通过 AES-256-GCM 加密。
- Canonical AAD 为固定 148 字节。
- Encrypted Frame 使用端点 ES256 P1363 Low-S 签名。
- TLS 1.3 仍然必需，应用层加密不替代 TLS。

首发 Suite：

```text
MSS-AWP-SUITE-0001
SHA-256
ECDSA P-256 / ES256
RFC 7638 JWK Thumbprint
HPKE P-256 + HKDF-SHA256 + AES-256-GCM
Session AES-256-GCM
```

Managed Mode 是未来可选受控解密模式，必须由用户显式启用、独立审计，并明确不属于端到端加密。

## 9. Runtime 与 Workspace 安全边界

Platform/HC 只能向 ABA 提交：

```text
runtime_profile_id
workspace_id
session_id
requested ACP capabilities
authorization revision
```

不能提交：

```text
command
args
cwd
env
shell script
binary URL
arbitrary local path
```

真实命令、参数、工作目录、环境变量和允许关系只在 ABA 本地配置。未知 ID、重复 ID、路径逃逸、Symlink 越界和 Revision 回滚均失败关闭。

## 10. AWP v1 关键事实

- WSS Binary Message 承载一个 Protobuf `WirePacket`。
- WebSocket Subprotocol：`mss.awp.v1`。
- ACP Payload 是一个完整原始 JSON 值：单 Request/Response/Notification 或完整 Batch Array。
- Platform 不解析或重构 ACP Payload。
- 每个方向独立 Sequence 和 Direction Key。
- Nonce：4 字节随机前缀 + 8 字节大端 Sequence。
- Encrypted Frame 重放时原始 ID、Sequence、AAD、Ciphertext 和 Signature 完全不变。
- ACK 表示接收端已安全持久接收，不表示 ACP 副作用完成。

## 11. 可靠性模型

Platform 提供至少一次密文 Frame 交付；Endpoint 通过 Message ID/Channel/Sequence 去重。ABA Journal 最小状态：

```text
RECEIVED
 -> DISPATCH_STARTED
 -> DISPATCHED
 -> RESPONDED

DISPATCH_STARTED + crash + cannot prove outcome
 -> UNCERTAIN
```

- 收到并持久化 `RECEIVED` 后才 ACK Platform。
- `DISPATCHED` 不重复交给 ACP Agent。
- `UNCERTAIN` 不自动重试，必须在 HC 明确显示。
- Journal 默认设计上限 64 MiB；达到硬限制拒绝新高成本操作但保留控制/关闭能力。

## 12. 轮换模型

独立处理：

- Platform Root/UCA Rotation。
- Endpoint Signing/KEM Key 与 AEC/HEC Rotation。
- Access/Refresh Token Rotation。
- ACP Session Key Generation Rotation。

Session Key：

```text
PENDING -> DISTRIBUTING -> READY -> ACTIVE
-> GRACE -> RETIRED -> DESTROYED
```

同一 Session 最多一个 Active Generation 和一个下一代。吊销 HC 后撤销 Token/Ticket/连接、禁止新 Key Package，并触发相关 Session 的新 Generation。

## 13. mss-boot-admin 复用点

v1.3.7 已核验存在：

- 服务端 User Session 和当前 Principal/Role 重新加载。
- Browser Session 与 PAT 边界。
- 受信任 Origin 的单次 WebSocket Ticket 模式。
- System Schedule 与持久化用户任务分离的 Task Server。
- 用户、RBAC、审计、通知、GORM、配置和 Ant Design v6 管理端基础。

ACP 会扩展这些基础，但不把 Endpoint 身份混入普通人类 JWT，也不把 ACP Binary Gateway 塞进通知 Hub。

## 14. 计划中的 Monorepo

```text
AGENT.md
README.md
docs/
protocol/
aba/
platform/
hc/
deploy/
scripts/
```

- `docs/`：全部长期设计、决策、记忆、计划和验证证据。
- `protocol/`：Proto、JSON Schema、测试向量和兼容契约。
- `aba/`：Rust `acp-brige-agent`。
- `platform/`：mss-boot-admin v1.3.7 源码和 ACP 扩展。
- `hc/`：共享 TypeScript 核心、Web 和小程序。

实际存在文件与实现状态以远端分支和 `work-log.md` 为准。

## 15. Git 与工作纪律

- 初始设计和记忆提交到 `main`。
- 完整基线后从最新 `main` 创建 `codex/bootstrap-harness-platform-foundation`。
- 功能代码不直接提交到 `main`。
- 每个可恢复代码/文档检查点先 commit 并 push，再测试或做下一项。
- 测试发现问题通过新提交修复，不 rebase/force-push 已共享历史。
- 未经用户明确授权不自动合并功能分支。
- 状态必须区分 Designed/Written/Pushed/Built/Tested/Browser/Device/Security。

## 16. 当前阶段

当前处于 Phase 0/Phase 1 交界：

- 设计、PRD、安全、协议、Platform、ABA、HC、路线和验证文档正在/已经建立于 `main`。
- 完整设计基线完成后才创建功能分支。
- 首个功能分支只做 Monorepo、上游锁/导入、AWP Schema、ABA Rust 基础、HC/Platform 边界和 CI 骨架。
- 不能把骨架或 push 描述为完整远程 Agent 平台已可用。

## 17. 未决但已登记事项

需要后续 ADR/Spike 确认：

- vendored 上游实际导入和长期同步工具细节。
- Platform 多实例 Gateway 跨实例路由实现。
- 微信小程序密码学和安全存储真实能力。
- 密文 Frame 生产存储与分区方案。
- Root/UCA 生产 KMS/HSM Provider。
- Windows ABA 完整 Secure Store/Job Object 支持。
- Opaque 历史恢复公钥功能。

这些未决项不得由临时代码默认值偷偷冻结。

## 18. 更新要求

当名称、基线、信任边界、协议、密码学、状态机、目录、当前阶段或关键未决项变化时，在同一检查点更新本文件。提交和验证明细只写入 `work-log.md`，避免本文件变成流水账。
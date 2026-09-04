# Harness Platform MVP 产品需求文档

- **版本**：0.1
- **状态**：Accepted for implementation
- **日期**：2026-09-04
- **目标分支**：`codex/bootstrap-harness-platform-foundation`
- **Platform 基线**：`mss-boot-admin v1.3.7` Thin Host import 模式
- **协议**：AWP v1 / ACP Stable v1

## 1. 产品定义

Harness Platform MVP 让一名已登录用户可以在浏览器 HC 中，安全连接自己电脑或服务器上的 ABA，并通过 ABA 使用一个本地 ACP Agent。Platform 负责身份、配对、会话控制、密文中继、可靠投递和管理；Platform 默认不能读取 ACP Prompt、源码、Tool 参数或 Agent 响应。

MVP 必须形成一个真实、可重复的垂直闭环：

```text
用户登录 Platform
→ ABA 本地生成独立密钥并发起配对
→ 用户在 Platform 审批 ABA
→ 浏览器 HC 生成自己的独立密钥并注册
→ HC 选择 ABA + Runtime + Workspace 创建 Session
→ ABA 只按本地白名单启动测试 ACP Agent
→ ABA 给 HC 分发加密 Session Key Package
→ HC 与 ABA 通过 Platform 交换一轮端到端加密 ACP 请求/响应
→ 任一端断线后按 ACK Cursor 恢复未确认原 Frame
→ 用户吊销 HC 或 ABA，旧连接和新访问立即失效
```

## 2. 产品假设

MVP 要验证四个核心假设：

1. 用户愿意安装一个足够轻、只主动出站连接的 ABA，以换取远程使用本地 Agent 的能力。
2. Platform 可以只理解身份、路由和可靠性元数据，而不读取 ACP 内容。
3. mss-boot-admin Thin Host 能承载管理控制面，同时保持 Foundation 后续升级能力。
4. 端点独立密钥、一次性 WSS Ticket、连接挑战和本地白名单可以把远程 Agent 控制限制在明确安全边界内。

## 3. MVP 用户与角色

### 3.1 Owner

- 使用 mss-boot-admin 现有登录、用户和服务端 Session；
- 审批或拒绝 ABA Enrollment；
- 注册、查看、暂停和吊销 HC/ABA Endpoint；
- 创建、查看和关闭自己的 ACP Session；
- 查看密文投递、积压、Gap、Rekey 和 `UNCERTAIN` 状态；
- 不能通过 Platform 下发任意本地命令。

### 3.2 Platform Administrator

MVP 中可与 Owner 是同一人，用于配置运行环境、查看脱敏健康与审计。管理员权限不等于 Opaque 内容解密权限。

### 3.3 ABA

- 本地生成并保存 E-SIG/E-KEM；
- 主动连接 Platform；
- 校验本地 Runtime/Workspace/Capability 白名单；
- 启动和回收本地 ACP 进程；
- 生成 Session Root Key；
- 加解密 ACP Frame；
- 维护有界 Sequence/Dispatch Journal。

### 3.4 HC

- 浏览器或 Node 参考客户端本地生成独立 E-SIG/E-KEM；
- 获取发送者约束 Token 和单次 WSS Ticket；
- 创建 Session、接收 Key Package、发送和接收 ACP Frame；
- 展示连接、Session、Gap、Permission 和错误状态。

## 4. 范围

### 4.1 必须交付

- Platform Thin Host 前后端 import 基线；
- Harness Platform 管理业务模块；
- 独立数据面 Gateway；
- ABA Enrollment、HC Registration、Endpoint Credential 与吊销；
- DPoP 风格发送者约束 HTTP 请求；
- 短期、单次 WSS Ticket 和首帧 E-SIG Challenge；
- 一个 ABA、一个 HC、一个 Session；
- ABA 本地 Runtime/Workspace ID 白名单；
- 一个确定性测试 ACP Agent；
- ABA 生成 Generation 1 SRK，并以 HPKE Suite 0001 给 HC 包装；
- AWP v1 148 字节 Canonical AAD；
- AES-256-GCM ACP Frame 与 Endpoint Signature；
- Platform 密文持久化、幂等写入、ACK Cursor、Gap 和原 Frame Replay；
- ABA 有界 Journal、Sequence 使用前持久化和 `UNCERTAIN`；
- Endpoint 吊销关闭连接并阻止新 Ticket/Key Package；
- Admin Web 中的 Overview、Enrollment、Endpoint、Session 和 Delivery 页面；
- 可重复本地启动、CI E2E、Opaque Canary 和版本化验证报告。

### 4.2 明确不在 MVP

- 微信小程序正式版；
- 多人协作或多个 HC 同时控制同一 Session；
- Managed/服务端解密模式；
- 多 Region、多 Gateway 实例和跨节点连接目录；
- 完整 Root/UCA 自动轮换；
- 生产级 KMS/HSM 集成；
- 任意 Shell、任意可执行文件、远程路径或环境变量下发；
- Artifact 大文件通道、服务端全文检索和内容摘要；
- 自动更新 ABA；
- 商业计费与配额套餐。

这些项目不得用未验证占位实现冒充完成。

## 5. 技术与升级基线

### 5.1 Platform

```text
backend:  github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7
frontend: @mss-boot-io/admin-web@1.3.7
source:   77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:       1.26.6
Node:     24.x
pnpm:     10.34.5
```

Platform 只能在 Thin Host 业务接缝中开发。MVP 完成时，针对同一 v1.3.7 执行 `mss upgrade admin` 只读计划必须为 no-op，且业务自有文件原字节保留。

### 5.2 ABA

- Rust 1.88.0；
- `agent-client-protocol = 2.0.0`，Stable v1；
- 默认 `unsafe_code = forbid`；
- 依赖锁、格式、Clippy 和测试全部通过；
- MVP 支持 Linux，macOS/Windows 只要求可编译边界和文档，不宣称真机验证。

### 5.3 HC

- TypeScript strict；
- 浏览器 WebCrypto 为主实现；
- Node 24 参考 Runner 使用同一协议/加密代码执行 E2E；
- 私钥不得进入 Cookie、LocalStorage、URL 或普通日志。

## 6. 系统拓扑

```text
┌──────────────── mss-boot-admin Thin Host ────────────────┐
│ Admin Server: Human Session / RBAC / Management API/UI   │
│ Harness Module: Endpoint / Enrollment / Session views    │
└───────────────────────┬───────────────────────────────────┘
                        │ shared domain/store
┌───────────────────────▼───────────────────────────────────┐
│ Harness Gateway: endpoint auth / ticket / WSS / relay    │
│ Platform sees canonical AAD, ciphertext and signatures   │
└───────────────┬──────────────────────────────┬─────────────┘
                │ TLS/WSS                     │ TLS/WSS
        ┌───────▼────────┐             ┌───────▼────────┐
        │ HC Web/Runner  │             │ ABA Rust       │
        │ E-SIG/E-KEM    │             │ E-SIG/E-KEM    │
        └────────────────┘             └───────┬────────┘
                                               │ local ACP stdio
                                       ┌───────▼────────┐
                                       │ Test ACP Agent │
                                       └────────────────┘
```

Admin Server 与 Gateway 是同一 Go Module 的两个命令，可以在 MVP Compose 中分进程部署。它们共享数据库模型和领域服务，但 Gateway 不复用普通通知 WebSocket Hub。

## 7. 用户故事与验收

### US-001：启动 Platform

作为部署者，我可以使用一条 Compose 命令启动 Admin、Gateway、数据库和必要依赖。

验收：

- 所有服务有健康检查；
- 缺少生产必需配置时 Fail Closed；
- 默认开发配置不包含提交到仓库的真实 Secret；
- Admin Web 可访问并显示 Harness 入口。

### US-002：ABA Enrollment

作为 Owner，我可以在 ABA 终端看到短 User Code，并在 Platform 页面审批。

验收：

- ABA 本地生成独立 E-SIG/E-KEM；
- Platform 只接收公钥；
- Device Code 和 User Code 原值只返回一次，数据库只保存哈希；
- Enrollment 具有 `PENDING → APPROVED → CONSUMED`，并支持 DENIED/EXPIRED；
- Consume 是原子单次操作；
- ABA 获得发送者约束 Credential，而不是可独立使用的裸 Token。

### US-003：HC 注册

作为 Owner，我首次打开 HC 时可以注册此浏览器端点。

验收：

- HC 生成与 ABA 不同的 E-SIG/E-KEM；
- 注册绑定当前 Human Session、Challenge 和 Origin；
- HC Endpoint 可在管理页面单独吊销；
- 相同公钥 Thumbprint 不能创建第二个活动 Endpoint。

### US-004：创建 Session

作为 Owner，我可以从允许的 ABA、Runtime Profile 和 Workspace 中创建 Session。

验收：

- Platform 请求只包含稳定 ID；
- 请求包含 `command/args/cwd/env/shell/script/localPath` 时拒绝；
- ABA 必须再次命中本地 Runtime 与 Workspace 白名单；
- Symlink/realpath 逃逸被拒绝；
- ABA 离线时返回稳定、可诊断且不泄露内部路径的错误。

### US-005：加密 ACP 请求/响应

作为 Owner，我可以在 HC 发送一条 ACP Prompt，并收到测试 ACP Agent 的响应。

验收：

- ABA 生成 32 字节 SRK；
- Platform 只保存面向 HC 的加密 Key Package；
- HC 与 ABA 派生方向隔离的 AES-256-GCM Key；
- AWP AAD 恰好 148 字节；
- Platform 可验证 Endpoint Signature，但不能解密 Payload；
- 测试 Agent 收到完整 ACP Transport Frame 并返回完整 Response；
- Prompt 与 Response Canary 在 Platform DB、日志和 Audit 中零明文命中。

### US-006：可靠恢复

作为用户，网络短暂中断后会话可以恢复未确认消息。

验收：

- 每方向 Sequence 从 1 单调递增；
- Sequence 在使用前持久化；
- 相同 Sequence 只允许相同原始 Frame；
- ACK 只在接收端安全持久化后推进；
- 重连重放原 Frame，不重新加密；
- `DISPATCH_STARTED` 后崩溃且执行结果不确定时进入 `UNCERTAIN`，不会自动重复 Tool 副作用。

### US-007：吊销

作为 Owner，我可以吊销 HC 或 ABA。

验收：

- 新 Token、Ticket、WSS 和 Key Package 立即被拒绝；
- 当前连接在目标 SLO 内关闭；
- HC 吊销使相关 Session 进入 `REKEY_REQUIRED/CLOSED`；
- ABA 吊销关闭其 Session；
- 审计只记录动作、主体、对象和结果，不记录 ACP 内容。

## 8. 状态模型

### 8.1 Endpoint

```text
PENDING → ACTIVE → SUSPENDED → ACTIVE
                   └──────────→ REVOKED
ACTIVE ───────────────────────→ REVOKED
```

REVOKED 不可恢复，只能重新 Enrollment。

### 8.2 Enrollment

```text
PENDING → APPROVED → CONSUMED
   ├────→ DENIED
   └────→ EXPIRED
```

### 8.3 Session

```text
CREATING → WAITING_KEY → ACTIVE → DRAINING → CLOSED
   │             │          ├────→ REKEY_REQUIRED
   │             │          ├────→ UNCERTAIN
   └─────────────┴──────────→ FAILED
ACTIVE ─────────────────────→ ABA_REVOKED
```

### 8.4 Frame

```text
STORED → ROUTED → RECEIVER_ACKED → EXPIRED
   └────────────→ CONFLICT
```

## 9. 数据模型

MVP 表：

- `harness_endpoints`：Owner/Tenant、类型、名称、Public JWK、JKT、状态、最后在线；
- `harness_enrollments`：Code Hash、请求公钥、状态、审批/消费/过期时间；
- `harness_endpoint_credentials`：Token Hash、JKT、Scope、有效期、吊销；
- `harness_sessions`：ABA/HC、Runtime/Workspace ID、状态、Generation；
- `harness_session_key_packages`：Recipient、Generation、HPKE 密文和 ABA Signature；
- `harness_ws_tickets`：Ticket Hash、Endpoint、Purpose、Origin、Expiry、Consumed；
- `harness_frames`：原始 148 字节 AAD、Ciphertext、Signature、Sequence、Hash、状态；
- `harness_ack_cursors`：Session/Generation/Direction 的最高连续 ACK；
- `harness_dispatch_records`：ABA 本地持久化，不在 Platform；
- `harness_audit_events`：脱敏安全事件；
- `harness_idempotency_records`：写 API 幂等结果。

强制唯一约束：

```text
endpoint JKT within owner
credential token hash
active enrollment code hash
message_id
(session_id, generation, sender_endpoint_id, sequence)
(session_id, generation, recipient_endpoint_id) key package
idempotency key within actor + operation
```

## 10. API 契约

### 10.1 Human 管理 API

挂载在 Admin 已保护 `/api` 组：

```text
GET  /harness/v1/overview
GET  /harness/v1/enrollments
POST /harness/v1/enrollments/{id}/approve
POST /harness/v1/enrollments/{id}/deny
GET  /harness/v1/endpoints
POST /harness/v1/endpoints/{id}/suspend
POST /harness/v1/endpoints/{id}/revoke
GET  /harness/v1/sessions
POST /harness/v1/sessions/{id}/close
GET  /harness/v1/sessions/{id}/delivery
```

### 10.2 Endpoint HTTP API

Gateway：

```text
POST /gateway/v1/enrollments
GET  /gateway/v1/enrollments/{id}
POST /gateway/v1/enrollments/{id}/consume
POST /gateway/v1/tokens/refresh
POST /gateway/v1/ws/tickets
POST /gateway/v1/sessions
GET  /gateway/v1/sessions/{id}
GET  /gateway/v1/key-packages
POST /gateway/v1/key-packages/{id}/ack
GET  /gateway/v1/health
```

HC Web 首次注册使用受 Admin Browser Session、CSRF、可信 Origin 和 `harness:operate` 保护的 Human API：

```text
POST /admin/api/harness/v1/hc/challenges
POST /admin/api/harness/v1/hc/endpoints
```

注册成功后立即切换到 Endpoint Token + DPoP；Gateway 不接受 Admin Cookie。详见 ADR-0005。

除 Enrollment Start/Poll/Consume 的严格特例外，Endpoint API 使用 `Authorization: DPoP <token>` 和 `DPoP` JWS。验证至少覆盖 `htu`、`htm`、`iat`、`jti`、`ath`、Server Nonce、Endpoint JKT 与 Replay Cache。

### 10.3 WSS

```text
GET /gateway/v1/ws
Sec-WebSocket-Protocol: mss.awp.v1, mss.ticket.<opaque>
```

Ticket 原值不放 URL；服务端只选择 `mss.awp.v1`，不回显 Ticket。Ticket 默认 30 秒、哈希保存、单次原子消费，并绑定 Endpoint、Credential、Origin/Purpose 和协议版本。Upgrade 后必须完成 Server Challenge；READY 前业务 Frame 被拒绝。

## 11. 密码学契约

MVP Suite 0001：

```text
Endpoint signature: ECDSA P-256 / SHA-256 / fixed 64-byte P1363 / low-S
JWK identity:       RFC 7638 SHA-256 thumbprint
Key package:        HPKE Base P-256 / HKDF-SHA256 / AES-256-GCM
Session KDF:        HKDF-SHA256, independent HC→ABA and ABA→HC keys
Frame AEAD:         AES-256-GCM
Frame AAD:          canonical 148-byte AWP v1 value
Transport:          TLS 1.3 + WSS binary
```

Nonce：

```text
nonce = generation_direction_prefix[4] || uint64_be(sequence)
```

任何 Sequence 回退、Journal 损坏或 Prefix 不确定都必须切换新 Generation，不能猜测恢复。

## 12. 管理与 HC 体验

### 12.1 Admin Web

- Overview：在线 ABA、活动 Session、未 ACK Frame、失败/吊销；
- Enrollments：User Code、请求设备、过期倒计时、审批/拒绝；
- Endpoints：类型、名称、状态、JKT 缩略、版本、最后在线、暂停/吊销；
- Sessions：ABA、Runtime、Workspace、状态、Generation、积压、关闭；
- Delivery：Sequence、ACK、Gap、重放次数、冲突和 `UNCERTAIN`；
- Audit：安全事件元数据，不展示 ACP 内容。

所有页面必须覆盖 Loading、Empty、Error、Forbidden 和正常状态，并提供中英文菜单文案。

### 12.2 HC

- 首次 Endpoint 注册；
- ABA/Runtime/Workspace 选择；
- Session 连接状态；
- Prompt/Response 时间线；
- Permission Request 确认；
- Gap/重连/吊销/`UNCERTAIN` 明确提示；
- 默认不提供服务端历史全文搜索。

## 13. 非功能要求

- 单 Gateway 实例目标：1,000 空闲连接、100 活跃 Session 的有界验证档；
- 典型 4–64 KiB Frame，单 Frame 最大 1 MiB；
- 连接、Session、队列、未 ACK 字节、重试和 Journal 均有上限；
- 一个慢消费者不能阻塞其他连接；
- 所有写操作有 Idempotency Key 或协议幂等键；
- 错误使用稳定 Code 和安全 Message；
- 健康检查不泄露密钥、路径或用户内容；
- 数据保留默认仅保存密文 24 小时或至 ACK 后短 Grace，配置有上限；
- MVP CI 不依赖真实用户、真实云凭据或生产 Secret。

## 14. 部署

MVP 提供：

- `docker compose` 本地演示；
- Platform Admin 与 Gateway 独立容器/进程；
- SQLite 确定性 CI 配置；
- PostgreSQL 作为首个部署候选，Repository 不绑定 SQLite 特性；
- ABA 本地二进制和示例配置；
- HC Web 构建和 Node 参考 Runner；
- 测试 ACP Agent。

生产安全模式不得自动生成并明文持久化 Root/Token Secret。开发模式生成的材料必须放在忽略目录并标记不可用于生产。

## 15. 验证矩阵

### 15.1 每个提交

- 文档/协议结构；
- Go test/vet；
- Rust fmt/clippy/test locked；
- TypeScript lint/typecheck/test；
- Thin Host import 合同；
- Secret/Canary 静态扫描。

### 15.2 MVP E2E

必须实际执行并记录：

1. 启动数据库、Admin、Gateway；
2. 启动 ABA + 测试 ACP Agent；
3. ABA Enrollment Start；
4. Owner 审批；
5. ABA Consume；
6. HC 注册；
7. 双端 Ticket + WSS Challenge；
8. 创建 Session；
9. ABA 本地 Allowlist 接受；
10. Key Package 分发和 ACK；
11. 一轮加密 ACP Request/Response；
12. 强制 HC 断线并恢复原 Frame；
13. 验证重复 Sequence 同内容幂等、不同内容冲突；
14. 吊销 HC 并证明新 Ticket/WSS 被拒绝；
15. 扫描 Platform DB、日志、Audit 不含 Canary 明文。

### 15.3 负向安全

- Token 无私钥；
- DPoP JTI 重放；
- Ticket 并发消费；
- Challenge 重放；
- Header/Ciphertext/Signature 单字节篡改；
- 未知 Critical Flag；
- 错误 Sender/Receiver/Session/Generation；
- Sequence 重放冲突；
- Runtime/Workspace 未授权；
- 路径逃逸；
- 超大 Frame/队列满；
- 被吊销 Endpoint；
- Platform DB 离线尝试解密失败。

## 16. Definition of Done

MVP 只有同时满足以下条件才完成：

- 本文所有 Must 需求有实现或明确测试证据；
- Thin Host 使用精确 1.3.7 imports，升级 no-op；
- Platform、ABA、HC、协议和测试 Agent 代码均已 push；
- 所有声明通过的命令由 CI 或当前执行者实际运行；
- E2E 垂直链路通过；
- Opaque Canary 零命中；
- 没有裸私钥、固定生产 Secret、任意命令通道或无界队列；
- 失败与修复分别有不可改写的 Commit；
- 版本化验证报告包含分支、完整 SHA、工具链、命令和未验证项；
- 面向 `main` 的 PR 已创建但未自动合并；
- PR 描述诚实区分 MVP 已验证范围和生产化待办。

## 17. 发布判断

该 MVP 可以被称为“可演示、可重复、默认 Opaque 的单实例参考实现”，不能仅凭 CI 通过宣称已生产就绪。生产发布仍需要真实 KMS、PostgreSQL 故障恢复、多实例 Gateway、外部安全评审、容量验证、Linux/macOS/Windows 真机和 Root Rotation 演练。

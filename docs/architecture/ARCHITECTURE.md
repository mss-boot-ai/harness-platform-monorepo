# Harness Platform 总体架构

- **状态**：Accepted
- **适用范围**：Platform、ABA、HC、ACP Agent 及其交互边界
- **基线日期**：2026-09-03

## 1. 架构目标

Harness Platform 的架构目标不是把一个完整远程执行平台塞进本地 Agent，而是把系统拆分为清晰的控制面、轻量本地安全边界和多形态客户端：

```text
Platform = 重控制面 + 身份/证书 + 密文中继 + 持久化 + 运营
ABA      = 本地私钥 + 本地策略 + ACP 生命周期 + 加密桥接 + 最小可靠性
HC       = 用户交互 + 端点身份 + 会话密钥 + ACP UI
```

架构必须同时满足：

- ABA 可以部署在个人电脑、服务器、Docker 或 Kubernetes Pod。
- ABA 默认不暴露任何本地入站端口。
- Platform 默认无法读取 ACP JSON-RPC 明文。
- HC 与 ABA 均拥有独立、可吊销、可轮换的 Endpoint Identity。
- 网络中断可恢复，但不以重复执行高风险操作为代价。
- Platform 以 mss-boot-admin v1.3.7 为基础，不重复建设成熟后台能力。

## 2. 系统上下文

```text
┌─────────────────────────────────────────────────────────────┐
│                             HC                              │
│  Web / 微信小程序 / App / 桌面端                            │
│                                                             │
│  - Human Session                                            │
│  - HC Endpoint Identity                                     │
│  - HEC / DPoP                                               │
│  - Session Key / ACP UI                                     │
└──────────────────────────────┬──────────────────────────────┘
                               │ TLS 1.3 + AWP encrypted frames
                               │ HTTPS bootstrap + WSS data plane
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                         Platform                            │
│               based on mss-boot-admin v1.3.7               │
│                                                             │
│  Identity / RBAC / Endpoint / PKI / Ticket / Gateway        │
│  Session Registry / Ciphertext Store / ACK / Replay         │
│  Audit / Notification / Rotation / Policy / Operations      │
│                                                             │
│  Default Opaque Mode: no ACP plaintext or endpoint private  │
│  key access                                                  │
└──────────────────────────────┬──────────────────────────────┘
                               │ TLS 1.3 + AWP encrypted frames
                               │ outbound connection initiated by ABA
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                  ABA: acp-brige-agent                       │
│                                                             │
│  Endpoint Key / AEC / Platform Pin / Local Policy           │
│  Runtime + Workspace Mapping / ACP Process Supervisor       │
│  Crypto / Frame Verification / Journal / Bridge             │
└──────────────────────────────┬──────────────────────────────┘
                               │ ACP v1 JSON-RPC over local transport
                               │ normally child-process stdio
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                     Local ACP Agent                         │
│  Codex ACP / DeepSeek Harness / OpenCode / custom agent     │
└─────────────────────────────────────────────────────────────┘
```

## 3. 分层模型

### 3.1 Human Identity Plane

确认“哪个 Platform 用户正在操作”。由 mss-boot-admin 用户、登录 Provider、服务端 Session、角色和策略负责。

### 3.2 Endpoint Identity Plane

确认“哪个 ABA/HC 安装实例正在操作”。由 Endpoint ID、Signing Key、KEM Key、AEC/HEC、Token Binding、DPoP、Ticket 和连接挑战负责。

### 3.3 Authorization Plane

确认“这个用户通过这个 Endpoint 是否有权访问这个 ABA、Workspace、Runtime 和 Session”。有效权限是多层交集，不是单一 JWT Claim。

### 3.4 Key Distribution Plane

管理 Platform Trust Manifest、端点证书、证书状态、Key Package、轮换 Generation 和 ACK。Platform 可以编排，但 Opaque Mode 下不能解封 SRK。

### 3.5 Encrypted Data Plane

承载加密后的原始 ACP JSON-RPC。Platform 负责二进制 Frame 路由、存储、ACK、Replay、Backpressure 和配额，不解析业务 Payload。

### 3.6 Local Execution Plane

只存在 ABA 机器上。包括 Runtime Profile、Workspace 真实路径、ACP 子进程和本地工具能力。Platform 不能向这一层下发任意 Shell。

### 3.7 Operations Plane

包括审计、通知、指标、后台任务、证书到期扫描、保留清理、连接健康、故障处理和管理员界面。

## 4. 组件职责

## 4.1 Platform

Platform 是服务端控制面和密文路由层，主要模块包括：

```text
Platform
├── mss-boot-admin foundation
│   ├── users / roles / menus
│   ├── browser sessions / PAT boundary
│   ├── audit / notification
│   ├── task scheduler
│   ├── database / cache / config
│   └── admin frontend
├── ACP Identity Service
├── Endpoint Enrollment Service
├── Certificate and Trust Service
├── Token / DPoP Service
├── ACP WebSocket Ticket Service
├── ACP Gateway
├── Session Registry
├── Ciphertext Store and Replay Service
├── Rotation Orchestrator
├── Runtime/Workspace Authorization Registry
└── ACP Operations UI
```

Platform 可以知道：

- Tenant/User/Endpoint ID。
- Endpoint 类型、证书序列、状态、在线时间和版本。
- Session 的路由关系、状态、Key Generation。
- Frame 的 Channel、Direction、Sequence、大小、时间和 ACK。
- 管理操作与安全事件。

Opaque Mode 下 Platform 不应知道：

- ACP Method、Prompt、Tool 参数、源代码、Diff、终端输出。
- Endpoint 私钥。
- SRK 和派生方向密钥。
- Runtime 的真实 command、args、cwd、env。
- Workspace 的真实本地路径。

## 4.2 ABA

ABA 是本地最小可信计算边界。它必须保留的职责：

- 本地生成并保护 Signing/KEM 私钥。
- 校验 Platform Trust Manifest、AEC 和吊销状态。
- 主动连接 Platform，并完成持有证明。
- 维护 Runtime/Workspace 本地映射。
- 启动、停止并回收 ACP Agent 子进程组。
- 使用官方 Rust SDK 维持 ACP v1 语义和 Batch 边界。
- 生成 SRK、封装 Key Package、派生方向密钥。
- 对 AWP Frame 加解密、签名、验签和防重放。
- 维护有容量上限的本地 Journal。
- 在执行状态不确定时返回 UNCERTAIN。

不属于 ABA 的职责：

- 用户和租户管理。
- 证书签发和全局吊销中心。
- 全局 Session 搜索和运营页面。
- 大规模密文历史数据库。
- 复杂轮换任务调度。
- 服务端内容索引和摘要。

## 4.3 HC

HC 是用户交互端和会话加密端点：

- 建立 Human Session。
- 本地生成 HC Endpoint Signing/KEM Key。
- 注册 HEC 并持有 DPoP 绑定凭据。
- 获取一次性 WebSocket Ticket。
- 完成升级后的私钥 Challenge。
- 接收和解封 Key Package。
- 派生方向密钥并加解密 ACP Payload。
- 展示 ACP 流式结果、权限请求、连接和 UNCERTAIN 状态。
- 管理本 Endpoint、可见 ABA、证书和安全警告。

## 4.4 Local ACP Agent

Local ACP Agent 是独立进程或嵌入式组件。Harness Platform 不重写 Agent 业务能力，只通过标准 ACP v1 连接。ABA 必须保持 ACP 请求、响应、通知和 Batch 的协议语义。

## 5. 控制面与数据面分离

### 5.1 控制面消息

控制面消息包括：

- Enrollment 与审批。
- Endpoint/证书状态。
- Trust Manifest。
- Token、DPoP Nonce 与 Ticket。
- OpenTunnel/CloseTunnel。
- Runtime/Workspace 能力摘要。
- Key Package 分发与 ACK。
- 轮换、吊销和策略通知。
- 心跳和连接健康。

控制面消息虽然不一定包含 ACP 内容，但仍必须经过身份验证、授权、签名和版本控制。

### 5.2 数据面消息

数据面只承载：

```text
opaque ACP JSON-RPC bytes -> AEAD ciphertext -> signed AWP frame
```

Platform 不应把数据面 Payload 转换为自己的 Event JSON，也不应为了标题、索引或通知而解密。

## 6. 核心交互流程

### 6.1 ABA Enrollment

```text
ABA                         Platform                       User/HC
│ generate keys                │                              │
│ enrollment request + proof   │                              │
├─────────────────────────────►│                              │
│ device/user code             │                              │
│◄─────────────────────────────┤                              │
│                              │ display approval             │
│                              ├─────────────────────────────►│
│                              │ second-factor approval       │
│                              │◄─────────────────────────────┤
│ encrypted AEC + credentials  │                              │
│◄─────────────────────────────┤                              │
│ cache trust/cert securely    │                              │
```

Enrollment Code 只短期有效并以哈希保存。最终凭据用 ABA KEM 公钥保护，即使 Code 泄露也不能直接取得可用私钥绑定凭据。

### 6.2 HC WebSocket

```text
HC                    Platform HTTP              ACP Gateway
│ authenticated POST ticket      │                    │
├───────────────────────────────►│                    │
│ one-time ticket                │                    │
│◄───────────────────────────────┤                    │
│ WSS subprotocol(ticket)        │                    │
├────────────────────────────────────────────────────►│
│                                │ consume/bind ticket│
│ server challenge              ◄─────────────────────┤
│ signed challenge               │                    │
├────────────────────────────────────────────────────►│
│ READY                          │                    │
```

浏览器不能可靠设置自定义 WebSocket Authorization Header，因此必须先通过认证 HTTPS 获取单次 Ticket；Ticket 不是长期身份凭据。

### 6.3 Session Open 与 Keying

```text
HC                         Platform                         ABA
│ create session               │                             │
├─────────────────────────────►│ authorize                   │
│                              │ OpenTunnel(ids only)        │
│                              ├────────────────────────────►│
│                              │                             │ validate local policy
│                              │                             │ spawn ACP process
│                              │                             │ generate SRK
│                              │ KeyPackage(to HC)           │
│◄─────────────────────────────┤◄────────────────────────────┤
│ decrypt SRK                  │                             │
│ encrypted ACP initialize     │                             │
├─────────────────────────────►├────────────────────────────►│
```

Platform 发送给 ABA 的 OpenTunnel 只能包含经过授权的不透明 ID 和协议元数据，不包含任意本地执行参数。

### 6.4 Frame Relay

```text
sender:
  ACP JSON-RPC bytes
    -> derive direction key
    -> allocate persistent sequence
    -> AEAD(AAD, plaintext)
    -> sign(AAD digest + ciphertext)
    -> send AWP frame

Platform:
  authenticate connection
    -> validate routing envelope and quotas
    -> optionally validate endpoint signature without decrypting
    -> persist idempotently
    -> route or queue

receiver:
  validate cert/revocation/signature/sequence
    -> AEAD decrypt
    -> deliver once according to local journal
    -> cumulative/selective ACK
```

### 6.5 Reconnect

重连建立新的 Transport Connection，但原 Logical Channel、Message ID 和 Sequence 不变。新连接携带 ACK Cursor，Platform 重放原始 Frame。新建连接不能重写未确认密文，否则会破坏签名和幂等语义。

## 7. 信任边界

```text
Boundary A: HC runtime and secure storage
Boundary B: public network / reverse proxy / CDN
Boundary C: Platform API and Gateway
Boundary D: Platform database/cache/KMS
Boundary E: ABA host and secure storage
Boundary F: local ACP child process/workspace
```

关键假设：

- 网络和代理均不可信，因此 TLS 与应用层加密同时存在。
- Platform 业务进程可能被读取，但 Opaque Mode 仍不应拥有 Endpoint 私钥或 SRK。
- ABA 主机一旦完全失陷，该主机上的 Session 与 Workspace 无法继续保密；系统目标是限制横向影响和支持吊销。
- Web HC 执行环境受 Platform 下发 JavaScript 影响，其 Assurance Level 低于硬件密钥原生客户端。
- Local ACP Agent 可能有高权限，ABA 必须通过本地白名单和进程限制缩小攻击面。

完整威胁模型见 `SECURITY.md`。

## 8. 数据存储边界

### 8.1 Platform 数据库

保存关系型权威状态：Endpoint、证书、Enrollment、Session、参与者、Key Package、ACK Cursor、策略、审计、状态机和幂等记录。

### 8.2 Platform 密文存储

可以与关系数据库分离，用于较大规模的 Encrypted Frame 和后续 Artifact。必须按 Tenant/Session 分区、加配额和保留策略。

### 8.3 Redis/共享缓存

用于短期 Ticket、DPoP Nonce/JTI、防重放窗口、连接目录和临时路由。生产环境不能把仅存在进程内内存作为唯一权威。

### 8.4 KMS/Signer

只承担 Platform Root/UCA 签名或 Managed Mode 的显式受控解封。Endpoint 私钥永远不进入 KMS。

### 8.5 ABA 本地状态

- 系统安全存储：Endpoint 私钥、Refresh Token、需要持久化的 Session Key。
- 普通配置：Platform URL、Endpoint ID、Runtime/Workspace 映射。
- SQLite/等价 Journal：Sequence、Message 状态、ACK、Manifest Revision；容量有上限。

### 8.6 HC 本地状态

- 安全存储：Signing/KEM 私钥、Refresh Token 或安全 Session 引用、Session Key。
- 普通缓存：Endpoint ID、证书、Trust Manifest、非敏感 UI 状态。

## 9. Platform 高可用架构

```text
Ingress / OpenResty / LB
        │
        ├── Platform API replicas
        └── ACP Gateway replicas
                  │
          Shared connection directory
                  │
        ┌─────────┴─────────┐
        │                   │
   Relational DB       Redis/shared cache
        │                   │
   Ciphertext store     ticket/nonce/route
        │
   KMS / Signer
```

Gateway 的连接对象本身存在于某个实例，但路由目录必须知道 Endpoint 当前连接归属。跨实例 Frame 可以通过内部消息总线、流或 RPC 转发；实现选择需另行 ADR。数据库不是高频实时 Frame 路由总线。

## 10. Monorepo 目标布局

```text
harness-platform-monorepo/
├── AGENT.md
├── docs/
├── protocol/
│   ├── proto/aba/wire/v1/
│   ├── jsonschema/
│   ├── vectors/
│   └── compatibility/
├── aba/
│   ├── Cargo.toml
│   ├── src/
│   └── tests/
├── platform/
│   ├── upstream.lock
│   ├── admin ACP modules
│   └── web ACP pages
├── hc/
│   ├── shared/
│   ├── web/
│   └── miniapp/
├── deploy/
└── scripts/
```

共享协议优先从 `protocol/` 生成 Rust、Go 和 TypeScript 类型，禁止三端手写互相漂移的 Frame 结构。

## 11. 扩展策略

### 11.1 新 ACP Agent

只新增 ABA Runtime Profile 或适配器，不改变 Platform 业务协议。Agent 能力通过 ACP `initialize` 协商，不由 Platform 硬编码。

### 11.2 新 HC

复用 Human Auth、Endpoint Enrollment、AWP、Key Package 和 Session API；新增平台特定安全存储与 UI。

### 11.3 新密码学 Suite

通过 Trust Manifest 和 Wire Crypto Suite Version 协商。旧 Suite 在 Grace 内验证，新会话按策略使用新 Suite；不得静默替换算法。

### 11.4 新 Platform 部署模式

单机、Compose 和 Kubernetes 共享同一逻辑模型。生产依赖可替换，但状态和安全契约不能改变。

## 12. 架构禁止项

- 通过 URL Query 传长期 Token。
- 一个用户所有 Endpoint 共用私钥。
- Platform 生成端点私钥再分发。
- ABA 在公网监听控制端口。
- Platform 通过 JSON 指定任意 command/args/cwd/env。
- 把 ACP Payload 明文写入日志、数据库或通知。
- 使用无版本自定义加密格式。
- 仅靠进程内 Map 实现生产 Ticket、防重放或 ACK 权威状态。
- 把“已 push”当作构建、互操作或安全验证。

## 13. 架构完成定义

架构层面进入可实现状态需要：

- PRD 与组件边界一致。
- Endpoint Identity、Trust、Token、Ticket、Key Package 和轮换状态机已定义。
- AWP Header、AAD、签名输入、序列和兼容规则可生成测试向量。
- Platform 数据模型与 API 能表达全部状态。
- ABA/HC 能在不依赖 Platform 明文的情况下完成 Session。
- 断线、重复和 UNCERTAIN 行为有明确规则。
- 所有未决实现选择被列出，而不是被临时代码默默决定。
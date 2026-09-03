# Harness Platform 产品需求文档（PRD）

- **状态**：Accepted as implementation baseline
- **版本**：0.1
- **日期**：2026-09-03
- **产品仓库**：`mss-boot-ai/harness-platform-monorepo`
- **Platform 基线**：`mss-boot-io/mss-boot-admin v1.3.7`，源提交 `77b53d41092741eac62fa6418c0bdbf87413c7cd`
- **ACP 首发基线**：稳定 Wire Protocol v1；官方 Rust SDK 2.0.x

## 1. 执行摘要

Harness Platform 是一个面向 AI 编程 Agent 的远程安全控制与协作平台。用户可以在个人电脑、服务器或容器中运行轻量 `acp-brige-agent`（ABA），再通过微信小程序、Web 或后续原生客户端（统称 HC）安全连接本地 ACP Agent，创建和继续编程会话、查看流式输出、确认权限并管理设备。

Platform 基于 `mss-boot-admin 1.3.7` 构建，承担所有重控制面能力：用户与租户、RBAC、Endpoint 身份、证书、轮换、WebSocket 鉴权、会话路由、密文持久化、ACK/离线重放、审计、通知、运营管理和后台任务。ABA 保持足够轻，只承担本地安全边界、端点私钥、ACP 子进程生命周期、加解密、协议桥接和最小可靠性 Journal。

默认采用 **Opaque Mode**：ACP JSON-RPC 在 HC 与 ABA 之间端到端加密，Platform 只看见完成路由和可靠性所需的有限元数据，不能读取 Prompt、源码、Diff、Tool 参数和 Agent 输出。企业场景未来可显式启用受审计的 Managed Mode，但该模式不属于端到端加密。

## 2. 背景与问题

当前 AI 编程 Agent 通常运行在开发者本地终端或 IDE 中，存在以下问题：

1. 用户离开电脑后无法安全地继续观察和控制 Agent。
2. 不同 Agent、Harness 和客户端之间协议不统一，接入成本高。
3. 直接暴露本地端口、SSH 或远程桌面会扩大攻击面。
4. 使用普通 WebSocket/Bearer Token 难以识别具体设备，Token 泄露后容易被重放。
5. 中继服务器若能读取全部 Prompt、源码和工具调用，会形成高价值隐私与供应链风险。
6. 网络断开、移动端切换、进程崩溃容易造成消息丢失或危险命令重复执行。
7. 证书和密钥若没有成熟轮换、吊销与恢复机制，系统无法长期运营。
8. 把证书、会话、存储、运营和审计都放到本地 Agent 会使其过重，难以在个人电脑、服务器和 Pod 中统一部署。

## 3. 产品愿景

为每个开发者提供一个不暴露本地入站端口、默认端到端加密、可跨端使用、可审计且可恢复的 ACP 远程控制平面，使任意兼容 ACP 的编程 Agent 都能通过统一的 Platform、ABA 与 HC 体系安全接入。

## 4. 目标

### 4.1 产品目标

- 用户能够在数分钟内安装 ABA、完成配对并从 HC 连接本地 ACP Agent。
- ABA 在个人电脑、普通服务器、Docker 和 Kubernetes Pod 中采用一致核心模型运行。
- HC 能查看可用 ABA、Workspace、Runtime Profile 和 ACP Session，并发起或继续会话。
- 网络中断后能够安全恢复连接、补发未确认密文并避免高风险请求重复执行。
- 用户能够查看、审批、吊销 ABA/HC Endpoint，并手动或按策略轮换证书和 Session Key。
- Platform 默认不能读取 ACP 内容明文。
- Platform 能够基于 mss-boot-admin 的能力提供成熟的后台管理、权限、审计、任务和运营界面。

### 4.2 工程目标

- Platform 精确基于 `mss-boot-admin v1.3.7`，构建可证明、可复现。
- ABA 使用 Rust 和官方 ACP Rust SDK，稳定支持 ACP v1。
- 外层 ABA Wire Protocol（AWP）独立版本化，Schema、实现与测试向量同步演进。
- 安全敏感状态使用显式状态机、数据库唯一约束和幂等键。
- 每个可恢复检查点提交并 push，所有验证状态可追溯。

## 5. 非目标

首发版本不包含：

- 通用远程 Shell、SSH 替代品或远程桌面。
- Platform 任意下发二进制、命令、参数、环境变量或工作目录。
- 绕过本地 ACP Agent 自身权限模型的执行器。
- 默认服务端读取、索引或训练用户的 Prompt、源码和输出。
- 在 HC 中直接执行本地工具。
- ACP draft v2 的稳定兼容承诺。
- 多人实时共同编辑同一 Prompt 的复杂协同编辑器。
- 大文件直接塞入单个 JSON-RPC Frame；制品传输使用后续独立加密 Artifact 通道。
- 完整企业 KMS/HSM 多厂商覆盖；MVP 只要求清晰接口和一个可运行后端。
- 以硬件序列号作为不可变设备身份。

## 6. 用户与角色

### 6.1 个人开发者

在自己的电脑或服务器运行一个或多个 ACP Agent，希望通过手机或 Web 远程查看和控制。

### 6.2 团队开发者

在团队租户下使用共享 Platform，但 ABA、Workspace 和 Session 仍需精细授权，不能跨成员默认可见。

### 6.3 租户管理员

管理成员、Endpoint、证书策略、Runtime/Workspace 授权、保留策略和安全事件。

### 6.4 Platform 运维人员

维护 Platform 服务、数据库、缓存、KMS、队列/后台任务、网关、监控和升级，不应拥有默认读取用户 ACP 明文的能力。

### 6.5 安全与审计人员

查看身份、授权、证书、吊销、轮换、连接和管理操作审计；在 Opaque Mode 下不读取会话内容。

## 7. 核心术语

- **Platform**：基于 mss-boot-admin 的后台控制面和密文中继。
- **ABA**：`acp-brige-agent`，Rust 轻量桥接代理。
- **HC**：微信小程序、Web、App、桌面端等 H 端客户端。
- **Endpoint**：一个具有独立签名密钥、KEM 密钥、证书和状态的安装实例。
- **AEC/HEC**：ABA/HC Endpoint Certificate。
- **UCA**：用户或租户级通信 CA。
- **DPoP**：应用层持有证明，用于把 Token 与 Endpoint 签名私钥绑定。
- **ACP Session**：HC 与某个 ABA 后的 ACP Agent 之间的逻辑会话。
- **SRK**：由 ABA 为 Session 生成的 Session Root Key。
- **Key Package**：使用目标 Endpoint KEM 公钥封装的 Session Key 材料。
- **AWP**：Platform、ABA、HC 之间的 ABA Wire Protocol。
- **Runtime Profile**：ABA 本地配置的 ACP Agent 启动模板。
- **Workspace**：ABA 本地配置并允许指定 Runtime 使用的工作目录映射。

## 8. 产品原则

1. 本地没有入站端口也能使用。
2. 用户身份和端点身份同时成立才可访问。
3. 每个 Endpoint 独立密钥、独立证书、独立吊销。
4. Platform 签发证书但不持有端点私钥。
5. 默认 Opaque，服务端只处理中继元数据和密文。
6. ABA 只接受本地白名单 ID，不接受远端任意 Shell 参数。
7. 掉线可以恢复，但执行状态不确定时绝不自动重复高风险操作。
8. 证书、Token、Ticket 和 Session Key 都有明确生命周期。
9. 所有管理动作可审计；所有秘密不进入普通日志。
10. 提交、构建、测试和验收状态分别记录。

## 9. 关键用户旅程

### 9.1 首次安装 ABA

1. 用户在 Platform 登录并查看 ABA 安装指引。
2. 用户在目标机器安装 ABA。
3. ABA 本地生成 Installation ID、Signing Key 和 KEM Key。
4. ABA 请求 Enrollment，终端显示短期用户码和 Platform 指纹。
5. 用户在已登录 HC/Platform 审批，核对设备名称、系统、IP 和密钥指纹。
6. Platform 签发独立 AEC，并返回绑定端点持有证明的短期凭据。
7. ABA 把私钥和 Refresh Token 保存到系统安全存储，把证书与清单缓存到本地。
8. ABA 建立主动出站 WSS，完成 Ticket/DPoP 与首帧签名挑战后进入 Online。

### 9.2 首次注册 HC

1. 用户通过 Platform 账户、微信身份绑定或后续支持的 IdP 登录。
2. HC 本地生成独立 Signing Key 和 KEM Key。
3. HC 对 Platform Challenge 签名，提交 Endpoint 注册。
4. Platform 根据策略自动批准或要求已有可信 Endpoint/二次认证审批。
5. Platform 签发 HEC，并把 Token 绑定到该 HC 的 Signing Key。
6. HC 缓存 HEC、信任清单和私钥引用。

### 9.3 创建 ACP Session

1. HC 获取用户可访问的 ABA、Runtime Profile 和 Workspace 列表。
2. 用户选择 ABA、Runtime Profile、Workspace，并发起 Session。
3. Platform 验证当前用户 RBAC、HEC、Token Scope、Session ACL 和本地资源授权映射。
4. Platform 向目标 ABA 发送只包含 ID 的 `OpenTunnel` 控制请求。
5. ABA 验证授权，按本地配置启动 ACP Agent，并生成 SRK。
6. ABA 使用 HC 的 KEM 公钥封装 SRK，Platform 只保存并转发 Key Package。
7. HC 解封 SRK，双方派生独立方向密钥。
8. HC 发送加密 ACP `initialize` 和后续 JSON-RPC；Platform 只做密文中继。

### 9.4 断线恢复

1. HC 或 ABA 连接断开后使用退避策略重连。
2. 重连重新获取短期 Ticket，并完成端点挑战。
3. 双方提交每个 Channel 的最高连续 ACK Sequence。
4. Platform 只补发接收方未确认的原始密文 Frame，保持原 `message_id` 和 `sequence`。
5. ABA 使用本地 Journal 去重。
6. 已进入本地 Agent 但结果未知的请求标记为 `UNCERTAIN`，HC 显示人工确认，不自动重放。

### 9.5 吊销丢失的 HC

1. 用户在可信端或 Platform 管理界面选择丢失 HC。
2. 通过二次认证确认吊销。
3. Platform 吊销 HEC、Access/Refresh Token 和所有 Ticket，关闭现有连接。
4. Platform 阻止该 Endpoint 获取新 Key Package。
5. Platform 通知相关 ABA 为仍活跃的 Session 轮换 SRK。
6. 剩余 HC 获取新 Key；丢失 HC 无法读取吊销后的内容。

### 9.6 定期轮换

1. 用户配置 Endpoint Certificate、UCA 和 Session Key 的轮换策略。
2. Platform 固定数量的扫描任务发现到期对象。
3. Endpoint 本地生成新密钥，通过旧/新双签证明持有。
4. 新旧证书短期重叠，连接和 Token 迁移到新证书。
5. Session Key 经 Key Package 分发、ACK、激活、Grace 和销毁状态机切换。
6. 全过程可查看状态、失败原因和审计记录。

## 10. 功能需求

### 10.1 Platform 基础（PF）

- **PF-001**：Platform 必须以 `mss-boot-admin v1.3.7` 精确源提交为基础，构建过程能输出基线标签与 SHA。
- **PF-002**：必须复用 mss-boot-admin 的用户、角色、菜单、Session、审计、配置、任务和前端框架，不建立重复认证体系。
- **PF-003**：ACP Gateway 必须与现有通知 WebSocket Hub 分离，支持二进制 Frame、Endpoint 路由、ACK、Replay 和 Backpressure。
- **PF-004**：所有 ACP 模块必须支持单用户部署和多租户字段隔离；跨租户访问默认拒绝。
- **PF-005**：Platform 必须提供健康、就绪、版本、基线和依赖状态接口，但不泄露秘密。
- **PF-006**：生产模式下 Root/CA 签名操作必须通过 Signer/KMS 接口完成。

### 10.2 用户与授权（AU）

- **AU-001**：HC 必须先建立当前有效的人类用户 Session。
- **AU-002**：每次安全敏感请求必须重新确认当前用户、角色、Session 和 Endpoint 状态，不能只信任陈旧 JWT Claims。
- **AU-003**：有效权限为 Current RBAC、Endpoint Scope、Token Scope、Session ACL、Runtime/Workspace Policy 的交集。
- **AU-004**：ABA Token 与 HC Token 必须有不同 `typ` 和 `aud`。
- **AU-005**：敏感操作包括注册/吊销 Endpoint、根轮换、恢复、Managed Mode、长期授权，必须二次认证。
- **AU-006**：微信小程序登录必须由 Platform 服务端使用临时 Code 换取身份并绑定 Platform User，不接受客户端直接传入的 openid 作为凭据。

### 10.3 Endpoint 注册与管理（EP）

- **EP-001**：每个 ABA/HC 安装实例本地生成独立 Signing Key 与 KEM Key。
- **EP-002**：Platform 不得生成、接收、保存或导出端点私钥。
- **EP-003**：Endpoint 注册必须验证两个公钥的持有证明。
- **EP-004**：ABA Enrollment 使用短期 Device Code/User Code 模式，Code 仅以哈希形式保存。
- **EP-005**：审批界面显示 Endpoint 类型、名称、系统、版本、IP、请求时间和公钥指纹。
- **EP-006**：Endpoint 支持 Active、Pending、Suspended、Revoked、Expired、Compromised 状态。
- **EP-007**：用户可以单独重命名、暂停、吊销 Endpoint，并查看最近在线、证书和会话。
- **EP-008**：硬件标识只能作为展示信号，不能作为唯一身份或恢复凭据。

### 10.4 证书与信任（PKI）

- **PKI-001**：Platform 维护 Platform Root Signing Key、在线签发层和用户/租户 UCA 的分层信任模型。
- **PKI-002**：AEC/HEC 必须绑定 Endpoint ID、Owner/Tenant、Endpoint Type、Signing JWK Thumbprint、KEM Thumbprint、Scope、序列号、策略版本和有效期。
- **PKI-003**：Platform 提供签名的 Trust/Certificate Manifest，包含 Current/Next Root、UCA、吊销版本和生效时间。
- **PKI-004**：Endpoint 首次注册时通过 TLS、用户审批和指纹确认建立 Pin；不得静默替换未知新 Root。
- **PKI-005**：支持证书签发、续期、轮换、吊销、状态查询和紧急失陷流程。
- **PKI-006**：证书状态变更必须单调增加 Revision，并主动通知连接端。

### 10.5 Token、DPoP 与 Ticket（TT）

- **TT-001**：Access Token 短期有效，Refresh Token 绑定 Endpoint Signing Key。
- **TT-002**：受保护 API 使用 DPoP 或等价持有证明，验证方法、URL、时间、JTI、Nonce 和 Access Token 哈希。
- **TT-003**：DPoP JTI/Nonce 必须防重放，时钟偏差窗口可配置且有上限。
- **TT-004**：WebSocket 使用短期、单次消费 Ticket，不把长期 Token 放入 URL。
- **TT-005**：Ticket 绑定用户 Session、Endpoint、证书序列号、Signing Thumbprint、Origin、Purpose 和到期时间。
- **TT-006**：WebSocket 升级后 Platform 发出随机 Challenge，Endpoint 必须用本地私钥签名后才进入 READY。
- **TT-007**：Ticket 和 Challenge 响应必须 `no-store`，不得进入访问日志明文。

### 10.6 ABA 在线连接（CN）

- **CN-001**：ABA 默认只建立主动出站 TLS 1.3 HTTPS/WSS，不监听公网或局域网端口。
- **CN-002**：连接包含 CONNECTING、AUTHENTICATING、CHALLENGED、READY、DRAINING、CLOSED 状态。
- **CN-003**：支持指数退避、随机抖动、服务器 Retry-After 和连接代次编号。
- **CN-004**：Platform 同一 Endpoint 只允许策略定义数量的 Active Connection；新连接替换旧连接必须有明确 fencing token。
- **CN-005**：心跳只报告必要健康信息，不上传本地文件列表、环境变量或命令历史。

### 10.7 Runtime 与 Workspace（RW）

- **RW-001**：Runtime Profile 和 Workspace 的真实命令、参数、路径和环境变量只存在 ABA 本地配置。
- **RW-002**：Platform 只保存不透明 ID、展示名称、能力摘要和授权关系。
- **RW-003**：Platform/HC 不得向 ABA 发送任意 command、args、cwd、env 或可执行内容。
- **RW-004**：Workspace 必须显式声明可使用的 Runtime Profile。
- **RW-005**：ABA 拒绝未知、重复、禁用或策略不匹配的 ID。
- **RW-006**：ABA 只上传经过本地允许的能力摘要，并支持用户关闭可见性。

### 10.8 ACP Session（SS）

- **SS-001**：用户可选择授权 ABA、Runtime Profile、Workspace 创建 Session。
- **SS-002**：Session 保存 Owner、ABA、参与者、状态、Key Generation、创建和最后活动时间，不保存默认明文标题。
- **SS-003**：ABA 使用官方 ACP Rust SDK 启动和桥接本地 ACP Agent，首发稳定支持 ACP v1。
- **SS-004**：ACP `initialize` 的 `protocolVersion` 与 Capability 协商原样端到端传递。
- **SS-005**：Platform 不解析、修改或重新构造 ACP JSON-RPC 业务 Payload。
- **SS-006**：Session 支持 Creating、Keying、Ready、Active、Paused、Uncertain、Closing、Closed、Failed 状态。
- **SS-007**：关闭 Session 必须回收本地子进程组和敏感会话密钥。
- **SS-008**：多 HC 参与同一 Session 必须逐端授权并分别生成 Key Package。

### 10.9 加密（CR）

- **CR-001**：SRK 由 ABA 使用操作系统 CSPRNG 生成，Platform 默认不可获得明文。
- **CR-002**：ABA 使用每个目标 HC 的 KEM 公钥独立封装 SRK。
- **CR-003**：从 SRK 派生 HC→ABA、ABA→HC 独立方向密钥，并包含 Session、Generation 和用途域分离。
- **CR-004**：ACP Payload 使用标准 AEAD；Header 作为 AAD；发送端对 AAD 摘要和密文签名。
- **CR-005**：同 Key 同方向的 Nonce 永不重复，Sequence 持久化且不回退。
- **CR-006**：首发不启用跨消息动态压缩。
- **CR-007**：密码学算法由 Versioned Crypto Suite 标识，迁移不得无版本静默变化。
- **CR-008**：私钥与 Session Key 尽量保存在系统安全存储或受保护内存中，不得明文写入配置。

### 10.10 ABA Wire Protocol（WP）

- **WP-001**：AWP 使用独立 Major/Minor Version，Schema、实现和 Golden Vector 同步发布。
- **WP-002**：Frame 至少包含 Version、Message ID、Channel ID、Session ID、Sender、Receiver、Direction、Sequence、Ack Sequence、Key ID、Type、时间、Ciphertext 和 Signature。
- **WP-003**：必须保留 ACP JSON-RPC Batch 边界。
- **WP-004**：每个 Frame 有严格大小限制；大制品走后续 Artifact 通道。
- **WP-005**：未知 Major Version 拒绝；未知非关键 Minor 字段按 Schema 规则处理；未知安全关键字段失败关闭。
- **WP-006**：控制消息与加密 ACP Payload 使用不同 Frame Type 和授权范围。

### 10.11 可靠性与重放（RL）

- **RL-001**：Platform 按 Channel/Direction/Sequence 保存有序密文 Frame，并以唯一约束防重复。
- **RL-002**：接收方维护最高连续 ACK 和有限乱序窗口。
- **RL-003**：重连只补发未确认 Frame，保持原 ID、Sequence、Ciphertext 和 Signature。
- **RL-004**：ABA 本地 Journal 状态至少包括 RECEIVED、DISPATCH_STARTED、DISPATCHED、RESPONDED、UNCERTAIN。
- **RL-005**：DISPATCH_STARTED 后崩溃且无法证明未执行时，不得自动再次交给 ACP Agent。
- **RL-006**：Journal 必须有容量上限、事务提交、清理和磁盘满策略。
- **RL-007**：Platform 实施每 Endpoint/Session 的窗口、速率、存储配额和背压。
- **RL-008**：超出配额时优先拒绝新高成本消息，不能无限占用内存或磁盘。

### 10.12 轮换与吊销（KR）

- **KR-001**：支持 Platform Root/UCA、Endpoint Certificate、Endpoint Key 和 Session Key 不同层级的轮换策略。
- **KR-002**：Endpoint Key 轮换使用旧 Key 和新 Key 双重持有证明。
- **KR-003**：Root/UCA 轮换采用 Prepare、Publish Next、Client Ack、Activate、Grace、Retire 流程。
- **KR-004**：Session Key 使用 Pending、Distributing、Ready、Active、Grace、Retired、Destroyed 状态机。
- **KR-005**：激活不要求所有长期离线 HC ACK；未及时获取新 Key 的 HC 需要重新授权。
- **KR-006**：吊销 HC 触发 Token/Ticket/连接失效和相关 Session Key 紧急轮换。
- **KR-007**：轮换失败可重试且幂等，不得生成多个互相竞争的 Active Generation。
- **KR-008**：用户可配置允许范围内的周期、提前续期、Grace、历史 Key 保留和手动立即轮换。

### 10.13 审计、通知与运营（OP）

- **OP-001**：注册、审批、拒绝、登录、连接、授权、吊销、轮换、恢复、Managed Mode 和策略修改均产生审计事件。
- **OP-002**：审计只记录主体、Endpoint、资源、动作、结果、时间、证书序列和关联 ID，不记录 ACP 明文或秘密。
- **OP-003**：安全事件通过 Platform 通知中心和 HC 推送用户。
- **OP-004**：管理员可查看在线 Endpoint、连接质量、积压、轮换失败、证书到期和存储使用。
- **OP-005**：后台任务数量固定，通过数据库 `next_*_at` 扫描，不为每个用户创建 Cron。
- **OP-006**：Platform 支持配置密文保留、审计保留和 Endpoint 离线阈值。

### 10.14 HC 体验（HC）

- **HC-001**：HC 首页展示当前可用 ABA、状态、最后在线和安全警告。
- **HC-002**：创建 Session 时只展示当前用户有权使用的 Runtime/Workspace 组合。
- **HC-003**：流式展示 Agent 输出、工具调用、权限请求和状态变化。
- **HC-004**：危险操作以明确风险、目标 Workspace、请求来源和有效期展示，不提供默认“永久允许全部”。
- **HC-005**：断线、重连、补发和 UNCERTAIN 状态必须对用户可见。
- **HC-006**：用户可管理本 HC 的名称、证书、密钥轮换和退出/吊销。
- **HC-007**：Web HC、小程序 HC 和原生 HC 标记不同 Assurance Level。
- **HC-008**：HC 日志、崩溃上报和遥测不得上传 ACP 明文。

### 10.15 Managed Mode（MM，非 MVP 默认能力）

- **MM-001**：只能由用户/租户管理员显式开启，默认关闭。
- **MM-002**：UI 必须明确说明 Platform 可以读取内容，不能标记为端到端加密。
- **MM-003**：解密权限通过独立 KMS Grant、用途、时间和 Worker 身份控制。
- **MM-004**：每次解密产生审计事件，普通 API 进程和数据库不能直接读取明文 Key。
- **MM-005**：允许租户级禁止 Managed Mode。

## 11. 非功能需求

### 11.1 安全

- TLS 1.3 为生产默认，禁止明文 WebSocket。
- 密码学只使用成熟库和标准算法；不手写原语。
- 所有秘密执行日志脱敏和仓库扫描。
- 跨租户测试、Endpoint 冒充、Token/Ticket 重放、Origin 攻击和权限交集必须进入安全测试。
- Platform Root/UCA 生产私钥不得明文保存在数据库、配置或普通环境变量。

### 11.2 隐私

- Opaque Mode 下 Platform 不收集 ACP Payload 明文。
- 最小化采集系统信息，不上传本地文件树、Shell 历史、环境变量和值。
- 用户可查看 Platform 保存的 Endpoint、Session 元数据、密文保留和审计记录。
- 删除账号/租户时执行可审计的数据删除或密钥销毁流程。

### 11.3 可用性

MVP 目标：

- Platform API 月可用性设计目标 99.9%，实际承诺在生产准备阶段确定。
- 单节点故障后，Endpoint 可通过无状态 Gateway 或共享状态恢复连接。
- 重启不能造成已确认 Frame 大规模重复。
- KMS 暂时不可用时，已有 Opaque Session 的数据中继应尽可能继续；新签发/轮换失败关闭。

### 11.4 性能

初始容量目标作为设计输入，不作为未经压测的承诺：

- 单 ABA 默认最多 8 个并发 ACP Session，可配置且受本地资源限制。
- 单 Session 在正常网络下中继附加 P95 延迟目标低于 150 ms，不包含 Agent 推理时间。
- 小型 Frame P95 Platform 入站到出站路由处理目标低于 50 ms。
- 单 Frame 默认上限 1 MiB，协议上限和配置上限取更小值。
- Platform 必须支持水平扩展 Gateway，并避免依赖进程内唯一状态。

具体 SLO 必须通过容量测试后再冻结。

### 11.5 兼容性

- 首发支持 ACP Stable v1。
- ABA 官方 Rust SDK 2.0.x 由 Cargo.lock 固定补丁版本。
- AWP v1 发布后保持向后兼容；破坏性变更进入新 Major。
- Platform 数据库迁移必须支持前滚；回滚能力按迁移类型记录。
- HC API 使用版本前缀或明确兼容策略。

### 11.6 可观测性

- 指标：连接数、Endpoint 在线、Session 状态、Frame 速率、积压、ACK 延迟、重连、轮换、吊销、KMS 错误、Journal 使用率。
- Trace 只记录关联 ID 和阶段，不采集密文之外的内容，更不采集明文。
- 日志支持按 Endpoint/Session/Connection ID 排查，但 ID 不直接包含用户隐私。

### 11.7 可部署性

- ABA：macOS、Linux、Windows 为目标；MVP 优先 Linux/macOS，Windows 状态在实施计划明确。
- ABA：二进制、Docker 和 Kubernetes Sidecar/Pod 模式共享同一核心。
- Platform：支持本地开发、Docker Compose 和 Kubernetes。
- HC：首发 Web 与微信小程序至少实现一个完整可用路径，另一个可在同阶段并行完成。

## 12. 数据与保留

### 12.1 Platform 保存

- 用户/租户和权限数据。
- Endpoint 公钥、证书、状态和必要设备展示信息。
- Enrollment 哈希和过期状态。
- ACP Session 元数据和参与者。
- Key Package 密文。
- AWP Encrypted Frame、ACK 和路由元数据。
- 审计事件、策略、通知和运行指标。

### 12.2 Platform 默认不保存

- Endpoint 私钥。
- SRK 明文。
- ACP JSON-RPC 明文。
- Prompt、源代码、Diff、Tool 参数、终端输出明文。
- 用户本地环境变量、Shell 历史和完整文件树。

### 12.3 建议默认保留

以下值需通过实际成本与用户体验验证后冻结：

- 未完成密文 Frame：7 天或直到明确终止 Session。
- 已 ACK 密文 Frame：24 小时后可清理；用户可选择更长加密历史。
- Enrollment：过期后 24 小时清理。
- 普通审计：180 天。
- 高风险安全审计：365 天。
- 吊销记录和证书序列：至少覆盖最长证书与历史验证周期。

## 13. MVP 范围

### 13.1 必须完成

1. mss-boot-admin v1.3.7 Platform 基线可重现引入与版本证明。
2. 单用户/基础租户模型下的用户 Session 和 ACP RBAC。
3. ABA Enrollment、AEC、HC Endpoint 注册、HEC 和吊销。
4. DPoP 绑定的短期 Token；一次性 WebSocket Ticket；首帧挑战。
5. Rust ABA 主动 WSS、Runtime/Workspace 本地白名单和 ACP v1 子进程桥接。
6. Opaque Session：ABA 生成 SRK、对一个 HC 生成 Key Package、双向 AEAD 加密。
7. AWP v1 Schema、Sequence、ACK、Platform 密文存储与基础离线重放。
8. ABA 本地 Journal 和 UNCERTAIN 处理。
9. Platform Endpoint、Session、证书和审计管理页面。
10. 一个完整 HC（Web 或小程序）端到端链路，另一个端完成身份和连接基础。
11. Endpoint Certificate 手动轮换与吊销触发 Session Key 轮换。
12. 单元、集成、协议测试向量和最小端到端验证。

### 13.2 MVP 可延期但必须保留扩展点

- Root/UCA 全自动无感轮换。
- 多 HC 同时参与一个 Session。
- Managed Mode。
- 原生 App 硬件密钥。
- 企业外部 KMS 多实现。
- 跨区域 Platform 多活。
- 独立加密 Artifact 通道。
- 完整 Windows ABA 支持。

## 14. 分阶段发布

### Phase 0：设计与骨架

- 文档、PRD、协议、状态机和威胁模型冻结。
- 单仓库目录、构建、版本锁、Schema 生成和 CI 骨架。

### Phase 1：身份与安全连接

- Platform 基线、Endpoint 数据模型、Enrollment、证书、DPoP、Ticket 和 WSS Challenge。
- ABA 本地 Key Store 与连接状态机。

### Phase 2：单 Session Opaque MVP

- Runtime/Workspace、ACP 子进程、SRK/Key Package、加密 Frame、HC 流式 UI。

### Phase 3：可靠性与运营

- ACK/Replay、Journal、UNCERTAIN、配额、背压、管理页面、审计和告警。

### Phase 4：轮换与多端

- Endpoint Key/Certificate、Session Key 自动轮换，多 HC 参与和紧急吊销。

### Phase 5：生产强化

- KMS/HSM、容量、故障注入、安全审计、供应链、SBOM、升级与灾备。

## 15. 产品指标

MVP 内测阶段关注：

- ABA 首次注册成功率。
- 从安装到首次 Session Ready 的中位时间。
- Endpoint WSS 连接成功率和重连成功率。
- ACP Frame 端到端延迟与 ACK 延迟。
- 无人工介入的断线恢复成功率。
- 重复执行事件数量，目标为 0；UNCERTAIN 必须可见。
- 证书轮换成功率和平均完成时间。
- 安全事件中 Token/Ticket 重放被阻止的比例。
- Platform 日志秘密扫描结果，目标为 0 泄露。
- 用户主动吊销丢失 Endpoint 到连接失效的延迟。

## 16. 验收标准

MVP 只有在以下条件全部具备证据时才可标记完成：

1. Platform 构建输出证明来自 mss-boot-admin `v1.3.7` 的精确源提交。
2. ABA/HC 私钥仅本地产生，数据库和日志扫描未发现私钥、SRK 或长期凭据。
3. 新 ABA 通过 User Code 审批注册，未审批设备无法连接。
4. 新 HC 通过用户身份与公钥持有证明注册，Endpoint 可单独吊销。
5. 窃取 Access Token 但没有 Endpoint 私钥时，受保护 API 和 WSS 连接均失败。
6. 重复使用 WebSocket Ticket 失败；错误 Origin、错误 Endpoint、错误证书序列均失败。
7. Platform 数据库只能看到密文 Frame，无法解析 ACP Method 或 Payload。
8. HC 能经 Platform 与 ABA 后的真实或标准测试 ACP Agent 完成 initialize、new session、prompt、stream/cancel 基础流程。
9. 网络中断后未确认 Frame 按原 Sequence 恢复，已确认 Frame 不重复交付。
10. 在 DISPATCH_STARTED 后模拟 ABA 崩溃，恢复后请求进入 UNCERTAIN，不自动重复执行。
11. 吊销 HC 后现有连接关闭、Token/Ticket 失效，无法获取新 Key Package。
12. 手动轮换 Endpoint Certificate 和 Session Key 成功，旧 Key 在 Grace 后不能发送新 Frame。
13. Runtime/Workspace 只接受本地配置 ID；构造远端 command/args/cwd/env 请求被协议或 ABA 拒绝。
14. 完成跨租户、重放、乱序、证书过期、时钟偏差、磁盘满和日志秘密测试。
15. 所有已声明支持的平台都有构建结果；未执行真机/浏览器/小程序测试必须明确标记，不能冒充验收通过。

## 17. 风险与缓解

### 17.1 Web HC 供应链风险

Platform 被完全攻破后可能下发恶意 JavaScript，使用浏览器当前上下文中的密钥能力。缓解：标记 Assurance Level；高风险操作使用 Passkey、已有可信 Endpoint 或原生端；未来提供可验证静态客户端。

### 17.2 端到端加密与后台功能冲突

Opaque Mode 无法服务端全文搜索和自动摘要。缓解：客户端本地索引；将 Managed Mode 作为显式、受审计、非 E2EE 的选择。

### 17.3 消息至少一次与副作用

网络恢复可能重放 Frame。缓解：固定 Message ID/Sequence、Platform ACK、ABA Journal、危险状态 UNCERTAIN、ACP 侧尽可能传递幂等上下文。

### 17.4 证书轮换锁死

所有端同时 ACK 会被离线设备阻塞。缓解：按 Active Participants 激活，离线设备重新授权；Root 失陷提供人工指纹恢复流程。

### 17.5 mss-boot-admin 上游演进

长期固定版本会产生安全补丁压力。缓解：锁定 1.3.7 起步，同时维护 Upstream Delta 和受控升级 ADR，不跟随浮动 main。

### 17.6 ABA 变重

可靠性、策略和缓存容易不断下沉。缓解：所有新增职责先判断是否属于本地不可外移安全边界；超过最小职责默认放 Platform。

### 17.7 密文元数据泄露

Platform 仍能看到连接、时间、大小、Endpoint 和 Session 路由。缓解：文档明确边界；最小化元数据；可选 Padding 在后续评估。

## 18. 依赖与假设

- mss-boot-admin v1.3.7 的认证、Session、RBAC、任务和前端基础可扩展。
- Platform 可使用 PostgreSQL/MySQL 等当前 Admin 支持数据库，并使用 Redis 或等价共享状态处理 Ticket/Nonce/连接协调。
- ABA 目标系统有可用的系统安全存储；缺失时需要受密码保护的本地加密存储，不允许明文降级。
- 目标 ACP Agent 提供稳定的 ACP v1 Transport，首发主要通过子进程 stdio。
- 微信小程序的身份与安全存储能力需要通过真实开发者环境验证。

## 19. 尚待实现阶段决策

以下内容不阻塞设计基线，但必须在对应阶段通过 ADR 固化：

- Platform 源码引入采用 vendored fork、subtree 还是可重现生成的维护方式。
- 首发精确密码学 Suite（P-256/AES-GCM 与 X25519/ChaCha20 等兼容权衡）。
- HEC/AEC 使用 X.509、COSE/JWS 应用证书或双轨格式的最终 Canonicalization。
- Platform Gateway 的共享连接目录和跨实例路由实现。
- HC MVP 优先 Web 还是微信小程序，以及各自安全存储实现。
- 密文历史默认保留和存储成本模型。
- Windows 系统安全存储和子进程组完整实现时间。

这些决策不得通过临时代码默认值悄悄冻结。

## 20. 发布门禁

- 设计门禁：PRD、架构、安全、协议、数据模型和验证计划一致。
- 代码门禁：格式、静态检查、单元测试、依赖与许可证检查通过。
- 协议门禁：Schema、Golden Vector、Rust/Go/TS 互操作通过。
- 安全门禁：Threat Model 用例、重放、越权、秘密扫描和依赖漏洞满足标准。
- 端到端门禁：真实 Platform、ABA、HC、ACP Agent 完成核心旅程。
- 运营门禁：升级、回滚、备份、密钥恢复、告警和容量证据齐备。
- 文档门禁：工作日志明确提交、实际验证和未验证项。

任何一个门禁未执行，只能描述为“已实现待验证”，不能标记生产可用。
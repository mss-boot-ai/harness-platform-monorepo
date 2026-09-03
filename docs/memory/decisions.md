# Harness Platform 决策台账

- **状态**：Canonical decision register
- **最后更新**：2026-09-03
- **规则**：状态为 `Accepted` 的决策不得被实现代码或后续代理静默推翻。重大变更必须新增 ADR，并把原决策标记为 `Superseded`。

## 状态说明

- `Accepted`：已经确定，当前实施必须遵守。
- `Proposed`：有方向但尚未冻结，不能作为稳定契约。
- `Superseded`：已被后续 ADR 取代。
- `Rejected`：明确不采用，避免后续重复讨论。

## 决策列表

### D-001：三角色架构

- **状态**：Accepted
- **决定**：系统由 Platform、ABA 和 HC 三类核心角色组成。
- **原因**：把重控制面、本地不可外移安全边界和用户交互分离，避免本地 Agent 过重。
- **影响**：新增能力必须先判断归属；默认重操作进入 Platform。

### D-002：ABA 正式命名

- **状态**：Accepted
- **决定**：本地代理正式名称使用 `acp-brige-agent`，简称 ABA。
- **原因**：用户已明确命名。
- **影响**：不得擅自改成 `acp-bridge-agent`；改名需 ADR 和迁移方案。

### D-003：ABA 使用 Rust

- **状态**：Accepted
- **决定**：ABA 使用 Rust 开发。
- **原因**：ACP 官方实现无 Go SDK，而 Rust 有官方 SDK；ABA 需要低资源、跨平台和内存安全基础。
- **影响**：提交 Cargo.lock，优先官方 ACP Rust SDK，不在 Go 中重写完整 ACP。

### D-004：Platform 基于 mss-boot-admin v1.3.7

- **状态**：Accepted
- **决定**：Platform 必须基于 `mss-boot-io/mss-boot-admin` 的 `v1.3.7`，peeled commit `77b53d41092741eac62fa6418c0bdbf87413c7cd`。
- **原因**：复用成熟后台、用户、RBAC、Session、任务、审计和前端能力，并锁定可重现基线。
- **影响**：禁止跟随浮动 main/latest；升级需 ADR。

### D-005：Monorepo

- **状态**：Accepted
- **决定**：Platform、ABA、HC、协议和部署资产统一存放在 `harness-platform-monorepo`。
- **原因**：协议和测试向量需要跨语言原子演进；简化版本协调和项目记忆。
- **影响**：共享协议从 `protocol/` 生成，不在三端手写副本。

### D-006：长期记忆只放 docs

- **状态**：Accepted
- **决定**：所有项目长期记忆、决策、设计、计划和验证证据放在 `docs/`。
- **原因**：跨会话、设备和 Agent 共享，不依赖模型隐式记忆。
- **影响**：根目录只保留入口和 Agent 契约；秘密不得作为记忆提交。

### D-007：先 main 设计基线，再开功能分支

- **状态**：Accepted
- **决定**：本轮设计和记忆先提交 `main`，之后从最新 main 创建功能分支开发。
- **原因**：确保实现有稳定唯一事实源。
- **影响**：设计基线后功能代码不得直接提交 main。

### D-008：代码检查点先 push 再测试

- **状态**：Accepted
- **决定**：每完成一个可恢复代码切片，先 commit/push，再执行其他操作和测试；修复使用新提交。
- **原因**：防止 Pro/会话环境异常造成代码丢失。
- **影响**：提交信息/工作日志明确未验证；禁止把 push 当测试通过。

### D-009：ABA 保持轻量

- **状态**：Accepted
- **决定**：ABA 只负责端点私钥、本地信任/策略、ACP 生命周期、加解密、桥接和最小 Journal。
- **原因**：统一部署到个人电脑、服务器和 Pod，并缩小本地攻击面。
- **影响**：用户、证书签发、全局路由、密文历史、运营和复杂任务全部在 Platform。

### D-010：ABA 默认无入站端口

- **状态**：Accepted
- **决定**：ABA 默认只主动建立到 Platform 的 TLS/WSS 出站连接。
- **原因**：用户机器不暴露公网/局域网控制端口，降低网络配置和安全风险。
- **影响**：HC 不直连 ABA；所有远端通信经 Platform 中继。

### D-011：Platform 不能下发任意 Shell

- **状态**：Accepted
- **决定**：Platform/HC 只能引用 ABA 本地 Runtime Profile ID 和 Workspace ID，不能发送 command、args、cwd、env、脚本、二进制或任意路径。
- **原因**：即使 Platform 被攻破，也不应直接成为本地通用远程代码执行通道。
- **影响**：真实执行配置只存在 ABA 本地；协议层拒绝危险字段。

### D-012：每个 Endpoint 独立密钥和凭据

- **状态**：Accepted
- **决定**：每个 ABA、Web HC、小程序 HC、App HC 安装实例有独立 Signing Key、KEM Key、AEC/HEC 和吊销状态。
- **原因**：可识别真实端点、单独吊销、限制泄露范围，避免互相冒充。
- **影响**：禁止用户级共享私钥或共用证书。

### D-013：端点私钥本地产生

- **状态**：Accepted
- **决定**：Platform 负责签发和管理凭据，但不生成、接收、保存或导出 Endpoint 私钥。
- **原因**：避免 Platform 成为所有端点的单点私钥托管和冒充主体。
- **影响**：Enrollment 必须验证公钥持有；本地 Secure Store 是必需项。

### D-014：身份签名密钥和 KEM 密钥分离

- **状态**：Accepted
- **决定**：Endpoint Signing Key 与 Endpoint KEM Key 分开。
- **原因**：用途隔离、算法生命周期独立、减少密钥复用风险。
- **影响**：Credential 同时绑定两个 JWK Thumbprint。

### D-015：默认 Opaque Mode

- **状态**：Accepted
- **决定**：ACP JSON-RPC 默认在 HC 与 ABA 之间端到端应用层加密，Platform 无法读取明文。
- **原因**：Prompt、源码、Diff 和工具调用高度敏感，服务端不应默认成为明文集中点。
- **影响**：Platform 只能做密文中继、ACK/Replay 和元数据运营；服务端全文搜索/摘要默认不可用。

### D-016：Managed Mode 不是 E2EE

- **状态**：Accepted
- **决定**：未来允许用户显式开启 Platform 受控解密，但必须单独审计并明确不是端到端加密。
- **原因**：兼顾企业审计/搜索需求和信任透明度。
- **影响**：默认关闭，普通 API/数据库不能直接拿明文 Key。

### D-017：TLS 与应用层加密同时存在

- **状态**：Accepted
- **决定**：生产通信使用 TLS 1.3，同时使用 AWP 应用层加密。
- **原因**：应用层加密不保护全部握手/路由元数据，TLS 也不能阻止 Platform 读取普通明文 Payload；两者职责不同。
- **影响**：不允许以 E2EE 为理由使用明文 WebSocket。

### D-018：首发 ACP Stable v1

- **状态**：Accepted
- **决定**：首发稳定支持 ACP Wire v1；官方 Rust SDK 2.0.x 由 Cargo.lock 固定。
- **原因**：SDK 2.0 保持 ACP v1 Wire 稳定，同时提供当前官方 API。
- **影响**：ACP draft v2 默认禁用，实验支持需 Feature Flag 和 ADR。

### D-019：AWP 独立版本化

- **状态**：Accepted
- **决定**：Platform/ABA/HC 外层通信使用独立 ABA Wire Protocol（AWP）Major/Minor Version。
- **原因**：身份、加密、ACK/Replay 和控制面不属于 ACP 本身，需要独立兼容契约。
- **影响**：AWP Schema、实现和 Golden Vector 同一提交演进。

### D-020：Protobuf Binary Packet

- **状态**：Accepted
- **决定**：AWP v1 使用 Protobuf 二进制 Packet，经 WebSocket Binary Message 传输。
- **原因**：减少 JSON+Base64 开销并提供严格字段类型和兼容规则。
- **影响**：安全签名不能依赖普通 Protobuf 序列化顺序，使用独立 Canonical Transcript/AAD。

### D-021：保持 ACP 原始 Transport Frame/Batch

- **状态**：Accepted
- **决定**：ACP JSON-RPC 作为完整 UTF-8 JSON 值不透明加密，保留 Request/Response/Notification 和 Batch 边界。
- **原因**：Platform 不应成为 ACP Schema 代理，避免扩展字段和新 Method 导致服务端同步升级。
- **影响**：Platform 不解析 Method；ABA 使用官方 SDK 的 Batch-aware Transport 边界。

### D-022：首发 Crypto Suite 0001

- **状态**：Accepted for initial implementation
- **决定**：首发采用 SHA-256、P-256/ES256、RFC 7638、HPKE P-256/HKDF-SHA256/AES-256-GCM、Payload AES-256-GCM。
- **原因**：Rust、浏览器 WebCrypto和小程序可移植性优先。
- **影响**：Suite 有显式 ID，未来算法迁移不得静默替换。

### D-023：Canonical AAD 为固定 148 字节

- **状态**：Accepted
- **决定**：AWP v1 Encrypted Frame 的 Canonical AAD 使用协议文档定义的固定 148 字节大端编码。
- **原因**：避免 Rust/Go/TS 的 Protobuf/JSON 序列化差异。
- **影响**：三端必须共享 Offset 测试；早期口头 128 字节说法已被逐字段计算取代。

### D-024：Session Root Key 由 ABA 生成

- **状态**：Accepted
- **决定**：每个 ACP Session/Generation 的 SRK 由 ABA 使用 OS CSPRNG 生成，并通过每个 HC 的 KEM 公钥单独封装。
- **原因**：Platform 默认不能解密，且 ABA 是本地 ACP 会话的安全端点。
- **影响**：Platform 只保存 Key Package；新增 HC 需要 ABA 在线生成新 Package。

### D-025：Token 使用持有证明

- **状态**：Accepted
- **决定**：Endpoint Access/Refresh Token 与 Signing Key 绑定，受保护 API 使用 DPoP 或等价 Proof-of-Possession。
- **原因**：窃取 Token 但没有端点私钥时不能直接重放。
- **影响**：JTI/Nonce 防重放必须在共享状态中实现；ABA/HC Token 类型和 Audience 不同。

### D-026：WebSocket 两阶段鉴权

- **状态**：Accepted
- **决定**：先通过认证 HTTPS 获取单次 Ticket，再通过 WebSocket Subprotocol 携带；升级后执行 Endpoint 私钥 Challenge。
- **原因**：浏览器 WebSocket API 不能可靠设置自定义 Authorization Header；Ticket 本身仍需和端点私钥绑定。
- **影响**：Ticket 绑定 Session/Endpoint/Origin/Purpose，单次消费并 No Store。

### D-027：ACP Gateway 与通知 Hub 分离

- **状态**：Accepted
- **决定**：Platform 新建独立 ACP Gateway，不把二进制密文、ACK 和 Replay 加入 mss-boot-admin 现有通知 WebSocket Hub。
- **原因**：两者路由粒度、协议、持久化、背压和安全模型不同。
- **影响**：可以复用 Ticket 安全模式，但不是复用同一 Hub。

### D-028：至少一次密文交付，端点去重

- **状态**：Accepted
- **决定**：Platform 提供 at-least-once Encrypted Frame Delivery，Endpoint 通过 Channel/Sequence/Message ID 去重。
- **原因**：分布式网络中无法用简单 ACK 保证本地副作用 exactly-once。
- **影响**：协议不虚假承诺 exactly-once；ABA 需要 Journal。

### D-029：UNCERTAIN 不自动重试

- **状态**：Accepted
- **决定**：ABA 在 `DISPATCH_STARTED` 后崩溃且无法证明是否执行时，将请求标记 `UNCERTAIN`，不自动再次派发。
- **原因**：避免文件写入、命令和外部副作用重复。
- **影响**：HC 必须清晰展示并要求用户处理。

### D-030：固定数量轮换任务

- **状态**：Accepted
- **决定**：Platform 使用少量 System Schedule 扫描 `next_action_at`，不为每个用户/证书/Session 创建 Cron。
- **原因**：避免 Cron 数量随租户规模线性增长，复用 mss-boot Task Server。
- **影响**：多实例扫描需数据库 Lease 和幂等。

### D-031：轮换分层

- **状态**：Accepted
- **决定**：Root/UCA、Endpoint Key/Credential、Token Family 和 Session Key 使用独立状态机与周期。
- **原因**：不同资产的影响范围和紧急性不同，不能用一张共享证书统一轮换。
- **影响**：吊销 HC 触发 Token/Ticket/连接失效和 Session Key 紧急轮换。

### D-032：Web HC 安全等级不夸大

- **状态**：Accepted
- **决定**：Web、微信小程序和原生 HC 使用不同 Assurance Level；Web JavaScript 被 Platform 完全替换时无法提供原生硬件级保证。
- **原因**：诚实反映客户端执行环境与供应链边界。
- **影响**：高风险操作可要求 Passkey、原生/硬件端或多个可信端确认。

### D-033：Platform 首选 vendored upstream

- **状态**：Accepted as preferred implementation path
- **决定**：mss-boot-admin 精确源树首选以 vendored/subtree-style 导入 `platform/`，并通过锁文件记录来源；不把 Git Submodule 作为默认开发体验。
- **原因**：本项目需要直接修改 Platform、统一 checkout/commit/CI 和离线可构建。
- **影响**：首次导入应单独提交；若工具环境暂时无法批量导入，先提供可重现脚本和锁，但不得声称源码已导入。

### D-034：不默认压缩 ACP Payload

- **状态**：Accepted
- **决定**：AWP v1 不对 ACP JSON-RPC 使用跨消息动态压缩。
- **原因**：降低侧信道、状态复杂性和跨语言兼容风险。
- **影响**：大制品使用未来独立 Artifact 通道。

### D-035：多标签页必须单写者

- **状态**：Accepted
- **决定**：同一 Web HC Endpoint Key 不能被多个标签页并行分配 Sequence；使用 Leader Election/浏览器锁或创建独立临时 Endpoint。
- **原因**：防止同一 Direction Key 下 Sequence/Nonce 冲突。
- **影响**：前端 Session Core 必须有明确连接所有权。

## Proposed 决策

### P-001：Platform 跨实例 Gateway 路由实现

- **状态**：Proposed
- **候选**：Redis Streams、NATS JetStream、RabbitMQ 或内部 RPC + 持久 Frame Store。
- **约束**：Connection Directory 共享、ACK 权威唯一、控制消息优先、重启可恢复。

### P-002：微信小程序 Crypto/Secure Store 能力

- **状态**：Proposed pending real-device spike
- **决定前提**：真实 iOS/Android 小程序验证随机数、P-256、HPKE、AES-GCM、密钥存储和 Socket 生命周期。
- **约束**：能力不足时降低权限/Assurance，不明文保存秘密。

### P-003：生产 Frame Store

- **状态**：Proposed
- **候选**：数据库分区表起步，规模增长后关系索引 + 对象/流存储。
- **约束**：唯一性、ACK/保留、配额和跨实例恢复不可丢失。

### P-004：Opaque 历史恢复

- **状态**：Proposed for later phase
- **候选**：用户本地生成 Recovery KEM Key，ABA 额外封装 SRK；Platform 只存密文恢复包。
- **约束**：默认关闭，Platform 不托管恢复私钥。

## Rejected 决策

### R-001：Platform 为所有端点生成一张共享证书/私钥

- **状态**：Rejected
- **原因**：端点可互相冒充、无法单独吊销、单点泄露影响全部设备且不是真正 E2EE。

### R-002：用普通长期 Bearer Token 作为 ABA 身份

- **状态**：Rejected
- **原因**：Token 泄露可直接重放，无法证明端点私钥持有。

### R-003：Platform 直接向 ABA 下发任意命令

- **状态**：Rejected
- **原因**：把 Platform 变成通用远程代码执行通道，破坏本地安全边界。

### R-004：把 ACP 明文存 Platform 以方便搜索

- **状态**：Rejected as default
- **原因**：与默认 Opaque 信任边界冲突；只能作为用户显式 Managed Mode。

### R-005：把现有通知 WebSocket Hub 直接改成 ACP 数据面

- **状态**：Rejected
- **原因**：普通 JSON 用户通知与 Endpoint 二进制密文、ACK/Replay、Fencing 和背压契约不同。

### R-006：把所有轮换对象注册成独立 Cron

- **状态**：Rejected
- **原因**：规模不可控、分布式一致性复杂，应由固定扫描任务和数据库状态驱动。

## 更新规则

新增或改变 Accepted 决策时：

1. 重大架构/安全变更先写 ADR。
2. 在本文件新增编号或把旧项标记 Superseded，并引用 ADR。
3. 同步 PRD、架构、协议、实现计划和 Agent 契约。
4. 在工作日志记录提交、验证和迁移影响。
5. 不删除历史决策，以便理解为什么当前设计存在。
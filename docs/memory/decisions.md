# Harness Platform 决策台账

- **状态**：Canonical decision register
- **最后更新**：2026-09-04
- **规则**：`Accepted` 决策不得被代码或后续代理静默推翻。重大变化必须新增 ADR，并把旧决定标记为 `Superseded`；不得删除历史。

## 状态

- `Accepted`：当前实施必须遵守。
- `Accepted for initial implementation`：首版冻结，后续变化仍需 ADR。
- `Proposed`：尚未冻结。
- `Superseded`：被后续 ADR 取代。
- `Rejected`：明确不采用。

## Accepted 决策

### D-001 至 D-011：角色、仓库与本地边界

- **D-001 三角色**：系统由 Platform、ABA、HC 组成；重控制面默认进入 Platform。
- **D-002 ABA 命名**：正式名称 `acp-brige-agent`，简称 ABA；改名需 ADR。
- **D-003 Rust ABA**：ABA 使用 Rust 和官方 ACP Rust SDK，提交准确 `Cargo.lock`。
- **D-004 Platform 基线**：固定 `mss-boot-admin v1.3.7`，peeled commit `77b53d41092741eac62fa6418c0bdbf87413c7cd`；禁止浮动 main/latest。
- **D-005 Monorepo**：Platform、ABA、HC、协议和部署资产同仓，跨语言协议原子演进。
- **D-006 docs 记忆**：长期设计、决定、计划和验证证据只放 `docs/`。
- **D-007 分支顺序**：设计基线先进入 `main`，功能代码使用 Topic Branch。
- **D-008 防丢失纪律**：单一切片 commit/push 后再完整测试；修复使用新提交。
- **D-009 ABA 轻量**：ABA 只保留端点私钥、本地策略、ACP 生命周期、加解密、桥接和最小 Journal。
- **D-010 ABA 无入站**：ABA 默认只主动连接 Platform；HC 不直连 ABA。
- **D-011 禁止任意 Shell**：Platform/HC 只能引用本地 Runtime/Workspace ID，不能下发 command、args、cwd、env、脚本、二进制或任意路径。

### D-012 至 D-018：Endpoint、加密与 ACP

- **D-012 独立 Endpoint**：每个 ABA/HC 安装有独立 Signing Key、KEM Key、AEC/HEC 与吊销状态。
- **D-013 私钥本地产生**：Platform 不生成、接收、保存或导出 Endpoint 私钥；Enrollment 证明持有。
- **D-014 签名/KEM 分离**：身份签名密钥和 KEM 密钥不得复用。
- **D-015 默认 Opaque Mode**：ACP JSON-RPC 在 HC 与 ABA 之间端到端加密，Platform 默认无法读取明文。
- **D-016 Managed 不是 E2EE**：未来受控解密必须用户显式启用、单独审计并明确改变信任边界。
- **D-017 TLS + 应用层加密**：生产强制 TLS 1.3，同时使用 AWP 内容加密。
- **D-018 ACP Stable v1**：首发兼容 ACP Wire v1；Rust SDK 2.0.x 由锁文件固定，Draft v2 默认禁用。

### D-019 至 D-029：AWP、密码学与可靠性

- **D-019 AWP 独立版本化**：身份、加密、可靠性和控制面不修改 ACP Wire。
- **D-020 Protobuf Binary Packet**：AWP v1 通过 WSS Binary Message 承载 Protobuf；安全签名使用独立 Canonical Transcript/AAD。
- **D-021 保持 ACP 原始边界**：完整 Request/Response/Notification/Batch JSON 值不透明加密；Platform 不解析 Method。
- **D-022 Suite 0001**：`Accepted for initial implementation`；SHA-256、P-256/ES256、RFC 7638、HPKE P-256/HKDF-SHA256/AES-256-GCM 和 Payload AES-256-GCM。
- **D-023 固定 AAD**：AWP v1 Encrypted Frame Canonical AAD 为固定 148 字节大端编码。
- **D-024 ABA 生成 SRK**：每个 Session/Generation 的 SRK 由 ABA 使用 OS CSPRNG 生成，并按 HC 单独封装。
- **D-025 持有证明 Token**：Endpoint Token 与 Signing Key 绑定；JTI/Nonce 防重放使用共享状态。
- **D-026 WSS 两阶段鉴权**：认证 HTTPS 获取短期单次 Ticket，Upgrade 后完成 Endpoint 私钥 Challenge。
- **D-027 Gateway 分离**：ACP Binary Gateway 与普通通知 WebSocket Hub 分离。
- **D-028 至少一次交付**：Platform 提供 at-least-once 密文交付，Endpoint 按 Message ID/Channel/Sequence 去重。
- **D-029 UNCERTAIN**：`DISPATCH_STARTED` 后崩溃且结果不明时不自动重试，HC 必须明确展示。

### D-030 至 D-036：运营、客户端与 Platform 集成

- **D-030 固定数量轮换任务**：Platform 用少量 System Schedule 扫描 `next_action_at`，不为每个对象创建 Cron。
- **D-031 分层轮换**：Root/UCA、Endpoint Key/Credential、Token Family 和 Session Key 使用独立状态机。
- **D-032 诚实的 HC Assurance**：Web、小程序和原生 HC 使用不同安全等级，不能夸大 Web JavaScript 保证。
- **D-034 不默认压缩**：AWP v1 不对 ACP JSON-RPC 使用跨消息动态压缩；大制品走未来独立通道。
- **D-035 多标签页单写者**：同一 Web HC Endpoint Key 不能由多个标签页并行分配 Sequence。
- **D-036 Platform Thin Host import**：`Accepted`；根据 ADR-0004，Platform 由官方 `mss v1.3.7` 生成，后端导入 `github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7`，前端导入 `@mss-boot-io/admin-web@1.3.7`；仓库只拥有 Harness 业务，不复制 Foundation 核心源码，不使用本地 `replace` 或浮动依赖；升级走 `mss upgrade admin` 三方升级并要求最终 no-op。

## Superseded 决策

### D-033：Platform 首选 vendored upstream

- **状态**：Superseded by ADR-0004 and D-036
- **原决定**：把 mss-boot-admin 精确源树以 vendored/subtree-style 导入 `platform/`。
- **取代原因**：v1.3.7 已提供正式 Thin Host/versioned import 和可保留业务文件的升级流程；复制 Foundation 会扩大维护与审查面并损失升级能力。
- **迁移结果**：开发分支已使用官方 v1.3.7 Thin Host；实际完成和验证状态继续以远端文件、`work-log.md` 和 CI 为准。

## Proposed 决策

- **P-001 多实例 Gateway 路由**：候选 Redis Streams、NATS JetStream、RabbitMQ 或内部 RPC + 持久 Frame Store；必须保持 Connection Ownership、ACK 唯一和重启恢复。
- **P-002 微信小程序能力**：待 iOS/Android 真机 Spike 验证随机数、P-256、HPKE、AES-GCM、Secure Store 和 Socket 生命周期；能力不足时降低 Assurance，不能明文保存秘密。
- **P-003 生产 Frame Store**：候选数据库分区表，规模增长后关系索引 + 对象/流存储；唯一性、ACK/保留与配额不可丢失。
- **P-004 Opaque 历史恢复**：未来可由用户本地 Recovery KEM Key 接收 ABA 额外封装的 SRK；Platform 不托管恢复私钥。

## Rejected 决策

- **R-001 共享证书/私钥**：拒绝；端点可互相冒充且无法单独吊销。
- **R-002 长期裸 Bearer Token**：拒绝；Token 泄露即可重放。
- **R-003 Platform 任意命令**：拒绝；会形成通用远程代码执行通道。
- **R-004 默认保存 ACP 明文**：拒绝；只有显式 Managed Mode 才能改变该边界。
- **R-005 复用通知 Hub 作为 ACP 数据面**：拒绝；协议、路由、可靠性、背压和身份模型不同。
- **R-006 每对象独立 Cron**：拒绝；规模和分布式一致性不可控。

## 更新规则

1. 重大架构/安全变化先写 ADR。
2. 本文件新增编号或把旧项标记 Superseded，并引用 ADR。
3. 同步 PRD、架构、协议、实施计划和 `AGENT.md`。
4. 在 `work-log.md` 记录提交、CI、失败、修复和未执行项。
5. 不删除历史决定。
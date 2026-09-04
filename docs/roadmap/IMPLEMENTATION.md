# Harness Platform MVP 实施计划

- **状态**：Accepted
- **修订日期**：2026-09-04
- **目标分支**：`codex/bootstrap-harness-platform-foundation`
- **原则**：每个完整检查点先 commit/push，再执行完整测试；修复形成新提交，不改写历史。

## 1. 已完成基线

- 设计基线位于 `main`；
- 开发分支已创建且没有 force-push；
- Platform 已从 vendored Foundation 纠正为官方 `mss v1.3.7` Thin Host；
- 后端精确 import `admin@v1.3.7`；
- 前端精确 import `admin-web@1.3.7`；
- Thin Host import 合同、Go test/vet、前端 lint/test/build、协议和 ABA CI 已通过；
- AWP v1 Proto 与 Rust 148 字节 AAD 骨架已存在。

## 2. 检查点规则

每个 Slice：

```text
读取远端 HEAD 和相关 ADR
→ 编写单一意图改动
→ 最小格式/语法/秘密检查
→ commit
→ 非强制 push
→ 记录完整 SHA
→ 对该 SHA 执行完整 CI/集成/E2E
→ 失败则修复、commit/push、对新 SHA 重测
→ 更新验证记录
```

CI 仍在执行时不 push 下一检查点，避免 `cancel-in-progress` 隐藏真实结果。

## 3. M0：MVP 产品与架构契约

交付：

- `docs/product/MVP-PRD.md`；
- `docs/architecture/MVP.md`；
- 本实施计划和验证矩阵；
- import 纠正记忆与 Accepted ADR。

门槛：文档检查和全部基线 CI 通过。

## 4. M1：Platform 领域与持久化

**当前状态**：Verified；实现与浏览器验证证据见 [`verification/2026-09-04-platform-m1.md`](verification/2026-09-04-platform-m1.md)。

### 1.1 Pure Domain

实现 Endpoint、Enrollment、Credential、Session、KeyPackage、Ticket、Frame、ACK、Audit 和 Idempotency 状态模型。Clock、ID、Random、Hasher 通过接口注入，单元测试不依赖真实时间。

检查点：

```text
feat(platform): add harness MVP domain model
```

### 1.2 Repository 与 Migration

- GORM Repository；
- 固定 Migration ID；
- SQLite 集成测试；
- 唯一约束、CAS、事务和冲突测试；
- Readiness。

检查点：

```text
feat(platform): persist harness identity and relay state
```

### 1.3 Admin Business Module

- `business.Module` 显式注册；
- Human Principal owner/tenant 授权；
- Overview、Enrollment、Endpoint、Session、Delivery API；
- 管理操作 Audit；
- 中英文 Admin 页面。

检查点按后端、前端拆分并分别验证。

## 5. M2：Endpoint 身份、DPoP 与 Gateway

**当前状态**：Verified（本地单实例）；JWK/ES256/DPoP、HC H5 注册、ABA Enrollment、双端 Refresh/Ticket、Trust Pin、签名 READY、Fencing/Ping、活动 Credential 复核与 ABA Full-Jitter 重连已验证。生产 KMS Signer、跨实例连接目录/Kick 与 AWP Heartbeat Control 仍是生产化工作，详见 [`verification/2026-09-05-mvp-final.md`](verification/2026-09-05-mvp-final.md)。

### 2.1 JWK/Signature

Go/Rust/TS 实现 RFC 7638、ES256 P1363 low-S 和固定向量。

### 2.2 Enrollment/Credential

实现 ABA Start/Approve/Poll/Consume、HC Challenge/Register、Token Hash 和 Refresh Family。

### 2.3 DPoP

验证 htu/htm/iat/jti/ath/nonce，维护 Replay Cache，Token 与 Endpoint JKT 一致。错误信息不形成签名 Oracle。

### 2.4 Ticket/WSS

一次性 Ticket、Subprotocol、Challenge、READY、Fencing、Heartbeat、有界写队列。

每个切片独立 commit/push 后验证。

## 6. M3：Session、HPKE 与 Opaque Relay

**当前状态**：Verified（本地单 HC Prompt MVP）；外部 ACP Process、Suite 0001 HPKE/KDF/AEAD、Key Package、Opaque Relay、Prompt/Response、CloseTunnel 和 UNCERTAIN 已验证。Permission UI、Generation 2 Rekey 与多 Participant 保留为后续范围。

### 3.1 Runtime/Workspace

ABA 严格配置、重复 ID/未知字段拒绝、realpath、安全环境和进程组。

### 3.2 ACP Test Agent

实现确定性 Stable v1 Agent，用于 initialize/session/prompt/permission/response E2E，不访问网络或真实仓库。

### 3.3 Session Control

HC 创建 Session，Platform 授权，ABA 本地 Allowlist，OpenTunnel Accepted/Rejected。

### 3.4 HPKE/KDF/AEAD

三语言共同向量：HPKE Package、方向 Key、Nonce、AAD、Ciphertext、Signature 和篡改负向测试。

### 3.5 Relay

Platform 只验证外层和签名，保存/路由原始密文；ABA/HC 解密并桥接 ACP。

## 7. M4：可靠性与吊销

**当前状态**：Verified（本地单实例）；双向持久 ACK、原 Frame Resume、HC Inbox、ABA Journal、网络恢复、执行不确定与活动 Credential 复核已验证。整个 ABA 进程重启的 Session Key 恢复、跨实例 Kick 和生产容量/长稳仍未验证。

### 4.1 Frame/ACK

幂等写、冲突、最高连续 ACK、Missing Ranges、原 Frame Replay、Retention。

### 4.2 ABA Journal

Sequence 使用前持久化、Inbound/Outbound、Dispatch 状态、容量和清理。

### 4.3 Resume/故障

网络断开、Gateway/ABA/HC 重启、慢消费者、磁盘满和时钟偏差测试。

### 4.4 Revocation

HC/ABA Token/Ticket/连接失效、Session Close/Rekey Required、Audit 和通知。

## 8. M5：产品体验与部署

**当前状态**：Verified（本地启动方式）；Admin/H5、四进程 Launcher、健康检查和清理已验证。生产 Compose/Kubernetes、KMS/OS Keyring 与 TLS 发布配置未验证。

- Admin Overview/Enrollment/Endpoint/Session/Delivery；
- HC Endpoint、连接、Prompt、Permission、Gap、UNCERTAIN；
- Compose：Admin、Gateway、数据库、测试 Agent；
- ABA 示例配置和本地启动；
- 健康检查、最小指标和脱敏日志；
- README 演示流程。

## 9. M6：最终验证与 PR

按 `docs/roadmap/VERIFICATION.md` 执行：

- 全语言静态/单元/集成；
- Thin Host no-op 升级；
- 完整 E2E；
- Opaque Canary；
- 负向安全；
- 故障恢复；
- 有界容量烟测；
- Secret 和依赖扫描。

失败先修复并 push，再重测。全部目标检查通过后：

1. 写最终版本化验证报告；
2. 确认分支远端 HEAD；
3. 创建面向 `main` 的 PR；
4. PR 描述列出完整提交、实际证据、MVP 限制和生产待办；
5. 不自动合并。

## 10. MVP Commit 规划

建议顺序：

```text
docs: define executable harness platform MVP
feat(platform): add harness MVP domain model
feat(platform): persist harness identity and relay state
feat(platform): register harness management module
feat(platform): implement endpoint gateway and DPoP
feat(protocol): add cross-language suite 0001 vectors
feat(aba): implement enrollment connector and local policy
feat(hc): implement endpoint and opaque session client
feat(relay): add reliable encrypted session vertical slice
feat(platform-web): add harness management experience
feat(deploy): add reproducible MVP compose
fix(...): ...                       # 验证发现的问题
test(e2e): verify opaque MVP lifecycle
docs: record final MVP verification
```

提交可因实现边界进一步拆小，但不能合并成一个不可审查大提交。

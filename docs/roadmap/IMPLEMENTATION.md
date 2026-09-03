# Harness Platform 实施计划

- **状态**：Accepted
- **原则**：先设计基线入 `main`，再从最新 `main` 创建功能分支；每个可恢复代码检查点立即 commit 并 push，然后再执行后续测试和下一项工作。

## 1. 分支策略

### 1.1 设计基线

本轮完整设计、PRD、记忆和 Agent 契约直接提交到 `main`，这是用户明确要求的一次性初始化动作。

### 1.2 功能开发

设计基线完成后，从精确 `main` HEAD 创建：

```text
codex/bootstrap-harness-platform-foundation
```

功能分支规则：

- 不直接提交功能代码到 `main`。
- 不 force-push、不 rebase 已 push 的共享分支。
- 开始每个工作段前读取远端分支和 HEAD。
- 发现分支已有未知提交时先审查，不覆盖。
- 未经用户明确授权，不自动合并功能分支。

后续大阶段建议独立分支和 PR：

```text
feat/platform-endpoint-identity
feat/awp-v1-session-crypto
feat/aba-acp-proxy
feat/hc-web-mvp
feat/hc-miniapp-mvp
feat/reliable-replay
feat/key-rotation
```

## 2. 防丢失检查点顺序

用户明确要求代码写好后先提交和 push，再进行其他操作和测试。每个切片按以下顺序：

```text
A. 读取远端状态和相关设计
B. 编写一个单一意图的最小代码切片
C. 人工/静态检查差异、秘密和无关文件
D. commit + push（明确 unverified when applicable）
E. 运行格式化、构建、测试、协议或浏览器验证
F. 若发现问题：新修复 commit + push，禁止改写已推送提交
G. 更新 docs/memory/work-log.md 并 commit + push
H. 再进入下一切片
```

推荐未验证提交消息：

```text
feat(aba): scaffold connector state model (unverified)
```

验证通过后的跟进提交可以是：

```text
test(aba): verify connector state transitions
fix(aba): correct reconnect fencing after validation
```

Push 不等于验证，工作日志必须分别记录。

## 3. Phase 0：设计与仓库基线

### 目标

建立后续代理和开发者共享的唯一事实源。

### 交付物

- README 与 `AGENT.md`。
- 完整 PRD。
- 总体架构、安全、协议、Platform、ABA、HC 设计。
- 实施与验证计划。
- 项目记忆、已确认决策和工作日志。
- 上游/标准引用。

### 完成条件

- 全部文件在 `main`。
- 记录精确 main SHA。
- 文档之间命名和基线一致。
- 从该 SHA 创建功能分支。

## 4. Phase 1：Monorepo 与构建骨架

### Slice 1.1：目录与工作区

创建：

```text
protocol/
aba/
platform/
hc/
deploy/
scripts/
```

添加根级 Task/Make/Just 入口、`.gitignore`、格式和编辑器基础配置。

检查点：只包含骨架，无业务实现。

### Slice 1.2：Platform 上游锁与导入工具

- `platform/.upstream/mss-boot-admin.lock.yaml`。
- 锁定 tag object、source SHA、tree SHA 和 Go 版本。
- 可重现导入/验证脚本。
- 保留 MIT License/Notice。
- 版本证明脚本拒绝漂移。

### Slice 1.3：实际导入 mss-boot-admin v1.3.7

把精确上游源树导入 `platform/`。导入提交必须单独且不混入 ACP 业务改动，便于以后比较和升级。

验证：Tree/关键文件/Go Module 与锁一致。

### Slice 1.4：ABA Rust Workspace

- `aba/Cargo.toml`、`Cargo.lock`、最小 CLI/库。
- 锁定官方 ACP Rust SDK 2.0.x，默认 Stable v1。
- 禁止 experimental v2 默认 Feature。
- 版本输出包含 Git SHA、AWP Version 和 SDK 版本。

### Slice 1.5：HC Workspace

- TypeScript Workspace。
- `packages/protocol`、`crypto`、`session-core` 空骨架。
- Web/小程序 Adapter 边界。
- 依赖锁文件。

### Slice 1.6：CI 骨架

独立 Job：

- docs/link/markdown。
- protocol schema/lint/generation。
- Rust fmt/clippy/test/deny。
- Platform Go fmt/vet/test。
- HC lint/typecheck/test。
- secret scan/license/SBOM（逐步启用）。

CI 未配置或未运行时不得称构建通过。

## 5. Phase 2：AWP v1 协议与互操作基础

### Slice 2.1：Proto Schema

实现 `WirePacket`、Challenge、Control、Encrypted、ACK、Error 和所有枚举。字段编号一经进入 Golden Vector 不随意变更。

### Slice 2.2：Canonical AAD

Rust、Go、TypeScript 分别实现固定 148 字节 AAD 编码；共享 Offset 测试。

### Slice 2.3：Crypto Suite 0001

- RFC 7638 Thumbprint。
- ES256 Low-S P1363。
- HPKE P-256/HKDF-SHA256/AES-256-GCM。
- HKDF 方向密钥。
- AES-GCM Frame。

### Slice 2.4：Golden Vector

固定公开测试 Key，生成合法与错误向量。三端必须读取同一文件，不能各自生成“自己的正确答案”。

### Slice 2.5：Compatibility Gate

- Protobuf Breaking Check。
- Wire Major/Minor 测试。
- Unknown Critical Flag。
- Batch Payload 保持。

## 6. Phase 3：Platform 身份与 Endpoint

### Slice 3.1：ACP 配置与模块开关

生产模式校验 Public Base URL、Redis/共享状态、Signer 和 Crypto Suite。

### Slice 3.2：数据库迁移第一组

- Crypto Profile/Issuer。
- Endpoint/Credential。
- Enrollment。
- Token Family。
- Audit/Idempotency。

独立迁移提交，验证索引和约束。

### Slice 3.3：Signer/KMS 抽象

先实现测试/开发 `local-sealed`，接口适配生产 KMS。生产禁用 plaintext。

### Slice 3.4：Trust Manifest 与 Endpoint Credential

JWS 签发、验证、Revision、防回滚、缓存头和安全错误。

### Slice 3.5：ABA Enrollment

Start、审批、Poll/Consume、Code 哈希、双公钥持有证明和领取包 KEM 保护。

### Slice 3.6：HC Endpoint Enrollment

Human Session + Challenge + Endpoint Proof + HEC。

### Slice 3.7：DPoP 与 Token Family

独立 ABA/HC Token Type/Audience，JTI/Nonce、Trusted Proxy URI 规范化、Refresh Rotation 和 Reuse Detection。

## 7. Phase 4：Ticket 与 ACP Gateway

### Slice 4.1：ACP Ticket Store

扩展现有安全模式但使用独立 Namespace/Record。Ticket 绑定 Endpoint、Credential、Purpose、Origin/Context。

### Slice 4.2：Gateway 连接状态机

WSS Binary、ServerChallenge、ChallengeResponse、ConnectionReady、Generation/Fencing。

### Slice 4.3：Connection Directory

先实现单实例接口和测试，再接共享 Lease/跨实例目录。接口不能把进程内 Map 固化为生产权威。

### Slice 4.4：Ingress Validation

大小、Proto、Version、Endpoint、Signature、Route、Quota 和稳定 Error Code。

### Slice 4.5：Platform 管理页面第一组

ABA Enrollment、Endpoint 列表、状态、Credential 和吊销。

## 8. Phase 5：ABA Identity 与 Connector

### Slice 5.1：严格配置模型

Runtime/Workspace/Limit，未知字段与重复 ID 失败。

### Slice 5.2：Secure Store

抽象和 Linux/macOS MVP 后端；开发不安全后端必须显式开关。

### Slice 5.3：Enrollment CLI

生成 Key、设备码流程、指纹显示、领取和本地缓存。

### Slice 5.4：DPoP/Ticket/WSS

HTTP Token、Ticket、Challenge、Ready、Heartbeat、Reconnect。

### Slice 5.5：单写者与 Fencing

本地进程锁、Sequence Allocator 所有权、旧连接关闭。

## 9. Phase 6：ACP Session 与 Opaque 加密

### Slice 6.1：Platform Session/Participant/Channel 模型

数据库迁移、状态机、授权交集和 OpenTunnel Control。

### Slice 6.2：ABA Local Policy

Runtime/Workspace ID 映射、路径和 Symlink 边界、环境 allowlist。

### Slice 6.3：Process Supervisor

测试 ACP Agent、进程组、启动/握手/关闭超时和 Secret FD 隔离。

### Slice 6.4：官方 ACP SDK Proxy

Stable v1，保留 TransportFrame/Batch。

### Slice 6.5：SRK/Key Package

ABA 生成 Generation 1，面向一个 HC 的 HPKE Package、ACK 和激活。

### Slice 6.6：Encrypted ACP Frame

HC→ABA、ABA→HC 方向密钥、AAD、AEAD、签名和 ACP 转发。

### Slice 6.7：首个端到端链路

真实 Platform、一个 ABA、一个 HC、测试 ACP Agent 完成 initialize/new session/prompt/stream/cancel。

## 10. Phase 7：可靠性

### Slice 7.1：Platform Frame/ACK Store

唯一约束、幂等写入、Cursor 单调更新和保留标记。

### Slice 7.2：ABA Journal

SQLite Schema、RECEIVED→DISPATCH 状态、Outbox 和容量限制。

### Slice 7.3：Resume/Replay

新 Ticket/连接、Cursor、未确认原 Frame 重放、乱序窗口。

### Slice 7.4：UNCERTAIN

故障注入在 DISPATCH_STARTED 崩溃，恢复后可见且不重派。

### Slice 7.5：Backpressure/Quota

有界队列、慢消费者、Tenant/Endpoint/Session 配额和控制通道优先。

## 11. Phase 8：HC 产品体验

### Slice 8.1：共享 Identity/Crypto/Session Core

与 Golden Vector 对齐。

### Slice 8.2：Web HC

Endpoint Key、Secure Store、Ticket/WSS、Session UI、权限卡、刷新恢复和多标签页 Leader。

### Slice 8.3：微信小程序能力 Spike

先在真机验证 Crypto、随机数、安全存储、Socket 前后台和登录 Code，再确定完整实现。能力不够时调整 Assurance/权限，不允许明文降级。

### Slice 8.4：小程序 Session MVP

ABA 列表、Session、流式 UI、权限、重连和安全设置。

### Slice 8.5：Platform 管理页面第二组

Session、Participant、积压、审计、轮换和运营视图。

## 12. Phase 9：轮换与吊销

### Slice 9.1：Endpoint Key/Credential Rotation

旧/新双签、Overlap、Token 迁移、旧 Credential Renewal-only/Revoked。

### Slice 9.2：Session Key Rotation

Pending→Distributing→Ready→Active→Grace→Retired→Destroyed。

### Slice 9.3：HC 吊销紧急轮换

Token/Ticket/连接关闭，阻止新 Package，剩余 Participant 新 Generation。

### Slice 9.4：Root/UCA Rotation

Current/Next Manifest、交叉签名、ACK、Dual Trust、Retire 和失陷恢复。

### Slice 9.5：固定系统任务

扫描、Lease、幂等、失败告警，不产生 per-user Cron。

## 13. Phase 10：生产强化

- 多实例 Gateway 和跨实例路由。
- 数据分区、密文成本与保留。
- KMS/HSM 生产实现。
- 容量和长连接压力测试。
- 故障注入、灾难恢复和 Root Recovery Drill。
- 安全审计、依赖/SBOM/制品签名。
- macOS/Linux/Windows、Docker/K8s 矩阵。
- 升级/回滚和上游 mss-boot-admin Delta 管理。

## 14. 每阶段退出条件

每个 Phase 退出前必须：

- 代码和文档在远端分支。
- 精确提交 SHA 可见。
- 对应单元/集成/互操作测试实际执行。
- 未执行浏览器、真机、KMS 或多节点测试明确列出。
- Work Log 更新。
- 没有高优先级安全不变量破坏。
- 下一阶段从最新已 push 检查点开始。

## 15. 初始开发分支的限定范围

`codex/bootstrap-harness-platform-foundation` 首轮只做可安全完成的基础：

1. Monorepo 目录和工具入口。
2. Platform 上游精确锁与导入机制；在可行时完成实际源码导入。
3. AWP v1 初始 Protobuf Schema。
4. ABA Rust Workspace 和配置/协议基础模型。
5. Platform/HC 占位边界和共享协议生成计划。
6. 最小 CI/验证脚本。

不会在没有三端协议向量和身份模型基础前直接写“可远程执行”的业务逻辑。

## 16. 工作拆分原则

- 一个提交只做一个可说明意图。
- Schema 与生成代码/向量同一检查点。
- 数据库迁移和业务 Handler 尽量分开提交。
- 安全状态机先测再接 UI。
- 不以大量空壳接口冒充进度；每个 Slice 要有可验证契约。
- 不为了暂时编译通过绕过 DPoP、证书状态、Tenant 条件或本地白名单。

## 17. 进度报告格式

```text
Branch:
Base SHA:
Current SHA:

Designed:
Written:
Committed and pushed:
Actually built:
Actually tested:
Browser/miniapp/device verified:
Security verified:
Not verified:
Known risks:
Next checkpoint:
```

该格式同时写入工作日志和 PR 描述。
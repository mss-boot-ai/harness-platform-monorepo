# Harness Platform 验证计划与证据门禁

- **状态**：Accepted
- **原则**：实现、提交、构建、测试、浏览器验收、真机验证和安全验证必须分别记录。
- **执行顺序**：按照用户要求，代码检查点先 commit 并 push，再执行本文件中的验证；测试产生的修复以新提交继续 push，禁止改写已推送历史。

## 1. 验证状态定义

每个工作项只能使用以下明确状态：

| 状态 | 含义 |
| --- | --- |
| Designed | 需求、契约或状态机已经写入文档 |
| Written | 代码/配置已经编写，但不表示可编译 |
| Pushed | 对应提交已经存在于远端分支 |
| Built | 在记录的环境中实际完成构建 |
| Unit Verified | 实际运行相关单元测试并通过 |
| Integration Verified | 实际运行跨模块/数据库/网络集成测试并通过 |
| Interop Verified | Rust、Go、TypeScript 对同一协议向量互操作通过 |
| Browser Verified | 在记录的真实浏览器中完成功能验收 |
| Miniapp Device Verified | 在微信开发者工具之外的真实设备中验证 |
| Security Verified | 本文件要求的安全测试实际执行并通过 |
| Production Qualified | 全部门禁、容量、灾备、升级和运营验证完成 |

禁止把 `Pushed`、CI 排队或代码审阅替代后续状态。

## 2. 每次检查点的证据模板

写入 `docs/memory/work-log.md`：

```text
Date/time and timezone:
Branch:
Base SHA:
Commit SHA:
Scope:

Designed:
Written:
Committed and pushed:
Commands actually executed:
Environment:
Results:
Artifacts/log references:
Browser/miniapp/device evidence:
Security evidence:
Not executed:
Known failures/risks:
Next safe checkpoint:
```

失败也必须记录；不能只保留成功日志。

## 3. 验证环境记录

每次验证至少记录：

- OS、CPU 架构和容器/虚拟化信息。
- Rust/Go/Node/pnpm/protoc/buf 版本。
- Platform 上游基线标签和提交。
- ABA、HC、AWP 和数据库 Schema 版本。
- 数据库、Redis、KMS/Signer 实现及版本。
- 浏览器/微信版本和设备型号（涉及客户端时）。
- 测试是否使用模拟器、真实服务或真实设备。
- 被刻意关闭的 Feature Flag。

秘密和生产凭据不得进入证据。

## 4. 文档门禁

### 4.1 检查项

- Markdown 格式和链接有效。
- `docs/README.md` 指向的文件全部存在。
- Platform/ABA/HC 名称和 `acp-brige-agent` 拼写一致。
- mss-boot-admin 基线始终为 `v1.3.7` / `77b53d41092741eac62fa6418c0bdbf87413c7cd`。
- 协议中的 AAD 长度、字段 Offset、算法和状态机一致。
- PRD 功能需求能映射到架构和测试。
- `docs/memory/` 不含秘密和聊天全文。

### 4.2 建议命令

```bash
markdownlint-cli2 "**/*.md"
lychee --no-progress "**/*.md"
grep -R "acp-bridge-agent" .    # 除纠错说明外应为空
grep -R "mss-boot-admin.*main" docs AGENT.md README.md
```

## 5. 上游 Platform 基线验证

### 5.1 锁定检查

必须证明：

```text
repo       = mss-boot-io/mss-boot-admin
tag        = v1.3.7
tag object = 41c6517950f7f5f642418f5d4a49386e9c200b15
peeled SHA = 77b53d41092741eac62fa6418c0bdbf87413c7cd
Go         = 1.26.6
```

验证脚本需要：

- 从锁文件读取值，不依赖浮动分支。
- 验证导入源码 Tree/关键文件哈希。
- 检查 MIT License 和版权保留。
- 构建输出包含上游标签和提交。
- 上游漂移导致 CI 失败，而不是警告后继续。

### 5.2 上游差异验证

每次升级/同步生成：

- Old/New Upstream Commit。
- Upstream Diff。
- Harness Platform Delta。
- 冲突解决清单。
- 数据库/API/前端兼容报告。
- 回滚策略。

## 6. AWP Schema 与兼容性验证

### 6.1 Schema

- `protoc`/`buf lint` 通过。
- 字段号不重复、不复用已删除字段。
- 所有 ID 长度、Sequence、Generation 和错误码限制有验证。
- Protobuf Breaking Check 对已发布 v1 基线通过。

### 6.2 Canonical AAD

Rust、Go、TypeScript 必须对同一输入生成完全一致的 148 字节结果。测试逐一断言：

```text
magic/version/suite/type/flags
message/channel/session IDs
sender/receiver IDs
direction/reserved bytes
sequence/generation/key ID
timestamp/ciphertext length
```

额外测试：

- 保留字节非零拒绝。
- 字段端序错误拒绝。
- 长度不是 148 拒绝。
- ID 长度错误拒绝。

### 6.3 Golden Vector

公共测试向量覆盖：

- RFC 7638 Thumbprint。
- HKDF 派生两个方向密钥。
- HPKE Key Package Seal/Open。
- AES-256-GCM 正确和错误 Tag。
- ES256 P1363 Low-S 签名。
- High-S、错误长度和篡改签名拒绝。
- 单 Request、Response、Notification 和 Batch ACP Payload。
- Control/Ack Transcript。
- Sequence Replay、旧 Generation 和未知 Critical Flag。

三端读取同一向量文件，不允许运行时各自重新随机生成期望值。

## 7. ABA Rust 验证

### 7.1 基础门禁

```bash
cargo fmt --all -- --check
cargo clippy --workspace --all-targets --all-features -- -D warnings
cargo test --workspace --all-targets
cargo deny check
```

Experimental ACP v2 Feature 需要独立 Job；默认构建必须确认未启用。

### 7.2 配置

- 未知字段、重复 ID、相对路径、非法 URL、未知 Runtime/Workspace 拒绝。
- Symlink 越界和路径切换攻击拒绝。
- Platform 远端消息不能覆盖 command/args/cwd/env。
- 配置热更新若未设计则明确要求重启，不偷偷部分生效。

### 7.3 Secure Store

- 每个 OS 后端创建、读取、删除和访问控制。
- 无安全后端时生产模式失败。
- 开发不安全后端必须显式开关并产生警告。
- 私钥不出现在配置、日志、Core Dump 测试样本或普通文件中。
- Key Handle/Secret Drop 后执行可验证的清理行为（在库能力范围内）。

### 7.4 Connector

- Ticket 获取、WSS Upgrade、ServerChallenge 验证、ChallengeResponse 和 Ready。
- 错误 Root、Credential、Generation、Fencing Token 拒绝。
- 指数退避、Full Jitter、Retry-After。
- 两个 ABA 进程不能并行使用同一 Endpoint 身份分配 Sequence。
- Credential Revoked/Expired 进入终止状态，不无限重试。

### 7.5 ACP Process Supervisor

- 成功启动标准测试 ACP Agent。
- 启动失败、握手超时、stderr 洪泛、异常退出、ABA 退出。
- Linux 进程组、Windows Job Object、macOS 行为分别验证。
- 子进程不继承 Endpoint 私钥或不必要文件描述符。
- Environment 只有本地 Allowlist。
- 所有退出路径回收子进程。

### 7.6 Journal

- RECEIVE/ACK/Dispatch 的事务顺序。
- 重复 Message ID/Sequence 返回原状态。
- `DISPATCH_STARTED` 崩溃恢复为 `UNCERTAIN`。
- Outbox ACK 后清理。
- WAL/DB 损坏、磁盘只读、磁盘满和容量硬限制。
- Sequence 持久化失败时停止发送，不复用旧 Nonce。

## 8. Platform Go 验证

### 8.1 基础门禁

在导入的 Platform 根目录实际执行：

```bash
gofmt -w/check affected files
go test ./...
go vet ./...
golangci-lint run
```

若上游已有更严格命令，优先复用。记录实际 Go 版本 `1.26.6` 是否可用；使用不同版本时必须标明，不可宣称完整基线验证。

### 8.2 数据库迁移

每个支持数据库至少验证：

- 空库前滚。
- 从 mss-boot-admin v1.3.7 既有库前滚。
- 重复启动不重复创建/破坏。
- 唯一约束和部分索引真实存在。
- 跨租户查询必须带 Tenant 条件。
- 同 Session 不会出现两个 Active Key Generation。
- ACK 只能单调前进。
- 不可逆迁移有明确说明和备份要求。

### 8.3 Human/Endpoint Auth

- 当前用户、角色和服务端 Session 被重新读取。
- ABA Token 不能调用 HC/普通 Admin 接口。
- HC Token 不能冒充 ABA。
- 过期、暂停、吊销 Endpoint 均失败。
- Effective Permission 每一层缺失时拒绝。
- Refresh Token 重用撤销整个 Family。

### 8.4 DPoP

- 正常 Proof。
- JTI 重放。
- Nonce 缺失/过期/错误。
- `htm`、`htu`、`ath` 不匹配。
- Trusted Proxy Header 注入。
- 时钟偏差边界。
- 不同 Gateway 实例之间防重放一致。

### 8.5 Ticket/Challenge

- Ticket 单次消费。
- 到期、错误 Purpose、Endpoint、Credential Serial、Origin、Session。
- 并发消费只有一个成功。
- Challenge 超时、错误签名和旧 Connection Generation。
- 新连接 Fencing 后旧连接不能产生新动作。

### 8.6 Gateway

- Binary Packet 大小前置限制。
- Proto/Version/Critical Flag/Signature/Route 校验。
- Frame 幂等持久化。
- 本地与跨实例路由。
- 慢消费者和有界 Queue。
- 重启后未确认 Frame 仍可恢复。
- 控制消息在数据面拥塞时可发送。
- Gateway 不解析 ACP Method；日志无 Payload。

### 8.7 后台任务

- 多实例只抢占一次。
- 失败可幂等重试。
- 到期/轮换/保留扫描使用固定任务数量。
- 一个离线用户/Endpoint 不阻塞其他对象。
- 任务停止/重启不产生双 Active Generation。

## 9. HC 验证

### 9.1 共享核心

```bash
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

- AWP/crypto 向量与 Rust/Go 相同。
- Session、Key Rotation、ACK 和 Reconnect 状态机属性测试。
- 重复 Frame 不重复 UI 事件。
- 同 Sequence 不同 Ciphertext 触发安全关闭。

### 9.2 Web 浏览器矩阵

至少记录：

- Chrome Stable。
- Edge Stable。
- Safari 当前支持版本。
- Firefox Stable（若宣称支持）。

真实验证：

- WebCrypto 不可导出 Key 和 IndexedDB 持久行为。
- 首次注册、刷新、退出、吊销。
- Ticket/Origin/WSS。
- 页面刷新后 Resume。
- 多标签页 Leader Election 与 Sequence 单写者。
- IndexedDB 配额/清理/损坏。
- CSP、XSS 回归和第三方脚本限制。
- DevTools Console/Network 无秘密或明文泄露。

### 9.3 微信小程序真机

模拟器不能替代真机。至少验证：

- `wx.login` Code 单次交换和重放拒绝。
- CSPRNG、P-256、HPKE、AES-GCM 的真实能力、性能和兼容性。
- 密钥/Token 安全存储实现。
- Socket 前后台、系统回收、网络 Wi-Fi/蜂窝切换。
- 页面卸载/重开后的 ACK/Resume。
- 包体和性能限制。
- iOS/Android 各至少一个真实设备。

能力不足时降低 Assurance 或调整产品范围，不明文降级。

## 10. 端到端验证

### 10.1 基础场景

真实组合：

```text
HC -> Platform API/Gateway -> ABA -> ACP test agent
```

完成：

1. Human Login。
2. HC Endpoint 注册。
3. ABA Device Code Enrollment 与审批。
4. ABA 在线。
5. Runtime/Workspace 展示与授权。
6. 创建 Session。
7. SRK/Key Package/ACK/激活。
8. ACP initialize。
9. new session / prompt / stream / permission / cancel。
10. 正常关闭并回收 Agent。

### 10.2 真实 ACP Agent

测试 Agent通过后，再至少选一个真实兼容 ACP v1 的 Agent验证。记录版本、启动配置和不支持能力；测试 Agent结果不能替代真实 Agent 兼容结论。

### 10.3 Opaque 证明

- 抓取 Platform API/Gateway/DB/Redis/Frame Store 数据。
- 证明无法查找已知 Prompt/Method/代码片段。
- 证明只有 HC/ABA 能解密测试向量。
- 检查日志、Trace、指标和错误。

## 11. 网络与故障注入

对 HC↔Platform、ABA↔Platform 分别注入：

- 断线、半开连接、DNS 失败、TLS 失败。
- 延迟、抖动、丢包、重复、乱序。
- Gateway 重启和滚动升级。
- Redis/数据库短时故障。
- KMS 不可用。
- Frame Store 满/慢。
- ABA Journal 满/损坏。
- HC 本地存储配额满。

预期：

- 未确认 Frame 可恢复。
- 已确认 Frame 不重复交付。
- KMS 故障不允许新签发/轮换；既有 Opaque Relay 按设计继续或明确停止。
- 不出现无界内存/磁盘增长。
- 控制/吊销通道仍可用或明确失败关闭。

## 12. 安全测试矩阵

### 12.1 身份与授权

- 未审批 ABA。
- 窃取 Token 但无 Signing Key。
- 复制另一个 Endpoint 的 HEC/AEC。
- 跨用户、跨租户、跨 Session、跨 Workspace。
- 降低 Policy/Manifest/Status Revision。
- 普通用户调用 Trust/Managed Mode 管理。

### 12.2 重放与协议

- Device/User Code 重放。
- DPoP JTI/Nonce 重放。
- Ticket 并发/重复消费。
- Challenge Response 重放到新 Connection。
- Frame Message ID/Sequence 重放。
- 旧 Generation 在 Grace 后发送。
- ACK 回退和伪造 Gap Range。
- Unknown Critical Flag/Control Type。

### 12.3 密码学

- AAD 任一字段篡改。
- Ciphertext/Tag/Signature 篡改。
- High-S ECDSA。
- Nonce/Sequence 重复检测。
- Key Package Recipient 替换。
- Credential Algorithm Confusion、未知 `crit`、错误 Issuer。
- Trust Manifest Root 替换和回滚。

### 12.4 本地执行

- 在 OpenTunnel 中注入 command/args/cwd/env/路径。
- Runtime ID 混淆、重复 ID、Unicode/大小写冲突。
- Workspace `..`、Symlink、Mount 切换。
- 环境变量注入和 PATH 劫持。
- ACP 子进程尝试读取 ABA Secret。

### 12.5 应用与供应链

- XSS/CSRF/CORS/Origin。
- SQL 注入、反序列化和超大 Proto。
- SSRF（Platform URL/Artifact 后续能力）。
- 依赖漏洞、恶意安装脚本和许可证。
- 构建制品篡改、错误架构和版本回滚。
- Secret Scan 和历史扫描。

## 13. 轮换与恢复验证

### 13.1 Endpoint

- 正常旧/新双签。
- 旧 Key 不可用、新 Key 持有证明缺失。
- Overlap 中连接迁移。
- 旧 Credential 进入 Renewal-only 后不能新连。
- Refresh Token 绑定迁移。

### 13.2 Session Key

- 一个 HC、多个 HC。
- HC 离线不阻塞策略定义的激活。
- Key Package ACK 丢失/重复。
- 激活中 ABA/Platform/HC 分别崩溃。
- Grace 内只解密，不能新发送。
- Retired/Destroyed 后内存和 Store 清理。

### 13.3 Root/UCA

- Current/Next 发布、交叉签名、Endpoint ACK、Dual Trust、退休。
- 老客户端和新客户端兼容窗口。
- 旧 Root 失陷的人工恢复演练。
- 未授权 Root 替换绝不静默成功。

### 13.4 吊销

测量从用户确认到：

- Token 失败。
- Ticket 清除。
- Active WSS 关闭。
- 新 Key Package 拒绝。
- 新 Session Key 激活。

并验证已吊销 Endpoint 不能读吊销后 Frame。

## 14. 性能与容量

设计目标需压测后才能成为 SLO。压测维度：

- Gateway 并发 WSS 连接。
- 每秒 Binary Frame 和平均/最大大小。
- ACK 延迟与未确认窗口。
- 单 Tenant/Endpoint/Session 热点。
- 数据库 Frame 索引、分区和清理。
- Redis Ticket/Nonce/Connection Directory。
- Key Rotation 扫描和风暴。
- ABA CPU/内存/Journal I/O。
- HC 解密、JSON 解析和大流式渲染。

结果包含 P50/P95/P99、错误率、资源曲线、测试数据生成方式和瓶颈。不得只报告平均值。

## 15. 升级与回滚

- 从干净 v1.3.7 Platform 数据库安装。
- 从仅 mss-boot-admin v1.3.7 运行状态升级。
- Gateway/ABA/HC 混合版本兼容。
- AWP Minor 升级和旧客户端。
- 数据库迁移失败恢复。
- ABA 二进制升级失败和回滚。
- Trust/Credential Rotation 与应用版本升级同时发生。

不可逆迁移必须在发布前执行备份恢复演练。

## 16. 发布与供应链门禁

- 精确 Git SHA、上游 SHA、AWP、Schema、Rust/Go/Node 版本写入 Build Info。
- 所有制品有 SHA-256、签名和 SBOM。
- CI 从干净 checkout 构建，不依赖未提交文件。
- 可重复构建差异有解释。
- 发布密钥/凭据通过受控身份，不写入仓库或日志。
- 安装包验证签名和目标架构。
- 高危依赖漏洞无未处理例外。

## 17. 生产资格门禁

只有全部满足才能标记 `Production Qualified`：

1. PRD MVP 验收项有逐项证据。
2. Rust/Go/TS 互操作和真实 ACP Agent通过。
3. Web 与宣称支持的小程序/OS 真机矩阵通过。
4. 安全测试、秘密扫描和外部/独立审查完成。
5. 容量达到目标并有余量。
6. 多实例、故障恢复、备份、灾备和 Root Recovery 演练完成。
7. 升级/回滚真实执行。
8. 监控、告警、Runbook、值班和数据保留就绪。
9. 所有已知高风险问题关闭或由负责人明确接受。
10. 文档和工作日志与发布提交一致。

## 18. 当前阶段允许的结论

在初始开发分支中，最多可以声明：

- Monorepo 骨架已写/已 push。
- Protocol Schema 已写并在某工具版本下 lint。
- ABA/Platform/HC 某模块在记录环境下构建或测试通过。

在尚未完成真实 Platform、ABA、HC、ACP Agent 链路前，不得声明“远程 Agent 平台已可用”或“端到端加密已验证”。
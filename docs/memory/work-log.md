# Harness Platform 工作与验证日志

- **状态**：Append-only operational memory
- **时区约定**：每条记录必须写明日期、时间和时区。
- **纪律**：记录实际完成的工作、远端提交和验证证据；失败与未执行项同样保留。不要把本文件改写成只显示成功的宣传材料。

---

## 2026-09-04 10:29 +08:00 — Foundation 与 Platform M1 Draft PR 检查点

### 请求范围

- 补齐功能分支从 Monorepo Foundation 到现有 Platform M1 后端的工作与验证记录。
- 把当前成果建立为面向 `main` 的 Draft PR 检查点，不把它描述为完整 MVP 或可运行的远程 Agent 平台。
- 记录后续工作和当前问题，作为下一开发会话的稳定起点。

### 开工检查

```text
repository:    mss-boot-ai/harness-platform-monorepo
branch:        codex/bootstrap-harness-platform-foundation
main SHA:      234cd9a9d6457318188d9007895fced38af4949c
branch SHA:    8c5f00f135acd914b7aa20702562c49f81affd9a
ahead/behind:  30 / 0 against origin/main
remote branch: exists at the same SHA
existing PR:   none
worktree:      clean before this documentation change
```

已执行 `git fetch --all --prune`、远端 `ls-remote`、本地状态检查、分支差异检查、GitHub PR 查询和 Actions 查询。未发现需要覆盖的未知本地改动，也未发现同分支 PR。本文档不包含真实 Token、Ticket、私钥、恢复码、用户数据或生产地址。

### 已设计、编写并 push 的检查点

| 范围 | 远端提交 | 状态与边界 |
| --- | --- | --- |
| Monorepo 与 CI 骨架 | `1e7b2cab230e4af194cc567ee1e7ddb22f593afc` | 已编写并 push；创建根目录、组件目录和初始工作流 |
| Platform v1.3.7 Thin Host | `88da744cb80356801ee010b2e39537961ab9f6c2` | 已编写并 push；用官方 Thin Host 替换错误的 vendored Foundation，固定 Go/npm 依赖边界 |
| AWP v1 Proto | `847b54000c4ef6f414f98f78fcfa08b18742d6ba` | 已编写并 push；当前只证明 Schema 可编译，不是跨语言兼容验证 |
| ABA Rust/AAD Foundation | `56acbe7fc459e7ebe7961190a7a3f373806f50b6` | 已编写并 push；Rust 1.88、SDK 2.0.0 锁、严格配置和 148 字节 AAD；没有 Enrollment/WSS/加密/ACP Proxy |
| MVP 产品与架构契约 M0 | `083000c1008bb39a5a16cc76c3a6d8e0c028c072` | 已设计、编写并 push；MVP PRD、实现架构、实施与验证计划 |
| Platform Pure Domain M1.1 | `f3611d5b24b1f45d6a97668cf14898d45707575a` | 已编写并 push；Endpoint、Enrollment、Credential、Session、Ticket、Frame、ACK 等状态和单元测试 |
| Platform Repository/Migration M1.2 | `180a50e56efd97749c3b2bfa1212d94c2bf95603` | 已编写并 push；显式 Migration、SQLite Store、Enrollment/Ticket/Frame/ACK/Revocation 事务测试 |
| Platform Admin Module 后端 M1.3 | `8c5f00f135acd914b7aa20702562c49f81affd9a` | 已编写并 push；模块注册、Overview、Enrollment、Endpoint、Session、Delivery API 和 owner/tenant 隔离 |

分支相对 `main` 共 30 个提交、109 个文件变更，统计约为 27,944 行新增和 2,074 行删除。分支名仍为 Foundation，但 `docs/roadmap/IMPLEMENTATION.md` 已把目标扩展到 M0–M6 完整 MVP；本检查点只覆盖 Foundation、M0 和 M1 的部分后端范围。

### 远端 CI 证据

当前实现 HEAD `8c5f00f135acd914b7aa20702562c49f81affd9a` 对应 GitHub Actions Run `33813060590`，结论为 `success`。实际通过的 Job 与命令范围：

- `documentation`：`./scripts/check-docs.sh`；
- `platform-import`：Thin Host import boundary、`go test -count=1 ./...`、`go vet ./...`、Platform Admin Web `pnpm install --frozen-lockfile`、`pnpm lint`、`pnpm test`、`pnpm build`；
- `protocol`：安装 `protoc` 后运行 `./scripts/check-protocol.sh`；
- `aba-rust`：Rust 1.88.0 locked metadata、fmt、Clippy `-D warnings` 和 workspace tests；
- `hc-typescript`：Job 成功，但由于 `hc/package.json` 不存在而显式跳过，不能记为 HC 构建或测试通过。

历史失败保留如下，均使用后续新提交修复，没有改写远端历史：

- `c28d50afc9657cf177074577aaca167e06209ced` 的 Run `33811622386` 失败；随后以 `f2af0aded8949179488b25007e4686c3ca3affbc` 增加 Delivery Frame 映射。
- `f2af0aded8949179488b25007e4686c3ca3affbc` 的 Run `33811823581` 仍失败；随后以 `816f2fc623267ce18c5799d4bb0431d50bef4c6a` 修复持久化行解码，Run `33812007872` 通过。
- `6a222b004c247b1c69bc12cf419b3fd7606edc68` 的 Run `33812913067` 失败；随后以 `8c5f00f135acd914b7aa20702562c49f81affd9a` 改用公开 Store API 验证 tenant-scoped revoke，Run `33813060590` 通过。

本次补日志前只读取并核对上述远端 CI 证据，没有在本地重新运行完整构建或测试。本工作日志自身的提交和 push 发生后，必须等待该新 SHA 的 CI 结果，不能沿用 `8c5f00f` 的成功结论。

### 当前问题与未完成项

- M1 尚不能标记为完整完成：`harness_session_key_packages`、`harness_audit_events`、`harness_idempotency_records` 及对应服务/事务尚未实现。
- Admin Business Module 只有后端 API；中英文 Overview、Enrollment、Endpoint、Session、Delivery 页面仍不存在。
- 当前管理写 API 尚未形成计划要求的持久化 Audit 与完整 `Idempotency-Key` 行为。
- M2 的 JWK/ES256、Credential 签发、DPoP、Token Family、Ticket HTTP API 和 WSS Gateway 未实现。
- M3/M4 的 HPKE、Session 控制、密文 Relay、ABA Journal、Resume、故障恢复和在线吊销闭环未实现。
- ABA 仍是 Foundation：没有 Secure Store、Enrollment、WSS Connector、Process Supervisor、Journal 或 ACP Proxy。
- HC 仅有 README；没有 TypeScript Workspace、Endpoint、Crypto、Session 或 UI。
- 没有 Test Agent、Compose 演示环境或 `HC -> Platform -> ABA -> ACP Agent` 端到端链路。
- 尚未执行浏览器、微信真机、跨语言 Golden Vector、Race Detector、`mss verify --all`、Thin Host no-op upgrade、Opaque Canary、负向安全、故障注入、容量、依赖、许可证、SBOM 或正式秘密扫描。
- `docs/memory/project-memory.md` 的 Platform 引入方式和当前阶段仍描述旧的 vendored/Phase 0 状态，已被 ADR-0004 和 MVP 文档取代；后续应单独校正，避免在本日志提交中混入第二个意图。

### 安全与兼容性判断

- 当前提交没有实现可远程建立的 Endpoint 数据面，因此不能宣称 Token、Ticket、WSS、E2EE、重放防护或在线吊销已经安全验证。
- 已有 Store 测试覆盖 owner/tenant 隔离、Ticket 单消费者、Frame 冲突、ACK 单调和吊销的数据库状态级联；这不等于连接、Token、Rekey 或多实例行为已经验证。
- Platform 仍使用 ADR-0004 固定的 mss-boot-admin/admin-web v1.3.7 Thin Host 模式；本检查点没有更改 AWP Wire 字段号或密码学套件。

### 下一安全检查点

1. 提交并 push 本工作日志，等待新 SHA 的完整 CI。
2. 创建面向 `main` 的 Draft PR，标题和正文明确这是 Foundation + Platform M1 后端检查点，不是 MVP 完成 PR；不自动合并。
3. 在 PR 中优先审查 Thin Host 边界、Migration/Store 事务、owner/tenant 授权和管理 API 安全投影。
4. 后续用独立提交补齐 M1 Audit、Idempotency、Key Package 持久化与 Admin 页面，再决定合并检查点还是进入 M2。
5. M2 从共享 JWK/ES256/DPoP Golden Vector 开始，不先搭建可绕过身份验证的 WSS 通道。

---

## 2026-09-03 — 仓库初始化与完整设计基线

### 请求范围

- 初始化 `mss-boot-ai/harness-platform-monorepo`。
- 将当前完整设计和规划写入仓库。
- 所有长期记忆放入 `docs/`。
- 编写 `AGENT.md`。
- 明确 Platform 基于 `mss-boot-admin 1.3.7`。
- 编写完整 PRD。
- 设计文档和记忆进入 `main` 后，从最新 `main` 创建功能分支开始开发。
- 每个代码检查点先 commit/push，再执行测试和后续操作，防止运行环境异常造成内容丢失。

### 仓库检查

```text
repository:     mss-boot-ai/harness-platform-monorepo
initial state:  empty repository, no branches
visibility:     private
permissions:    admin/maintain/push/pull/triage available
created branch: main via first repository commit
```

### 外部基线核验

`mss-boot-admin`：

```text
repository:    mss-boot-io/mss-boot-admin
tag:           v1.3.7
tag object:    41c6517950f7f5f642418f5d4a49386e9c200b15
peeled commit: 77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:            1.26.6
license:       MIT
```

实际读取并检查了 v1.3.7 的：

- `go.mod`。
- `LICENSE`。
- `admin/middleware/auth.go`。
- `admin/apis/ws.go`。
- `admin/center/websocket/handler.go`。
- `mss-boot/core/server/task/server.go`。

确认该版本有可复用的服务端用户 Session、当前 Principal/Role 重新加载、Browser WebSocket 单次 Ticket/Origin 验证模式和 System Schedule Task Server。该检查只证明源码中存在这些机制，不等于 Harness Platform ACP 扩展已经实现或运行验证。

ACP：

```text
initial wire target: stable ACP v1
ABA language:        Rust
official SDK family: agentclientprotocol/rust-sdk 2.0.x
latest checked:      v2.0.0, published 2026-07-23
```

确认官方 SDK 2.0 保持稳定 ACP v1 Wire Schema，同时 SDK API 有破坏性变化；Draft ACP v2 为可选实验能力，不进入首发默认。

### 已提交并 push 到 main

每个文件均通过 GitHub 写入并产生远端提交：

| 提交 | 内容 |
| --- | --- |
| `01540ea847aebe24388b407572caf19de9affd0c` | 初始化 `README.md`，确定项目、术语、基线、边界和 Git 纪律 |
| `f44bc48d89f0a2b0522c0d98ef5f722906700878` | 完整 `AGENT.md` 仓库操作契约 |
| `8af1f1471ab84ec15ddc8522859c2203afeb6678` | 初始 `docs/README.md` 文档索引 |
| `46d5c5bf867619024fa8b823d90fad3aa4ec8cc0` | 完整产品需求 `docs/product/PRD.md` |
| `566c6736cdb83bee67234ae1a8dd88721ec0e6d5` | 总体架构 `ARCHITECTURE.md` |
| `60011985ec209727cf6fe6cd489c2ecfb469fd31` | 威胁、身份、证书、密码学和轮换 `SECURITY.md` |
| `89e0fc0369e6937bc53be087a127e035217af4a9` | AWP v1、Frame、ACK、Replay 和状态机 `PROTOCOL.md` |
| `c0e4155403ba6aeec4fb204506ba162948ff08dc` | mss-boot-admin 集成、数据模型和 API `PLATFORM.md` |
| `8689d4096b1a89cb2dd72572bfc0a5ccd7a1629c` | Rust ABA 完整内部设计 `ABA.md` |
| `11547e3cd1b60010302374451b3a8c03a989835f` | Web/小程序/原生 HC 设计 `HC.md` |
| `9c16556bd704635a4192457a9be128f026dc46ba` | 分阶段实施和检查点计划 |
| `f2f058b514de0c3f9b8448027c833ea1072c8fcb` | 验证矩阵、安全测试和生产门禁 |
| `a63786d94632fd912d2e708f7ce547bdca01eb9c` | Canonical Project Memory |
| `8c79f4614a61ee0b1b52adf8bc001a8876f1db4f` | Accepted/Proposed/Rejected 决策台账 |
| `d49075cfcf74d68587f62c33fcc7d8ce257e7294` | 上游、ACP、RFC 和工具参考索引 |
| `d13e680e535be3e0b10bc36c4a62f07e83b56e14` | ADR 流程和索引 |
| `3c014a0a9bdfb1fb46b1b0c565d91d8a6e8c30e0` | ADR-0001 三角色架构 |
| `2a36cd1d2722272b9266bf91d6830ce0f8d85506` | ADR-0002 mss-boot-admin v1.3.7 精确基线 |
| `9508e24d10c77c4d551efbcabc681f5a65984e8b` | ADR-0003 默认 Opaque 与独立 AWP |
| `a593794b977e25353261e22aff64449cdc2f948b` | `AGENTS.md` 工具兼容入口，唯一契约仍是 `AGENT.md` |
| `8689430ee327639ac84eb860005510342f3f97af` | 文档索引加入 ADR 阅读入口 |

本日志自身的提交会成为上述设计序列之后的新 `main` HEAD。创建功能分支时必须读取并记录该精确 HEAD，而不能根据本表猜测。

### 关键设计结果

- Platform、ABA、HC 三角色边界已冻结。
- ABA 正式名称 `acp-brige-agent`、Rust、默认无入站端口。
- Platform 固定 mss-boot-admin v1.3.7，不跟随浮动 main。
- Endpoint Signing/KEM 私钥本地产生，每端独立；Platform 不托管。
- Human Identity、Endpoint Identity 和当前授权取交集。
- DPoP + 单次 WSS Ticket + 升级后 Challenge。
- 默认 Opaque Mode，ABA 生成 SRK，Platform 只中继 Key Package 和密文。
- AWP v1 使用 Binary Protobuf，Canonical AAD 固定 148 字节。
- Platform 提供至少一次密文交付；ABA Journal 去重，危险崩溃进入 UNCERTAIN。
- Platform/HC 不能下发任意 command/args/cwd/env，只引用本地 Runtime/Workspace ID。
- Root/UCA、Endpoint Credential/Key、Token 和 Session Key 分层轮换。

### 实际验证状态

**已实际完成**：

- GitHub 仓库可访问和写权限检查。
- 空仓库/默认分支状态检查。
- 所有上表文档的远端创建/更新和 push。
- mss-boot-admin v1.3.7 标签对象、peeled commit、Go 版本、License 和若干关键源码的静态读取。
- 官方 ACP Rust SDK v2.0.0 Release 信息静态读取。

**未执行**：

- Markdown Lint、链接检查和本地文档构建。
- Platform/ABA/HC 代码构建；此时尚未创建功能代码。
- AWP Proto Lint、生成和互操作测试；此时尚未创建 Schema 文件。
- Rust `cargo`、Go `go test`、前端 `pnpm`。
- 浏览器、微信小程序真机、真实 ACP Agent。
- DPoP、Ticket、加密、轮换、重放、故障注入和安全测试。
- Platform 上游完整源码实际导入。

因此当前只能描述为：**完整设计基线已编写并提交到 main，尚未实现和验证产品功能。**

### 下一安全检查点

1. 读取本日志提交完成后的最新 `main` SHA。
2. 从该 SHA 创建 `codex/bootstrap-harness-platform-foundation`。
3. 先提交并 push Monorepo/上游锁/协议/ABA 基础代码检查点。
4. 再执行可用的静态、构建和测试验证。
5. 所有修复作为新提交 push，并在本日志的功能分支版本中追加证据。

---

## 追加规则

后续记录按时间追加在上方最新位置或保持明确时间顺序，不能删除失败记录。若压缩旧日志，原始证据必须仍能从 Git 历史恢复。

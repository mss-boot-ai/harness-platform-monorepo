# Harness Platform 工作与验证日志

- **状态**：Append-only operational memory
- **时区约定**：每条记录必须写明时区；本轮日期按 2026-09-03 记录。
- **纪律**：记录实际完成的工作、远端提交和验证证据；失败与未执行项同样保留。不要把本文件改写成只显示成功的宣传材料。

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
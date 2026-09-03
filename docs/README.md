# Harness Platform 文档索引

本目录保存 `harness-platform-monorepo` 的全部长期设计、产品需求、架构决策、项目记忆、实施计划和验证证据。根目录只保留仓库入口和 Agent 操作契约；任何长期记忆不得散落在根目录或个人临时文件中。

## 1. 建议阅读顺序

1. [`../AGENT.md`](../AGENT.md)：仓库操作、安全边界和开发纪律。
2. [`memory/project-memory.md`](memory/project-memory.md)：项目当前事实、命名、基线和状态。
3. [`memory/decisions.md`](memory/decisions.md)：已经确认且不得擅自推翻的决策。
4. [`adr/README.md`](adr/README.md)：重大架构、安全和协议决策记录。
5. [`product/PRD.md`](product/PRD.md)：完整产品需求、角色、场景、范围和验收标准。
6. [`architecture/ARCHITECTURE.md`](architecture/ARCHITECTURE.md)：系统总体架构与组件边界。
7. [`architecture/SECURITY.md`](architecture/SECURITY.md)：信任模型、身份、证书、密码学和轮换。
8. [`architecture/PROTOCOL.md`](architecture/PROTOCOL.md)：ABA Wire Protocol、加密 Frame、状态机与可靠性语义。
9. [`architecture/PLATFORM.md`](architecture/PLATFORM.md)：基于 mss-boot-admin v1.3.7 的 Platform 集成、数据模型和 API。
10. [`architecture/ABA.md`](architecture/ABA.md)：Rust `acp-brige-agent` 的内部设计。
11. [`architecture/HC.md`](architecture/HC.md)：Web、小程序和后续原生 HC 的设计。
12. [`roadmap/IMPLEMENTATION.md`](roadmap/IMPLEMENTATION.md)：分阶段实施、分支和检查点计划。
13. [`roadmap/VERIFICATION.md`](roadmap/VERIFICATION.md)：测试矩阵、验收门禁和证据要求。
14. [`memory/work-log.md`](memory/work-log.md)：实际工作、提交、验证与未完成项记录。
15. [`references.md`](references.md)：上游版本、标准和外部规范引用。

## 2. 文档权威顺序

发生冲突时按以下顺序处理：

1. 用户最新明确指令。
2. 已合并 ADR 或 `memory/decisions.md` 中状态为 `Accepted` 的决策。
3. PRD 的产品范围和验收标准。
4. 架构与协议文档。
5. 实施和验证计划。
6. 项目记忆与工作日志。
7. 代码注释和临时讨论。

代码与文档不一致时，不默认以代码为准。必须确认是实现缺陷还是设计已经变更，并在同一检查点修正文档或实现。

## 3. 固定基线

### Platform

```text
upstream:    mss-boot-io/mss-boot-admin
tag:         v1.3.7
tag object:  41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:          1.26.6
```

### ABA / ACP

```text
name:         acp-brige-agent
short name:   ABA
language:     Rust
ACP wire:     stable v1
Rust SDK:     official agentclientprotocol/rust-sdk 2.0.x
lock policy:  exact Cargo.lock
ACP v2:       disabled by default and outside initial compatibility promise
```

## 4. 项目术语

| 术语 | 含义 |
| --- | --- |
| Platform | 基于 mss-boot-admin 的后台控制面、身份中心、证书中心、加密中继与持久化平台 |
| ABA | `acp-brige-agent`，Rust 轻量 ACP 桥接代理 |
| HC | H 端客户端，包括微信小程序、Web、App 和桌面端 |
| Endpoint | 一个独立安装实例，拥有独立签名密钥、KEM 密钥、证书和吊销状态 |
| AEC | ABA Endpoint Certificate |
| HEC | HC Endpoint Certificate |
| UCA | User Communication CA，用户或租户级通信签发域 |
| SRK | Session Root Key，由 ABA 为 ACP Session 生成 |
| Key Package | 使用目标 Endpoint KEM 公钥封装的 SRK 或下一代会话密钥材料 |
| AWP | ABA Wire Protocol，Platform、ABA、HC 之间的版本化外层协议 |
| Opaque Mode | Platform 只中继和保存密文，不能读取 ACP 明文的默认模式 |
| Managed Mode | 用户显式授权 Platform 受控解密的可选模式，不属于端到端加密 |

## 5. 文档状态

每份设计文档头部应使用以下状态之一：

- `Draft`：正在讨论，不能作为兼容承诺。
- `Accepted`：已经确认，实施不得擅自偏离。
- `Implemented`：已实现，但仍需查看验证状态。
- `Verified`：已按验证文档执行并留下证据。
- `Superseded`：已被新的 ADR 或文档取代。

“已 push”不是 `Verified`。

## 6. 变更规则

以下变更必须同步更新文档：

- 名称、角色或信任边界。
- mss-boot-admin 或 ACP SDK 基线。
- Endpoint 身份、证书、Token、Ticket 或轮换规则。
- Wire Schema、Canonicalization、AAD、签名输入或兼容性。
- 数据库表、唯一约束、状态机或保留策略。
- Runtime/Workspace 授权模型。
- Platform、ABA、HC 的职责边界。
- 用户流程、MVP 范围或验收标准。
- 构建、测试、部署和安全门禁。

重大变更先写 ADR，再改主设计；普通澄清可直接修改对应文档并在 `memory/work-log.md` 留痕。

## 7. 记忆规则

`docs/memory/` 中只记录可长期复用的事实与工作证据：

- 不保存私钥、Token、Ticket、恢复码、生产凭据或真实用户敏感数据。
- 不复制聊天全文，只保留已经确定的结论和必要上下文。
- 每条重要决策写清日期、状态、原因和影响。
- 每个工作检查点记录分支、提交 SHA、已执行验证和未验证项。
- 新会话开始时优先读取记忆文件，不依赖模型自身对历史对话的回忆。
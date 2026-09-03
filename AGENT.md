# Harness Platform Agent Operating Contract

本文件是本仓库中所有 AI 开发代理、自动化编码工具和人工协作者的强制工作契约。开始任何设计、编码、重构、测试、发布或仓库操作前，必须完整阅读本文件以及 `docs/README.md` 指向的相关文档。

## 1. 项目术语不可漂移

以下名称已经确定，未经 ADR 不得重命名、扩写成其他含义或引入竞争术语：

- **Platform**：基于 `mss-boot-admin` 的后台控制面、身份中心、证书中心、加密中继和持久化平台。
- **ABA**：`acp-brige-agent` 的简称；它是 Rust 编写的轻量 ACP 桥接代理。
- **HC**：H 端客户端，包括微信小程序、Web、原生 App 和桌面端。
- **ACP Session**：HC 经 Platform 与某个 ABA 背后的本地 ACP Agent 建立的逻辑会话。
- **Endpoint**：一个具有独立密钥、证书和吊销状态的 ABA 或 HC 安装实例。

`acp-brige-agent` 的拼写是当前正式项目名称。不要擅自修正为 `acp-bridge-agent`。

## 2. 固定上游基线

Platform 必须基于以下精确上游版本开发：

```text
repository: mss-boot-io/mss-boot-admin
tag:        v1.3.7
tag object: 41c6517950f7f5f642418f5d4a49386e9c200b15
commit:     77b53d41092741eac62fa6418c0bdbf87413c7cd
go:         1.26.6
```

强制规则：

1. 不得把 `mss-boot-admin/main`、`latest`、浮动分支或未固定 Docker 标签作为构建基线。
2. 上游升级必须先新增 ADR，包含升级原因、旧/新提交、变更范围、数据库迁移、API/前端兼容性、验证计划和回滚方案。
3. 不得为了省事绕开 mss-boot-admin 已有的认证、RBAC、Session、任务调度、审计、配置和前端框架另造平行系统。
4. 扩展点不足时，优先在 Platform 内形成边界清晰的 ACP 模块；不得把重操作塞回 ABA。
5. 对上游源码的引入方式必须可重现，并能证明最终构建确实来自上述提交。

ACP 首发兼容基线：

```text
wire protocol: ACP v1 stable
ABA SDK:       official agentclientprotocol/rust-sdk 2.0.x
lock policy:   exact Cargo.lock
ACP v2:        experimental only; disabled by default
```

## 3. 开工前必须执行

每次工作开始时，必须先完成并记录：

1. 获取远端仓库和当前分支的最新状态。
2. 记录当前分支名和准确 HEAD SHA。
3. 检查是否存在同名远端分支、未合并 PR 或其他人的新提交。
4. 阅读 `docs/memory/project-memory.md`、`docs/memory/decisions.md` 和 `docs/memory/work-log.md`。
5. 阅读本次改动涉及的 PRD、架构、ADR、协议和测试文档。
6. 确认没有秘密、真实令牌、私钥、恢复码、用户数据或生产地址即将写入仓库。

不得重复询问仓库中已经明确记录的决策。发现文档互相冲突时先停止相关实现，在 `docs/memory/decisions.md` 记录冲突，并通过 ADR 解决。

## 4. Git 与防丢失纪律

### 4.1 main 分支

设计基线和长期记忆的首轮落盘允许按用户明确指令提交到 `main`。完成该基线后：

- 功能代码不得直接提交到 `main`。
- 必须从最新 `main` 创建 topic branch。
- 禁止 force-push、rebase 已共享分支、改写远端历史或覆盖未知工作。
- 合并必须通过明确审查流程；除非用户再次明确授权，不自动合并功能分支。

### 4.2 检查点提交

为防止运行环境或长会话异常导致内容丢失，严格采用以下顺序：

1. 完成一个单一、可恢复的代码或文档检查点。
2. 检查差异中无秘密、生成物污染或无关改动。
3. 立即 commit 并 push。
4. 再进行后续构建、测试、浏览器验收、真机验证或下一项开发。
5. 测试发现问题时，以新的修复提交解决并再次 push，不重写已推送历史。

首次检查点提交可以是尚未验证的实现，但提交消息或工作记录必须明确 `unverified`。任何人不得把“已提交/已 push”描述成“验证通过”。

推荐提交类型：

```text
docs: ...
chore: ...
feat(protocol): ...
feat(aba): ...
feat(platform): ...
feat(hc): ...
fix(...): ...
test(...): ...
refactor(...): ...
```

## 5. 状态陈述必须诚实

所有阶段总结必须分开说明：

- 已设计。
- 已编写。
- 已提交并 push。
- 已实际构建。
- 已实际运行测试。
- 已执行浏览器/小程序/真机/跨端验证。
- 已执行安全测试。
- 尚未验证或需要其他环境验证。

禁止使用“完成”“可用”“稳定”“安全”“生产就绪”等笼统表述掩盖未执行验证。

## 6. 架构边界

### 6.1 Platform 负责

- 用户、租户、组织和 RBAC。
- 人类身份认证与 Platform Session。
- ABA/HC Endpoint 注册、审批、状态和吊销。
- Platform Root、用户通信 CA、端点证书和证书状态清单。
- 手动轮换、定期轮换、紧急轮换与轮换任务编排。
- DPoP/Proof-of-Possession 校验与短期 Access Token。
- 一次性 WebSocket Ticket、连接鉴权和在线路由。
- ACP 加密 Frame 的存储、ACK、离线重放、背压和保留策略。
- ACP Session、参与者、Runtime Profile、Workspace 授权映射。
- 审计、通知、运营管理、监控和后台任务。
- Platform Web 管理界面和对 HC 的公共 API。

### 6.2 ABA 只负责必要本地职责

- 本地生成、保护和使用 Endpoint 私钥。
- 缓存 Platform 信任根、端点证书、清单和发给自己的 Key Package。
- 主动建立到 Platform 的 HTTPS/WSS 出站连接。
- ACP Agent 子进程的启动、停止、进程组回收和协议连接。
- 把加密通道中的 ACP JSON-RPC 原样桥接到本地 ACP Transport。
- 加密、解密、签名、验签、序列号和重放窗口。
- 本地 Runtime Profile 与 Workspace 白名单。
- 极小型、有上限的可靠性 Journal 和崩溃恢复状态。
- 本地资源限制和故障关闭。

ABA 不得承担用户管理、RBAC、全局会话索引、证书签发、复杂工作流、全文搜索、运营后台、服务端 AI 摘要或大规模消息数据库。

### 6.3 HC 负责

- 用户登录和交互界面。
- 本地生成 HC Endpoint 密钥并完成端点注册。
- 缓存端点证书、Platform Trust Manifest 和 Session Key。
- 获取一次性 WebSocket Ticket，并完成连接首帧挑战。
- 加解密 ACP JSON-RPC、展示流式结果和发起权限确认。
- 不把私钥、Session Root Key 或明文 ACP 内容交给 Platform。

## 7. 安全不变量

任何实现不得破坏以下不变量：

1. Platform 不生成或保存 ABA/HC Endpoint 私钥。
2. 不同端点不得共享同一私钥或同一端点证书。
3. 身份签名密钥与密钥交换密钥必须分离。
4. ACP 内容默认使用端到端应用层加密；TLS 1.3 仍然必需。
5. Platform 默认只存密文，不得把 Prompt、Tool 参数、源码、Diff 或 Agent 输出写入普通日志。
6. Access/Refresh Token 必须与端点持有证明绑定，不能降级为长期裸 Bearer Token。
7. WebSocket 必须使用短期、单次消费、绑定用户 Session、Endpoint、Origin 和用途的 Ticket。
8. WebSocket 升级后必须再完成端点私钥挑战，未通过不得进入 READY。
9. Platform 或 HC 不能向 ABA 下发任意可执行文件、Shell 命令、cwd、环境变量或任意路径。
10. ABA 只接受本地配置中存在的 Runtime Profile ID 和 Workspace ID。
11. 同一方向、同一 Key 下 AEAD Nonce 永不重复；Sequence 不能回退。
12. 所有入站 Frame 必须执行大小限制、证书状态检查、签名检查、AAD 校验、序列校验和重放检测。
13. 吊销 HC 后必须关闭连接、撤销 Token、禁止新 Key Package，并触发相关 Session Key 轮换。
14. 证书或清单更新失败时不得自动信任未知新根。
15. 轮换期间旧 Key 只能在明确 Grace Window 内用于解密，不得继续发送新消息。
16. 不确定是否已将高风险 ACP 请求交给本地 Agent 时，状态必须是 `UNCERTAIN`，不得自动重复执行。
17. 日志、错误和指标不得包含密钥、Token、Ticket、完整证书请求、明文 ACP 或真实用户敏感内容。
18. Managed Decryption 模式只能由用户明确启用，必须独立审计，不能宣传为端到端加密。

发现任何代码需要打破上述不变量时，停止实现并先提交安全 ADR。

## 8. 身份与授权原则

Platform 对请求的有效权限必须是交集，而不是只相信某个 JWT：

```text
EffectivePermission =
    CurrentUserRBAC
  ∩ EndpointCertificateScopes
  ∩ AccessTokenScopes
  ∩ SessionACL
  ∩ RuntimeAndWorkspacePolicy
```

ABA Principal 和 HC Principal 必须使用不同 Token Type 与 Audience。不得把普通后台用户 Token 用作 ABA 设备凭据，也不得允许 ABA Token 调用普通后台管理 API。

端点注册必须证明同时持有：

- Endpoint Signing Key；
- Endpoint KEM Key；
- 与当前 Platform 用户或设备审批流程相匹配的授权上下文。

不得使用 MAC、硬盘序列号、CPU 序列号等稳定硬件标识作为主身份；安装 ID 使用安全随机值。硬件信息只可作为低信任展示信号。

## 9. 协议实现规则

- ACP JSON-RPC 作为不透明 UTF-8 Payload 原样传输，不转换成 Platform 自定义业务事件。
- 外层 ABA Wire Protocol 使用版本化二进制 Schema，并拥有独立兼容策略。
- 必须保留 ACP JSON-RPC Batch 的边界，不能把一个 Batch 悄悄拆成语义不同的独立调用。
- `message_id`、`channel_id`、`session_id`、`sequence`、`key_id` 和方向是可靠性与加密 AAD 的一部分。
- Protocol Schema、实现和测试向量必须同一提交演进。
- 未知字段按协议版本策略处理；不得无声丢弃影响安全的字段。
- Protocol v1 一旦发布，破坏性变更只能进入新的 Wire Major Version。
- 所有时间戳只用于审计和过期辅助，不能替代 Sequence/Nonce 的唯一性保证。

## 10. 密码学实现规则

- 只使用经过审查的成熟密码学库，不手写加密原语。
- 首发算法套件以架构文档和协议常量为准；算法标识必须进入 Frame/Manifest Versioning。
- 私钥对象尽量不可导出，敏感内存使用 `zeroize`/等价机制。
- 随机数必须来自操作系统 CSPRNG。
- 密钥派生必须包含用途、方向、Session 和 Generation 的域分离信息。
- AEAD Header 必须作为 AAD；密文及 AAD 摘要必须由发送端点签名。
- 测试向量不得包含生产密钥；固定测试密钥必须明确标记为测试专用。
- 不得发明自定义证书格式却不写规范、Canonicalization、签名输入和验证规则。

## 11. Platform 开发规则

- 优先复用 mss-boot-admin v1.3.7 的用户、角色、Session、审计、WebSocket Ticket 思路、任务调度、数据库和前端基础设施。
- ACP Gateway 与现有通知 WebSocket Hub 分离：前者面向二进制 Frame、Endpoint 路由、ACK、Replay 和 Backpressure。
- 不为每个用户创建 Cron Job；使用固定数量系统扫描任务，通过 `next_*_at` 字段驱动。
- 数据库唯一约束和事务必须承担幂等保证，不能只依赖应用内锁。
- 密文 Frame 与普通业务表分离，配置容量上限、保留时间和分区/归档策略。
- KMS/Signer 通过接口适配，生产模式不得把 Root/CA 私钥明文放入数据库、配置文件或环境变量。
- 所有安全敏感 API 必须有审计事件、细粒度权限和幂等键。

## 12. ABA Rust 开发规则

- 使用稳定 Rust 工具链并提交 `Cargo.lock`。
- 依赖官方 ACP Rust SDK 的稳定 v1 API；不得默认启用 draft v2。
- Runtime/Workspace 配置解析必须严格，未知字段和重复 ID 失败关闭。
- ABA 默认不监听公网或局域网端口。
- Journal 必须有磁盘上限、事务语义和清理策略。
- 子进程必须使用独立进程组；取消、超时、ABA 退出时可靠回收。
- 禁止 Platform 直接提供命令、参数、工作目录或环境变量；本地 Profile 决定这些值。
- 关键状态机必须使用显式枚举，禁止通过多个布尔值拼装安全状态。

## 13. HC 开发规则

- Web 和小程序的能力差异必须明确，不假设浏览器支持传统客户端证书或任意自定义 WebSocket Header。
- WebSocket 采用“认证 HTTPS 获取一次性 Ticket + Subprotocol 携带 Ticket + 首帧签名挑战”。
- Web HC 的安全级别不得夸大；Platform 被完全攻破并替换 Web JavaScript 时，浏览器运行环境无法提供原生 App 等级的保护。
- 高风险操作要求二次验证或高可信端点确认。
- 本地存储失败时不得退化为明文保存私钥和 Session Key。
- 前端日志和遥测不采集 ACP 明文。

## 14. 测试与验证要求

每个功能至少考虑以下层次，并在工作记录中写明实际执行项：

- 单元测试。
- 状态机和属性测试。
- 协议 Golden/Test Vector。
- 数据库迁移与回滚测试。
- Platform API 集成测试。
- ABA 与模拟 ACP Agent 的进程和传输测试。
- HC/Platform/ABA 端到端测试。
- 断线、乱序、重复、延迟、丢包和磁盘满故障注入。
- 证书过期、吊销、时钟偏差、轮换中断和旧 Key 重放。
- 权限越权、跨租户、Ticket 重用、DPoP 重放和 Origin 攻击。
- 日志与错误中的秘密扫描。
- 依赖漏洞、许可证、SBOM 和制品可重复性检查。

禁止通过删除、跳过或放宽测试来让 CI 变绿。需要暂时隔离不稳定测试时，必须记录原因、负责人和恢复条件。

## 15. 文档与记忆维护

所有长期记忆文件必须位于 `docs/`：

- 项目事实和当前状态：`docs/memory/project-memory.md`
- 已确认决策：`docs/memory/decisions.md`
- 工作与验证记录：`docs/memory/work-log.md`
- 架构决策：`docs/adr/`
- 产品需求：`docs/product/`
- 架构设计：`docs/architecture/`
- 路线与测试计划：`docs/roadmap/`

代码改动若改变契约、状态机、数据库、接口、安全边界或操作方式，必须在同一检查点更新对应文档。不要在根目录、个人主目录或未跟踪临时文件中保存项目长期记忆。

## 16. 完成条件

一个工作项只有在以下内容都明确后才可以结束：

1. 设计/实现范围与未完成项。
2. 精确分支和提交 SHA。
3. 提交已 push 的证据。
4. 实际执行的命令与测试结果。
5. 未执行验证及其所需环境。
6. 安全和兼容性影响。
7. 文档与记忆是否同步。
8. 下一项工作从哪个稳定检查点继续。

不要承诺后台继续工作，不要虚构测试结果，不要把 GitHub push 当成运行验证。
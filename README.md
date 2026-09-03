# Harness Platform Monorepo

`harness-platform-monorepo` 是面向 AI 编程 Agent 的远程安全控制与协作平台单仓库。

本项目由三个核心角色组成：

- **Platform**：基于 `mss-boot-admin` 的后台控制面、身份中心、证书中心、加密中继、持久化与运维平台。
- **ABA**：`acp-brige-agent` 的简称。使用 Rust 开发，作为部署在个人电脑、服务器或容器中的轻量 ACP 桥接代理。
- **HC**：H 端客户端的统称，包括微信小程序、Web、未来的原生 App 与桌面端。

> `acp-brige-agent` 是当前项目确定的正式名称和拼写。除非通过 ADR 明确变更，代码、文档、制品和接口中不得擅自改名。

## 固定技术基线

Platform 必须基于以下不可漂移的上游基线开发：

- 上游仓库：`mss-boot-io/mss-boot-admin`
- 标签：`v1.3.7`
- 标签对象：`41c6517950f7f5f642418f5d4a49386e9c200b15`
- 剥离后的源提交：`77b53d41092741eac62fa6418c0bdbf87413c7cd`
- Go 版本基线：`1.26.6`

任何升级必须先提交 ADR，记录升级原因、兼容性影响、迁移方案、验证证据以及回滚路径。不得把上游 `main`、浮动分支或未固定版本作为构建输入。

ACP 首发协议与 SDK 基线：

- ACP 稳定 Wire Protocol：`v1`
- ABA 语言：Rust
- 官方 SDK：`agentclientprotocol/rust-sdk` 的 `2.0.x` 系列，具体补丁版本由 `Cargo.lock` 固定
- ACP 草案 `v2` 不进入首发兼容承诺；实验性支持必须通过独立 Feature Flag 和 ADR 引入

## 核心设计原则

1. **ABA 足够轻**：只承担本地安全边界、ACP 生命周期、加解密、协议桥接、最小可靠性日志和主动出站连接。
2. **重操作归 Platform**：用户、租户、RBAC、设备、证书、轮换、会话、路由、密文存储、离线重放、审计、通知、策略和定时任务全部由 Platform 承担。
3. **端点私钥本地产生**：Platform 可以签发和管理证书，但不得生成、保存或导出 ABA/HC 的端点私钥。
4. **每个端点独立身份**：每个 ABA、Web HC、小程序 HC、App HC 均拥有独立签名密钥、密钥交换密钥、端点证书和吊销状态。
5. **ACP 内容默认端到端加密**：Platform 默认仅能看见路由和可靠性元数据，不能读取 ACP JSON-RPC 明文。
6. **TLS 不能省略**：应用层加密不替代 TLS 1.3；TLS 保护握手、注册、证书获取和传输元数据。
7. **本地不开放入站端口**：ABA 默认只建立到 Platform 的主动出站 HTTPS/WSS 连接。
8. **Platform 不能下发任意 Shell**：Platform/HC 只能引用 ABA 本地预先配置的 Runtime Profile 与 Workspace ID。
9. **故障关闭**：身份、证书、轮换、重放和权限状态不确定时拒绝执行，而不是降级为弱认证或明文。
10. **设计、实现、验证分开陈述**：提交或 push 不等于构建通过、测试通过或安全验证通过。

## 计划中的单仓库布局

```text
.
├── AGENT.md                 # 所有自动化开发代理必须遵守的仓库级提示
├── docs/                    # 设计、PRD、ADR、项目记忆、计划和验证证据
├── protocol/                # ABA Wire Protocol、Schema、测试向量和兼容性契约
├── aba/                     # Rust acp-brige-agent
├── platform/                # 基于 mss-boot-admin v1.3.7 的 Platform
├── hc/                      # Web、小程序与后续原生 HC
├── deploy/                  # 本地、Docker、Kubernetes 等部署资产
└── scripts/                 # 可重复执行的检查、同步和验证脚本
```

## 文档入口

完整设计、PRD、威胁模型、身份注册、加密协议、轮换机制、可靠性语义、数据模型、开发路线和长期记忆均放在 `docs/`。`docs/README.md` 是唯一文档索引与阅读顺序入口。

## Git 与恢复纪律

本项目以防止长会话或运行环境异常造成工作丢失为最高工程纪律之一：

- 设计与长期记忆先进入 `main`，再从最新 `main` 创建功能分支。
- 功能开发不得直接提交到 `main`。
- 每完成一个可独立恢复的代码或文档检查点，立即 commit 并 push 后再进行下一步。
- 禁止 force-push、改写共享历史或覆盖未知远端工作。
- 每次开始工作先读取远端最新提交、当前分支和已有提交。
- 每次提交保持单一意图，并记录实际执行过的验证。
- 未执行的构建、测试、浏览器验收、真机测试和安全测试必须明确标记为未验证。

## 当前状态

仓库正在建立设计基线。实现分支将在完整设计与项目记忆提交到 `main` 后创建。
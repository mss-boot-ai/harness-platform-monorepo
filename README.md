# Harness Platform Monorepo

Harness Platform 是面向 AI 编程 Agent 的远程安全控制与协作平台。

```text
HC ⇄ Platform ⇄ ABA ⇄ Local ACP Agent
```

- **Platform**：集中控制面、身份与证书、Opaque 密文中继、可靠性存储、审计和运营后台。
- **ABA**：`acp-brige-agent`，Rust 轻量本地安全桥接，只主动出站连接。
- **HC**：Web、微信小程序、App 与桌面客户端。

## Platform 基线：import，不复制源码

Platform 必须使用 `mss-boot-admin v1.3.7` 的正式 Thin Host 模型：

```text
backend:  github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7
frontend: @mss-boot-io/admin-web@1.3.7
```

`platform/` 只保存组合胶水、Harness 业务模块、业务页面、配置、Migration 和测试。禁止复制 Foundation 的 Admin、Framework、模板或完整前端源码，禁止通过本地 `replace` 绕过公共版本。

这样保留官方 `mss upgrade admin` 的三方升级能力，让 Foundation 升级只更新受管文件，同时保留 Harness 自有代码。

## 固定发布身份

```text
tag:         v1.3.7
tag object:  41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:          1.26.6
```

升级必须先 ADR、只读计划、显式应用和完整回归；不得使用浮动 main/latest。

## 核心原则

1. 每个 ABA/HC 独立生成签名与 KEM 密钥；Platform 不持有端点私钥。
2. ACP JSON-RPC 默认端到端加密，Platform 只保存密文和最小路由元数据。
3. TLS、DPoP、短期单次 WSS Ticket 和连接首帧挑战共同保护身份。
4. Platform 只能引用 ABA 本地 Runtime/Workspace ID，不能下发任意命令、路径或环境变量。
5. Sequence/Nonce 不回退；执行结果不确定时进入 `UNCERTAIN`，不自动重放。
6. 所有队列、Frame、Journal 和重试有明确上限。
7. 写完一个检查点立即 commit/push，再做完整构建、测试和验证。
8. 已 push 与已验证严格分开陈述。

## Monorepo

```text
.
├── AGENT.md
├── docs/
├── protocol/
├── aba/
├── platform/      # mss-boot-admin v1.3.7 Thin Host
├── hc/
├── deploy/
└── scripts/
```

完整产品要求见 `docs/product/PRD.md`，架构见 `docs/architecture/`，长期决策和验证证据全部位于 `docs/`。

## 当前目标

在 `codex/bootstrap-harness-platform-foundation` 分支完成可演示、可恢复、默认 Opaque 的 MVP，实际验证后向 `main` 创建 PR，不自动合并。

# ADR-0001：Platform、ABA 与 HC 三角色架构

- **状态**：Accepted
- **日期**：2026-09-03
- **决策所有者**：Harness Platform 项目
- **取代**：无
- **被取代**：无

## 背景

目标产品需要让用户通过 Web、微信小程序和后续原生客户端远程控制运行在个人电脑、服务器或 Kubernetes Pod 中的 ACP 编程 Agent。系统必须避免暴露本地入站端口、支持身份和证书管理、密文中继、离线恢复、权限确认和长期运营。

若把用户、证书、全局会话、存储、路由、审计和运营都放在本地 Agent，会使本地组件过重、难以跨环境部署，也会导致每台机器重复实现后台能力。若全部放在 Platform，则服务端可能获得本地任意执行能力和端到端解密密钥，破坏本地安全边界。

## 决定

采用三角色架构：

1. **Platform**：基于 mss-boot-admin 的服务端控制面、身份中心、证书中心、加密中继、持久化、审计、通知和运营后台。
2. **ABA**：正式名称 `acp-brige-agent`，简称 ABA，使用 Rust 开发；只承担本地不可外移的安全与桥接职责。
3. **HC**：H 端客户端统称，包括 Web、微信小程序、原生 App 和桌面端；承担用户交互、Endpoint Identity 和会话内容加解密。

数据流：

```text
HC <— TLS 1.3 + AWP —> Platform <— TLS 1.3 + AWP —> ABA <— local ACP v1 —> Agent
```

## 职责分配

### Platform

- Human Identity、租户、用户、RBAC 和服务端 Session。
- ABA/HC Endpoint 注册、审批、状态和吊销。
- Root/UCA、AEC/HEC、Trust Manifest、Token、DPoP、Ticket 和轮换编排。
- ACP Session Registry、Participant、Runtime/Workspace Grant。
- Binary Gateway、密文 Frame、ACK、Replay、Backpressure 和配额。
- 审计、通知、后台任务、运营和管理界面。

### ABA

- 本地生成和保护 Endpoint Signing/KEM Private Key。
- Platform Trust/Endpoint Credential 验证。
- 主动出站连接、AWP 加解密和签名。
- 本地 Runtime Profile/Workspace 白名单。
- ACP Agent 子进程生命周期和标准 ACP v1 Proxy。
- 最小有界 Journal、Sequence 和 UNCERTAIN 状态。

### HC

- Human Login、用户界面和权限确认。
- 本地 HC Endpoint Key/HEC/DPoP。
- Ticket/Challenge、Key Package、Session Key 和 AWP 加解密。
- ACP 流式内容、断线/重连和安全状态展示。

## 选项

### 选项 A：全部能力放本地 Agent

拒绝。组件过重，无法统一部署；证书、路由、运营和全局存储重复建设。

### 选项 B：全部能力放 Platform

拒绝。Platform 会获得本地任意执行能力和内容密钥，失陷影响过大。

### 选项 C：HC 直连本地 ABA

拒绝作为默认架构。需要入站端口、NAT/防火墙/发现配置，移动端和跨网络体验差。

### 选项 D：三角色分层

接受。把重控制面集中到 Platform，同时保留 ABA 的本地安全边界和 HC 的端到端加密能力。

## 理由

- ABA 足够轻，适合个人电脑、服务器、容器和 Pod。
- Platform 可复用成熟后台并水平扩展。
- HC/ABA 能在 Platform 只看密文的情况下完成 ACP 会话。
- 本地真实命令和路径只由用户配置，Platform 不能成为通用远程 Shell。
- Endpoint 能独立识别、吊销和轮换。

## 后果

正面：

- 组件边界清晰，安全责任可测试。
- 所有远端通信从 ABA 主动出站，减少网络攻击面。
- Platform 可以处理可靠性和运营而不默认获得明文。
- 新 HC、新 ACP Agent 和新部署方式都有明确扩展点。

成本：

- 需要跨 Rust、Go、TypeScript 的协议和密码学互操作。
- Opaque Mode 限制服务端全文搜索和摘要。
- 多端密钥分发、断线恢复和轮换状态机复杂。
- Web、小程序和原生客户端需要不同安全存储适配。

## 安全与隐私影响

- Platform 不能生成或保存 Endpoint 私钥。
- ABA 默认无入站监听端口。
- Platform/HC 不能发送任意 command、args、cwd、env。
- ACP 内容默认端到端加密。
- 三角色之间的身份、授权和信任边界必须独立验证。

## 兼容与迁移

这是初始架构，无旧系统迁移。未来若拆分 Platform 服务，外部 Platform/ABA/HC/AWP 契约保持，内部服务边界可演进。

正式名称 `acp-brige-agent` 若未来修正拼写，需要独立 ADR，覆盖二进制、包、服务、配置和文档兼容别名。

## 验证

- ABA 构建中无后台用户/证书签发/大规模历史模块。
- ABA 默认不监听端口。
- Platform Gateway 可中继密文但无法解析 ACP Method。
- Platform 发送含 command/args/cwd/env 的伪造控制消息被协议或 ABA 拒绝。
- 一个真实 HC、Platform、ABA、ACP Agent 完成端到端 Session。
- 各组件职责测试映射到 `docs/roadmap/VERIFICATION.md`。

## 参考

- `docs/product/PRD.md`
- `docs/architecture/ARCHITECTURE.md`
- `docs/architecture/ABA.md`
- `docs/architecture/HC.md`

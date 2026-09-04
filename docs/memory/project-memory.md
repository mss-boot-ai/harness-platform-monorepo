# Harness Platform 项目记忆

- **状态**：Canonical project memory
- **最后结构化更新**：2026-09-04
- **用途**：供新会话、AI Agent 和开发者恢复当前项目事实；提交、CI、失败和未执行项以 `work-log.md`、远端 Git、PR 与 Actions 为准。

## 1. 项目身份与当前检查点

```text
repository:      mss-boot-ai/harness-platform-monorepo
main:            design and long-term memory baseline
development:     codex/bootstrap-harness-platform-foundation
draft PR:        #1
M1 continuation starting SHA:
                 98f8b3f76326b2dac2e2febe9bc527dbcb5199e0
latest verified remote implementation SHA:
                 6239ad3ba45ff82018d233e878c119a2f01443a1
latest paired CI: push 33894174661 / PR 33894180563
```

本项目用于构建一个可从 Web、微信小程序和后续原生客户端安全控制本地 ACP 编程 Agent 的平台。聊天中的完成声明不是证据；当前文件、远端提交、PR 和当前 SHA 的 CI 才是事实源。

## 2. 固定术语

- **Platform**：基于 mss-boot-admin 的后台控制面、身份、证书、密文中继、持久化和运营平台。
- **ABA**：`acp-brige-agent`，Rust 轻量本地 ACP 桥接与安全边界。
- **HC**：H 端客户端，包括 Web、微信小程序、App 和桌面端。
- **AWP**：ABA Wire Protocol，HC、Platform、ABA 之间独立版本化的外层协议。

`acp-brige-agent` 是当前正式拼写；未经 ADR 不得改名。

## 3. Platform 固定基线与正确引入方式

```text
upstream repository:  mss-boot-io/mss-boot-admin
release tag:          v1.3.7
annotated tag object: 41c6517950f7f5f642418f5d4a49386e9c200b15
peeled source commit: 77b53d41092741eac62fa6418c0bdbf87413c7cd
backend import:       github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7
frontend import:      @mss-boot-io/admin-web@1.3.7
Go version:           1.26.6
```

Platform 是官方 `mss v1.3.7` 生成的 **Thin Host/versioned import** 业务应用：

- `platform/` 只拥有组合入口、Harness 业务模块、显式 Migration、业务页面、配置和测试；
- 禁止复制 Foundation 的 `admin/`、`mss-boot/`、`templates/` 或完整 Admin Web 源码；
- 禁止本地 `replace`、临时 tarball、源码路径和浮动版本；
- 后端通过 `business.Module` 编译期注册；
- 前端通过 `platform/web/src/business/` 扩展；
- 后续升级使用目标版本的 `mss upgrade admin` 三方升级流程，并要求最终 no-op 计划。

此前 vendored/subtree-style 方案已被 ADR-0004 取代，不再是当前方案。

## 4. ACP 与 ABA 基线

```text
ACP wire compatibility: stable ACP v1
ABA implementation:     Rust
official SDK:            agentclientprotocol/rust-sdk 2.0.x
version lock:            exact Cargo.lock
ACP draft v2:            disabled by default
```

ABA 必须足够轻，只保留端点私钥、本地 Runtime/Workspace 策略、ACP 子进程、加解密、协议桥接、Sequence 和有界 Journal；默认只主动出站，不开放公网管理端口。

## 5. 系统边界

```text
HC <— TLS 1.3 + encrypted AWP —> Platform <— TLS 1.3 + encrypted AWP —> ABA
                                                                        │
                                                                        └— local ACP v1 —> Agent
```

- Platform 承担用户、租户、RBAC、Endpoint、凭据、轮换、密文存储、ACK/Replay、审计、通知和后台任务。
- ABA 执行本地不可外移安全职责。
- HC 是用户交互和端到端加密端点。
- ACP Gateway 与 mss-boot-admin 普通通知 WebSocket Hub 分离。
- Platform/HC 只能引用 `runtime_profile_id`、`workspace_id` 和已定义 Capability，不能下发任意命令、参数、环境变量、脚本或本地路径。

## 6. 身份与加密不变量

每个 ABA 和 HC 都是独立 Endpoint：

- 端点本地生成独立 E-SIG 与 E-KEM；
- Platform 不生成、接收或保存端点私钥；
- AEC/HEC、Token 和吊销状态按端点隔离；
- ABA Token、HC Token 和 Human Admin Session 不可互换；
- 受保护 API 使用发送者持有证明；
- WSS 使用短期单次 Ticket，并在 Upgrade 后完成端点私钥 Challenge。

默认是 **Opaque Mode**：

- SRK 由 ABA 为每个 Session/Generation 生成；
- Platform 只保存面向 HC 的 Key Package 密文；
- ACP JSON-RPC 原始 UTF-8 JSON 值端到端加密；
- AWP v1 Canonical AAD 固定为 148 字节；
- 首发 Suite 使用 SHA-256、P-256/ES256、RFC 7638、HPKE P-256/HKDF-SHA256/AES-256-GCM 和 Payload AES-256-GCM；
- TLS 1.3 仍然强制；
- Audit、日志、错误和 Trace 不得包含 Token、Ticket、私钥、SRK、ACP 明文、源码或 Tool 参数。

## 7. 可靠性不变量

- Platform 提供至少一次密文 Frame 交付，端点按 Message ID/Channel/Sequence 去重；
- 每方向独立 Sequence 和 Direction Key；
- Nonce 是 4 字节随机前缀加 8 字节大端 Sequence；
- 重连重放原始 Frame，不重新加密相同 Sequence；
- ACK 表示安全持久接收，不表示 ACP 副作用完成；
- ABA 在 `DISPATCH_STARTED` 后崩溃且无法证明结果时进入 `UNCERTAIN`，禁止自动重复执行。

## 8. 当前真实完成度

截至实现检查点 `bafa9da3b567c47ebf689c5d6036a124a149fd1e`，已由远端文件以及 push Run `33886744485`、PR Run `33886748998` 支持的范围是：

- Platform Thin Host/versioned import 基线与 CI 骨架；
- AWP v1 Proto 和 ABA Rust/AAD Foundation；
- MVP PRD、架构、实施与验证计划；
- Platform Endpoint、Enrollment、Credential、Session、Key Package、Ticket、Frame、ACK、Audit 和 Idempotency 的 Pure Domain；
- 显式前向 Migration、SQLite Store、事务、并发、冲突和关键唯一索引契约测试；
- Overview、Enrollment、Endpoint、Session、Delivery 管理后端 API；
- owner/tenant 精确隔离和安全 View Model；
- 管理写 API 的持久化脱敏 Audit 与 `Idempotency-Key` 行为；
- Key Package 只接受 Suite 0001，并按 Session、Issuer ABA、Recipient HC、Credential 和 Generation 状态失败关闭；
- Readiness 验证关键唯一索引的唯一性、列集合和列顺序；
- `harness:read`、`harness:operate`、`harness:approve`、`harness:revoke` 的授权 Migration、路由绑定和拒绝测试。
- Overview、Enrollment、Endpoint、Session、Delivery 五类 Admin Web 页面；
- Typed API、每次写操作的新 `Idempotency-Key`、中英文文案以及 Loading/Empty/Error/Forbidden/Ready 状态；
- 官方 Thin Host 本地部署、`mss doctor --strict`、`mss verify --all` 和 v1.3.7 no-op upgrade dry-run；
- 内置浏览器对五页面空态、正常态、审批/暂停/恢复操作、错误态和 403 禁止态的验收。

Platform M1 已达到当前 Accepted M1 范围的实现与验证检查点；详细证据见 `docs/roadmap/verification/2026-09-04-platform-m1.md`。这仍不等于 Platform、ABA、HC MVP 或真实 E2E 完成。

在此基础上，M2 已完成可独立验证的身份子切片：HC H5 workspace、本地不可导出 P-256 双密钥、IndexedDB `CryptoKey` 持久化、Suite 0001 三语言 JWK/ES256/DPoP 向量、nonce-bound DPoP primitives、ADR-0005 Human-bound 注册边界，以及真实 Browser Session → Challenge → Endpoint Registration。证据见 `docs/roadmap/verification/2026-09-05-hc-registration.md`。

## 9. 当前目标与缺口

下一阶段按 M2/M3 安全依赖推进，同时优先建立可在浏览器调试的 HC H5 workspace。HC H5 可以先实现共享类型、安全存储能力探测、状态机与 UI，但在 M2 的 DPoP、Credential、Ticket/WSS API 就绪前不得伪造“已联通”。当前主要工作是：

- 完成 Refresh Family rotation、数据库共享 DPoP Replay/Nonce 和 Endpoint 鉴权；
- 实现一次性 Ticket 与 WSS Challenge/READY/Fencing；
- 继续实现 ABA Enrollment/Connector、Session、HPKE/Relay 和确定性 ACP Test Agent；
- 形成 HC H5 → Platform → ABA → ACP Agent 的真实本地 E2E，再进入可靠性、吊销和部署收口。

## 10. 当前明确未完成或未验证

除非后续当前 SHA 证据更新，下列项目均按未完成/未验证处理：

- M2 的 Refresh Token Family rotation、持久 DPoP Replay/Nonce、HEC/Trust Manifest、Ticket API 与 WSS Gateway；
- ABA Secure Store、Enrollment、Connector、Process Supervisor、Journal 与 ACP Proxy；
- Test Agent、Compose 和 HC→Platform→ABA→ACP Agent E2E；
- 浏览器和微信真机；
- Opaque Canary；
- 负向安全、故障注入和容量；
- 生产数据库、多实例 Gateway、KMS/HSM 和外部安全评审。

CI 中 HC Job 因缺少 `hc/package.json` 而显式跳过时，只能记录为 Skip，不能记为 HC 通过。

## 11. Monorepo 所有权

```text
AGENT.md
README.md
docs/                 长期设计、决策、记忆、计划和证据
protocol/             Proto、Schema、向量和兼容契约
aba/                  Rust acp-brige-agent
platform/             mss-boot-admin v1.3.7 Thin Host 与 Harness 业务扩展
hc/                   共享 TypeScript 核心、Web 和小程序（当前尚未建立 Workspace）
deploy/               部署资产
scripts/              可重复检查脚本
```

## 12. Git 与状态纪律

- 功能代码只在 `codex/bootstrap-harness-platform-foundation` 开发，保持 Draft PR #1，不自动合并；
- 每个单一意图改动先最小检查，再 commit/push，再等待当前 SHA 的完整 CI；
- CI 运行期间不继续 push；
- 失败通过新修复提交解决，不 amend、rebase 或 force-push 已共享历史；
- 每个检查点在 `work-log.md` 记录完整 SHA、CI Run、实际命令、结果和未执行项；
- 必须区分已设计、已编写、已 push、已测试、已浏览器验证、已真机验证和已安全验证。

## 13. 更新规则

当名称、基线、Thin Host 方式、信任边界、协议、密码学、状态机、目录、当前阶段或关键未决项变化时，在同一检查点更新本文件。具体流水账只写 `work-log.md`。

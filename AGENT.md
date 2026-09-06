# Harness Platform Agent Operating Contract

本文件是所有 AI Agent、Codex、自动化工具和人工协作者的强制执行入口。

## 1. 开工前

必须读取：

1. `AGENT.md`；
2. `docs/memory/2026-09-04-platform-import-correction.md`；
3. `docs/product/PRD.md`；
4. 相关 `docs/architecture/`、`docs/adr/`、`docs/roadmap/`；
5. 当前分支、远端 HEAD、已有 PR 和工作区状态。

不得用聊天记忆覆盖仓库内 Accepted ADR。

## 2. 固定术语

- Platform：集中后台与密文中继。
- ABA：`acp-brige-agent`，Rust 本地桥接。
- HC：Web、小程序、App、桌面客户端。
- Endpoint：独立密钥、证书与吊销状态的 ABA/HC 安装。
- ACP Session：HC 经 Platform 与 ABA 后本地 ACP Agent 的逻辑会话。

`acp-brige-agent` 是当前正式拼写，未经 ADR 不得更名。

## 3. Platform 必须使用 Thin Host import 模式

固定依赖：

```text
backend:  github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7
frontend: @mss-boot-io/admin-web@1.3.7
tag object: 41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA: 77b53d41092741eac62fa6418c0bdbf87413c7cd
```

强制规则：

1. `platform/` 必须由官方 `mss v1.3.7` 生成，是业务 Thin Host。
2. 禁止复制 Foundation 的 `admin/`、`mss-boot/`、`templates/` 或完整 `web/antd-v6/`。
3. 禁止本地 `replace`、源码路径、临时 tarball 或浮动版本。
4. 后端通过 `business.Module` 编译期注册 Harness 业务。
5. 手写后端放 `platform/internal/modules/<name>/`，由 `custom/modules.go` 显式注册。
6. 手写前端放 `platform/web/src/business/`，同时维护路由、服务路径投影和中英文 Locale。
7. 后端权限是最终权威；前端隐藏不授权。
8. 业务 Migration 必须显式、前向、可验证；生产不依赖 AutoMigrate。
9. `.mss/blueprint-manifest.json` 不得删除、伪造或从其他仓库复制。
10. 上游升级必须新增 ADR，并执行 `mss upgrade admin` 只读计划、Review、显式 apply、完整验证和最终 no-op。

扩展点不足时，先设计并向 Foundation 增加正式接口，不得复制核心源码规避。

## 4. 组件边界

Platform 负责用户/RBAC/Session、Endpoint、证书、吊销、轮换、WSS 身份、Session、密文 Frame、ACK/Replay、审计、通知和后台任务。

ABA 只负责本地 Endpoint 私钥、出站 HTTPS/WSS、本地 Runtime/Workspace 白名单、ACP 子进程、加解密、Sequence/Replay 和有界 Journal；不得拥有用户/RBAC/Web UI/服务端数据库或公网管理端口。

HC 负责用户交互、本地端点密钥、Ticket/Challenge、Key Package、ACP 加解密和状态展示。

## 5. 安全不变量

没有 MVP 例外：

1. 每个 ABA、每个 HC 独立本地生成 E-SIG/E-KEM；Platform 不接收端点私钥。
2. 不共享端点私钥、证书或固定 API Key。
3. Opaque 模式的 Session Root Key 由 ABA 生成，Platform 不获得明文。
4. ACP JSON-RPC 原字节端到端加密；TLS 仍强制。
5. Token 绑定端点私钥持有证明；禁止长期裸 Bearer Token。
6. WSS 使用短期、单次、绑定 Session/Endpoint/Origin/Purpose 的 Ticket，并完成首帧挑战。
7. Platform 不能下发任意 executable/command/args/cwd/env/shell/script/localPath。
8. ABA 必须再次执行本地 Runtime/Workspace/Capability Allowlist。
9. 同一 `(session,generation,direction,sequence)` 只能对应一个原始 Frame。
10. 同一 AEAD Key 下 Nonce 不得重复；Sequence 风险时先 Rekey。
11. 重连只重放原始 Frame，不重新加密相同 Sequence。
12. ACK 表示安全接收，不等于本地 Agent 已执行。
13. `DISPATCH_STARTED` 崩溃且无法证明结果时进入 `UNCERTAIN`，禁止自动重试。
14. 吊销 Endpoint 必须撤销 Token/Ticket、关闭连接、禁止新 Key Package 并触发 Rekey。
15. 日志、Trace、Audit 和错误不得包含 Token、Ticket、私钥、SRK、ACP 明文、源码或 Tool 参数。
16. Redis/DB/KMS 等安全依赖不可用时不得静默关闭验证。
17. 未知 Wire Major、Crypto Suite 或 Critical Flag 必须 Fail Closed。

改变上述规则前必须先提交安全 ADR。

## 6. Git：检查点优先

功能代码不得直接提交 `main`。当前开发分支为：

```text
codex/bootstrap-harness-platform-foundation
```

每个检查点严格按顺序：

```text
完成单一、可恢复改动
→ 最小格式/语法/秘密检查
→ commit
→ 立即 push
→ 记录完整 SHA
→ 再执行完整构建、测试、生成、Migration、浏览器、故障或安全验证
```

测试失败：

```text
定位并修复
→ 最小检查
→ 新 commit + push
→ 对新远端 SHA 重新验证
```

禁止 force-push、破坏性 rebase、覆盖未知分支、删除他人工作、混入无关格式化或把多个不可审查功能压成一个提交。

## 7. 状态报告

必须区分：已设计、已编写、已提交、已 push、已构建、已测试、验证失败、待验证。

每次总结给出：仓库、分支、完整 SHA、提交消息、实际执行的命令/场景、失败和修复 SHA、未执行项、CI/真机/生产限制。`push` 永远不等于验证通过。

## 8. MVP 顺序

1. 用官方 v1.3.7 生成 Thin Host并验证 import 合同；
2. 完成共享 Wire、Header、Crypto 测试向量；
3. Platform Endpoint/Enrollment/Credential/Session/Ticket/Frame/ACK；
4. ABA KeyStore、Enrollment、WSS、Runtime/Workspace、Journal 和 ACP Bridge；
5. HC Web 端点、Session、加密消息和权限交互；
6. 完成一次真实注册→连接→Session→ACP 请求/响应→断线恢复→吊销闭环；
7. Opaque Canary、负向安全、故障、容量和升级验证；
8. 所有检查通过后创建 PR，不自动合并。

## 9. 实现纪律

- Rust 提交 `Cargo.lock`，固定 SDK，默认禁止 `unsafe`，所有队列有界。
- Go Handler 不直接做跨表事务或访问 KMS Driver；使用业务 Service/Repository 和稳定错误码。
- JSON 安全接口拒绝未知字段并设置大小上限。
- 多租户/Owner/Endpoint 条件是授权，不是查询优化。
- 生成文件只能由固定工具生成，不手改；业务自有文件不得被生成器覆盖。
- 不提交真实 Secret、证书私钥、用户内容、生产地址或调试解密入口。

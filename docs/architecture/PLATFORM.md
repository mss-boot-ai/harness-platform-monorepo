# Platform 架构：mss-boot-admin v1.3.7 Thin Host

- **状态**：Accepted
- **修订日期**：2026-09-04
- **权威 ADR**：ADR-0004

## 1. 定位

Platform 是 Harness Platform 的集中控制面、身份与证书中心、ACP 密文中继、可靠性存储、审计和运营后台。它不是 mss-boot-admin 的源码分叉，而是一个正式 Thin Host 业务应用。

```text
mss-boot-admin/admin@v1.3.7 ── Go import ──> Platform backend
@mss-boot-io/admin-web@1.3.7 ─ npm import ──> Platform web
                                               │
                                               └─ Harness business modules only
```

## 2. 依赖基线

```text
backend module: github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7
frontend pkg:   @mss-boot-io/admin-web@1.3.7
source commit:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:             1.26.6
Node:           24.x
package manager: pnpm (由生成项目锁定)
```

不得使用源码复制、本地 replace、浮动版本或第二套前端 SPA。

## 3. 组合入口

后端入口调用 Admin 的应用组合 API：

```go
adminapp.ExecuteContext(
    context.Background(),
    adminapp.WithBusinessModules(modules.Modules()...),
)
```

`modules.Modules()` 以确定顺序合并生成模块和手写模块。业务模块实现 `business.Module`，在 `Register` 中一次性声明：

- Descriptor；
- 前向 Migration；
- Readiness；
- 受保护路由；
- Presentation Capability。

注册失败必须事务性回滚，重复模块、路由或权限必须失败关闭。

## 4. Harness MVP 业务模块

MVP 使用一个手写聚合模块 `harness`，内部再按领域分包，避免过早拆成多个独立服务：

```text
platform/internal/modules/harness/
├── module.go
├── descriptor.go
├── migration.go
├── readiness.go
├── auth.go
├── model/
├── service/
│   ├── enrollment.go
│   ├── endpoint.go
│   ├── session.go
│   ├── ticket.go
│   └── relay.go
├── api/
│   ├── enrollment.go
│   ├── endpoint.go
│   ├── session.go
│   ├── ticket.go
│   └── health.go
├── gateway/
└── jobs/
```

该模块可在后续按负载拆分 Gateway Worker，但 P0 先保持单部署单元和清晰接口。

## 5. MVP 数据模型

MVP 必须具备以下持久化对象：

- `harness_endpoints`：ABA/HC 独立端点、公钥、JKT、状态和版本；
- `harness_enrollments`：短期配对、哈希 Code、审批和消费状态；
- `harness_endpoint_credentials`：AEC/HEC 元数据、Scope、有效期和吊销状态；
- `harness_sessions`：ABA、Runtime、Workspace、Owner、状态和 Key Generation；
- `harness_session_participants`：Session 与 HC 的关系；
- `harness_ws_tickets`：短期单次 Ticket 的哈希与绑定上下文；
- `harness_frames`：Opaque Frame、方向、Sequence、Ciphertext、Signature 和状态；
- `harness_ack_cursors`：每通道最高连续 ACK；
- `harness_audit_events`：不含秘密的安全审计；
- `harness_idempotency_records`：管理 API 幂等结果。

Session Key 明文、Endpoint 私钥、Refresh Token 明文和 ACP JSON-RPC 明文不得进入 Platform 数据库。

## 6. API 边界

MVP 后端统一挂载在 Admin 已保护的 `/api` 组下：

```text
/api/harness/v1/health
/api/harness/v1/enrollments
/api/harness/v1/endpoints
/api/harness/v1/sessions
/api/harness/v1/ws/tickets
/api/harness/v1/relay/frames
/api/harness/v1/relay/acks
```

管理操作由 Human Principal + 后端权限保护；ABA/HC 数据面后续增加 Endpoint Principal 与 DPoP。敏感 JSON 使用拒绝未知字段、大小限制、幂等键和稳定错误码。

## 7. 前端边界

Admin Web 是完整前端发行单元。Harness 页面只放在：

```text
platform/web/src/business/
├── routes.config.ts
├── route-registrations.ts
├── locales/zh-CN.ts
├── locales/en-US.ts
├── services/harness.ts
└── pages/
    ├── Overview/
    ├── Endpoints/
    ├── Enrollments/
    └── Sessions/
```

MVP 页面覆盖加载、空、错误和拒绝状态。前端只表达体验层权限；所有读取和写入由后端重新鉴权。

## 8. ACP Gateway

现有普通通知 Hub 不承担 ACP 数据面。MVP 先在 Harness 模块内提供独立 Gateway 接口和内存连接目录：

- Binary Frame；
- Endpoint 连接身份；
- 单连接写队列上限；
- Message/Sequence 去重；
- ACK/Replay；
- 慢消费者背压；
- 连接 Fencing；
- 安全关闭码。

达到多实例需求后，用 Redis/NATS/PostgreSQL 通知等适配器实现 Connection Ownership 和跨节点转发；领域接口不绑定具体总线。

## 9. 迁移与就绪

业务模块使用显式、可前向执行的 Migration，并在挂载路由前验证：

- Migration Ledger 已记录；
- 必需表和唯一约束存在；
- 权限与默认管理员策略已写入；
- 敏感字段没有错误的明文列；
- 依赖不可用时不挂载业务路由。

生产发布先运行 `migrate`，成功后才启动 `server`。

## 10. 上游升级

```text
安装目标版本 mss 工具
→ mss upgrade status
→ mss upgrade admin <version>（只读计划）
→ Review 三方差异和冲突
→ 显式 --apply --yes
→ mss doctor --strict
→ mss verify --all
→ 再次只读计划，必须 no-op
```

升级只更新受管组合层；`internal/modules/harness/`、`web/src/business/`、`.mss/features/` 和未知业务文件必须原字节保留。

## 11. MVP 完成门槛

- Thin Host import 结构和锁完整；
- 后端 Harness Module 可编译、迁移、就绪并挂载路由；
- Endpoint/Enrollment/Session/Ticket/Frame/ACK 的主流程和负向测试通过；
- Web 页面可以查看端点、配对与 Session；
- ABA 与 Platform 形成一次真实注册、连接、Session 和加密消息闭环；
- Opaque Canary 在 Platform DB/日志/审计中零命中；
- 分支所有检查点已 push，CI 通过并创建 PR。

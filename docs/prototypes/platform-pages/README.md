# Platform 端完整页面

- 版本：v0.1
- 状态：Visual baseline（含未实现页面，逐页状态见下表）
- 保真度：F3（结构稿）
- 界面：Platform 控制面 18 个页面：Harness 业务模块与 mss-boot-admin v1.3.7 Foundation 模块。
- 使用者：管理员 + 运维
- 关联文档：[`../../roadmap/verification/2026-09-04-platform-m1.md`](../../roadmap/verification/2026-09-04-platform-m1.md)、[`../../architecture/PLATFORM.md`](../../architecture/PLATFORM.md)、[`../../architecture/PROTOCOL.md`](../../architecture/PROTOCOL.md)、[`../../architecture/SECURITY.md`](../../architecture/SECURITY.md)、AGENT.md §3、D-012/D-015/D-027/D-028/D-030/D-031/D-037
- 关联代码：`platform/web/src/business/pages`、`platform/web/src/business/{routes.config.ts,route-registrations.ts,harness/*}`、`platform/internal/harness/{gateway,store,domain,enrollment,registration}`
- 已实现部分：五个 Harness Admin 页面（Overview / Enrollments / Endpoints / Sessions / Delivery）、Typed API、写操作 `Idempotency-Key`、中英文 Locale、Loading/Empty/Error/Forbidden/Ready 五态；对应后端管理 API；`harness:read|operate|approve|revoke` 的授权 Migration、路由绑定与拒绝测试；五个页面已浏览器验收。Foundation 用户/角色/菜单/审计/通知来自 v1.3.7。
- 未实现部分：证书与 Trust Manifest 页面（P10）、轮换任务页面（P11）的完整能力；Endpoint 详情与 Session 详情的轮换/强制 Rekey 操作；跨实例 Gateway 路由与全局 Connection Directory（P-001）；生产 Frame Store（P-003）；生产 KMS/HSM Signer Adapter；活跃连接 Fencing/Kick。
- 非目标：不复制 Foundation 源码；不引入第二套导航或路由体系；不定义新权限、新字段或新 API；不提供任何会话内容查看入口。
- 首次建立：2026-09-15
- 最后更新：2026-09-15

## 1. 页面清单与实现状态

| # | 页面 | 路由 | 实现 |
| --- | --- | --- | --- |
| P01 | 登录（Admin） | `/admin/login` | 已实现（Foundation） |
| P02 | Overview 总览 | `/admin/harness/overview` | 已实现 |
| P03 | Enrollments 列表 | `/admin/harness/enrollments` | 已实现 |
| P04 | Enrollment 审批 | `…/enrollments/:id` | 已实现 |
| P05 | Endpoints 列表 | `/admin/harness/endpoints` | 已实现 |
| P06 | Endpoint 详情 | `…/endpoints/:id` | 部分实现（轮换/时间线未完成） |
| P07 | Sessions 列表 | `/admin/harness/sessions` | 已实现 |
| P08 | Session 详情 | `…/sessions/:id` | 部分实现（Rekey 未完成） |
| P09 | Delivery 投递 | `/admin/harness/delivery` | 已实现 |
| P10 | 证书与 Trust Manifest | `/admin/harness/trust` | 未实现 |
| P11 | 轮换任务 | `/admin/harness/rotation` | 未实现 |
| P12 | 审计日志 | `/admin/audit` | 已实现 |
| P13 | 通知 | `/admin/notifications` | 已实现 |
| P14 | 用户管理 | `/admin/users` | Foundation v1.3.7 |
| P15 | 角色与权限 | `/admin/roles` | Foundation v1.3.7 |
| P16 | 菜单与路由 | `/admin/menus` | Foundation v1.3.7 |
| P17 | 系统健康 | `/admin/status` | 部分实现 |
| P18 | 403 / 错误页 | — | 已实现 |

「已实现」指该界面在前端与后端存在并有验证记录；「部分实现」指只读部分存在、详情或操作未完成；「未实现」为纯设计。此列不改变 `docs/roadmap/DELIVERY.md` 与验证报告的结论。

## 2. 关键页面约束

| 页面 | 约束 |
| --- | --- |
| P02 Overview | 只放计数与状态，不放会话摘要、Prompt 或工具参数 |
| P03/P04 Enrollments | 无「全部批准」；批准需 `harness:approve` + 二次认证；设备码明文不落库 |
| P05/P06 Endpoints | 吊销为独立权限与独立入口；不显示证书完整值、Token 或密钥材料 |
| P07/P08 Sessions | 不持有 SRK、不展示 ACP 明文与本地路径；无内容检索 |
| P09 Delivery | Payload 为密文；ACK ≠ 已执行；无「重试执行」 |
| P10 Trust | Root 回滚必须被检测；新 Root 未交叉签名时拒绝 |
| P11 Rotation | 四层资产独立周期；固定扫描任务，不为每资产建 Cron |
| P12 Audit | 脱敏；失败记录同样保留 |
| P13 Notifications | 提醒非通道；打开后回到页面查权威状态 |
| P15 Roles | 后端权限为最终权威；前端隐藏不授权 |
| P17 Status | 安全依赖不可用时 Fail Closed，不静默降级 |
| P18 Errors | 稳定错误码 + 下一步动作；无堆栈、SQL、内部路径 |

## 3. 安全相关表达

- **权限兜底**：无 `harness:revoke` 时按钮不渲染，但直连 API 仍返回 403。
- **Opaque 语义**：Overview 与 Sessions 页显式声明 Platform 不持有端点私钥与 SRK。
- **无解密入口**：全站不提供「查看会话内容」入口；Managed Mode 的未来入口需独立页面与审计（D-016）。
- **审计**：写操作带 `Idempotency-Key` 并记录脱敏审计。
- **日志禁止项**：Token、Ticket、私钥、SRK、ACP 明文、源码、Tool 参数一律不得出现在日志、Trace、错误页。

## 4. 与文档的一致性

| 文档要求 | 页面落点 |
| --- | --- |
| AGENT.md §3.7 前端隐藏不授权 | P05、P15、P18 |
| AGENT.md §3.5/§3.6 手写前端位置与 Locale | P16 |
| AGENT.md §5.16 Fail Closed | P17 |
| D-012 每端独立密钥与吊销 | P05、P06 |
| D-015 默认 Opaque | P02、P07 |
| D-027 Gateway 与通知 Hub 分离 | P09、P13 |
| D-028 至少一次交付，ACK ≠ 执行 | P08、P09 |
| D-030/D-031 固定任务与分层轮换 | P11 |
| D-037 Admin Cookie 边界 | P01 |

## 5. 待确认问题

1. Delivery 页的「暂停」是否需要二次确认？（会直接影响用户设备上的投递）
2. 多实例部署后 Overview 与 P17 的计数口径是全局还是当前实例？取决于 P-001。
3. 证书与轮换是否需要独立顶级菜单，还是并入 Endpoint 详情？（影响 P16 菜单注册）
4. 运维是否需要「无内容的连接健康」独立视图，还是并入 Overview？
5. P08 的「强制 Rekey」触发条件与旧代退役时序待 PROTOCOL 侧确认。

## 6. 版本记录

| 版本 | 日期 | 变更 | 触发文档 / 决策 |
| --- | --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：覆盖 18 个页面（Harness + Foundation），取代 `platform-admin-v1/` | `docs/roadmap/verification/2026-09-04-platform-m1.md`、AGENT.md §3 |

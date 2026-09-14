# Platform 管理后台五页面

- 版本：v0.1
- 状态：Visual baseline
- 界面：基于 mss-boot-admin v1.3.7 Thin Host 的 Harness 业务后台五个页面，以及五态与权限矩阵。
- 使用者：管理员 + 运维
- 关联文档：[`../../roadmap/verification/2026-09-04-platform-m1.md`](../../roadmap/verification/2026-09-04-platform-m1.md)、[`../../architecture/PLATFORM.md`](../../architecture/PLATFORM.md)、[`../../product/PRD.md`](../../product/PRD.md)、AGENT.md §3
- 关联代码：`platform/web/src/business/pages/{Overview,Enrollments,Endpoints,Sessions,Delivery}/index.tsx`、`platform/web/src/business/{routes.config.ts,harness/api.ts,harness/contract.ts}`
- 已实现部分：五个 Admin Web 页面、Typed API、每次写操作新建 `Idempotency-Key`、中英文 Locale、Loading/Empty/Error/Forbidden/Ready 状态；Overview / Enrollment / Endpoint / Session / Delivery 管理后端 API；`harness:read|operate|approve|revoke` 的授权 Migration、路由绑定与拒绝测试；五个页面已在浏览器做过空态、正常态、审批/暂停/恢复、错误态与 403 验收。
- 未实现部分：跨实例 Gateway 路由与全局 Connection Directory（P-001）；生产 Frame Store（P-003）；生产 KMS/HSM Signer Adapter；活跃连接 Fencing/Kick；保留清理与轮换任务的完整后台任务编排。
- 非目标：不复制 Foundation 的 Admin、Framework、模板或完整前端源码；不引入第二套导航或路由体系；不在本原型中定义新权限、新字段或新 API。
- 首次建立：2026-09-15
- 最后更新：2026-09-15

## 1. 界面目标

后台是 Platform 运营与安全响应的操作面。它必须让管理员看得清「有哪些设备、哪些会话、投递是否健康」，同时**看不到任何业务明文**。原型要确认：

- 五个页面的职责边界是否互不重叠；
- 密文投递（Delivery）层的操作是否被限制在投递语义内；
- 五态与权限兜底是否覆盖完整；
- 是否存在任何暗示「Platform 可以读取内容」的措辞或控件。

## 2. 布局与信息层级

```text
左侧导航：HARNESS 五个页面（Overview / Enrollments / Endpoints / Sessions / Delivery）
          下方保留 FOUNDATION 区（用户/角色/菜单、审计/通知/任务），视觉上从属
页面内： 指标卡（仅计数与状态） -> 数据表 -> 写操作按钮（按权限渲染）
```

- Overview 只放**计数与状态**，不放会话摘要或内容预览。
- 页面之间不建立新的导航层级；不新增与 Foundation 并行的路由体系。
- Delivery 页的操作仅限投递层（暂停/恢复），不提供「重试执行」。

## 3. 页面职责与列定义

| 页面 | 权限落点 | 关键列 | 明确不显示 |
| --- | --- | --- | --- |
| Overview | `harness:read` | Endpoint / Session / Delivery 计数、Trust Manifest revision | 会话内容摘要、Prompt、工具参数 |
| Enrollments | `harness:approve` | Endpoint、请求时间、状态、批准/拒绝 | 设备码明文、私钥、指纹完整值 |
| Endpoints | `harness:revoke` | Endpoint、类型、证书序列（截断）、状态 | 证书完整值、Token、密钥材料 |
| Sessions | `harness:read` | Session ID、ABA/HC 绑定、Key Generation、状态 | SRK、ACP 明文、本地路径 |
| Delivery | `harness:operate` | Channel、方向、已持久化、已 ACK、重放/积压 | Frame 明文、Tool 参数 |

## 4. 五态

| 状态 | 表达要点 |
| --- | --- |
| Ready | 正常数据表 + 分页；写操作按权限出现 |
| Empty | 明确下一步动作（如「先在本地启动 ABA 并完成设备授权」） |
| Loading | 骨架占位，列头先出现；**不**把未加载的计数显示为 0 |
| Error | 稳定错误码 + 下一步动作 + 请求 ID；无堆栈、SQL、内部路径 |
| 403 Forbidden | 说明缺少哪个权限并给出求助路径；作为正常兜底而非异常 |

## 5. 安全相关表达

- **密文语义**：Delivery 页显式标注 Payload 为密文，并写明「ACK 表示安全接收，不等于本地 Agent 已执行」。
- **权限兜底**：写明「前端隐藏不授权，但后端权限是最终权威」——无 `harness:revoke` 时按钮不渲染，但直连 API 仍会被拒绝。
- **审计**：写明写操作带 `Idempotency-Key` 并记录脱敏审计。
- **无 Managed Mode 入口**：本版后台不提供任何「解密查看内容」的入口；Managed Mode 属未来独立页面并需单独审计（D-016、`HC.md` §22）。
- **吊销入口分离**：吊销与批准不混在同一按钮组，避免误触。

## 6. 与文档的一致性

| 文档要求 | 原型落点 |
| --- | --- |
| AGENT.md §3.7 后端权限是最终权威，前端隐藏不授权 | 原型 §1 注释、§3 五态 403 |
| D-027 ACP Gateway 与通知 Hub 分离 | 导航中 Delivery 独立于 Foundation 通知 |
| D-028 至少一次交付 + 端点去重；ACK 不等于执行 | 原型 §2 Delivery 页注释 |
| D-015/D-016 默认 Opaque，Managed Mode 不是 E2EE | 原型 §5；后台无解密入口 |
| `PLATFORM.md` 数据模型与 API | 各页列定义 |

## 7. 待确认问题

1. Delivery 页的「暂停」是否需要二次确认？会直接影响在用户设备上等待投递的会话。
2. 多实例部署后，Overview 的计数是全局还是当前实例？取决于 P-001 的 Connection Directory 方案。
3. 是否需要给运维提供只读的「连接健康」视图（不含任何内容），还是并入 Overview？
4. 轮换与保留清理的运维入口应放在 Foundation 任务页还是 HARNESS 侧？待 D-030/D-031 实现时确认。

## 8. 版本记录

| 版本 | 日期 | 变更 | 触发文档 / 决策 |
| --- | --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：导航结构、五页面列定义、五态、权限矩阵 | `docs/roadmap/verification/2026-09-04-platform-m1.md`、AGENT.md §3 |

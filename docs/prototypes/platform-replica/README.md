# Platform 管理后台还原稿

- 版本：v0.2
- 状态：Visual baseline（含未实现页面，逐页状态见下）
- 保真度：F4（还原稿，页面主体）／顶栏与布局为重建
- 界面：Platform Harness 业务五个页面的静态还原，加 403 与空/加载/错误态，共 10 屏。
- 使用者：管理员 + 运维
- 关联文档：[`../../roadmap/verification/2026-09-04-platform-m1.md`](../../roadmap/verification/2026-09-04-platform-m1.md)、[`../../architecture/PLATFORM.md`](../../architecture/PLATFORM.md)、AGENT.md §3
- 关联代码（还原来源）：`platform/internal/modules/harness/module.go`、`platform/internal/modules/harness/authorization_migration.go`、`platform/web/src/business/locales/zh-CN.ts`、`platform/web/src/business/pages/{Overview,Enrollments,Endpoints,Sessions,Delivery}/index.tsx`、`platform/web/src/business/harness/{HarnessPage,HarnessAsyncContent,format}.tsx`、`platform/web/src/business/routes.config.ts`、`platform/web/src/generated/routes.ts`、`platform/web/package.json`
- 已实现部分：五个 Harness 页面及其列定义、状态色、操作按钮、弹窗文案均来自已实现代码；菜单条目与 4 项权限来自后端 Migration；M1 验证报告覆盖五页的浏览器验收、403 禁止态、错误态与空态。
- 未实现部分：本稿不含证书与 Trust Manifest 页面、轮换任务页面（这两页在代码中不存在）；不覆盖真实登录跳转、CSRF、`Idempotency-Key` 生成、i18n 切换、表头排序与列宽拖拽、真实响应式折叠行为。
- 非目标：不是可运行应用；不复制 Foundation 源码；不新增字段、权限或 API。
- 首次建立：2026-09-15
- 最后更新：2026-09-15

## 1. 还原方式与可信度

| 项 | 做法 | 可信度 |
| --- | --- | --- |
| 页面标题 / 描述 | 取自 `locales/zh-CN.ts` 的 message id，未改写 | 高 |
| 表格列与顺序 | 逐列对齐各 `index.tsx` 的 `columns` 数组 | 高 |
| `scroll.x` / `pagination` | 按源码原值（960 / 1120 / 1200，均为 `false`） | 高 |
| 状态标签配色 | 取自 `harness/format.tsx` 的 `statusColors` 映射表 | 高（映射）；色值为 antd 6 默认调色板重建 |
| 操作按钮与权限门控 | 按源码条件复现（`canApprove` / `canRevoke` / `canOperate`） | 高 |
| 弹窗与 Popconfirm 文案 | 逐字取自源码 | 高 |
| Tabs | 五项取自 `HarnessPage` 的 `navigation` 常量 | 高 |
| **菜单条目与权限项** | **取自后端 `module.go` 的 `business.Menu` 与 `authorization_migration.go` 的种子** | **高：源码级事实** |
| 错误态文案 | `session was not found` 取自 M1 验收报告的实测结果 | 高 |
| 顶栏 / 布局 / 主题 token | **重建**：实包为 `@mss-boot-io/admin-web 1.3.7`，不在仓库且本地未安装 | **低：非逐像素** |
| 侧栏图标 path | **近似**：`@ant-design/icons 6.3.2` 未安装，按 `RobotOutlined` 语义自绘 | 低：语义对齐，path 非原始 |
| 空 / 错误 / 加载 / 403 组件外观 | 来自该包的 `PageEmpty` 等导出，外观重建 | 低：组件外观重建，分支逻辑源码级 |

## 2. 屏清单

| # | 屏 | 源码位置 | 可信度 |
| --- | --- | --- | --- |
| P1 | Harness 平台概览 | `pages/Overview/index.tsx` | 主体源码级；外壳重建 |
| P2 | 端点注册审批 | `pages/Enrollments/index.tsx` | 同上 |
| P2b | 验证用户码弹窗 | 同上 | 文案源码级；组件外观重建 |
| P3 | 端点管理 + 吊销确认 | `pages/Endpoints/index.tsx` | 主体源码级；外壳重建 |
| P4 | ACP 会话 + 关闭确认 | `pages/Sessions/index.tsx` | 同上 |
| P5 | 密文投递状态 | `pages/Delivery/index.tsx` | 同上 |
| P6 | 403 禁止态 | `HarnessPage.tsx` → `PageForbidden` | 文案源码级；组件外观重建 |
| P7a | 空态 | `HarnessAsyncContent.tsx` → `PageEmpty` | 分支逻辑源码级；外观重建 |
| P7b | 加载态 | `HarnessAsyncContent.tsx` → `PageLoading` | 同上 |
| P7c | 错误态 | `HarnessAsyncContent.tsx` → `PageError` + M1 实测文案 | 同上 |

## 3. 源码级事实（不装依赖也能确认的部分）

### 3.1 菜单条目

来自 `platform/internal/modules/harness/module.go` 的 `business.Menu`：

```text
DisplayName:   Harness 平台
DisplayNameEn: Harness Platform
Icon:          RobotOutlined
Order:         45
Path:          /harness
```

来自 `authorization_migration.go` 写入的菜单记录（`MenuAccessType`，可见）：

```text
name "Harness Platform" · path /harness · method GET · permission harness:read · sort 45 · HideInMenu false
```

**同级还有 `Order ≠ 45` 的 Foundation 菜单**，由 `@mss-boot-io/admin-web` 运行时注入，名称不可读。`platform/web/src/generated/routes.ts` 是空数组，本地也无法从该文件推断。

### 3.2 权限项（`permission` 组件记录，全部 `hidden: true`）

| 权限码 | 显示名 | 组件路径 |
| --- | --- | --- |
| `harness:read` | 查看 Harness 状态 | `/harness/permissions/read` |
| `harness:operate` | 操作 Harness 会话 | `/harness/permissions/operate` |
| `harness:approve` | 审批 Harness 注册 | `/harness/permissions/approve` |
| `harness:revoke` | 暂停或吊销 Harness 端点 | `/harness/permissions/revoke` |

英文侧显示名（`module.go` 的 `business.Permission.DisplayName`）分别为 Read Harness state / Operate Harness sessions / Approve Harness enrollments / Revoke Harness endpoints。

### 3.3 前端路由与页内导航

- `routes.config.ts`：`/harness` 重定向到 `/harness/overview`；`/harness/overview` 是**唯一** `hideInMenu` 未设置的路由，其余四页全部 `hideInMenu: true`。
- 因此侧栏只有一个可见条目，五页之间靠 `HarnessPage` 内的 **Tabs** 切换（概览 / 注册审批 / 端点 / 会话 / 投递）。
- 把五个页面画成五个侧栏菜单项是**错的**。

### 3.4 实测到的错误态文案

M1 验收报告记录：查询不存在的 Session 时显示错误态与稳定错误串 `session was not found`（英文，非中文包装文案）。

## 4. 关键还原点（易做错的地方）

1. **侧栏只有一个 Harness 条目**，五页靠页内 Tabs 切换（见 §3.3）。
2. **概览是 5 个统计卡不是 4 个**：待处理注册、活跃端点、活跃会话、未确认密文帧、**冲突密文帧**。栅格 `xs=24 sm=12 xl=8`。
3. **审批弹窗的确定按钮文案随决策切换**（批准 / 拒绝），共用同一个「验证用户码」弹窗，用户码为空时禁用。
4. **端点管理的操作按钮按状态三态变化**：ACTIVE → 暂停+吊销；SUSPENDED → 恢复+吊销；REVOKED → `—`。
5. **会话页的终止态集合**是 `ABA_REVOKED` / `CLOSED` / `FAILED`，这些行没有「关闭」但仍有「查看投递」。
6. **投递页未输入 Session ID 时不发请求**，只显示一行次要文字；输入框提示是「输入 32 字符 Session ID」。
7. **`CompactID` 自带复制按钮且宽度上限 180px**，长 ID 显示为省略号 + 复制图标。
8. **四态判定顺序**：loading → forbidden → error → empty → ready，`forbidden` 优先于 `error`。
9. **概览页没有任何内容级信息**：只有计数，没有 Prompt、标题或工具参数。

## 5. 还原偏差

1. **顶栏与布局非逐像素**（见 §1 末三行）。要消除差距需要在该目录安装依赖后重新对照实包。
2. **Harness 菜单条目已源码级确认，同级 Foundation 菜单未还原**：同级 `Order ≠ 45` 的菜单不可读，用虚线框说明，未虚构。
3. **侧栏图标 path 是近似**：`@ant-design/icons` 未安装，按 `RobotOutlined` 语义自绘。
4. **示例数据为占位值**：端点名、ID、时间、Runtime / Workspace、字节数均按源码字段格式编造。
5. **标签色值为 antd 6 默认调色板重建**，不是从实包 CSS 读取。
6. **Popconfirm / Modal 用叠加层复现**，不响应点击。
7. **未覆盖**：真实登录跳转、请求头与 CSRF、`Idempotency-Key` 生成、`message.useMessage()` 全局提示动效、en-US 文案、表头排序与列宽拖拽。

## 6. 待确认问题

1. 侧栏宽度与是否深色，需安装依赖后从实包确认；当前为 `208px` 浅色重建。
2. 「冲突密文帧」在概览里的业务含义与处置入口是否需要补充说明页面？
3. 投递页的 Session ID 输入是否需要历史下拉或从会话页带参跳转的可见提示？
4. 4 项权限的组件记录（`/harness/permissions/*`）是否需要在 Foundation 菜单/权限页可见，还是保持隐藏？

## 7. 版本记录

| 版本 | 日期 | 变更 | 触发文档 / 决策 |
| --- | --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：按真实 locales 与页面组件还原 9 屏；外壳标注为重建 | `platform/web/src/business` 当前源码 |
| v0.2 | 2026-09-15 | 从后端 `module.go` 与 `authorization_migration.go` 回填菜单条目与 4 项权限的真实数据；新增 P7c 错误态（文案取自 M1 实测）；更正屏数计数；侧栏增加源码级事实说明 | M1 验收报告、`platform/internal/modules/harness` |

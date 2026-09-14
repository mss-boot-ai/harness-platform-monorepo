# Harness Platform 原型图与说明

- 状态：Draft（目录与规范本身待评审）
- 文档版本：v0.2
- 建立日期：2026-09-15
- 分支：`design/prototype-gallery`
- 用途：用可离线打开的低保真原型表达 Platform / ABA / HC 的界面与关键交互，并为每个原型配一份可追溯的说明文档。

## 1. 这里放什么、不放什么

**放**：

- 界面布局与信息层级（低保真线框）
- 关键交互流程与状态流转
- 权限卡、`UNCERTAIN`、Backpressure 等安全相关 UI 的表达方式
- 与 `docs/architecture/`、`docs/product/`、`docs/remote/` 的对照关系

**不放**：

- 生产实现代码（在 `hc/`、`platform/`、`aba/`）
- 真实密钥、Token、Ticket、生产地址或用户数据
- 对未实现能力的「已完成」声明

原型是**表达手段**，不是验收证据。任何「已实现 / 已验证」结论只能来自代码、提交、CI 和验证报告，引用规则见 [`../README.md`](../README.md) §5。

## 2. 状态与版本

### 2.1 状态标签

| 标签 | 含义 |
| --- | --- |
| `Design only` | 仅设计，代码中无对应实现 |
| `Partial impl` | 代码中有部分实现，原型包含未实现部分 |
| `Visual baseline` | 对应界面已实现并验收，原型作为视觉/回归参照 |
| `Superseded` | 已被新原型取代，保留历史 |

### 2.2 版本号

每个原型独立编号 `vMAJOR.MINOR`，首版一律 `v0.1`。

- **版本号只标识设计稿迭代**，不是 Platform / ABA / HC 的产品版本，不代表实现进度，也不得被引用为「已发布」「已上线」的依据。
- 版本号必须在三处一致：原型 `README.md` 头部、原型 `index.html` 徽标、下方清单表。
- 信息架构或关键流程变化递增 MAJOR，同结构微调递增 MINOR，具体规则见 [`CONVENTIONS.md`](CONVENTIONS.md) §4。

## 3. 原型清单

| 目录 | 界面 | 版本 | 状态 | 关联文档 | 关联代码 |
| --- | --- | --- | --- | --- | --- |
| [`hc-remote-console/`](hc-remote-console/) | HC 远程会话主界面（移动 + 桌面） | v0.1 | Partial impl | [`../remote/README.md`](../remote/README.md)、[`../product/HC-CHAT-EXPERIENCE.md`](../product/HC-CHAT-EXPERIENCE.md)、[`../architecture/HC.md`](../architecture/HC.md) | `hc/web/src/{chat,remote}` |
| [`aba-enrollment/`](aba-enrollment/) | ABA 设备授权（Enrollment） | v0.1 | Partial impl | [`../architecture/ARCHITECTURE.md`](../architecture/ARCHITECTURE.md) §6.1、ADR-0005 | `platform/internal/harness/enrollment`、`aba/src/identity` |
| [`platform-admin/`](platform-admin/) | Platform 管理后台五页面 | v0.1 | Visual baseline | [`../roadmap/verification/2026-09-04-platform-m1.md`](../roadmap/verification/2026-09-04-platform-m1.md)、[`../architecture/PLATFORM.md`](../architecture/PLATFORM.md) | `platform/web/src/business/pages` |
| [`TEMPLATE/`](TEMPLATE/) | 新增原型骨架与样式源 | v0.1 | 模板 | [`CONVENTIONS.md`](CONVENTIONS.md) | 无 |

## 4. 建议阅读顺序

1. 本文件
2. [`CONVENTIONS.md`](CONVENTIONS.md)：制作规范、版本规则与禁止事项
3. 按需打开某个原型的 `README.md`（先看状态、版本与「已实现 / 未实现」），再打开同目录 `index.html`

## 5. 如何打开

原型是自包含单文件 HTML：双击用浏览器打开即可，不需要构建、开发服务器或网络。文件内不引入任何外部脚本、字体、图片或 CDN。

`index.html` 顶部会同时显示版本徽标与状态徽标，因此单独打开文件也能看清它是设计稿还是已实现界面的参照。

## 6. 新增或修改原型

1. 复制 `TEMPLATE/` 目录并重命名（kebab-case，如 `session-permissions/`）。
2. 按 `CONVENTIONS.md` §2 填写 README 头部字段，版本填 `v0.1`，并建立「版本记录」表。
3. 在 `index.html` 顶部放 `v0.1` 与状态徽标。
4. 在本文件 §3 清单新增一行，版本列保持一致。
5. 原型外观变化若反映产品决策变化，必须在对应 `docs/architecture/`、`docs/product/` 或 ADR 中同步；**原型不单独作为决策来源**。
6. 按 `AGENT.md` §6：先最小检查、commit + push，再做完整检查。

## 7. 与正式文档的关系

本目录**不修改**任何已接受的架构或安全契约，也不替代 `docs/product/`、`docs/architecture/`、`docs/device-fabric/` 与 ADR。发生冲突时，以 [`../README.md`](../README.md) §2 的文档权威顺序为准。

原型评审产生的结论必须写回正式文档；只存在于原型里的结论，下次会话不予承认。

## 8. 文档版本记录

| 版本 | 日期 | 变更 |
| --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：目录、规范、模板与三个首批原型 |
| v0.2 | 2026-09-15 | 新增原型版本编号要求（`vMAJOR.MINOR`），清单增加「版本」列 |

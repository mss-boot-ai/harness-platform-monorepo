# Harness Platform 原型图与说明

- 状态：Draft（目录与规范本身待评审）
- 文档版本：v0.3
- 建立日期：2026-09-15
- 分支：`design/prototype-gallery`
- 用途：用可离线打开的结构化原型表达 Platform 与 HC 的完整页面与关键交互，并为每个原型配一份可追溯的说明文档。

## 1. 这里放什么、不放什么

**放**：

- 两个前端面的**完整页面清单**（HC 端 16 页、Platform 端 18 页）
- 界面布局与信息层级、真实字段与示例值、操作入口
- 关键交互流程与状态流转（连接态、执行态、权限、轮换）
- 与 `docs/architecture/`、`docs/product/`、`docs/remote/` 的对照关系

**不放**：

- 生产实现代码（在 `hc/`、`platform/`、`aba/`）
- 真实密钥、Token、Ticket、生产地址或用户数据
- 对未实现能力的「已完成」声明

原型是**表达手段**，不是验收证据。任何「已实现 / 已验证」结论只能来自代码、提交、CI 和验证报告，引用规则见 [`../README.md`](../README.md) §5。

## 2. 状态、保真度与版本

### 2.1 状态标签

| 标签 | 含义 |
| --- | --- |
| `Design only` | 仅设计，代码中无对应实现 |
| `Partial impl` | 代码中有部分实现，原型包含未实现部分 |
| `Visual baseline` | 对应界面已实现并验收，原型作为视觉/回归参照 |
| `Superseded` | 已被新原型取代，保留历史 |

### 2.2 保真度

| 级别 | 名称 | 用途 |
| --- | --- | --- |
| `F1` | 线框 | 讨论单个结构或流程争议 |
| `F2` | 局部稿 | 单个页面或组件的设计确认 |
| `F3` | **结构稿** | 完整页面清单交付（默认级别） |

完整页面清单必须用 F3。F3 要求：应用外壳、页面头、真实字段与示例值、操作按钮、状态变体、空/错/无权限态。规则见 [`CONVENTIONS.md`](CONVENTIONS.md) §2。

### 2.3 版本号

每个原型独立编号 `vMAJOR.MINOR`，首版一律 `v0.1`。

- **版本号只标识设计稿迭代**，不是 Platform / ABA / HC 的产品版本，不代表实现进度，也不得被引用为「已发布」「已上线」的依据。
- 版本号必须在三处一致：原型 `README.md` 头部、原型 `index.html` 徽标、下方清单表。
- 详情见 [`CONVENTIONS.md`](CONVENTIONS.md) §5。

## 3. 原型清单

| 目录 | 界面 | 页面数 | 版本 | 保真度 | 状态 | 关联文档 |
| --- | --- | --- | --- | --- | --- | --- |
| [`hc-pages/`](hc-pages/) | **HC 端完整页面** | 16 | v0.1 | F3 | Partial impl | [`../architecture/HC.md`](../architecture/HC.md) §20、[`../remote/README.md`](../remote/README.md) |
| [`platform-pages/`](platform-pages/) | **Platform 端完整页面** | 18 | v0.1 | F3 | Visual baseline | [`../roadmap/verification/2026-09-04-platform-m1.md`](../roadmap/verification/2026-09-04-platform-m1.md)、[`../architecture/PLATFORM.md`](../architecture/PLATFORM.md) |
| [`aba-enrollment/`](aba-enrollment/) | ABA 设备授权流程（终端 + 流程 + 状态机） | — | v0.1 | F3 | Partial impl | [`../architecture/ARCHITECTURE.md`](../architecture/ARCHITECTURE.md) §6.1、ADR-0005 |
| [`TEMPLATE/`](TEMPLATE/) | 新增原型骨架与样式源 | — | v0.1 | F3 | 模板 | [`CONVENTIONS.md`](CONVENTIONS.md) |
| [`hc-remote-console-v1/`](hc-remote-console-v1/) | ~~HC 远程会话主界面~~ | — | v0.1 | F2 | Superseded | 已被 `hc-pages/` 取代 |
| [`platform-admin-v1/`](platform-admin-v1/) | ~~Platform 管理后台五页面~~ | — | v0.1 | F2 | Superseded | 已被 `platform-pages/` 取代 |

## 4. 建议阅读顺序

1. 本文件
2. [`CONVENTIONS.md`](CONVENTIONS.md)：制作规范、保真度、版本规则与禁止事项
3. [`platform-pages/README.md`](platform-pages/README.md) 与 [`hc-pages/README.md`](hc-pages/README.md)：页面清单与实现状态
4. 打开对应 `index.html` 查看页面稿

## 5. 如何打开

原型是自包含单文件 HTML：双击用浏览器打开即可，不需要构建、开发服务器或网络。文件内不引入任何外部脚本、字体、图片或 CDN。

`index.html` 顶部会同时显示版本徽标与状态徽标，因此单独打开文件也能看清它是设计稿还是已实现界面的参照。

## 6. 新增或修改原型

1. 复制 `TEMPLATE/` 目录并重命名（kebab-case）。
2. 按 `CONVENTIONS.md` §3 填写 README 头部字段（含保真度），版本填 `v0.1`，并建立「版本记录」表。
3. 在 `index.html` 顶部放 `v0.1` 与状态徽标。
4. 在本文件 §3 清单新增一行，版本与保真度保持一致。
5. 页面清单类原型必须附「页面 / 路由或入口 / 实现状态」表。
6. 原型外观变化若反映产品决策变化，必须在对应 `docs/architecture/`、`docs/product/` 或 ADR 中同步；**原型不单独作为决策来源**。
7. 按 `AGENT.md` §6：先最小检查、commit + push，再做完整检查。

## 7. 与正式文档的关系

本目录**不修改**任何已接受的架构或安全契约，也不替代 `docs/product/`、`docs/architecture/`、`docs/device-fabric/` 与 ADR。发生冲突时，以 [`../README.md`](../README.md) §2 的文档权威顺序为准。

原型评审产生的结论必须写回正式文档；只存在于原型里的结论，下次会话不予承认。

## 8. 文档版本记录

| 版本 | 日期 | 变更 |
| --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：目录、规范、模板与三个首批原型 |
| v0.2 | 2026-09-15 | 新增原型版本编号要求（`vMAJOR.MINOR`），清单增加「版本」列 |
| v0.3 | 2026-09-15 | 新增 `hc-pages/`（16 页）与 `platform-pages/`（18 页）完整页面稿；引入保真度分级（F1/F2/F3）；`hc-remote-console-v1/`、`platform-admin-v1/` 标为 Superseded |

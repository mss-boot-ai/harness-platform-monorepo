# Platform 管理后台还原稿

- 版本：v0.3
- 状态：Visual baseline（含未实现页面，逐页状态见下）
- 保真度：F4（还原稿）／应用外壳为**包内源码级**（v0.3 起，不再是重建）
- 界面：Platform（mss-boot-admin v1.3.7 Thin Host）Harness 业务页面 + 外壳 + 四态，共 10 屏。
- 使用者：管理员 + 运维
- 关联文档：[`../../roadmap/verification/2026-09-04-platform-m1.md`](../../roadmap/verification/2026-09-04-platform-m1.md)、[`../../architecture/PLATFORM.md`](../../architecture/PLATFORM.md)、AGENT.md §3
- 关联代码（还原来源）：
  - **上游包**：`@mss-boot-io/admin-web@1.3.7` 的 `package/default-settings.ts`、`package/core-routes.cjs`、`package/locales/{zh-CN,en-US}.ts`、`src/shared/design-system/{theme.ts,PageContainer.tsx,PageState.tsx}`、`src/shared/layout/{RuntimeLayout,LayoutChrome,HeaderActions}.tsx`、`src/tailwind.css`
  - **本仓库**：`platform/internal/modules/harness/{module.go,authorization_migration.go}`、`platform/web/src/business/{locales/zh-CN.ts,routes.config.ts,harness/*,pages/*}`
- 已实现部分：五个 Harness 页面（列定义、状态色、操作按钮、弹窗文案）；菜单条目与 4 项权限；应用外壳（主题、布局、品牌、菜单、水印、页脚、头部动作）；M1 报告覆盖五页浏览器验收、403、错误态与空态。
- 未实现部分：证书与 Trust Manifest 页面、轮换任务页面（代码中不存在）；未覆盖登录页、菜单搜索浮层、通知浮层、语言切换浮层、移动端头部。
- 非目标：不是可运行应用；不复制 Foundation 源码；不复制第三方品牌图形；不新增字段、权限或 API。
- 首次建立：2026-09-15
- 最后更新：2026-09-15

## 1. 上游包怎么取的（重要）

`@mss-boot-io/admin-web@1.3.7` 不在本仓库，本地也未安装依赖。取包方式：

```bash
mkdir -p /tmp/proto-ref/admin-web-1.3.7 && cd /tmp/proto-ref/admin-web-1.3.7
curl -sSL -o pkg.tgz https://registry.npmjs.org/@mss-boot-io/admin-web/-/admin-web-1.3.7.tgz
tar xzf pkg.tgz      # 得到 package/（含 src/ 源码，不只有构建产物）
```

- **只下载到临时目录解析，没有安装到仓库**，`platform/web` 下不产生 `node_modules`，仓库无任何新增未跟踪文件。
- 该 tgz 的 `integrity` 为 `sha512-dWobxeye4pTIeBfNZqFXCVKSVwJbrV9lz/LQebCIe/M4DK57uLP/IBMei8U6A76ekNmU94oyOCJIwpugMoornA==`。
- 包内包含完整 `src/`，因此外壳、主题与菜单都能取到实数。

## 2. 已确认的上游实数（v0.3 新增）

### 2.1 默认主题：暗色

`package/default-settings.ts`：

```text
navTheme:      realDark        ← 关键
colorPrimary:  #1677ff
layout:        mix
contentWidth:  Fluid
fixedHeader:   false
fixSiderbar:   true
colorWeak:     false
title:         mss-boot-io
logo:          /logo.svg
splitMenus:    false
```

`src/shared/design-system/theme.ts` 用 navTheme 驱动 antd：

```ts
algorithm: settings.navTheme === 'realDark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
cssVar: { prefix: 'mss' },
token: { colorPrimary: settings.colorPrimary, borderRadius: 8,
         fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif" },
components: { Button: { controlHeight: 36 }, Card: { headerFontSize: 16 },
              Table: { headerBg: 'var(--mss-color-fill-quaternary)' } },
```

`ThemeRuntimeProvider` 把它交给 `ConfigProvider`，并设 `html.dataset.mssTheme = navTheme`、`html.style.colorScheme = 'dark'`，`colorWeak` 时给 `body` 加 `filter: invert(80%)`。**结论：整个后台默认是暗色的。**

### 2.2 布局与外壳

| 项 | 值 | 来源 |
| --- | --- | --- |
| 布局模式 | `mix`（顶部头栏 + 左侧菜单栏） | `default-settings.ts` |
| 头栏高度 | 56px | ProLayout 默认 |
| 侧栏宽度 | 208px | ProLayout 默认 |
| 品牌 | logo `/logo.svg`（28×28，`h-7 w-7`）+ 标题，链接到 `/workplace` | `LayoutChrome.tsx` |
| 面包屑 | `breadcrumbRender: (routers=[]) => routers` | `RuntimeLayout.tsx` |
| 水印 | 内容为当前用户显示名，`gapX 320` / `gapY 240` / `fontSize 12`，暗色下 `rgba(255,255,255,0.035)` | `RuntimeLayout.tsx` |
| 页脚 | `© {年份} {版权}`（默认 `mss-boot-io`）+ 备案号（有则显示）+ GitHub 链接 `mss-boot-admin` | `LayoutChrome.tsx` |
| 头部动作 | 菜单搜索、通知、帮助文档（`https://docs.mss-boot-io.top`）、语言切换、头像+用户名菜单 | `HeaderActions.tsx` |
| 头像菜单项 | 账户中心 `/account/center`、个人设置 `/account/settings`、退出登录 | `HeaderActions.tsx` + i18n |
| 全局样式 | `body { background: var(--mss-color-bg-layout) }`；`*:focus-visible { outline: 2px solid var(--mss-color-primary) }` | `src/tailwind.css` |

### 2.3 Foundation 菜单（16 项，取自 `core-routes.cjs` + `locales/zh-CN.ts`）

| 路径 | icon 字段 | 中文名 | i18n key |
| --- | --- | --- | --- |
| `/workplace` | dashboard | 工作台 | `menu.workplace` |
| `/users` | user | 用户管理 | `menu.users` |
| `/role` | team | 角色管理 | `menu.role` |
| `/menu` | menu | 菜单管理 | `menu.menu-management` |
| `/departments` | apartment | 部门管理 | `menu.departments` |
| `/posts` | cluster | 岗位管理 | `menu.posts` |
| `/task` | wallet | 任务调度 | `menu.task` |
| `/notice` | message | 通知中心 | `menu.notice` |
| `/log` | fileText | 日志中心 | `menu.system-log` |
| `/system-config` | inbox | 系统配置 | `menu.system-config` |
| `/app-config` | setting | 应用设置 | `menu.app-config` |
| `/language` | translation | 语言管理 | `menu.language` |
| `/option` | unorderedList | 选项管理 | `menu.option` |
| `/presentation-config` | layout | 页面展示配置 | `menu.presentation-config` |
| `/security` | safety | 安全管理 | `menu.security` |
| `/security/online-sessions` | desktop | 在线会话 | `menu.online-sessions` |

另有 `hideInMenu: true` 的路由（`/users/control/*`、`/role/create`、`/menu/:id`、`/task/:id`、`/account/*` 等）不出现在菜单中。

**Harness 条目**（后端 `module.go` 的 `business.Menu`）：`DisplayName: "Harness 平台"`、`DisplayNameEn: "Harness Platform"`、`Icon: "RobotOutlined"`、`Order: 45`、`Path: "/harness"`。

### 2.4 页面状态组件（`PageState.tsx` 原文）

| 组件 | 实现 |
| --- | --- |
| `PageLoading` | `<div aria-busy="true" role="status"><Skeleton active paragraph={{rows}} title/></div>`，默认 `rows=5` |
| `PageEmpty` | `<Empty description={...} image={Empty.PRESENTED_IMAGE_SIMPLE}/>` —— **简化插画**，不是默认大插画 |
| `PageError` | `<Result status="error" title="加载失败" subTitle={message} extra={<Button type="primary" icon={<ReloadOutlined/>}>重试</Button>}/>` |
| `PageForbidden` | `<Result status="403" icon={<LockOutlined/>} title="403" subTitle={message}/>` |

`PageContainer` 是 ProComponents `PageContainer` 的适配器，把标题包成 `<h1>`（`color:inherit; font:inherit; margin:0`）。

### 2.5 相关界面文案（`locales/zh-CN.ts`）

| key | 文案 |
| --- | --- |
| `states.loadError` | 加载失败 |
| `states.forbidden` | 你没有访问此页面的权限。 |
| `states.notFound` | 页面不存在，或该能力尚未注册到 Ant Design 6 应用。 |
| `actions.retry` | 重试 |
| `actions.refresh` | 刷新 |
| `menu.account-center` / `menu.account-settings` / `menu.logout` | 账户中心 / 个人设置 / 退出登录 |
| `navigation.documentation` | 打开帮助文档 |

Harness 侧覆盖：`harness.states.forbidden` = 「当前账号没有访问 Harness 管理功能的权限。」

## 3. 屏清单

| # | 屏 | 证据 |
| --- | --- | --- |
| P1 | Harness 平台概览 | `pages/Overview/index.tsx` + §2 外壳实数 |
| P2 | 端点注册审批 | `pages/Enrollments/index.tsx` |
| P2b | 验证用户码弹窗 | 同上 + antd Modal |
| P3 | 端点管理 + 吊销确认 | `pages/Endpoints/index.tsx` + antd Popconfirm |
| P4 | ACP 会话 + 关闭确认 | `pages/Sessions/index.tsx` |
| P5 | 密文投递状态 | `pages/Delivery/index.tsx` |
| P6 | 403 禁止态 | `PageState.tsx` → `PageForbidden` |
| P7a | 空态 | `PageState.tsx` → `PageEmpty` |
| P7b | 加载态 | `PageState.tsx` → `PageLoading` |
| P7c | 错误态 | `PageState.tsx` → `PageError` + M1 实测文案 |

## 4. 关键还原点（易做错的地方）

1. **默认是暗色主题**（`navTheme: realDark` → `darkAlgorithm`），不是浅色。
2. **侧栏只有一个 Harness 条目**，其余四页 `hideInMenu: true`，五页靠页内 **Tabs** 切换。
3. **概览是 5 个统计卡不是 4 个**（含「冲突密文帧」），栅格 `xs=24 sm=12 xl=8`。
4. **审批弹窗的确定按钮文案随决策切换**，用户码为空时禁用。
5. **端点管理操作列三态**：ACTIVE → 暂停+吊销；SUSPENDED → 恢复+吊销；REVOKED → `—`。
6. **会话页终止态** `ABA_REVOKED / CLOSED / FAILED` 不出「关闭」，但保留「查看投递」。
7. **投递页未输入 Session ID 时不发请求**，只显示一行次要文字。
8. **`CompactID` 自带复制按钮且 `maxWidth: 180`**，长 ID 省略号显示。
9. **四态判定顺序** loading → forbidden → error → empty → ready，`forbidden` 优先。
10. **空态用简化插画**（`PRESENTED_IMAGE_SIMPLE`），不是默认大插画。
11. **页面有用户名水印**，暗色下 `rgba(255,255,255,0.035)`。
12. **`borderRadius` 是 8、按钮高 36**，不是 antd 默认的 6 / 32。

## 5. 还原偏差

1. **品牌 logo 用占位块。** 生产用 `/logo.svg`（166×166 内嵌位图，mss-boot-io 商标）。按「不复制第三方品牌、商标」的约定，只还原 28×28 尺寸与位置。
2. **菜单图标 path 为近似。** `@ant-design/icons` 未安装；图标语义取自 `core-routes.cjs` 的 `icon` 字段，图形自绘。
3. **ProLayout 内部配色按 realDark 语义重建。** 顶栏与侧栏用 `#001529`、选中项用 `colorPrimary`；ProLayout 实际 token 值（尤其悬停态、子菜单缩进）可能略有差异。
4. **antd 暗色 token 取 darkAlgorithm 默认值**（`bg-layout #000`、`bg-container #141414`、`bg-elevated #1f1f1f`、`border #424242`、`text rgba(255,255,255,.85)`），非从构建产物 CSS 读取。已确认的实数只有 §2.1 列出的那些。
5. **示例数据为占位值**，不是任何真实运行数据。
6. **交互未绑定**：Tabs、按钮、Popconfirm、Modal 均为静态；水印为静态平铺，不随登录用户变化。
7. **未覆盖**：登录页（`/user/login`，`layout:false`）、菜单搜索浮层、通知浮层、语言切换浮层、头像下拉菜单、`message.useMessage()`、`colorWeak` 的 `body{filter:invert(80%)}`、移动端 `AccessibleMobileHeader`、面包屑完整层级、表头排序与列宽拖拽。

## 6. 待确认问题

1. 生产实际运行的 `navTheme` 是否被应用级或用户级 override 覆盖为浅色？代码默认值是 `realDark`，但 `ThemeSettings` 支持应用/用户层覆盖，部署后可能不同 —— **这是本稿最需要你确认的一点**。
2. 「冲突密文帧」的业务含义与处置入口是否需要补充说明页面？
3. 4 项权限的组件记录（`/harness/permissions/*`）是否需要在菜单管理页可见，还是保持隐藏？
4. 是否需要补 `工作台`（`/workplace`）一屏，让对照开发的 agent 有完整入口示例？

## 7. 版本记录

| 版本 | 日期 | 变更 | 触发文档 / 决策 |
| --- | --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：按真实 locales 与页面组件还原 9 屏；外壳标注为重建 | `platform/web/src/business` 当前源码 |
| v0.2 | 2026-09-15 | 从后端 `module.go` 与 `authorization_migration.go` 回填菜单条目与 4 项权限；新增 P7c 错误态；屏数更正为 10 | M1 验收报告、`platform/internal/modules/harness` |
| v0.3 | 2026-09-15 | **从 `@mss-boot-io/admin-web@1.3.7` 包内源码取得外壳实数**：默认 `navTheme: realDark` 导致整站暗色（v0.2 浅色方向错误）、`layout: mix`、品牌 `mss-boot-io`、`borderRadius 8`、`Button.controlHeight 36`、16 项 Foundation 菜单名、用户名水印、页脚、头部动作、四态组件实现；整体改为暗色并重绘 | 上游包 `package/*` + `src/shared/*` |

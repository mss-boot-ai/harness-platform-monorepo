# HC Web 还原稿

- 版本：v0.1
- 状态：Partial impl
- 保真度：F4（还原稿）
- 界面：HC Web 生产界面的静态还原，覆盖欢迎页、会话页、侧栏、执行环境、设置弹窗、确认弹窗、只读态、端点非独占态与移动端，共 10 屏。
- 使用者：最终用户
- 关联文档：[`../../architecture/HC.md`](../../architecture/HC.md)、[`../../product/HC-CHAT-EXPERIENCE.md`](../../product/HC-CHAT-EXPERIENCE.md)、[`../../remote/README.md`](../../remote/README.md)、[`../../roadmap/verification/2026-09-14-hc-remote-conversations.md`](../../roadmap/verification/2026-09-14-hc-remote-conversations.md)
- 关联代码（还原来源）：`hc/web/src/styles.css`（全文照抄）、`hc/web/src/App.tsx`、`hc/web/src/SessionSetup.tsx`、`hc/web/src/GatewaySetup.tsx`、`hc/web/src/PlatformSetup.tsx`、`hc/web/src/chat/{ChatWorkspace,Dialog,Icon,Markdown,model}.tsx`、`hc/web/src/remote/RuntimeControls.tsx`
- 已实现部分：本稿覆盖的界面均在代码中存在；DPoP + 单次 Ticket + WSS 首帧挑战、持久多会话、刷新恢复、标签页独占、审批/取消后继续等已由验证报告覆盖。
- 未实现部分：PWA 安装提示、`manifest.webmanifest`、真实 WebSocket 连接过程、IndexedDB 实际读写、中文输入法 composition 保护的实际行为、跨设备 Attachment 与控制租约相关界面（属 R08/R09 未完成范围）。
- 非目标：不是可运行应用，不绑定任何交互；不替代真实构建产物；不修改任何生产样式或代码。
- 首次建立：2026-09-15
- 最后更新：2026-09-15

## 1. 还原方式

| 项 | 做法 | 可信度 |
| --- | --- | --- |
| 样式 | `hc/web/src/styles.css` **逐行照抄**，未改一个字符 | 高：与生产为同一份 CSS 文本 |
| DOM 结构 | 按 `ChatWorkspace` / `SessionSetup` / `RuntimeActivity` 的 JSX 层级复现 | 高：类名、嵌套、条件分支均对齐源码 |
| 全部文案 | 从 TSX 中逐条摘出（占位符、提示、弹窗、状态胶囊、aria-label、title） | 高：中文文案未改写 |
| 图标 | 使用 `chat/Icon.tsx` 里的原始 SVG path 与 `stroke-width=1.7` | 高 |
| 示例数据 | 会话标题、Runtime ID、指纹、工具输入输出为占位值 | 中：字段名与格式对齐源码，取值是编造的 |
| 弹窗背景 | 用叠加层复现 `::backdrop` | 中：静态文件无法调用 `showModal()` |

## 2. 屏清单

| # | 屏 | 触发条件 |
| --- | --- | --- |
| R1 | 欢迎页（空态） | 无消息且非只读 |
| R2 | 会话页（计划/工具/权限/用量） | 有消息且执行端上报了 runtime 事件 |
| R3 | 侧栏 · 搜索与会话列表 | 点击「搜索本地会话」 |
| R3b | 只读态与保留草稿 | 会话密钥缺失、过期或端点故障 |
| R4 | 执行环境 popover | 点击标题栏 Runtime ID |
| R5 | 连接与设置 · 第 1 步 | 未创建浏览器身份 |
| R6 | 连接与设置 · 已就绪 | 身份 + 账户 + 网关均就绪 |
| R7 | 结束当前会话确认 | 点击「结束会话」 |
| R8 | 确认工具授权 | 点击 `allow_*` 类权限选项 |
| R9 | 端点非独占 / 不支持 | 另一标签页持有 Web Locks，或浏览器不支持 |
| R10 | 移动端（390px） | 视口 ≤ 700px |

## 3. 关键还原点（易做错、必须对齐源码的地方）

1. **侧栏条目副标题**取值固定为五选一：`已结束 · 只读` / `需要检查` / `等待授权` / `正在回复` / `已保存`，判定顺序即 <code>isTerminal → fault||blocked||recovery → pending permission → awaiting → 默认</code>。
2. **欢迎页输入框占位符随连接态变化**：未连接是「有什么想交给 Agent 的？」，已连接是「给你的 Agent 发送消息…」。一个字的差别，但源码里是两个分支。
3. **发送按钮在未连接时不是发送**：`aria-label` 是「连接 Agent」，`title` 是「先连接 Agent，草稿会保留」，点击走的是打开设置。
4. **方形按钮有两种语义**：Runtime 支持 cancel 时是「停止本轮」（保留会话），否则是「结束当前会话」（整个会话）。两者都**不撤销**已执行的操作。
5. **runtime-activity 的渲染顺序**是 计划卡 → 工具卡 → 权限卡，不是按事件时间混排。
6. **工具卡有 `tool-{status}` 修饰类**，失败态用 `tool-failed` 变红边。
7. **权限按钮文案由执行端 kind 决定**：`allow_once`/`allow_always`/`reject_once`/`reject_always`，且另有独立的「不授予权限」按钮。不存在「永久允许所有命令」。
8. **只读态覆盖整个输入区**，不是禁用输入框；草稿非空时给只读 textarea + 复制按钮。
9. **`UNCERTAIN` 与「回复中断」是两层信号**：`uncertain-label`（执行结果待确认）+ 正文的 `message-muted`（不要直接重发）。
10. **标题栏显示的是 Runtime ID**（`runtimeProfileId`），不是模型名。

## 4. 还原偏差

1. **画廊外壳**：为在同一文件并排展示 11 屏，额外加了 `.gal` / `.screen` 两组样式，并把 `.chat-shell` 的 `height:100dvh` 覆盖为 `100%`。生产界面没有这两组样式。
2. **弹窗背景**：生产用 `<dialog>` 的 `::backdrop`；静态文件无法 `showModal()`，改用一层 `.dialog-stage` 复现同一视觉（`#10181350` + `blur(3px)`）。
3. **示例数据**全部为占位值。
4. **交互**未绑定：按钮无行为，`<details>` 可折叠，输入框可输入但不发送。
5. **字体**：生产 CSS 声明 Inter 但未外链加载，实际渲染取决于系统是否安装；本稿同样如此，因此在未装 Inter 的机器上会回退到系统字体，与 CSS 声明一致。

## 5. 待确认问题

1. R5 里安全存储「不支持」分支的状态胶囊文案是「不支持」，主按钮禁用；是否需要额外给出可用的替代路径说明？
2. R9 的两种降级文案（非独占 / 不支持 Web Locks）是否需要在标题栏也放一个常驻标识？
3. `latest-button`（回到最新）未单独出屏，是否需要补一屏展示滚动位置与按钮位置？
4. 小程序 HC 的界面结构完全不同，是否单独立稿？

## 6. 版本记录

| 版本 | 日期 | 变更 | 触发文档 / 决策 |
| --- | --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：按真实 `styles.css` 与组件源码还原 11 屏 | `hc/web/src` 当前源码 |

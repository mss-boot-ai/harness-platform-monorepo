# Harness Remote 实施与验收矩阵

- 日期：2026-09-13
- 起点：`c1daa456f1f8bb23f98d2e7677ac6f097f4f47e4`
- 当前检查点：设计已编写；本文件首次提交时下列功能均未以本轮代码验收。
- 所有阶段留在PR #3，以可恢复提交推进；禁止force-push或自动合并。

## 2026-09-14 当前增量状态

PR #3 已合并为 main `1227b392e664959c987865e14c55758a1c620952`，并协调部署到 dev-242。用户实际使用发现项目选择、发送和失败恢复缺口，当前在 `codex/remote-project-workspace-recovery` 继续完整产品交付；新入口见 [PRODUCT-COMPLETION](PRODUCT-COMPLETION.md)。开发阶段采用统一当前契约，旧报告不再作为已部署真实模型流程完成的证据。

以下首次状态表保留原规划。当前 HC C3 子切片已在 `a0dad3fdbfb9a15b701f4973c48b8d2158072442` 验证：生产页面接入持久会话控制器、同一浏览器端点刷新恢复、双会话配置/草稿/事件隔离、标签页独占与接管、审批/取消后继续、原始密文补传及吊销拒绝。102 个 HC 单元测试、两个断网工具测试和 12 项 PR CI 通过，其中认证浏览器运行实际 Admin/Gateway/ABA 与确定性 ACP 测试程序。见[完整证据](../roadmap/verification/2026-09-14-hc-remote-conversations.md)。

这只验证了 R06/R07 相关的 HC 子集，不把 R05、R06、R07 或完整 C3 标为完成。执行主机重启恢复、权威工作区写入隔离、独立设备 Attachment/控制租约、真实模型验收、资源、生产部署与最终跨端场景仍未完成。浏览器同 profile 的标签页接管不是跨设备授权。

## 1. 功能和证据跟踪

状态只允许planned/implemented/verified/blocked；verified必须列出测试SHA、命令/CI和证据范围，模拟与真实Agent分开。一个控件或未集成模块不能算完整功能。

| ID | 交付项 | 首次状态 | 完成门槛 |
| --- | --- | --- | --- |
| R01 | 双向非阻塞Agent I/O | planned | prompt期间ping/取消/审批可处理，stdout即时转发，有界背压 |
| R02 | 真实能力与配置 | planned | 读取真实model/mode/thought/context配置；只在确认后生效 |
| R03 | CancelTurn | planned | 停本轮，不关闭会话；之后新轮成功；无副作用回滚谎报 |
| R04 | 权限往返 | planned | 原始pending请求、拒绝/批准/过期、一次消费、跨请求伪造拒绝 |
| R05 | 持久Session Host | planned | 客户端关闭后执行继续；Host恢复有完整状态且不重做未知动作 |
| R06 | 多会话与workspace隔离 | planned | 新建不关闭旧会话；切换各自配置/草稿/流；写冲突正确拒绝 |
| R07 | 加密历史与刷新恢复 | planned | 刷新同Endpoint恢复；Platform DB/log无明文；缺key明确恢复流程 |
| R08 | 多端Attachment | planned | 两个独立浏览器profile授权接入同Run；独立key/撤销 |
| R09 | 控制租约与跨端审批 | planned | 两端竞争唯一writer；旧generation拒绝；一次批准不重复 |
| R10 | 工具/计划/usage/成果UI | planned | 实际事件驱动、Diff/失败状态/usage来源准确 |
| R11 | 安全资源 | planned | 上传/下载、ACL、配额、截断/替换/路径逃逸拒绝 |
| R12 | Runtime adapters | planned | 至少一个真实完整Runtime；其他Runtime能力矩阵，不伪造支持 |
| R13 | 移动PWA与通知 | planned | 窄屏、前后台、草稿、权限提示、通知不泄密 |
| R14 | 故障/安全/部署 | planned | 网络断开、进程重启、吊销、日志满、重放、长任务和回滚 |
| R15 | 最终端到端验收 | planned | 真实跨端场景通过；不是只跑合成fixture |

## 2. 提交切片

C0：本方案、ADR与验收矩阵先落盘提交。
C1：非阻塞Agent进程I/O、反向请求和取消，确定性Agent故障测试。
C2：真实配置/能力、HC Item/Approval/Config界面及对应协议回归。
C3：Session Host、业务journal/operation去重、快照与多会话。
C4：持久安全恢复、Attachment独立授权/密钥、控制租约。
C5：Runtime适配、资源/成果/PWA/通知，整条产品联调。
C6：负向安全、实际Runtime、跨端故障、部署产物和最终证据。

每个检查点先最小语法/秘密检查→commit/push→完整CI；失败新提交修复。不得删除测试来制造成功，不把上个SHA的CI自动沿用给新功能。

## 3. 必测矩阵

- 原有Thin Host/AWP签名、AAD、HPKE、ACK和原Frame重放回归。
- JSON-RPC请求ID方向/作用域，未知方法、批处理、超大/截断/无换行输入、输出flood。
- 真正的流式时序：第一更新出现在最终结果前，长prompt期间取消不等待完成。
- 权限：模型伪造批准、错误option ID、跨Run/epoch批准、重复/过期/撤销、断线。
- 配置：不支持选项拒绝、pending不当effective、会话A不污染B、model切换更新可用档位。
- 多端：两个独立key store、同账号无Grant不能解密、只读不能写、接管fencing。
- 恢复：刷新、断网、Gateway/ABA/Host/Agent分别重启，epoch变化、journal满、snapshot/event无空窗。
- 副作用：DISPATCH_STARTED后掉电进入UNKNOWN；重投递不重做；cancel不等于回滚。
- 内容安全：Prompt/工具参数/Token/key canary不能出现在Platform logs/DB/raw artifact；XSS/资源URL/路径逃逸拒绝。
- 产品：320/390px、桌面、中文IME、键盘/触摸、历史/草稿、审批可读性、工具状态与成果下载。

## 4. 最终真实场景

在桌面HC用真实Runtime启动多步任务；确认实际开始后关闭页面。另一个独立且明确授权的手机HC恢复同Conversation，读取准确历史和工具状态，完成一次真实权限交互，取消当前Turn后继续下一Turn。全过程不重复原动作、默认Platform不可读内容，并保留可核验的SHA/日志/截图。

第二组验证故障恢复：Gateway重启、执行端短断网、端点吊销、Agent进程失败，分别验证有界队列、fencing与UNKNOWN。不能仅以模拟数据和React测试宣布上述场景通过。

## 5. 环境限制的处理

缺真实模型凭据、目标主机连接、外设或部署环境时，保持相应项目blocked/待验证并明确说明；使用确定性测试提供其能证明的部分，不谎称真实验收。PR仍保持Draft直到门槛实际完成。源代码可通过授权GitHub连接器读取/写入；完整构建可在现有CI执行，产物不包含checkout凭据、环境秘密或用户数据。

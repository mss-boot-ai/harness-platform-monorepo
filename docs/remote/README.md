# Harness Remote 产品技术方案

- 日期：2026-09-13
- 分支：`design/device-fabric-foundation`，PR #3。
- 代码起点：`c1daa456f1f8bb23f98d2e7677ac6f097f4f47e4`。
- 状态：用户已授权实施；本提交仅设计和规划，进度见[DELIVERY](DELIVERY.md)。
- 架构决策：[ADR-0007](../adr/0007-remote-session-host-and-product-completion.md)。

## 1. 产品目标与边界

从手机/浏览器完整使用运行在自己电脑、服务器或容器里的Agent。远程发起、查看实时消息和工具、审批、模型配置、取消本轮、继续任务、阅读成果、跨刷新/跨端恢复，执行与秘密保留在用户认可的主机。

普通用户始终从新建对话和输入框开始，不强迫先填写Task/Run表单。底层对象自动建立，复杂元数据放详情。目标是可日常使用的Remote产品，不把“长得像聊天网站”当成完成，也不承诺复制第三方模型质量、服务或商标。

主机必须持续可用。手机退出不影响已授权任务，主机掉电不承诺正在执行的外部动作可透明恢复。对未知副作用显示结果不确定，并提供查询/人工确认而非自动重跑。

## 2. 当前缺口（起点事实）

现有ABA process以同步prompt收集一轮消息；Gateway处理过程等待该轮，控制消息可能被阻塞。DeepSeek适配器先run再返回文本，不支持动态配置/完整权限往返和loadSession。HC当前一个活动会话、本页只读历史、关闭会话代替停止本轮。已有加密、Journal和ACK可复用，但不等于持久业务会话或跨端授权。

这些事实来自起点代码而非对当前产品的永久限制；实现后逐项更新证据，不覆盖旧报告。

## 3. 分层

```text
HC Web / PWA
   |  独立端点身份、加密Attachment、按能力呈现
Platform
   |  身份/RBAC、元数据授权、密文中继/配额/投递
ABA
   |  本地密钥、持有证明、出站连接、Workspace/Runtime准入
Runtime Session Host（可信执行区）
   |  Conversation/Run/Turn、事件日志、配置、权限、恢复
   +-- ACP Runtime adapter
   +-- Codex App Server adapter（按实际版本验证）
   +-- DeepSeek adapter（能力不支持就明确禁用）
```

Session Host不是另一个模型规划器。它与现有Device Fabric的Coordinator最终是同一个运行中心，设备仅通过受限交互/工具接入。首版允许本地组件同机，生产进程/UID隔离必须以实际配置证明。

## 4. 领域与状态

Conversation为长期用户上下文；Run绑定一次Runtime/Host/Workspace执行尝试；RuntimeSession为真实Agent会话；Turn为一次输入；Item为文本/工具/审批/usage/成果；Attachment为端点连接。

Conversation状态open/archived/deleted，删除是生命周期管理不等于回滚。Run为starting/idle/running/waiting_approval/interrupted/unknown/closed/failed。Turn为accepted/running/waiting_approval/completed/cancel_requested/cancelled/failed/unknown。

关闭Attachment不改变Run，取消Turn不关闭Conversation，新建Conversation不杀旧Run。Runtime崩溃不能用新的request套用旧Approval。切换Runtime/Host建立新Run并明确上下文交接，切换model只有Runtime支持才会话内生效。

## 5. 非阻塞执行核心

网络读写、解密、Agent进程I/O分别有界队列。每个Agent有单一stdin writer与stdout reader，JSON-RPC请求ID在作用域内映射；server-request权限往返与client-request独立。控制/取消不在模型prompt同一阻塞锁里。

分开启动、模型、工具、审批、idle和Run预算超时。取消只发一次且有确认状态，关闭有graceful deadline后按本地策略回收进程组。Agent日志不能进stdout污染协议，也不能把任意异常文本写公开日志。

初始化与session/new能力结果不丢弃；按安全映射回传给HC。只允许许可会话方法和与原pending request绑定的响应，不开放任意fs/terminal/RPC代理。原ACP sessionId与远端逻辑Session ID保持显式映射。

## 6. 配置、能力与权限

Capabilities包含实际Agent身份/版本、支持的模型/推理档位/模式、cancel/resume/tools/resources/contextUsage等。Runtime Profile目录只公开不透明ID和安全摘要，真实command/args/env/cwd留本地。

ConfigState保存revision、requested、effective、pendingChange；每轮保存effective快照。改变配置须同一writer、idle边界（或Runtime明确支持），拒绝未知字段/旧revision。模型变化后重新发现相关选项。没有准确Token数据时显示unknown，不用文本字符数伪造。

权限是用户/RBAC、endpoint scope、Session ACL、本地Workspace/Toolset、Runtime沙箱和动作审批的交集。Agent的模式标签不是OS权限证明。拒绝请求、过期、撤销和断线必须传回真实runtime，不留一个UI假批准。

## 7. 持久运行与恢复

可信Host对已接受操作先持久journal，后执行。业务operation_id、规范请求digest、runtime_epoch和事件revision用于去重/恢复。相同操作ID不同payload必须冲突。网络重试只重放原始Frame；业务重连先查原操作，不生成新ID重做。

事件日志记录Run/Turn/item与revision；Snapshot带last_revision，订阅从其下一项开始。一致性快照和事件读取不得有丢事件窗口。断流可请求缺口，清理后返回明确HISTORY_GAP并加载新快照。

HC刷新恢复本地安全wrap的密钥引用、当前端点授权和加密历史；进程重启恢复宿主journal。Runtime本身无法恢复在途执行时标UNKNOWN，不能拿history load成功冒充外部副作用确认。

## 8. 多端与控制租约

每Attachment独立keying与cursor；新手机由可信端授权或显式恢复流程加入。Platform不能因为同账号就给新Endpoint发旧SRK。历史由可信Host读取后为新端加密；新Attachment的history snapshot不是篡改旧Frame。

Run writer lease含endpoint、actor、generation、expires_at；Host对写操作fence旧generation。lease失效只阻止新写，不自动撤销正在执行的动作。观察者不能修改config或批准工具。

Approval包含request ID、runtime epoch、Run/Turn、动作digest、resource scope、policy revision、期限及可用选项。Decision绑定完整上下文、用户Endpoint和签名，CAS一次消费。多端首个有效终态生效，其余ALREADY_DECIDED。高风险按策略二次认证。

## 9. HC产品功能

会话列表支持新建、重命名、归档、搜索、恢复和并行切换；模型/推理/权限/工作区绑定会话。输入框区分已接收、发送未确认、执行中、取消待确认和可继续。草稿按会话隔离；网络恢复不自动重发。

Item Renderer分别呈现助手文本、工具卡、审批、usage、计划、Diff和Artifact。忽略/隐藏不可信原始HTML，代码/URL受限，历史搜索在有权解密的端执行。移动端保留易触达的输入/取消/审批，桌面可展开成果栏。

错误必须给下一步动作；不支持能力不显示假控件。执行状态与连接状态分别展示。通知是提醒而非可靠消息通道，不含未同意的敏感摘要，打开后查询权威状态。

## 10. 数据、资源和部署

Platform沿现有Thin Host模块扩展Conversation/Run元数据、Attachment/Grant/Lease、密文事件/资源引用；业务标题、Prompt、工具参数和私钥不落平台明文。迁移expand-only，保留原Session兼容。

Host保存受保护的journal/快照和Runtime会话索引；Workspace写入默认单writer或隔离worktree。资源为随机ID、受限类型/字节数、独立内容密钥和加密manifest；读取须验证授权与目录准入，不允许任意URL下载或路径逃逸。

先同一Gateway部署，扩多副本前验证全局fencing和跨实例路由。feature flags支持禁新Run、drain和回退旧链路，不能清空UNKNOWN日志来“修好状态”。没有明确部署授权/环境证明，不修改现有生产服务。

## 11. 上游参考与锁定

2026-09-13查阅：
- <https://developers.openai.com/codex/app-server>（现官方重定向至ChatGPT Learn）。
- <https://agentclientprotocol.com/protocol/v1/session-config-options>。
- <https://agentclientprotocol.com/protocol/v1/prompt-turn>。

适配以锁定SDK/Runtime实际schema为准，不用网页latest作为依赖，不把实验性接口当所有Agent兼容承诺。上述是协议参考，不是上游功能已集成的证据。

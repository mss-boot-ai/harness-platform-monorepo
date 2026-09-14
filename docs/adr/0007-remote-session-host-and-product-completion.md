# ADR-0007: Remote Session Host 与完整产品交付

- 日期：2026-09-13
- 状态：Accepted for implementation by user instruction；本PR未合并，不代表功能已实现。
- 起点：`c1daa456f1f8bb23f98d2e7677ac6f097f4f47e4`，PR #3，`design/device-fabric-foundation`。
- 保留：ADR-0001～0005的Thin Host、端点身份、默认Opaque与本地准入安全不变量。
- 细化/收敛：ADR-0006中的Companion/Run Coordinator/Headless HC保持提案；统一依赖本ADR的Runtime Session Host，不建立第二个独立运行中心。

## 用户决定

按照Remote完整产品方案继续当前PR；先提交设计与规划，再实现并推进实际验证。完成的依据是版本化验收，不是提交数、控件数量或模拟截图。不考虑Anthropic MHS，不直接推main，不force-push/rebase，不自动合并，不静默部署现有线上环境。

## 核心决定

1. 用户的Conversation/Run与HC页面、WebSocket、AWP Channel分离。关闭页面仅分离连接，不默认终止已经接受的执行。
2. Runtime Session Host是用户可信执行区内的单一业务运行状态中心。保留ABA的密钥、网络和本地准入职责；Agent Loop继续由真实运行时提供。
3. 网络读、协议验证、Agent stdin/stdout、持久journal和网络写独立调度。任何长模型调用、权限等待都不能阻塞连接ping、取消或其他会话。
4. ACP-first；原生运行时由明确适配器接入。能力发现、配置、历史、工具、usage、取消和审批按实际能力提供；不支持的能力明确返回，不伪造模型/推理档位。
5. 维护Conversation、Run、RuntimeSession、Turn、Item、Attachment、Approval和Artifact的不同生命周期。新聊天不强制关闭原聊天；同Workspace写入默认互斥。
6. 配置修改具有requested/effective/revision；每轮保留生效配置。权限模式不替代本地沙箱；模型不能批准自己。
7. 业务operation_id与传输message_id分离。重复投递不重复执行，原ID不同参数冲突；已开始但结果无法证明时UNKNOWN/UNCERTAIN。
8. 可信执行区持有业务事件和快照；Platform只保存加密载荷及最小路由/授权元数据。快照与增量事件使用一致revision，无订阅空窗。
9. AWP v1现有字段、AAD、签名、Nonce和原Frame重放语义不重新解释。需要Attach/恢复的新能力独立协商、版本化并有交叉向量；保留旧通道回归。
10. 每个端点独立密钥，每个Attachment独立授权和加密上下文。相同账号登录不自动获得旧会话解密权；新端通过可信端或明确恢复授权加入。
11. 一次Run可有多个授权观察端，但一个有效writer。控制租约有单调generation，可信执行端也做fencing；内容权、控制权、工具权、审批权分别授权。
12. 审批绑定原运行时请求、process epoch、Run、准确动作摘要、有效期和权限revision，一次消费；批准不等于动作执行成功。
13. CancelTurn与CloseSession独立。取消确认后仍可在同一对话继续；已发生副作用不宣称回滚。
14. 大文件、Diff与报告使用独立有配额的加密资源；不能通过资源API绕过workspace路径准入或请求任意URL。
15. 完整Remote目标以Web/PWA＋用户可信执行主机为主线；原生App、小程序、Mosaico、浏览器电脑控制和K8s调度沿用扩展路线，不能借扩展未完成掩盖Remote核心欠账，也不为等硬件拖延核心交付。

## 交付边界

首个真实运行时必须验证流式、审批、取消、配置和继续工作；第二个Runtime有独立supported/tested/unsupported矩阵。确定性测试Agent只作协议故障与CI验证，不算真实模型验收。没有目标账户/凭据、工具链或实际设备证据的项目保持待验证；不得以文档写成Implemented替代代码。

## 安全与兼容

不将Endpoint私钥、SRK、明文Prompt或工具参数引入Platform日志/数据库；不增加任意远程shell、程序、启动参数或路径注入。已有本地Profile由owner配置，远端只能引用已许可ID。运行时主机失陷或主机关闭不保证继续执行；新端恢复需受控密钥流程。

实施和证据入口：`../remote/README.md`、`../remote/DELIVERY.md`。每项状态必须关联具体提交与实际测试。所有门槛通过前PR保持Draft。

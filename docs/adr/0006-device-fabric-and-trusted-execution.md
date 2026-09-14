# ADR-0006：设备能力平面与可信执行边界

- 状态：Draft / 待评审；不是已经实现或验证的功能。
- 日期：2026-09-13
- 代码基线：`1102cb0ba1d2d95112957b492908d1af7a41e2c0`
- 扩展：ADR-0001、ADR-0003、ADR-0005；不废弃其 ACP 安全契约。
- 完整设计入口：[`../device-fabric/README.md`](../device-fabric/README.md)

## 背景

现有产品已经有 Platform、Rust ABA、Web HC 和 ACP v1 的加密远程会话。下一阶段需要让 HC Web/App/小程序与 ESP-Mosaico 同时成为交互终端和可授权的设备能力提供者。不能通过把 ESP 伪装成 HC_WEB、向 AWP v1 任意加入载荷，或让 Platform 解密设备请求来完成扩展。

现有产品设计分支中的 Task/Run/Approval/Artifact 模型应继续采用，避免退回以连接和协议诊断为中心的用户体验。对该分支的读取不代表合并或覆盖其内容。

## 决定

1. 产品仍是跨设备 Agent 工作台。设备能力是任务执行与人机交互的扩展，不另起一个通用 IoT 平台。
2. 保持 AWP v1 字段、方向、AAD、密钥包和重放语义不变；仅修复兼容性缺陷，不把它改造成万能设备协议。
3. 新增版本独立的 MDP 设备协议与 Device Fabric 领域。MDP 0.1 是设计修订号，拟议首个 wire major 为 1，尚未发布。
4. Endpoint 是安装实例安全主体；Device 是该主体提供的逻辑资源，二者是一对多关系。Roles 由服务端授权，Capabilities 是能力声明，二者不能混用。
5. Physical 与 Virtual Device 使用同一能力描述、调用、事件和资源语义；运行环境、权限状态、在线能力及身份保证级别保留差异。
6. Platform 只处理元数据授权、路由、密文持久化和配额。设备参数、结果、传感器值、音频、任务内容默认不对 Platform 明文可见。
7. 新建用户可信执行区内的 Device Bridge，作为 MCP Server 和 MDP 消费端。它可与 ABA 同机，但使用独立进程、独立 Endpoint 凭据和本地授权策略，不把 Agent Loop、硬件驱动或用户管理塞进 ABA。
8. MCP 是 Agent 调工具的北向接口；MDP 是设备互联与交互投影的南向接口；ACP 保留 Agent 会话语义。接口统一不等于所有数据都走一个协议。
9. 真实授权决策不能建模为普通 `request_confirmation` Tool。Approval Authority 运行在可信执行区，绑定原始动作、参数摘要、Run、有效期和权限版本；设备仅显示并提交有来源证明的决策。
10. 屏幕被触摸不等于已验证审批人身份。Mosaico 默认是低权限共享桌面终端；高风险操作必须转交满足策略的已认证 HC。
11. MDP 先采用 HTTPS bootstrap、WSS 二进制传输、受限 Protobuf 结构及既有密码学原语的独立安全 profile。不引入新的消息中间件作为首版依赖。
12. 首个纵向切片必须包含真实 Agent/MCP 调用，而不是等设备功能全部完成后才接 MCP。硬件到货前使用软件模拟器。
13. 不把 MHS 纳入依赖、适配器、协议命名或路线图。ESP-Claw、Linux 和板端自主 Agent Loop 均不作为本方案依赖。

## 不能选择的捷径

- 平台部署一个能读全部会话/设备明文的 MCP Server，却仍宣传默认端到端加密。
- 给设备分配 HC_WEB 类型或复制 HC/ABA 私钥来快速上线。
- 把模型返回的 `approved: true` 当作人的授权。
- 向 MCP 参数开放任意 shell、程序路径、GPIO 编号、HTTP URL 或动态代码。
- 因硬件密码库尚未适配而静默退化为 TLS-only。
- 从在线、附近或电量状态推断一个终端有权接收敏感内容。
- 为建立通用框架同时开发 Go/Rust/TypeScript/C 四套完整 SDK。

## 部署与信任

```text
HC -- AWP密文 --> Platform -- AWP密文 --> ABA --> 本地ACP Agent
                                                     |
                                               本地受限MCP
                                                     |
                                               Device Bridge
                                                     |
设备/HC Provider <-- MDP密文 -- Platform <-- MDP密文 ---+
```

Device Bridge 是明确受信任的明文终止点，只获得执行设备调用需要的上下文；它不自动拥有其他 Session 的 SRK。若将它托管在运营方服务器上，则必须明确标注该服务器是可信内容处理方，不能继续宣称该方不可读明文。

## 兼容、迁移与回滚

采用 expand-only migration。旧 EndpointType 和 Session 表继续工作，新增角色/运行形态侧表、设备表、MDP 投递表以及 Task/Run 映射。AWP Gateway 继续拒绝不支持的端点类型和载荷。

新能力使用独立 feature flag，初始关闭。关闭 MDP、Bridge 和 HC Provider 后旧 HC→ABA 链路仍可运行。不能通过删除未知执行状态或清空 Journal 来完成回滚。

## 代价与限制

增加一个可信区组件以及一组协议和密钥生命周期。MDP 加密不能阻止端点本身失陷，也不能阻止 Platform 拒绝服务。静态接收方 HPKE 密钥不提供针对事后接收方私钥失陷的完整前向保密。多终端审批与会话控制权分离，必须显式实现，不能凭新增一张设备表获得。

## 评审与验收

以 `docs/device-fabric/DELIVERY.md` 为实施顺序和验收清单；在代码、跨语言密码学、真实 Agent、浏览器和真机证据完成前，不把本 ADR 标记为 Implemented/Verified。

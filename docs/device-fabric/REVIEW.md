# 当前设计审阅与修订结论

- 状态：Draft，2026-09-13。
- 审阅类型：架构和关键代码路径的静态审阅；不是完整代码安全审计或新一轮 E2E 验证。
- 基线与范围见 [README](README.md)。

## 1. 保留的正确设计

Platform/ABA/HC 分层、ABA 出站连接、用户身份与 Endpoint 身份分离、默认 Opaque、端点独立密钥、AWP 原 Frame 重放、ACK 不等于执行成功、执行不确定时不自动重试，以及 Thin Host import 全部保留。

产品设计分支已经提出 Task、Run、Approval、Artifact 和控制租约；这是比把 Session 或在线设备当产品中心更好的方向。设备扩展应补充这个模型，而非另建以传感器和连接数为核心的后台。

## 2. 必须修订的发现

| ID | 优先级 | 依据与问题 | 本方案决策 |
| --- | --- | --- | --- |
| R01 | P0 | 旧提案把 Device MCP/状态/参数放到 Platform，与 `ARCHITECTURE.md`、ADR-0003 默认不读业务明文的边界冲突 | 明文终止在用户可信 Device Bridge，Platform 存密文与最小元数据 |
| R02 | P0 | `request_confirmation` 普通 Procedure 不能证明真实人的身份，也不能关联原始 ACP 权限请求 | Approval 独立域；原动作摘要、有效期、决策来源和一次消费；模型不能批准自己 |
| R03 | P0 | `wire.proto`、Domain、ACK/Resume 均限定 HC↔ABA，Session 固定两个端点 | AWP v1 不泛化；MDP 独立；不向 Mosaico 分发整场 ACP 会话密钥 |
| R04 | P0 | 当前 DeepSeek 适配器拒绝非空 `mcpServers`，image/audio/loadSession 为 false；run 后才分块发送最终文本 | 单独实现受本地配置约束的 Runtime Tool Binding；不得声称已有实时语音、任意工具接入、会话恢复或完整审批 |
| R05 | P0 | capabilities/roles 混用可能使设备自报能力后获得会话或执行权限 | Roles 为授权结果，Manifest 为不可信声明；可调用集取所有策略的交集 |
| R06 | P0 | 旧方案未定义执行后掉线、重启及 cancel 超时语义 | 分离 Delivery/Invocation/Approval；持久执行日志、UNKNOWN/UNCERTAIN、禁止盲目重发副作用 |
| R07 | P1 | 仅新增 Device Domain 没有补齐用户从 Mosaico 发起/继续任务的路径 | 增加受限 Interaction Projection 与短期 Actor Lease，不把 GPIO 事件直接解释为用户 Prompt |
| R08 | P1 | 单一 EndpointType 混合角色和运行形态；Web 注册又绑定 Browser Session/Origin | 新增角色与运行形态侧表、设备 Enrollment；保留旧类型兼容与浏览器 Origin 防护 |
| R09 | P1 | 一个 Endpoint 不等于一块实体硬件；Browser Tab 不是整个 PC 的权限 | Device 是逻辑资源，绑定 Endpoint 与 binding_epoch；不按硬件序列号继承信任 |
| R10 | P1 | 泛化 `device.invoke(name,args)` 容易退化成不受限远程控制 | 首版固定低风险 capability contract；受限 JSON Schema、许可的版本和参数；无 GPIO/shell/script 万能入口 |
| R11 | P1 | 先建三四套 SDK、MQTT/NATS 和调度器会拉大交付半径 | 首版 WSS+现有数据库、一个 Bridge、一个模拟器；按真实复用再抽库 |
| R12 | P1 | 最新产品蓝图未合并，main 仍是单 HC MVP；旧报告的 Verified 有明确范围 | 记录两个基线；运行代码事实与设计目标分开；不要将后续范围标成完成 |

R01/R02 是对上一轮扩展方案的修订，不据此断言现有 main 已经存在明文泄露漏洞。R03 是扩展约束，不是 AWP 当前双端设计的缺陷。

## 3. 关键文件的实施影响

### `platform/internal/harness/domain/model.go`

现有 `EndpointType.Valid()` 只接受 ABA、HC_WEB、HC_REFERENCE；不能仅在前端加一个 ESP 类型。引入通用 Provider 类型时，要同步服务端校验、Enrollment、Credential Scopes、Gateway 路由和数据库验证，但不允许这个类型进入 AWP 会话。

`Session{ABAEndpointID,HCEndpointID,...}` 保留。新增 Task/Run 到 Session 的映射，而不是改成任意端点列表后假装支持多人会话。

### `platform/internal/harness/gateway/session.go`

`authenticateHCRequest` 同时检查 HC 类型、Origin 和 scope。硬件不能伪造 Origin 来复用它；新的机器 Endpoint Enrollment 与 MDP 鉴权是独立入口。浏览器连接 MDP 仍必须校验 Origin/CSRF 语义。

### `protocol/proto/mss/awp/v1/wire.proto`

新增 device schema 放到新 namespace。旧枚举值、148 字节 AAD、序列空间、HPKE Package、ACK 与精确重放测试向量均不可重解释。

### `aba/adapters/deepseek_harness_acp.py`

当前真实适配器不是完整工具执行、审批和实时 token 代理。不能简单删除 MCP 拒绝语句：必须先建立本地 Toolset 白名单和真实 SDK 能力映射。Agent 不支持的 capability 明确禁用。对另一个真实 MCP-capable Agent 的验证，也不能替代当前 DeepSeek adapter 的兼容验证。

### `hc/`

保留现有 core；新增 Device Provider adapter 及任务 UI。仅将 UI 确实需要的内容解密，浏览器生命周期变化使某项 capability 不可用时必须更新 availability，而非维持一个虚假的 online=true。

## 4. 审阅后的范围排序

第一优先是一个真实可用的用户闭环：任务→真实 Agent→受限设备调用→Mosaico/模拟器呈现→可信用户交互→结果。不是再做数轮只验证连接和证书的里程碑。

安全、协议负向和断线测试作为每个产品切片的完成条件一起交付，而不是独立拖延产品设计的长期项目。

## 5. 当前不能宣称的状态

没有执行本轮 Go/Rust/HC 构建或测试，没有烧录 Mosaico，没有验证 BSP/HPKE C 库和完整安全存储。旧 PR 的验证只能作为旧提交的历史证据。新协议、数据库对象、MCP 工具和页面均为待实现设计。

# 组件、信任域与端到端技术架构

- 状态：Draft，2026-09-13。

## 1. 总体布局

```text
                  Platform / 不持设备业务明文
    ┌──────────────────────────────────────────────────┐
    │ Identity、Roles、Grant、Endpoint/Device Registry  │
    │ AWP Relay       MDP Relay       Ciphertext Store │
    │ 元数据审计、配额、在线租约、Task/Run路由元数据     │
    └─────────┬────────────────┬───────────────────────┘
         AWP密文           MDP密文
              │                │
   ┌──────────┴─────┐     ┌────┴──────────────────────┐
   │ HC / ABA       │     │ Web/App/Mosaico Provider  │
   │ 旧会话保留      │     │ 设备能力与受限交互          │
   └────────────────┘     └───────────────────────────┘

用户认可的可信执行区（电脑/服务器/容器，非Platform的隐式解密插件）
  ABA -- 本地ACP --> Agent Runtime -- 本地MCP --> Device Bridge
                         |
                  Runtime Interaction Adapter
                         |
              Run Coordinator / Approval Authority
```

Bridge、Interaction Adapter、Run Coordinator 是逻辑职责；首版允许同一 Rust companion 进程按模块实现，避免无必要微服务化。它与 ABA 独立进程/凭据，默认只出站 WSS；本地接口采用 stdio 或受 OS 权限保护的 Unix Socket。Windows 适配使用命名管道或受认证的 loopback，不暴露公网端口。

## 2. 各组件职责

| 组件 | 拥有 | 不拥有 |
| --- | --- | --- |
| Platform | 用户/RBAC、端点与设备绑定、元数据策略、加密路由、保留/配额、吊销 | 设备私钥、ACP SRK、Prompt、调用参数、音视频明文 |
| ABA | 现有密钥、Runtime/Workspace 白名单、ACP 生命周期、AWP/Journal | 驱动、Agent Loop、设备管理后台、全局业务策略 |
| Runtime Adapter | 特定 Agent API 到 ACP 的映射、本地许可 Toolset、原始权限请求映射 | 平台任意下发的可执行命令 |
| Device Bridge | MCP 工具、可信参数验证、加密 MDP、Invocation Journal、已配对设备密钥 | 任意租户权限、其他 Run 全量上下文、固件发布权限 |
| Interaction Adapter | 受限用户意图、任务状态投影、控制租约检查、ACP 请求关联 | 未批准自动接管、伪装人类审批 |
| Approval Authority | 原始待授权动作、一次性决策、动作摘要校验、审计凭据 | 模型传来的自我批准 |
| Provider | 本地驱动、能力状态、本地许可、呈现/采集、执行去重、安全停止 | 平台管理员权限、未经允许的通用 Shell |

## 3. 身份与设备模型

`Endpoint` 表示一个可独立撤销的安装实例。`Device` 表示可调度的逻辑资源；一个 Endpoint 可提供多个 Device。

不把每个麦克风和按钮都强行建成独立 Device：默认 Mosaico 是一个 Device，屏幕/声音/按钮是 Capabilities；独立所有权或生命周期的外接资源才拆 Device。

新增 Endpoint 元数据：`runtime_kind`、`client_family`、`reported_version`、`assurance_evidence`。Roles 如 `human_interface`、`device_provider`、`device_consumer`、`execution_host` 由授权记录发放。设备声明角色不能激活它；HC 的 device_provider 角色默认关闭，需要用户主动启用。

DeviceBinding 包含 `(tenant_id, device_id, endpoint_id, binding_epoch)`。重新安装、更换桥或重新认领导致 epoch 增长并撤回旧授权；硬件序列号仅是可选说明，不是身份。一个用户在同机运行 ABA 和 Bridge 时，二者仍是两个 Endpoint，避免共用私钥和生命周期。

## 4. 数据平面与控制平面

### 4.1 Platform 可见

随机 Device/Run/Invocation ID、所属空间、Endpoint、粗粒度许可类型、可选公开 capability 名称、协议/固件版本、连接租约、消息长度、收发时间、密文 hash、投递 ACK、限额和明确选择公开的运维摘要。

能力详情可能暴露工作习惯和设备位置，默认采用最小公开摘要；完整 schema、state、权限细节和 human content 加密给有权主体。Task 标题和普通设备名称也允许敏感模式，只在 HC 解密。

### 4.2 可信端点可见

Bridge 解析调用参数和结果，Provider 解析自己要执行的动作，HC 解析自己获授权的内容。需要结果转发给另一个端点时，为该接收方单独加密，不能复制一个群共享根密钥给全部设备。

Platform 依据 Grant 的资源/主体范围拦截；Bridge 和 Provider 对参数级约束再次强制执行。只有租户 RBAC 而无本地 owner 授权，不能接管设备。

## 5. Agent 调设备的真实路径

1. 设备配对，发布经本地驱动和用户配置允许的 Descriptor；Bridge 固定该 Device 绑定和密钥指纹。
2. 本地管理员为 Runtime Profile 配置许可的 Toolset ID；程序路径、连接地址及密钥只在本地配置。
3. Runtime Adapter 向 Agent 注册预定义 MCP 服务。远端 HC/Platform 只选择已授权的 Profile/Toolset ID，不能传 `command/args/env/url`。
4. Agent 调用如 `device_display_card`。Bridge 从本地启动上下文取得 actor/run/workspace，不信任模型传入的 userId、role 或 grant。
5. Bridge 校验 schema、设备许可、当前绑定/权限版本、参数范围和资源忙状态，为调用分配稳定 Invocation ID。
6. 调用参数及绑定信息端到端加密后通过 MDP 中继；Provider 验证来源、授权、期限、版本和本地许可，先记执行日志再动作。
7. Provider 回传端到端加密的状态/结果，Bridge 维护权威 Invocation 状态并返回 MCP 结果。
8. 长动作先返回 Invocation ID，Agent 用查询 Tool 获取结果；不靠把 MCP 连接无限挂起来模拟任务系统。

当前 DeepSeek Adapter 不具备这个接入，必须新增经过测试的本地 SDK Tool Binding；不能仅移除其 `mcpServers` 拒绝校验。第一阶段先用确定性 Test Agent 验证，再用真实 MCP-capable Runtime 验证，分别记录能力矩阵。

## 6. Mosaico 发任务与 ACP 的关系

Mosaico 不是完整 ACP 客户端，也不直接获得现有 HC Session SRK。它使用有限的交互消息：`interaction.submit`、`interaction.cancel_requested`、`interaction.choice`、`projection.update`。

已认证 HC 创建短期 `ActorLease`，绑定 Device、可信 Coordinator、Task/Run、允许的操作、有效期及用户身份保证级别。设备凭据证明“哪台设备”，ActorLease 证明“哪个用户近期授权这台设备做什么”，两者都校验。

Coordinator 校验控制租约后，通过本地 Runtime Adapter 创建或访问该 Run 的 ACP client。新设备发起任务先创建独立 Run；接管一个现有 Run 必须经显式控制权交接。不能打开第二个未经授权 ACP client 同时写同一会话。

运行在 HC 内的临时交互桥可用于开发，但 HC 关闭时标记不可用；不能伪装为后台持续运行。常驻能力由用户可信主机上的 companion 提供。

## 7. Task、Run、Session、Invocation 的边界

- Task：用户目标、内容授权和成果集合。
- Run：一次执行尝试，绑定 Runtime、Workspace、执行主体与控制租约。
- SessionBinding：Run 到当前 AWP/ACP Session 的关联，不改原 Session 表。
- Invocation：一次设备动作的稳定意图和结果。重投递不产生新 Invocation。
- Projection：发往特定 UI 终端的有限状态视图，有 revision、TTL 和接收方。
- Approval：授权一个准确动作；不等于 Task 完成，也不授予后续所有动作。

Run Coordinator 维护业务执行状态；Platform 保存已签名状态声明和路由元数据，不从 ACK/Socket 在线推断“任务已执行”。离线导致观测过期，不自动把 Run 判为失败或重启。

## 8. 能力描述与可用性

能力合约版本采用 `display.card.v1` 等固定名称。Descriptor 包含合约/schema digest、输入输出限制、副作用类型、是否可取消、超时、互斥组、数据敏感级别和本地 permission 状态。

不能只报 `camera: true`。状态应能表达 `supported`、`permission_required`、`foreground_only`、`busy`、`unavailable`。本地权限取消、浏览器挂起或硬件拔除时更新 revision；已发出的调用仍按对应执行状态处理，不能仅隐藏菜单。

模型可见 Tool 描述来自受控模板。第三方 Device Manifest 是不可信数据，不能直接拼入系统提示词或动态生成 shell。

## 9. 事件和投影

运维心跳走控制面。按钮、传感器变化、语音输入等业务事件走加密 MDP；描述规则由 Bridge 的用户配置决定。事件不会自动启动模型或产生新任务。

事件触发规则至少绑定 owner、Device、event type、模板版本、冷却时间、并发上限、预算和幂等 key。首版只允许显式用户触发；自动化放后续切片，避免传感器抖动导致大量 Agent Run。

MCP 可支持协议通知和资源订阅；本设计不声称它没有事件能力。但它不是全部端点互联和音频传输层，因此设备事件与大资源不强制全部经 MCP 消息承载。

## 10. 最小部署

Platform 沿既有 Go Thin Host/数据库发布，新增 Device Fabric 模块与独立 MDP 路由；现有 Gateway 可同二进制承载，但队列/限额/连接目录必须隔离。Bridge 作为用户侧独立进程，Mosaico 与 Web Provider 均出站连接。

不新增 Redis/NATS/MQTT 等强制依赖。首版声明单 MDP Gateway 实例；数据库 outbox 提供持久性。将来多实例必须有租约/连接 fencing 和跨实例投递，实现前不得仅增加 replicas。

暂不建设全局调度器。选择设备采用显式绑定；多设备时先按授权、有效绑定、能力可用性、用户指定顺序过滤，不以“最近”作为授权或私密内容投递依据。

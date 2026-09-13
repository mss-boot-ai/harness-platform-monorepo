# Device Fabric 技术设计 v0.1

- 日期：2026-09-13
- 状态：Draft / 完整实施设计提案；没有修改运行代码。
- 仓库：`mss-boot-ai/harness-platform-monorepo`
- main 基线：`1102cb0ba1d2d95112957b492908d1af7a41e2c0`
- 审阅的产品设计：`codex/product-design-cross-platform` @ `26c76a8888b4212d84d611f86d0a0e632dcf1a60`
- 本轮分支：`design/device-fabric-foundation`

## 一句话结论

保留安全 ACP 工作链路，把 HC 与 ESP-Mosaico 的可用能力统一起来，但不把会话、授权和物理执行混成一个万能 RPC；设备业务明文在用户认可的执行区处理，Platform 继续中继密文。

## 阅读顺序

| 文档 | 回答的问题 |
| --- | --- |
| [REVIEW.md](REVIEW.md) | 当前设计哪些成立，哪些存在冲突或实现缺口？ |
| [PRODUCT.md](PRODUCT.md) | 首版用户闭环、页面、非目标和成功标准是什么？ |
| [ARCHITECTURE.md](ARCHITECTURE.md) | 各组件怎么连接，Agent 实际如何调用设备？ |
| [PROTOCOL.md](PROTOCOL.md) | MDP 消息、编码、兼容、重放和状态机如何定义？ |
| [SECURITY.md](SECURITY.md) | 哪些主体能解密、授权、审批和执行？ |
| [DATA-API.md](DATA-API.md) | 表、索引、事务、HTTP 与 MCP 契约是什么？ |
| [CLIENTS.md](CLIENTS.md) | Web/App/小程序与 Mosaico 分别如何实现？ |
| [DELIVERY.md](DELIVERY.md) | 按什么切片开发，如何验证、部署和回滚？ |
| [ADR-0006](../adr/0006-device-fabric-and-trusted-execution.md) | 哪些关键取舍需要项目确认？ |

## 与旧文档的关系

`docs/product/PRD.md`、`docs/architecture/*` 和 ADR-0001～0005 仍描述已接受的 ACP 产品及安全基线。本目录补充新的产品领域，不把提案冒充已合并决策，不重命名 `acp-brige-agent`，不升级 mss-boot-admin 基线。

产品设计分支比 main 多一份 `docs/product/design-v1/PRODUCT-BLUEPRINT.md`。本次沿用其“Task→Run→Session”及 HC/Console 职责划分，不覆盖该分支，也不声称其页面已经实现。本提案从 main 新建，独立于该分支；未来合并时仅需协调文档入口和产品信息架构。

## 首个可交付闭环

```text
HC 发起任务
 -> 本地 ACP Agent 通过已许可 MCP Tool 调用
 -> Device Bridge 授权并加密
 -> Platform 中继
 -> 模拟器 / Web Provider / Mosaico 展示卡片
 -> 用户在终端发出受限交互
 -> 可信 Interaction Adapter 接收
 -> 同一任务呈现结果
```

先完成卡片、状态、取消请求和低风险交互，再完成真正审批，最后接按键语音与加密资源。调用结果不可伪造为执行成功；物理终端也不会自动成为完整 ACP Session 参与者。

## 已审阅的主要依据

基线代码：

- `platform/internal/harness/domain/model.go`：EndpointType、Session 双端绑定。
- `platform/internal/harness/gateway/session.go`：HC 身份、Origin、会话创建与 ABA 查找。
- `platform/internal/harness/gateway/{ack,resume}.go`：HC/ABA 方向约束。
- `protocol/proto/mss/awp/v1/wire.proto`：AWP 仅 ACP 载荷及专用方向。
- `aba/adapters/deepseek_harness_acp.py`：文本输入、MCP 注入拒绝、loadSession 限制。
- `docs/roadmap/IMPLEMENTATION.md` 与 PR #2：实现状态、验证范围与未完成项。
- `AGENT.md`、ADR-0001/0003/0004/0005：安全边界、Thin Host 和检查点纪律。

外部公开规范（2026-09-13 查阅，不以网页 latest 作为构建依赖）：

- ACP v1 Session Setup：<https://agentclientprotocol.com/protocol/v1/session-setup>
- MCP Tools：<https://modelcontextprotocol.io/specification/2026-07-28/server/tools>
- MCP Authorization：<https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization>
- HPKE：<https://www.rfc-editor.org/rfc/rfc9180.html>
- DPoP：<https://www.rfc-editor.org/rfc/rfc9449.html>
- JCS：<https://www.rfc-editor.org/rfc/rfc8785.html>
- ESP-Mosaico：<https://docs.espressif.com/projects/esp-dev-kits/en/latest/esp32s31/esp-mosaico/user_guide.html>

MCP 实现必须协商实际 SDK 和 Agent 支持的版本；引用现行规范不代表仓库已具备其全部能力。板卡 target、BSP、工具链必须在真机切片锁定，不凭聊天中的版本猜测构建。

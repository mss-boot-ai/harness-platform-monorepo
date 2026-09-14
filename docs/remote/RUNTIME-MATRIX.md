# Remote 真实运行时矩阵

日期：2026-09-14。此表严格区分运行时 API、当前适配器实现与实际验收。

| 运行时 | 已核对版本 | 当前状态 | 后续门槛 |
| --- | --- | --- | --- |
| DeepSeek Harness SDK/runtime-bin | 两者均为 `0.1.1rc1` | 实机 ACP 初始化/新会话通过；一次真实轮次出现 provider HTTP 504；旧 adapter 将非 completed 结果升级为进程退出 | 结构化失败、真流式事件、继续多轮、真实工具及支持的控制动作 |
| 确定性 ACP 测试程序 | 随仓库版本锁定 | 已有配置、审批、取消和故障回归；不是模型验收 | 继续覆盖当前目录/恢复契约 |
| 其他完整 Remote adapter | 未锁定、未安装、未验收 | 不宣称支持 | 若 DeepSeek API 无法满足必需控制，选择一个实际可用且明确锁定的运行时 |

已从部署 SDK 的公开 API 核对：`DeepSeekHarness.run` 支持 `on_notification`，返回 `RunResult` 的 finish_reason/events/notifications；`HarnessClient` 提供订阅、请求、通知和请求响应 API。当前 adapter 尚未把这些能力完整映射到 ACP，不能把底层 API 存在当成已验证支持。

模型服务的不可用、明确终止失败和未知执行结果须分别表示；不自动重跑用户原请求，不把虚构模型列表、usage 或取消确认用于填充 UI。发布验证只保存稳定错误类别、版本、计数和测试场景，不保存真实用户内容或凭据。

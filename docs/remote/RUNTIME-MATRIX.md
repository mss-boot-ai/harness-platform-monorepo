# Remote 真实运行时矩阵

日期：2026-09-15。此表严格区分运行时 API、当前适配器实现与实际验收。

| 运行时 | 已核对版本 | 当前状态 | 后续门槛 |
| --- | --- | --- | --- |
| DeepSeek Harness SDK/runtime-bin | 两者均为 `0.1.1rc1` | 实机 ACP 初始化/新会话通过；一次真实轮次出现 provider HTTP 504；旧 adapter 将非 completed 结果升级为进程退出 | 结构化失败、真流式事件、继续多轮、真实工具及支持的控制动作 |
| 确定性 ACP 测试程序 | 随仓库版本锁定 | 已有配置、审批、取消和故障回归；不是模型验收 | 继续覆盖当前目录/恢复契约 |
| Codex App Server | `openai-codex==0.147.0` 及其同版本 CLI runtime | ACP adapter 已提交；实际模型已通过三轮流式/上下文/项目文件读取。配置回执曾验证失败，已修正，完整控制复测中 | 显式审批、取消后继续、模型配置、当前完整版本端到端与部署验收 |

已从部署 SDK 的公开 API 核对：`DeepSeekHarness.run` 支持 `on_notification`，返回 `RunResult` 的 finish_reason/events/notifications；`HarnessClient` 提供订阅、请求、通知和请求响应 API。更新后的 adapter 映射流式事件和明确终止错误；4 项适配器回归通过。真实多轮尚待复测，不宣称支持人工审批或取消。

Codex 使用独立的本地 `HARNESS_CODEX_API_BASE_URL`、`HARNESS_CODEX_API_KEY`、`HARNESS_CODEX_MODEL` 和可选 `HARNESS_CODEX_MODELS`。不继承 DeepSeek provider 配置，不传输主机配置到 HC。模型选项取本地允许名单与固定运行时实际目录的交集。命令扩权只可拒绝；项目内的真实 file-change 审批必须包含完整有界 diff 并在批准时复查路径。当前固定 runtime 的只读模式不是目录级读隔离，运行于专用低权限 OS 账户；可写模式限制项目根且关闭额外网络与临时目录写入。未暴露任意 shell/路径配置入口。

运行时的 `thread/resume` 对已经加载的 Thread 返回原配置。适配器仅在空闲时解除订阅后恢复同一持久 Thread，并核对 model/provider/effort/sandbox/cwd 回执；无历史的新 Thread 使用确认过配置的新空 Thread。任何不确定回执都停止该 Run，不能谎称界面配置已生效。用户轮次不会因配置切换被重跑。

模型服务的不可用、明确终止失败和未知执行结果须分别表示；不自动重跑用户原请求，不把虚构模型列表、usage 或取消确认用于填充 UI。发布验证只保存稳定错误类别、版本、计数和测试场景，不保存真实用户内容或凭据。

Codex 适配使用公开 [App Server 协议](https://learn.chatgpt.com/docs/app-server) 与已安装版本的 schema 核对初始化、模型发现、轮次中断和审批；[官方 SDK 文档](https://learn.chatgpt.com/docs/codex-sdk)说明 Python 包包含固定的 CLI runtime。这里选择该运行时提供完整控制，保留 DeepSeek 作为独立能力受限路径。主机已有 MSS Responses 网关与 Codex runner，可复用现有配置；不会复制另一个用户/主机的 ChatGPT 登录状态。

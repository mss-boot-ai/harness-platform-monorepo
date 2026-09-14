# Remote 项目选择、运行恢复与真实运行时检查点

日期：2026-09-15 +08:00。状态：开发分支已实现并分层验证，尚未更新 dev-242，完整 Remote 未交付。

仓库 `mss-boot-ai/harness-platform-monorepo`，分支 `codex/remote-project-workspace-recovery`。原部署仍为 main `1227b392e664959c987865e14c55758a1c620952`。以下不是已部署结果，也不是最终跨设备验收。

## 当前实现

- ABA 当前连接绑定的显式 opt-in 执行目录，HC 真实项目与 Agent 选择；owner/tenant 校验、代次 fencing、到期拒绝、每 5 分钟续发。
- 加密 Conversation 索引与 Run 分离；每对话创建意图、草稿、目标、运行引用、重命名、归档和恢复。损坏记录隔离并保留，未知执行不自动重跑。
- 精确状态/操作查询、取消创建 tombstone、独立授权关闭失效 Run、保留历史继续新 Run；正常重新登录复用匹配的有效 Endpoint，吊销后显式创建新身份。
- HC 有界自动重连；正常服务端凭据到期关闭也会刷新授权并重连。本地断开/卸载会明确停止重试。
- ABA 异步启动、全局/Runtime 有界并发、本地目录 inode 独占锁。不同 ID 或 Runtime 不能绕过同一目录锁；不把 HC 界面互斥当成主机授权。
- DeepSeek 明确失败/流式适配及能力受限声明；固定 Codex App Server 0.147.0 实际流式、配置、一次审批、取消后继续。

## 已验证范围与版本

| 检查 | 源码/证据 | 结果与范围 |
| --- | --- | --- |
| HC lint、typecheck、test、build | `334b5ef063c663598865876e58742251dcdf23a0`；nvm Node 24、pnpm 10.34.5 | 25 文件、118 测试通过 |
| Thin Host 完整验证 | 同一 HC 检查点期间运行官方 `mss v1.3.7 verify --all` | 固定依赖/生成边界、Go build/test、后台 frontend build/lint/test 通过；未运行选装真机测试 |
| ABA test/clippy | `f899537bb6573ab7af0dfa67e5c143aebb5e320a`；Rust 1.88.0、locked、clippy -D warnings | 35 unit + 5 duplex + 1 supervisor 通过；包括目录续发取消与目录锁 |
| 实际 ABA/Gateway race 集成 | 测试修复源 `eeafd33cf59a06b9cb4757f69bab72970e733f97`，ABA 构建源 `334b5ef063c663598865876e58742251dcdf23a0` | 连续 5 次 HPKE、流式、配置、反向权限、ping、取消/继续、重连与原帧补传通过；确定性 ACP |
| Python adapter 回归 | `1e89707135fd1a856b166d02bc90539caf143f58` | 12 测试通过；真实配置回执、不确定配置 fencing、完整 diff 与一次授权、输出边界 |
| 真实模型/控制 | `6ea1d675db6066f2c8aee0b8c32165ed9ba91d5f`，专用低权限账户与临时项目，现有本地 Responses provider | 三轮流式、上下文追问、真实读取 README、配置确认、真实拒绝/批准文件修改、取消与后续轮次通过 |
| CI 生产 HC 浏览器 | [Remote Integration 34880760037](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34880760037)，源 `334b5ef063c663598865876e58742251dcdf23a0` | production-hc-browser job 通过：双会话、草稿、配置/审批/取消、刷新、原帧 replay、安装独占、吊销、Opaque Canary；不是整次 CI 全绿 |
| CI UI | [HC Chat UI 34880765560](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34880765560)，源 `ac15c2d15a6bd386660e86a0d00053bd0b74a158` | UI smoke job 通过 |

内置浏览器另在生产入口连接隔离 Admin/Gateway/ABA：两个具名项目分别发送成功，刷新恢复各自目标/历史，重命名/归档/恢复保留内容。两次主动断开测试连接后自动恢复，草稿保持未发送；本地 fixture 审计仍只有一次执行。随后项目 B 创建与回复成功，未影响项目 A 草稿。

真实模型测试输出仅保存状态与计数，不保存凭据、provider body 或业务源码。运行时以真实临时文件存在/内容核对批准结果；取消不声明回滚副作用。后续增强探针将进一步要求在实际 tool 已开始之后取消，并复核首次配置与配置后原上下文，不能把此前的早期取消结果冒充这些新条件。

## 失败记录

- 初始目录迁移新增后，旧固定 migration 断言失败；修复源 `d682a9517d2a135230f600a3bdc8ef56ad3fbfc2`。
- SQLite 并发元数据写锁错误；`ff33f57985e5437c3bdf5981ed69a46cd91b0475` 添加只针对已回滚元数据事务的有界重试，重复集成通过。
- Conversation 拆分后旧草稿断言指向 Run；`5ab61f134e1bd70a91237e6696d972bdee7e4868` 更新新持久索引断言后 118 测试通过。
- Codex 首次探针遇到固定版本 sandbox schema 不匹配；修复 `933a8177c5a2f049818f31ea4610dc39423b54c6`。之后真实配置回执仍显示旧 effort；`1e89707135fd1a856b166d02bc90539caf143f58` 在空闲时解除订阅/恢复并验证回执，后续真实控制通过。
- 初始 provider 配置不匹配且 SSH 一次中断；不作为模型成功证据。Codex 使用专用变量及本机已有 Codex runner 的本地 provider 配置，未改动已有 runner。
- 异步启动使旧测试的立即 reopen 断言失败；修复 `29b9f58b79a4e6ff33a22f64535c5f5e42c9e4dc`。clippy 参数数量失败修复 `ac15c2d15a6bd386660e86a0d00053bd0b74a158`。
- CI 34880760037 的 encrypted-duplex job 因旧 readiness 只查设备是否已登记，过早创建返回 ABA_OFFLINE；本地复现后 `eeafd33cf59a06b9cb4757f69bab72970e733f97` 改查当前 ready 目录并通过连续 5 次验证。新版 CI 34881667711 尚在运行。

## 尚未完成

ChatGPT 独立复核、coordinated dev-242 更新与备份/回滚演练、部署后原失败会话恢复、真实模型通过完整 HC 链路仍须取证。后续持久 Session Host/operation journal、跨进程恢复、独立设备 Attachment/控制租约、安全资源/成果/PWA/通知和最终跨端场景继续按 DELIVERY 矩阵推进，本检查点不替代这些交付项。

# 实施切片、验收、部署与回滚

- 状态：Draft，2026-09-13。
- 当前已完成的是设计，不是下表功能。

## 1. 交付原则

先交付可以演示的用户闭环，不先重构全部Endpoint/AWP、堆完整IoT后台或开发调度器。每个切片的安全和故障测试与产品功能一起完成，不安排无止境的独立验证阶段。

每次实施从最新main核对已合并ADR/分支状态；单一可恢复改动最小检查后commit并非强制push，再完整验证，失败以新提交修复。禁止main直推功能、force-push、破坏性rebase和覆盖现有产品设计分支。

## 2. 切片与依赖

| 切片 | 内容 | 用户可见结果 | 完成条件 |
| --- | --- | --- | --- |
| DF0 设计基线 | 本目录、ADR、安全边界、产品蓝图对齐 | 明确要做什么、不做什么 | 评审关键决定；代码仍不变 |
| DF1 基础互联 | 最小角色/设备/绑定/Grant，MDP加密路由，Rust模拟器 | HC可配对、查看模拟设备、撤销 | 注册→双向密文→receipt→掉线恢复；AWP旧向量不变 |
| DF2 Agent纵向闭环 | Bridge stdio MCP、本地Toolset、最小Task/Run映射、卡片/状态Tool | 真实Agent让模拟器/Web显示任务卡 | 一个确定性Test Agent及一个真实Runtime成功；无远端命令注入 |
| DF3 Web与Mosaico | HC Provider、BSP锁、C侧互操作、显示/触摸/状态、绑定UI | 相同Tool操作Web与实体终端 | 真实板端crypto/断线/重启/撤销；按端报告assurance |
| DF4 受限人类交互 | Interaction Adapter、ActorLease、Run控制租约、任务投影 | 从终端发许可任务/输入/取消 | 不支持的会话接管明确拒绝；控制权竞争处理 |
| DF5 审批 | 原ACP请求映射、Approval Authority、完整可信呈现、决策签名/CAS | 低风险跨端批准，高风险转HC | 重放/参数替换/超时/两端竞争/假人类决策全部拒绝 |
| DF6 资源与语音 | 加密资源、按键录音、可信STT/TTS、媒体配额 | 用Mosaico说话发任务并听结果 | 明确采集同意、取消不上传、资源授权与无明文canary |
| DF7 产品化 | 固件签名/回滚、适配更多HC端、组织共享、运维指标 | 可持续升级与管理 | 目标环境故障/恢复/升级证据 |

DF3的BSP和本地驱动诊断可与DF1/DF2并行；不需要等硬件到货才能完成Agent纵向闭环。不要把MCP排在全部设备功能之后。

资源调度、K8s agent池、自动事件触发、远程HTTP MCP、更多物理执行器和实时全双工语音在这些切片之后单独设计，不混进首版依赖。

## 3. 首批PR建议

1. `design/device-fabric-foundation`：仅文档。
2. `feat/device-fabric-registry`：最小领域/迁移/配对UI，特性默认关闭。
3. `feat/mdp-opaque-relay`：协议schema、固定向量、路由/inbox/outbox/receipt及模拟器。
4. `feat/device-mcp-vertical-slice`：可信Bridge、Test Agent、真实Runtime、本地Toolset与卡片闭环。
5. `feat/hc-device-provider`：Web运行形态/授权/前台状态投影。
6. `feat/mosaico-device-runtime`：固件与真机证据。
7. 交互、审批、媒体分别独立PR，不一次混入全部跨端状态机。

分支名为计划，不提前创建空实现分支。具体PR可按可审查工作量再拆，但不允许仅提交stub后宣称完整集成。

## 4. 回归和验收矩阵

| 组 | 场景 | 应有结果 |
| --- | --- | --- |
| AWP回归 | 旧HC/ABA注册、Prompt、ACK、原Frame重放、Close | 行为和既有AAD/向量不变 |
| 协议互操作 | Rust/Go/TS/C同向量；错版本/签名/AAD/长度 | 一致成功或fail closed |
| 权限隔离 | 跨owner/tenant、伪造role、旧grant/binding/boot | 不投递或不执行 |
| 可靠性 | 重复same ID、different payload、断网、网关重启 | 去重/冲突，不二次副作用 |
| 设备掉电 | DISPATCH_STARTED后掉电 | UNKNOWN，查询/人工确认而非自动重做 |
| 取消 | 开始前、执行中、完成后、cancel ACK丢失 | 明确requested/confirmed/unknown，无伪回滚 |
| 审批 | 参数变更、request进程重启、双端竞争、过期、模型伪造 | 无效拒绝；唯一有效决策 |
| 浏览器 | permission撤回、后台挂起、多tab竞争、关闭页面 | capability降级与正确错误，不虚假在线 |
| 隐私 | Prompt/参数/音频canary经过链路 | Platform日志/DB/object中无明文 |
| 背压 | 队列满、日志满、慢消费者 | 限额/拒绝，不先执行再丢记录 |
| 资源 | 任意URL、越权ref、错digest、截断/重放分块 | 拒绝，不SSRF、不拼接污染 |
| 真机 | USB首次刷写、显示/触摸、供电、重连、crypto | 记录板版本/固件/日志，不推断未测能力 |

## 5. 性能与容量目标

首版可复现测试环境先使用100个模拟Provider和1块Mosaico；实际成功规模以测量为准。测试默认控制packet/队列预算来自PROTOCOL，数据库容量按平均密文尺寸×速率×保留时长计算，不以连接数推断容量。

采集p50/p95配对耗时、MDP往返、投递积压、密文存储、Bridge Tool耗时、设备heap峰值、掉线重连时间和UNKNOWN比例。初步体验目标为健康网络下简单卡片操作p95≤1秒（不含模型调用），仅作为验收目标，不作为已达成性能宣传。

不收集敏感tool参数或Prompt用于指标标签，避免高基数ID导致监控资源爆炸。模板使用量、任务成果等产品指标仅用明确允许的元数据。

## 6. 发布与回滚

Flag至少分开：`device_fabric.enabled`、`device_fabric.mdp.enabled`、`hc.device_provider.enabled`、`device_fabric.interaction.enabled`、`device_fabric.approvals.enabled`、`device_fabric.media.enabled`。

首次数据库扩展只加新表/列和索引，旧代码继续可运行。先上线兼容Platform、再Bridge/模拟器、再Web、最后固件。启用每个flag前检查相应协议与授权版本。

回滚先禁新授权/新调用，再drain连接/等待或标记在途动作状态，最后回滚二进制/固件。不要drop表或清空UNKNOWN记录。确认操作已经开始的设备不能通过管理状态回滚而宣称物理效果已撤销。

多Gateway实例不是首版默认。扩大副本前验证持久generation/fencing、跨实例踢线、消息路由与数据库故障恢复。

## 7. 外部版本与待验证项

必须锁定：MCP SDK及协商协议版本、真实Agent版本/接入方式、ESP-IDF/BSP/组件/编译器、MCU HPKE与签名库、分区和可恢复存储。当前未作无证据版本猜测。

在正式实现PR中记录每项能力 supported/tested/unsupported，不从官方存在某规范推断目标Agent或板卡已支持。

## 8. 本轮实际状态

本轮通过GitHub连接器读取main、产品设计分支和关键代码，完成设计审阅与文档。未改运行代码，未部署，未测试数据库迁移，未执行Go/Rust/HC构建或测试，未烧录真机。容器直接git clone因无法解析github.com失败，后续读取/写入使用已连接GitHub工具；这不构成仓库构建失败。

提交/PR和远端读回状态在本轮最终交付记录中说明。仓库原有验证报告仍只对应原提交，本设计不得借其Verified标签宣称新功能已验证。

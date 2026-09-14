# Remote 产品交付入口

当前工作基于已合并的 `1227b392e664959c987865e14c55758a1c620952`，分支为 `codex/remote-project-workspace-recovery`。用户要求以开发阶段的统一契约完成产品流程。

问题证据和验收合同见 [2026-09-14 开发阶段补全](2026-09-14-DEV-PRODUCT-COMPLETION.md)，真实运行时状态见 [Runtime 矩阵](RUNTIME-MATRIX.md)。旧 C3 证据保留其确定性运行时及单浏览器范围。

当前依次交付：

1. ABA 发布当前连接绑定的有界项目/Runtime 目录；HC 选择真实名称和允许组合。
2. Conversation 与执行 Run 分离，完善创建核对、登录恢复、失败后继续、关闭、重命名与归档。
3. 真实运行时流式事件、明确失败映射、非阻塞启动及本地工作区执行互斥，并协调更新 dev-242 验证真实流程。
4. 持久 Session Host、跨进程恢复和 operation 去重。
5. 独立设备授权、Attachment 密钥和控制租约。
6. 资源、成果、移动/PWA 和完整 Remote 实际验收。

2026-09-15：报告故障的部署检查点已通过 Codex with ChatGPT 独立复核，按其已记录范围接受，不代表完整产品完成。当前开始[同端点持久 Host 契约](HOST-DURABILITY.md)的 H0–H5；部署保持已验收的 `b482117a406545e9e6a20a6d641d827b1688244d`，新 Host 候选尚未上线。

用户已补充授权：开发部署直接复用现有 admin，不创建新的账号。H1 已改为现有 `harness-aba` 身份下的服务内委派与实际 namespace 隔离，不新增 root 管理服务/账号，也不替换登录、密码或端点身份。真实 Codex 控制流程及确定性 Host 崩溃后进程范围核对已有检查点证据，见[验证报告](../roadmap/verification/2026-09-15-existing-account-host-isolation.md)；这不等于业务历史/操作恢复已经完成。

## 执行目录 v1

`POST /gateway/v1/catalog` 仅接受无浏览器 Origin 的 ABA DPoP 身份。请求包含字符串形式的 `connectionGeneration` 和 `catalog`，后者仅有 `version:1`、`runtimes:[{id,displayName}]`、`workspaces:[{id,displayName,runtimeIds}]`。每类最多 64 项；字段、标识、显示名称和关联均验证，拒绝未知字段。

Gateway 在当前连接互斥检查中持久化目录。目录内容散列是 revision，发布记录绑定 owner、tenant、ABA、connection generation，有效期最多 15 分钟。连接替换、离线、吊销、过期都不能继续作为新执行的可选目标。

`POST /gateway/v1/endpoints/abas` 按已认证 HC 的用户/租户列出设备及 `catalog`，其状态区分 ready、offline、not-published、stale、unavailable。设备名称与目录不构成执行授权，创建和 ABA 启动仍需独立准入检查。

ABA 本地配置通过 `publish_catalog = true` 明确允许发布上述元数据；默认关闭，关闭时发布空目录。首次连接与重连完成身份验证后发布，发布不会启动 Agent；修改配置后重新启动 ABA 生效。目录正文上限 12 KiB，HTTP 外层仍遵守已有 16 KiB 上限。

ABA 在同一有效连接内每 5 分钟续发目录。续发在单个独立、有界 HTTP 工作线程完成，不阻塞 Agent 输出和控制处理；连接退出立即取消后续续发，正在执行的 HTTP 受既有 20 秒超时约束。续发失败不放宽准入：目录到期后停止接受新执行，已有会话的传输保持独立。

迁移 `20260914010000` 新增 `harness_execution_catalogs`，通过既有迁移注册与 readiness 接入。HC 使用实际目录选择项目和 Agent，保存新对话的目标；已有运行保留原绑定。Gateway 创建前验证目录有效性和允许组合；先核对已存在的幂等记录，因此目标目录变化不会把原创建重试变成新的执行。完整浏览器/实机验收仍待 A/B/C 检查点联调。

## Conversation 与 Run 生命周期

独立的加密对话索引保存名称、归档、草稿、目标、运行引用和逐对话创建意图。原有运行快照继续拥有自己的密钥、序号、原帧 outbox 和请求映射。首次接入时将旧快照关联为单运行对话，保留原密钥、字节与游标；一次创建未确认不会阻止另一对话选择其他项目。

重新开始运行使用新的 Session ID，保留原对话 ID 与旧历史。发送和控制动作携带所见 Run ID，迟到的创建、取消、状态响应不能把原输入发送到替代运行。明确的 ACP 终止失败显示失败轮次并允许下一轮；未知结果和完整性失败仍须核对及结束旧运行。关闭/核对使用独立授权 API，不要求旧内容密钥可恢复。

用户可重命名、归档、恢复、复制消息与草稿、关闭失效运行并继续。归档不取消执行或释放工作区；删除历史之前需确认所有相关运行已结束。单份损坏记录保留并隔离，其他对话保持可访问；共享存储密钥缺失不触发静默重置。

关闭是明确的两阶段状态：保存幂等关闭意图进入 `DRAINING`，立即停止新消息准入；收到该 ABA 验证通过的清理回执后才成为 `CLOSED`。重复关闭、精确状态查询和 ABA 重连会补发待处理关闭，而非重放用户任务。H1 候选要求先关闭一次性启动 gate，确认整个已记录 cgroup 为空并持久记录，随后才释放工作区；仅持久关闭记录支持 `ALREADY_CLOSED`（控制接收方全零），不再用空内存 map 或 leader 退出代替清理证明。记录缺失/损坏、inode 不匹配或未确认清理继续 fenced/DRAINING。Platform 仍按原 Session 的 ABA/HC 绑定确认。关闭竞态中的已验证旧帧不会继续中继，也不会影响其他运行的连接。HC 在确认前保留原 Run、恢复记录和关闭操作 ID，不能把接收关闭请求描述为实际停机，更不表示撤销已有项目副作用。

已不确定的 Run 在 ABA 重连时只重复有签名的未知结果通知，不再声明正常 Resume/ACK 或补传业务帧。Gateway 对已验证、但属于已隔离状态的在途 ACK/帧不入库、不转发、不确认；不会因此断开承载其他 Run 的整个设备连接。迟到的未知结果通知不能把 `DRAINING` 改回 `UNCERTAIN`，关闭意图仍需完成确认。

前向迁移 `20260915010000` 为 Session 增加有界 `startup_failure_code`，默认空值，保留原记录。精确状态接口在启动失败时返回 `startupFailureCode`，仅允许固定的本地准入错误：项目占用、Runtime/资源占用、启动失败/超时和本地策略拒绝等。未知 Agent 文本归一为 `AGENT_START_FAILED`，不进入公开元数据。生命周期目录锁包含空闲时段；这不是多空闲上下文调度器。

HC 将所有未结束的 Run（含空闲、归档和关闭待确认）计为项目占用，展示当前端有权识别的占用对话及导航。释放必须显式确认结束原 Run；新对话的草稿保持未发送，只有执行端确认清理后才允许用户再次发送。新运行是全新的 Agent 上下文，旧对话历史并非自动重放的上下文。缺少本地内容记录时可通过已授权的精确运行状态关闭，而不绕过原 HC Endpoint 的授权。

创建与恢复使用端点范围的精确查询：`POST /gateway/v1/sessions/{id}/status` 返回该 HC 的会话状态，`POST /gateway/v1/session-operations/{key}` 查询原创建结果。查询不到不被解释为“从未执行”。`POST /gateway/v1/session-operations/{key}/cancel` 与原创建共享唯一操作键：先取消则留下终态记录，延迟创建被拒绝；已经创建则返回原 Session，由用户的关闭动作处理。重复取消不会生成另一份操作，取消有审计记录。

## 浏览器身份恢复

重新登录使用新的人类会话与新的持有证明，在两把公钥、owner、tenant 和现有有效 HC 身份一致时复用 Endpoint ID，签发新的短期凭据。事务内再次检查吊销与绑定，吊销不能被重新登录撤销。

已吊销身份可经明确的界面动作登记新的浏览器身份。IndexedDB v4 只增加活动身份引用，旧身份行、信任 pin 和加密内容均保留；替换使用 compare-and-swap 检查，不清空历史或重用旧授权。新身份仍须正常登录注册，不能据此读取旧 Endpoint 的受限会话。

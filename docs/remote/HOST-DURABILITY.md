# 同端点持久 Host 契约

日期：2026-09-15。状态：H1 有界进程隔离、固定 provider 出口及 scope 恢复检查点已通过独立复核，尚未部署；H2–H5 未完成。证据见[现有账号隔离报告](../roadmap/verification/2026-09-15-existing-account-host-isolation.md)。

本阶段落实 ADR-0007，不改变既有 AWP v1 的 AAD、签名、Nonce、方向和原始帧重放含义。当前已验收部署 `b482117a406545e9e6a20a6d641d827b1688244d` 保持运行；只有新候选完整验证后才协调更新。

## 权威与范围

Host 是唯一的业务执行状态中心，位于 `aba/src/host/`。本阶段可与 ABA 共用服务生命周期，因此 ABA 进程退出就是 Host 重启，不宣称运行时透明存活。

Platform 只负责当前用户/端点授权、最小路由元数据及密文投递。ABA transport 负责身份、加解密、帧序号和原字节补传。Host 独立服务运行时、提交业务状态/事件、判断恢复资格和维护进程/工作区所有权。HC 是加密缓存及界面，不是动作是否执行的权威。

本阶段仅同 Endpoint 恢复。独立设备 Grant/Attachment、writer lease、安全资源和 PWA/通知仍为后续门槛。保留保守的整个 Run 生命周期工作区互斥，不同时引入多空闲上下文调度。

## 不同标识不能混用

| 标识 | 含义 |
| --- | --- |
| Conversation ID | 长期对话标识，独立于任何一次执行 |
| Run ID | 绑定本地目标、所有者和一次执行上下文 |
| RuntimeSession ID | 真实运行时会话，仅由可信 Host/adapter 记录和恢复 |
| operationId | 随机稳定业务操作标识；HC 在加密前保存，重复操作不产生新标识 |
| JSON-RPC ID | 当前请求/响应关联，不作为永久业务去重键 |
| AWP message ID/sequence | 传输身份及方向序号，不替代 operationId |
| Host/runtime epoch | 主机启动及运行时替换的不同代次 |
| Event revision | 每个 Run 的连续业务事件序号，独立于帧序号 |
| Process scope ID | 持久的本地进程包含范围及其启动身份，不是未经验证的 PID |

## 接受、执行与恢复

操作按验证后的规范表示计算摘要；相同 endpoint/Run/operationId 与相同摘要返回已有状态，相同 ID 不同内容冲突。Prompt、配置、审批决定与取消均适用，不能只为创建会话实现幂等。

顺序固定为：验证和授权 → 事务提交 accepted → 提交 dispatch-started → 仅提交一次给运行时 → 事务提交结果/事件/发布意图 → 发布已提交内容。保存失败不得继续执行。审批决定先提交后交付；交付状态未知时不能重新批准或借新 epoch 重用。

| 持久状态 | 重启后的处理 |
| --- | --- |
| 未提交接受 | 不报告已接受，不从推测状态派发 |
| 已接受且可证明未派发 | 保留原操作；仅在当前授权、所有权及恢复检查通过后派发 |
| 已开始但无持久终态 | UNKNOWN，不自动重新提交 |
| 终态及事件已提交 | 返回已记录结果，不再调用模型或工具 |
| 丢失 epoch 的待审批 | 不可操作，不授予替代运行时权限 |
| 关闭已请求、清理未确认 | 保留所有权并继续 fenced/cleanup-pending |
| 清理已确认 | 允许明确授权的替代 Run |

已加载历史不证明外部副作用成功；管道 write 成功也不是业务终态。取消 Turn、后台工具结束、关闭 Runtime 和确认清理是四种不同事实。

## H1：可核验的进程范围与事件归属

不得以 leader 已退出或内存 map 为空证明所有后代结束。清理结果必须显式返回，不能由 `Drop` 的执行或发出信号推断。TERM、强制停止及确认检查均有界，且不能阻塞其他运行的 I/O；未确认时保留目录所有权并拒绝成功关闭回执。

目标主机已只读核对为 cgroup v2、systemd 249、Linux 5.15 和 bubblewrap 0.6.1。每 Run 独立 cgroup 管理后代，包括改变进程组的进程。记录范围随机身份、boot ID、cgroup inode 和受限本地 profile/工作区绑定，再允许运行时工作；重启先核对此范围，绝不向未经验证、可能已复用的 PID 发破坏性指令。

需要特权的包含/查询操作仅由 purpose-specific 本地组件执行：没有公网接口，校验调用者 OS 身份，只接受固定操作与不透明 Run/profile ID，真实程序/参数/cwd/env 取 root/owner 管理的本地配置。不能成为任意 root 命令代理。运行时 UID/文件可见范围与 Host key 分离；目录 mode 0700 不是对同 UID 子进程的隔离证明。此部署接缝先在隔离环境验证，不能直接改动正在运行的版本。

### 2026-09-15 用户补充：不新增账号

用户授权开发环境部署/权限变更，但明确要求直接复用现有 admin，不创建新的账号。平台 admin 登录和密码、端点身份保持原样；主机不存在 OS admin，继续使用现有 `harness-aba` 服务身份，不创建第二个运行账号，也不修改无关的 port-forward 身份。

优先使用现有 ABA systemd 单元的 cgroup v2 delegation，由 ABA 在**自己的已委派子树**内管理每 Run 范围，无需新增 root 管理服务或授权通用 systemctl。可信启动助手必须先进入已记录范围，再启动 Agent。bubblewrap 只挂载所选工作区、只读运行时和独立 runtime HOME，使用新的 PID/mount/user/IPC/UTS/cgroup namespace；不暴露 Host 状态、密钥、宿主 `/proc`、`/run` 控制接口或可写 cgroup。新 UID 不是必要条件，实际 namespace 和句柄隔离证据是必要条件。

关闭使用整个已验证范围的 `cgroup.kill`，再确认 `cgroup.events` 的 populated=0；leader 退出不提前返回。清理或持久提交失败继续占用工作区。范围记录及关闭 tombstone 保留以处理重启/丢失回执，缺失记录不等于已关闭。该 H1 元数据登记不是 H2 的业务操作存储，不宣称已经具备完整持久 Host。

H1 scope registry v2 在启动前记录原始 delegation root。相同 boot 下，只能在该原位置核对；将同一 state directory 改配到另一服务 root 必须非破坏性地报显式迁移/核对需要，不能根据新 root 的缺失记录确认关闭，也不能越权操作旧 root。确定的新 boot 可按旧进程已不存在的规则退休，但不推断业务成功。历史 v1 或缺失位置绑定的记录一律保留并拒绝自动升级/猜测位置；开发验证使用新目录显式初始化，不能覆盖旧 registry。已持久关闭但尚未删除的已知空 cgroup，仅在位置、inode、shut gate 与空范围同时确认后继续退休。

socketpair 使用正向域/类型/协议白名单：仅 AF_UNIX、protocol=0、SOCK_STREAM/SOCK_SEQPACKET，以及明确的 CLOEXEC/NONBLOCK flags。其余类型（含 SOCK_RAW/DGRAM）、域、协议与 flags 均拒绝；保留真实嵌套 bwrap 所需的私有已连接管道，而非“除了 datagram 都允许”。

Adapter 保留有界的 runtime thread/turn/item 映射。A 结束后开始 B，A 的迟到工具结果仍只更新 A；没有可信归属时进入有界 unknown/诊断状态，不套用当前 awaiting，也不把 Turn 结束当成所有工具成功。

## H2：一个事务持久化边界

采用本地事务存储，依赖在实现前锁定兼容的正式版本。Run、Operation、Turn、Approval、Event、publication intent、cleanup 和 retirement 记录的相关转换必须同事务提交。现有 frame journal 不充当业务历史；剩余跨存储边界需要明确恢复协议，不能让 HC 已 ACK 的结果被 Host 忘记。

敏感请求、结果、事件、配置和审批内容在进入数据库/WAL 前密封。Host wrapping key 独立于 SRK，置于运行时无法读取的位置，也不能进入其环境或工作区。丢 key、损坏、无法 sync/commit 时只读或停止新写，不静默创建替代 key。

限制 Run/操作/事件数量、单记录和总字节、队列及页大小；为终态、UNKNOWN 和清理保留容量。压缩保留 active/consumed/retired 的去重证据；删除或归档历史不能重新开放旧操作执行。迁移幂等且前向，保留旧 ciphertext、message ID、序号预留和 UNKNOWN 记录。旧 frame-only 数据不得升级为“已知完成”的业务事实。

## H3：独立执行引擎

网络连接重试、Agent I/O 和 Host 存储各自连续、有界地服务。Gateway 离线不停止事件吸收。已授权任务继续产生的内容先落 Host；容量耗尽时按契约停止/隔离，不能丢弃未记录输出再报告 completed。

通过明确协商的加密 Host operation contract 传递业务操作；所有绑定来自已认证通道和持久 Run，而非模型文本。当前 AWP 编码和 crypto 输入保持原语义。Host 是运行时及业务事件唯一 owner，不能在 ControlState 再维护一份可独立改变的 Run 状态。

## H4：快照与恢复

Snapshot 表示准确 revision R；后续从 R+1 连续读取，无订阅空窗。有界分页、稳定快照身份、revision 去重与缺口检查是协议要求。已压缩范围返回 HISTORY_GAP，并显式转向新快照，不能跳过事件。

HC 将 applied revision 与投影同本地提交，再 ACK。快照替换保持正在编辑的草稿和选中会话；恢复消息、工具、原审批 epoch 和 effective config 必须来自同一边界。UNKNOWN 和 cleanup-pending 不能因刷新消失。

恢复原通道必须能证明持久 key package、接收者、generation、cursors、原 outbox 与当前授权一致。中断预留/过期 key/缺失材料不得以重置计数或重新加密旧序号修复。旧通道不可用时，仅同 Endpoint 可申请明确协商的只读 recovery purpose；不启动 Agent，不把 UNKNOWN 改回 ACTIVE，只交付新的加密状态/历史快照。任何必要协议/schema 增量单独版本化并用固定工具生成。

Codex 当前 `loadSession: false` 保持诚实。只有保存了本地 runtime 身份、工作区及版本绑定，且可证明 idle/已清理的记录，才允许 Host 驱动的 fresh-process resume。HC 不能提交任意 runtime session/path。须以真实新进程、上下文保留且没有重放旧 Prompt 的测试证明后再宣布支持。

## H5：验收与发布

必须覆盖：接受前、接受后未派发、派发后无结果、工具效果已发生但结果未提交、提交后发布前、审批提交后断线、Gateway 离线持续输出、快照并发事件、leader 退出后后代存活/换组、存储满/损坏/key 丢失、Nonce 预留缺包、压缩/retirement 后旧操作再投递。

保留现有 HC/Go/Rust/adapter/crypto/Thin Host 回归。确定性故障程序与真实模型分别记录，跳过测试不算通过。真实验收要经过 HC→Gateway→ABA/Host→runtime，并在目标服务限制下证明：离线记录、符合资格的 idle 新进程恢复、在途失败保留 UNKNOWN、迟到工具归属、确认清理后显式复用工作区。

新候选发布前记录完整 source/component hashes、迁移、私有备份与前向修复边界；发布后重验关键场景。不得清空历史、Nonce、UNKNOWN 或扩大成无约束权限来使测试通过。

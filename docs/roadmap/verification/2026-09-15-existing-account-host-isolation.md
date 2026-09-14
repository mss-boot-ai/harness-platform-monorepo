# 复用现有账号的 Host 隔离检查点

日期：2026-09-15。状态：H1 部分已实现并在 dev-242 验证；未部署到现有 ABA 服务。H2–H5 与完整 Remote 未完成。

## 用户约束与边界

本轮用户授权继续开发部署，但要求直接复用 admin，不创建新账号。只读核对确认 admin 是现有平台账号；主机没有 OS admin，已有 `harness-aba`（UID/GID 995）及 `harness-port-forward`（UID/GID 994）。没有新增或替换应用账号、OS 用户、密码、ABA/HC 身份或登录凭据。

采用现有 ABA 服务的 cgroup v2 delegation，未新增 root RPC 管理服务，未授予 sudo 或通用 systemd 管理能力。Root 部署身份仅用于安装候选测试产物、创建限定测试目录及独立的短期验证单元。原有 `harness-aba.service` 在已完成的验证前后保持 MainPID `43042`、InvocationID `f900bb5e2ee0451a93f9e2fdd87ac51c`、User `harness-aba`、Delegate `no`。

当前正式运行的报告问题修复仍是 `b482117a406545e9e6a20a6d641d827b1688244d`，其公共 HC 验收见[原部署报告](2026-09-15-dev-242-remote-recovery.md)。本轮没有替换其镜像、二进制、配置、数据库或 PVC，也没有清空旧对话、UNKNOWN、nonce 或 journal。

## 源码与实现

仓库：`mss-boot-ai/harness-platform-monorepo`。分支：`codex/remote-project-workspace-recovery`。所有检查点均先最小检查、commit、普通 push，再验证；未 amend、force-push 或自动合并。

- `6c36545fe128e8aa87936b69f18e6cd9b7880f5b`：将 H0 约束改为复用现有身份，优先服务内 delegation。
- `acefbb5425125fc6ed5deae8182b5ed7bb1fbd20`：进程范围登记、显式清理回执与异步关闭骨架。
- `8d1360061f75f799d90e216ce59ff738d3ffc45b`：Host/runs 分层、资源预留、一次性启动 gate、禁止网络回退。
- `0d9890aee6119da2f4db4e21e0b3e7e286b1a12e`：严格 provider 策略通过真实回复和文件读取；修复原生 `client_metadata` 被拒绝的问题。
- `ecb70c4610330eb32a10790e0c97183793159a63`：通过真实审批、配置、实际命令取消和继续场景；加入辅助监听器失效检测。

`aba/src/process/supervision.rs` 保留随机 scope、boot/device/inode、profile/工作区绑定和关闭 tombstone。持久 claim 在启动前提交；gate 独占且一次消费。关闭先阻止晚启动，再强制清理并检查整个 cgroup，最后持久记录并释放目录锁。未知记录、未确认清理或提交失败不返回“已经关闭”。这些是进程所有权元数据，不是 H2 的业务事务日志。

Host 位于委派树的 host 叶节点；运行时位于 runs 子树。运行时总量仅使用外层内存预算的 75%，预留 64 个 task 给 Host；默认单 Run 内存 1 GiB、96 task。外层实际测试为 MemoryMax=4G、TasksMax=256、NoNewPrivileges、空 capabilities、ProtectSystem=strict、私有临时目录/设备，保留 Codex 嵌套沙箱需要的 AF_NETLINK。

运行时使用 private mount/PID/user/IPC/UTS/cgroup/network namespace；只挂载选定工作区、只读 runtime、自己的 HOME。真实 provider key 只在外层可信代理中；沙箱仅收到非秘密占位值。实际运行时看不到代理 Unix socket；只通过命名空间内 loopback 调用固定 HTTPS provider。没有任意 CONNECT、目标地址、重定向或客户端 header 覆盖。模型、调用数量、输出预算、工具种类及输入引用有受限策略；附加 `client_metadata` 被剥离。

HTTP 头/body 有绝对接收期限；上游使用可取消 future、120 秒总期限且禁用 POST 自动重试。下游断开会释放请求与并发许可。seccomp 拒绝 Unix socket 创建、可重新寻址的 datagram socketpair、keyring/ptrace/process-memory/pidfd-getfd/io_uring；为真实嵌套 bwrap 保留已连接 stream/seqpacket 私有管道，仍需独立验证这些例外。

## 已完成证据

验证工具：`deploy/systemd/verify-delegated-scope.py` 与受控 fixture。它只查找现有用户；比较验证前后的账号清单、正式服务 PID/InvocationID，且拒绝覆盖既有测试目标。

| 检查点 | 实际验证范围 |
| --- | --- |
| `8d1360061f75f799d90e216ce59ff738d3ffc45b` | 48 个 Rust 单测 + 6 个集成测试；17 项初始真实内核检查。初版 UID/服务存活断言较弱，后续已改为精确 UID 与 PID/InvocationID 对比。 |
| `0d9890aee6119da2f4db4e21e0b3e7e286b1a12e` | 56 个 Rust 单测 + 6 个集成测试、clippy all-targets `-D warnings`、16 个 Python adapter 测试；21 项严格 provider-enabled 隔离检查；真实模型回复和文件读取。 |
| `ecb70c4610330eb32a10790e0c97183793159a63` | Rust 测试及 clippy 通过；同样的 21 项隔离检查；真实 Codex 两轮回复/读取、一次拒绝且文件不存在、一次允许且文件内容准确、确认 effort=low、观察到实际 sleep 进程后取消、同会话继续成功、完整 scope 清理。 |
| `ca0f35b4ab4154cc48151a89406efef636360f11` | 24 项真实内核/进程事实通过：进一步验证 stream/seqpacket pair 在 peer 关闭前后仍不能连接工作区控制 socket；真实 SIGKILL 测试 Host 主进程后，在新的进程/服务 invocation 只核对并清理旧 scope，未启动 runtime；真实 Codex 完整控制流程再次通过。 |

`ecb70c...` 目标主机二进制 SHA-256：`b0f7785cd8151f975a7095515edfe7d9ef77fb34ae8abcf145eb7cab080dae4e`。主机为 Linux `5.15.0-170-generic`、systemd `249.11`、bubblewrap `0.6.1`；Codex SDK/CLI 仍固定 `0.147.0`。

负向用例包括：Host 私有文件/端点 key 路径、宿主 proc、控制总线、可写 cgroup、宿主 loopback/private TCP、abstract/pathname Unix socket、可重新寻址的 datagram pair、跨边界文件描述符；TERM-resistant/setsid 后台子进程在 scope 关闭后停止。协议单测还覆盖重复启动 gate、丢失关闭回执、inode 不匹配、未空范围、提交失败、未知记录、慢速字节投递、实际 header 重建与不跟随重定向、连续四次下游中断后许可回收。

实际拒绝/批准只针对新建测试工作区的单个合成文件；未给真实工程文件授予自动修改权限。上述取消确认是 Turn 取消，不宣称撤销已发生的工具效果；完整子进程清理证据来自后续 scope 关闭。

原始命令输出留在本次本地验证目录和目标主机各候选测试目录；通过 Codex with ChatGPT 的执行记录发布的仅为对应检查点的安全输出。没有向 ChatGPT 粘贴代码、diff、原始请求、凭据或真实用户内容。

## 失败与修复

`136109fcd17da2cfbac294bb708c760f03e8dd2b` 的真实 Codex 测试启动失败：旧 adapter 强制 HTTPS，不接受命名空间内本地 relay，且最初测试指向已部署旧 adapter。`663072325843a199185c36e460a5e42212ab3e67` 以候选 adapter、固定本地 transport 模式及非秘密占位值修复；对外仍严格 HTTPS，真实回复和读取通过。

`b45be63f8b7e0e900f7d9b453243cd70968032c3` 加强请求白名单后，本地测试/隔离检查通过，但真实模型请求被拒绝。后续检查点仅增加有上限的固定字段/类别/指纹诊断，不记录任意内容。确认额外字段为原生 `client_metadata` 后，在 `0d9890...` 转发前剥离，完整重验通过；未扩大成任意参数透传。失败日志与修复 SHA 均保留。

## 尚未建立的结论

本报告不把尚未完成的独立双 scope 故障、所有启动/重启切点、HC 公共入口升级验收、H2 sealed 事务存储、H3 离线执行服务、H4 同端点快照恢复、跨设备 Grant/lease、资源和 PWA 标为完成。新的 Host-death/私有连接管道检查正在独立检查点推进，不能沿用以上 SHA 的通过结果。

补充：`ca0f35b...` 已完成上述特定 Host-death 和私有连接管道检查，其二进制 SHA-256 为 `9c810fc3ed18dd52f28af286c41930d29e36a78ca7f3e0a51c6b652125a91c9c`。这证明了进程范围登记/清理恢复，不证明旧业务输出、审批或未知操作可以恢复。双 scope 存活与组合回归在后续检查点继续。

本轮依据 [OpenAI 配置文档](https://learn.chatgpt.com/docs/config-file/config-reference)、[cgroup v2](https://www.kernel.org/doc/html/v5.15/admin-guide/cgroup-v2.html) 和 [seccomp](https://www.kernel.org/doc/html/v5.15/userspace-api/seccomp_filter.html) 核对接口，再以已锁定运行时及目标主机实际验证；文档不是实测替代品。

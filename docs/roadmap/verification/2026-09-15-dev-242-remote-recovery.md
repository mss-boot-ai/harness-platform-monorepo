# dev-242 Remote 恢复检查点上线验收

日期：2026-09-15 +08:00。状态：报告问题的恢复检查点已部署并验收，完整 Remote 仍在实施。

仓库 `mss-boot-ai/harness-platform-monorepo`，工作分支 `codex/remote-project-workspace-recovery`。

## 发布边界

- 原部署：main `1227b392e664959c987865e14c55758a1c620952`。
- 当前运行代码：`b482117a406545e9e6a20a6d641d827b1688244d`（`fix(deploy): permit the runtime sandbox network namespace bootstrap`）。API、Admin Web、HC、Gateway 镜像和 ABA 均绑定此完整 SHA。
- 后续部署防漂移检查：`3b17ad31de3f1e20f7c0d3689acce73431e84ddb`；仅部署脚本/说明变化，未冒充新的已部署应用版本。
- 当前新执行使用固定 Codex App Server/CLI `0.147.0`，模型为本机已经配置并实际验收的 `gpt-5.6-luna`。旧 DeepSeek 身份、运行记录、适配器及原配置备份保留；新项目白名单采用 Codex。
- 没有合并本分支，也没有把此检查点当作完整 Remote 完成。

## 已验证的代码与运行时

HC 的 124 个测试、lint/typecheck/build 通过；真实 ABA/Gateway race 测试增加 Agent 进程失败后的 ABA 重连与确认关闭，连续 3 次通过。此前 42 个 Rust 测试与严格 Clippy、官方 `mss v1.3.7 verify --all` 通过。具体早期失败和修复见[前一检查点](2026-09-15-remote-project-recovery.md)。

最终发布 SHA 的 [Remote Integration 34892932995](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34892932995) 两个 job 均成功，[HC Chat UI 34892937928](https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34892937928) 成功。这些 CI 中的 ACP 是确定性程序，不作为真实模型证明。

真实模型另通过固定 Python adapter 探针和实际 ABA `runtime probe`。按目标 systemd 限制运行的完整控制探针也通过：专用账户、NoNewPrivileges、ProtectSystem=strict、ProtectHome、PrivateTmp/Devices、空 capability 集、限定写目录和地址族均保留。

## 部署中的问题与处理

1. 原 systemd 地址族白名单缺少 `AF_NETLINK`，Codex 内部沙箱无法初始化隔离网络命名空间的 loopback。独立 bwrap 检查复现 `NETLINK_ROUTE` socket 错误；只增加该必需地址族后检查及完整真实工具探针成功，没有删除其他权限限制。
2. 保留现场既有 Redis-backed 应用配置和 Secret 引用；没有重启或替换 Redis，也没有重新生成用户密码、Admin 签名材料或数据库凭据。
3. 一个旧 HC ConfigMap 挂载覆盖了镜像内 Nginx 配置。差异仅为缺少 Trusted Types CSP。移除该挂载、保留旧 ConfigMap 和备份后，实际配置恢复为发布镜像内容；HTTPS/HSTS/CSP 校验通过。脚本增加防覆盖预检，避免后续仅看镜像标签而遗漏有效配置。
4. HC Pod 重新滚动后出现一次短暂 Ingress 503；后续完整检查通过，没有将这次瞬时失败记为成功。

## 数据、身份与回滚边界

私有备份包含原数据库 custom-format dump、工作负载/Secret/配置清单、Gateway 签名状态、主机状态和原四个应用镜像，并完成归档可读性与校验和检查。切换前再次停止 ABA 并保存静止主机状态。没有执行数据库回灌、密钥重建、浏览器清空或未知用户任务重放。

Gateway root/online 私钥在主机内比较一致；只报告比较结果，不输出私钥。信任发布 revision 从 6 增至 7。ABA 两把身份私钥比较一致。

| 存储 | 原 UID 与当前 UID |
| --- | --- |
| TimescaleDB PVC | `478f94a3-4d63-463c-a84a-630e6aa69d95`，保持一致 |
| Gateway trust PVC | `0a4b7932-e55a-40c5-88fc-3355b474aec7`，保持一致 |

前向迁移 `20260914010000`、`20260915010000` 已应用。既有两个失效 DeepSeek 运行保留为原 `UNCERTAIN`/`REKEY_REQUIRED` 状态，没有通过删记录或改成成功来消除问题。

浏览器进入新索引后采用前向修复策略；不能把镜像回退理解为可安全回退浏览器记录、Nonce 或会话状态。备份保留作受控人工恢复与审计使用，未宣称做过生产数据库恢复或任意旧版降级验收。

## 旧 main 浏览器数据升级

内置浏览器在隔离真实 Admin/Gateway/ABA 上先运行旧 main HC：一次实际 fixture 进程退出形成失败/不确定历史；另一项目错误创建保留未确认意图和草稿。刷新后旧状态仍存在。

随后只更换 HC 构建，保留同源浏览器数据与原身份：新索引恢复两份对话；可取消原创建、保留原草稿、选择具名项目/Agent；可确认关闭旧失败运行并在原对话创建新 Run，旧历史仍在。新对话选择已占用的同一项目时显示占用者，显式释放后才允许再次发送。

该长时间场景还发现并修复不确定 Run 在 ABA 重连时发送正常 ACK/Resume 导致整台设备反复断线的问题（`e49eeac1b752bd1a28ff9465458cebb7bf5da83f`），以及迟到关闭提示和重复能力读取竞态。没有清空数据或把旧用户消息自动重跑。

## 已部署 HC 的真实验收

通过既有 HTTPS HC 入口、正常账户流程和内置浏览器完成：

- 已吊销浏览器身份拒绝恢复；明确登记新独立身份后正常登录，旧身份记录不被复活或删除。
- 具名选择 Harness Platform 与独立验收工作区，不输入底层 ID。
- 项目 A 首轮标记、原上下文追问、真实读取仓库 README；仓库工作区仍干净。
- 项目 B 发起真实文件修改；刷新后原待审批请求与加密草稿恢复。
- 拒绝第一次修改，主机确认目标文件不存在；再次请求并核对完整范围，明确批准后确认文件及精确内容落盘。
- 推理强度从 medium 改为 low，界面显示执行端确认后的配置 revision；并非先改 UI 值。
- 观察实际 `sleep` 工具开始后停止本轮，收到运行时 cancelled 回执；随后同一对话新轮回复成功。
- 最后明确关闭两个验收 Run，数据库都为 `CLOSED`，主机仅剩 ABA 主进程。测试历史保留，容量释放；没有关闭不属于这些验收的旧运行。

### 内容不透明检查

仅在主机内流式扫描所选平台数据与日志，输出计数/布尔结果，不保存或发布其正文。两份验收标记及其常见编码未出现：

| 表面 | 扫描字节 | 明文标记 |
| --- | ---: | --- |
| 数据库数据导出流 | 4,718,558 | 未发现 |
| API 日志 | 149,821 | 未发现 |
| Gateway 日志 | 62 | 未发现 |
| ABA 服务日志 | 10,248 | 未发现 |

这些证据证明所测标记未泄露，不等于任意数据/故障组合的全面审计。

## 仍需继续的完整产品工作

持久 Session Host、operation 去重与快照/事件连续性、独立设备 Attachment/控制租约、安全资源与成果、PWA/通知以及最终跨端故障场景尚未交付。

另记录真实运行时的边界：取消已验证的是 Turn 中断与后续对话，不宣称回滚已发生的效果或终止所有后台命令。取消后某些工具卡仍保留最后的“执行中”状态；需要补全迟到/后台工具状态的归属与投影。独立进程树/子进程在异常退出时的完整清理仍需在后续 Host 故障矩阵单独验证，不能由本轮正常关闭测试推断。

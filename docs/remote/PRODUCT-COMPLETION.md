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

## 执行目录 v1

`POST /gateway/v1/catalog` 仅接受无浏览器 Origin 的 ABA DPoP 身份。请求包含字符串形式的 `connectionGeneration` 和 `catalog`，后者仅有 `version:1`、`runtimes:[{id,displayName}]`、`workspaces:[{id,displayName,runtimeIds}]`。每类最多 64 项；字段、标识、显示名称和关联均验证，拒绝未知字段。

Gateway 在当前连接互斥检查中持久化目录。目录内容散列是 revision，发布记录绑定 owner、tenant、ABA、connection generation，有效期最多 15 分钟。连接替换、离线、吊销、过期都不能继续作为新执行的可选目标。

`POST /gateway/v1/endpoints/abas` 按已认证 HC 的用户/租户列出设备及 `catalog`，其状态区分 ready、offline、not-published、stale、unavailable。设备名称与目录不构成执行授权，创建和 ABA 启动仍需独立准入检查。

ABA 本地配置通过 `publish_catalog = true` 明确允许发布上述元数据；默认关闭，关闭时发布空目录。首次连接与重连完成身份验证后发布，发布不会启动 Agent；修改配置后重新启动 ABA 生效。目录正文上限 12 KiB，HTTP 外层仍遵守已有 16 KiB 上限。

迁移 `20260914010000` 新增 `harness_execution_catalogs`，通过既有迁移注册与 readiness 接入。此检查点只提供目录后端基础；ABA 发布、HC 选择与新建校验后续接入后才能标记功能完成。

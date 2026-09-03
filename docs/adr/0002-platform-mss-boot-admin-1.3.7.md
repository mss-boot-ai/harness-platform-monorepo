# ADR-0002：Platform 固定基于 mss-boot-admin v1.3.7

- **状态**：Accepted
- **日期**：2026-09-03
- **决策所有者**：Harness Platform 项目
- **取代**：无
- **被取代**：无

## 背景

Harness Platform 需要成熟的用户、角色、菜单、服务端 Session、审计、通知、任务调度、配置、数据库和 Web 管理端。如果从空白服务重建这些能力，会扩大范围、重复维护并推迟 ACP 核心能力。

mss-boot-admin 已具备所需后台基础。为保证构建可重现、避免上游 `main` 漂移和开发过程中的隐式兼容变化，需要锁定一个精确版本，而不只记录模糊的“基于最新版”。

## 决定

Platform 固定基于：

```text
repository:  mss-boot-io/mss-boot-admin
tag:         v1.3.7
tag object:  41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:          1.26.6
license:     MIT
```

禁止使用浮动 `main`、`latest` 或未固定容器标签作为 Platform 源码基线。

首选把精确上游源树以 vendored/subtree-style 导入 monorepo 的 `platform/`，保留 License，并在 `platform/.upstream/mss-boot-admin.lock.yaml` 记录来源、Tag Object、Peeled Commit、Tree 和导入方式。

## 复用范围

优先复用：

- 用户、租户/组织扩展点、角色、菜单和 Action 权限。
- Browser Human Authentication 与持久化服务端 Session。
- 请求时加载当前 Principal/Role 的模式。
- WebSocket 单次 Ticket、Origin 和当前 Session 重新验证的安全模式。
- 审计、通知、任务 Server、数据库、缓存、配置和前端框架。

需要独立 ACP 实现：

- ABA/HC Endpoint Principal、AEC/HEC 和 DPoP Token。
- ACP Ticket Record/Namespace。
- Binary AWP Gateway、Connection Fencing、ACK/Replay 和 Backpressure。
- Endpoint/Session/Key/Frame 数据模型。
- Opaque Encryption 和 Key Package。

现有通知 WebSocket Hub 不直接改造成 ACP 数据面。

## 选项

### 选项 A：从空白 Go 服务开发 Platform

拒绝。重复实现用户、权限、Session、审计、任务和后台界面，成本和风险高。

### 选项 B：运行时依赖 mss-boot-admin 浮动 main

拒绝。构建不可重现，上游变化可能静默改变认证、数据库和前端行为。

### 选项 C：Git Submodule 作为默认方式

不选为首选。Submodule 可以保持来源清晰，但增加 clone/init、离线构建、统一修改和 CI 复杂度。

### 选项 D：精确源树 vendored/subtree-style

接受。每次 checkout 包含完整可修改源码，仍通过锁文件和独立导入提交保持来源可追溯。

## 理由

- 直接复用成熟能力，集中精力实现 ACP 安全与可靠性。
- 精确 Tag Object 和 Peeled Commit 防止标签或分支歧义。
- 单仓库内可原子修改 Platform、Protocol、ABA 和 HC。
- 独立上游导入提交便于未来比较和升级。
- MIT License 允许复制和修改，但必须保留版权和许可文本。

## 后果

正面：

- Platform 初始能力完整。
- 认证、Session、任务和前端不用另造体系。
- 版本来源可证明。

成本：

- Monorepo 体积变大。
- 后续安全补丁需要维护 Upstream Delta。
- 每次升级必须处理本项目对 Platform 的修改冲突。
- Go 工具链需要满足上游 1.26.6 基线。

## 安全与隐私影响

- 复用 Human Session 不表示复用普通 Bearer Token 作为 ABA 身份。
- ACP Endpoint Middleware 必须重新读取当前 Endpoint/Credential/Policy 状态。
- ACP Ticket 在现有安全模式上增加 Endpoint、Credential、DPoP 和 Purpose 绑定。
- Platform Root/UCA 私钥仍通过独立 KMS/Signer 管理，不能写入上游普通配置。

## 兼容与迁移

初始导入应为单独提交，只包含上游源树和许可，不混入 ACP 业务改动。随后提交上游锁、Build Info 和 ACP 模块。

升级流程：

1. 新 ADR 提议目标 Tag/Commit。
2. 计算上游旧/新 Diff 和本项目 Delta。
3. 在独立同步分支导入。
4. 解决冲突并记录。
5. 执行数据库、API、前端、认证、浏览器和回滚验证。
6. 经审查合并；不移动旧锁的历史记录。

## 验证

- Git/API 解析 `v1.3.7` 得到精确 Tag Object 和 Peeled Commit。
- 导入源树的 Tree/关键文件与锁匹配。
- `go.mod` 基线显示 Go 1.26.6。
- License 被保留。
- Platform 版本接口和制品 Build Info 输出上游标签和 SHA。
- 构建不访问浮动上游分支。
- ACP 模块测试确认 Human Principal 与 Endpoint Principal 分离。

## 参考

- `docs/architecture/PLATFORM.md`
- `docs/references.md`
- `docs/roadmap/VERIFICATION.md`

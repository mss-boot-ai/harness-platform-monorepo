# ADR-0004：Platform 使用 mss-boot-admin v1.3.7 Thin Host import 模式

- **状态**：Accepted
- **日期**：2026-09-04
- **取代**：ADR-0002 中的 vendored/subtree-style 引入决定

## 背景

`mss-boot-admin v1.3.7` 已把业务应用定义为 Thin Host：后端导入 `github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7`，前端导入 `@mss-boot-io/admin-web@1.3.7`。业务仓库只拥有组合入口、业务模块、配置、数据库前向迁移、业务页面和测试。

此前把完整 Foundation 源码复制到 `platform/`，虽然固定了版本，但会导致：

- 每次上游升级都需要合并大量无关源文件；
- 难以区分 Foundation 与 Harness 业务所有权；
- 失去 `mss upgrade admin` 的三方升级和业务文件保留能力；
- 本仓库 CI、依赖扫描和 Review 面积显著扩大；
- 容易直接修改核心认证、RBAC、Session 和 Admin Web，而不是使用正式扩展接缝。

## 决定

Platform 必须是由官方 `mss v1.3.7` 工具生成的 Thin Host，并固定以下公共依赖：

```text
backend:  github.com/mss-boot-io/mss-boot-admin/admin v1.3.7
frontend: @mss-boot-io/admin-web 1.3.7
```

仓库结构：

```text
platform/
├── .mss/                         # Thin Host 项目、锁和 Blueprint Manifest
├── cmd/server/main.go            # Admin 组合入口
├── internal/modules/             # Harness 业务模块
│   ├── all/                      # 生成模块组合
│   ├── custom/modules.go         # 手写模块显式注册
│   └── registry.go               # 受管组合层
├── web/                          # 导入完整 Admin Web 的业务扩展层
│   └── src/business/             # 路由、页面、本地化和注册
├── config/                       # 环境配置意图；不保存秘密
├── go.mod / go.sum
└── package/lock/build files
```

强制规则：

1. 不得把 Foundation 的 `admin/`、`mss-boot/`、`templates/`、`web/antd-v6/` 源码复制到 Thin Host。
2. `go.mod` 不得使用本地 `replace` 指向 Foundation 源码或相邻目录。
3. 前端必须依赖官方 npmjs 包，不使用本地 tarball、GitHub Packages 临时包或复制的 SPA。
4. 后端业务模块只能通过 `business.Module` 编译期注册；禁止包初始化发现和运行时插件装载。
5. 手写业务后端位于 `platform/internal/modules/<name>/`，由 `internal/modules/custom/modules.go` 显式注册。
6. 手写业务前端位于 `platform/web/src/business/`，通过业务路由、服务路径投影和双语 Locale 注册。
7. 后端权限始终是权威；前端菜单和按钮隐藏不能授权请求。
8. 数据库变更必须是业务模块拥有的显式前向 Migration，不依赖生产 `AutoMigrate`。
9. `.mss/blueprint-manifest.json` 必须保留，升级不得伪造或从其他仓库复制。
10. 上游升级使用目标版本的 `mss upgrade admin`：先只读计划，再显式 apply，最后要求 no-op 计划并执行完整回归。

## Harness 模块边界

Platform Thin Host 复用 Admin 的：

- 用户、角色、菜单和服务端 Session；
- 后端鉴权中间件与当前 Principal；
- 配置、数据库、审计、通知和任务服务器；
- 完整 Admin Web Shell、主题、国际化和构建工具。

Harness 自有模块实现：

- ABA/HC Endpoint、Enrollment、Credential、DPoP 与吊销；
- ACP Session、Runtime/Workspace 授权、密文 Frame、ACK/Replay；
- 独立 ACP Binary Gateway；
- 轮换、保留、健康扫描和安全审计；
- Harness 业务管理页面与 HC 页面。

## 迁移现有分支

当前开发分支上的 vendored `platform/` 由官方 `mss v1.3.7` 重新生成的 Thin Host 原子替换。替换提交只负责所有权边界，不混入 Harness 业务实现。替换完成并 push 后，才运行依赖安装、构建和测试；失败以新提交修复，不改写历史。

## 后果

正面：

- Harness 代码面显著缩小；
- 能使用正式升级工具持续跟进 mss-boot-admin；
- Foundation 与业务所有权清晰；
- 后端和前端都使用正式发布制品；
- 业务文件在三方升级中可保留。

成本：

- 构建依赖公共 Go Module 与 npmjs 可用性；
- 必须遵守 Thin Host 生成/受管文件边界；
- 扩展点不足时需先向 Foundation 补充正式接口，不能直接复制核心源码规避。

## 验证

- `platform/.mss/project.yaml` 表示 Thin Host；
- `platform/.mss/blueprint-manifest.json` 存在；
- `go.mod` 精确依赖 Admin v1.3.7，且不存在 Foundation 本地 replace；
- `web/package.json` 精确依赖 Admin Web 1.3.7；
- `platform/admin`、`platform/mss-boot`、`platform/templates` 不存在；
- `go test ./...`、前端 lint/test/build 和 `mss verify --all` 实际通过；
- `mss upgrade admin v1.3.7` 最终计划无 create/update/delete/conflict。

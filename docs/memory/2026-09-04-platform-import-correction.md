# 2026-09-04 Platform 引入方式纠正

## 用户纠正

`mss-boot-admin 1.3.7` 支持前后端 import 后进行业务开发，并通过 Thin Host Blueprint 与 `mss upgrade admin` 保持后续升级。因此，Harness Platform 不应复制完整 Foundation 源码。

## 权威结论

- Platform 仍固定 `mss-boot-admin v1.3.7`；
- 后端改为导入 `github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7`；
- 前端改为导入 `@mss-boot-io/admin-web@1.3.7`；
- `platform/` 是官方工具生成的 Thin Host，只拥有 Harness 业务；
- 旧 vendored/subtree-style 决定被 ADR-0004 取代；
- 删除复制进仓库的 Foundation 源码和对应导入/血缘校验脚本；
- 保留准确版本、Tag Object、Peeled Commit 和发布身份作为供应链证据；
- 每次升级使用官方生成器的三方升级，不手工覆盖业务文件。

## 执行顺序

1. 在现有开发分支提交本纠正、ADR、Platform 架构和生成工作流并 push；
2. 使用官方 `mss v1.3.7` 从空目录生成 Thin Host；
3. 原子替换当前 vendored `platform/`，形成独立提交并 push；
4. 再执行 import 合同、后端、前端和升级 no-op 验证；
5. 修复以新提交 push 后重测；
6. 在 Thin Host 上继续实现完整 MVP；
7. 全部实际验证后创建 PR，禁止自动合并。

## 状态

本文件只记录设计纠正。只有远端出现 Thin Host 替换提交并通过验证后，才能声明迁移完成。

# ADR-0002：Platform 固定基于 mss-boot-admin v1.3.7

- **状态**：Superseded
- **日期**：2026-09-03
- **取代者**：ADR-0004

## 原决定

本 ADR 最初正确地固定了 `mss-boot-admin v1.3.7` 的版本身份，但错误选择了把完整 Foundation 源码复制到本仓库 `platform/` 的 vendored/subtree-style 方案。

固定版本信息仍有效：

```text
repository:  mss-boot-io/mss-boot-admin
tag:         v1.3.7
tag object:  41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go module:   github.com/mss-boot-io/mss-boot-admin/admin@v1.3.7
Admin Web:   @mss-boot-io/admin-web@1.3.7
Go:          1.26.6
```

## 被纠正的内容

`mss-boot-admin 1.3.7` 已提供可升级的 Thin Host 模型：业务仓库通过 Go Module 导入完整 Admin 后端，通过 npm 包导入完整 Admin Web，只保留业务模块、配置、测试和组合胶水。复制 Foundation 源码会失去该模型的升级优势，并扩大本仓库维护面。

因此，本 ADR 不再作为引入方式依据。后续以 ADR-0004 为准；任何文档中关于把完整 mss-boot-admin 源码复制到 `platform/` 的描述均由 ADR-0004 覆盖。

# Repository scripts

- `check-docs.sh`：检查文档索引、术语和基线文件。
- `check-protocol.sh`：编译 AWP v1 Schema 并检查协议边界。
- `verify-platform-import.sh`：验证 `platform/` 是 mss-boot-admin v1.3.7 Thin Host，检查后端/前端精确依赖、Blueprint Manifest、禁止 vendored Foundation 与本地 replace。

Platform 创建与升级由官方 `mss v1.3.7` 工具负责。仓库不再提供复制完整 mss-boot-admin 源码的脚本。

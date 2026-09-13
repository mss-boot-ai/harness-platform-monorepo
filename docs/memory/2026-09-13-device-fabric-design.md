# 2026-09-13 Device Fabric 设计审阅记录

## 用户范围

用户要求review当前设计、完善并制定完整技术方案，明确暂时完全不考虑Anthropic MHS。已购买ESP-Mosaico作为物理终端；实际板卡修订和真机能力尚未验收。

## 读取基线

- main：`1102cb0ba1d2d95112957b492908d1af7a41e2c0`。
- 产品设计分支：`codex/product-design-cross-platform`，`26c76a8888b4212d84d611f86d0a0e632dcf1a60`；相对main仅增加PRODUCT-BLUEPRINT文档，本次未覆盖或合并该分支。
- 工作分支：从main新建`design/device-fabric-foundation`。

## 提案结论

保留AWP/ACP、ABA本地边界、Thin Host及默认Opaque；新增MDP和Device Fabric。Endpoint不等于Device，角色授权不等于能力声明。业务明文在用户可信Companion/Bridge处理，Platform只处理密文和元数据。模型不拥有批准权限；Mosaico默认为低权限共享终端。

现有DeepSeek adapter文本限定、拒绝远端MCP且不支持loadSession。Toolset必须本地许可。实体终端主动发任务采用明确授权的Headless HC/Coordinator-owned Run，不分享现有HC私钥或假装完成Session无缝接管。

## 交付位置

`docs/device-fabric/README.md`为入口，包含REVIEW、PRODUCT、ARCHITECTURE、INTEGRATION、PROTOCOL、SECURITY、DATA-API、CLIENTS、DELIVERY；ADR-0006为Draft。旧Accepted ADR及已运行代码不变。

## 验证边界

本轮为静态审阅与设计文档变更。没有运行新的应用构建/测试、Migration、浏览器或真机实验；不把旧PR验证状态沿用到新设计。GitHub提交与PR状态以最终交付记录为准，不在文档内自引用尚未产生的commit SHA。

# ABA 设备授权（Enrollment）

- 版本：v0.1
- 状态：Partial impl
- 界面：本机启动 ABA 后，用户在已认证 HC 上核对设备码并批准该设备，Platform 用 ABA KEM 公钥封装下发凭据。
- 使用者：最终用户 + 运维
- 关联文档：[`../../architecture/ARCHITECTURE.md`](../../architecture/ARCHITECTURE.md) §6.1、[`../../architecture/SECURITY.md`](../../architecture/SECURITY.md)、[`../../architecture/ABA.md`](../../architecture/ABA.md)、ADR-0005、D-012/D-013/D-014
- 关联代码：`platform/internal/harness/enrollment`、`platform/internal/harness/registration`、`aba/src/identity/{enrollment,keystore}.rs`
- 已实现部分：Platform 侧 Enrollment / Credential 域模型与显式前向 Migration；契约测试覆盖唯一索引与状态失败关闭；ABA 侧已具备 `identity/keystore` 与 `identity/enrollment` 模块骨架。
- 未实现部分：ABA 完整 Enrollment 与 Connector、进入 READY 的真实链路；凭据下发后 TRUST/证书缓存的落地；二次认证的 Assurance 策略执行；设备码超期与重发的完整交互。
- 非目标：不定义新的注册 API 路径或状态取值；不替代 `docs/architecture/` 中的流程定义；不描述生产部署与证书签发中心实现。
- 首次建立：2026-09-15
- 最后更新：2026-09-15

## 1. 界面目标

设备授权是用户第一次把一个本地 ABA 纳入信任的动作，也是唯一一次用户需要「肉眼比对」的环节。原型要确认：

- 设备码如何呈现，比对动作是否足够明确；
- 终端侧与 HC 侧的信息是否对齐（用户要在两屏之间核对）；
- 拒绝、过期、吊销三种负向结局是否都有清晰出口；
- 用户是否被误导为「需要抄写或保管密钥」（不需要）。

## 2. 布局与信息层级

| 区域 | 内容 |
| --- | --- |
| ABA 终端 | 状态行 + 显著设备码 + 有效期 + 提示语；不打印凭据或密钥 |
| HC 待授权列表 | 每设备一行：名称、类型、平台、首次出现时间、截断指纹、状态 |
| HC 设备详情 | 设备码输入/比对区、有效期倒计时、批准/拒绝/稍后 |
| 二次确认 | 独立弹窗，不提供「记住并跳过」 |

## 3. 关键状态与流转

见原型 §4 状态机表。要点：

- 只有 `READY` 之后 ABA 才能被选择建立 Session。
- `EXPIRED` 与 `DENIED` 都**不写入任何凭据**，可重新发起。
- `REVOKED` 必须撤销 Token/Ticket、关闭连接、禁止新 Key Package 并触发 Rekey（D-031）。

## 4. 安全相关表达

- **私钥生命周期**：原型显式标注 E-SIG / E-KEM 在本地生成且不可导出，Platform 不参与生成、不接收私钥（D-013、D-014）。
- **凭据封装**：凭据用目标 ABA 的 KEM 公钥封装，因此设备码泄露也不能直接取得可用凭据。
- **设备码**：短期有效，服务端只保存哈希；原型不展示明文存储痕迹。
- **列表层约束**：无「全部批准」；必须逐字符核对；指纹只显示截断值。
- **二次确认**：新增 Endpoint 为敏感操作，按 Assurance 策略要求二次 Human Auth（`HC.md` §9）。
- **日志边界**：终端与页面均不显示 Token、Ticket、证书完整值、指纹完整值或密钥材料。

## 5. 与文档的一致性

| 文档要求 | 原型落点 |
| --- | --- |
| `ARCHITECTURE.md` §6.1 流程时序 | 原型 §1 七步流程 |
| Enrollment Code 只短期有效并以哈希保存 | 原型 §1 第 3 步、§3 有效期条 |
| 最终凭据用 ABA KEM 公钥保护 | 原型 §1 第 6 步、§4 APPROVED 行 |
| D-012 每 Endpoint 独立密钥与凭据 | 原型 §3 设备详情、§4 状态机 |
| D-013 Platform 不生成/接收端点私钥 | 原型 §2 终端输出、§5 不可见清单 |
| `HC.md` §9 Assurance 与二次认证 | 原型 §3 二次确认条 |

## 6. 待确认问题

1. 设备码用 8 位（`K7F2-9QDA`）还是更短的分组？需要在可用性与暴力猜测之间取舍，待安全评审。
2. HC 与 ABA 在同一台机器时，是否提供剪贴板/本地回环比对的便捷路径？需确认不引入新的本地入参通道。
3. 设备码超期后是 ABA 自动重新发起，还是必须人工触发？涉及日志噪声与误授权风险。
4. 移动端是否能承担二次认证？取决于后续 Passkey/原生端计划（`HC.md` §6.3）。

## 7. 版本记录

| 版本 | 日期 | 变更 | 触发文档 / 决策 |
| --- | --- | --- | --- |
| v0.1 | 2026-09-15 | 首次建立：七步流程、终端输出、HC 授权页、状态机、信任边界 | `docs/architecture/ARCHITECTURE.md` §6.1、ADR-0005 |

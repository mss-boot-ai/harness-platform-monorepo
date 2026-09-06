# Architecture Decision Records

本目录保存 Harness Platform 的重大架构、安全、协议和上游基线决策。

## 规则

- 文件名：`NNNN-short-title.md`。
- 状态：`Proposed`、`Accepted`、`Superseded`、`Rejected`。
- 已接受 ADR 不原地改写历史结论；需要改变时新增 ADR，并在旧文件中标记 `Superseded by ADR-NNNN`。
- ADR 应记录背景、决定、选项、理由、后果、安全与兼容影响、迁移和验证要求。
- 接受 ADR 后同步 `docs/memory/decisions.md`、PRD、主架构和相关实现文档。
- 不把临时代码实现、聊天讨论或未验证假设当作 ADR。

## 当前 ADR

- [ADR-0001：Platform、ABA 与 HC 三角色架构](0001-platform-aba-hc-architecture.md)
- [ADR-0002：Platform 固定基于 mss-boot-admin v1.3.7](0002-platform-mss-boot-admin-1.3.7.md)
- [ADR-0003：默认 Opaque 加密与独立 AWP](0003-opaque-encryption-and-awp.md)
- [ADR-0004：Platform 使用 mss-boot-admin v1.3.7 Thin Host import 模式](0004-platform-thin-host-import-mode.md)
- [ADR-0005：HC 注册使用 Admin Browser Session 边界](0005-hc-registration-admin-session-boundary.md)

## 模板

```markdown
# ADR-NNNN: Title

- Status:
- Date:
- Decision owners:
- Supersedes:
- Superseded by:

## Context

## Decision

## Options considered

## Rationale

## Consequences

## Security and privacy impact

## Compatibility and migration

## Verification

## References
```

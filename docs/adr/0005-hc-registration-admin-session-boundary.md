# ADR-0005：HC 注册使用 Admin Browser Session 边界

- **状态**：Accepted
- **日期**：2026-09-04
- **决策所有者**：Harness Platform
- **取代**：MVP PRD 中把 HC Challenge/Register 列在 `/gateway/v1/*` 的路径决定

## 背景

HC Web 首次注册必须同时证明当前 Human Identity 与本地 Endpoint Signing Key 的持有。Platform 固定复用 mss-boot-admin v1.3.7 的安全 Browser Session；该上游实现把 HttpOnly `mss_admin_session` Cookie 的 Path 固定为 `/admin/api`，并用绑定的 CSRF Cookie、可信 Origin 和当前 Principal 保护不安全方法。

若 HC 注册接口继续位于 `/gateway/v1/hc/challenges` 和 `/gateway/v1/hc/endpoints`：

- 浏览器不会向该 Path 发送 Admin Browser Session Cookie；
- 复制或放宽 Cookie Path 需要修改 Foundation 核心，违反 Thin Host 边界；
- 让 Gateway 接受 Admin Bearer/Cookie 会混淆 Human 与 Endpoint Principal；
- 使用浏览器可读长期 Admin Token 会退回已被上游移除的不安全模式。

## 决定

HC Web 的 Human-bound 注册接口挂载在 Thin Host 已保护的 Admin API：

```text
POST /admin/api/harness/v1/hc/challenges
POST /admin/api/harness/v1/hc/endpoints
```

两条接口必须同时满足：

- 当前 mss-boot-admin Browser Session 有效；
- 当前 Principal/tenant 有 `harness:operate` 权限；
- 可信 Origin 与 Admin Browser CSRF 校验通过；
- 请求体大小、未知字段、挑战有效期和单次消费失败关闭；
- 浏览器使用本地不可导出 E-SIG 对包含挑战、Origin、Signing/KEM JKT 和注册上下文的固定 Transcript 签名；
- Platform 重新计算 JKT 并验证持有证明，不能相信客户端提交的 Thumbprint。

注册成功后，Human Session 不再作为 Endpoint 数据面凭据：

```text
/admin/api/harness/v1/hc/*   Human Browser Session + CSRF + Endpoint possession proof
/gateway/v1/*                Endpoint Access Token + DPoP + Server Nonce
```

Gateway 不接受 Admin Cookie 或 Admin Bearer Token。Admin API 不接受 Endpoint Token 替代 Human 注册、审批或管理授权。

Web HC 的短期 Access Token 只驻留内存。Refresh Credential 使用仅限 refresh Path 的 HttpOnly Cookie，并继续绑定 Endpoint DPoP Key；不得写入 Local Storage。H5 本地开发通过同源开发代理访问 `/admin/api` 和 `/gateway/v1`，部署时由同一可信站点反向代理，不依赖宽泛 CORS。

## 考虑过的选项

1. 扩大 Admin Cookie Path 到 `/`：拒绝；需要修改 Foundation 认证核心，并把高权限 Cookie 暴露给更多路径。
2. 让 Gateway 接受 Admin Cookie：拒绝；破坏 Human/Endpoint Principal 隔离。
3. 向 H5 返回浏览器可读 Admin Bearer Token：拒绝；扩大 XSS 后 Token 直接外带风险。
4. 为注册建立独立 OIDC：未来可用，但 MVP 会重复登录体系且不需要。
5. 在受保护 Admin API 完成注册后切换 Endpoint DPoP：采用。

## 理由

该方案完全复用 v1.3.7 已验证的 Cookie Path、Origin、CSRF 和当前授权能力，同时保持 Gateway 为独立 Endpoint Principal 数据面。路径表达真实身份边界，不需要复制 Foundation 或降低 Cookie 安全策略。

## 后果

- PRD、MVP 架构和 HC 文档中的两个注册路径需要更新；
- H5 必须与 Admin API 同源，或通过受控同源代理开发；
- Harness Admin 权限表新增两条 `harness:operate` 路由；
- 注册响应和日志必须 `Cache-Control: no-store`，不得记录 Token、Challenge 原值或公钥持有签名；
- Endpoint 注册之后的 Ticket、Session、Key Package、Frame 和 ACK 全部只走 Gateway 身份。

## 安全与隐私影响

正面影响：不扩大 Admin Cookie Path；不向 JavaScript 暴露 Admin Token；不让 Gateway 混用 Human 身份；注册仍验证 Endpoint 私钥持有。

剩余风险：同源 H5 的 XSS 可以调用 Human-bound 注册接口并使用当前页面可用的 Endpoint Key。仍需 CSP、Trusted Types、依赖锁、短挑战、用户可见端点审计和吊销；DPoP 不能消除同一执行上下文中的 XSS。

## 兼容与迁移

尚未发布或实现旧 `/gateway/v1/hc/*` 路径，因此不提供兼容别名。若未来原生/小程序不使用 Admin Browser Session，应新增独立验证过的 Human Login Adapter，而不是放宽本 ADR。

## 验证

- 路由授权测试证明两条 Admin API 需要 `harness:operate`；
- 无 Session、错误 Origin、缺少/错误 CSRF、跨 owner/tenant、过期/重复 Challenge、错误 JKT/签名均拒绝；
- 注册只创建一个 Endpoint/Credential，重复或并发完成不产生第二个活动 Endpoint；
- 浏览器 Network/Storage 验证 Admin Token 不可由 JavaScript 读取，Endpoint Access Token 不进 Local Storage，Refresh Cookie 只发送到 refresh Path；
- Gateway 测试证明 Admin Cookie 不能替代 Endpoint DPoP。

## 参考

- ADR-0004 Thin Host import 模式
- mss-boot-admin v1.3.7 `middleware/browser_session.go`
- RFC 9449 DPoP

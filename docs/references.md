# Harness Platform 参考资料

- **状态**：Maintained reference index
- **最后核验**：2026-09-03
- **说明**：本文只记录上游和标准的权威入口。外部链接内容可能变化；实现必须通过仓库锁文件固定实际版本。

## 1. Platform 上游

### mss-boot-admin

- 仓库：https://github.com/mss-boot-io/mss-boot-admin
- 固定标签：https://github.com/mss-boot-io/mss-boot-admin/releases/tag/v1.3.7
- 标签引用：https://api.github.com/repos/mss-boot-io/mss-boot-admin/git/ref/tags/v1.3.7
- 标签对象：`41c6517950f7f5f642418f5d4a49386e9c200b15`
- Peeled Source Commit：https://github.com/mss-boot-io/mss-boot-admin/commit/77b53d41092741eac62fa6418c0bdbf87413c7cd
- 固定 Go Module：https://github.com/mss-boot-io/mss-boot-admin/blob/v1.3.7/go.mod
- License：https://github.com/mss-boot-io/mss-boot-admin/blob/v1.3.7/LICENSE

已核验基线：

```text
tag:         v1.3.7
tag object:  41c6517950f7f5f642418f5d4a49386e9c200b15
source SHA:  77b53d41092741eac62fa6418c0bdbf87413c7cd
Go:          1.26.6
license:     MIT
```

重要代码参考：

- Human Auth / durable session：https://github.com/mss-boot-io/mss-boot-admin/blob/v1.3.7/admin/middleware/auth.go
- Browser WebSocket Ticket：https://github.com/mss-boot-io/mss-boot-admin/blob/v1.3.7/admin/apis/ws.go
- WebSocket Hub：https://github.com/mss-boot-io/mss-boot-admin/blob/v1.3.7/admin/center/websocket/handler.go
- Task Server：https://github.com/mss-boot-io/mss-boot-admin/blob/v1.3.7/mss-boot/core/server/task/server.go

这些文件用于理解可复用模式，不表示 ACP 模块直接复用通知 Hub 或普通 JWT Principal。

## 2. Agent Client Protocol（ACP）

- 官方站点：https://agentclientprotocol.com/
- 协议仓库：https://github.com/agentclientprotocol/agent-client-protocol
- Schema：https://github.com/agentclientprotocol/agent-client-protocol/tree/main/schema
- 官方 Rust SDK：https://github.com/agentclientprotocol/rust-sdk
- Rust SDK 文档：https://agentclientprotocol.github.io/rust-sdk/
- Rust SDK 2.0.0：https://github.com/agentclientprotocol/rust-sdk/releases/tag/v2.0.0
- Rust SDK 2.0 Migration：https://agentclientprotocol.github.io/rust-sdk/migration_v2.0.html

项目首发约束：

```text
ACP Wire Protocol: stable v1
Rust SDK family:   2.0.x
Exact dependency:  Cargo.lock
Draft ACP v2:      disabled by default
```

SDK 版本与 ACP Wire Version 是不同维度，不得把 SDK 2.0 自动理解成 ACP Wire v2。

## 3. 密码学与身份标准

### TLS 与 WebSocket

- TLS 1.3 — RFC 8446：https://www.rfc-editor.org/rfc/rfc8446.html
- WebSocket — RFC 6455：https://www.rfc-editor.org/rfc/rfc6455.html

### JWS/JWK 与指纹

- JSON Web Signature — RFC 7515：https://www.rfc-editor.org/rfc/rfc7515.html
- JSON Web Key — RFC 7517：https://www.rfc-editor.org/rfc/rfc7517.html
- JWK Thumbprint — RFC 7638：https://www.rfc-editor.org/rfc/rfc7638.html
- JSON Web Algorithms — RFC 7518：https://www.rfc-editor.org/rfc/rfc7518.html

### Token 持有证明与 OAuth 安全

- DPoP — RFC 9449：https://www.rfc-editor.org/rfc/rfc9449.html
- OAuth 2.0 Device Authorization Grant — RFC 8628：https://www.rfc-editor.org/rfc/rfc8628.html
- OAuth 2.0 Mutual-TLS — RFC 8705：https://www.rfc-editor.org/rfc/rfc8705.html
- OAuth 2.0 Security Best Current Practice — RFC 9700：https://www.rfc-editor.org/rfc/rfc9700.html

Harness Platform 的 Enrollment 参考 Device Authorization 的用户体验，但 Endpoint Credential、双公钥持有证明和领取包加密是项目自己的应用协议，必须遵守 AWP 规范。

### 密钥派生与封装

- HKDF — RFC 5869：https://www.rfc-editor.org/rfc/rfc5869.html
- HPKE — RFC 9180：https://www.rfc-editor.org/rfc/rfc9180.html

### 算法实现参考

- NIST SP 800-56A Rev. 3（离散对数密钥建立）：https://csrc.nist.gov/pubs/sp/800/56/a/r3/final
- NIST SP 800-38D（GCM）：https://csrc.nist.gov/pubs/sp/800/38/d/final
- FIPS 186-5（数字签名）：https://csrc.nist.gov/pubs/fips/186-5/final

本项目只通过成熟库使用这些原语，不自行实现椭圆曲线、HKDF、HPKE 或 AEAD。

## 4. 浏览器与客户端能力

- Web Cryptography API：https://www.w3.org/TR/WebCryptoAPI/
- Indexed Database API：https://www.w3.org/TR/IndexedDB/
- Content Security Policy Level 3：https://www.w3.org/TR/CSP3/
- Trusted Types：https://w3c.github.io/trusted-types/dist/spec/
- Web Locks API：https://w3c.github.io/web-locks/

Web 标准存在并不代表目标浏览器/小程序真实支持。HC 能力必须通过 `docs/roadmap/VERIFICATION.md` 规定的浏览器和真机测试确认。

## 5. Protobuf 与兼容性

- Protocol Buffers：https://protobuf.dev/
- Proto3 Language Guide：https://protobuf.dev/programming-guides/proto3/
- Buf Schema Registry/CLI：https://buf.build/docs/
- Buf Breaking Change Detection：https://buf.build/docs/breaking/

Protobuf 负责 Wire Schema，但安全签名输入使用项目定义的固定 Canonical AAD/Transcript，不能假设普通 Protobuf 序列化是跨实现规范化签名格式。

## 6. Rust 安全与供应链工具

- RustSec Advisory Database：https://rustsec.org/
- cargo-audit：https://github.com/RustSec/rustsec/tree/main/cargo-audit
- cargo-deny：https://github.com/EmbarkStudios/cargo-deny
- cargo-vet：https://mozilla.github.io/cargo-vet/
- zeroize：https://docs.rs/zeroize/
- secrecy：https://docs.rs/secrecy/

具体依赖版本以 `aba/Cargo.lock` 为准，本文链接不构成浮动依赖授权。

## 7. Go 与前端供应链

- Go Modules：https://go.dev/ref/mod
- Go Vulnerability Management：https://go.dev/security/vuln/
- govulncheck：https://go.dev/doc/tutorial/govulncheck
- npm lockfile：https://docs.npmjs.com/cli/configuring-npm/package-lock-json
- pnpm lockfile/CLI：https://pnpm.io/
- CycloneDX SBOM：https://cyclonedx.org/
- SLSA：https://slsa.dev/

## 8. 参考优先级

发生冲突时：

1. 已冻结的 Harness Platform AWP/安全规范定义应用协议细节。
2. 对标准算法、JWS、DPoP、HPKE、TLS 等行为，以对应 RFC 为准。
3. 对 ACP Payload 语义，以项目锁定的稳定 ACP v1 Schema 和官方 SDK 行为为准。
4. 对 Platform 现有行为，以锁定的 mss-boot-admin v1.3.7 源提交为准。
5. 博客、Issue、示例和二手文章只能作为背景，不能覆盖规范。

## 9. 更新规则

- 增加外部规范时记录用途和实际锁定版本。
- 标准或 SDK 更新不会自动改变当前实现；需要 ADR 和兼容验证。
- 外部链接失效时修复链接，但不得在没有验证的情况下替换为不同语义来源。
- 不在本文保存访问令牌、私有 Registry 凭据或内部生产 URL。
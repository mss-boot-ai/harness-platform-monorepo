# Harness Platform MVP 实现架构

- **状态**：Accepted
- **日期**：2026-09-04
- **产品契约**：`docs/product/MVP-PRD.md`

## 1. 架构原则

MVP 采用一个 Monorepo、四个可独立测试的运行单元：

```text
platform/cmd/server             mss-boot-admin Thin Host 管理控制面
platform/cmd/harness-gateway    Endpoint HTTP/WSS 数据面
aba                             Rust 本地安全桥接
hc                              TypeScript 浏览器/Node 参考客户端
```

另有 `test-agent` 作为确定性 ACP Stable v1 测试实现。Platform 的两个 Go 命令共享 `platform/internal/harness` 领域包和 Repository，但认证入口严格隔离：Human 管理 API 使用 Admin Session/RBAC，Endpoint 数据面使用发送者约束 Credential。

## 2. Platform 包布局

```text
platform/
├── cmd/server/main.go
├── cmd/harness-gateway/main.go
├── internal/modules/harness/       # Admin business.Module 适配层
│   ├── module.go
│   ├── migrations.go
│   ├── readiness.go
│   ├── authorization.go
│   └── routes.go
├── internal/harness/               # 与 Admin 框架解耦的核心领域
│   ├── model.go
│   ├── errors.go
│   ├── clock.go
│   ├── ids.go
│   ├── crypto/
│   ├── store/
│   ├── enrollment/
│   ├── endpoint/
│   ├── session/
│   ├── delivery/
│   ├── ticket/
│   ├── audit/
│   └── gateway/
└── web/src/business/               # Thin Host 手写前端接缝
```

规则：

- 领域 Service 不导入 Gin、Admin middleware 或具体数据库 Driver；
- Handler 只解析、鉴权、调用 Service、映射稳定错误；
- Repository 负责事务、唯一约束和并发 CAS；
- Gateway Connection Directory 是接口，不把内存 Map 作为持久权威；
- AWP Packet 原始字节进入 Store，禁止重新编码后替代签名/AAD 输入；
- 管理页面只读安全投影，永不返回 Token Hash、Ticket、私钥或 Payload。

## 3. 身份与 Credential

每个 Endpoint：

```text
endpoint_id
owner_user_id / tenant_id
endpoint_type: ABA | HC_WEB | HC_REFERENCE
signing_public_jwk / sign_jkt
kem_public_jwk / kem_jkt
credential_family_id
status / row_version
```

Endpoint Access Token 是 32 字节随机不透明值，数据库只保存 SHA-256 Hash。Token 通过 DPoP `cnf.jkt` 绑定 E-SIG。Refresh 采用 Family Rotation；MVP 可以只发短 Access + 单次 Refresh，但旧 Refresh 重用必须吊销 Family。

## 4. Human 与 Endpoint API 隔离

```text
Admin protected /api/harness/v1/*
    Human Session → current Principal → owner/tenant authorization

Gateway /gateway/v1/*
    Endpoint Token + DPoP → current Endpoint/Credential → resource authorization
```

Gateway 不接受 Admin Cookie 作为 Endpoint 身份；Admin API 不接受 Endpoint Token 代替 Human 审批。

## 5. Enrollment

ABA Enrollment 采用 Device Authorization 风格：

- Start 接收 Endpoint 名称、版本、E-SIG/E-KEM Public JWK 和持有证明；
- Platform 生成高熵 Device Code 与人工 User Code；
- 原值仅返回一次，持久化 Hash；
- Human 审批后，ABA 使用 Device Code + 新 Challenge 消费；
- Consume 在事务内创建 Endpoint、Credential 和 Audit，并把 Enrollment 标记 CONSUMED；
- 重复 Consume 返回同一安全结果或稳定冲突，不创建第二个 Endpoint。

HC 注册由已登录 Admin 页面发起 Challenge，浏览器本地生成 Key 并完成持有证明。

## 6. WSS 状态机

```text
HTTP_AUTHENTICATED
→ TICKET_ISSUED
→ UPGRADED
→ CHALLENGED
→ READY
→ DRAINING
→ CLOSED
```

READY 前只允许 Challenge Control。Connection Registry Key 至少为 `endpoint_id + connection_generation`；新连接成功后 Fence 旧连接。每连接 Reader/Writer 单一所有者，写队列有最大 Frame 数和字节数。

## 7. Session 与本地执行

Session 创建输入只有：

```text
aba_endpoint_id
hc_endpoint_id
runtime_profile_id
workspace_id
requested_capabilities
idempotency_key
```

ABA 本地配置把 ID 映射为实际 executable、固定 args、最小环境和绝对 Workspace。Platform 不知道本地路径。启动前 ABA 解析 realpath，校验路径位于允许根，并创建独立进程组；关闭时先 ACP shutdown/TERM，再超时 KILL 整个进程树。

MVP Test Agent 只实现确定性 ACP initialize/session/prompt 响应，不访问真实源码或外部网络。

## 8. Key Package 与 Frame

ABA 为 Session Generation 1 生成：

```text
SRK[32]
session_nonce
hc_to_aba_nonce_prefix[4]
aba_to_hc_nonce_prefix[4]
key_id[16]
```

SRK 通过 HC E-KEM 公钥使用 HPKE Suite 0001 包装。Platform 保存 Package 密文、Recipient、Generation、Context Hash 和 ABA Signature。HC 验证 ABA Credential/Signature 后解封并 ACK。

方向 Key 使用 HKDF-SHA256，Domain Separation 包含 Session、Generation、Direction 和 Suite。Frame 使用 148 字节 Canonical AAD、AES-GCM 和发送 Endpoint P1363 Signature。

## 9. 可靠投递

Platform：

- 先验证外层上限、Header 和 Endpoint Signature；
- 用唯一约束写入原始 AAD/Ciphertext/Signature；
- 相同幂等键 + 相同 Hash 返回已有状态；
- 相同键 + 不同 Hash 标记 CONFLICT 并断开 Session；
- Receiver 在线则路由，否则保留至 Retention；
- 只根据安全接收 ACK 推进最高连续 Cursor；
- Resume 重放数据库中的原始 Frame。

ABA/HC：

- Sequence 使用前持久化或安全预留；
- 收到 Frame 后先验签/解密/持久化，再 ACK；
- ABA 在写 ACP stdin 前记录 `DISPATCH_STARTED`；
- 无法证明是否完整写入时进入 `UNCERTAIN`；
- 不自动重派可能产生副作用的请求。

## 10. 数据库与 Migration

MVP Repository 使用 GORM，但业务 Migration 是显式注册、带固定 ID 的前向 Migration。测试运行 SQLite；实现不得使用 SQLite 专有语义作为领域前提。PostgreSQL 作为第一个部署数据库候选。

Migration 必须创建唯一约束、索引和状态 Check。Readiness 在挂载 Admin 路由前验证 Migration Ledger 和关键表/索引，失败时不提供半初始化 API。

## 11. 前端

Admin Web 手写接缝：

- `routes.config.ts` 声明 Harness 页面；
- `route-registrations.ts` 投影服务端菜单路径；
- 中英文 Locale；
- `services/harness.ts` 统一请求和错误映射；
- 页面只接收安全 View Model。

HC 包共享：

```text
hc/src/identity
hc/src/dpop
hc/src/awp
hc/src/hpke
hc/src/session
hc/src/client
```

Node Runner 与浏览器使用同一代码；仅 Storage 和 UI Adapter 不同。

## 12. 测试架构

- Pure domain unit tests：状态机、授权、幂等、Sequence；
- Repository tests：真实 SQLite、并发唯一约束、事务；
- Gateway tests：httptest + WebSocket；
- Rust tests：配置、KeyStore、Wire、Journal、Process Supervisor；
- TS tests：WebCrypto、DPoP、AAD、HPKE、Session；
- Shared vectors：Go/Rust/TS 读取同一 `protocol/testdata/v1`；
- E2E：CI 启动 Gateway，运行 ABA 与 HC Runner，执行完整闭环；
- Opaque Canary：扫描数据库、日志和 Audit。

## 13. 故障与资源边界

默认测试配置：

```text
max_frame_bytes            1 MiB
max_connection_queue       128 frames / 8 MiB
max_unacked_per_session    256 frames / 16 MiB
max_sessions_per_endpoint  4
journal_max_bytes          64 MiB
wss_ticket_ttl             30s
challenge_ttl              10s
access_token_ttl           10m
```

所有值由配置控制并有硬上限。达到上限返回稳定 Backpressure 错误，不无限分配内存或静默丢 Frame。

## 14. 生产演进接缝

MVP 接口必须允许后续替换：

- SQLite → PostgreSQL；
- Local Signer → KMS/Vault/HSM；
- 单实例 Connection Directory → Redis/NATS/DB Lease；
- Test Agent → 任意固定配置 ACP Agent；
- HC Reference Runner → 正式 Web/小程序/App；
- 单 Participant → 多 HC + Session Rekey。

这些替换不能改变 AWP v1 已发布 Wire、安全不变量和 ACK 含义。

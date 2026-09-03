# Platform 设计：基于 mss-boot-admin v1.3.7

- **状态**：Accepted
- **上游标签**：`v1.3.7`
- **标签对象**：`41c6517950f7f5f642418f5d4a49386e9c200b15`
- **源提交**：`77b53d41092741eac62fa6418c0bdbf87413c7cd`
- **Go 基线**：`1.26.6`

## 1. 基线与引入策略

Platform 不是从空白 Go 服务重新开发，而是在 monorepo 的 `platform/` 目录中维护一份可修改、可追溯的 mss-boot-admin v1.3.7 源码基线。

首选引入方式为 **vendored subtree-style import**：

- 上游完整源码进入 `platform/`，不依赖开发者额外初始化 Git Submodule。
- 首次导入必须来自精确 peeled commit，而不是浮动 tag checkout 结果。
- 保留上游 `LICENSE`、版权和必要 Notice。
- 在 `platform/.upstream/mss-boot-admin.lock.yaml` 记录仓库、tag、tag object、source SHA、tree SHA、导入时间和导入方法。
- Platform 构建注入 `MSS_BOOT_ADMIN_BASE_TAG` 与 `MSS_BOOT_ADMIN_BASE_COMMIT`，版本接口和制品 Build Info 可验证。
- 上游升级只通过独立同步分支和 ADR，不自动跟随上游 main。

不采用 Git Submodule 作为默认开发体验，原因是本项目需要直接修改 Platform 源码、统一提交和保证离线/CI checkout 后可构建。若首次自动导入受工具限制，允许先提交锁文件和可重现导入脚本，但代码进入功能实现前必须完成实际源码导入。

## 2. 可复用的 mss-boot-admin 能力

### 2.1 Human Authentication 与 Session

v1.3.7 已具备：

- 用户认证 Middleware。
- 服务端持久化 User Session。
- JWT 中携带 Session ID。
- 请求时重新加载当前 Principal 和角色，而不是永久信任登录时快照。
- Browser Session 与 PAT/自动化边界。
- 登录与认证失败审计。

ACP 模块必须复用该 Human Principal，不另建平行用户密码或长期 Browser Bearer Token。

### 2.2 WebSocket Ticket 模式

v1.3.7 已有浏览器先通过认证 Session 获取短期 Ticket、通过 `Sec-WebSocket-Protocol` 传递、连接时单次消费、验证 Origin、重新查询当前用户/角色和 Session 的机制。

ACP 模块复用这个安全模式和基础包思路，但新增独立 Ticket Namespace 和 Record 字段，绑定：

```text
purpose = acp-ws
endpoint_id
endpoint_type
credential_serial
sign_jkt
session_id (human session for HC)
origin/client context
connection nonce
expires_at
single_use
```

不能直接复用普通通知 Ticket Record 丢失 Endpoint 绑定。

### 2.3 Task Server

v1.3.7 已有 System Schedule 与持久化用户任务隔离的 Task Server。ACP 使用固定数量系统任务扫描数据库，不为每个用户或每张证书创建 Cron Entry。

### 2.4 Audit、Notification、RBAC 和前端

复用现有：

- 用户/角色/菜单/Action 权限。
- 审计服务与登录日志风格。
- 通知中心。
- GORM 数据访问与配置中心。
- Ant Design v6 Web 管理端。

ACP 安全事件需要专用结构化审计表，但可以通过现有通知服务提示用户。

## 3. 不复用的边界

### 3.1 现有通知 WebSocket Hub

现有 Hub 面向用户级 JSON Event 和普通通知。ACP Gateway 需要：

- Endpoint 级身份与路由。
- Binary Protobuf Packet。
- 连接 Fencing。
- Channel/Direction/Sequence。
- 持久化 ACK/Replay。
- 慢消费者 Backpressure。
- 密文和控制帧不同优先级。

因此新建 `admin/center/acpgateway`，不把 ACP Packet 注册为普通通知 Event。

### 3.2 普通 gin-jwt Bearer Middleware

ABA 不是人类用户浏览器。ABA/HC Endpoint Access Token 有独立 `typ`、`aud`、DPoP 和 Endpoint Principal。实现独立 ACP Endpoint Authentication Middleware，并在需要时与 Human Principal 组合。

### 3.3 普通任务表

证书与 Key Rotation 的权威状态在 ACP 表中；Task Server 只触发扫描 Worker。不能把每个轮换实例映射成普通 Cron Job。

## 4. Platform 模块布局

完成上游导入后，建议在现有结构中增加：

```text
platform/
├── .upstream/
│   ├── mss-boot-admin.lock.yaml
│   └── README.md
├── admin/
│   ├── apis/acp/
│   │   ├── bootstrap.go
│   │   ├── enrollment.go
│   │   ├── endpoint.go
│   │   ├── credential.go
│   │   ├── token.go
│   │   ├── ticket.go
│   │   ├── session.go
│   │   ├── key.go
│   │   └── operations.go
│   ├── dto/acp/
│   ├── models/acp_*.go
│   ├── service/acp/
│   │   ├── identity.go
│   │   ├── enrollment.go
│   │   ├── signer.go
│   │   ├── credential.go
│   │   ├── dpop.go
│   │   ├── ticket.go
│   │   ├── authorization.go
│   │   ├── session.go
│   │   ├── relay.go
│   │   ├── rotation.go
│   │   └── audit.go
│   ├── middleware/acp/
│   │   ├── endpoint_auth.go
│   │   ├── dpop.go
│   │   └── principal.go
│   ├── center/acpgateway/
│   │   ├── gateway.go
│   │   ├── connection.go
│   │   ├── directory.go
│   │   ├── router.go
│   │   ├── replay.go
│   │   ├── backpressure.go
│   │   └── protocol.go
│   └── jobs/acp/
│       ├── rotation_scan.go
│       ├── retention.go
│       ├── enrollment_expiration.go
│       └── endpoint_health.go
└── web/antd-v6/
    └── ACP pages/components/services/locales
```

Protocol 生成的 Go 类型放入 monorepo 共享生成目录或 `platform/internal/gen/awpv1`，禁止手工复制定义。

## 5. Principal 模型

### 5.1 Human Principal

沿用 mss-boot-admin 当前 Principal，并增加 ACP Action 权限。

### 5.2 Endpoint Principal

```go
type EndpointPrincipal struct {
    TenantID          string
    OwnerUserID       string
    EndpointID        string
    EndpointType      string // aba, hc-web, hc-miniapp, hc-native
    CredentialSerial  string
    SigningJKT        string
    KEMJKT            string
    AssuranceLevel    string
    Scopes            []string
    PolicyRevision    uint64
    StatusRevision    uint64
}
```

### 5.3 Combined Request Context

HC 管理/Session 创建请求同时需要：

- Current Human Principal。
- Current Human Session。
- HC Endpoint Principal。
- DPoP Proof。

ABA Gateway 请求只需要 Endpoint Principal、DPoP/Ticket 和 AEC，不伪装成人类用户；OwnerUserID 仅用于授权归属。

## 6. 有效授权计算

```text
EffectivePermission =
  HumanCurrentRBAC
  ∩ EndpointCredentialScopes
  ∩ AccessTokenScopes
  ∩ EndpointStatusPolicy
  ∩ SessionACL
  ∩ RuntimeWorkspaceAuthorization
```

实现要求：

- 每个安全敏感请求读取当前数据库权威，不只用 Token Claims。
- 授权结果绑定 `authorization_revision`。
- Platform 发送 OpenTunnel 时携带 Revision；ABA 拒绝低于本地已接受 Revision 的控制请求。
- 跨 Tenant 查询在 Repository 层显式加 Tenant 条件，并有测试。

## 7. 数据模型

以下表名可按 mss-boot-admin 命名规范调整，但语义和约束必须保留。

### 7.1 `acp_crypto_profiles`

```text
id                       PK
scope_type               user | tenant
scope_id
mode                     opaque | managed
active_uca_id
current_manifest_revision
allowed_crypto_suites
rotation_policy_json
status
created_at / updated_at
```

唯一：`(scope_type, scope_id)` 当前只允许一个 Active Profile。

### 7.2 `acp_trust_issuers`

```text
id
crypto_profile_id
issuer_type              root | online | uca
key_reference            KMS reference, never private key bytes
public_jwk/certificate
thumbprint
status                   next | active | grace | retired | compromised
not_before / expires_at
activated_at / retired_at
parent_issuer_id
```

### 7.3 `acp_endpoints`

```text
id                       16-byte logical ID represented by DB-safe type
 tenant_id
owner_user_id
endpoint_type
endpoint_name
installation_id_hash
sign_public_jwk
sign_jkt
kem_public_jwk
kem_jkt
assurance_level
status
status_revision
policy_revision
software_version
platform / architecture
last_seen_at
created_at / updated_at / revoked_at
```

唯一：

- `(tenant_id, id)`。
- `(tenant_id, sign_jkt)` 在 Active/Pending 范围内。
- `(tenant_id, kem_jkt)` 在 Active/Pending 范围内。

### 7.4 `acp_endpoint_credentials`

```text
id / serial
endpoint_id
issuer_id
credential_type          AEC | HEC
credential_jws
sign_jkt / kem_jkt
crypto_suite
scopes_json
policy_revision
status                   issued | active | overlap | renewal_only | revoked | expired
not_before / expires_at
replaced_by_serial
revoked_reason
created_at / activated_at / revoked_at
```

约束：同 Endpoint 最多一个 Primary Active Credential，轮换期允许一个 Overlap。

### 7.5 `acp_enrollments`

```text
id
tenant_hint
endpoint_type
endpoint_name
installation_id_hash
sign_public_jwk / sign_jkt
kem_public_jwk / kem_jkt
request_transcript_hash
device_code_hash
user_code_hash
requested_ip / user_agent
state                    pending | approved | denied | consumed | expired
expires_at
approved_by_user_id
approved_at / consumed_at
attempt_count
created_at
```

原始 Code 不入库；User Code 使用带 Server Pepper 的慢哈希或高熵索引策略。

### 7.6 `acp_token_families`

```text
id
endpoint_id
credential_serial
sign_jkt
family_revision
status
last_refresh_hash
reuse_detected_at
expires_at
revoked_at
```

Refresh Token 只保存哈希；旋转重用触发整个 Family 吊销。

### 7.7 `acp_sessions`

```text
id
tenant_id
owner_user_id
aba_endpoint_id
runtime_profile_id
workspace_id
status
authorization_revision
active_key_generation
pending_key_generation
created_at / last_activity_at / closed_at
close_reason
version
```

Session 使用 Optimistic Version 或行锁保护状态转换。

### 7.8 `acp_session_participants`

```text
session_id
hc_endpoint_id
role
status
joined_at / revoked_at
last_key_generation_ack
last_seen_at
```

唯一：`(session_id, hc_endpoint_id)`。

### 7.9 `acp_key_generations`

```text
session_id
generation
crypto_suite
status
not_before / activated_at / grace_until / destroyed_at
created_by_aba_endpoint_id
activation_policy_json
version
```

部分唯一索引：每个 Session 最多一个 Active；最多一个 Pending/Distributing/Ready Next Generation。

### 7.10 `acp_key_packages`

```text
id
session_id
generation
issuer_aba_endpoint_id
recipient_hc_endpoint_id
hpke_enc
hpke_ciphertext
issuer_signature
not_before / expires_at
status                   pending | delivered | acknowledged | expired | revoked
acknowledged_at
created_at
```

唯一：`(session_id, generation, recipient_hc_endpoint_id)`。

### 7.11 `acp_channels`

```text
id
session_id
aba_endpoint_id
hc_endpoint_id
status
hc_to_aba_next_sequence
aba_to_hc_next_sequence
hc_to_aba_ack
aba_to_hc_ack
key_generation
created_at / closed_at
version
```

Sequence 分配主要由 Endpoint 本地执行；Platform 字段用于观察和约束，不得造成 Nonce 源冲突。

### 7.12 `acp_frames`

```text
id/message_id
channel_id
session_id
sender_endpoint_id
receiver_endpoint_id
direction
sequence
key_generation
key_id
frame_type
flags
created_at_ms
ciphertext
signature
stored_at
expires_at
size_bytes
```

唯一：

- `message_id`。
- `(channel_id, direction, sequence)`。

密文较大规模时可把 Blob 放对象/流存储，关系表保存索引和哈希；MVP 可先数据库分区表，但必须有容量计划。

### 7.13 `acp_channel_acks`

```text
channel_id
receiver_endpoint_id
acknowledged_direction
highest_contiguous_sequence
received_ranges_json or normalized ranges
key_generation
updated_at
version
```

ACK 只能单调前进，更新使用条件 SQL/事务。

### 7.14 Runtime/Workspace Catalog

Platform 只保存 ABA 上报的展示摘要和授权，不保存真实本地执行值：

```text
acp_runtime_catalog: endpoint_id, runtime_profile_id, display_name, capability_digest, status
acp_workspace_catalog: endpoint_id, workspace_id, display_name, allowed_runtime_ids, status
acp_resource_grants: subject/user/role, endpoint_id, runtime_id, workspace_id, permissions
```

### 7.15 `acp_audit_events`

```text
id
tenant_id
actor_type / actor_id
endpoint_id
resource_type / resource_id
action
result
credential_serial
connection_id
correlation_id
safe_metadata_json
created_at
```

`safe_metadata_json` 有字段白名单，禁止任意错误对象或请求 Body。

### 7.16 `acp_idempotency_records`

用于 Enrollment Approve、Revoke、Rotate、Session Create/Close 等管理 API：

```text
scope
idempotency_key_hash
request_hash
result_reference
status
expires_at
```

相同 Key 不同 Request Hash 返回冲突。

## 8. API 命名

管理和 HC API：

```text
/admin/api/acp/v1/...
```

ABA Bootstrap/Endpoint API 可使用同一 Host 的独立前缀：

```text
/acp/api/v1/...
```

不把 ABA 无 Human Session 的接口放在普通 Admin Auth Middleware 后再做例外。

## 9. API 清单

### 9.1 Bootstrap 与 Manifest

```text
GET  /acp/api/v1/bootstrap
GET  /acp/api/v1/trust-manifest
```

Bootstrap 返回公开、签名或可验证的 Protocol/Suite、Root Fingerprint、Enrollment URL 和最小版本，不返回秘密。

### 9.2 ABA Enrollment

```text
POST /acp/api/v1/aba/enrollments
POST /acp/api/v1/aba/enrollments/{id}/poll
POST /acp/api/v1/aba/enrollments/{id}/consume

GET  /admin/api/acp/v1/aba/enrollments/{id}
POST /admin/api/acp/v1/aba/enrollments/{id}/approve
POST /admin/api/acp/v1/aba/enrollments/{id}/deny
```

Approve 要求 Human Session、ACP Action、二次认证和 Idempotency-Key。

### 9.3 HC Endpoint

```text
POST /admin/api/acp/v1/hc/challenges
POST /admin/api/acp/v1/hc/endpoints
GET  /admin/api/acp/v1/endpoints
GET  /admin/api/acp/v1/endpoints/{id}
POST /admin/api/acp/v1/endpoints/{id}/suspend
POST /admin/api/acp/v1/endpoints/{id}/resume
POST /admin/api/acp/v1/endpoints/{id}/revoke
POST /admin/api/acp/v1/endpoints/{id}/rotate
```

### 9.4 Endpoint Token

```text
POST /acp/api/v1/tokens/exchange
POST /acp/api/v1/tokens/refresh
POST /acp/api/v1/tokens/revoke
```

每个请求验证 Endpoint Credential、DPoP 或 Enrollment Transcript。Refresh Token 旋转且检测重用。

### 9.5 WebSocket Ticket

```text
POST /admin/api/acp/v1/ws/tickets    # HC
POST /acp/api/v1/ws/tickets          # ABA
GET  /acp/api/v1/ws/connect
```

两种 Ticket Record 的 Principal 来源不同，但都绑定 Endpoint 和 Purpose。Connect 只消费 Ticket、执行 Origin/Context 检查并交给 ACP Gateway。

### 9.6 Session

```text
POST /admin/api/acp/v1/sessions
GET  /admin/api/acp/v1/sessions
GET  /admin/api/acp/v1/sessions/{id}
POST /admin/api/acp/v1/sessions/{id}/participants
DELETE /admin/api/acp/v1/sessions/{id}/participants/{endpointId}
POST /admin/api/acp/v1/sessions/{id}/pause
POST /admin/api/acp/v1/sessions/{id}/resume
POST /admin/api/acp/v1/sessions/{id}/close
POST /admin/api/acp/v1/sessions/{id}/rotate-key
```

Session Create Request 只包含 Endpoint、Runtime Profile ID、Workspace ID 和用户可理解选项，不包含 command 等本地值。

### 9.7 Key Package

```text
GET  /admin/api/acp/v1/key-packages/pending
POST /admin/api/acp/v1/key-packages/{id}/ack
```

实际在线分发优先走 WSS Control Frame；HTTP API 用于恢复/轮询。返回设置 No Store。

### 9.8 Certificate 与策略

```text
GET  /admin/api/acp/v1/crypto-profile
PUT  /admin/api/acp/v1/crypto-profile/rotation-policy
GET  /admin/api/acp/v1/credentials
POST /admin/api/acp/v1/credentials/{serial}/revoke
POST /admin/api/acp/v1/trust/rotate
GET  /admin/api/acp/v1/rotation-jobs
```

Root/UCA 操作要求最高权限和二次认证。

### 9.9 Operations

```text
GET /admin/api/acp/v1/operations/overview
GET /admin/api/acp/v1/operations/connections
GET /admin/api/acp/v1/operations/backlog
GET /admin/api/acp/v1/operations/security-events
GET /admin/api/acp/v1/audit-events
```

## 10. DPoP Middleware 顺序

推荐顺序：

```text
request size / trusted proxy normalization
-> route audience/type selection
-> parse Endpoint Access Token
-> parse and verify DPoP header signature
-> verify htm/htu/iat/jti/nonce/ath
-> load Endpoint and Credential current state
-> build EndpointPrincipal
-> optional Human Session authentication
-> current RBAC and authorization intersection
-> handler
-> structured audit
```

`htu` 规范化必须使用可信的 Public Base URL 和受信任代理配置，不能直接相信任意 `X-Forwarded-Host`。

## 11. ACP Gateway

### 11.1 Connection Registry

每个 READY 连接记录：

```text
endpoint_id
endpoint_type
credential_serial
connection_id
generation
fencing_token
gateway_instance_id
connected_at / last_seen
send queue metrics
```

单实例 Connection 对象保存在内存；共享目录记录归属和 Lease。Lease 过期后才可判定旧连接失效。

### 11.2 Ingress Pipeline

```text
binary size check
-> protobuf decode with limits
-> wire/version/type check
-> endpoint/connection fencing
-> signature and envelope validation
-> authorization/routing validation
-> idempotent persistence
-> local or cross-instance delivery
-> metrics/audit (no plaintext)
```

### 11.3 Cross-instance Routing

最终实现通过 ADR 选择 Redis Streams、NATS JetStream、RabbitMQ 或内部 RPC。要求：

- 不把进程内 Map 当唯一目录。
- 支持目标 Gateway 重启和 Frame 持久化恢复。
- 不产生第二套与数据库冲突的 ACK 权威。
- 控制消息和数据消息可设置优先级。

### 11.4 Backpressure

每 Connection 有有界发送队列。队列满时停止从共享路由拉取并反馈 Backpressure；不得创建无界 Goroutine/Channel。

## 12. 后台系统任务

固定 System Schedule：

```text
acp-enrollment-expiration-scan
acp-endpoint-credential-renewal-scan
acp-session-key-rotation-scan
acp-key-activation-scan
acp-key-grace-expiration-scan
acp-revocation-broadcast
acp-frame-retention-scan
acp-audit-retention-scan
acp-endpoint-health-scan
acp-token-family-cleanup
```

任务扫描 `next_action_at`/状态并使用数据库抢占 Lease。多 Platform 实例下同一对象只能被一个 Worker 处理；失败幂等重试，错误进入运营告警。

## 13. KMS/Signer 接口

```go
type Signer interface {
    PublicKey(ctx context.Context, keyRef string) (jwk []byte, err error)
    SignDigest(ctx context.Context, keyRef string, alg string, digest []byte) ([]byte, error)
    Health(ctx context.Context, keyRef string) error
}
```

开发可有 `local-sealed` 实现，但：

- 私钥必须以独立 Master Key 加密。
- Master Key 不进仓库。
- 生产配置默认拒绝 `plaintext` provider。
- 日志只记录 Key Reference，不记录签名输入中的秘密或密钥。

## 14. 配置

建议：

```yaml
acp:
  enabled: true
  publicBaseURL: https://platform.example.com
  wire:
    major: 1
    minor: 0
    maxPacketBytes: 1048576
    maxInflightPerChannel: 1024
  gateway:
    heartbeatInterval: 20s
    connectionLeaseTTL: 60s
    sendQueueSize: 256
  enrollment:
    ttl: 10m
    pollInterval: 5s
  ticket:
    ttl: 30s
  token:
    accessTTL: 5m
    refreshTTL: 30d
  crypto:
    suite: MSS-AWP-SUITE-0001
    signerProvider: kms
    rootKeyRef: ...
  retention:
    ackedFrame: 24h
    unackedFrame: 168h
```

配置校验失败时启动失败；生产模式缺少 Redis/共享 Ticket Store、Signer 或数据库约束时失败关闭。

## 15. RBAC Action

建议 Action：

```text
acp:endpoint:view
acp:endpoint:approve
acp:endpoint:manage-own
acp:endpoint:revoke
acp:session:create
acp:session:view-own
acp:session:manage
acp:resource:grant
acp:credential:view
acp:credential:rotate
acp:trust:manage
acp:operations:view
acp:audit:view
acp:managed-mode:enable
```

普通用户默认只能管理自己的 Endpoint/Session；租户管理员权限也必须受 Tenant 条件约束。

## 16. Platform Web 页面

```text
ACP Overview
├── 在线 ABA / HC / Session
├── Frame backlog / rotation alerts
Endpoints
├── ABA enrollment approval
├── HC list / status / revoke
├── credential and key status
Sessions
├── session metadata / participants
├── connection and uncertain state
Security
├── trust manifest / issuer status
├── rotation policy / manual rotation
├── revoked credentials
Runtime & Workspace Grants
Audit & Operations
```

Opaque Mode 页面不得尝试展示 ACP 内容。会话标题默认使用本地 HC 生成或用户手动命名；若上传标题，必须明确它是可见元数据。

## 17. 数据库迁移规则

- 每个迁移可重复检测，不使用危险隐式 AutoMigrate 替代生产迁移审查。
- 唯一约束、部分索引、状态 Check 和 Tenant 索引必须显式定义。
- 大表 `acp_frames` 的分区/归档在生产前压测。
- 迁移包含前滚验证和回滚/不可逆说明。
- 升级期间旧/新 Gateway 的 Wire/API 兼容窗口明确。

## 18. 上游差异管理

`platform/.upstream/` 维护：

- 当前基线锁。
- 首次导入命令与 Tree Hash。
- 对上游修改的分类清单。
- 后续同步冲突和安全补丁记录。

禁止把 `platform/` 当成无法追踪来源的代码复制。每次上游升级必须能比较：

```text
old upstream SHA
new upstream SHA
our platform delta
conflict resolutions
migration and verification evidence
```

## 19. Platform 最小验收

- 版本接口返回精确 mss-boot-admin 基线。
- Human Session、Endpoint Principal 和权限交集生效。
- ABA/HC Token 不能互换 Audience。
- Ticket 一次消费并绑定 Endpoint/Origin/Purpose。
- ACP Gateway 不依赖普通通知 Hub。
- 二进制 Packet 可持久化、路由、ACK、重放且不解析 ACP 明文。
- 跨租户、吊销、重复、乱序和 Backpressure 测试通过。
- 定时轮换任务固定数量且多实例幂等。
- 数据库、缓存、日志和备份中不存在 Endpoint 私钥和 SRK 明文。
- Web 管理页面能够完成 Endpoint、Session、证书、轮换和审计操作。
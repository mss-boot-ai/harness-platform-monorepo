# ABA 设计：Rust `acp-brige-agent`

- **状态**：Accepted
- **正式名称**：`acp-brige-agent`
- **简称**：ABA
- **语言**：Rust
- **ACP 基线**：稳定 v1，官方 Rust SDK 2.0.x

## 1. 定位

ABA 是运行在用户控制机器上的轻量本地桥接与安全边界。它不是后台管理系统、任务编排平台、远程 Shell 或数据仓库。

ABA 的存在理由只有三类：

1. 某些能力必须在本地才能安全完成：端点私钥、本地 Runtime/Workspace 白名单、ACP 子进程与本地文件权限。
2. ACP JSON-RPC 需要在本地 ACP Transport 与远端加密通道之间桥接。
3. 为避免网络重放造成危险副作用，需要最小本地 Journal 和执行不确定状态。

其他重操作全部上移 Platform。

## 2. 目标

- 单个静态或最小动态依赖二进制即可运行。
- 默认无入站监听端口，只主动连接 Platform。
- 支持个人电脑、Linux 服务器、Docker 和 Kubernetes Pod。
- 冷启动快、空闲内存低、磁盘状态有严格上限。
- 不内置用户管理、Web UI、Redis、PostgreSQL 或大规模历史记录。
- ACP v1 语义、请求/通知/响应和 Batch 边界不被破坏。
- Platform 被攻破时也不能直接任意指定本地 command/args/cwd/env。

## 3. 非目标

- 本地通用任务调度中心。
- 自带 AI 模型、Prompt 编排或内容索引。
- 暴露 HTTP API 给局域网控制。
- 直接执行 Platform 下发的任意 Shell。
- 承担全局证书签发和轮换任务调度。
- 保存全部 ACP 历史明文。
- 在 ABA 中实现另一套 ACP Schema。

## 4. 进程架构

```text
┌──────────────────────────────────────────────────────────┐
│                      acp-brige-agent                      │
│                                                          │
│  CLI / bootstrap                                         │
│        │                                                 │
│  Config Loader ── Local Policy Registry                  │
│        │                                                 │
│  Identity Manager ── Secure Store                        │
│        │                                                 │
│  Platform Connector ── WSS / Ticket / Challenge          │
│        │                                                 │
│  Wire Router ── Crypto ── Replay Window ── Journal       │
│        │                                                 │
│  Session Manager ── Process Supervisor ── ACP Proxy       │
└─────────────────────────────────┬────────────────────────┘
                                  │ stdio / local transport
                                  ▼
                         Local ACP Agent process
```

## 5. Rust Workspace 与模块

```text
aba/
├── Cargo.toml
├── Cargo.lock
├── build.rs                     # protocol generation only if required
├── src/
│   ├── main.rs
│   ├── lib.rs
│   ├── cli.rs
│   ├── config/
│   │   ├── mod.rs
│   │   ├── model.rs
│   │   ├── loader.rs
│   │   └── validation.rs
│   ├── identity/
│   │   ├── mod.rs
│   │   ├── enrollment.rs
│   │   ├── credential.rs
│   │   ├── trust_manifest.rs
│   │   ├── token.rs
│   │   └── keystore.rs
│   ├── crypto/
│   │   ├── mod.rs
│   │   ├── suite.rs
│   │   ├── hpke.rs
│   │   ├── kdf.rs
│   │   ├── frame.rs
│   │   └── secret.rs
│   ├── platform/
│   │   ├── mod.rs
│   │   ├── http.rs
│   │   ├── ticket.rs
│   │   ├── websocket.rs
│   │   ├── reconnect.rs
│   │   └── heartbeat.rs
│   ├── wire/
│   │   ├── mod.rs
│   │   ├── generated.rs
│   │   ├── aad.rs
│   │   ├── validation.rs
│   │   └── errors.rs
│   ├── policy/
│   │   ├── mod.rs
│   │   ├── runtime.rs
│   │   ├── workspace.rs
│   │   └── authorization.rs
│   ├── acp/
│   │   ├── mod.rs
│   │   ├── proxy.rs
│   │   ├── process.rs
│   │   ├── transport.rs
│   │   └── supervisor.rs
│   ├── session/
│   │   ├── mod.rs
│   │   ├── manager.rs
│   │   ├── state.rs
│   │   └── keys.rs
│   ├── journal/
│   │   ├── mod.rs
│   │   ├── sqlite.rs
│   │   ├── records.rs
│   │   └── cleanup.rs
│   └── observability/
│       ├── mod.rs
│       ├── logging.rs
│       └── metrics.rs
└── tests/
    ├── config_validation.rs
    ├── wire_vectors.rs
    ├── journal_recovery.rs
    ├── process_supervisor.rs
    └── fake_platform_e2e.rs
```

共享 AWP Protobuf 由 `protocol/` 生成；ABA 不维护私有协议副本。

## 6. 依赖原则

首发依赖类别：

- Async Runtime：`tokio`。
- ACP：官方 `agent-client-protocol` 2.0.x，稳定 v1 API。
- HTTP/TLS：`reqwest` + `rustls`。
- WebSocket：与 rustls 集成、支持 Binary Frame 和有界流控的成熟库。
- Protobuf：`prost`。
- Crypto：成熟 P-256、HPKE、HKDF、AES-GCM、SHA-256 库。
- Secret Hygiene：`zeroize`、`secrecy` 或等价。
- Secure Store：平台 Keyring 抽象；具体后端按 OS。
- Journal：`rusqlite` 或等价嵌入式事务数据库。
- Config：`serde` + 严格 TOML/YAML 解析。
- CLI：`clap`。
- Logs：`tracing`。

要求：

- 提交 `Cargo.lock`。
- 禁止默认启用 ACP draft v2 Feature。
- 禁止无审查地启用庞大默认 Feature。
- CI 执行 `cargo deny`/许可证、漏洞和重复依赖检查。

## 7. CLI

```text
aba enroll --platform <url> [--name <name>]
aba run [--config <path>]
aba status
aba doctor
aba rotate-identity
aba revoke-local
aba config validate
aba version --json
```

### 7.1 `enroll`

- 生成 Endpoint Key。
- 发起 Device Code Enrollment。
- 显示 User Code、Verification URI 和 Platform Root Fingerprint。
- 轮询审批并安全保存 AEC/Token/Manifest。
- 不把 Device Code、Token 或私钥输出到普通日志。

### 7.2 `run`

- 验证配置和 Secure Store。
- 加载持久化 Revision/Sequence。
- 刷新 Trust Manifest 和短期凭据。
- 建立 Platform 连接。
- 进入事件循环并管理 Session。

### 7.3 `doctor`

只做诊断：

- DNS/TLS/Platform Bootstrap。
- Trust Manifest 与 AEC 有效性。
- Secure Store 可用性。
- Runtime 可执行文件存在且权限合适。
- Workspace 路径与边界。
- Journal 完整性和容量。

默认不启动真实 Agent、不读取 Workspace 内容、不输出秘密。

## 8. 配置

示例：

```toml
schema_version = 1

[platform]
url = "https://platform.example.com"
connect_timeout = "10s"
heartbeat_interval = "20s"

[limits]
max_sessions = 8
max_packet_bytes = 1048576
max_inflight_per_channel = 1024
journal_max_bytes = 67108864

[[runtime]]
id = "codex-acp"
display_name = "Codex ACP"
command = "/usr/local/bin/codex-acp"
args = []
env_allow = ["PATH", "HOME"]
startup_timeout = "20s"
shutdown_timeout = "10s"
max_sessions = 2

[[workspace]]
id = "mss-boot-admin"
display_name = "mss-boot-admin"
path = "/home/user/workspace/mss-boot-admin"
allowed_runtimes = ["codex-acp"]
follow_symlinks = false
```

### 8.1 严格解析

- 未知顶层/安全字段默认拒绝。
- 重复 Runtime/Workspace ID 拒绝。
- 相对 Workspace Path 拒绝。
- Command 必须是本地绝对路径或明确允许的受控查找策略。
- Workspace 必须 Canonicalize，并防止符号链接逃逸。
- Runtime 环境变量只从本地 allowlist 构造。
- Platform URL 生产模式必须 HTTPS。

配置中不保存私钥、Token、SRK 或 Device Code。

## 9. Secure Store 抽象

```rust
trait SecureStore {
    async fn put_secret(&self, name: &SecretName, value: SecretVec<u8>) -> Result<()>;
    async fn get_secret(&self, name: &SecretName) -> Result<Option<SecretVec<u8>>>;
    async fn delete_secret(&self, name: &SecretName) -> Result<()>;
    async fn supports_non_exportable_keys(&self) -> bool;
}
```

后端：

- macOS：Keychain/Secure Enclave 可用能力。
- Windows：Credential Manager/DPAPI/CNG。
- Linux Desktop：Secret Service。
- Headless Linux：受本地 Master Key 或系统服务保护的加密 Store；权限 `0600` 不是加密替代品。

Secure Store 不可用时默认拒绝 Enrollment/Run。开发模式的文件 Store 必须明确 `--insecure-dev-keystore`，只允许 loopback/Test，启动时高亮警告，不能在生产配置静默启用。

## 10. Identity Manager

职责：

- 生成 Signing/KEM Key。
- Enrollment Transcript 签名。
- 缓存/验证 AEC 和 Trust Manifest。
- Refresh Token 旋转。
- DPoP Proof 生成。
- WebSocket Challenge 签名。
- Endpoint Key 轮换和旧/新双签。
- 对吊销/过期/Revision 回滚故障关闭。

不会：

- 签发自身 Credential。
- 信任未经当前 Root/用户确认的新 Root。
- 把私钥导出给 Platform。

## 11. Platform Connector

状态机：

```text
STOPPED
 -> BOOTSTRAPPING
 -> TOKEN_READY
 -> TICKET_READY
 -> CONNECTING
 -> CHALLENGED
 -> READY
 -> BACKING_OFF
 -> DRAINING
 -> STOPPED
```

### 11.1 重连

- 指数退避 + Full Jitter。
- 尊重服务器 `Retry-After`。
- 网络恢复立即触发但有抖动，避免惊群。
- 认证失败、Credential Revoked 不无限重试；进入明确 Terminal 状态。
- 新连接 READY 后发送 ResumeState。

### 11.2 单写者

同一 Endpoint 同一时间只能有一个 Sequence Allocator/Active Connector。进程级锁和本地 Instance Lease 防止用户误启动两个 ABA 进程共享同一身份。第二实例默认退出，而不是并行发送。

## 12. Wire Router

Ingress：

```text
length limit
-> protobuf decode
-> version/type/field validation
-> connection fencing for control
-> credential/revision/signature
-> sequence/replay window
-> AEAD decrypt (encrypted frame)
-> journal/session dispatch
```

Egress：

```text
session output/control
-> allocate IDs and sequence
-> build canonical AAD/transcript
-> encrypt/sign
-> persist outbound if required
-> bounded send queue
```

任何验证失败都不能把原 Packet/明文打印到日志。

## 13. Local Policy Registry

Platform 只看到本地资源摘要。真实配置由 ABA 决定：

```rust
struct RuntimeProfile {
    id: RuntimeId,
    command: PathBuf,
    args: Vec<OsString>,
    env_allow: BTreeSet<OsString>,
    limits: RuntimeLimits,
}

struct WorkspaceProfile {
    id: WorkspaceId,
    canonical_path: PathBuf,
    allowed_runtimes: BTreeSet<RuntimeId>,
    follow_symlinks: bool,
}
```

OpenTunnel 验证：

- ID 存在、启用。
- Workspace 允许 Runtime。
- Platform Authorization Revision 不回滚。
- Session/HC Endpoint 在授权控制消息中匹配。
- 并发和资源配额未超过。
- 本地路径仍在允许边界内。

禁止从远端消息填充 Command、Args、Env、Cwd。

## 14. ACP Process Supervisor

### 14.1 生命周期

```text
ALLOCATED
 -> SPAWNING
 -> HANDSHAKING
 -> RUNNING
 -> STOPPING
 -> EXITED

SPAWNING/HANDSHAKING/RUNNING -> FAILED
```

- 每个 Session 默认独立 ACP 子进程，除非 Runtime Profile 明确支持安全复用。
- 使用独立进程组/Job Object。
- stdout/stdin 只用于 ACP Transport；stderr 作为受限诊断日志，必须脱敏和限流。
- 启动超时、握手超时、空闲超时、关闭超时均可配置且有上限。
- ABA 退出时先停止接收新请求，再取消 Session、发送终止信号并强制回收。
- 子进程不能继承 ABA 的 Endpoint 私钥、Refresh Token 或不必要文件描述符。

### 14.2 环境

以最小环境启动：

- 只传本地 allowlist。
- 不传 ABA 身份秘密。
- Workspace 作为本地 CWD，由 Profile 决定。
- 容器/Kubernetes 可进一步使用用户、seccomp、只读文件系统和资源限额。

## 15. 官方 ACP SDK 集成

- 使用 `agent-client-protocol` 2.0.x 的稳定 v1 Builder/Proxy API。
- 保持 SDK `TransportFrame` 的 Batch-aware 边界。
- ABA 的网络加密层在 ACP Transport 边界之外，不修改 ACP Schema。
- ACP draft v2 只能在独立 Cargo Feature（例如 `experimental-acp-v2`）下编译，默认关闭，并且不能影响 v1 Wire 测试。
- 对 SDK 升级记录 API 迁移、协议兼容和测试结果。

## 16. Session Manager

每个 Session 记录：

```text
session_id
channel/participant
runtime_id / workspace_id
authorization_revision
ACP process handle
state
active/pending key generation
inbound/outbound sequence state
last activity
```

状态：

```text
OPENING -> ACP_HANDSHAKE -> KEYING -> READY -> ACTIVE
ACTIVE -> PAUSED -> ACTIVE
ACTIVE -> UNCERTAIN
* -> CLOSING -> CLOSED
* -> FAILED
```

状态转换通过单线程 Actor 或严格锁域串行化，避免多个异步任务同时启动/关闭/轮换同一 Session。

## 17. Session Key Manager

- ABA 生成 SRK。
- 按 HC Endpoint KEM Public Key 生成独立 Key Package。
- 记录 Generation 状态和 Participant ACK。
- 新 Generation 激活后停止用旧 Key 发送。
- Grace 结束清理旧 Direction Key 和不再需要的 SRK。
- 锁内存能力可用时启用；Secret 类型 Drop 时 Zeroize。
- Trust/Participant 状态变化时重新授权，不能只根据旧 Key Package 继续新增连接。

## 18. Journal

### 18.1 目的

Journal 不是会话历史数据库，只用于：

- 防止同一加密 Frame 重复交给本地 ACP Agent。
- 持久化 Sequence/Replay Cursor。
- 在崩溃恢复后识别 UNCERTAIN。
- 保存有限未确认出站 Frame。

### 18.2 表

```text
meta(key, value)
channels(channel_id, direction, next_sequence, highest_received, key_generation)
inbound_messages(message_id, channel_id, sequence, state, payload_digest, updated_at)
outbound_frames(message_id, channel_id, sequence, encrypted_packet, acked, created_at)
sessions(session_id, state, runtime_id, workspace_id, process_marker, updated_at)
```

不保存 ACP 明文长期历史。`payload_digest` 只用于一致性检测，不能替代签名。

### 18.3 事务顺序

入站：

1. 验签解密。
2. 事务插入 `RECEIVED`；重复唯一键返回已有状态。
3. 提交后向 Platform ACK。
4. 事务更新 `DISPATCH_STARTED`。
5. 交给 ACP SDK。
6. 成功接受后更新 `DISPATCHED`。
7. 响应完成更新 `RESPONDED`。

崩溃恢复发现 `DISPATCH_STARTED` 且无法证明未执行：改为 `UNCERTAIN`。

出站：

1. 事务分配 Sequence。
2. 构造并加密签名。
3. 事务保存完整不可变 Encrypted Packet。
4. 提交后发送。
5. 收到 ACK 后标记并按策略清理。

### 18.4 上限

- 默认 64 MiB。
- 达到软阈值先清理 ACK/已完成记录。
- 达到硬阈值拒绝新 Session/请求，并保持控制通道用于关闭和诊断。
- 禁止无界 WAL 增长；定期 Checkpoint。

## 19. 取消与权限请求

- ACP Notification（例如 cancel）按协议原样桥接。
- 本地 Agent 权限请求经加密 ACP 返回 HC。
- ABA 不代表用户自动批准未知权限。
- 本地 Profile 可配置不可突破的硬禁止项，即使 HC 请求允许也拒绝。
- HC 的“本次允许”不自动转化为 ABA 永久本地策略。

## 20. 可观测性

### 20.1 日志

结构化字段：

```text
endpoint_id (safe text)
connection_id
session_id
channel_id
state transition
stable error code
sequence range
packet size
```

禁止日志：Token、Ticket、Device Code、私钥、Key Package 明文、SRK、ACP 明文、真实 Workspace 路径和 Runtime 环境值。

### 20.2 本地指标

- Connector 状态/重连次数。
- Active Session/进程数。
- Inflight、ACK 延迟、Replay 数。
- Journal 容量。
- Credential/Manifest 到期。
- Crypto/Signature/Sequence 错误分类。

默认只把聚合指标上报 Platform，不上报敏感内容。

## 21. 部署模式

### 21.1 个人电脑

- 用户级服务启动。
- 使用系统 Keychain。
- 配置位于用户目录，权限最小。
- 不要求管理员权限。

### 21.2 Linux 服务器

- systemd Service。
- 独立低权限用户。
- `ProtectSystem`、`NoNewPrivileges` 等加固选项。
- Workspace 通过组权限显式授权。

### 21.3 Docker

- 非 root。
- 只挂载需要的 Workspace、配置和状态目录。
- Secure Store/密钥通过受保护 Volume 或外部 Secret Provider。
- 不挂载 Docker Socket。

### 21.4 Kubernetes

- 独立 Deployment/Pod 或与 Agent 同 Pod 的 Sidecar。
- ServiceAccount 最小权限。
- 不需要 Service 暴露入站端口。
- NetworkPolicy 只允许 Platform 出站和必要依赖。
- Workspace PVC 只按需求挂载。

## 22. 升级

MVP 可先手动替换二进制。后续自动更新必须：

- Platform 提供签名 Manifest。
- ABA 验证版本、目标平台、哈希和发布签名。
- 下载到临时文件并原子替换。
- 支持健康检查和回滚。
- 不在 ACP Session 活跃时无提示强制更新。
- 最低版本策略不能成为 Platform 任意代码执行通道。

## 23. ABA 最小验收

- 无入站监听端口。
- Enrollment 私钥只在本地生成并保存。
- DPoP、Ticket、Challenge 可连接真实 Platform。
- AWP v1 Golden Vector 全部通过。
- Runtime/Workspace 未知 ID 和远端任意命令字段被拒绝。
- 能启动测试 ACP Agent并保持 Batch 语义。
- 断线重连按原 Frame 重放。
- 重复 Frame 不重复派发。
- DISPATCH_STARTED 崩溃恢复为 UNCERTAIN。
- Journal 达到上限时安全降级而非磁盘无限增长。
- 吊销/过期/Manifest 回滚导致连接关闭或拒绝。
- 退出时 ACP 子进程组全部回收。
- 日志秘密扫描为零。
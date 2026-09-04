# ABA Wire Protocol（AWP）v1 设计

- **状态**：Accepted for initial implementation
- **Wire Major**：1
- **Wire Minor**：0
- **WebSocket Subprotocol**：`mss.awp.v1`
- **ACP Payload**：稳定 ACP v1 JSON-RPC，不透明传输

## 1. 目标

AWP 是 HC、Platform 和 ABA 之间的外层协议。它不替代 ACP，而是提供：

- Endpoint 身份绑定。
- WebSocket 连接挑战与 Fencing。
- 版本协商。
- Session/Channel 路由。
- ACP JSON-RPC 应用层加密。
- Frame 签名、序列、防重放。
- ACK、断线恢复和离线重放。
- Key Package、轮换和连接控制。
- 大小、窗口、背压和错误语义。

AWP 不理解 ACP Method，不改变 ACP Request ID，不拆解 ACP Batch，不重新构造 ACP JSON-RPC。

## 2. 传输

### 2.1 Bootstrap

Enrollment、Token、Trust Manifest、Ticket 和普通管理 API 使用 HTTPS/TLS 1.3。

### 2.2 长连接

数据面使用 WSS/TLS 1.3。一个 WebSocket Binary Message 承载一个完整 `WirePacket`。WebSocket 自身可以分片，但应用层收到后必须形成一个完整 Packet。

禁止：

- 明文 `ws://` 生产连接。
- Text Frame 承载业务包。
- URL Query 携带长期 Token。
- 一个 WebSocket Message 拼接多个未加长度边界的 Packet。

### 2.3 子协议与 Ticket

客户端发送：

```text
Sec-WebSocket-Protocol: mss.awp.v1, mss.ticket.<base64url-ticket>
```

Ticket 原子单次消费。服务端选择 `mss.awp.v1` 作为响应子协议，不回显 Ticket 子协议。

## 3. ID 与字节序

- `message_id`、`channel_id`、`session_id`、`endpoint_id`、`key_id` 使用 16 字节标识，推荐 ULID/UUIDv7 的 128-bit 表示。
- Wire 中使用原始 16 字节，不使用可变大小文本。
- 日志和 API 展示时使用规范化文本形式。
- 整数 Canonical Encoding 使用无符号大端。
- Sequence 从 1 开始；0 表示未分配/不适用。
- 全零 16 字节 ID 只允许在协议明确标记为“不适用”的连接级消息中出现。

## 4. 顶层 Packet

Schema 以 Protobuf 3 定义，逻辑结构：

```protobuf
message WirePacket {
  uint32 wire_major = 1;
  uint32 wire_minor = 2;
  bytes packet_id = 3;          // exactly 16 bytes

  oneof body {
    ServerChallenge server_challenge = 10;
    ChallengeResponse challenge_response = 11;
    ConnectionReady connection_ready = 12;
    ControlFrame control = 13;
    EncryptedFrame encrypted = 14;
    AckFrame ack = 15;
    ErrorFrame error = 16;
  }
}
```

顶层 Packet 只负责类型边界。不同 Body 拥有独立签名 Transcript。Protobuf 的序列化字节不能直接作为安全签名 Canonicalization；安全输入按本文定义单独编码。

## 5. 连接状态机

```text
DISCONNECTED
   │ TCP/TLS/WSS
   ▼
UPGRADED
   │ ServerChallenge
   ▼
CHALLENGED
   │ valid ChallengeResponse
   ▼
READY
   │ drain / revoke / replace / close
   ▼
DRAINING
   ▼
CLOSED
```

### 5.1 ServerChallenge

```protobuf
message ServerChallenge {
  bytes connection_id = 1;               // 16
  uint64 connection_generation = 2;
  bytes server_nonce = 3;                 // 32
  int64 server_time_ms = 4;
  uint64 trust_manifest_revision = 5;
  uint64 credential_status_revision = 6;
  bytes server_signature = 7;
}
```

服务端签名 Transcript：

```text
"mss-awp-server-challenge-v1"
|| connection_id
|| u64be(connection_generation)
|| server_nonce
|| i64be(server_time_ms)
|| u64be(trust_manifest_revision)
|| u64be(credential_status_revision)
|| endpoint_id_from_ticket
```

Endpoint 使用 Trust Manifest 中的在线服务签名公钥验证。

### 5.2 ChallengeResponse

```protobuf
message ChallengeResponse {
  bytes connection_id = 1;
  uint64 connection_generation = 2;
  bytes endpoint_id = 3;
  bytes credential_serial = 4;           // 16
  bytes client_nonce = 5;                 // 32
  uint64 last_manifest_revision = 6;
  uint64 last_credential_status_revision = 7;
  bytes endpoint_signature = 8;           // ES256 raw R||S
}
```

签名 Transcript：

```text
"mss-awp-client-challenge-v1"
|| connection_id
|| u64be(connection_generation)
|| server_nonce
|| client_nonce
|| endpoint_id
|| credential_serial
|| utf8(selected_subprotocol)
|| u64be(last_manifest_revision)
|| u64be(last_credential_status_revision)
```

### 5.3 ConnectionReady

```protobuf
message ConnectionReady {
  bytes connection_id = 1;
  uint64 connection_generation = 2;
  bytes fencing_token = 3;                // 32 random bytes
  int64 ready_at_ms = 4;
  uint32 max_packet_bytes = 5;
  uint32 max_inflight_frames = 6;
  uint32 heartbeat_interval_ms = 7;
  bytes server_signature = 8;
}
```

服务端签名 Transcript：

```text
"mss-awp-connection-ready-v1"
|| connection_id
|| u64be(connection_generation)
|| fencing_token
|| i64be(ready_at_ms)
|| u32be(max_packet_bytes)
|| u32be(max_inflight_frames)
|| u32be(heartbeat_interval_ms)
|| endpoint_id_from_ticket
```

同一 Endpoint 的新 READY 连接创建更高 `connection_generation`。Platform 标记旧连接 DRAINING，并拒绝旧连接产生新的控制动作。数据 Frame 可以按原始字节在新连接重放，但发送端不能让两个连接并行分配新 Sequence。

## 6. 控制 Frame

```protobuf
message ControlFrame {
  bytes message_id = 1;          // 16
  bytes sender_endpoint_id = 2;  // Platform control identity uses reserved ID
  bytes receiver_endpoint_id = 3;
  uint64 control_sequence = 4;
  int64 created_at_ms = 5;
  ControlType type = 6;
  bytes payload = 7;             // type-specific protobuf
  bytes signature = 8;
}
```

控制消息由发送主体签名，签名 Transcript：

```text
"mss-awp-control-v1"
|| message_id
|| sender_endpoint_id
|| receiver_endpoint_id
|| u64be(control_sequence)
|| i64be(created_at_ms)
|| u32be(control_type)
|| u32be(payload_length)
|| payload
```

Platform 控制消息使用 Trust Manifest 中声明的 Online Control Signing Key。Endpoint 控制消息使用自己的 Endpoint Signing Key。

### 6.1 ControlType

首发类型：

```text
HEARTBEAT
HEARTBEAT_ACK
RESUME_STATE
TRUST_MANIFEST_HINT
CREDENTIAL_STATUS_HINT
OPEN_TUNNEL_REQUEST
OPEN_TUNNEL_RESULT
CLOSE_TUNNEL_REQUEST
CLOSE_TUNNEL_RESULT
SESSION_KEY_PACKAGE
SESSION_KEY_PACKAGE_ACK
ROTATE_SESSION_KEY_REQUEST
ROTATE_SESSION_KEY_STATUS
ENDPOINT_DRAIN
```

未知 ControlType 必须返回 `UNSUPPORTED_CONTROL_TYPE`，不能忽略安全相关动作。

### 6.2 OpenTunnelRequest

Platform 到 ABA：

```protobuf
message OpenTunnelRequest {
  bytes session_id = 1;
  bytes hc_endpoint_id = 2;
  string runtime_profile_id = 3;
  string workspace_id = 4;
  uint64 authorization_revision = 5;
  uint64 requested_key_generation = 6;
  repeated string requested_acp_capabilities = 7;
  int64 expires_at_ms = 8;
}
```

协议层禁止包含 `command`、`args`、`cwd`、`env`、脚本、二进制或任意路径。ABA 只按本地配置映射两个 ID。

### 6.3 OpenTunnelResult

```protobuf
message OpenTunnelResult {
  bytes session_id = 1;
  OpenTunnelStatus status = 2;
  string stable_error_code = 3;
  uint64 accepted_authorization_revision = 4;
  uint64 active_key_generation = 5;
  repeated string negotiated_capability_hints = 6;
}
```

错误文本不包含本地真实路径、命令、环境变量或 ACP Payload。

### 6.4 SessionKeyPackage

```protobuf
message SessionKeyPackage {
  bytes session_id = 1;
  uint64 key_generation = 2;
  bytes issuer_aba_endpoint_id = 3;
  bytes recipient_hc_endpoint_id = 4;
  string crypto_suite = 5;
  bytes hpke_enc = 6;
  bytes hpke_ciphertext = 7;
  int64 not_before_ms = 8;
  int64 expires_at_ms = 9;
  bytes issuer_signature = 10;
}
```

`hpke_ciphertext` 的明文由安全文档定义。Platform 可以保存、路由、验证 ABA 签名，但不能解封。

### 6.5 ResumeState

```protobuf
message ResumeState {
  repeated ChannelCursor cursors = 1;
}

message ChannelCursor {
  bytes channel_id = 1;
  Direction direction = 2;
  uint64 highest_contiguous_sequence = 3;
  repeated SequenceRange received_ranges = 4;
  uint64 key_generation = 5;
}
```

每次 Resume 限制 Cursor 数和 Range 数，防止超大控制包。

## 7. 加密 ACP Frame

```protobuf
message EncryptedFrame {
  uint32 crypto_suite_id = 1;
  FrameType frame_type = 2;
  uint32 flags = 3;

  bytes message_id = 4;           // 16
  bytes channel_id = 5;           // 16
  bytes session_id = 6;           // 16
  bytes sender_endpoint_id = 7;   // 16
  bytes receiver_endpoint_id = 8; // 16

  Direction direction = 9;
  uint64 sequence = 10;
  uint64 key_generation = 11;
  bytes key_id = 12;               // 16
  int64 created_at_ms = 13;

  bytes ciphertext = 14;
  bytes signature = 15;            // ES256 raw R||S, 64 bytes
}
```

### 7.1 FrameType

首发：

```text
ACP_TRANSPORT_FRAME = 1
```

后续 Artifact、Padding 等功能必须增加新类型和能力协商，不能复用 ACP 类型偷偷改变明文格式。

### 7.2 Direction

```text
HC_TO_ABA = 1
ABA_TO_HC = 2
```

Direction 与实际 Sender/Receiver 类型必须一致。Platform 和接收端都验证。

### 7.3 Flags

v1.0：

```text
bit 0: ACP_BATCH       // payload 是一个 JSON-RPC batch value
bit 1: RETRANSMISSION  // 禁止发送端修改原 Frame；仅网关内部/接收统计提示
bits 2..15: reserved non-critical
bits 16..31: critical; unknown set bit causes rejection
```

`RETRANSMISSION` 不应在原始签名 Frame 上被 Platform 修改。实际实现可把“本次是重放”放在 WebSocket 传输元数据或单独 Delivery Envelope 中，而不是改 EncryptedFrame。最终 Proto 实现应保持 EncryptedFrame 完全不可变。

## 8. Canonical AAD v1

为避免不同语言 Protobuf 序列化差异，AAD 使用固定 128 字节编码：

```text
offset size field
0      4    ASCII "AWP1"
4      2    wire_major u16be
6      2    wire_minor u16be
8      2    crypto_suite_id u16be
10     2    frame_type u16be
12     4    flags u32be
16     16   message_id
32     16   channel_id
48     16   session_id
64     16   sender_endpoint_id
80     16   receiver_endpoint_id
96     1    direction
97     7    zero reserved bytes
104    8    sequence u64be
112    8    key_generation u64be
120    16   key_id
136    8    created_at_ms i64be
144    4    ciphertext_length u32be
```

上述实际总长度为 **148 字节**。实现必须把 `AAD_V1_LENGTH = 148` 写入共享协议常量并测试每个 Offset；文档中不得使用含糊的“序列化 Header”。保留位必须全零，非零拒绝。

> 注意：最初设计草案曾口头称固定 128 字节；本文的逐字段计算是权威值 148 字节。代码和测试向量必须使用 148。

AAD 不包含 Signature 和 Ciphertext 本身，但包含 Ciphertext Length。

## 9. AEAD 与签名

### 9.1 Nonce

```text
nonce = nonce_prefix[4] || u64be(sequence)
```

`nonce_prefix` 来自 Key Package，每个 Endpoint、Direction、Generation 独立。

### 9.2 Ciphertext

```text
ciphertext = AES-256-GCM-Seal(
  key = direction_key,
  nonce = nonce,
  plaintext = exact ACP transport frame bytes,
  aad = canonical_aad_v1
)
```

### 9.3 Signature

```text
digest = SHA256(
  ASCII "mss-awp-frame-signature-v1"
  || canonical_aad_v1
  || ciphertext
)

signature = ECDSA-P256(digest)
```

Wire Signature 使用固定 64 字节 IEEE P1363 `R || S`，要求 Low-S Canonicalization。验证端拒绝长度错误、High-S、无效点和非规范签名。

### 9.4 验证顺序

接收端：

1. 验证 Packet/Frame 长度与字段尺寸。
2. 验证 Major/Minor、Suite、Type 和 Critical Flags。
3. 验证 Sender/Receiver/Direction/Session/Channel 关系。
4. 验证 Endpoint Credential 和吊销 Revision。
5. 构造 Canonical AAD。
6. 验证 Frame Signature。
7. 验证 Key Generation、Sequence 和 Replay Window。
8. AEAD Open。
9. 验证明文是单个完整 UTF-8 JSON value；不做业务方法解析。
10. 按 Journal/HC Inbox 语义持久化后 ACK。

错误响应不得回显 Ciphertext 或解密后的明文。

## 10. ACP Payload 语义

`ACP_TRANSPORT_FRAME` 明文是官方 Rust SDK `TransportFrame` 在线路边界对应的一个完整 JSON 值 UTF-8 字节：

- 单个 JSON-RPC Request、Response 或 Notification；或
- 一个 JSON-RPC Batch Array。

必须：

- 保持完整字节和 Batch 边界。
- 不改变 Request ID 类型和值。
- 不对 JSON 重排字段再转发。
- 不把 Notification 变成 Request。
- 不合并原本独立的 JSON 值。

ABA 可以在交给 SDK 前执行严格 UTF-8/JSON 边界校验，但 Platform 不执行 ACP 内容解析。

## 11. Sequence 与 Channel

- 每个 Session Participant Pair 拥有一个 `channel_id`。
- HC→ABA 与 ABA→HC 各有独立 Sequence 空间和 Direction Key。
- Sequence 在创建 Ciphertext 前事务性分配；分配后即使发送失败也不回收。
- 允许 Sequence 空洞，不允许复用。
- Receiver 窗口默认 1024，可协商更小，服务端可施加上限。
- 超过窗口的未来 Sequence 返回 `SEQUENCE_WINDOW_EXCEEDED`。
- 低于已清理窗口的 Frame 返回/记录 `STALE_REPLAY`，不再次交付。

## 12. ACK

```protobuf
message AckFrame {
  bytes ack_id = 1;                 // 16
  bytes channel_id = 2;
  bytes session_id = 3;
  bytes endpoint_id = 4;            // ACK sender
  Direction acknowledged_direction = 5;
  uint64 highest_contiguous_sequence = 6;
  repeated SequenceRange received_ranges = 7;
  uint64 key_generation = 8;
  int64 created_at_ms = 9;
  bytes signature = 10;
}

message SequenceRange {
  uint64 start = 1;
  uint64 end = 2; // inclusive
}
```

限制：

- Ranges 排序、互不重叠、都高于 Highest Contiguous。
- 最多 32 个 Range。
- ACK 只能单调前进；回退值忽略并记录。
- ACK Signature Transcript 包含所有字段的固定编码和 Range 列表。

### 12.1 ACK 时机

HC：成功验签、解密并将明文放入可恢复 Inbox/状态机后 ACK。

ABA：成功验签、解密，并将 Message 以 `RECEIVED` 状态事务性写入本地 Journal 后 ACK。ACK 不表示 ACP Agent 已完成执行。

Platform：保存 Endpoint ACK 后，按照保留策略将 Frame 标记可清理；不能把 WSS Socket Write 成功当成 ACK。

## 13. 交付保证

AWP 提供：

- Platform 到 Endpoint 的 **at-least-once encrypted frame delivery**。
- Endpoint 基于 Channel/Sequence/Message ID 的 **deduplicated intake**。
- 不承诺本地 ACP 副作用的理论 exactly-once。

ABA Journal：

```text
RECEIVED
  -> DISPATCH_STARTED
  -> DISPATCHED
  -> RESPONDED

DISPATCH_STARTED + crash + unknown result
  -> UNCERTAIN
```

- `RECEIVED` 可恢复后安全派发。
- `DISPATCHED` 不再次派发相同消息。
- `UNCERTAIN` 不自动重试，向 HC 产生明确状态。
- ACP Response 作为新加密 Frame 返回，拥有自己的 Message ID/Sequence。

## 14. 重连与重放

1. 新连接完成 Ticket、Challenge 和 READY。
2. Endpoint 发送 `ResumeState`。
3. Platform 验证 Cursor 单调性和 Endpoint/Session 权限。
4. Platform 从 `highest_contiguous + gaps` 计算缺失 Frame。
5. 重放完全相同的 EncryptedFrame 字节，不改 ID、Sequence、AAD、Ciphertext 或 Signature。
6. Receiver 重复 Frame 只更新统计/ACK，不再次交付。

如果 Platform 已丢失 Endpoint 声称缺失的 Frame，返回 `REPLAY_DATA_UNAVAILABLE`，Session 进入 Degraded/需要用户处理，不能伪造空成功。

## 15. Heartbeat

Heartbeat 是签名控制消息，包含：

- Connection ID/Generation。
- 单调本地计数器。
- ABA 进程健康摘要。
- Active Session 数。
- Journal 使用率分桶。
- 当前 Manifest/Credential Revision。

不包含文件树、环境变量、命令、Prompt 或资源明细。Heartbeat Ack 提供服务端时间和下一次间隔。

## 16. 大小和配额

默认：

```text
max WirePacket:           1 MiB
max ciphertext:           1 MiB - envelope overhead
max control payload:      64 KiB
max ranges per ACK:       32
max cursors per resume:   256
max inflight per channel: 1024
```

最终值由 Server Ready 下发，但不能超过客户端编译时安全上限。超过限制在分配大内存和解密前拒绝。

AWP v1 不提供跨消息压缩。大文件/制品未来使用独立加密 Artifact Protocol。

## 17. Backpressure

- Sender 不得超过 `max_inflight_frames`。
- Platform 对 Endpoint、Session、Tenant 有独立积压和速率限制。
- Receiver 慢时 Platform 停止继续推送而不是无限缓存到内存。
- 积压超过持久化配额时拒绝新 Session/新高成本 Frame，并返回稳定错误。
- 控制消息保留最小优先通道，防止数据面拥塞阻塞吊销和关闭。

## 18. ErrorFrame

```protobuf
message ErrorFrame {
  bytes error_id = 1;
  bytes related_message_id = 2;
  ErrorCode code = 3;
  bool retryable = 4;
  uint32 retry_after_ms = 5;
  string safe_message = 6;
  bytes server_or_endpoint_signature = 7;
}
```

首发稳定错误码：

```text
INVALID_PACKET
UNSUPPORTED_WIRE_VERSION
UNSUPPORTED_CRYPTO_SUITE
UNSUPPORTED_CONTROL_TYPE
UNKNOWN_CRITICAL_FLAG
UNAUTHORIZED_ENDPOINT
CREDENTIAL_EXPIRED
CREDENTIAL_REVOKED
POLICY_REVISION_STALE
TICKET_INVALID
CHALLENGE_FAILED
SESSION_NOT_FOUND
SESSION_NOT_AUTHORIZED
CHANNEL_NOT_FOUND
RUNTIME_NOT_ALLOWED
WORKSPACE_NOT_ALLOWED
KEY_GENERATION_STALE
KEY_PACKAGE_REQUIRED
SIGNATURE_INVALID
AEAD_FAILED
SEQUENCE_REPLAY
SEQUENCE_WINDOW_EXCEEDED
ACK_INVALID
REPLAY_DATA_UNAVAILABLE
BACKPRESSURE
QUOTA_EXCEEDED
LOCAL_DISPATCH_UNCERTAIN
INTERNAL_TEMPORARY
```

`safe_message` 不包含内部堆栈、路径、SQL、密钥或 Payload。客户端逻辑依赖 ErrorCode，不解析文字。

## 19. Session 状态机

Platform 权威状态：

```text
REQUESTED
 -> AUTHORIZED
 -> OPENING
 -> KEYING
 -> READY
 -> ACTIVE
 -> PAUSED
 -> CLOSING
 -> CLOSED

OPENING/KEYING/ACTIVE -> FAILED
ACTIVE -> UNCERTAIN
UNCERTAIN -> CLOSED or user-approved recovery path
```

ABA 本地状态与 Platform 状态通过 Control Sequence/Revision 对齐。Platform 不能在没有 ABA 确认时把 OPENING 直接标成 ACTIVE。

## 20. Key Generation 状态机

```text
PENDING
 -> DISTRIBUTING
 -> READY
 -> ACTIVE
 -> GRACE
 -> RETIRED
 -> DESTROYED
```

同一 Session 最多一个 `ACTIVE`、最多一个 `PENDING/DISTRIBUTING/READY` 下一代。数据库唯一约束必须保证。旧代进入 GRACE 后不能发送新 Frame。

## 21. 版本兼容

### 21.1 Major

- 子协议中声明 Major。
- 未支持 Major 在 WSS 升级或首包阶段拒绝。
- 破坏 AAD、Frame、状态或语义的变更进入新 Major。

### 21.2 Minor

- Minor 只允许增加通过能力协商启用的非破坏字段或消息。
- 未知 Critical Flag 拒绝。
- 未知 Control/Frame Type 拒绝，除非协商明确标记为可忽略扩展。
- Sender 使用双方支持的最小兼容 Minor。

### 21.3 ACP 版本

AWP Version 与 ACP Version 独立。首发 AWP v1 可以中继 ACP v1。未来中继 ACP v2 必须经过能力协商和实验开关，不改变既有 ACP v1 Payload 语义。

## 22. 测试向量

`protocol/vectors/awp/v1/` 必须包含：

- Canonical AAD 每字段 Offset 和最终 Hex。
- HKDF 固定输入/输出。
- HPKE Key Package 封装/解封。
- AES-GCM Frame 明文/密文/Tag。
- Low-S ES256 Frame Signature。
- Control/Ack Transcript 和签名。
- 单请求、Notification、Response 和 Batch Payload。
- 错误长度、High-S、篡改 AAD、重复 Nonce、旧 Generation、Sequence Replay。
- Rust、Go、TypeScript 读取同一向量的互操作测试。

固定测试私钥只能用于公开测试向量，明确标记 `TEST ONLY`。

## 23. 协议变更门禁

任何修改以下内容的 PR 必须同时修改 Schema、本文、测试向量和三端兼容测试：

- 字段编号、长度和 ID 表示。
- Canonical AAD。
- 签名 Transcript。
- Nonce、Sequence、ACK 或 Replay 语义。
- Frame/Control/Error 类型。
- Crypto Suite。
- Session/Key 状态机。
- 大小与 Critical Extension 规则。

未完成互操作验证前，不得把协议状态标记为 Verified。

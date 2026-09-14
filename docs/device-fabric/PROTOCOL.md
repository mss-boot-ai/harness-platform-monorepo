# MDP 消息、合约与可靠性设计

- 状态：Draft，设计修订 0.1，2026-09-13。
- 拟议 namespace：`mss.device.v1`；拟议 WSS subprotocol：`mss.mdp.v1`。
- 本文不是已发布的 wire 兼容承诺。正式 Schema 和测试向量在实施切片中生成，不手写生成文件。

## 1. 传输取舍

首版 HTTPS 负责注册/授权/发现，WSS 承载双向小消息。内部使用 Protobuf，MCU 采用 bounded/nanopb-compatible 子集：定长 ID、限长 bytes/string、有限 repeated，不使用无界递归、任意 Any 或通用 Struct。JSON 仅用于管理接口、受限参数 schema 和诊断示例。

`protocol/proto/mss/awp/v1/*` 不移动、不改字段语义。新文件在 `protocol/proto/mss/device/v1/`，初始拆为 `wire.proto`、`contracts.proto`、`interaction.proto`；不为目录整洁提前抽旧 AWP 的 common 类型。

## 2. WirePacket 合同

拟议字段号：major=1、minor=2、packet_id=3；oneof 为 server_challenge=10、challenge_response=11、ready=12、control=13、encrypted=20、receipt=21、error=22。编号属于提案，发布前生成兼容快照。

连接状态：

```text
UNPAIRED -> ENROLLED -> TICKET_PENDING -> CONNECTING
 -> CHALLENGED -> READY -> RECONNECTING
 -> SUSPENDED / REVOKED / CLOSED
```

HTTPS 短期 Ticket 绑定 Endpoint、凭据 serial、purpose=`mdp`、client family、浏览器 Origin（适用时）、允许版本和过期时间。Ticket 只可消费一次，不能放 URL 或普通日志。首版建议有效期 30 秒。

WSS 后由服务端发有签名的 challenge，包含连接 ID/generation、随机 nonce、时间、版本与资源限额。客户端验证 Trust Manifest 后签回服务端 challenge 摘要、双方 ID、client nonce 和选择参数；READY 签名绑定完整握手。复用既有 ES256/DPoP/签名编码实现，但所有 transcript 使用 MDP 独立域字符串，不把 AWP Ticket 或签名跨协议使用。

浏览器必须校验 Origin；native/MCU 使用独立机器注册和 proof，不伪造浏览器 Origin。服务端不能仅凭无 Origin 就认定是可信 native client。

## 3. 控制消息与业务消息

控制消息限于 REGISTER_SUMMARY、HEARTBEAT、PRESENCE、CREDENTIAL_STATUS、GRANT_REVISION、DRAIN、RESUME 和限额协商。它们不携带业务参数、录音或任务内容。

加密业务消息包括：

| 消息 | 内容与用途 |
| --- | --- |
| descriptor.get / descriptor.result | 获取获授权的完整能力描述 |
| state.read / state.snapshot | 状态与 revision、观测时间、freshness |
| procedure.invoke | 稳定调用 ID、参数、约束、许可版本 |
| invocation.accepted / invocation.updated / invocation.result | 业务接收、执行状态、终态结果 |
| invocation.query / invocation.cancel | 查询与尽力取消，不自动回滚 |
| event.publish / event.subscribe | 授权事件、序号、有效期、订阅约束 |
| projection.update | 面向指定终端的受限任务视图 |
| interaction.submit / interaction.cancel_requested / interaction.choice | 有 ActorLease 的人类意图 |
| approval.present / approval.decision | 专用审批事务，禁止经通用 Tool 合成决策 |
| resource.manifest / resource.available | 加密资源描述及引用 |

每个消息都绑定发送/接收 Endpoint、Device、binding_epoch、授权上下文和期限。消息内的 user/role 声明不构成身份，必须与签名凭据和服务端/本地授权匹配。

## 4. EncryptedEnvelope 与端到端保护

首版使用 RFC 9180 single-shot HPKE，而不把 ACP Session Root Key 共享给设备。HPKE 选型沿现有 Suite 0001 原语：DHKEM(P-256, HKDF-SHA256)、HKDF-SHA256、AES-256-GCM；另以独立 P-256 Signing Key 做 ES256/P1363 low-S 签名。具体实现必须使用维护中的库并通过交叉向量，不自写椭圆曲线原语。

外层字段：

- protocol major/minor、crypto_profile、message_class、critical_flags。
- 随机 `message_id`、`tenant_route_id`、`sender_endpoint_id`、`recipient_endpoint_id`、`device_id`、`grant_id`：各 16 bytes；没有值时全零且须由对应消息规则允许。
- `sender_credential_id`、`recipient_credential_id`：各 16 bytes。
- `binding_epoch`、`created_at_ms`、`expires_at_ms`：uint64。
- `hpke_enc`：P-256 uncompressed，65 bytes；`ciphertext`：受限长度；`signature`：64 bytes。

AAD 明确编码为：ASCII `MSS-MDP-ENVELOPE-V1\0`，随后按上述顺序拼接 major/minor/profile/class（各 uint16 BE）、flags（uint32 BE）、9 个固定 16-byte ID、3 个 uint64 BE。ID 顺序固定为 message、tenant_route、sender、recipient、device、grant、sender_credential、recipient_credential、reserved_context_id。`reserved_context_id` 首版必须全零，未来用途须新 profile；它不是 ACP session_id。所有字段必须在分配密文缓冲区前做长度/取值校验。

HPKE `info` 为独立字节字符串 `MSS-MDP-HPKE-V1`，`aad` 为上述 AAD。每条新 envelope 使用新 encapsulation，并只 seal 一次。签名输入为 `MSS-MDP-SIG-V1\0 || AAD || hpke_enc || uint32_be(ciphertext_length) || ciphertext`；由 ES256 按规范哈希并签名，不额外不一致地重复哈希。禁止以 Protobuf 序列化字节是否 deterministic 来代替签名规范。

消息加密后先持久化原始 envelope，再发送。重试重发相同 bytes，不换 message_id 或重加密。接收方验证签名、证书/绑定/授权/期限、HPKE 解密、内外字段一致性、重复消息和业务幂等。

HPKE 自身不提供业务去重、身份授权或消息顺序。本 profile 必须与下述 Journal、绑定 epoch、期限以及 SECURITY 文档共同使用。静态接收方私钥以后失陷可能解密留存密文，不能宣传完全前向保密。

## 5. 重连与投递

不用把 AWP 的方向 sequence 强行移植进 MDP。平台为路由收件箱提供不透明分页游标；`RESUME` 提交最后持久确认位置。Receipt 必须签名并绑定原始 message_id、原 envelope hash、收件 Endpoint 和 binding_epoch；客户端不信任单纯游标来决定操作已执行。

可靠性分三类：

- Reliable：调用、终态结果、审批、交互意图。平台 outbox 重送原 bytes，接收方持久 inbox 去重。
- Latest-value：状态快照/进度。以对象 revision 取最新，不重放过期进度；缺少增量时请求 snapshot。
- Ephemeral：高频采样和未来音频帧。丢失可接受，不占可靠命令队列；首版不开放高频媒体。

`RECEIVED` 只表示 inbox 已持久接受，不能等价为 `RUNNING` 或 `SUCCEEDED`。发送方看不到业务响应时调用 query/reconcile，不创建新 Invocation 重试副作用。

## 6. Invocation 合同

加密 body 至少包含：

```text
invocation_id, device_id, binding_epoch, provider_boot_id
contract_id, contract_version, descriptor_digest
run_id?, actor_lease_id?, grant_id, grant_revision
canonical_argument_bytes, argument_digest
not_before, deadline, expected_state_revision?
execution_policy, approval_evidence?, execution_challenge?
```

参数采用受限 JSON Schema：只允许已知属性，限制字符串字节数、数组长度、嵌套深度及数值范围；不接受 NaN/Infinity 或重复 JSON key。参数摘要以 RFC 8785 JCS 的规范 bytes 计算；对超过安全整数范围的计数使用十进制字符串。签名/审批对准这些准确 bytes，而不是自然语言摘要。

Provider 不接受不存在、旧版本未许可或变化了的合约。`descriptor_digest` 改变时，调用必须重新进行授权检查；不能在旧审批上换参数。

副作用等级是已审批本地合约属性，不相信模型声称 readOnly/idempotent：`read_only`、`idempotent_set`、`non_idempotent`、`safety_critical`。首版只开放前两类的少数低风险能力。

状态机：

```text
CREATED -> AUTHORIZED -> QUEUED -> ACCEPTED -> RUNNING
                                             |-> SUCCEEDED
                                             |-> FAILED
                                             |-> UNKNOWN
CREATED/AUTHORIZED/QUEUED -> DENIED / EXPIRED
ACCEPTED/RUNNING -> CANCEL_REQUESTED -> CANCELLED / UNKNOWN / 原执行终态
```

`EXPIRED` 只用于能够证明尚未开始的调用。发出后超时且不知设备是否开始，必须 UNKNOWN（UI 为结果不确定），不能标为“超时失败，因此安全重试”。FAILED 也可能有部分副作用，结果附加 effect certainty，不能隐含已回滚。

UNKNOWN 可通过设备签名结果或明确人工复核转为已知状态；保留所有历史事件，不覆盖证据。查询不重复执行。

## 7. Provider Journal、重启与互斥

本地 ledger 键为 `(device_id,binding_epoch,invocation_id)`，保存参数 digest、contract digest、状态和结果。先写 `DISPATCH_STARTED` 并落盘，再调用驱动。相同 ID/digest 返回原状态；相同 ID 不同 digest 返回 CONFLICT。

掉电后 `DISPATCH_STARTED` 且无可证明结果即 UNKNOWN。对于有幂等硬件读回语义的 set 动作，可以读回状态进行确认，但不能假装天然 exactly-once。

每次 Provider 启动生成 `provider_boot_id`。需要活体在线执行的调用绑定当前 boot；旧 boot 命令不可在重启后直接执行。副作用/审批使用由 Provider 新产生、短期存于内存并一次消费的 execution challenge，再绑定 Invocation，防止仅靠平台时间重放旧批准。初版断线不自动执行积压的动作，只恢复状态/查询。

互斥组初版至少 `display`、`audio_capture`、`audio_playback`、`approval_surface`。Provider 本地资源锁是最终权威；Bridge 的排队不能绕过设备忙状态。不同设备的多动作不承诺分布式事务，报告部分完成，补偿动作必须独立授权。

## 8. 首版能力合同

| 合同 | 参数界限（建议默认，可配置收紧） | 返回 |
| --- | --- | --- |
| display.card.v1 | title≤96 UTF-8 bytes，body≤2048，最多3个预定义导航动作，TTL≤300s | rendered_revision |
| device.status.read.v1 | 已许可字段集合，不接受任意路径 | snapshot、observed_at、freshness |
| haptic.pulse.v1 | duration 1～500ms，冷却≥1s | completed |
| interaction.submit.v1 | 文本≤4096 bytes，必须有 ActorLease | accepted_intent_id |
| interaction.cancel.v1 | 指定已授权 Run，绑定控制租约 | requested，不宣称已停止 |

`approval.present` 是系统专用呈现消息，不是通用 capability Tool。网页链接只使用授权的 HC 路由 token；不执行用户传来的 JavaScript、任意 URL scheme 或固件脚本。

## 9. 默认资源预算

作为首版测试预算而非已测性能：最大 WirePacket 64KiB，普通命令 body≤8KiB，Descriptor≤32KiB/32 capabilities；每连接 reliable inflight≤8，内存发送队列≤256KiB；心跳30s、presence租约90s；单 Device 副作用并发默认1；未发出命令默认期限30s，审批默认60s。

平台每 Endpoint 默认 outbox≤1000条/10MiB；TTL最长24h，动作本身的更短 deadline 优先。设备 dedupe Journal 预算1MiB，活动执行记录不可因容量被删除；容量不足返回 BACKPRESSURE，禁止先执行后再尝试记日志。清理仅移除超过全部重试期限且无活动事务的 tombstone。

上述限制由双方协商取更小值；降级或不支持必须返回稳定错误，不截断密文或静默丢掉 Reliable 消息。MCU 最终预算以真机测量校正。

## 10. 错误和兼容

稳定错误至少：UNSUPPORTED_VERSION、UNSUPPORTED_CONTRACT、UNAUTHORIZED、PERMISSION_REQUIRED、STALE_BINDING、STALE_GRANT、STALE_DESCRIPTOR、STALE_BOOT、DEVICE_OFFLINE、BUSY、EXPIRED、CONFLICT、BACKPRESSURE、RESULT_UNKNOWN、RESOURCE_GONE、CANCEL_NOT_SUPPORTED。

未知 major/profile/critical flag 关闭或拒绝；未知非关键能力不授予权限。schema 新字段只有在协商兼容时接收；安全上下文缺失默认拒绝。不要将空字段理解成 `*` 权限。

Golden vectors 覆盖 AAD、HPKE、签名、Receipt、JCS 和篡改/重放负例；签名源编码与各语言生成绑定一起冻结。Go/Rust/TS 以及 MCU 库必须使用相同输入输出向量。本文的拟议编码在这组向量完成前不得作为生产协议发布。

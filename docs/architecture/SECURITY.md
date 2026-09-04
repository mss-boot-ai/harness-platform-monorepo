# Harness Platform 安全、身份与密钥设计

- **状态**：Accepted security baseline
- **模式**：Opaque Mode 默认；Managed Mode 可选且非 E2EE
- **适用对象**：Platform、ABA、HC、ACP Session

## 1. 安全目标

1. 网络攻击者不能读取或篡改 ACP JSON-RPC。
2. 单独窃取 Access Token、Refresh Token、WebSocket Ticket 或数据库内容不能冒充 Endpoint。
3. ABA 与每个 HC 都能被 Platform 唯一识别、单独授权、轮换和吊销。
4. Platform 可以完成身份、路由、存储、ACK、重放和运营，但默认不能读取 ACP 内容。
5. Platform 被攻破时，攻击者不能通过服务端现有数据直接恢复 Endpoint 私钥或全部历史 SRK。
6. 丢失或失陷一个 HC/ABA 不应自动危及同一用户的其他 Endpoint。
7. 根、端点证书、端点密钥和 Session Key 均有成熟的生成、激活、重叠、轮换、吊销和恢复流程。
8. 不允许 Platform 将 ABA 降级为任意远程 Shell。
9. 消息重复、乱序、回滚和时钟偏差不能导致 Nonce 重用或危险请求静默重复执行。

## 2. 非安全承诺

以下情况超出 Opaque Mode 能单独解决的范围，但系统必须限制影响并明确说明：

- ABA 主机完全失陷后，攻击者可读取该主机内存、Workspace 和当前会话。
- 原生 HC 主机完全失陷后，攻击者可读取该端当前可访问内容。
- Platform 完全失陷并替换 Web HC JavaScript 后，可攻击正在运行的 Web 客户端；因此 Web HC 的 Assurance Level 低于硬件密钥原生客户端。
- 已被吊销 Endpoint 若此前已获得旧历史密钥，无法被密码学强制“忘记”历史内容；吊销保护未来 Generation。
- 流量大小、时间、Endpoint 和 Session 路由等元数据默认仍对 Platform 可见。
- 用户把明文复制到其他系统后，本项目无法控制外部副本。

## 3. 保护资产

### 3.1 最高敏感

- Platform Root/UCA 私钥或 KMS 签名权限。
- ABA/HC Endpoint Signing Private Key。
- ABA/HC Endpoint KEM Private Key。
- SRK 与派生方向密钥。
- Refresh Token、恢复凭据和高可信审批能力。
- ACP 明文：Prompt、代码、Diff、Tool 参数、终端输出和 Agent 响应。

### 3.2 中敏感

- Access Token、DPoP Nonce 状态、WebSocket Ticket。
- Endpoint 证书、Key Package、Session ACL。
- Runtime/Workspace 授权关系。
- 密文 Frame、ACK 和审计事件。

### 3.3 元数据

- Endpoint 名称、平台、版本、IP、在线时间。
- Session 创建时间、路由、Frame 大小和频率。

## 4. 攻击者模型

- 被动或主动网络中间人。
- 能读取反向代理/CDN/日志的攻击者。
- 窃取 Token 但没有 Endpoint 私钥的攻击者。
- 持有合法但低权限 HC 的恶意用户。
- 被吊销或失陷的 Endpoint。
- 尝试跨用户/跨租户访问的合法账户。
- 读取 Platform 数据库、缓存或对象存储备份的攻击者。
- 入侵单个 Platform API/Gateway 实例的攻击者。
- 恶意或被替换的本地 ACP Agent。
- 供应链攻击者：依赖、构建脚本、发布制品或更新通道。
- 通过重复、乱序、回滚、延迟或磁盘耗尽制造状态错误的攻击者。

## 5. 信任层级

```text
Platform Root Signing Key (PRSK)
        │
        ├── Platform Online Issuer / Manifest Signer
        │
        └── User or Tenant Communication CA (UCA)
                 │
                 ├── ABA Endpoint Credential (AEC)
                 └── HC Endpoint Credential (HEC)
```

端点证书只证明身份和公钥绑定，不直接用于长期加密全部 ACP 内容。会话内容使用独立 SRK 和方向密钥。

## 6. 密钥分类

| 密钥 | 生成位置 | 保存位置 | 用途 | 是否可导出 |
| --- | --- | --- | --- | --- |
| PRSK | Platform 管理域 | HSM/KMS/Vault；开发环境密封存储 | 签发在线层、UCA、Trust Manifest | 生产禁止导出 |
| UCA Key | Platform KMS | KMS | 签发 AEC/HEC、状态清单 | 生产禁止导出 |
| Endpoint Signing Key | 每个 ABA/HC 本地 | OS Secure Store/硬件密钥 | DPoP、Challenge、Frame 签名、轮换证明 | 尽量不可导出 |
| Endpoint KEM Key | 每个 ABA/HC 本地 | OS Secure Store | HPKE Key Package 解封 | 尽量不可导出 |
| SRK | ABA 每个 Session/Generation | ABA Secure Store/受保护内存 | 派生双向 Session Key | 不上传明文 |
| Direction Key | ABA/HC 内存 | 受保护内存 | AEAD 加解密 | 不持久化或最小化 |
| Access Token | Platform 签发 | Endpoint 安全存储/内存 | 短期 API/WSS 授权 | 绑定 Signing Key |
| Refresh Token | Platform 签发 | Endpoint Secure Store | 获取新 Access Token | 绑定 Signing Key |
| WS Ticket | Platform 生成 | 短期内存/Redis | 单次 WebSocket 升级 | 不持久保存 |

禁止把 Endpoint Signing Key 与 KEM Key 合并成一套密钥，也禁止多个 Endpoint 共享私钥。

## 7. 首发密码学套件

### 7.1 Suite 标识

首发定义 `MSS-AWP-SUITE-0001`：

```text
Hash:              SHA-256
Endpoint signature: ECDSA P-256 / ES256
JWK thumbprint:     RFC 7638 SHA-256
Key package:        HPKE RFC 9180
KEM:                DHKEM(P-256, HKDF-SHA256)
KDF:                HKDF-SHA256
HPKE AEAD:          AES-256-GCM
Session payload:    AES-256-GCM
Random:             operating-system CSPRNG
```

选择 P-256/AES-GCM 是为了覆盖 Rust、浏览器 WebCrypto 和小程序可移植实现。HC 不能使用原生 WebCrypto 时，只能采用经过审查的 JS/WASM 密码学实现，禁止自写曲线或 AEAD。

Suite 必须显式出现在 Trust Manifest、Endpoint Credential、Key Package 和 Session 元数据中。未来增加 Suite 使用新标识和协商，不静默替换。

### 7.2 Key Derivation

ABA 为每个 Session Generation 生成 32 字节 SRK，派生：

```text
PRK = HKDF-Extract(
  salt = session_nonce,
  IKM  = SRK
)

K_hc_to_aba = HKDF-Expand(
  PRK,
  info = "mss-awp/v1/session/<session_id>/generation/<g>/hc-to-aba",
  L = 32
)

K_aba_to_hc = HKDF-Expand(
  PRK,
  info = "mss-awp/v1/session/<session_id>/generation/<g>/aba-to-hc",
  L = 32
)
```

Suite 0001 将 `<session_id>` 与 `<hc_endpoint_id>` 编码为 32 位小写 hex，将 `<g>` 编码为无前导零十进制 ASCII；即使 MVP 只有一个 HC，也始终使用包含 `/endpoint/<hc_endpoint_id>` 的多参与者形式，禁止两种 info 格式并存。

多 HC 模式下，默认每个参与者拥有独立方向密钥域：`.../endpoint/<hc_endpoint_id>/...`，避免一个 HC 获得另一个 HC 的发送能力。

### 7.3 Nonce

每个 Direction Key 使用：

```text
nonce = nonce_prefix_32 || sequence_u64_be
```

- `nonce_prefix` 在 Generation 激活时由 CSPRNG 生成并进入 Key Package/Session Key Metadata。
- `sequence` 在发送 Frame 前事务性分配并持久化。
- 同一 Key 下 Sequence 不得回退或复用。
- 检测到 Sequence 状态损坏时必须停止发送并轮换 Generation，不能猜测继续。

## 8. Endpoint Credential

首发 Endpoint Credential 使用 Platform 签名的 JWS Compact Token。签名输入由 JWS 标准确定，不对解析后的 JSON 重新序列化。Payload 至少包含：

```json
{
  "iss": "uca-id",
  "sub": "endpoint-id",
  "tenant_id": "tenant-id",
  "owner_user_id": "user-id",
  "endpoint_type": "aba",
  "sign_jkt": "RFC7638-thumbprint",
  "kem_jkt": "RFC7638-thumbprint",
  "crypto_suite": "MSS-AWP-SUITE-0001",
  "assurance_level": "software",
  "scopes": ["acp:connect", "acp:bridge"],
  "serial": "credential-serial",
  "policy_revision": 1,
  "iat": 0,
  "nbf": 0,
  "exp": 0
}
```

类型：

- `AEC`：ABA Endpoint Credential。
- `HEC`：HC Endpoint Credential。

Credential 验证顺序：

1. 严格解析 JWS Protected Header，拒绝未知关键参数和不允许算法。
2. 根据 Trust Manifest 找到有效 Issuer。
3. 验证签名和时间窗口。
4. 验证 Tenant/Owner/Endpoint Type。
5. 验证 Signing/KEM JWK Thumbprint 与注册记录一致。
6. 查询 Credential Serial 与 Endpoint 当前状态。
7. 验证 Scope、Policy Revision 和目标 Audience/用途。

ABA 可选增加短期 X.509 mTLS 证书作为部署增强，但 AEC + DPoP 是跨代理部署的必选应用层身份。mTLS 不能替代 Endpoint Credential 和 Frame 签名。

## 9. Platform Trust Manifest

Endpoint 首次连接后缓存 Platform 签名的 Trust Manifest：

```text
manifest_version
issued_at / expires_at
current_root
next_root (optional)
online_issuers
uca certificates
revocation_revision
minimum_client_version
allowed_crypto_suites
policy_revision
signature
```

安全要求：

- Manifest 自身由当前可信 Root/Manifest Signer 签名。
- Version 单调递增；Endpoint 拒绝回滚到更低版本。
- `next_root` 必须由当前可信根交叉签名或经过用户明确恢复确认。
- Endpoint 保存最近已接受的 Version 和 Root Fingerprint。
- Manifest 过期且无法刷新时，已有短期连接可以按有限 Grace 运行；新 Enrollment、证书签发和未知 Root 必须失败关闭。

## 10. ABA 首次 Enrollment

### 10.1 本地初始化

ABA 生成：

```text
installation_id = 128+ bit random identifier
signing_key      = P-256
kem_key          = P-256
request_nonce    = CSPRNG
```

私钥立即写入 OS Secure Store；无法安全保存时 Enrollment 失败，不降级到明文 TOML。

### 10.2 Start Request

ABA 通过 TLS 调用 Enrollment Start，发送：

- Installation ID、Endpoint Type/Name。
- Signing/KEM Public JWK。
- 平台、架构、ABA 版本。
- Request Nonce。
- 使用 Signing Key 对规范化 Enrollment Transcript 的签名。

Platform 验证公钥格式、证明持有、速率和重复注册后返回：

- Enrollment ID。
- 高熵 Device Code（只给 ABA，Platform 仅保存哈希）。
- 可人工输入的 User Code。
- Verification URI/二维码内容。
- 到期和轮询间隔。
- Platform Root Fingerprint 与 Bootstrap Manifest。

### 10.3 用户审批

已登录用户在 Platform/可信 HC 中看到：

- Endpoint 名称、类型、系统、IP、版本。
- Signing/KEM 指纹。
- 请求时间和到期。
- Platform Root Fingerprint。
- 初始 Runtime/Workspace 授权范围。

审批必须使用当前服务端 Session，并按策略要求密码重验证、Passkey 或已有可信 Endpoint 签名。

### 10.4 凭据领取

审批后 Platform 签发 AEC 和 DPoP 绑定 Token。领取响应包含：

- AEC。
- Trust Manifest。
- Access/Refresh Token 或一次性 Token 交换材料。
- Endpoint/Policy Revision。

整个领取包再使用 ABA KEM Public Key 加密，并绑定 Enrollment Transcript。ABA 必须同时证明 Device Code 和 Signing Key 持有，防止只窃取 Code 的攻击者领取。

## 11. HC 注册

### 11.1 Human Authentication

HC 先完成 Platform Human Session。Web 使用安全 Cookie/服务端 Session；小程序由 Platform 服务端用一次性 `wx.login` Code 换取微信主体并绑定 Platform User。

### 11.2 Endpoint Key

每个浏览器 Profile、小程序安装或 App 安装独立生成 Signing/KEM Key。不同 HC 不共享私钥。

### 11.3 Registration Challenge

Platform 返回一次性 Challenge：

```text
challenge_id
nonce
user_id
expected_origin/app_id
expires_at
```

HC 对以下 Transcript 签名：

```text
"mss-hc-enroll-v1" || challenge_id || nonce || user_id
|| sign_jkt || kem_jkt || endpoint_type || origin_or_app_id
```

Platform 验证当前 Human Session 与 Proof 后，根据策略签发 HEC 或进入 Pending Approval。

### 11.4 Assurance Level

- `web-software`：浏览器软件密钥。
- `miniapp-software`：小程序受平台沙箱保护的软件密钥。
- `native-secure-store`：原生系统安全存储。
- `hardware-backed`：硬件/Passkey 证明。

高风险动作的最低 Assurance Level 由策略控制。

## 12. Token 与 DPoP

### 12.1 Token 类型

```text
aba-access+jwt  aud=platform-acp-gateway
hc-access+jwt   aud=platform-acp-gateway
```

Token 至少包含 Tenant、Endpoint ID、Credential Serial、Scope、Session/Policy Revision 和：

```json
{"cnf":{"jkt":"endpoint-signing-jwk-thumbprint"}}
```

Access Token 短期有效；Refresh Token 是旋转型、单次使用族，服务端检测旧 Token 重用并撤销整个族。

### 12.2 DPoP Proof

每个受保护请求发送新 Proof，验证：

- `typ=dpop+jwt`。
- Header JWK Thumbprint 与 Access Token `cnf.jkt` 一致。
- `htm` 与实际方法一致。
- `htu` 与规范化目标 URI 一致。
- `iat` 在允许窗口内。
- `jti` 未使用。
- `nonce` 为 Platform 当前下发值。
- `ath` 匹配 Access Token 哈希。
- Endpoint/Credential/Session 仍有效。

JTI/Nonce 防重放状态必须存在共享缓存或数据库，不能仅在单个 API 实例内存中。

## 13. WebSocket Ticket 与连接挑战

### 13.1 Ticket Issue

HC/ABA 先通过带 DPoP 的 HTTPS 获取 Ticket。Ticket 是 256-bit 以上随机不透明值，服务端只保存哈希和绑定记录：

```text
tenant_id
user/session_id (HC)
endpoint_id
endpoint_type
credential_serial
sign_jkt
origin or client context
purpose
channel/connection nonce
issued_at / expires_at
single_use
```

响应必须设置 `Cache-Control: no-store`、`Pragma: no-cache` 和 `Referrer-Policy: no-referrer`。

### 13.2 Upgrade

浏览器通过 `Sec-WebSocket-Protocol` 发送应用子协议和 Ticket，不把长期 Token 放入 URL。Platform 原子消费 Ticket后再升级。

### 13.3 First-frame Challenge

升级后 Gateway 发送：

```text
connection_id
connection_generation
server_nonce
server_time
manifest_revision
credential_status_revision
```

Endpoint 签名：

```text
"mss-awp-ws-challenge-v1"
|| connection_id
|| connection_generation
|| server_nonce
|| endpoint_id
|| credential_serial
|| selected_subprotocol
```

未在短时限内通过则关闭连接。READY 连接绑定 `endpoint_id + connection_generation + fencing_token`，旧连接不能继续发送。

## 14. Key Package 与 Session 加密

### 14.1 ABA 生成 SRK

每个 Session Generation 由 ABA 生成新的 SRK 和方向 Nonce Prefix。ABA 不把 SRK 明文发给 Platform。

### 14.2 HPKE 封装

对每个授权 HC 独立生成 Key Package，HPKE `info` 至少绑定：

```text
mss-key-package-v1
session_id
generation
sender_aba_endpoint_id
recipient_hc_endpoint_id
crypto_suite
policy_revision
```

Suite 0001 固定使用 HPKE Base Mode：DHKEM(P-256, HKDF-SHA256) `0x0010`、HKDF-SHA256 `0x0001`、AES-256-GCM `0x0002`。`info` 不是 JSON 或 Protobuf，而是以下精确字节串：

```text
utf8("mss-key-package-v1")
|| session_id[16]
|| u64be(generation)
|| sender_aba_endpoint_id[16]
|| recipient_hc_endpoint_id[16]
|| u16be(crypto_suite = 1)
|| u64be(policy_revision)
```

HPKE `aad` 与上述 `info` 使用完全相同的 84 字节。P-256 公钥和 `enc` 使用 SEC1 未压缩 65 字节编码。

HPKE 明文包含：

```text
SRK
session_nonce
nonce prefixes
not_before / expires_at
generation
participant role
```

Suite 0001 的明文精确编码为 141 字节：

```text
utf8("mss-key-package-plaintext-v1")
|| session_id[16]
|| u64be(generation)
|| SRK[32]
|| session_nonce[32]
|| hc_to_aba_nonce_prefix[4]
|| aba_to_hc_nonce_prefix[4]
|| i64be(not_before_ms)
|| i64be(expires_at_ms)
|| u8(participant_role = 1 /* HC */)
```

解封后必须重新验证明文 Session、Generation、时间与 Participant Role；不匹配时销毁所得秘密并失败关闭。

ABA 再使用 Endpoint Signing Key 对 Key Package Envelope 签名。Envelope Transcript 为：

```text
utf8("mss-key-package-envelope-v1")
|| key_package_id[16]
|| session_id[16]
|| u64be(generation)
|| issuer_aba_endpoint_id[16]
|| recipient_hc_endpoint_id[16]
|| issuer_credential_id[16]
|| u16be(crypto_suite = 1)
|| u64be(policy_revision)
|| i64be(not_before_ms)
|| i64be(expires_at_ms)
|| u32be(hpke_enc_length) || hpke_enc
|| u32be(hpke_ciphertext_length) || hpke_ciphertext
```

Platform 保存 `SHA-256(info)` 作为 Context Hash，可验证来源但不能解封。

### 14.3 Participant 加入

新增 HC 不能从另一个普通 HC 直接获得 SRK。Platform 完成用户/Session 授权后，请求当前 ABA 为该 HC 单独封装；ABA 离线时加入保持 Pending。

## 15. Frame 保护

### 15.1 AAD

AWP 使用规范定义的 Canonical AAD Byte Encoding，不依赖语言默认 JSON/Protobuf 字段顺序。AAD 覆盖：

```text
wire_version
crypto_suite
frame_type
flags
message_id
channel_id
session_id
sender_endpoint_id
receiver_endpoint_id
direction
sequence
key_generation
key_id
created_at_ms
ciphertext_length
```

### 15.2 加密与签名

```text
ciphertext = AES-256-GCM(direction_key, nonce, plaintext, aad)
signature  = ECDSA-P256-SHA256(
  endpoint_signing_key,
  SHA256("mss-awp-frame-signature-v1" || aad || ciphertext)
)
```

接收顺序：

1. 验证 Envelope 限制和路由。
2. 验证 Credential/Endpoint 未吊销。
3. 验证签名。
4. 验证 Generation、Sequence 和 Replay Window。
5. AEAD 解密。
6. 把明文交给本地 ACP/HC。
7. 持久化 ACK。

Platform 可以选择验证签名后存储，但不应尝试解密。

## 16. 轮换体系

## 16.1 Root/UCA 轮换

状态：

```text
PREPARE -> PUBLISHED_NEXT -> ACKING -> ACTIVE_DUAL
-> ACTIVE_NEW -> RETIRING_OLD -> RETIRED
```

规则：

- 新 Root/UCA 先发布到 Trust Manifest。
- 当前可信根对新根做交叉签名。
- Endpoint 缓存并 ACK 新 Manifest。
- 达到策略阈值后新签发使用新 Issuer，旧 Issuer 仍验证。
- Grace 结束后旧 Issuer 退休，但历史验证元数据按保留策略存在。
- 旧根失陷时不能只依赖旧根交叉签名；需管理员恢复流程、已知可信 Endpoint 共识或人工核对新指纹。

## 16.2 Endpoint Key/Credential 轮换

Endpoint 本地生成全新 Signing/KEM Key，并同时提供：

```text
old_signature(new_sign_jwk || new_kem_jwk || nonce || endpoint_id)
new_signature(new_sign_jwk || new_kem_jwk || nonce || endpoint_id)
```

状态：

```text
REQUESTED -> PROOF_VERIFIED -> ISSUED -> OVERLAP
-> PRIMARY_NEW -> OLD_RENEWAL_ONLY -> OLD_REVOKED
```

Token 族迁移到新 `cnf.jkt`；旧 Credential 在 Overlap 后不能创建新连接。

## 16.3 Session Key 轮换

状态：

```text
PENDING -> DISTRIBUTING -> READY -> ACTIVE
-> GRACE -> RETIRED -> DESTROYED
```

- ABA 生成新 SRK。
- 为每个 Active Participant 创建新 Key Package。
- Platform 收集 ABA 和至少一个满足策略的 HC ACK。
- 激活后发送方只用新 Generation。
- 旧 Generation 在 Grace 内只允许解密和完成在途消息。
- 长期离线 HC 不阻塞激活；其返回时重新授权并领取当前 Generation。

## 16.4 吊销触发

吊销 Endpoint 后原子执行或编排：

1. Endpoint 状态变为 Revoked/Compromised。
2. Credential Serial 写入吊销状态并提升 Revision。
3. 撤销 Access/Refresh Token 族。
4. 删除/失效未消费 Ticket 和 DPoP Nonce。
5. Gateway 关闭所有该 Endpoint 连接。
6. 拒绝新的 Key Package。
7. 通知相关 ABA 轮换仍活跃 Session 的 SRK。
8. 产生不可包含秘密的高风险审计与用户通知。

## 17. 恢复机制

### 17.1 丢失 HC

通过其他可信 HC、Passkey 或 Platform 账户二次认证吊销。新 HC 重新 Enrollment；当前 ABA 可以为新 HC 封装当前 Session Key。没有任何可信 Endpoint 且账户恢复后，历史 Opaque 数据是否可恢复取决于是否配置用户持有的 Recovery Endpoint/Recovery Public Key。

### 17.2 丢失 ABA

吊销 AEC 和 Token，关闭 Session。新机器以新 Endpoint 注册；不复制旧 ABA 私钥。Workspace/Agent 状态由用户自己的存储和 ACP Agent 恢复能力决定。

### 17.3 用户可选恢复密钥（后续阶段）

可提供用户本地生成的 Recovery KEM Public Key，让 ABA 额外封装历史 SRK。Platform 只保存加密 Recovery Package。恢复私钥由用户离线保存，Platform 不托管。该能力默认关闭，并清楚提示恢复私钥丢失无法恢复。

### 17.4 Platform Root 灾难恢复

自托管管理员必须在 Platform 外备份 KMS/HSM 恢复材料和 Root Fingerprint。恢复事件要求显式安全模式、双人审批（团队部署）、新 Manifest 和强制 Endpoint 确认，不能静默替换 Pin。

## 18. 权限与本地执行安全

Platform 发送给 ABA 的执行相关控制消息只允许：

```text
runtime_profile_id
workspace_id
session_id
requested ACP capabilities
policy revision
```

明确禁止：

```text
command
args
cwd
env
shell script
binary URL
arbitrary path
```

ABA 本地配置决定真实值，并验证 Workspace 与 Runtime 的允许关系。未知 ID、被禁用 Profile、路径逃逸、符号链接越界或策略 Revision 回滚全部拒绝。

## 19. 日志、审计与遥测

日志中禁止：

- Token、Ticket、Device Code/User Code 原值。
- 私钥、SRK、方向密钥、HPKE 明文。
- ACP JSON-RPC 明文和解密错误中的 Payload。
- Runtime 环境变量值和 Workspace 文件内容。

允许记录：

- 哈希化或截断关联 ID。
- Endpoint/Session/Connection ID。
- Credential Serial。
- 状态转换、错误分类、Frame 大小和 Sequence 范围。
- 管理主体和结果。

错误响应对客户端使用稳定错误码，对日志保留内部关联 ID；不向未授权调用者暴露证书或用户是否存在。

## 20. 防回滚与时间

- Trust Manifest、Policy、Credential Status、Session Generation 使用单调 Revision。
- Endpoint 持久化最近接受值并拒绝降低。
- 时间只用于有效期和审计；Replay 主要依靠 JTI、Nonce、Sequence 和 Revision。
- 允许有限时钟偏差，但严重偏差时提示用户校时并限制新凭据操作。
- ABA Journal/HC 状态损坏时，不重置 Sequence 继续使用旧 Key；强制新 Generation。

## 21. DoS 与资源限制

Platform：

- Enrollment/IP/User/Endpoint 速率限制。
- Ticket、DPoP 和 Challenge 配额。
- Frame 大小、窗口、积压、Session 和存储配额。
- 慢消费者 Backpressure、连接空闲超时和最大并发。

ABA：

- 最大 Session/子进程数。
- 每 Runtime CPU/内存/时间限制扩展点。
- Journal 磁盘上限和磁盘满故障关闭。
- Frame 解码前长度限制。

HC：

- 流式缓冲上限。
- 大输出虚拟化和本地缓存配额。

## 22. 供应链安全

- Platform 基线锁定 mss-boot-admin v1.3.7 精确提交。
- Rust/Go/JS 依赖锁定并执行漏洞和许可证检查。
- Protocol 生成器版本锁定；生成代码可重现。
- 发布制品生成 SBOM、哈希和签名。
- ABA 自动更新必须验证签名、版本单调和目标平台；首发可先手动升级，不允许不安全静默下载执行。
- CI 和开发日志不得输出秘密。

## 23. 安全验收最小集合

- Token 泄露但无私钥无法调用 API/WSS。
- Ticket 只能消费一次，绑定错误 Origin/Endpoint/Session 时失败。
- 旧 Manifest、Credential Revision、Generation 和 Sequence 回滚失败。
- 跨用户/跨租户/跨 Endpoint 访问失败。
- 数据库备份不能恢复 ACP 明文或 Endpoint 私钥。
- Platform Gateway 能中继但不能解析 ACP Method。
- 吊销后连接、Token、Ticket 和新 Key Package 均失效。
- Endpoint/Session 轮换中断可幂等恢复，不产生双 Active Generation。
- Nonce/Sequence 故障不会继续复用旧 Key。
- ABA 拒绝远端任意 command/args/cwd/env。
- 秘密扫描、依赖扫描和日志检查通过。

## 24. 安全设计变更规则

以下变更必须先通过 ADR 和安全评审：

- 密钥层级或生成位置。
- Endpoint Credential 格式。
- Crypto Suite、AAD、Nonce 或签名输入。
- DPoP/Ticket/Challenge 绑定字段。
- Platform 是否可以解密。
- Runtime/Workspace 远端控制能力。
- 轮换激活条件或 Grace 规则。
- 恢复密钥或密钥托管。

不得通过实现便利性削弱这些边界。

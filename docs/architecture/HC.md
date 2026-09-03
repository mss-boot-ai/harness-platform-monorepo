# HC 设计：Web、微信小程序与原生客户端

- **状态**：Accepted
- **HC 含义**：H 端客户端统称
- **首发形态**：Web HC 与微信小程序 HC
- **后续形态**：原生 App、桌面端

## 1. 定位

HC 是用户与远端 ACP Session 交互的客户端，同时也是端到端加密的一端。HC 不只是 Platform 页面：每个 HC 安装实例都有独立 Endpoint Identity、Signing Key、KEM Key、HEC、DPoP 绑定凭据和 Session Key。

HC 的 Human Identity 与 Endpoint Identity 必须同时成立：

```text
Platform User Session
      ∩
HC Endpoint Credential
      ∩
Current RBAC / Session ACL / Resource Grant
```

## 2. 目标

- 用户可从手机或浏览器查看在线 ABA，创建和继续 ACP Session。
- 支持 ACP 流式输出、工具调用、权限确认、取消和状态展示。
- HC 本地生成私钥，Platform 不接收私钥。
- 获取、验证并缓存 Platform Trust Manifest、HEC 和 Key Package。
- 通过一次性 Ticket 建立 WSS，并完成 Endpoint Challenge。
- 默认本地加解密 ACP JSON-RPC，Platform 只中继密文。
- 不同客户端形态共享协议、状态机和业务模型，同时保留平台安全能力差异。

## 3. 非目标

- HC 直接连接 ABA 本地端口。
- HC 获得 ABA 主机上的任意 Shell。
- 在 Platform 端保存 HC 私钥或解密历史。
- 假设 Web、小程序和原生 App 拥有相同安全等级。
- 用 Local Storage 明文保存 Token、私钥或 Session Key。
- 把网络断开时的未发送 Prompt 当作已经执行。

## 4. 代码布局

建议：

```text
hc/
├── packages/
│   ├── protocol/            # generated AWP types + canonical encoders
│   ├── crypto/              # suite abstraction, vectors, no UI dependency
│   ├── identity/            # endpoint enrollment, HEC, DPoP
│   ├── session-core/        # state machine, frame/ack/reconnect
│   └── ui-model/            # shared view model and safe error mapping
├── web/
│   ├── responsive PWA
│   ├── browser secure-store adapter
│   └── WebSocket/Origin adapter
├── miniapp/
│   ├── WeChat lifecycle and login adapter
│   ├── mini-program secure-store adapter
│   └── socket/storage constraints adapter
└── native/                  # later
```

共享包使用 TypeScript 并从 `protocol/` 生成类型。UI 框架和小程序框架可在实现 Spike 后锁定，但密码学、AWP 和 Session 状态机不能依赖特定 UI 框架。

## 5. HC Endpoint Identity

每个安装实例生成：

```text
endpoint_id       Platform 分配或注册后确认
signing_key       P-256, used for DPoP/challenge/frame signature
kem_key           P-256, used for HPKE key package
endpoint_type     hc-web | hc-miniapp | hc-native | hc-desktop
assurance_level   determined by storage and platform proof
```

浏览器不同 Profile、隐私窗口、小程序不同安装和原生设备都视为不同 Endpoint，不共享私钥。

## 6. Human Login

### 6.1 Web HC

- 使用 Platform 现有安全 Browser Session Cookie。
- 不把登录 Access Token放在 Local Storage。
- 所有改变状态的 API 要求可信 Origin、CSRF/当前 Browser Session 策略和 HC DPoP。
- 登录后仍需注册或解锁 HC Endpoint，不能把浏览器 Session 当作 Endpoint Identity。

### 6.2 微信小程序 HC

```text
wx temporary login code
  -> Platform server exchanges code with WeChat
  -> verified app identity/open identity
  -> bind/load Platform user
  -> create Platform server-side session/token context
```

客户端直接提交的 openid/unionid 不是凭据。Platform 必须验证 App ID、Code 一次性和绑定关系。

### 6.3 原生客户端

后续支持 OIDC/Platform Login/Passkey，并把 Endpoint Key 放入 OS Key Store 或硬件保护区。

## 7. Endpoint 注册流程

```text
HC                         Platform
│ human login ready            │
│ request endpoint challenge   │
├─────────────────────────────►│
│ challenge + manifest         │
│◄─────────────────────────────┤
│ generate signing/KEM keys    │
│ sign enrollment transcript   │
│ submit public keys + proof   │
├─────────────────────────────►│
│ policy auto/pending approval │
│ HEC + DPoP-bound credentials │
│◄─────────────────────────────┤
│ verify and cache             │
```

若本地安全存储不可用，注册失败并提供说明，不降级明文保存。

## 8. 本地安全存储

统一接口：

```ts
interface HcSecureStore {
  createSigningKey(): Promise<SigningKeyHandle>;
  createKemKey(): Promise<KemKeyHandle>;
  sign(handle: SigningKeyHandle, data: Uint8Array): Promise<Uint8Array>;
  hpkeOpen(handle: KemKeyHandle, input: HpkeInput): Promise<Uint8Array>;
  putSecret(name: string, secret: Uint8Array): Promise<void>;
  getSecret(name: string): Promise<Uint8Array | null>;
  deleteSecret(name: string): Promise<void>;
  assurance(): Promise<AssuranceLevel>;
}
```

### 8.1 Web

优先：

- WebCrypto 生成不可导出 `CryptoKey`。
- 通过 IndexedDB 结构化克隆保存 Key Handle（能力验证通过时）。
- Session Key 默认只驻留内存；需要恢复时使用本地不可导出 Wrapping Key 加密后存 IndexedDB。
- Refresh Token 优先由安全 Cookie 或本地 Endpoint-bound 交换机制持有，不放 Local Storage。

若浏览器不支持安全持久化，不自动导出私钥；可以将该 HC 作为临时 Endpoint，关闭页面即失效，或提示使用受支持客户端。

### 8.2 微信小程序

必须先完成真实运行环境 Spike，验证随机数、椭圆曲线、AES-GCM/HPKE、密钥不可导出性和安全存储能力。若平台只提供普通键值存储，则使用经审查的本地 Wrapping 方案并明确 Assurance Level；无法达到最低标准时，小程序只作为低权限审批/观察端或改由原生容器承载。

严禁因为 API 受限把私钥、SRK 或 Refresh Token 明文写入普通 Storage。

### 8.3 原生

使用 Keychain/Keystore/DPAPI/CNG 等，并优先硬件保护、用户认证门禁和不可导出 Key。

## 9. Assurance Level

```text
web-ephemeral          临时非持久端点
web-software           浏览器不可导出软件密钥
miniapp-wrapped        小程序本地加密包装
native-secure-store    OS 安全存储
hardware-backed        硬件密钥/Passkey 证明
```

Platform 策略可以要求：

- 普通 Prompt：`web-software` 以上。
- 新增/吊销 Endpoint：二次 Human Auth。
- Root Recovery/Managed Mode：`hardware-backed` 或多个可信 Endpoint。

Web 页面不得把软件密钥宣传成硬件级安全。

## 10. Trust Manifest 与 HEC 缓存

HC 缓存：

- Current/Next Platform Root Public Key/Fingerprint。
- UCA/Online Signer。
- Trust Manifest Revision/Expiration。
- HEC 原始 JWS。
- Endpoint/Credential Status Revision。
- Allowed Crypto Suites 和最小客户端版本。

规则：

- 首次建立信任依赖 TLS、Human Session 和指纹/可信页面。
- Manifest Version 只能单调增加。
- 新 Root 未由当前 Root 交叉签名或用户恢复确认时拒绝。
- HEC 过期或吊销时停止创建新连接。
- 缓存只保存公有证书材料；私钥由 Secure Store Handle 引用。

## 11. API 与 DPoP

每次 Endpoint 受保护请求：

1. 取得短期 Access Token。
2. 构造唯一 JTI 和当前 Server Nonce。
3. 对 `htu`、`htm`、`iat`、`jti`、`nonce`、`ath` 生成 DPoP Proof。
4. Platform 返回 Nonce Challenge 时按标准重试一次，不无限循环。
5. Token/HEC/Endpoint 被吊销时清除本地 Session 并提示重新注册。

请求队列不能复用同一个 DPoP Proof。

## 12. WebSocket 连接

状态：

```text
SIGNED_OUT
 -> HUMAN_AUTHENTICATED
 -> ENDPOINT_READY
 -> TICKET_ISSUING
 -> CONNECTING
 -> CHALLENGED
 -> READY
 -> RECONNECTING
 -> REVOKED/CLOSED
```

流程：

1. 带 Human Session、HEC/Access Token 和 DPoP 调用 Ticket API。
2. 使用 `mss.awp.v1` 和 Ticket Subprotocol 建 WSS。
3. 验证 Platform ServerChallenge 签名和 Manifest Revision。
4. 用 HC Signing Key签 ChallengeResponse。
5. 收到 ConnectionReady 后建立有界发送/接收队列。
6. 发送 ResumeState。
7. 重连时使用新 Ticket，但保留 Session/Channel ACK Cursor。

Ticket 不写日志、不放 URL、不缓存。

## 13. Session 创建 UI

用户必须明确选择：

- ABA Endpoint。
- Runtime Profile。
- Workspace。
- 会话模式/ACP 可见能力（如有）。

页面展示：

- ABA 在线/最后在线。
- 资源由该 ABA 本地配置提供的事实。
- 当前 HC Assurance Level。
- Opaque/Managed Mode。
- 安全警告和预计等待状态。

HC 发送的请求只有 ID，不包含本地 command/args/cwd/env。

## 14. Key Package

收到 `SESSION_KEY_PACKAGE`：

1. 验证 Session、Generation、Issuer ABA、Recipient HC 和有效期。
2. 验证 ABA AEC 与 Key Package 签名。
3. 使用本 HC KEM Key HPKE Open。
4. 验证明文中的 Session/Generation/Endpoint/Nonce Prefix 与 Envelope 一致。
5. 派生本 HC 独立方向密钥。
6. 安全保存或只驻留内存。
7. 发送签名 ACK。
8. 只有 Platform/ABA 激活该 Generation 后才用其发送。

同一 Generation 的不同 Key Package 若解出不一致 SRK，立即停止 Session 并产生安全错误。

## 15. ACP UI 模型

HC 解密后解析 ACP JSON-RPC，只在本地形成 UI 状态：

- Session Initialize/Capabilities。
- 用户 Prompt。
- Agent 文本/思考状态（按协议支持）。
- Tool Call/Permission Request。
- Diff、文件变更或终端输出组件。
- Cancel/Stop 状态。
- ACP Error。

Platform 不接收解析后的 UI 模型。需要跨 HC 历史同步时，发送端到端加密的 ACP/应用扩展 Frame，或由用户显式上传可见元数据；不得默认把明文状态写回 Platform。

## 16. 权限确认 UX

权限卡必须展示：

- 请求来源 ABA/ACP Agent。
- Session 和 Workspace 展示名。
- 权限/工具类型。
- 目标文件或命令摘要（来自加密 ACP 内容，只在 HC 显示）。
- 风险等级。
- 允许范围和有效期。

按钮：

```text
拒绝
仅本次允许
本 Session 允许（若本地策略允许）
```

首发不提供“永久允许所有命令”。ABA 本地硬策略始终优先于 HC 允许。

## 17. 流式与背压

- 使用增量 View Model，避免把无限输出保存在内存。
- 超大终端输出虚拟化和截断展示，但协议 ACK 只在数据安全进入本地可恢复队列后发送。
- UI 渲染慢不能阻塞控制通道；网络接收与渲染队列分离且有界。
- 达到本地存储上限时暂停接收/发送 ACK 并显示 Backpressure，不静默丢弃。

## 18. ACK、Inbox 与重放

HC 维护：

```text
channel_id + direction
highest_contiguous_sequence
received gap ranges
message_id dedupe
session key generation
```

ACK 时机：验签、解密、解析为完整 ACP Transport Frame，并持久化到本地 Inbox/状态机后。

重复 Frame：

- 不重复显示、不重复触发权限动作。
- 可以再次发送当前 ACK。
- 若相同 Sequence 出现不同 Ciphertext/Signature，视为安全事件并关闭 Session。

## 19. 离线与页面生命周期

- 未连接时用户可以编辑 Prompt 草稿，但明确标记“尚未发送”。
- 不在后台无确认地排队执行高风险 Prompt。
- Web 页面刷新后先恢复 Endpoint、Manifest、Session Metadata 和 Key，再发送 ResumeState。
- 小程序进入后台可能被暂停或断 Socket；恢复时总是重新 Ticket/Challenge，不假设旧连接存活。
- `UNCERTAIN` 状态必须置顶显示，并要求用户选择关闭 Session、检查实际 Workspace 或按安全恢复流程继续。

## 20. 主要页面

```text
登录/绑定
Endpoint 注册/解锁
ABA 列表
ABA 详情（Runtime/Workspace 摘要）
Session 列表
新建 Session
ACP 会话
权限请求中心
连接与重连状态
当前 HC 安全设置
所有 Endpoint 管理
证书/轮换状态
安全通知与审计摘要
```

Web HC 为响应式 PWA；Platform 管理后台和 HC 用户界面可以共享登录体系，但路由、Bundle 和权限边界应清晰。

## 21. Opaque Mode UI

明确显示：

- “会话内容在此 HC 与 ABA 之间加密”。
- “Platform 可见连接、时间、大小和路由元数据”。
- “丢失全部有权 Endpoint/恢复密钥可能无法恢复历史”。

Platform Session 列表若需要标题：

- 默认使用用户手动输入的可见名称；或
- HC 本地显示私有标题，不上传；或
- 用户显式选择上传非敏感标题元数据。

不得偷偷解密内容生成服务端标题。

## 22. Managed Mode UI

未来启用时必须：

- 单独页面明确“Platform 可以读取内容”。
- 显示用途、保留、解密 Worker、审计和关闭影响。
- 二次认证。
- Session 级或租户策略级显式开关。
- 不能用绿色 E2EE 标识。

## 23. 错误展示

UI 使用稳定错误码映射安全文案：

- Credential Revoked：本 HC 已被吊销，停止重试。
- Key Package Required：等待 ABA 在线分发。
- Sequence Replay：发生安全异常，关闭会话并提供审计 ID。
- Backpressure：接收端/Platform 积压，暂缓发送。
- Local Dispatch Uncertain：请求可能已执行，不自动重试。

不展示内部堆栈、SQL、真实本地路径或密钥材料。

## 24. 遥测与崩溃上报

允许：

- 页面/功能成功率。
- 稳定错误码。
- 连接阶段和延迟。
- Frame 数量/大小分桶。
- 客户端版本与 Assurance Level。

禁止：

- ACP 明文。
- Prompt/文件名/命令/终端输出。
- Token、Ticket、证书完整值、Key Package、密钥。
- 用户粘贴内容和 Workspace 路径。

## 25. 前端供应链

- 依赖锁文件必须提交。
- 构建产物生成哈希/SBOM。
- 禁止运行时从未知 CDN 加载可执行脚本。
- CSP、Trusted Types（Web 可用时）和依赖漏洞检查进入门禁。
- Crypto/Wire 核心尽量小且独立审查。
- 测试环境使用固定测试密钥，生产构建禁止包含测试 Secret。

## 26. 测试

共享核心：

- AWP Golden Vector。
- DPoP Proof。
- Trust Manifest/HEC 验证。
- HPKE Key Package。
- Sequence/ACK/Replay。
- Session/Rotation 状态机。

Web：

- 多浏览器 Secure Store 能力。
- Origin/Ticket/刷新/多标签页单写者。
- IndexedDB 损坏和配额。
- XSS/CSP/日志秘密。

小程序：

- 真机随机数、Crypto 性能和存储行为。
- 登录 Code 重放。
- 前后台 Socket 生命周期。
- 网络切换和系统清理 Storage。

端到端：

- Platform + HC + ABA + 测试 ACP Agent。
- 断线、重放、吊销和轮换。

## 27. 多标签页/多实例

Web 同一 Endpoint Key 不允许多个标签页同时作为 Sequence Writer。使用浏览器锁/Leader Election：

- 一个 Leader 持有 WSS 和 Sequence Allocator。
- 其他标签页通过本地受控通道与 Leader 通信，或只读。
- Leader 丢失后新 Leader 先恢复持久 Sequence，再连接。
- 无法保证单写者时创建新的临时 Endpoint，不能并行复用 Nonce 空间。

## 28. HC 最小验收

- 私钥本地生成，未上传 Platform。
- Human Session 与 Endpoint Identity 同时验证。
- DPoP 请求、单次 Ticket 和 Challenge 完成。
- Trust Manifest 回滚和未知 Root 被拒绝。
- Key Package 只能由目标 HC 解封。
- AWP v1 加解密和 Golden Vector 通过。
- ACP 单消息与 Batch 正确显示。
- 重复 Frame 不重复 UI/权限动作。
- 页面刷新/小程序恢复后 Resume 正确。
- 吊销后停止连接并清除本地可撤销凭据。
- UNCERTAIN 清晰展示且不自动重试。
- 日志、遥测和崩溃报告不包含 ACP 明文或秘密。
- 实际浏览器和小程序真机验证结果分别记录，不能用模拟器结果代替真机。
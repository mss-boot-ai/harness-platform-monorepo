# Session Control 与 ABA 本地策略验证记录

- **状态**：Verified for this slice
- **日期**：2026-09-05
- **分支**：`codex/bootstrap-harness-platform-foundation`
- **验证 SHA**：`3561f04024743c9c76fe48160c8dceb98f814b9a`
- **范围**：活动连接目录、Generation Fencing、Session 原子创建、签名 OpenTunnel、ABA 本地策略、HC H5 Session UI

## 交付

- READY 后的单写者活动连接目录，128 Packet/8 MiB 有界发送队列和 30 秒 WebSocket Ping；
- 数据库 Connection Generation 作为权威顺序，更高 generation 替换并关闭旧连接，迟到的低 generation 不能夺回活动槽位；
- READY 时只更新 ACTIVE Endpoint 的 `LastSeenAt`；
- HC 使用 Endpoint Access + `session:manage` Scope + 精确浏览器 Origin + DPoP 调用 Session/ABA Discovery API；
- 在线 ABA Discovery 只返回同 owner/tenant、ACTIVE 且当前连接目录在线的 ABA 安全投影；
- Session Create 对 ABA/HC 角色、owner/tenant、ACTIVE/Revoked 状态、Runtime/Workspace/Capability ID 和每 ABA 活跃 Session 上限做服务端校验；
- Session、Audit、Idempotency 在同一数据库事务创建；相同请求稳定回放，不重复发送 OpenTunnel；
- Platform 使用 Online Signer 对 `OpenTunnelRequest` Control Transcript 签名，远端消息只含稳定 ID、Revision、Capability 和到期时间，不含 command/args/env/cwd/path；
- ABA 常驻 `run` 控制循环验证 Platform Online Signature、receiver、时间、连续 control sequence 和 payload；
- ABA 本地策略再次检查 Runtime/Workspace ID、允许关系、真实文件/目录、非 symlink 与 Unix 可执行位；未知 ID 返回稳定拒绝码；
- ABA 用 Endpoint E-SIG 签名 `OpenTunnelResult`；Gateway 验证 ABA、Session、HC、sequence、time、signature、status、revision 和 capability 子集后推动 `CREATING → WAITING_KEY/FAILED`；
- HC H5 每次重连先轮换 Refresh，再申请 Ticket；页面级 DPoP nonce 操作串行化，避免合法并发覆盖 Endpoint Nonce CAS；
- H5 发现在线 ABA、提交 Idempotency-Key Session 请求并轮询安全管理投影。

## 提交与 CI

| SHA | 内容 |
| --- | --- |
| `f2a8cb8580831f3caefb48879ee6d5dbf32ab40e` | 活动连接目录、Heartbeat Ping、Generation Fencing、LastSeen |
| `42111351a3b4b7f7871688ddaf613bf9ebf40cfb` | Endpoint Session、Audit、Idempotency 原子事务 |
| `5e5cd959c593836cc4ac04a6ee6c1469ee336fe1` | HC DPoP Session API 与签名 OpenTunnelRequest |
| `1521040f1b5394bc8a51c368bdefd10d0ebbfa80` | 验证 ABA OpenTunnelResult 并更新 Session |
| `9c101f8d9f0ce5a954e2a141aa93a6a5e016dd0f` | ABA 常驻控制循环与本地策略 |
| `e1d962b003db82a7eb5f3ecd4f094e1905b2a197` | HC H5 Session 创建体验 |
| `9ef184e183cacc2cedab078cebedecd0a7c53fd2` | 使用 HC DPoP 发现 ABA，移除 Admin Cookie 依赖 |
| `faddd3247a50b70c1e8e542db6b4c5db3f99b3f1` | 重连前先轮换过期 Access |
| `018359d78edd70dc11afea65cea209a4cd743a6b` | ABA Discovery 改为保留 Origin 的 POST |
| `3561f04024743c9c76fe48160c8dceb98f814b9a` | 串行化 H5 Endpoint Nonce 操作 |

最终验证 SHA：

- push Foundation CI `33910804383`：`success`；
- PR Foundation CI `33910809172`：`success`；
- documentation、platform-import、protocol、aba-rust、hc-typescript 全部成功。

关键前序检查点的 push/PR CI 同样完整成功：`f2a8cb8...` 为 `33907044854`/`33907052433`，`4211135...` 为 `33907411729`/`33907415647`，`5e5cd95...` 为 `33907830303`/`33907838508`，`1521040...` 为 `33908465372`/`33908470307`，`9c101f8...` 为 `33908881937`/`33908886068`。

## 本地静态与测试证据

```text
platform: go test -count=1 ./...
platform: go vet ./...
platform: go test -race -count=1 ./internal/harness/gateway ./internal/harness/store
aba:      cargo fmt --check
aba:      cargo clippy --all-targets --all-features -- -D warnings
aba:      cargo test --all-targets                              # 25 passed
hc:       pnpm lint
hc:       pnpm typecheck
hc:       pnpm test                                             # 7 files / 18 passed
hc:       pnpm build
```

Go 集成测试使用两个真实 WebSocket client 完成 signed Challenge，验证新 READY generation 关闭旧连接；Session 测试验证在线 ABA Discovery、HC DPoP、幂等创建、Platform-signed OpenTunnel、ABA-signed ACCEPTED、状态进入 WAITING_KEY 和重复请求不重复投递。Rust 测试验证有效本地配置 ACCEPTED、未知配置稳定 REJECTED、篡改 Platform 签名失败关闭。

## 内置浏览器与真实 ABA 证据

本地进程为 Backend `:8080`、HC H5 `localhost:8001`、Gateway `127.0.0.1:8082` 和常驻 Rust ABA。ABA 使用忽略的 loopback 开发配置，Runtime ID `test-agent` 只映射 `/usr/bin/true` 用于本地策略存在/权限检查，本切片没有启动该进程；Workspace ID `harness-platform` 映射当前 Monorepo 绝对路径。配置、KeyStore、Token 和本地数据库均未提交。

实际完成：

1. ABA 常驻连接显示 Endpoint `3f32d562...`、Generation 5；
2. H5 在已过期旧 Access 的条件下通过 HttpOnly Refresh 恢复并重新 WSS READY，Generation 10；
3. H5 通过 HC DPoP 查询到唯一在线 ABA `Local ABA H5 Debug 2`；
4. H5 以 Runtime `test-agent`、Workspace `harness-platform` 创建 Session `6618463385...`；
5. Platform 原子写 Session/Audit/Idempotency 并向 ABA 发送签名 OpenTunnelRequest；
6. ABA 验证 Platform 与本地策略，返回 Endpoint 签名 ACCEPTED；
7. Gateway 验签并更新 Session，H5 页面显示 `WAITING_KEY`。

## 失败—修复

- H5 首版用 Admin Management Cookie 列 ABA，长开页面因 Human Session 过期无法发现；`9ef184e...` 改为 HC Endpoint DPoP 数据面接口。
- H5 重连直接复用内存中已过期 Access，Ticket 返回 `ENDPOINT_CREDENTIAL_EXPIRED`；`faddd32...` 改为重连前 Refresh rotation。
- 同源浏览器 GET 不发送 Origin，而 Gateway 正确要求精确 Origin；`018359d...` 将 ABA Discovery 改为 DPoP POST，没有放宽服务端 Origin 门槛。
- Refresh/Ticket 与 ABA Discovery 并发覆盖同一 Endpoint Nonce CAS，返回 `DPOP_NONCE_CHANGED`；`3561f04...` 延后发布新 Registration 并用页面级队列串行化完整 nonce challenge/proof 操作。
- 环境没有 `sqlite3` CLI，未用临时脚本绕过；状态旁证来自 H5 管理投影、Go Store/HTTP/WSS 集成测试和 Gateway/ABA 实际协议结果。

## 边界与下一阶段

- Session 当前到 `WAITING_KEY`，不等于可用 ACP Session；
- `/usr/bin/true` 只证明 Runtime 文件与本地策略校验，没有启动 ACP Test Agent；
- ABA 尚未生成 SRK/HPKE Key Package，HC 尚未解封方向密钥；
- EncryptedFrame、ACP Prompt/Response、ACK/Replay、Journal/UNCERTAIN 和 Session Close 尚未进入真实 E2E；
- 活动连接目录为单实例内存实现，数据库 generation 仅提供持久 fencing 顺序，不宣称跨 Gateway 节点在线路由。

下一检查点按 RFC 9180 实现 Suite 0001 HPKE、HKDF 方向密钥、AES-256-GCM、固定 Context/AAD 和三语言共同向量，再把 ACCEPTED Session 推到 Key Package/ACTIVE。

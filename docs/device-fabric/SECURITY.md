# 身份、授权、审批与内容安全

- 状态：Draft，2026-09-13。
- 继承：ADR-0003 与 `AGENT.md` 的所有现有 AWP 安全不变量。

## 1. 明确威胁边界

保护目标：网络/代理不能改动作；Platform 的普通业务进程和数据库不能默认读会话、设备参数与采集内容；设备被盗可撤销；旧授权、重放、并发审批和进程重启不能自动产生新副作用。

不承诺：端点主机完全失陷后仍保密，恶意固件仍可靠确认真实人，Platform 失陷时仍可用，第三方语音服务不处理提交给它的内容，或低价开发板天然具有硬件级不可导出私钥。

Web 执行环境受其脚本供应链影响。用户自托管 Bridge 是显式可信端点；把 Bridge 放到运营方托管环境会改变内容信任边界，必须让用户看到并认可，不能叫相同的 Opaque 模式而隐瞒。

## 2. 配对与凭据

每个 Provider/Bridge 在本地独立生成 signing/KEM keys。Platform 只存公钥、证书/credential serial、状态和 token hash，不接收私钥。

新机器 Enrollment：Start（公钥+持有证明）→短期 user code/device code→已登录 HC 核对指纹和申请范围→批准→设备再次证明持有→领取绑定凭据。复用既有原语与 token family，不复制另一个 Endpoint 的 token，也不把 MCU 当浏览器注册。

设备凭据 scope 初始限于 `mdp:connect`、`device:self:publish`、`device:self:result`；Bridge 按授权获得 `device:invoke`、`device:subscribe`。用户会话操作必须有额外 ActorLease。`human_interface` 角色不自动授予会话写权限。

MDP token 和 ticket 绑定用途与 audience。DPoP 验证 method、准确外部 URL、iat、jti、nonce、ath 和公钥 thumbprint；复用现有 nonce/replay 验证，不能为了并发而允许 proof 重用。

## 3. Roles、Capabilities、Grants 与本地策略

有效授权是交集：

```text
当前用户/租户权限
∩ Endpoint credential scopes 与状态
∩ Device owner 签署/确认的资源授权
∩ Run/Workspace/Toolset 许可
∩ Descriptor 已许可版本和参数上限
∩ Provider 本地用户许可与实时可用性
∩ 对应动作所需的审批证据
```

Roles 由平台授权，Manifest 由设备声明。设备 self-report 的 assurance、readOnly、idempotent、risk 等不是可信授权证据；使用本地已审查的合约模板与管理员确认记录。

首次配对时，Device 和 Bridge 缓存经用户核对的对端密钥指纹与 Device owner 授权根。平台重新签出另一张证书不能自动替换已配对的所有者/Bridge 信任。正常轮换需旧新双签与单调版本；丢失旧 key 时走明确恢复流程，撤销旧授权并提示重新配对。

Grant 包含授权主体/设备/binding_epoch、允许合约、参数限制、数据用途、读写模式、最大持续时间、scope与期限。平台保存路由层 grant id/主体/资源/版本；私密参数限制可作为 owner 签署的加密 policy bundle 分发，Bridge 和 Provider 再校验，避免平台只有路由许可就能造任意设备命令。

## 4. 为什么审批不是普通 Tool

必须区分三件事：

- 问卷或普通 `interaction.choice`：用户选项，不提升权限。
- `approval.present`：显示来自可信执行区的真实待授权动作。
- `approval.decision`：有身份和动作绑定的授权证据，只接受独立人类客户端/设备签名。

Agent 可以提出需要审批的动作，但不能调用“批准”Tool，也不能提供 `approved=true` 来跳过流程。通用 display card 的区域不得覆盖系统保留的审批信任标识。

## 5. Approval 数据与状态

ApprovalRequest 必须绑定：

```text
approval_id, authority_endpoint_id, task_id, run_id
source_protocol, source_request_id, source_process_epoch
invocation_id?、target_device_id?、binding_epoch?
canonical_action_digest, schema/contract_digest
resource_scope, risk_class, policy_revision
allowed_decisions, eligible_approvers, expires_at
```

动作 digest 覆盖准确参数/目标/版本和原始请求身份，不能只哈希一段“允许部署吗”的自然语言。显示内容和 digest 必须来自同一可信原始动作；不得由模型提供一个安全摘要，却执行另外的参数。

Decision 必须包含 approval_id、action digest、decision、approver user/Endpoint、ActorLease、随机 nonce、时间、授权版本和 Endpoint signature。Authority 验证并原子消费一次，重新检查当前权限，再映射到原 ACP request ID。请求所属进程已经变化、权限请求已关闭或用户取消时，迟到决策不能批准新的动作。

状态：PENDING→APPROVED/DENIED/EXPIRED/CANCELLED；决策通过不等于动作执行成功。审批消费记录与后续执行 Journal 关联；掉电不能再次消费同一批准产生第二次动作。

多终端收到同一请求时，Authority 进行 CAS。第一个满足策略的有效终态决策生效，其余收到 ALREADY_DECIDED。需要多签的未来策略另建规则，不能把冲突的 approve/deny 简化成最后写入者获胜。Authority 不可达时绝不本地猜测批准。

## 6. 人类身份、控制权与设备保证级别

控制权、内容阅读权、设备操作权、审批权是四个不同授权。观察一个 Run 不意味着能发 Prompt；审批一个动作不意味着接管整个 Run。

ActorLease 由已认证 HC/可信身份流程建立，绑定具体 Device、Run/模板范围、操作集合、有效期与身份保证级别，建议最长5分钟，离开/锁屏/撤销即失效。它是有限委托，不是复制 HC 凭据。

Mosaico 首版默认 `embedded-development`；不因 ESP 芯片支持某安全功能就宣称已启用 Secure Boot/Flash Encryption 或硬件不可导出密钥。设备首次到货必须确认板版本、固件与密钥存储实际能力。

默认策略：

| 动作 | 未激活 Mosaico | 有限 ActorLease 的 Mosaico | 满足策略的 HC |
| --- | --- | --- | --- |
| 收公开状态/低敏卡片 | 允许按授权 | 允许 | 允许 |
| 发起许可模板/输入 | 拒绝 | 限指定范围 | 按用户权限 |
| 低风险一次性确认 | 拒绝 | 经明确策略允许且完整显示 | 允许 |
| 生产部署、数据删除、付款、长期授权 | 拒绝 | 拒绝并转交HC | 二次认证/策略审批 |
| 固件签名/授予新设备权限 | 拒绝 | 拒绝 | 管理流程，不经Agent Tool |

按键长按是防误触措施，不是用户身份认证。无法呈现完整动作关键信息的设备必须拒绝批准，不能把小屏幕当降低确认标准的理由。

## 7. 本地 Runtime 和 MCP 隔离

Bridge 仅把已授权能力模板暴露给 Agent。工具 `readOnlyHint` 等注解仅作 UI 提示，不当安全决定。MCP 输入中的 userId、tenantId、runId、risk 或 URL 都不能覆盖进程启动时的可信上下文。

每个 Run 建立独立 MCP 逻辑会话和最小 scopes。stdio/Unix Socket 默认本地使用；生产按 OS 权限隔离 Bridge 与 Agent sandbox，避免 Agent 通过 shell 读取 Bridge 长期凭据。相同 UID 的开发模式不能声称具备这种隔离，限制为测试设备和低风险操作。

禁止远端自由注入 `mcpServers` 的 command/args/env。适配器只接受本地配置映射到的 toolset_id；真实 Agent 的外部 MCP 支持情况必须逐个验证。

后续公开 HTTP MCP 时采用规范授权流程、准确 resource audience、token 验证与会话隔离；不把 Platform access token 直接透传给其他 MCP Server。首版不用公开 HTTP MCP 来绕过设备出站网络模型。

## 8. 撤销、租约和恢复

Platform 撤销 Endpoint/Grant 时撤销 token/ticket、fence 连接、停止路由并发布单调 revision。Bridge 与 Provider 在每次 dispatch 前复核授权；默认执行授权租约≤60秒，过期且无法取得可信新状态时拒绝新动作。

远端系统无法保证对已经离线且正在执行的物理动作“瞬时撤销”。本地 timeout、资源限额和安全停止才是兜底；后续高风险执行器必须有独立硬件安全措施。首版不接此类负载。

密钥轮换：新请求使用新 credential；旧消息只在有限 grace 内验证/解密并检查原 deadline。不能因为轮换后收到重复业务请求而创建第二次副作用。失去 KEM key 的队列密文可能不可恢复，应报告 RESOURCE/KEY_UNAVAILABLE 并走人工确认，不默默降密或换参数重试。

设备 factory reset 清理私钥/授权、本地敏感缓存和绑定。重新认领生成新 Endpoint/epoch；旧固件镜像或恢复备份不得恢复已吊销身份。首次 MCU 实现没有合格持久防回滚时，只承诺开发级安全，禁用高风险授权。

## 9. 音频、图片与 Artifact

首版语音为按键录音、最长30秒、明确录音灯/界面与取消；不默认持续上传环境声音。设备加密后上传资源，可信执行区解密并按明确用户配置发送给 STT/模型；不能把“公网网关仅转发”与“公网网关调用 STT 并读取录音”混为一谈。

资源使用随机 resource_id、独立随机256-bit内容密钥、AES-GCM 分块；nonce 为32-bit全零前缀加64-bit chunk index，同一内容密钥只用于一个不可变资源，修改资源必须新 key。AAD 绑定 resource_id、producer、resource_generation、chunk index 和长度。完整加密 manifest 绑定总长度、分块数、格式与内容摘要，防止截断/拼接。内容密钥通过独立 HPKE message 仅分发给获授权接收方。

平台对象存储只保存密文与密文 digest；不把 plaintext digest 暴露为可枚举内容标识。短期下载/上传凭据限定对象、字节上限和操作；S3 URL 本身不是 Agent 的授权证明。Bridge 获取资源仍验证 resource ACL、sender 和 digest，防任意 URL/SSRF。

实时音频的会话密钥、丢包、打断、编解码和协商是后续独立媒体 profile，不拿控制 envelope 每20ms做一次 HPKE，也不把原始媒体塞进 MCP 参数。

## 10. 审计与安全验收

平台日志只含稳定错误、ID、元数据状态、长度、密文 hash；禁记 raw params、Prompt、URL token、音频转写、私钥。详细审批/执行审计由可信端签署并加密给审计授权人；Platform 只保存摘要引用和加密事件。

必测：跨租户/Owner访问、伪造角色/能力、密钥替换、AAD篡改、过期/旧boot命令、重放与重复参数冲突、模型伪造批准、两端审批竞争、审批后参数变化、取消竞争、掉电UNKNOWN、吊销与离线租约、平台明文canary、恶意描述和资源URL。

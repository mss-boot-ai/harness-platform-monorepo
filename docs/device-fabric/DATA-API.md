# 数据模型、事务、HTTP 和 MCP 契约

- 状态：Draft，2026-09-13；以下均为拟议契约，不是已存在的 API。
- Go Thin Host 与当前 SQLite/PostgreSQL 验证方式保留；正式 Migration ID 在实施时核对已有注册表后分配。

## 1. 领域与存储原则

Platform 保存权限和路由层权威状态；业务调用明文和精细执行日志位于可信 Bridge/Provider。不要在 Platform 建 `arguments JSONB`、`transcript TEXT` 或无加密 `sensor_value` 便利字段。

ID 沿现有项目32位小写hex编码（wire为16 bytes），revision/epoch 单调 uint64，HTTP 中超过安全整数范围的量以十进制字符串表示。Timestamp 使用UTC毫秒，持续时间使用单调时钟，不混用客户端本地时区。

所有对象有 tenant_id、owner或明确授权关系。不能认为随机ID不可猜就省去授权。个人空间沿现有身份语义处理，即使 tenant_id 为空，也要按 owner_user_id 或显式 grant 隔离。跨表关联使用同租户条件；Shared Device 必须有明确资源授权。

## 2. 新增持久化对象

| 对象/拟议表 | 关键字段 | 约束/索引 |
| --- | --- | --- |
| endpoint_runtime_profiles | endpoint_id、runtime_kind、client_family、assurance_evidence_ref、version | endpoint_id唯一；证据自报与已核验分开 |
| endpoint_role_grants | endpoint_id、role、issuer、scope、status、revision、expires_at | 同主体/role有效范围不能冲突；撤销可追溯 |
| fabric_devices | id、tenant_id、owner_user_id、provider_endpoint_id、binding_epoch、status、public_summary、row_version | (tenant,id)唯一；provider查询索引；绑定CAS |
| fabric_device_bindings | device_id、endpoint_id、epoch、approved_by、proof_ref、active_from/to | (tenant,device,epoch)唯一；至多一个活动绑定 |
| fabric_descriptors | device_id、revision、contract_catalog_version、ciphertext_ref、ciphertext_hash、signer_id | (tenant,device,revision)唯一；不存私密明文schema |
| fabric_grants | id、subject_endpoint_id、device_id、binding_epoch、scope_class、revision、status、expires_at、sealed_policy_ref | 资源/主体/版本索引；状态与过期索引 |
| fabric_connections | endpoint_id、connection_generation、gateway_id、lease_until、fencing_token_hash | endpoint_id唯一活动连接；generation CAS |
| fabric_messages | message_id、sender/recipient、device_id、binding_epoch、grant_id、class、envelope_bytes、ciphertext_hash、expires_at | (tenant,message_id,recipient)唯一；重复不同hash拒绝 |
| fabric_outbox | message_id、recipient、delivery_status、attempts、next_attempt、lease_owner/until | message+recipient唯一；(status,next_attempt)索引 |
| fabric_receipts | message_id、recipient、signed_receipt、receipt_hash、received_at | message+recipient唯一；引用原envelope |
| fabric_invocations | id、requester、provider、device、grant、run_ref、signed_status、status_revision、result_ref、deadline | 稳定id唯一；(tenant,device,status)与run索引 |
| fabric_subscriptions | id、subscriber、device、event_scope、filter_digest、lease_until、quota | 受grant约束；到期清理；过滤内容可加密 |
| fabric_resource_refs | id、producer、ciphertext_object_key、ciphertext_hash、size、expires_at | 随机对象ID；无公开桶；读写授权分离 |
| harness_tasks | id、project_ref、owner、sealed_content_ref、created_at | Task业务内容加密；租户/owner索引 |
| harness_runs | id、task_id、executor、runtime_profile_id、workspace_id、signed_state、state_revision | 一个Run对应一次执行尝试 |
| harness_run_sessions | run_id、awp_session_id、binding_revision | 保留原session表，不重写双方ID |
| harness_control_leases | run_id、controller_endpoint_id、lease_generation、expires_at | 每Run单writer，CAS接管；不包含内容密钥 |
| harness_approvals | id、run_id、authority_endpoint_id、status、decision_revision、sealed_request_ref、sealed_decision_ref、expires_at | 状态只接受可信authority签名声明 |

这不是要求一次建完所有表。DF1 只建角色/设备/绑定/授权/消息/outbox/receipt；DF2 增加调用与最小Task/Run关联；审批、资源和订阅按后续切片添加。可共享现有审计与幂等组件，不再创建第二套用户、租户或全局PKI。

## 3. Provider/Bridge 本地对象

Bridge 保存加密磁盘上的 Run/Invocation Journal、已授权设备完整Descriptor、MCP请求关联、待定Approval及结果。Provider 保存自己的绑定、key handles、本地许可和执行Journal。两者均有字节上限和清理规则。

Platform 的 invocation 状态是可信端签名报告的投影，不能根据消息已入库自己写 SUCCEEDED。Provider 是实际动作执行状态来源，Bridge 协调结果/UNKNOWN，Approval Authority 决定审批消费状态。

## 4. 关键事务

### T1：认领设备

验证当前Human身份/授权、Endpoint公钥proof、Enrollment尚未消费→锁定Enrollment→分配Device绑定/epoch、角色最小scope与凭据→记录审计→标为consumed→提交。重试返回同一结果或一致冲突，不能第二次生成独立身份。

### T2：更换绑定

显式用户批准→CAS旧row_version/binding_epoch→撤销旧grant、使旧路由不可执行→新epoch/new binding→审计与通知outbox同事务。没有新批准不能按序列号自动恢复信任。

### T3：接收可靠消息

验证连接与fencing、发送方签名、租户/owner/grant/绑定/大小/期限→事务插入不可变envelope和delivery outbox。唯一键重复且hash相同返回同一投递；hash不同为CONFLICT。

Gateway 不持数据库锁跨网络发送。worker领取有截止的outbox lease→发原bytes→收到收件端有效receipt→事务更新投递状态。崩溃可再发相同bytes。

### T4：设备执行

验证解密内容和本地策略→以Invocation ID/digest查询ledger→锁互斥组→持久DISPATCH_STARTED→调用驱动→持久结果→可靠回报。写Journal失败不得执行。crash恢复不能以outbox重投当作重新执行授权。

### T5：审批消费

Authority验证决策签名/用户/lease/原动作digest/当前request进程epoch/权限revision→CAS PENDING→终态→持久消费记录→传递到原运行时请求。原动作尚未被接受且Authority崩溃，恢复后先查询本地请求映射；无法证明结果则UNKNOWN，不重建新权限请求来套用旧approve。

Platform只记录已签名的决策投影；Console管理员不能直接UPDATE一行把一个动作批准。

## 5. HTTP API 分层

### Human 管理接口

前缀 `/admin/api/device-fabric/v1`，继续使用既有Browser Session、CSRF/可信Origin、RBAC、资源scope和必要的HC proof。

| 方法与路径 | 语义 |
| --- | --- |
| GET /devices | 列表仅含获授权公开摘要，cursor分页 |
| GET /devices/{id} | 可见metadata和能力摘要，不返回默认私密state |
| POST /enrollments/{id}/approve | 用户批准配对及最小权限 |
| POST /enrollments/{id}/deny | 拒绝配对 |
| POST /devices/{id}/grants | 创建资源授权，绑定版本/期限 |
| POST /grants/{id}/revoke | 撤销授权及单调revision |
| POST /devices/{id}/rebind | 明确认领/重绑定，If-Match必需 |
| POST /devices/{id}/revoke | 撤销设备身份与路由 |
| GET /invocations | 可见元数据与签名状态投影 |
| GET /audit | 复用现有审计授权，不解密内容 |

这里没有通用 `/devices/{id}/execute` 明文管理员后门，也没有 Console `/approvals/{id}/approve` 替用户越权批准业务动作。

### 机器 Endpoint 接口

前缀 `/gateway/mdp/v1`，与旧 `/gateway/v1` AWP入口隔离。

| 方法与路径 | 身份与职责 |
| --- | --- |
| POST /enrollments/start | challenge+proof、严格速率限制，无长期权限 |
| POST /enrollments/poll | device code与key proof；码按哈希存储 |
| POST /enrollments/consume | 一次消费，凭据绑定本地key |
| POST /credentials/refresh | 当前token family+DPoP，不跨协议扩scope |
| POST /tickets | 短时单次WSS ticket，purpose=mdp |
| GET /ws | subprotocol ticket + signed challenge，非query token |
| POST /device-summaries | 签名的最小目录摘要，已许可角色 |
| GET /routes/{deviceId} | 已授权绑定、凭据公钥与租约，不发私钥 |
| POST /resources/uploads | 限对象/大小/期限的上传授权，内容必须已加密 |
| POST /resources/{id}/downloads | 检查资源scope后短期读取密文 |

`devices` API名不是授权边界；服务层须验证归属、角色、scope和活跃revision。Browser Provider调用同样要校验Origin，不能绕过其运行形态限制。

### 并发、幂等与错误

改变管理状态的请求要求 Idempotency-Key；scope包含actor+operation+resource，保留24h（设计默认），相同key不同body摘要为409。revision更新要求If-Match，过期为412。401缺失/失效身份，403权限不足，409冲突/忙，410失效绑定或资源，413超限，429背压，503服务不可用。

响应统一 `code/message/correlationId/retryable`；retryable只表传输/API重试安全，不暗示可以重做一个已经开始的设备动作。敏感对象对无权用户不泄露存在性。

## 6. MCP 最小工具集

MCP Server 在用户可信 Bridge，不在默认不可读明文的 Platform。首版本地stdio，HTTP远程模式后续单独验收授权。使用官方维护SDK并锁版本，协议协商与真实Agent兼容矩阵一起提交。

| Tool | 输入 | 结果/边界 |
| --- | --- | --- |
| devices_list | capability_filter?、cursor?、limit≤50 | 仅本Run获授权设备 |
| device_describe | device_id | 经过验证/净化的Descriptor |
| device_status_read | device_id、允许字段集合 | observed_at、revision、freshness、值 |
| device_display_card | device_id、title、body、ttl_ms | invocation_id、状态/结果 |
| device_haptic_pulse | device_id、duration_ms | 有上限的动作与调用ID |
| device_invocation_get | invocation_id | 权限内的最新状态与结果 |
| device_invocation_cancel | invocation_id | cancellation_requested，不宣称已停止 |
| device_resource_get | resource_id | 获授权内容引用/限量预览，不接受任意URL |

没有 `approve`、`execute_shell`、`write_gpio` 或接受任意 procedure 字符串的无边界 Tool。后续扩展新合约须注册受控模板，不能把设备描述直接转换成任意函数。

`device_display_card`示例 input schema（概念示例，生成时必须保持限制）：

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["device_id", "title", "body", "ttl_ms"],
  "properties": {
    "device_id": {"type": "string", "pattern": "^[0-9a-f]{32}$"},
    "title": {"type": "string", "maxLength": 96},
    "body": {"type": "string", "maxLength": 2048},
    "ttl_ms": {"type": "integer", "minimum": 1000, "maximum": 300000}
  }
}
```

JSON Schema maxLength计字符，Bridge/Provider仍须额外检查PROTOCOL规定的UTF-8字节上限，不能误以为它限制了bytes。

MCP Tool结果采用结构化 `invocation_id/status/effect_certainty/result_ref/error_code`。业务失败按SDK支持返回isError及结构化状态；UNKNOWN不是可自动重做的临时错误。长动作尽快返回ID，不依赖尚未验证的MCP task扩展。

MCP请求ID只在当前MCP会话内有意义。Bridge按 `(run,mcp_session,request_id)`保存一次受理映射；客户端需保留返回的Invocation ID查询。连接丢失/新请求ID不自动表示重复意图可安全再次执行；副作用由稳定Invocation/审批证据保护。

## 7. 资源与媒体

资源元数据上限与WSS队列分离。首次资源切片支持HTTP加密分块对象，不强依赖现有S3部署地址。控制消息只带随机resource_ref、加密manifest和权限引用；内容key不进入Platform日志或数据库明文字段。

平台只按密文字节计费/限流；BYOK模型花费或用户设备真实电量等没有可信数据时显示未知，不编造计量。

## 8. 清理与扩容

持久删除按依赖顺序清理已过期 outbox/receipts/messages/resource refs；活动Invocation/Approval及不可判定状态不得因普通TTL任务误删。保留敏感数据的密钥清理由可信端执行，平台删除密文不能替代销毁端点已缓存明文。

初版使用已有关系数据库和固定数量后台扫描任务；不每Device创建一个无界goroutine或定时任务。连接、消息队列、订阅和事件速率都有硬上限。扩大Gateway副本前完成数据库/目录fencing与跨实例路由测试。

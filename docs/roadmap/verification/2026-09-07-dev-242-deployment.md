# 242 开发环境部署验证记录

- **状态**：Deployed；真实 H5 / DeepSeek 主流程验证通过；非生产验收
- **日期**：2026-09-07 14:43 +08:00
- **主线基线**：`64f69044491b0f817e634d98ed4c5e413037e44f`
- **分支**：`codex/deploy-harness-dev-242`
- **PR**：https://github.com/mss-boot-ai/harness-platform-monorepo/pull/2

## 实际部署

主机 `167.17.68.242`，context `kubernetes-admin@cluster.local`，namespace `harness-dev`。
IngressClass nginx，StorageClass OpenEBS local（WaitForFirstConsumer）。
TimescaleDB 2.29.2 / PostgreSQL 16 使用 10 GiB PVC；Gateway 单副本 StatefulSet 使用独立
1 GiB trust PVC。两个 PVC Bound，全部服务 Pod Ready。

| 组件 | 已部署代码检查点 |
| --- | --- |
| Platform API、Admin Web、HC Web | `8932c16911a7befd3cd02af136a25b00c1102452` |
| Gateway | `71be3c901bd1f2003ad93cd264029598beb2fcb5` |
| ABA 宿主二进制 | `3d9cef351af6827c6c3472f42dc2d607eb5befb5` |

镜像使用完整 SHA 标签，导入 containerd k8s.io namespace。Gateway 热修复单独更新，
其余已验证镜像保留原检查点。ABA 为宿主 systemd 服务，用户 harness-aba；
独立 harness-port-forward 用户运行 loopback 桥接，仅监听 127.0.0.1:18082。
ServiceAccount RBAC 只允许读取与转发 harness-gateway-0，kubeconfig 为 root:指定组 0640。

独立 DeepSeek venv 中 SDK/runtime-bin 均固定 0.1.1rc1，通过 ACP stable-v1 stdio adapter
接入。Runtime ID 为 deepseek-harness，Workspace ID 为 harness-platform，
工作区为 /srv/harness-workspaces/harness-platform。既有 DeepSeek WebUI 服务不变。

## 访问与凭据

- Platform：https://platform-dev.mss-boot-io.top/
- HC H5：https://hc-dev.mss-boot-io.top/
- 既有 DeepSeek WebUI：https://harness.mss-boot-io.top/
- Platform 和 HC 使用同一 admin 用户；WebUI 使用既有 Basic Auth admin。
- 密码不入库：Platform 恢复源为主机 /var/lib/harness-deploy/dev-242/admin-bootstrap.env。
  运行期 Pod 不含初始密码。
- H5 先创建安全端点，再登录注册、获取 Ticket、选择 ABA，使用默认 Runtime/Workspace。
- 页面刷新后选择“恢复已注册端点”；旧 Session 内存密钥丢失时关闭遗留 Session 后重建。

双域名 DNS 已解析至 242，Let's Encrypt Certificate Ready。
公开 HTTPS、HTTP→HTTPS、HSTS/CSP、Gateway readiness 和 TLS 1.3 正向握手均验证成功。

## 真实验证

- 迁移 Job 成功；CI 在真实 TimescaleDB 上重复迁移两次，并验证复合唯一索引、
  partial/deferrable 拒绝、1 MiB 密文、并发写入和 refresh/revoke 锁序。
- 目标扩展查询返回 harness|2.29.2。
- DeepSeek ACP initialize/session-new 连续三次约 5.93、5.92、6.00 秒通过；
  另已通过真实在线模型独立 adapter Prompt 探针。
- 内置浏览器完成 Admin 登录、ABA 用户码审批和凭据领取；HC 完成持久 CryptoKey 身份、
  同源登录注册、DPoP/Ticket/WSS 首帧挑战。
- 第一条 Prompt 收到 HARNESS_242_E2E_OK；新会话收到 SECOND_SESSION_OK。
- H5 手动断线后代次从 4 增至 5，同一 Session 收到 RECONNECT_OK。
- 关闭 Session 后 DeepSeek 子进程数量为 0，ABA 保持 active。
- HC 重连后在同一 ABA 连接上再建下一 Session 成功，验证 KeyPackage ACK 修复。
- 定向检查当时 6 个加密 Frame，ciphertext 中 canary 明文命中为 0。
  这不等同于完整日志/秘密审计。

## 失败与修复

| 真实失败 | 修复检查点 |
| --- | --- |
| PostgreSQL DROP INDEX 生成非法 CURRENT_SCHEMA().index | be98a7f |
| GORM 返回表字段顺序而非真实索引顺序 | c114617 |
| partial/deferrable 索引误判及非原子修复 | c338269、5833601 |
| 节点 CPU requests 99%，DB Pending | dad8aeb；重建未启动的旧 Pending Pod |
| PostgreSQL CHAR 空可选 ID 解析失败 | 2025849 |
| 纳秒时间入库后精度变化使 enrollment polling 拒绝 | 6821077 |
| Web 子路由跳转到内部 http:8080 | 8932c16；新标签确认旧 301 缓存问题 |
| ON CONFLICT 计数列名歧义阻断 Nonce/重连 | 71be3c9 |
| HC 重连后新 Session ACK 序号重置导致 ABA 退出 | 3d9cef3 |

本地跨网络完整 PostgreSQL 测试曾在 5 分钟截止时超时，遗留的独立测试 schema 已清理。
独立 enrollment round-trip 真库测试随后通过；完整矩阵由 CI 真 TimescaleDB 环境通过。

## CI 证据

- 8932c16：https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34089902785
- 71be3c9：https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34090848331
- 3d9cef3：https://github.com/mss-boot-ai/harness-platform-monorepo/actions/runs/34091522630

均为成功，覆盖 7 个 jobs：文档、Platform import/backend/frontend、协议、
Rust ABA/adapter、HC、真实 TimescaleDB、四个容器构建与运行。
本地 Rust 32 个 lib 测试（含新 Session ACK/重复 ACK 回归）、Clippy 和静态 release 构建通过。

## 备份与开发限制

root-only 备份已生成于 /var/backups/harness-dev/harness-20260907.dump 和
/var/backups/harness-dev/gateway-trust-20260907.tar。尚未执行独立恢复演练；
pg_dump 提示 Timescale 内部 continuous_agg 循环外键。备份仍在同机，需另行配置异机备份。

- 单节点、单副本；滚动发布有短暂停机。CPU requests 余量很少，尚无负载性能承诺。
- ABA 使用显式 loopback 开发文件 KeyStore，未做生产 KMS/OS-Keyring 验收。
- ACP runtime 与 ABA 共享服务 UID，尚无独立 runtime 文件系统沙箱；此受控开发验证
  不能代替不可信本地 Agent 的生产隔离验收。
- Python 直接版本固定，尚无完整 wheel hash lock。
- 目标 Kubernetes 1.32.8、containerd 1.7.13、ingress-nginx 1.14.0、cert-manager 1.19.1；
  升级与生产加固不在本次部署验收内。
- Platform /readyz 仍为上游实现，Gateway 已检查 DB/Trust。
- 本次未重做完整吊销、密钥轮换、故障/容量/灾备矩阵；既有本地证据不能替代目标环境验收。

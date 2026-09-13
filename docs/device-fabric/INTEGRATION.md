# 实体终端、Headless HC 与既有 ACP 的具体接入

- 状态：Draft，2026-09-13。
- 本文细化ARCHITECTURE中Interaction Adapter/Run Coordinator的实现接缝，避免默认存在尚未实现的本地控制接口。

## 1. 两种模式并存

### 旧模式：Direct HC Session

```text
Web HC -- AWP E2EE -- Platform -- AWP E2EE -- ABA -- ACP -- Agent
```

完全保留。该Web HC持有自己的会话密钥、处理原ACP权限请求。Provider能力可以旁路加入，但不因此让另一个设备读整场对话。现有DeepSeek Session若不支持loadSession，不宣称可跨进程恢复。

### 新模式：Coordinator-owned Run

```text
Web/App/Mosaico -- MDP加密交互/投影 -- Platform -- MDP密文 -- 用户可信Companion
                                                               |
                                                     RunCoordinator / HeadlessHC
                                                               |
                            Platform <-- AWP密文 ---------------+
                                |
                              AWP密文
                                |
                               ABA -- ACP --> Agent -- 本地MCP --> DeviceBridge
```

Headless HC实现现有ACP客户端和AWP协议，替被明确授权的用户运行会话。Mosaico只持自己的MDP凭据和有限ActorLease，不持ACP SRK。Platform仍无法读取两条通道的业务内容。

Companion可在同一可信进程内放置Headless HC、Approval Authority和Device Bridge，但它们整体属于同一个明文信任域；不同模块/不同凭据不构成进程内内存隔离。生产按风险可拆进程。Headless HC与Bridge使用不同用途凭据，不复用ABA或用户Web HC私钥。

## 2. Headless HC不是伪装浏览器

现有wire枚举已有HC_NATIVE定义，但Platform Domain/认证路径未实现通用native HC。本方案新增Domain的HC_NATIVE及相应严格认证支持，不把机器声明成HC_WEB或HC_REFERENCE。

具体增量：

- 一个由owner配对、独立key的native HC安装身份；初始无任意用户会话权限。
- Owner通过已认证HC授予短期RunStartGrant：actor、目标ABA、Runtime Profile、Workspace、模板/任务范围、预算/超时、允许的终端、有效期和授权revision。
- 新native会话API使用DPoP、token scope、当前Grant和owner/tenant校验，不要求伪造浏览器Origin。浏览器旧API的Origin检查保留。
- shared session service按明确的HC endpoint policy处理类型，不把所有DEVICE_PROVIDER都视为HC。
- AWP Frame方向、AAD、OpenTunnel、key package与sequence不变；只是经过授权的native HC成为原先hc_endpoint_id的位置。

RunStartGrant是允许启动某个受限Run，不是模拟一次用户登录，也不是把设备凭据提升到管理员权限。用户撤回委托后禁止新Run/新用户意图；正在执行的动作按取消/不确定规则处理。

## 3. 从Mosaico发起任务的逐步过程

1. Owner在HC配对Mosaico及Companion，选择其可调用的模板/Workspace/Runtime和内容范围。
2. HC激活有限ActorLease；Mosaico提交的意图端到端加密给指定Companion，带稳定intent_id。
3. Coordinator校验ActorLease、设备绑定、模板版本、幂等和RunStartGrant；创建Task或引用已有Task并建立新的Run。
4. Headless HC通过新增native身份入口创建AWP Session，Platform事务绑定RunSession，向ABA发送既有OpenTunnel。
5. ABA再次执行本地Runtime/Workspace白名单，启动Agent；Headless HC获取只发给自己的KeyPackage并初始化ACP。
6. Coordinator把许可的意图转成ACP Prompt，经AWP发送；结果在Headless HC解密。
7. 根据获准内容范围生成Projection，单独加密给Mosaico和获授权的Web/App。原始源码/工具参数默认不发小屏设备。
8. 后续输入必须绑定同一个Run及当前控制租约；改变Runtime/执行环境形成新Run，不当作透明迁移。

intent重复仅返回原Task/Run，不再次创建运行；已经开始而响应丢失先查询。

## 4. 取消与控制租约

每Run只有一个有效人类控制writer，Headless HC是执行该控制权的协议客户端，不是另一个人类writer。ControlLease绑定控制Endpoint、actor、Run、generation和过期时间；写操作进入可信Coordinator后也复核，不能只依赖平台UI禁用按钮。

设备“取消”生成带Lease的请求。Headless HC映射到Runtime支持的ACP取消语义；不支持时显示不支持并交由当前本地策略处理。不能在所有Agent上假设存在相同的取消API，也不能默认杀进程当作已撤销物理效果。

现有Direct HC Session不能无缝切成Coordinator Run，因为当前AWP/实现是单HC且没有交接所有上下文与密钥的协议。本轮不偷拿原Web HC key。用户可继续在原HC处理，或明确用已有Task的授权上下文创建新的Coordinator Run。未来完整接管另做兼容设计。

## 5. 真正审批的来源

Coordinator-owned Run的Headless HC能接收并解密原ACP permission request；Approval Authority在这里绑定准确request ID、进程epoch、参数digest和状态。审批决定验证通过后，Headless HC把结果回到同一个仍待定的ACP请求。

Direct HC Session暂沿用自己的HC权限UI。跨端审批不能只由Platform复制一个卡片实现，也不能对当前尚未产生完整权限事件的DeepSeek适配器宣称支持。每个Runtime必须具备相应权限请求/响应桥接的证据；不支持的Runtime只提供通知与普通受限交互。

## 6. MCP本地配置与ABA最小改动

MCP Toolset是本地Runtime Profile的一部分；配置固定Bridge/helper程序、连接方式和允许合约。远端继续只选择RuntimeProfile/Workspace IDs，不在AWP控制帧传自由command/args/env。

若需要让一个新Agent进程绑定Run：ABA可新增可选LocalLaunchContext（仅本地、无入站端口），通过匿名pipe/继承FD交给本地已许可的Runtime wrapper，内容限定AWP session_id、HC endpoint_id、runtime_profile_id、workspace_id和一次性本地attach nonce。它不包含Prompt、用户提供的命令或长期密钥。

Wrapper通过受OS权限保护的本地接口向Companion注册。Companion验证session_id与Headless HC的RunSession映射、owner授权和一次性nonce后发放该Run最小MCP上下文。为防竞态，映射未就绪时有界等待，不接收未绑定Run的工具调用。实现时须为nonce签发/消费、peer身份和FD清理提供测试。

旧Profile不启用该hook时行为不变；该hook是明确的新增实现项，不是假定仓库已有能力。其职责限于安全身份绑定，不把Device Fabric逻辑搬入ABA。

当前DeepSeek adapter后续须用实际SDK支持的本地工具注册接口实现Toolset；若SDK不支持，返回UNSUPPORTED_RUNTIME_CAPABILITY或使用已验证的其他Runtime，不能直接接受远端注入来凑演示。

## 7. 对切片的补充

DF2完成真实Agent→设备Tool链路；DF4引入Headless HC/native身份入口、RunStartGrant和Coordinator-owned Run。DF5仅对已验证ACP权限交互的Runtime启用审批。

首个硬件演示不必等DF4：DF2/DF3已经能把真实任务结果显示到Mosaico；但“从Mosaico主动发起和继续任务”必须明确依赖DF4，而不能当作Device Registry自带功能。

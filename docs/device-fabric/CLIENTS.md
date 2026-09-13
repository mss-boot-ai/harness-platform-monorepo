# HC 与 ESP-Mosaico 实现方案

- 状态：Draft，2026-09-13。

## 1. 代码组织

```text
platform/internal/devicefabric/       # 新领域、service、store、mdp gateway
platform/internal/modules/devicefabric/ # Thin Host business.Module注册
platform/web/src/business/            # Console业务接缝
hc/packages/device-runtime/           # TS Provider/交互投影，不重写原core
hc/web/                              # 保留原AWP会话，按需启用MDP
protocol/proto/mss/device/v1/         # 新合约
protocol/testdata/device/            # 正负向互操作向量
 device/bridge/                      # Rust可信companion，本地MCP/MDP
 device/simulator/                   # 首版Go或Rust CLI，按复用成本选一项
 device/esp/                         # ESP-IDF C/C++工程
   components/mss_mdp/
   components/mss_identity/
   components/mss_device_runtime/
   components/mss_projection_ui/
   boards/esp_mosaico/
   main/
```

上面 `device/` 行的缩进只是目录展示，不属于路径。首版模拟器优先复用Bridge的Rust合约/crypto库，避免多一套密码实现；平台Go、HC TS、设备C互操作由固定向量验证。不同时创建Go/Rust/TS三套通用SDK。只有两个真实消费者需要时再抽公共crate/package。

ABA仍保留现有Rust工程；共享稳定原语可以抽库，但不整体搬迁wire或修改AWP生成包的路径。Foundation版本不在本轮升级。

## 2. HC 双角色实现

现有 AWP core 继续负责 ACP 会话、身份和加解密。新 Provider adapter 注册实际可用的 display/audio/file 等能力。二者可复用本地key-store接口，但 token scopes、ticket purpose、签名域、路由与队列分开。

用户关闭“允许Agent使用本设备能力”时，撤销Provider grant/订阅、停止采集，并保留正常HC聊天。页面退出/后台切换时撤回不再可执行的能力，不把同一浏览器实例当作持久在线硬件。

UI执行仅在主线程安全适配器进行，协议/加密在独立worker（支持时）；输入有长度上限。审批使用系统保留组件，不允许Tool提供HTML覆盖可信授权UI。卡片按结构化text呈现，过滤富文本、外部脚本与危险链接。

## 3. 各运行环境的能力边界

| 环境 | 首版可承诺的实现目标 | 必须动态检测/不能默认承诺 |
| --- | --- | --- |
| Web | 前台卡片、用户手势选择文件、受权限控制麦克风/相机 | 后台常驻、静默摄像头、系统全屏控制、全磁盘访问 |
| 小程序 | 平台许可页面能力、前台交互与配对 | 永久在线、无限socket、完整WebCrypto、系统级权限 |
| 原生App | OS许可的音频/相机/通知/本地安全存储 | iOS/Android后台行为一致、无提示敏感采集 |
| 桌面HC | 应用内交互与用户明确授权的本地能力 | 直接获得所有ABA能力或绕过OS权限 |
| Mosaico | 屏幕、触摸、有限录音/播放、状态与震动 | 完整ACP UI、无限日志、电池长续航、板端大模型 |

这是适配设计约束而非这些端已经完成的状态。每个适配器启动时构造availability，权限变化时增加revision。失去权限时返回PERMISSION_REQUIRED，不反复申请造成骚扰。

## 4. Mosaico 基线与硬件事实

官方ESP-Mosaico V1.0文档将其定义为ESP32-S31设备，target为`esp32s31`，提供480×480触摸显示、音频、IMU/磁力计、存储及BSP。文档特别要求核对CoreBoard丝印版本，USB第一次刷写可能需要手动进入下载模式；BSP提供USB CDC/自动下载路径。官方该版本列出的电池为65mAh，因此原型默认USB供电，不承诺全天便携续航。

来源：<https://docs.espressif.com/projects/esp-dev-kits/en/latest/esp32s31/esp-mosaico/user_guide.html>（2026-09-13查阅）。用户实际到货版本尚未确认；文档事实不代替真机验收。

不依赖ESP-Claw或Linux。C/C++ + ESP-IDF + 官方BSP是首版路线。先固定实际支持该板的ESP-IDF commit/tag、BSP版本、组件lock、编译器与分区表，再构建；不从聊天里猜ESP-IDF版本号。

## 5. 固件模块边界

- BoardAdapter：只由BSP封装LCD/touch/I2S/IMU/电量/震动；业务代码不散落GPIO常量。
- IdentityStore：sign/HPKE key handle、pairing、授权缓存、factory reset。
- MdpClient：TLS/WSS、DPoP、challenge、bounded decode、加密envelope、重连。
- DeviceRuntime：静态合约注册、参数校验、本地权限、资源锁和execution ledger。
- ProjectionUI：设备主页、任务卡、交互、专用审批、断线/UNKNOWN状态。
- AudioTask：按键采集/播放、有界ring buffer、可中断、显式指示。
- UpdateManager：签名固件、板型/版本校验、双槽恢复与授权发布。

网络和驱动回调不直接执行阻塞动作；通过有界队列交给专门任务。所有队列满时明确背压，音频流量不能饿死取消/审批控制。

## 6. 内存与持久化预算

设计估算，不是已测值：480×480 RGB565双缓冲约921,600 bytes；16kHz/16bit单声道30秒原始音频约960,000 bytes。不能把全部日志、音频、UI双缓冲和多条64KiB包都放内部SRAM。

采用DMA所需内部内存与大块PSRAM分层、有限显示缓冲、音频分块、组件峰值统计。先使用官方支持的NOR持久区域保存控制Journal，避免第一版依赖尚未验证的NAND掉电一致性；NAND优先用于可恢复UI/媒体资产缓存。

密钥和用户内容不能当普通资产写文件；NVS加密、Flash Encryption、Secure Boot、anti-rollback各自启用与证据分开记录。开发板默认不视为hardware-backed。没有安全持久存储时降级为低权限临时设备或拒绝启用敏感能力，不能明文落长期凭据。

## 7. 语音切片

第一版是push-to-talk，不做复杂唤醒/全双工：用户按键→录音指示→最多30秒采集→用户确认/取消→加密资源上传→可信STT→Interaction Adapter→Agent→可信TTS→加密回传→播放。

语音提供商在可信主机配置，API key不放板上，也不自动继承任一聊天产品订阅。音频转写后执行动作仍经过与文字输入相同的授权和审批。网络断开停止采集/给出可理解状态，不在恢复后偷偷上传取消的录音。

## 8. OTA与硬件安全

OTA不暴露为Agent的自由调用Tool。固件发布由用户授权管理流程完成；固件签名key独立于端点身份key。manifest绑定板型、固件hash、合约版本、最低兼容MDP版本与防回滚策略。

下载到非活动槽→验证签名/板型/完整性→切换→完成健康自检后确认有效→失败自动回退。实际分区容量、回退API与bootloader能力在目标ESP-IDF/BSP上验证。启用不可逆eFuse前必须单独人工批准，不在入门脚本自动烧写。

固件不允许经网络下发任意Lua/shell/脚本来绕过已编译白名单。模型只能调用受限能力，不能改安全驱动或批准自己的固件升级。

## 9. 真机记录要求

保存板丝印、供应商版本、工具链锁、BSP/component lock、分区表、固件hash、heap峰值、启动/重连/刷写日志（脱敏）、掉电恢复、音频/显示/触摸结果和安全存储实际等级。

未完成C侧HPKE/签名互操作前，只跑本地BSP诊断，不接真实账户或私密任务。不得用能亮屏、能WSS连接替代整套安全协议兼容证据。

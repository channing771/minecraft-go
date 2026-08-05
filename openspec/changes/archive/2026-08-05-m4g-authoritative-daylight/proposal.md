## Why

M4F 即将闭合资源、工具与权威采掘循环，但世界仍使用写死的全亮天空光，服务端也没有可持久化、可在多人间同步的世界时间；露天、屋内、白天和夜晚因此没有可观察差异。下一批需要先用一个有界的最小闭环加入权威昼夜和直射天空光，为后续方块光与怪物生成提供真实需求，而不提前实现通用光照引擎。

## What Changes

- 增加由服务端权威推进的绝对世界时间；一个昼夜固定为 `24000` tick，所有 Memory/TCP 客户端从权威玩家状态观察同一相位。
- 把世界 metadata 升级到 v2 并持久化世界时间；v1 世界稳定迁移并从黎明开始，自动保存和关服保存不得阻塞权威 tick。
- 为每个区块维护由方块确定性派生的固定高度表，以当前“空气透明、其他方块不透明”规则计算直射天空光 `0/15`；高度表和天空光不进入区块存档或网络 payload。
- 让网格化使用现有 `Quad.Light` 高四位，并在最高遮挡变化时只标脏受影响的垂直范围与相邻边界。
- 让地形、远端玩家、掉落物和天空背景按固定昼夜曲线变化；HUD 与昵称保持可读。
- **BREAKING**：线上协议升级为 v9，在既有固定长度 `PlayerState` 末尾追加绝对世界时间；v8 或其他版本在登录前稳定拒绝。
- benchmark 工作负载随之改变；当时标记为 scenario v11，后因该场景的正式链失败而并入 `stabilize-benchmark-gpu-timing` 的 scenario v12。既有分辨率、样本数、绝对门禁和 `20%` 相对阈值不变；M4F 的 M5 scenario v10 基线与归档是本 change 的硬前置，M2 基线保持不变。

非目标：横向天空光传播、方块光、火把、透明或半透明方块、动态阴影、太阳/月亮天体、天气、怪物生成规则，以及通用光照 worker 或调度框架。

## Capabilities

### New Capabilities

- `authoritative-daylight`: 定义权威世界时间、metadata v2 持久化、协议 v9 同步、直射天空光、增量重网格和昼夜呈现。

### 已转移的 Capabilities

M4G 确实改变了 benchmark 工作负载，当时据此把 producer 标记为 scenario v11。但 v11 的一次性正式链在 Memory→TCP 跨 transport 比较失败：`remote_gpu_complete p95_ms` 报出 `94.4%` 退化，而同一对报告的 p50 与 p99 几乎不变。根因是该指标此前用「提交到阻塞轮询返回」的墙钟差逐次计时，取值被宿主轮询实现量化到约 `1.28ms` 的整数倍，与 M4G 的改动无关。

因此 **v11 从未成为任何硬件的基线**，其 workload 变化随后并入 `stabilize-benchmark-gpu-timing` 的 scenario v12。`bounded-benchmark-workload` 与 `hardware-performance-baselines` 两个 capability 的修改由该 change 统一承担，本 change 不再重复声明，避免把从未生效的 v11 状态写进主规格。

## Impact

- 世界与网格：`internal/world`、`internal/mesh`、`internal/client`。
- 权威状态、协议与持久化：`internal/sim`、`internal/server`、`internal/network`、`internal/storage`。
- 图形与装配：`internal/render`、`cmd/mcgo`。
- 性能契约与文档：`cmd/mcgo` benchmark、`cmd/perfcheck`、M5 基线和相关中文说明。
- 兼容性：玩家 schema 保持 v3、区块 schema 保持 v4；metadata v1 可向 v2 迁移，旧程序遇到 v2 metadata 时稳定拒绝且不得覆盖。协议仅支持 v9，不提供版本协商或降级解码。
- 并发与性能：simulation owner 继续单写世界时间；metadata I/O 复用现有有界保存 worker；每区块只增加固定 `512` 字节高度表，网格任务继续使用现有有界队列、revision 印章和上传预算。
- 依赖：不新增第三方库或内部包，不扩展架构依赖白名单。

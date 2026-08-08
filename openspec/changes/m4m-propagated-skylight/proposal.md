## Why

现有客户端只从权威方块镜像派生二值直射天空光，洞口、屋檐和侧向开口后的空间会与洞穴深处同样黑暗。M4M 在不扩大服务端权威面的前提下，补齐可验证的横向天空光传播，并把它纳入现有无窗口视觉与性能门禁。

## What Changes

- 客户端从权威方块镜像派生 `0..15` 总天空光：直射起点为 `15`，横向每传播一格减 `1`，未知邻区为暗。
- 在既有有界 Mesher 路径中异步重算传播结果，并以固定失效范围处理方块、加载和遗忘变化。
- 新增 `skylight-tunnel` 无窗口视觉场景，要求网格与上传收敛后才抓帧。
- benchmark 升级为 scenario v14，并只允许显式 `13:14` workload 迁移；M5 基线通过一次 Memory/TCP 正式链后更新。
- **BREAKING（性能报告）**：新 workload 不再接受 v13 作为当前 scenario；线上协议 v13、玩家 schema v5、区块 schema v6 和 metadata v2 不变。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-daylight`：把直射二值天空光扩展为客户端派生的横向传播天空光，并调整有界 dirty 上限。
- `visual-verification`：增加收敛后抓取的 `skylight-tunnel` 无窗口视觉场景及其失败行为。
- `bounded-benchmark-workload`：将传播后的工作负载标记为 scenario v14，并冻结唯一 `13:14` 迁移。
- `hardware-performance-baselines`：要求以 M5 v14 Memory/TCP 正式链替换 M5 当前基线，M2 保持不变。

## Impact

影响 `internal/world` 的邻域采样、`internal/mesh` 的派生网格、`internal/client` 的镜像失效与 Mesher 调度、无窗口抓帧及 `cmd/perfcheck`。光照仍仅是客户端渲染派生数据：不增加服务端光照、方块光、透明方块、长期缓存、新 worker pool 或第三方依赖；不修改协议 v13、玩家 schema v5、区块 schema v6 或 metadata v2。

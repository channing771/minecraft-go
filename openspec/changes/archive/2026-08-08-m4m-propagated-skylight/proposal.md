## Why

现有客户端只从权威方块镜像派生二值直射天空光，洞口、屋檐和侧向开口后的空间会与洞穴深处同样黑暗。M4M 在不扩大服务端权威面的前提下，补齐可验证的横向天空光传播，并把它纳入现有无窗口视觉验证与性能记录流程。

## What Changes

- 客户端从权威方块镜像派生 `0..15` 总天空光：直射起点为 `15`，横向每传播一格减 `1`，未知邻区为暗。
- 在既有有界 Mesher 路径中异步重算传播结果，并以固定失效范围处理方块、加载和遗忘变化。
- 新增 `skylight-tunnel` 无窗口视觉场景，要求网格与上传收敛后才抓帧。
- benchmark 升级为 scenario v14，并只允许同 transport 的显式 `13:14` workload 迁移；完整的 M5 Memory 报告可立即精确更新 M5 基线，TCP 报告可独立记录，只有调用方显式请求时才执行同 scenario、同 commit 的跨 transport 比较。
- **BREAKING（性能报告）**：新 workload 不再接受 v13 作为当前 scenario；所有性能数值改为只记录和报告，不再使 producer、`perfcheck`、CI 或基线提升失败。线上协议 v13、玩家 schema v5、区块 schema v6 和 metadata v2 不变。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-daylight`：把直射二值天空光扩展为客户端派生的横向传播天空光，并调整有界 dirty 上限。
- `visual-verification`：增加收敛后抓取的 `skylight-tunnel` 无窗口视觉场景及其失败行为。
- `bounded-benchmark-workload`：将传播后的工作负载标记为 scenario v14，冻结唯一 `13:14` 迁移，并把性能阈值和回归结果改为只记录。
- `hardware-performance-baselines`：要求以完整 M5 v14 Memory 报告立即精确替换 M5 当前基线，TCP 独立记录且只在调用方显式请求时参与同 scenario、同 commit 且不改变基线状态的跨 transport 比较，M2 保持不变。

## Impact

影响 `internal/world` 的邻域采样、`internal/mesh` 的派生网格、`internal/client` 的镜像失效与 Mesher 调度、无窗口抓帧及 `cmd/perfcheck`。光照仍仅是客户端渲染派生数据：不增加服务端光照、方块光、透明方块、长期缓存、新 worker pool 或第三方依赖；不修改协议 v13、玩家 schema v5、区块 schema v6 或 metadata v2。

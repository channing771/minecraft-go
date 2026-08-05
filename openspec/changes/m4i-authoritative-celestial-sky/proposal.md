## Why

M4G 已让服务端权威世界时间驱动固定天空底色，但天空仍只是整屏 clear color，玩家无法从视线方向辨认日出、正午、日落和夜间天体。M4I 先于仍在另一工作区规划的 M4H 实现，并只消费既有 `WorldTimeTicks`，形成独立可见闭环且避开物品与掉落链路。

## What Changes

- 图形客户端从最后确认的权威世界时间派生同一条固定天体轨迹：日出时太阳位于东侧地平线，正午位于天顶，日落位于西侧地平线；月亮始终位于相反方向。
- 在现有 terrain render pass 内先绘制一个程序化 fullscreen triangle，生成昼夜天空渐变、太阳、月亮和确定性星空，再由地形及现有实体、昵称和 HUD 正常覆盖。
- 天空只随权威时间和相机朝向变化；相机平移不产生视差，旧或重复玩家状态不得使天体相位回退。
- 天空不使用纹理或新增二进制美术资源；每帧只更新固定大小 uniform，不启动 goroutine、不产生热路径堆分配。
- 本 change 直接基于已归档的 M4G：保持协议 v9、玩家 schema v3、区块 schema v4 和世界 metadata v2 不变，并把含天空绘制的 workload 从 scenario v12 提升为 scenario v13。
- 首个冻结候选 `f7d8f261e910863e189666f6e2181e606996f42f` 的唯一一次正式 Memory producer 因 flying p99 `12.175ms` 与进程峰值 RSS `2452.2MiB` 违反既有绝对门禁而停止，未生成报告、未运行 TCP、未覆盖基线；后续短时 A/B 与完整时长 no-stars 诊断也没有隔离 p99 或 RSS 根因，因此先用仅 benchmark 启用的三阶段 Go heap profile 定位保留对象，不放宽 p99、RSS 或其他门禁。
- 只优化实现而保持 2560x1440、天空视觉、draw 数量、阶段时长、样本和统计口径不变时继续使用 scenario v13；若诊断证明必须改变 benchmark workload 或测量口径，则先升级场景版本并修订本 change，不能把变化藏在 v13 中。
- 非目标：云、天气、动态阴影、体积雾、天体物理、季节、方块光、横向天空光传播、透明方块，以及任何投掷、丢弃、拾取、掉落实体物理或物品栏行为。

## Capabilities

### New Capabilities

- `celestial-sky-presentation`: 定义由权威世界时间驱动的天空渐变、太阳、月亮、星空、相机关系、遮挡顺序和固定资源上限。

### Modified Capabilities

- `bounded-benchmark-workload`: 把新增程序化天空绘制纳入交互客户端与无窗口 benchmark 的同一逐帧 workload，并以 scenario v13 阻止与旧场景静默混比。
- `hardware-performance-baselines`: 使用通过既有门禁的无窗口 Memory/TCP scenario v13 报告升级 M5 基线，同时保留 M2 与历史基线。

## Impact

- 主要影响 `internal/render/daylight.go`、`internal/render/renderer.go`、新增的程序化天空 WGSL、`cmd/mcgo` 相机装配及相应测试；不新增内部包或第三方依赖。
- 性能契约影响 `cmd/mcgo` benchmark、`cmd/perfcheck`、M5 基线与相关中文文档；固定分辨率、样本数、现有绝对门禁和 `20%` 相对阈值不放宽。
- 性能修复会使用不可提升的非正式诊断运行定位 Go 堆、原生图形资源与 fullscreen shader 成本；heap profile instrumentation 只在独立诊断进程中由环境变量显式启用，产物不进入正式报告并在根因记录后从候选源码移除。旧候选及其失败步骤保持冻结，新正式链必须绑定新的实现提交、全新路径和新的明确授权。
- `internal/core`、`internal/world`、`internal/sim`、`internal/server`、`internal/network`、`internal/storage`、掉落镜像及 `internal/render/drop.go` 不在范围内。协议、metadata、玩家 schema 与区块 schema 均保持 M4G 归档版本不变。
- 天空仅消费已确认客户端状态，不改变服务端权威、Memory/TCP 一致性或存档兼容性；回退只需回退渲染、benchmark 契约和对应基线，不需要迁移世界或玩家数据。

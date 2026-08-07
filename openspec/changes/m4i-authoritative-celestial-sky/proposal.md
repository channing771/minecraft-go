## Why

已归档的 M4L 保留了服务端权威世界时间驱动的固定天空底色，但天空仍只是整屏 clear color，玩家无法从视线方向辨认日出、正午、日落和夜间天体。M4I 最初与 M4H–M4L 并行开发；进入正式性能链前必须合并当前 M4L，并继续只消费既有 `WorldTimeTicks`，形成独立可见闭环且不改变主动丢弃、工具耐久、权威箱子或权威生命值链路。

## What Changes

- 图形客户端从最后确认的权威世界时间派生同一条固定天体轨迹：日出时太阳位于东侧地平线，正午位于天顶，日落位于西侧地平线；月亮始终位于相反方向。
- 在现有 terrain render pass 内先绘制一个程序化 fullscreen triangle，生成昼夜天空渐变、太阳、月亮和确定性星空，再由地形及现有实体、昵称和 HUD 正常覆盖。
- 天空只随权威时间和相机朝向变化；相机平移不产生视差，旧或重复玩家状态不得使天体相位回退。
- 天空不使用纹理或新增二进制美术资源；每帧只更新固定大小 uniform，不启动 goroutine、不产生热路径堆分配。
- 本 change 最终基于已归档的 M4L：保持协议 v13、玩家 schema v5、区块 schema v6 和世界 metadata v2 不变，并把含天空绘制的 workload 从 scenario v12 提升为 scenario v13。
- 首个冻结候选 `f7d8f261e910863e189666f6e2181e606996f42f` 的唯一一次正式 Memory producer 因 flying p99 `12.175ms` 与进程峰值 RSS `2452.2MiB` 违反既有绝对门禁而停止，未生成报告、未运行 TCP、未覆盖基线；后续唯一一次不可提升的三阶段 Go heap profile 已把最大 live heap 保留链定位到 benchmark `MemoryStore.chunks` 的区块深拷贝。8.2 复用当前 chunk codec，让 MemoryStore 保留当前编码 payload 并在读取时解码，不放宽 p99、RSS 或其他门禁，也不改变存档格式或外部存储语义；合并 M4L 后该路径自然使用 chunk schema v6 codec。
- 只优化实现而保持 2560x1440、天空视觉、draw 数量、阶段时长、样本和统计口径不变时继续使用 scenario v13；若诊断证明必须改变 benchmark workload 或测量口径，则先升级场景版本并修订本 change，不能把变化藏在 v13 中。
- 合并 M4H 前的候选 `4410dc8b5ec76acad7d5a28980ca88b83434d35f` 及其星空短路诊断、合并 M4H 的检查点 `6badbf4f35ad3e2f5d8047761857c5d39b6cc3ca`、完成 M4J 门禁的候选 `a859fcbc0abd63b4d39fc4321c2facf6fd57a63a` 均只保留为不可提升证据；合并当前 `origin/main@17807e580ca5675bc216d1e6b3d45adc1782de67` 后必须重新完成门禁、静稳预检和正式授权，且只能使用新的候选 HEAD 与全新 Memory/TCP 路径。
- 非目标：云、天气、动态阴影、体积雾、天体物理、季节、方块光、横向天空光传播、透明方块，以及修改 M4L 既有主动丢弃、工具耐久、权威箱子、权威生命值、死亡结算、协议、模拟、镜像、持久化或呈现语义。

## Capabilities

### New Capabilities

- `celestial-sky-presentation`: 定义由权威世界时间驱动的天空渐变、太阳、月亮、星空、相机关系、遮挡顺序和固定资源上限。

### Modified Capabilities

- `bounded-benchmark-workload`: 把新增程序化天空绘制纳入交互客户端与无窗口 benchmark 的同一逐帧 workload，并以 scenario v13 阻止与旧场景静默混比。
- `hardware-performance-baselines`: 使用通过既有门禁的无窗口 Memory/TCP scenario v13 报告升级 M5 基线，同时保留 M2 与历史基线。

## Impact

- 主要影响 `internal/render/daylight.go`、`internal/render/renderer.go`、新增的程序化天空 WGSL、`cmd/mcgo` 相机装配及相应测试；不新增内部包或第三方依赖。
- 性能契约影响 `cmd/mcgo` benchmark、`cmd/perfcheck`、M5 基线与相关中文文档；固定分辨率、样本数、现有绝对门禁和 `20%` 相对阈值不放宽。
- 性能修复使用不可提升的非正式诊断定位 Go 堆、原生图形资源与 fullscreen shader 成本；heap profile instrumentation 只在独立诊断进程中由环境变量显式启用，产物不进入正式报告，并已在根因记录后从候选源码移除。旧候选及其失败步骤保持冻结，新正式链必须绑定新的实现提交、全新路径和新的明确授权。
- `internal/storage` 只修改 `MemoryStore` 的进程内 chunk 表示并复用当前 chunk v6 codec；不改变磁盘格式、schema、Store 接口或 revision/批次原子性。合并后保留 M4L 的 `internal/core`、`internal/world`、`internal/sim`、`internal/server`、`internal/network`、容器与掉落镜像，以及 `internal/render` 的耐久和生命值呈现行为；M4I 不再修改这些链路。协议、metadata、玩家 schema 与区块 schema 均保持 M4L 归档版本不变。
- 天空仅消费已确认客户端状态，不改变服务端权威、Memory/TCP 一致性或存档兼容性；回退只需回退渲染、benchmark 契约和对应基线，不需要迁移世界或玩家数据。

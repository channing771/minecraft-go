## Context

动机见 `proposal.md`。当前 M4J 客户端在 `render.DayNightAt` 中从最后确认的 `WorldTimeTicks` 得到 `Sun`、`Daylight` 和一项整屏 `ClearColor`；`cmd/mcgo.renderFrame` 把同一 `Daylight` 传给 terrain、avatar 和 item-drop renderer。`render.Renderer.Render` 已拥有第一个带 color/depth clear 的 terrain pass，先执行 GPU cull，再绘制地形。`Camera` 只带 `ViewProj`、位置、昼夜亮度和 clear color；`cameraBytes` 每帧临时分配编码切片。

M4I 最终基于已归档的 M4J 实现：协议 v11、玩家 schema v4、区块 schema v5、世界 metadata v2 和 scenario v12 已由代码、主规格与 M5 基线确认。本 change 不改协议或存档，只把包含天空 draw 的 workload 推进到 scenario v13。早期实现与 M4H/M4J 并行开发；正式性能链前必须合并当前 M4J，并重新冻结候选。

## Goals / Non-Goals

**Goals:**

- 在不新增服务端状态的前提下，把既有权威昼夜相位变成可辨认方向的程序化天空。
- 复用现有 terrain pass、相机与昼夜计算，只增加一个固定 fullscreen draw 和固定 uniform。
- 让天空在交互客户端与 headless benchmark 中走同一路径，并保留现有深度、Hi-Z、实体和 HUD 顺序。
- 清除本次触达的 terrain camera 编码分配，使新增天空后的稳定 render hot path 仍为零堆分配。

**Non-Goals:**

- 不抽象通用 sky system、celestial entity、material graph 或多维度天空接口。
- 不增加纹理、mesh 资源、云、天气、雾、阴影、横向光传播或方块光。
- 不改变 avatar、item-drop、name-tag 或 hotbar pipeline；尤其不修改 M4J 已归档的主动丢弃、工具耐久、协议、模拟、镜像、持久化和 renderer 语义。
- 不为 shader 参数增加用户配置；视觉常量只有出现真实可调需求时再提升。

## Decisions

### 1. 天体参数继续由一个 CPU 纯函数派生

扩展现有 `DayNight` 结果，使 `DayNightAt(worldTime)` 除既有亮度和 fallback clear color 外，再返回太阳方向与星空可见度。令 `θ = 2π × (WorldTimeTicks mod 24000) / 24000`，太阳方向为 `[cos(θ), sin(θ), 0]`，其中 `+X` 是东、`-X` 是西；月亮在 shader 中直接取其相反方向。令 `t = clamp(Sun / 0.25, 0, 1)`，星空可见度固定为 `1 - t²(3-2t)`，因此只在太阳接近或低于地平线时平滑出现。

这个函数不读墙钟、不保存状态，也不按渲染帧外推。Memory/TCP 客户端已经共用 `PlayerState` 的旧状态过滤，因此天空只消费 `application.worldTimeTicks`，无需新增 receiver 分支。

否决在 WGSL 中重新计算整套昼夜相位：那会让 CPU 的 terrain/entity 亮度与天空拥有两份易漂移公式。否决为天体增加网络字段：方向完全由既有绝对时间确定，复制它只会扩大协议。

### 2. 在现有 terrain pass 内先画一个 fullscreen triangle

`Renderer` 增加一条 sky pipeline、一个固定 uniform buffer 和一个 bind group。terrain pass 完成 color/depth clear 后，先以 sky pipeline 提交一次 `Draw(3, 1)`，再切回现有 terrain pipeline 执行 indirect draw。sky pipeline 使用现有 color/depth format、关闭 depth write，并把 fullscreen triangle 放在远平面内侧；深度缓冲仍保持 clear 值，后续地形正常覆盖天空。

这个顺序不增加 render pass，不修改 Hi-Z 构建，也不需要扩展 `internal/gfx`。天空只是一条背景 draw；avatar、item-drop、name-tag 和 HUD 仍在 terrain pass 之后按现有顺序绘制。

否决独立 `SkyRenderer`：它会增加 application 构造依赖、生命周期和一套只服务单个 draw 的对象。否决天空盒或球体 mesh：fullscreen triangle 不需要顶点/索引资源，也不会受相机平移影响。否决单独 sky pass：现有 pass 已提供正确 clear 和覆盖顺序，新增 pass 只增加编码与 load/store 成本。

### 3. 世界方向由调用方提供的逆 ViewProj 重建

`render.Camera` 增加值类型 `ViewProjInv`。`cmd/mcgo.renderFrame` 每帧只调用一次 `Camera.ViewProj()`，再从该局部值派生一次逆矩阵，把同一 `viewProj` 继续传给 terrain、avatar 和 item-drop，并只让 terrain renderer 消费逆矩阵。`cmd/gfxspike` 使用相同装配并固定在正午相位；单元测试使用 identity 矩阵。

vertex shader 用内建 `vertex_index` 生成 fullscreen triangle；fragment shader 将屏幕 NDC 的远平面点乘 `ViewProjInv` 并归一化为世界视线。uniform 不包含相机位置，因此平移没有天空视差。

测试通过真实 `renderFrame` 写入的 terrain、avatar、item-drop 与 sky uniform 验证正向矩阵、逆矩阵和 `Daylight` 的共享关系；单次计算保持为 `renderFrame` 内一个局部值及一次 `Inv()` 的直接源码结构，由评审核对，不为调用次数加入 production hook、接口或回调。

否决在 `Renderer.Render` 内求逆：该函数的其他调用点常用零值测试 Camera，隐式求逆会把不可逆输入变成 CPU panic，也会重复 app 已知的相机工作。否决传 yaw/pitch/FOV/aspect：这会复制 `client.Camera` 的投影规则并扩大 render 与 client 的耦合。否决为自动计数抽象 camera 或注入矩阵函数：调用次数是局部实现约束，测试专用 production instrumentation 会扩大 API 且可能改变被测路径本身。

### 4. 一个 WGSL 同时生成渐变、天体和星空

`internal/render/shader/sky.wgsl` 使用固定常量完成以下合成：

- 以世界视线 Y 的平滑函数在地平线色和天顶色之间插值，再用既有 `Daylight` 混合日夜配色；日间天顶继续使用现有 `[0.42, 0.68, 0.92]`，夜间天顶继续使用 `[0.02, 0.03, 0.08]`，日间/夜间地平线分别固定为 `[0.72, 0.82, 0.95]` 与 `[0.06, 0.07, 0.12]`。
- 太阳和月亮各使用固定 `4°` 角直径及窄 `smoothstep` 边缘，通过视线与天体方向的点积生成圆盘；太阳固定为暖白 `[1.0, 0.92, 0.68]`，月亮固定为冷白 `[0.72, 0.80, 0.95]`，只有各自位于地平线上方时可见。
- 星空按归一化世界视线的固定量化单元和整数 hash 生成，使用固定密度、亮度和小圆点阈值，再乘 CPU 传入的夜间可见度与地平线上方遮罩。相同方向永远得到相同星点，不读取帧号或相机位置。

颜色、角直径、星密度和 hash 常量直接留在 shader；不增加配置或注册表。shader 编译测试、纯函数相位测试和 headless 像素探针共同固定行为。

否决噪声纹理和 sky cubemap：它们增加二进制资产、采样器和版权来源管理。否决复杂大气散射：当前没有天气、太阳阴影或曝光系统，固定渐变已经满足可辨认昼夜方向的目标。

### 5. uniform 编码复用 Renderer 内固定字节数组

terrain camera uniform 保持现有 `80` 字节布局；sky uniform 使用 `96` 字节：`ViewProjInv` 64 字节、太阳方向与 `Daylight` 16 字节、星空可见度及 padding 16 字节。`Renderer` 持有两个固定上传数组，编码函数写入调用方提供的切片，替换当前会分配的 `cameraBytes` 返回值。每帧固定执行 terrain camera 与 sky uniform 各一次 `Buffer.Write`，不创建切片、纹理、mesh 或集合。

否决把 sky 字段塞进 terrain 的 80 字节 uniform：两个 pipeline 的字段用途不同，共用布局会扩大 terrain shader 契约并使测试难以分别证明写入。两个固定 buffer 的成本更小且生命周期清楚。

### 6. 性能场景从 v12 升至 v13

程序化天空改变 still/flying 每一帧的 GPU workload，因此不能与 M4J 保留的 scenario v12 静默相对比较。producer、完整性校验和 `perfcheck` 只增加精确 `12:13` 迁移；该迁移继续只做报告完整性、硬件一致性和现有绝对门禁，不做跨 workload 相对比较。建立 v13 后，`12:13` 以外的旧迁移参数全部退役；历史 v6-v12 报告仍可单独校验和同场景比较。

固定 2560x1440、still/flying 时长、消息/mesher 上限、RSS、服务端 tick、GPU 探针、样本数、最小有意义增量、绝对门禁和 `20%` 相对阈值全部不变。`remote_gpu_complete` 仍只测远端角色与昵称批量 draw；天空成本由真实 still/flying 帧覆盖，不污染该指标定义。

### 7. 首个正式失败后先定位资源，再优化 shader

首个冻结候选 `f7d8f261e910863e189666f6e2181e606996f42f` 在完整门禁、五分钟冷却、两次静稳预检和精确授权后只执行了一次 Memory producer。still 为 p99 `5.702ms`、RSS `1378.9MiB`；flying 为 p99 `12.175ms`、RSS `2280.9MiB`；GPU 采样后进程历史峰值升至 `2452.2MiB`，随后八会话服务端探针因 `rss=2571304960` 超过既有 `2GiB` 上限而拒绝结果。producer 以 exit 1 结束，没有生成 JSON，未运行 `perfcheck` 或 TCP，M5/M2 基线字节均未改变。

修复不从调整门禁开始。第一轮明确标记、不可提升的诊断已完成：短时 full、no-sky 与 no-stars 没有分离 RSS，短时 no-stars 虽把 flying p99 从 `12.092ms` 降至 `11.555ms`，但保持生产 `60s/120s` 时长的 no-stars 仍为 `12.024ms`，GPU 采样后 RSS 仍为 `2353.3MiB`。这些单样本没有确认 `star_light` 的尾部成本，也没有隔离 RSS 来源，因此只作为负面证据。

随后只给 `cmd/mcgo` benchmark 临时增加 heap profile instrumentation，没有扩展正式 CLI 或报告 schema。私有环境变量 `MCGO_BENCHMARK_HEAP_PROFILE_PREFIX` 为空时完全不写文件；非空时在 post-still、post-flying、post-GPU 三个既有阶段边界分别调用最小 helper。helper 在每个快照前连续执行两次 `runtime.GC()`，再用标准库 `runtime/pprof.WriteHeapProfile` 以 `O_CREATE|O_EXCL|O_WRONLY` 与 `0600` 写独立文件。该 instrumentation 及其测试已在分析后逐字移除，候选源码不保留 profiling 开关。

instrumentation 只在一次 full-stars、生产 `10s/60s/120s/30s`、Memory transport 的不可提升运行中启用。不可追加的 pre-run sidecar 与独立 post-run sidecar 固化了精确 HEAD、干净状态、baseline 哈希、命令、输出元数据、birthtime 与 SHA-256。该次运行的 post-flying live Go heap 相对 post-still 增加 `523.25MB`；最大单一保留链 `server.saveWorker → storage.MemoryStore.SaveBatch → world.Chunk.Clone → world.Section.Clone → world.PalettedContainer.Clone` 保留 `402.35MB`，约占增量 `77%`，post-GPU 又增至 `622.23MB`。最终 owner 是 benchmark `storage.NewMemory` 创建并持有到进程结束的 `MemoryStore.chunks` map。profile 强制 GC 扰动运行，因此帧时、RSS 与失败状态仍只作诊断上下文，不得用于门禁、`perfcheck` 或基线。

8.2 在 `internal/storage.MemoryStore` 修复这个 owner，而不建立 benchmark 专用旁路。`memoryChunk` 不再长期持有 `*world.Chunk` 深拷贝，改为持有现有 `encodeChunkPayload` 产生的当前 zstd envelope；`LoadChunk` 使用现有 `decodeChunkPayload` 重建独立 chunk。这样继续保留所有 ChunkKey、revision、hash、重载结果与无别名语义，同时删除每个持久化区块的 Go 对象图、palette lookup map 和切片容量开销。该实现最初使用当时的 chunk v4 codec；合并 M4J 后同一调用自然使用当前 chunk v5 codec，不改变磁盘格式、Store 接口或 scenario v13 workload。

`SaveBatch` 必须继续全批校验后才产生可见提交。实现先完成 revision/hash 冲突归并，再为全部 pending candidate 编码；任一编码或 context 错误直接返回且 `store.chunks` 不变，只有全部编码成功后才逐项替换 map。编码返回的新切片已经由 Store 独占，不再额外 `bytes.Clone`。`LoadChunk` 在既有 mutex 保护下直接解码不可变 payload；先保持当前简单锁边界，只有实际竞争证据才考虑复制 payload 后解锁或引入 codec pool。

现有 M2 微基准显示单 chunk encode 约 `0.21ms`、decode 约 `0.22ms`；encode 的约 `1.79MB/op` 是短期分配而非长期 owner。保存仍运行在两个有界后台 worker，不阻塞权威 tick；8.2 必须用公开 Store API 的确定性 retained-heap 检查、既有无别名测试、storage race 测试、现有 codec/golden 覆盖和修复后的不可提升 full-stars 生产时长 Memory 诊断共同验证。如果 live heap owner 已消失但 p99 或 RSS 仍失败，保留该正确性修复并以新证据更新后续计划，不能预先加入 zstd pool 或放宽门禁。

否决 benchmark 专用丢弃、LRU 或限容 Store：它们会让已确认保存的旧 ChunkKey 在重载时丢失，破坏单机与正式 Store 的持久化语义。否决把 benchmark 改到临时 DiskStore：它会把文件系统、region cache 与 compaction I/O 混入 v13 workload。否决自建无压缩 memory codec 或提前池化 zstd：当前 chunk codec 已提供上限、校验和重建路径，只有实测证明 codec CPU/临时分配仍阻止门禁时才扩展。

8.2 的唯一一次 full-stars、生产 `10s/60s/120s/30s` Memory 诊断已经闭合 RSS：flying RSS/HeapAlloc 为 `1459.7/562.5MiB`，GPU 后与八会话 server probe 峰值均为 `1652.0MiB`，producer exit `0` 并写出 JSON。该输出只证明 encoded MemoryStore 移除了已定位的 retained owner；它没有运行 `perfcheck` 或 TCP，也不得提升为 baseline。稳定 `Renderer.Render` 已有真实候选零分配测试，因此不能把“零 Go 分配”等同于“进程无资源滞留”。

8.3 只做一个等价 shader 短路。`fs_main` 先把 `sky.star_visibility.x` clamp 到 `[0,1]`，令星光初值为 `0`；只有 visibility `>0` 且 `direction.y>0` 时才调用现有 `star_light`，随后继续乘原有 `smoothstep(0, 0.08, direction.y)`。这只跳过结果必为零的完整 cube-face、整数 hash、cell 与圆点计算；`star_light` 本身、hash 常量、星点密度/亮度、地平线渐隐、天空颜色、太阳/月亮圆盘、world-direction 重建、一次 fullscreen draw 和 `96` 字节 uniform 全部逐字保持。否决重写 hash、降低星点密度、移除 draw、降低分辨率、增加纹理或 CPU 分支，因为它们会改变固定星图、视觉或 workload；也不增加只锁定 WGSL 私有源码形状的测试，既有真实 headless 像素与零分配测试负责锁定可观察行为，评审直接核对受限 shader diff。

实现前后运行同一组 `TestSkyHeadless`、sky pipeline/draw/uniform 与 `TestRendererRenderDoesNotAllocate` 特征测试。通过后在已评审、干净的新 HEAD 上只执行一次不可提升的 full-stars、生产 `10s/60s/120s/30s` Memory v13 诊断，使用全新 `diag` 路径且不启用 heap instrumentation；不得运行 `perfcheck`、TCP、复制 baseline 或在失败后重试。只有 producer 正常写出报告、全部既有绝对门禁保持且 flying p99 `<12ms` 时才完成 8.3；否则保留证据、保持 8.3 未完成并先修订本 change，不能继续猜测另一项 shader 优化。

只要最终视觉、draw 数量、2560x1440、阶段时长、样本和指标定义不变，优化后的 producer 仍是 scenario v13。若必须降低分辨率、减少天空 draw、改变阶段/样本或修改测量定义，则该方案改变 workload，必须先把场景升级为 v14 并重新设计迁移；不得用它给 v13 制造通过。

否决直接把 RSS 门禁提高到 `2.5GiB` 或把 p99 放宽到 `14ms`：M4J 保留且未改写的 M5 v12 Memory 基线为 flying p99 `9.488ms`、进程峰值 `1672.8MiB`，本次 `2452.2MiB` 不是边界误差。否决直接重复旧 producer：一次性正式结果已经失败，重复运行既不能定位根因，也违反正式链约束。

### 8. 合并当前目标分支后重新冻结正式候选

星空短路候选 `4410dc8b5ec76acad7d5a28980ca88b83434d35f` 完成了 pre-M4H 的全仓门禁和不可提升诊断，但远端 `main` 随后归档到 M4H `15f2cf84d6b7e7186b17a0efb6d1ab5c019201e0`。M4I 在 `6badbf4f35ad3e2f5d8047761857c5d39b6cc3ca` 合入 M4H 并通过聚焦门禁后，远端 `main` 又归档到 M4J `34c9ba56ad213076c876ab5fffde791ed45ba6fb`。这些旧候选及诊断只证明 shader 与 encoded MemoryStore 的局部结论，不得进入正式迁移、TCP 比较或基线。

合并时保留 M4J 的 protocol v11、玩家 schema v4、区块 schema v5、主动丢弃、工具耐久、迁移、golden 与快捷栏呈现，同时保留 M4I 的权威天空、MemoryStore 编码保留、scenario v13 和唯一 `12:13` 迁移。`README.md` 冲突按两项能力并存解决；不得删除任一侧测试、二进制 golden 或把 M4J 的 protocol/schema 值退回。M4J 明确保持默认 benchmark workload 与两份基线字节不变，因此合并后仍使用 scenario v13；若冲突解决必须改变 draw、分辨率、阶段、样本、运动或指标定义，则先升级 v14。

合并完成后重新运行天空、主动丢弃、工具耐久、Memory/TCP、协议、存储、迁移/golden/fuzz、全仓 race、vet、archcheck、相关 benchmark 和 OpenSpec strict；通过后提交新的精确 HEAD。只有该 HEAD 完成五分钟冷却、两次静稳预检并绑定两个全新路径后，才能请求新的正式授权。

### 9. 受影响文件与依赖方向

- `internal/render/daylight.go`、`daylight_test.go`：纯天体相位、方向和夜间可见度。
- `internal/render/renderer.go`、相关 renderer 测试、`shader/sky.wgsl`：固定 sky pipeline、uniform、draw 顺序、生命周期和零分配。
- `cmd/mcgo/app.go`、`app_test.go`、`cmd/gfxspike/main.go`：一次矩阵计算与相位装配。
- `cmd/mcgo/benchmark.go`、scenario 测试、`cmd/perfcheck`：scenario v13 与唯一迁移。
- `internal/storage/memory.go`、`memory_test.go` 与聚焦内部测试：MemoryStore 使用当前 chunk v5 编码 payload 保留区块，并保持批次原子性与无别名读取。
- `README.md`、`docs/notes/perf-baseline*.{md,json}`：用户可见能力、场景兼容与正式基线证据。

不新增包、第三方依赖或 archcheck 白名单。只有 `internal/render` 继续通过既有 `internal/gfx` 封装接触 GPU；其他内部包不新增依赖。

## Risks / Trade-offs

- [实现起点与目标分支归档值漂移] → 正式链前核对归档代码、主规格和 M5 基线；任何落号差异先更新本 change 全部产物，禁止带着错误起点编码。
- [fullscreen shader 增加 fill-rate 成本] → 只画一个 triangle、无纹理采样且与 terrain 同 pass；用固定分辨率 headless benchmark 和既有门禁验证，不提高阈值。
- [星点在量化边界闪烁或地平线出现接缝] → hash 使用世界方向而非屏幕坐标并以平滑边缘限制亮度；用固定相机的 headless 像素与旋转往返测试锁定稳定性。
- [sky draw 意外写深度或改变覆盖顺序] → pipeline 显式关闭 depth write，并用命令记录测试和有地形遮挡的 headless 像素测试验证。
- [新增 uniform 造成逐帧分配] → 两份编码都写入 Renderer 自有固定数组，并用 `AllocsPerRun` 覆盖稳定 Render。
- [WGSL 与 CPU 昼夜曲线漂移] → CPU 计算方向、昼夜亮度和星空可见度，shader 只做基于这些参数的颜色合成。
- [诊断运行被误当成新基线] → 所有诊断路径使用独立 `diag` 名称，不执行正式迁移比较，不复制到 `docs/notes/perf-baseline*.json`；新正式运行仍需新 HEAD、全新路径和明确授权。
- [heap profile 扰动后续阶段] → profile 前强制 GC 与序列化仅用于定位保留对象，运行指标全部标记不可提升；正式候选移除 instrumentation 后从新 HEAD 重新执行完整门禁。
- [编码降低 live heap 但增加后台 CPU 与短期分配] → 先复用现有有界 codec 和两个 save worker，用 storage 微基准与完整诊断观察；没有证据时不增加 pool、缓存或并行度。
- [编码失败造成半批提交] → pending candidate 全部编码成功后才替换 `MemoryStore.chunks`，并用含一个不可编码 chunk 的批次测试锁定零部分提交。
- [只优化 p99 掩盖内存回归] → 固定先闭合 RSS 来源与 `2GiB` 门禁，再优化 shader；两者都通过后才能冻结候选。
- [动态分支反而产生 GPU divergence] → 只在全帧一致的 daytime visibility 为零或地平线以下像素短路；用真实 full-stars v13 诊断判定，不根据静态指令数宣称收益，失败后不叠加第二项优化。
- [为了通过而静默改变 workload] → 逐项核对 draw 数量、分辨率、阶段、样本和指标定义；任一变化先升级到 v14，不能继续标记 v13。

## Migration Plan

1. 核对 M4J 已完成规格同步和归档，并确认协议 v11、玩家 schema v4、区块 schema v5、metadata v2、scenario v12 与 M5 v12 基线；若任一不同，先修订本 change 并重新严格校验。
2. 先用纯函数和 fake/headless renderer 测试锁定相位、uniform、资源生命周期、draw 顺序和像素结果，再接入交互客户端与 `gfxspike`；自动验证保持无窗口。
3. 把 benchmark producer 和比较器升级为 scenario v13，冻结候选并完成全仓、架构、零分配和渲染 benchmark 门禁。
4. 保留候选 `f7d8f261e910863e189666f6e2181e606996f42f` 的正式失败证据，不重跑、不生成或修补报告、不运行 TCP；短时 A/B 与完整 no-stars 为负面证据，唯一三阶段 heap profile 已把 RSS 的最大 live owner 隔离到 `MemoryStore.chunks`，临时 instrumentation 已移除。
5. 先让 MemoryStore 以当前 chunk payload 保留区块，完成确定性 retention/原子性测试、storage race 测试与不可提升的 full-stars 生产时长 Memory 诊断；RSS 来源闭合后再处理 shader p99。保持 workload 不变时继续使用 scenario v13；若必须改变 workload 或测量口径，先修订产物并升级到 v14。
6. 合并当前 M4J 后重新运行完整门禁并冻结新候选；所有旧候选与诊断只作不可提升证据，不复用其授权或路径。
7. 新 Memory 通过相应场景迁移的完整性/绝对门禁后只执行一次 TCP，同场景跨 transport 通过后才提升 M5 Memory 精确字节。M2 文件保持不变。
8. 回退时同时回退天空 renderer、场景比较规则和新 M5 基线，恢复 M4J 的 v12 基线；保留 M4J protocol v11、主动丢弃与工具耐久能力，全部世界/玩家数据无需迁移。

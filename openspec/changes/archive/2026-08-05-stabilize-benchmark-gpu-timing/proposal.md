## Why

`remote_gpu_complete` 探针目前用「提交命令 → `Poll(wait=true)` 返回」的墙钟差作为一次样本。实测表明这个量几乎不含 GPU 绘制信息：

- 提交一个**完全空**的 command buffer，p50 为 `1.276ms`；提交一次 2560x1440 的 clear pass，p50 为 `1.284ms`。两者中位数相同，说明测得的主要是 `wgpuDevicePoll` 的固定往返开销，而不是远端角色与昵称的绘制耗时。
- 所有耗时都被量化到约 `1.28ms` 的整数倍：单次 clear 的高簇位于 `2.5ms`，64 次 clear 的簇位于 `2.5/3.9/5.2/6.4ms`，簇间距恒为 `1.28ms`。该节拍位于 wgpu-native 内部，调用方无法调整，增大绘制量只会把跳变移到更高倍数。
- 因此分位数落在「一倍簇与二倍簇的比例」临界点上：高簇占比在 `1%`–`6%` 之间的正常漂移，就能让 p95 从 `1.30ms` 跳到 `2.53ms`。把 `20%` 相对回归阈值套在量化步长为 `100%` 的指标上，数学上无法稳定。

M4G 的 scenario v11 正式链正是因此失败：Memory→TCP 跨 transport 比较报出 `remote_gpu_complete p95_ms` 退化 `94.4%`，而同一对报告的 p50（`1.279` 对 `1.279`）与 p99（`2.547` 对 `2.552`）几乎不变。这是门禁缺陷，不是 M4G 引入的性能退化。

同时 benchmark 目前连续 `190` 秒无 vsync 满速渲染（预热 `10s` + still `60s` + flying `120s`，`280`–`560 FPS`），GPU 长期满载。`remote_gpu_complete` 恰好在这段满载之后采集，热节流很可能是高簇占比漂移的推手之一；持续满载也让本机风扇长时间高转速。

## What Changes

- 把 `remote_gpu_complete` 改为批量分摊测量：一个样本记录一批固定数量远端绘制的总耗时除以该数量，使 `Poll` 的固定节拍被摊薄到可忽略，指标真正反映每次绘制的成本。
- 让相对回归门禁只作用于分辨率足以支撑 `20%` 判定的指标；量化或不可比的指标改用绝对上限表达。
- 在 benchmark 阶段之间加入显式冷却窗口，降低持续满载与热节流，同时不改变任何被测量的量。
- **BREAKING**：benchmark 升级为 scenario v12，只允许显式 `11:12` 迁移；`10:11` 退役。
- M4G 的 scenario v11 候选保持冻结不动；v12 基线建立后 M4G 再继续归档。

非目标：不改变 still/flying 时长与相机脚本、不改变 `2560x1440` 分辨率、不放宽任何既有绝对门禁、不引入自动硬件探测或跨硬件归一化、不修改 M2 基线、不引入通用 GPU profiling 框架。

已否决 GPU 时间戳查询方案：当前 WebGPU 绑定只暴露 `QueryTypeTimestamp` 常量与查询集合的创建/解析，没有任何写入时间戳的入口（`RenderPassDescriptor.TimestampWrites` 在绑定中被标注为 unused，也没有 `WriteTimestamp`），查询集合无法被填充。改用批量分摊无需新增依赖。

## Capabilities

### Modified Capabilities

- `bounded-benchmark-workload`：GPU 完成探针改用 GPU 时间戳、阶段间冷却窗口、scenario v12 与显式 `11:12` 迁移。
- `hardware-performance-baselines`：v12 正式链规则，以及 M4G v11 候选与本次基线的先后关系。

## Impact

- benchmark 与门禁：`cmd/mcgo` benchmark 与多人探针、`cmd/perfcheck`。
- 性能记录与文档：`docs/notes/perf-baseline.md`、`README.md`。
- 兼容性：不触及线上协议、玩家 schema、区块 schema 或世界 metadata；只有 benchmark 报告的 `scenario_version` 与 GPU 指标语义变化。历史 v6–v11 报告保持可读。
- 并发与性能：不新增 goroutine、worker 或无界队列；时间戳查询集合为固定大小，冷却窗口只是阶段之间的等待。

## Why

M5 无窗口 Memory 基线在系统无法进一步清理时出现 `flying p99 14.149ms`，超过既有 `12ms` 门禁。代码核查发现 benchmark 把消息排空上限 `4096` 同时当作单帧网格工作上限，而交互客户端只允许 `64`；更多 worker 因此可能把大批完成结果集中复制、上传到一个被计时帧中。

## What Changes

- 将客户端帧的消息排空上限与网格调度、回收上限分开传递。
- benchmark 载入阶段仍可快速收敛，但预热和正式计时使用与交互客户端一致的单帧网格上限 `64`，消息排空能力保持不变。
- **BREAKING**：因为正式 workload 语义改变，性能场景从 v6 升为 v7；v6 报告和 M2 基线保持冻结，比较器只在显式授权时接受同硬件 `6:7` 迁移。
- 保持现有延迟、FPS、RSS 阈值不变，不新增依赖、配置项或自动调参。
- 非目标：降低门禁、按硬件设置不同预算、重写 mesher/renderer、自动重跑失败的正式 M5 基线。

## Capabilities

### New Capabilities

- `bounded-benchmark-workload`: 规定计时帧必须分别限制消息排空与网格工作，并用新场景版本标识 workload 变更及兼容规则。

### Modified Capabilities

无。

## Impact

- 影响 `cmd/mcgo` 的帧驱动与 benchmark 场景版本、`cmd/perfcheck` 的版本兼容校验，以及相应测试与性能文档。
- 不影响游戏协议、存档格式、服务端权威边界、图形依赖边界或前台窗口行为。
- `apple-m5-performance-baseline` 在本修复完成前继续暂停；恢复时必须先把其规划更新为 scenario v7，并重新取得正式执行授权。

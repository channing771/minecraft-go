## Why

scenario v7 的 `remote_gpu_complete` 文档声称测量 `Submit + Device.Poll(true)`，实现却把标签准备、命令编码和资源释放也计入；同时 256 个样本的 p99 只由第三慢样本决定，使相同探针代码在同一 M5 上出现 2.618–4.909ms 的不可重复尾部波动，并阻断与远端渲染无关的 M4D 收尾。需要先纠正指标语义并提高单次正式报告的统计稳定性，不能靠重跑、提高阈值或覆盖失败报告绕过门禁。

首轮 scenario v8 正式链进一步暴露了阶段边界缺失：Memory 自检通过，但 TCP 的 GPU p99 为 `2.549958ms`，相对 Memory 的 `1.338333ms` 退化 `90.5%`。代码在关闭客户端后立即启动 GPU 探针，而服务端 trusted observer 只会在异步 writer 失败后卸载；探针本身不使用 transport，因此该结果不能证明 TCP 业务路径回退，只能证明关闭收尾、服务端 tick 与 Metal/OS 尾部仍可能污染样本。

## What Changes

- **BREAKING**：性能场景从 v7 升级为 v8；`remote_gpu_complete` 只测量 `Submit + Device.Poll(true)`，明确排除标签准备、命令编码和资源释放。
- 每份 v8 报告固定采集 2048 个 GPU 完成样本，使 p99 由约第 21 慢样本决定；20% 相对回归阈值和现有绝对门禁保持不变。
- GPU 探针开始前由服务端显式、幂等地卸载 trusted observer 并同步关闭其 endpoint；不得依赖 Memory/TCP 对端关闭传播或异步 writer 失败来形成测量边界。
- `cmd/perfcheck` 继续读取并校验历史 v6/v7 报告，但 v8 报告必须包含至少 2048 个 GPU 完成样本；不同 scenario 仍不得静默相对比较。
- 首轮 v8 Memory/TCP 报告只保留为失败证据，不得重跑、复用或提升。阶段屏障修复提交后，以新的精确 HEAD 和全新路径重新授权一条 Memory/TCP 各一次的无窗口正式链；任一步失败立即停止。全部通过后才更新 M5 当前基线及中文来源记录，M2 基线保持不变。
- M4D 的性能门禁只在 v8 基线建立后恢复，不提升本次失败的 v7 报告，也不把自比较当作 M4D 前后回归证明。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `bounded-benchmark-workload`：升级 scenario v8，收窄 GPU 完成计时边界并把固定样本数提高到 2048，同时保持既有阈值。
- `hardware-performance-baselines`：用一次性无窗口 Memory/TCP v8 正式链更新 M5 当前基线，保留跨硬件隔离、失败即停和显式选择规则。

## Impact

- 受影响代码：`internal/client/perf.go`、`internal/server` 的 trusted observer 生命周期、`cmd/mcgo/benchmark.go`、`cmd/mcgo/multiplayer_benchmark.go`、`cmd/perfcheck` 及对应测试；不改变游戏协议、存档、权威 tick、交互渲染或第三方依赖。
- 受影响文档：`docs/notes/perf-baseline-m5.json`、`docs/notes/perf-baseline-m5.md`、相关 OpenSpec 主规格，以及 M4D 的性能验收任务。
- 性能影响：正式 benchmark 增加约 1792 次离屏远端角色/昵称提交，预计只增加数秒离线验证时间；交互客户端和服务端运行时不受影响。
- 兼容性：v6/v7 JSON 仍可读取和校验，但不能与 v8 进行未授权的相对比较；M2 scenario v6 基线内容和路径不变。
- 验证：使用无窗口单元测试锁定计时事件边界、v8 样本下限和场景拒绝；正式报告仍只允许 Memory/TCP 各一次，失败不得重跑或覆盖基线。

## Why

M4D 的 M5 scenario v8 Memory 报告只因 `interest_diff.p99_ms` 相对基线退化 25% 而失败，但代码追踪和同提交历史报告证明该字段实际计量的是未隔离的整段单会话发布时间，并会在相同服务端探针代码下产生超过 20% 且方向反转的尾部波动。继续把它当作同硬件、同 transport 的稳定相对指标，会把运行时调度噪声误判为业务回退并反复阻断一次性正式验收。

## What Changes

- 从 scenario v6 及后续同 transport 的 20% 相对比较配置中移除 `interest_diff` 的 p50、p95 和 p99；不改变其他稳定指标、20% 比例或任何绝对门禁。
- 继续在报告中记录 `interest_diff`，并保留样本数、正值、分位数单调性和报告完整性校验；文档明确该历史字段实际表示单会话完整发布时间，不声称是纯兴趣差分。
- 不修改 benchmark producer、报告 schema 或工作负载，因此 scenario v8 和现有 M5 基线保持不变。
- 修复后只重新判定现有不可变 M4D Memory 报告，不重新采样；Memory 通过后，从原始 M4D 精确生产提交 `6d275a8` 的隔离 worktree 生成唯一一次无窗口 TCP 报告并继续既有一次性正式链。
- 不新增 `interest_diff` 硬编码耗时阈值；服务端整体退化继续由 tick、outbound bytes、RSS、队列上限和定向 `BenchmarkEightPlayerInterest` 证据覆盖。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `hardware-performance-baselines`: 明确同硬件回归只对统计稳定的字段执行相对比较，并规定历史 `interest_diff` 字段仅保留完整性校验与诊断语义。

## Impact

- 受影响代码：`cmd/perfcheck/main.go` 与 `cmd/perfcheck/main_test.go`。
- 受影响规格与记录：`openspec/specs/hardware-performance-baselines`、本 change 产物，以及 `m4d-authoritative-crafting` 的性能验收记录。
- 不影响 `cmd/mcgo` benchmark producer、服务端发布路径、线上协议、存档格式、报告 JSON schema、业务 transport 或第三方依赖。
- 兼容性：既有 scenario v6/v7/v8 报告继续可读；相同报告在新比较器下可能不再因 `interest_diff` 的相对波动失败，这是本变更唯一有意改变的判断结果。
- 性能与并发：不修改运行时代码；不重跑已失败的 Memory 采样，不覆盖任何正式基线。

## Why

现有正式性能基线只记录 `Apple M2 / 16GiB`，而当前开发机是 `Apple M5 / 24GiB`；`perfcheck` 正确拒绝跨硬件比较。此前 M5 scenario v6 正式 Memory 执行因飞行尾延迟超限而停止，现已通过独立 change 修复并升级为 scenario v7，因此需要在保留 M2 v6 基线的同时，为 M5 重新建立独立、可审计的 v7 比较起点。

## What Changes

- 新增独立的 M5 scenario v7 Memory 基线 JSON 和中文证据文档，不覆盖、改名或迁移现有 M2 v6 基线。
- 旧 v6 失败执行及后续 v7 修复诊断都不作为基线；更新后的规划提交并重新取得一次性正式授权后，才以全新路径各执行一次无窗口 Memory 与 TCP 报告。
- Memory 报告必须通过完整性与绝对门禁，并与同次 TCP 报告完成同硬件、同场景比较，才能提升为基线。
- 文档明确按报告 `hardware` 选择基线；M2 继续使用现有路径，M5 使用新路径。
- 本 change 不修改 `perfcheck`、性能阈值、报告 schema、benchmark 场景或生产代码；scenario v7 由已完成的尾延迟修复提供。
- 非目标：自动探测硬件并选择文件、合并不同硬件数据、为未知硬件生成基线、跨硬件归一化。

## Capabilities

### New Capabilities

- `hardware-performance-baselines`: 规定不同硬件的性能基线相互独立、只能由通过门禁的无窗口报告建立，并可按硬件明确选择。

### Modified Capabilities

无。

## Impact

- 新增 `docs/notes/perf-baseline-m5.json` 与 `docs/notes/perf-baseline-m5.md`。
- 现有 `docs/notes/perf-baseline.json` 和 `docs/notes/perf-baseline.md` 保持逐字不变。
- 不影响协议、存档、并发边界、依赖或运行时行为；只更新正式执行计划并增加硬件专属性能证据。

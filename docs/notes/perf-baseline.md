# 性能基线

- 记录日期：2026-07-27
- 硬件：Apple M5 / 24 GiB
- 系统：macOS 26.5.1
- Go 版本：go1.26.0 darwin/arm64

下方是纯 Go 微基准的可读记录；真实离屏 Metal 2560×1440 场景数据在
`perf-baseline.json`。GitHub CI 不比较跨机器绝对值。性能回归由同一台基准开发机
运行 `cmd/mcgo --benchmark`，再用 `cmd/perfcheck` 比较；退化超过 20% 判红。
换硬件或 `scenario_version` 后必须显式重建基线。

```text
BenchmarkRaycastBlocks-10                       625 ns/op         0 B/op       0 allocs/op
BenchmarkEngineStepIdle-10                    9.25 µs/op        72 B/op       2 allocs/op
BenchmarkEngineStepBlockChanges-10           64.17 µs/op     3480 B/op      25 allocs/op
BenchmarkRemeshBoundaryEdit-10              151.13 µs/op   555720 B/op    6922 allocs/op
BenchmarkMeshTerrainSection-10              387.67 µs/op    52352 B/op       5 allocs/op
BenchmarkGenerateChunk-10                   617.08 µs/op    30408 B/op     133 allocs/op
BenchmarkMeshChunk-10                         2.39 ms/op    75264 B/op      49 allocs/op
BenchmarkChunkSnapshotExport-10             610.92 µs/op    46040 B/op     141 allocs/op
BenchmarkChunkSnapshotImport-10              22.58 µs/op    10592 B/op      84 allocs/op
BenchmarkVisibleSections/r32-10              13.57 ms/op  2253952 candidate_bytes/frame  70436 candidate_sections
BenchmarkPalettePayloadEstimate-10            2.55 s/op     20.57 MB
BenchmarkPalettedContainerGet-10                 41 ns/op        0 B/op       0 allocs/op
```

scenario v2 使用不创建窗口的离屏 Metal 纹理，仍执行完整 Renderer/GPU 命令；
每秒交替执行一次权威挖掘/放置，并要求所有修改被接受且最终服务端/Mirror 哈希一致。
4,489 个含 halo 区块的快照到齐耗时 8.47 秒，完成网格与上传共 23.89 秒。
静止阶段为 299.5 fps、p99 3.699 ms；飞行阶段为 608.4 fps、p99 5.183 ms；
tick p50/p95/p99/max 为 0.004/2.122/2.891/5.187 ms，峰值 RSS 约 558.5 MiB。

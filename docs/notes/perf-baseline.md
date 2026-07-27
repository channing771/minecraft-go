# 性能基线

- 记录日期：2026-07-27
- 硬件：Apple M5 / 24 GiB
- 系统：macOS 26.5.1
- Go 版本：go1.26.0 darwin/arm64

下方是纯 Go 微基准的可读记录；真实 2560×1440 场景数据在
`perf-baseline.json`。GitHub CI 不比较跨机器绝对值。性能回归由同一台基准开发机
运行 `cmd/mcgo --benchmark`，再用 `cmd/perfcheck` 比较；退化超过 20% 判红。
换硬件或 `scenario_version` 后必须显式重建基线。

```text
BenchmarkMeshTerrainSection-10          3348       356891 ns/op      52352 B/op       5 allocs/op
BenchmarkGenerateChunk-10               2122       573082 ns/op      28328 B/op     127 allocs/op
BenchmarkMeshChunk-10                    500      2402015 ns/op      75264 B/op      49 allocs/op
BenchmarkVisibleSections/r16-10          409      2993999 ns/op     572768 candidate_bytes/frame   17899 candidate_sections    0 allocs/op
BenchmarkVisibleSections/r24-10          180      6620462 ns/op    1278528 candidate_bytes/frame   39954 candidate_sections    0 allocs/op
BenchmarkVisibleSections/r32-10           96     11827455 ns/op    2253952 candidate_bytes/frame   70436 candidate_sections    0 allocs/op
BenchmarkPalettePayloadEstimate-10         1   2424647250 ns/op      20.57 MB
BenchmarkPalettedContainerGet-10    702521785         1.532 ns/op          0 B/op       0 allocs/op
```

固定场景首次加载 4,489 个含 halo 区块耗时 3.23 秒。静止阶段为 120.0 fps、
p99 8.85 ms；飞行阶段为 120.0 fps、p99 8.95 ms；峰值 RSS 约 287 MiB。

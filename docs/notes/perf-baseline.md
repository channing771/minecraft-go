# 性能基线

- 记录日期：2026-07-28
- 硬件：Apple M5 / 24 GiB
- 系统：macOS 26.5.1
- Go 版本：go1.26.0 darwin/arm64
- framebuffer：2560x1440

下方是 M2B 玩家物理热路径的 CI-equivalent 微基准记录；真实离屏 Metal 场景数据在
`perf-baseline.json`。GitHub CI 不比较跨机器绝对值。性能回归由同一台基准开发机
运行下列命令，再用 `cmd/perfcheck` 比较；退化超过 20% 判红。换硬件或
`scenario_version` 后必须显式重建基线。

```text
BenchmarkStepPlayerFlat-10          21833 ns/op       0 B/op       0 allocs/op
BenchmarkStepPlayerColliding-10     14666 ns/op       0 B/op       0 allocs/op
BenchmarkStepPlayerStepping-10       6917 ns/op       0 B/op       0 allocs/op
```

scenario v3 的完整测量命令如下，仅使用不创建或聚焦窗口的离屏 Metal 纹理：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m2b-perf.json'
```

4,489 个含 halo 区块的快照到齐耗时 8.472557291 秒，完成网格与上传共
26.205887625 秒。静止阶段为 251.90006021798524 fps、p99 8.427 ms、峰值 RSS
433520640 bytes；飞行阶段为 468.91802505132716 fps、p99 6.496 ms、峰值 RSS
451837952 bytes。tick p50/p95/p99/max 为 0.007/2.096/2.934/24.605 ms。

两阶段均满足 fps >= 100、p99 < 12 ms、峰值 RSS < 2147483648 bytes；tick 满足
p99 < 10 ms、max < 50 ms。报告写入前，最终 trusted observer 中心的服务端与
Mirror revision/hash 已成功一致。

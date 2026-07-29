# 性能基线

- 记录日期：2026-07-29
- 硬件：Apple M5 / 24 GiB
- 系统：macOS 26.5.1
- Go 版本：go1.26.0 darwin/arm64
- framebuffer：2560x1440

下方是玩家物理热路径和 M3A 存储路径的同机微基准记录；真实离屏 Metal 场景数据在
`perf-baseline.json`。GitHub CI 不比较跨机器绝对值。性能回归由同一台基准开发机
运行下列命令，再用 `cmd/perfcheck` 比较；退化超过 20% 判红。换硬件或
`scenario_version` 后必须显式重建基线。

```text
BenchmarkStepPlayerFlat-10          21833 ns/op       0 B/op       0 allocs/op
BenchmarkStepPlayerColliding-10     14666 ns/op       0 B/op       0 allocs/op
BenchmarkStepPlayerStepping-10       6917 ns/op       0 B/op       0 allocs/op

BenchmarkChunkEncode-10             93822 ns/op 1691301 B/op      89 allocs/op
BenchmarkChunkDecode-10             30096 ns/op   60128 B/op     115 allocs/op
BenchmarkDiskStoreSave32-10      39213249 ns/op 54161814 B/op    2910 allocs/op
BenchmarkDiskStoreColdLoad-10       98878 ns/op  244526 B/op     210 allocs/op
```

存储测量命令如下。它覆盖单区块编码/解码、典型 32 区块批量提交和冷加载；数字只作为
Apple M5 同机基线，不作为跨机器 CI 阈值：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "^$" -bench=. -benchmem -count=1'
```

scenario v4 的完整测量命令如下，仅使用 MemoryStore、headless device 和不创建或聚焦
窗口的离屏 Metal 纹理：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3a-perf.json'
```

4,489 个含 halo 区块的快照到齐耗时 16.774024666 秒，完成网格与上传共
32.662777375 秒。静止阶段为 297.31039607075706 fps、p99 3.744 ms、峰值 RSS
1080377344 bytes；飞行阶段为 652.799139029791 fps、p99 5.206 ms、峰值 RSS
1281507328 bytes。tick p50/p95/p99/max 为 3.442/7.365/9.874/16.608 ms。
MemoryStore 保存快照/确认路径共记录 3310 个样本，p50/p95/p99/max 为
4.060834/6.063667/7.902125/20.533125 ms；不包含磁盘 I/O 或 zstd 压缩。

两阶段均满足 fps >= 100、p99 < 12 ms、峰值 RSS < 2147483648 bytes；tick 满足
p99 < 10 ms、max < 50 ms。报告写入前，最终 trusted observer 中心的服务端与
Mirror revision/hash 已成功一致。

同机比较命令使用第二份不变场景报告，并要求所有可比较指标退化不超过 20%：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3a-current.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck -baseline docs/notes/perf-baseline.json -current /tmp/mcgo-m3a-current.json -max-regression 0.20'
```

## Task 17 验收例外

2026-07-29 的提交前同机复跑受到明显主机内存压缩和 swap 压力影响。最新完整报告保留在
`/tmp/mcgo-m3a-current-20260729.json`（SHA-256
`e60c0aa35df486b21aaea4cd0b4647aa84e8427bdab14e7913dc637a1412bae0`）：绝对门禁因
tick p99 11.541ms 未通过，相对比较也出现 still、flying、tick 与 persistence 同时退化。
FPS、RSS、tick max 和 persistence max 仍通过，且平台无关测试、race、vet、架构门禁与
benchmark 可执行性全部通过。

用户明确选择豁免 Task 17 的“第二次同机场景必须通过”提交阻塞项。此例外不修改本页
accepted baseline、不放宽 20% 阈值或绝对门禁，也不把受压运行写回
`perf-baseline.json`；后续在主机资源稳定时仍应使用上述原命令重新核验。

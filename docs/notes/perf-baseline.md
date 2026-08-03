# 性能基线

## M3C scenario v6 accepted baseline

| Evidence | Value |
|---|---|
| implementation commit | `38c90a93cc1f03f0a1adb00b4bf97b0131e7d0ef` |
| toolchain | `go1.26.0 darwin/arm64` |
| hardware | `Apple M2 / 16GiB` |
| Memory report | `/tmp/mcgo-m3c-task7a-memory-38c90a93cc1f.json` — `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` |
| Memory log | `/tmp/mcgo-m3c-task7a-memory-38c90a93cc1f.log` — `dc04e13ead64cd9c936f188c934ccf6811797deb84d18607508ffec3209d1c47` |
| TCP report | `/tmp/mcgo-m3c-task7a-tcp-38c90a93cc1f.json` — `ecf245513cbd69d4422af27797f19846d64015bda746270bfe741750e50d614c` |
| TCP log | `/tmp/mcgo-m3c-task7a-tcp-38c90a93cc1f.log` — `274667a2cc1494b4b5280a9564c0732925df50574f3029833b031c0f610099b6` |
| v5 backup | `/tmp/mcgo-m3c-task7a-baseline-v5-38c90a93cc1f.json` — `428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191` |
| migration | `/tmp/mcgo-m3c-task7a-migration-38c90a93cc1f.log` — `17acc77e4e35079370e47da52274aa1cbfbb8ec1e305fd3812dae6c68d739c3d` |
| Memory→TCP | `/tmp/mcgo-m3c-task7a-compare-38c90a93cc1f.log` — `c53a229765c46ff7d5555d6679a0701465effcc0513da010282b4f02fa2942cd` |
| microbench | `/tmp/mcgo-m3c-task7a-micro-38c90a93cc1f.txt` — `bde036a38fbc0f62ccc4e5167fb498f6e41f513117e8a4178459ebc435cfe485` |
| stopped wrapper log | `/tmp/mcgo-m3c-memory-38c90a93cc1f.log` — `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` (0 bytes) |

Samples: Memory `200/1600`; TCP `200/1600`.

Policy: v6 cross-transport compares stable transport-related p50/p95/p99, FPS, RSS, load/snapshot, protocol, and persistence; raw max, queue high-water, and the independent Memory server probe are absolute-only. Same-transport additionally compares server tick/interest p50/p95/p99, outbound, and multiplayer RSS.

Recovery note: the original wrapper stopped at the nonexistent GVM label `go1.26.0` before `go run`; it produced no JSON and the preserved log above is empty. Task 7A used the installed `go1.26` alias, which reports `go1.26.0 darwin/arm64`, with new collision-safe paths. No benchmark command was retried.

### Memory multiplayer

```json
{"remote_state_encode":{"samples":62179,"p50_ms":0.001542,"p95_ms":0.004166,"p99_ms":0.006708,"max_ms":0.539625},"remote_state_decode":{"samples":62179,"p50_ms":0.000375,"p95_ms":0.001584,"p99_ms":0.002334,"max_ms":0.855667},"interest_diff":{"samples":1600,"p50_ms":0.007,"p95_ms":0.013083,"p99_ms":0.016209,"max_ms":0.027542},"roster_apply":{"samples":62179,"p50_ms":0.003041,"p95_ms":0.010167,"p99_ms":0.01825,"max_ms":0.717917},"interpolation":{"samples":62179,"p50_ms":0.000541,"p95_ms":0.001667,"p99_ms":0.002041,"max_ms":0.804666},"avatar_submit":{"samples":62180,"p50_ms":0.013042,"p95_ms":0.032,"p99_ms":0.037542,"max_ms":1.6365},"name_tag_submit":{"samples":62180,"p50_ms":0.010541,"p95_ms":0.030583,"p99_ms":0.040584,"max_ms":1.935209},"remote_gpu_complete":{"samples":256,"p50_ms":1.633625,"p95_ms":1.688916,"p99_ms":1.709083,"max_ms":3.157},"server_outbound_bytes":568532,"outbox_high_water":1,"player_jobs_high_water":6,"player_done_high_water":2,"peak_rss_bytes":1480851456}
```

### TCP multiplayer

```json
{"remote_state_encode":{"samples":61016,"p50_ms":0.001542,"p95_ms":0.004167,"p99_ms":0.006792,"max_ms":0.754042},"remote_state_decode":{"samples":61016,"p50_ms":0.000416,"p95_ms":0.001625,"p99_ms":0.002209,"max_ms":0.211458},"interest_diff":{"samples":1600,"p50_ms":0.006958,"p95_ms":0.012917,"p99_ms":0.017458,"max_ms":0.065834},"roster_apply":{"samples":61016,"p50_ms":0.003041,"p95_ms":0.009958,"p99_ms":0.017542,"max_ms":0.757708},"interpolation":{"samples":61016,"p50_ms":0.000541,"p95_ms":0.001708,"p99_ms":0.002042,"max_ms":0.67825},"avatar_submit":{"samples":61017,"p50_ms":0.013042,"p95_ms":0.031916,"p99_ms":0.037375,"max_ms":0.592292},"name_tag_submit":{"samples":61017,"p50_ms":0.010542,"p95_ms":0.030125,"p99_ms":0.04025,"max_ms":1.176959},"remote_gpu_complete":{"samples":256,"p50_ms":1.639959,"p95_ms":1.691083,"p99_ms":1.809542,"max_ms":2.036708},"server_outbound_bytes":568888,"outbox_high_water":1,"player_jobs_high_water":6,"player_done_high_water":2,"peak_rss_bytes":1554104320}
```

### Migration output

```text
场景迁移验证通过：报告完整、硬件一致且当前 v6 绝对门禁通过
```

### Memory→TCP output

```text
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

### Microbenchmarks

```text
goos: darwin
goarch: arm64
pkg: minecraft-go/internal/network
cpu: Apple M2
BenchmarkRemotePlayerStateCodec/Encode-8         	 3470312	       329.3 ns/op	    1048 B/op	       8 allocs/op
BenchmarkRemotePlayerStateCodec/Encode-8         	 3721521	       327.8 ns/op	    1048 B/op	       8 allocs/op
BenchmarkRemotePlayerStateCodec/Encode-8         	 3614332	       328.0 ns/op	    1048 B/op	       8 allocs/op
BenchmarkRemotePlayerStateCodec/Decode-8         	 4971238	       241.5 ns/op	     352 B/op	       2 allocs/op
BenchmarkRemotePlayerStateCodec/Decode-8         	 4979778	       241.4 ns/op	     352 B/op	       2 allocs/op
BenchmarkRemotePlayerStateCodec/Decode-8         	 4972978	       242.1 ns/op	     352 B/op	       2 allocs/op
PASS
ok  	minecraft-go/internal/network	7.767s
goos: darwin
goarch: arm64
pkg: minecraft-go/internal/server
cpu: Apple M2
BenchmarkEightPlayerInterest-8   	   30764	     39072 ns/op	   27555 B/op	     147 allocs/op
BenchmarkEightPlayerInterest-8   	   31246	     38174 ns/op	   27564 B/op	     147 allocs/op
BenchmarkEightPlayerInterest-8   	   36114	     35627 ns/op	   27578 B/op	     147 allocs/op
PASS
ok  	minecraft-go/internal/server	4.350s
2026/08/03 17:01:32 gfx: 后端=metal 适配器="Apple M2" 类型=integrated-gpu
goos: darwin
goarch: arm64
pkg: minecraft-go/internal/render
cpu: Apple M2
BenchmarkRemoteAvatarNameTag-8   	    7110	    172586 ns/op	     136 B/op	       9 allocs/op
BenchmarkRemoteAvatarNameTag-8   	2026/08/03 17:01:33 gfx: 后端=metal 适配器="Apple M2" 类型=integrated-gpu
    7215	    162635 ns/op	     136 B/op	       9 allocs/op
BenchmarkRemoteAvatarNameTag-8   	2026/08/03 17:01:35 gfx: 后端=metal 适配器="Apple M2" 类型=integrated-gpu
    6963	    166793 ns/op	     136 B/op	       9 allocs/op
PASS
ok  	minecraft-go/internal/render	5.647s
```

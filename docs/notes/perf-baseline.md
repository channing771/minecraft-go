# 性能基线

> 审计状态（2026-08-03）：下列 Task 7A/8 产物与哈希均原样保留，但后续代码评审发现当时的 `perfcheck` 未覆盖全部 v6 生产者绝对门禁与核心报告完整性。下列“通过”只描述历史命令在对应旧提交上的输出，不能证明修复后的校验器已经执行，也不能关闭 Task 17。修复后尚未运行任何正式性能命令；新的正式链必须在非性能门禁与独立复审通过后重新取得用户明确授权。

## 历史正式执行审计轨迹

### 原 Task 7 wrapper：启动前失败

- 授权：Task 6 checkpoint、干净 tracked state、冻结 v5 baseline 与精确 one-shot 范围汇报后，用户明确批准 Task 7。
- preflight（exit 0）：`HEAD=38c90a93cc1f03f0a1adb00b4bf97b0131e7d0ef`；tracked state 干净；v5 baseline JSON/Markdown SHA-256 分别为 `428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191` / `ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb`；无 benchmark 进程；目标 JSON/log 均不存在。
- 解析后的 preflight 断言为：

```text
test -z "$(git status --porcelain --untracked-files=no)"
test "$(git rev-parse HEAD)" = 38c90a93cc1f03f0a1adb00b4bf97b0131e7d0ef
jq -e '.scenario_version == 5' docs/notes/perf-baseline.json
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = 428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
test ! -e /tmp/mcgo-m3c-memory-38c90a93cc1f.json
test ! -e /tmp/mcgo-m3c-memory-38c90a93cc1f.log
```

- 唯一 wrapper 调用（exit 1）：

```text
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/tmp/mcgo-m3c-memory-38c90a93cc1f.json'" | tee /tmp/mcgo-m3c-memory-38c90a93cc1f.log
```

失败发生在 `gvm use go1.26.0`；`go run` 未启动，benchmark 进程启动次数为 0，JSON 不存在。空 log SHA-256 为 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`。这不是 benchmark 重试或 benchmark 结果。

### Task 7A：纠正 selector 后的单次恢复链

- 授权：上述失败及“未启动 benchmark/无 JSON”已向用户披露，用户随后明确批准使用已安装 selector `go1.26` 和全新的 `task7a` 路径执行恢复链。
- preflight（exit 0）：同一实现 commit；tracked state 干净；v5 baseline 两个哈希不变；`gvm use go1.26` 解析到报告 `go1.26.0 darwin/arm64` 的工具链；无 benchmark 进程；全部 `task7a` 目标路径不存在。
- 解析后的 preflight 断言为：

```text
test -z "$(git status --porcelain --untracked-files=no)"
test "$(git rev-parse HEAD)" = 38c90a93cc1f03f0a1adb00b4bf97b0131e7d0ef
jq -e '.scenario_version == 5' docs/notes/perf-baseline.json
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = 428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191
TERM=xterm-256color zsh -ic 'gvm use go1.26 >/dev/null && go version'
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
for path in /tmp/mcgo-m3c-task7a-{memory-38c90a93cc1f.json,memory-38c90a93cc1f.log,tcp-38c90a93cc1f.json,tcp-38c90a93cc1f.log,migration-38c90a93cc1f.log,compare-38c90a93cc1f.log,micro-38c90a93cc1f.txt,baseline-v5-38c90a93cc1f.json}; do test ! -e "$path"; done
```

- 以下五条命令各调用恰好 1 次且 exit 0；没有任何报告或比较命令重跑：

```text
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/tmp/mcgo-m3c-task7a-memory-38c90a93cc1f.json'" | tee /tmp/mcgo-m3c-task7a-memory-38c90a93cc1f.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current '/tmp/mcgo-m3c-task7a-memory-38c90a93cc1f.json' --max-regression 0.20 --allow-scenario-upgrade 5:6" | tee /tmp/mcgo-m3c-task7a-migration-38c90a93cc1f.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/tmp/mcgo-m3c-task7a-tcp-38c90a93cc1f.json'" | tee /tmp/mcgo-m3c-task7a-tcp-38c90a93cc1f.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '/tmp/mcgo-m3c-task7a-memory-38c90a93cc1f.json' --current '/tmp/mcgo-m3c-task7a-tcp-38c90a93cc1f.json' --max-regression 0.20" | tee /tmp/mcgo-m3c-task7a-compare-38c90a93cc1f.log

set -o pipefail
TERM=xterm-256color zsh -ic 'gvm use go1.26 >/dev/null && go test ./internal/network ./internal/server ./internal/render -run "^$" -bench "^(BenchmarkRemotePlayerStateCodec|BenchmarkEightPlayerInterest|BenchmarkRemoteAvatarNameTag)$" -benchmem -count=3' | tee /tmp/mcgo-m3c-task7a-micro-38c90a93cc1f.txt
```

比较输入绑定：迁移使用 v5 baseline SHA `428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191` 与 Memory SHA `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`；跨 transport 使用该 Memory SHA 与 TCP SHA `ecf245513cbd69d4422af27797f19846d64015bda746270bfe741750e50d614c`。两份报告均为精确 `200/1600`。泛化成功文本导致不同通过日志可能同为 SHA `c53a229765c46ff7d5555d6679a0701465effcc0513da010282b4f02fa2942cd`，因此日志哈希必须与上述命令及输入哈希共同解释。

### Task 8：单次 baseline→current 历史链

- 授权：v6 baseline commit `886a141db5a7fc9a46eddc1ae5da5a31e803a7e6` 与 Task 8 preflight 汇报后，用户明确批准该次 current-vs-baseline one-shot。
- preflight（exit 0）：tracked state 干净；baseline JSON 为 scenario 6 / Memory，SHA `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`；baseline Markdown SHA `a18010b9f0fe282e64639410cf769d9d701745579a5d0e71a6bc90cda2c55ee4`；无 benchmark 进程；三个 current 目标路径不存在。
- 解析后的 preflight 断言为：

```text
test -z "$(git status --porcelain --untracked-files=no)"
test "$(git rev-parse HEAD)" = 886a141db5a7fc9a46eddc1ae5da5a31e803a7e6
jq -e '.scenario_version == 6 and .transport == "memory"' docs/notes/perf-baseline.json
test "$(shasum -a 256 docs/notes/perf-baseline.json | awk '{print $1}')" = b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93
! pgrep -f '(^|/)(mcgo|mcgod)( |$)|go run ./cmd/(mcgo|mcgod)'
for path in /tmp/mcgo-m3c-current-886a141db5a7.json /tmp/mcgo-m3c-current-886a141db5a7.log /tmp/mcgo-m3c-current-compare-886a141db5a7.log; do test ! -e "$path"; done
```

- 以下两条命令各调用恰好 1 次且 exit 0；没有重跑：

```text
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/tmp/mcgo-m3c-current-886a141db5a7.json'" | tee /tmp/mcgo-m3c-current-886a141db5a7.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline.json --current '/tmp/mcgo-m3c-current-886a141db5a7.json' --max-regression 0.20" | tee /tmp/mcgo-m3c-current-compare-886a141db5a7.log
```

比较输入绑定：baseline SHA 为 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`，current SHA 为 `e4b8ad95412b4a526de72bd5758cdcead8da9005891e1dbb6af460789dea2b6c`；current 为精确 `200/1600`。比较日志 SHA 仍为泛化成功文本的 `c53a229765c46ff7d5555d6679a0701465effcc0513da010282b4f02fa2942cd`，不能脱离输入哈希单独证明比较对象。

### 后续评审失效边界

Task 8 独立代码评审随后接受了 4 项 Important：缺少 protocol encode/decode 与 player persistence 绝对门禁、v6 核心报告完整性校验不足、账本绑定不足、主计划提前勾选。主计划 completion 勾选已恢复为未完成；校验器与账本修复已通过规格/代码 follow-up 复审（No findings）。以上历史 JSON/log 仍可审计，但在修复后的 `perfcheck` 上取得新的正式证据前，不得将它们表述为最终验收通过。

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

## M3C v6 same-transport current check

| Evidence | Value |
|---|---|
| accepted baseline code commit | `38c90a93cc1f03f0a1adb00b4bf97b0131e7d0ef` |
| current report commit | `886a141db5a7fc9a46eddc1ae5da5a31e803a7e6` |
| current report | `/tmp/mcgo-m3c-current-886a141db5a7.json` — `e4b8ad95412b4a526de72bd5758cdcead8da9005891e1dbb6af460789dea2b6c` |
| current log | `/tmp/mcgo-m3c-current-886a141db5a7.log` — `9e1a120da57836ed52213f6e39b86e966e2e18e8ad5b383bfa3411207340ebcc` |
| baseline→current | `/tmp/mcgo-m3c-current-compare-886a141db5a7.log` — `c53a229765c46ff7d5555d6679a0701465effcc0513da010282b4f02fa2942cd` |

Samples: `200/1600`. Same-transport stable metrics and every absolute gate passed. No formal command was retried.

### Current multiplayer

```json
{"remote_state_encode":{"samples":61296,"p50_ms":0.001542,"p95_ms":0.00425,"p99_ms":0.007084,"max_ms":0.552167},"remote_state_decode":{"samples":61296,"p50_ms":0.000375,"p95_ms":0.001666,"p99_ms":0.00225,"max_ms":0.171792},"interest_diff":{"samples":1600,"p50_ms":0.006292,"p95_ms":0.012208,"p99_ms":0.0145,"max_ms":0.022625},"roster_apply":{"samples":61296,"p50_ms":0.003125,"p95_ms":0.010333,"p99_ms":0.01875,"max_ms":0.684667},"interpolation":{"samples":61296,"p50_ms":0.0005,"p95_ms":0.001667,"p99_ms":0.002042,"max_ms":0.280875},"avatar_submit":{"samples":61297,"p50_ms":0.012917,"p95_ms":0.032542,"p99_ms":0.038208,"max_ms":0.901542},"name_tag_submit":{"samples":61297,"p50_ms":0.010583,"p95_ms":0.031209,"p99_ms":0.042084,"max_ms":2.869334},"remote_gpu_complete":{"samples":256,"p50_ms":1.638375,"p95_ms":1.690042,"p99_ms":1.709375,"max_ms":4.716291},"server_outbound_bytes":568888,"outbox_high_water":1,"player_jobs_high_water":6,"player_done_high_water":2,"peak_rss_bytes":1360756736}
```

### Baseline→current output

```text
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

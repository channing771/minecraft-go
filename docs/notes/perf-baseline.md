# 性能基线

## 当前 M5 scenario v9 基线

2026-08-04 在冻结提交 `96deb04ed9f9c396b4df8dbeed145be872ac9af7` 上完成一次性无窗口正式链。报告身份为 `Apple M5 / 24GiB`、`macOS 26.5.1`、`go1.26.0 darwin/arm64`、`2560x1440`。采集从电池 79% 放电开始，结束时为 73%，启动前负载为 `5.76/4.87/4.14`；这组现实条件属于基线证据，不通过重跑筛选结果。

- 当前 Memory 基线：`docs/notes/perf-baseline-m5.json`，SHA-256 `70488080e09eb9fa52ce16f162a15768fd8d2bef85511c5e629a663e76140283`
- 正式 Memory 报告：`/tmp/mcgo-m5-v9-96deb04ed9f9-memory.json`，SHA-256 同上
- 正式 TCP 报告：`/tmp/mcgo-m5-v9-96deb04ed9f9-tcp.json`，SHA-256 `0ad12f022882159090115873678e4d7b7b3b7a489f40e870e8de0f4197b34b9e`
- 被替代的 scenario v8 Memory JSON：SHA-256 `f5d7420535f41d88497cd91178eef9baf138bfa7f73efb487efc06bc02322c8e`；历史说明保留在 `perf-baseline-m5.md` 和 Git 历史中

以下四条正式命令各执行一次且均为 exit 0，没有重跑，也没有启动或聚焦前台窗口：

```sh
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m5-v9-96deb04ed9f9-memory.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m5-v9-96deb04ed9f9-memory.json --max-regression 0.20 --allow-scenario-upgrade 8:9'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m5-v9-96deb04ed9f9-tcp.json'
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m5-v9-96deb04ed9f9-memory.json --current /tmp/mcgo-m5-v9-96deb04ed9f9-tcp.json --max-regression 0.20'
```

Memory 报告先通过 v8→v9 完整性、同硬件与绝对门禁；TCP 随后通过相对该 Memory 报告的同场景跨 transport 门禁。Memory/TCP 飞行阶段分别为 `621.8/638.5 FPS`、p95 `3.114/2.968ms`，`remote_gpu_complete` 都包含 2048 个样本。

## M4E scenario v9 升级规则

M4E 在固定种子世界中加入煤矿与铁矿，因此 benchmark 报告的 `scenario_version` 从 8 升为 9。帧率、tick、RSS、队列、2048 个 GPU 完成样本及 20% 退化阈值都保持不变。无后缀的 M2 scenario v6 基线保持冻结；M5 scenario v8 证据保留在 `perf-baseline-m5.md` 与 Git 历史中，`perf-baseline-m5.json` 当前保存 scenario v9，版本之间不得静默混比。

当前 `perfcheck` 只接受唯一的显式迁移参数 `--allow-scenario-upgrade 8:9`。迁移比较仍验证完整性、来源信息、同硬件与当前报告的全部绝对门禁，但会跳过跨场景的相对退化判定；反向、跳级、其他参数或默认 v8→v9 比较都被拒绝。上面的正式链在覆盖 M5 文件前执行了一次迁移验证；建立 v9 基线后，同硬件的后续报告直接执行同场景比较：

```sh
go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m5-v9-current.json --max-regression 0.20
```

本文后续的 `5:6` 历史命令只记录它们在当时提交上的审计轨迹，不代表当前工具仍接受该参数。

> 审计状态（2026-08-03）：下列 Task 7A/8 产物与哈希均原样保留，但后续代码评审发现当时的 `perfcheck` 未覆盖全部 v6 生产者绝对门禁与核心报告完整性。其“通过”只描述历史命令在对应旧提交上的输出，不能单独证明修复后的校验器或关闭 Task 17。修复 checkpoint 后已重新取得用户明确授权，并完成本文末尾的 repaired-checker formal validation；最终完成状态以该段及 closure gate 为准。

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

## M3C repaired-checker formal validation

Status: the repaired checker commit is `7951607237d0c5a8845c7e0ac08e08d558bef27f`. The user explicitly authorized this corrected one-shot chain after the repair checkpoint passed full non-performance gates and independent spec/code reviews.

The first authorized preflight attempt exited `127` after all state assertions because the loop variable `path` is a special zsh array tied to `PATH`; it prevented only the trailing evidence-print commands from resolving. No `go run` command launched, all five new evidence paths remained absent, tracked state remained clean, and no benchmark process existed. The failure was disclosed. After the user explicitly authorized the correction, the loop variable was changed only to `evidence_path` and the complete preflight exited `0`.

Corrected preflight assertions: exact HEAD `7951607237d0c5a8845c7e0ac08e08d558bef27f`; clean tracked state; `gvm use go1.26` reporting `go version go1.26.0 darwin/arm64`; accepted baseline SHA `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`; v5 backup SHA `428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191`; TCP report SHA `ecf245513cbd69d4422af27797f19846d64015bda746270bfe741750e50d614c`; no `mcgo`, `mcgod`, or matching `go run`; all five repair evidence paths absent.

| Evidence | Value |
|---|---|
| repaired checker commit | `7951607237d0c5a8845c7e0ac08e08d558bef27f` |
| migration inputs | v5 `428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191` → accepted Memory `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` |
| migration log | `/tmp/mcgo-m3c-repair-migration-7951607237d0.log` — `17acc77e4e35079370e47da52274aa1cbfbb8ec1e305fd3812dae6c68d739c3d` |
| cross-transport inputs | accepted Memory `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` → TCP `ecf245513cbd69d4422af27797f19846d64015bda746270bfe741750e50d614c` |
| cross-transport log | `/tmp/mcgo-m3c-repair-cross-7951607237d0.log` — `c53a229765c46ff7d5555d6679a0701465effcc0513da010282b4f02fa2942cd` |
| fresh current report | `/tmp/mcgo-m3c-repair-current-7951607237d0.json` — `68247fd10318ddd021e824b363f4bebc8efc1d4cf45b73fb4b79359d3cb20a70` |
| fresh current log | `/tmp/mcgo-m3c-repair-current-7951607237d0.log` — `62672ff2f10e5305662851008d36c473040823d7cfb596519df1d149cbec65f2` |
| same-transport inputs | accepted Memory `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` → fresh current `68247fd10318ddd021e824b363f4bebc8efc1d4cf45b73fb4b79359d3cb20a70` |
| same-transport log | `/tmp/mcgo-m3c-repair-current-compare-7951607237d0.log` — `c53a229765c46ff7d5555d6679a0701465effcc0513da010282b4f02fa2942cd` |
| accepted baseline after chain | unchanged SHA `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` |

Every following pipeline enabled `set -o pipefail`. Each command was invoked exactly once and exited `0`; no formal command was retried and no new TCP benchmark ran:

```text
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '/tmp/mcgo-m3c-task7a-baseline-v5-38c90a93cc1f.json' --current 'docs/notes/perf-baseline.json' --max-regression 0.20 --allow-scenario-upgrade 5:6" | tee /tmp/mcgo-m3c-repair-migration-7951607237d0.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline 'docs/notes/perf-baseline.json' --current '/tmp/mcgo-m3c-task7a-tcp-38c90a93cc1f.json' --max-regression 0.20" | tee /tmp/mcgo-m3c-repair-cross-7951607237d0.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/tmp/mcgo-m3c-repair-current-7951607237d0.json'" | tee /tmp/mcgo-m3c-repair-current-7951607237d0.log

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline 'docs/notes/perf-baseline.json' --current '/tmp/mcgo-m3c-repair-current-7951607237d0.json' --max-regression 0.20" | tee /tmp/mcgo-m3c-repair-current-compare-7951607237d0.log
```

The fresh current report is scenario 6 / Memory with matching hardware and `git_commit=7951607237d0c5a8845c7e0ac08e08d558bef27f`. Its phase keys are exactly `flying/still`; ticks are exactly `200` frames with `fps=0`; interest samples are exactly `1600`; all latency summaries are positive and monotonic; every producer, phase, tick, queue, and RSS absolute gate passed.

### Repaired-checker current summary

```json
{"load_seconds":26.606202709,"snapshot_seconds":21.620108333,"phases":{"flying":{"frames":50408,"fps":420.19958630080373,"p50_ms":1.712,"p95_ms":4.428,"p99_ms":9.466,"max_ms":24.177,"peak_rss_bytes":1413234688,"mean_candidate_sections":6.304773051896524,"mean_candidate_bytes":201.75273766068878,"mean_candidate_faces":874.0165053166164,"max_pending_uploads":0},"still":{"frames":11951,"fps":199.19847905769103,"p50_ms":5.003,"p95_ms":5.14,"p99_ms":5.459,"max_ms":8.713,"peak_rss_bytes":1132281856,"mean_candidate_sections":1667,"mean_candidate_bytes":53344,"mean_candidate_faces":206800,"max_pending_uploads":0}},"ticks":{"frames":200,"fps":0,"p50_ms":0.116,"p95_ms":0.139583,"p99_ms":0.147083,"max_ms":0.183375,"peak_rss_bytes":0,"mean_candidate_sections":0,"mean_candidate_bytes":0,"mean_candidate_faces":0,"max_pending_uploads":0},"persistence":{"snapshots":3583,"p50_ms":5.181416,"p95_ms":10.296,"p99_ms":11.276,"max_ms":25.375833},"protocol":{"encode_p99_ms":0.0005,"decode_p99_ms":0.000084,"bytes":38912},"player_persistence":{"snapshots":256,"p50_ms":0.0005,"p95_ms":0.000791,"p99_ms":0.001292,"max_ms":0.005083},"multiplayer":{"remote_state_encode":{"samples":62359,"p50_ms":0.001542,"p95_ms":0.004167,"p99_ms":0.006625,"max_ms":0.517917},"remote_state_decode":{"samples":62359,"p50_ms":0.000375,"p95_ms":0.001584,"p99_ms":0.002167,"max_ms":0.279417},"interest_diff":{"samples":1600,"p50_ms":0.006875,"p95_ms":0.013458,"p99_ms":0.016416,"max_ms":0.062542},"roster_apply":{"samples":62359,"p50_ms":0.003042,"p95_ms":0.009833,"p99_ms":0.016917,"max_ms":3.410334},"interpolation":{"samples":62359,"p50_ms":0.0005,"p95_ms":0.001708,"p99_ms":0.002,"max_ms":0.15775},"avatar_submit":{"samples":62360,"p50_ms":0.012792,"p95_ms":0.031625,"p99_ms":0.037375,"max_ms":0.625208},"name_tag_submit":{"samples":62360,"p50_ms":0.010416,"p95_ms":0.030249,"p99_ms":0.0395,"max_ms":3.426},"remote_gpu_complete":{"samples":256,"p50_ms":1.631916,"p95_ms":1.686583,"p99_ms":1.948792,"max_ms":2.074167},"server_outbound_bytes":569244,"outbox_high_water":1,"player_jobs_high_water":6,"player_done_high_water":2,"peak_rss_bytes":1443069952}}
```

### Repaired-checker outputs

```text
场景迁移验证通过：报告完整、硬件一致且当前 v6 绝对门禁通过
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

### Closure gate recovery

The first post-ledger full suite, vet, and diff-check passed, but the required race set exited `1` only at `TestTCPSendDeadlineAndSubsequentSend`: after peer `Close`, its immediate small Send returned `nil` rather than `ErrClosed`. No formal performance command was rerun.

GitNexus debugging traced `tcpServerStream.Send` to `tcpStream.send` and its socket `WriteFrame` path. The failure was an old test timing assumption: TCP does not guarantee that the first small local write after peer close has already observed FIN/RST. The test-only symbol impact was LOW with 0 callers and 0 affected flows. Production transport code was unchanged. The test now uses a one-second `Recv` context to observe peer EOF/`ErrClosed` first, then preserves the assertion that every subsequent Send returns `ErrClosed` immediately.

The synchronized test passed ordinary `count=50` and race `count=20`. The complete `go test ./... -count=1`, the required network/server/client/render/mcgo/perfcheck race set, vet, gofmt, and diff-check then all exited `0`. Independent follow-up review reported No findings and confirmed that the write-deadline contract remains intact while only the impossible first-write timing assumption was removed.

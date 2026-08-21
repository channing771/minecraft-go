# Apple M5 性能基线

## rust-engine-physics-step 物理 Step 积分迁移 record-only 备注（非新基线）

2026-08-15 在 rust-engine-physics-step 变更 Task 10 收尾验证中记录；宿主 Apple M5 / 24GiB、Go 1.26.0 darwin/arm64、Rust/cargo 1.97.1，同一主机同一 `go test ./internal/physics -run '^$' -bench . -benchmem -count=1` 命令。该组数值测量于 `1b4ad98`（本变更 1–9 完成时），最终 HEAD 为 `a92f076`。

- 迁移后（HEAD `1b4ad98`，生产 `physics.Step` 走 native `mornlea_physics_step`）：`BenchmarkStepPlayerFlat` 779.1 ns/op、`BenchmarkStepPlayerColliding` 976.7 ns/op、`BenchmarkStepPlayerStepping` 974.3 ns/op，均 0 B/op、0 allocs/op。
- 迁移前（commit `b44227e`，生产 Step 仍为 Go 积分、collision 走 native）：`BenchmarkStepPlayerFlat` 726.6 ns/op、`BenchmarkStepPlayerColliding` 941.6 ns/op、`BenchmarkStepPlayerStepping` 944.6 ns/op，均 0 allocs/op。

积分迁入 native 后仍保持 0 allocs/op（由 `TestStepPlayerDoesNotAllocate` 等锁定）；flat/colliding/stepping 分别 +52.5/+35.1/+29.7 ns/op（约 +7.2%/+3.7%/+3.1%），源于 step header 64→128 字节、输出 16→32 字节的编解码开销。`go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current docs/notes/perf-baseline-m5.json --max-regression 0.20` 自比较 exit 0，输出 `同场景性能记录完成`。以上数值只记录，不改变门禁与退出状态；M2 v15 与 M5 v14 基线字节未修改。

## Rust kernel 累计门禁 Attempt 6 scenario v16（record-only，非新基线）

2026-08-15 在 Linux GPU source-set 修复后的冻结 producer `931c57a7d4d017e37a94baf19eee833b042c68ce` 上完成 fresh 本地/macOS 累计门禁。宿主为 Apple M5 / 24GiB、macOS 26.5.1、Go 1.26.0 darwin/arm64；Rust/cargo 为 1.97.1。Rust、race、两条历史 fixture ×100、Hook、三项 fuzz、三组 benchmark、四个 native symbol、相邻 dylib、detached load、11 个 headless visual、Memory/TCP producer、自比较、跨 transport 比较和最终 full gates 均通过。

- Memory：`/private/tmp/mornlea-rust-kernel-attempt6-931c57a7d4d017e37a94baf19eee833b042c68ce-v16/memory-v16.json`，SHA-256 `4b8d007d62ad0260235c2a8551b0417334220b983127083ac3a1b96f06b92e9e`
- TCP：`/private/tmp/mornlea-rust-kernel-attempt6-931c57a7d4d017e37a94baf19eee833b042c68ce-v16/tcp-v16.json`，SHA-256 `7b200463511c027b03921d62f661ea85a4c34d34580fb41c4a4de64fd5b3944b`
- visual：`/private/tmp/mornlea-rust-kernel-attempt6-931c57a7d4d017e37a94baf19eee833b042c68ce-visual`，11 张 PNG 均为最大通道差 0、差异像素 0，且没有 `actual`/`diff` 文件。

| transport / 阶段 | frames | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 17044 | 284.1 | 3.438ms | 3.846ms | 4.312ms | 17.914ms | 1209.5MiB |
| Memory / flying | 54530 | 454.6 | 1.314ms | 6.872ms | 16.269ms | 145.369ms | 1408.4MiB |
| TCP / still | 17028 | 283.8 | 3.449ms | 3.840ms | 4.345ms | 20.630ms | 1175.3MiB |
| TCP / flying | 46326 | 386.2 | 1.706ms | 7.927ms | 16.361ms | 150.026ms | 1399.1MiB |

两份报告均为 scenario `16`、`2560x1440`、同一 producer 与硬件，并含完整 still/flying、200 tick、persistence、protocol、player persistence 与 multiplayer 数据。Memory/TCP 自比较和 Memory→TCP 比较均为 exit 0；flying p99 与跨 transport p50 波动只作性能记录。

这不是 baseline promotion：`perf-baseline.json`（M2 v15）和 `perf-baseline-m5.json`（M5 v14）字节未修改；11 张 golden、threshold、fixture、scenario、protocol 与 storage 也未修改。报告完整性、身份、真实 overflow、数据丢失与 I/O 仍为门禁。

## Rust kernel 累计门禁 Attempt 4 scenario v16（record-only，非新基线）

2026-08-15 在冻结 producer `5d510d91489391e949f17d5656b294b77693cf68` 上完成完整本地/macOS 累计门禁。宿主为 Apple M5 / 24GiB、macOS 26.5.1、Go 1.26.0 darwin/arm64；Rust/cargo 为 1.97.1。Rust、race、两条历史 fixture ×100、Hook、fuzz、benchmark、四个 native symbol、相邻 dylib、detached load、11 个 headless visual、Memory/TCP producer、自比较、跨 transport 比较和最终 full gates 均通过。

- Memory：`/private/tmp/mornlea-rust-kernel-attempt4-5d510d91489391e949f17d5656b294b77693cf68-v16/memory-v16.json`，SHA-256 `9b7ee48d9eed9085b82852f501d23249c49385232cac672e4593e6003e4ab6cb`
- TCP：`/private/tmp/mornlea-rust-kernel-attempt4-5d510d91489391e949f17d5656b294b77693cf68-v16/tcp-v16.json`，SHA-256 `16e663d8f272ca55f0b33f5a4467f9509bdc16cfc4c0c85887ed37c8bddadc6e`
- 两份报告均为 scenario `16`、`2560x1440`、同一 producer 与硬件；Memory/TCP 自比较和 Memory→TCP 比较均为 exit 0。Memory/TCP flying p99 `15.538/13.862ms` 只作性能记录。

这不是 baseline promotion：`perf-baseline.json`（M2 v15）和 `perf-baseline-m5.json`（M5 v14）字节未修改；11 张 golden、threshold、fixture 和 scenario 也未修改。报告完整性、身份、真实 overflow、数据丢失与 I/O 仍为门禁。

## Rust kernel 累计门禁 scenario v16（record-only，非新基线）

2026-08-14 在冻结 producer `37f6fbe12d0b3ce15a500c8fdaadb358cbe5f688` 上完成完整本地/macOS 累计门禁。宿主为 Apple M5 / 24GiB、macOS 26.5.1、Go 1.26.0 darwin/arm64；scenario 为 `16`。Memory/TCP producer、自比较和显式 Memory→TCP 比较均通过，性能数值只记录。

- Memory：`/private/tmp/mornlea-rust-kernel-attempt2-v16-37f6fbe12d0b3ce15a500c8fdaadb358cbe5f688/memory-v16.json`，SHA-256 `5bd268182693e4ed20d7391e5b8863935151b2be11093b417916634417277a2e`
- TCP：`/private/tmp/mornlea-rust-kernel-attempt2-v16-37f6fbe12d0b3ce15a500c8fdaadb358cbe5f688/tcp-v16.json`，SHA-256 `97db856de51bc12a9899f4a469222cc917fc8840fefed26c4563691e9b41d578`

这不是 baseline promotion：`perf-baseline.json`（M2 v15）和 `perf-baseline-m5.json`（M5 v14）未修改；完整性、身份、overflow、数据丢失与 I/O 仍为门禁。

## M5A scenario v16 record-only 证据（非新基线）

2026-08-14 在冻结提交 `2c1eb2dc3e49b2534958a7575eabba6935be99a3` 上生成两份完整无窗口报告。两者身份均为 scenario `16`、Apple M5 / 24GiB、macOS 26.5.1、Go 1.26.0 darwin/arm64、2560×1440；Memory 与 TCP 分别自比较通过，并完成一次显式跨 transport 比较。性能数值只记录，没有建立或提升任何 baseline。

- Memory：`/private/tmp/mornlea-m5a-v16.hj9xYp/memory-v16.json`
  - SHA-256：`1eb0ea86612b0c2d8a90618a0306e1802d515e934f7a5aea870b270b2a63cf37`
- TCP：`/private/tmp/mornlea-m5a-v16.hj9xYp/tcp-v16.json`
  - SHA-256：`6822cdb967bbe6ec62426fad90346913fa7b60ea8b56a396120f9b57628df1cb`

| transport / 阶段 | FPS | p99 | Peak RSS |
| --- | ---: | ---: | ---: |
| Memory / still | 287.5 | 4.502ms | 1447.8MiB |
| Memory / flying | 429.4 | 12.539ms | 1695.0MiB |
| TCP / still | 290.9 | 3.847ms | 1448.9MiB |
| TCP / flying | 426.2 | 13.010ms | 1766.0MiB |

采集时宿主为 Darwin 25.5.0 / `RELEASE_ARM64_T8142`、model `Mac17,2`、内存 25,769,803,776 bytes；uptime 为 3 天 5:53，load averages 为 3.43 / 4.33 / 3.57，Battery Power 90% 且正在放电，没有遗留 `mornlea`/`mornlea-server` 进程。宿主状态只作 provenance，没有阻止或改变 producer workload。

Memory flying p99 `12.539ms` 与 TCP flying p99 `13.010ms` 均按当前契约只记录；两份报告结构、阶段、样本、身份、真实 overflow 与数据丢失校验均通过。本节记录时唯一显式场景迁移是 `15:16`；`15:16` 此后随 producer 升到 scenario v17 一并退役，`16:17` 又随 producer 升到 scenario v18 退役，当前唯一显式迁移是 `17:18`，`14:15` 与 `14:16` 同样不得恢复或新增。`docs/notes/perf-baseline.json` 的 M2 v15 SHA-256 仍为 `9691d9752f309795e77176c6f959c357c4c97f1f7daaa4a5a6fddff8bf164d78`，`docs/notes/perf-baseline-m5.json` 的 M5 v14 SHA-256 仍为 `5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`。

## 历史 M4N scenario v15 状态

M4N 在 Apple M2 上独立建立 scenario v15 时，本文件对应的 M5 基线仍是下方 scenario v14；`docs/notes/perf-baseline-m5.json` 字节未改，SHA-256 仍为 `5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`。当时的显式 `14:15` 迁移规则只作为历史证据保留；当前规则见 `perf-baseline.md` 首节，不再授权 `14:15`、`14:16`、`15:16`、`16:17` 或跨硬件例外。

## 当前 scenario v14 基线

- 正式提交：`eb1a07a196ff948adde08e37d9af24ceb1988a14`
- scenario：`14`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON：`/private/tmp/mcgo-m4m-v14-eb1a07a196ff/memory-v14.json`，SHA-256 `5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`
- TCP JSON：`/private/tmp/mcgo-m4m-v14-eb1a07a196ff/tcp-v14.json`，SHA-256 `ed222025d8bcd0b7cdc6aa608155439695ea56a7e9703a8b10c93d7cc2f40f9e`
- 被替代的 scenario v13 Memory SHA-256：`452a1916cafa36a6383c1c6e2a7b3c125eab4623f21636b46db1bfe9b315f6f6`

这是 record-only 流程：性能数值只记录；完整 v14 Memory 报告先通过显式 `13:14` 的完整性与硬件身份校验，再立即精确复制到 `perf-baseline-m5.json`；TCP 随后独立生成。两项 producer 均为无窗口离屏运行，可独立重复生成；跨 transport 比较只在调用方显式请求时运行。

```bash
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/private/tmp/mcgo-m4m-v14-eb1a07a196ff/memory-v14.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/private/tmp/mcgo-m4m-v14-eb1a07a196ff/memory-v14.json' --max-regression 0.20 --allow-scenario-upgrade 13:14"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/private/tmp/mcgo-m4m-v14-eb1a07a196ff/tcp-v14.json'"
```

| transport / 阶段 | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 293.1 | 3.372ms | 3.570ms | 3.860ms | 27.553ms | 1393.1MiB |
| Memory / flying | 474.1 | 1.621ms | 3.864ms | 8.879ms | 22.856ms | 1643.7MiB |
| TCP / still | 293.3 | 3.371ms | 3.560ms | 3.896ms | 10.610ms | 1433.0MiB |
| TCP / flying | 467.9 | 1.632ms | 4.208ms | 9.017ms | 22.536ms | 1673.7MiB |

两份 `remote_gpu_complete` 都包含 `128` 个样本、每样本摊薄 `256` 次绘制；Memory/TCP p50 为 `0.089216/0.090346ms`。无后缀 M2 scenario v6 基线内容与路径不变，SHA-256 仍为 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`。回退 M4M 时同时恢复 scenario v13 producer/比较器与其 M5 基线；协议 v13、玩家 schema v5、区块 schema v6 和 metadata v2 无需迁移。

## 历史 scenario v13 基线

- 正式提交：`659de4859b4b78024c9b3157c2ce484bae26383e`
- scenario：`13`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON SHA-256：`452a1916cafa36a6383c1c6e2a7b3c125eab4623f21636b46db1bfe9b315f6f6`
- TCP JSON SHA-256：`f9d07c8ec0c629272c4d05ba81286366132c4b24620bdbdcdefa220309b9db17`
- 被替代的 scenario v12 Memory SHA-256：`9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f`

`perf-baseline-m5.json` 曾是上述 Memory 报告的精确字节副本，现已被 scenario v14 基线替代。以下静稳预检、绑定路径、一次性授权、失败即停和禁止重跑均仅为 v13 历史流程，不是 v14 当前要求。命令、临时路径、阶段指标和旧候选失败证据见 `perf-baseline.md`。

| transport / 阶段 | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 254.5 | 3.847ms | 4.178ms | 5.486ms | 38.871ms | 1382.2MiB |
| Memory / flying | 628.5 | 1.157ms | 2.955ms | 8.445ms | 78.054ms | 1604.2MiB |
| TCP / still | 254.6 | 3.866ms | 4.135ms | 4.688ms | 16.321ms | 1417.3MiB |
| TCP / flying | 613.0 | 1.180ms | 3.067ms | 8.498ms | 45.478ms | 1544.2MiB |

两份 `remote_gpu_complete` 都包含 `128` 个样本、每样本摊薄 `256` 次绘制；Memory/TCP p50 为 `0.092049/0.086326ms`。无后缀 M2 scenario v6 基线保持原路径，SHA-256 仍为 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`。

## 历史 scenario v10 基线

- 正式提交：`8fa7c08f327286223fb812c2f0f65f2aa2dcba03`
- scenario：`10`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`（build `25F80`）
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON SHA-256：`f681a888032bb3da6c96c854f66415d4268d26cada3bf407136b9a4adfc5a8b4`
- Memory log SHA-256：`6f44b9ae8d9dd54d9683f75020c455e554f26053c181d987b406f70567f18144`
- TCP JSON SHA-256：`cdfc2946967b00dc0cc90853c45ca005b8b9dd6d9a429c9e5d0454cbdb37e8fa`
- TCP log SHA-256：`dccce299294701d4279b6ccde43a0e9ee9478445f8d87885d6876a46c9614074`
- 被替代的 scenario v9 Memory SHA-256：`70488080e09eb9fa52ce16f162a15768fd8d2bef85511c5e629a663e76140283`

上述 Memory 报告曾是 `perf-baseline-m5.json` 的精确字节副本，现已被 scenario v13 基线替代。无后缀 M2 scenario v6 基线保持原路径，SHA-256 仍为 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`。

### 正式授权与静稳预检

流程修订提交 `a07bd1f3ade2a99b3ee8952c64e425e40c50e6a5` 加入宿主静稳门禁，完整门禁随后在 `8fa7c08f327286223fb812c2f0f65f2aa2dcba03` 重新冻结。自然冷却从 `2026-08-05T01:59:09Z` 开始；授权前两组证据为：

| UTC | load 1m | load 5m | 供电 | 电量 | AC 低电量模式 | 遗留进程 |
| --- | ---: | ---: | --- | ---: | ---: | --- |
| `2026-08-05T02:05:01Z` | 3.93 | 3.63 | AC | 97% charging | 0 | 无 |
| `2026-08-05T02:05:50Z` | 3.33 | 3.53 | AC | 97% charging | 0 | 无 |

两组间隔 49 秒，且距离冷却起点超过 5 分钟。用户在收到精确 HEAD、M2/M5 旧基线哈希和四个不存在的输出路径后明确授权。Memory 启动前于 `2026-08-05T02:08:53Z` 复核：HEAD/路径不变，工作树干净，load 1m/5m 为 `3.10/3.34`，AC 供电、电量 98%、AC 低电量模式为 0，且没有 `mcgo`/`perfcheck` 进程。没有主动结束用户进程、清理缓存、改变供电模式或启动前台窗口。

### 单次正式报告链

以下四条命令各调用恰好一次且 exit 0；Memory 通过迁移门禁后才启动 TCP，没有重跑：

```bash
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/tmp/mcgo-m5-v10-8fa7c08f3272-memory.json'" | tee /tmp/mcgo-m5-v10-8fa7c08f3272-memory.log

zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/tmp/mcgo-m5-v10-8fa7c08f3272-memory.json' --max-regression 0.20 --allow-scenario-upgrade 9:10"

set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/tmp/mcgo-m5-v10-8fa7c08f3272-tcp.json'" | tee /tmp/mcgo-m5-v10-8fa7c08f3272-tcp.log

zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline '/tmp/mcgo-m5-v10-8fa7c08f3272-memory.json' --current '/tmp/mcgo-m5-v10-8fa7c08f3272-tcp.json' --max-regression 0.20"
```

迁移输出：`场景迁移验证通过：报告完整、硬件一致且当前 v10 绝对门禁通过`。跨 transport 输出：`同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过`。

正式链完成后没有重跑 producer。提升 Memory 精确字节并通过 `cmp` 后，另各执行一次只读验证：TCP 报告与自身比较，以及 `docs/notes/perf-baseline-m5.json` 与正式 Memory 报告比较；两次均输出同场景性能比较通过。它们验证 TCP 自身和提升后基线，不创建新报告，也不改变四条正式命令各调用一次的事实。

| transport / 阶段 | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 267.8 | 3.557ms | 4.533ms | 7.011ms | 27.042ms | 1157.0MiB |
| Memory / flying | 529.1 | 1.390ms | 4.164ms | 7.766ms | 78.661ms | 1798.2MiB |
| TCP / still | 283.5 | 3.404ms | 3.864ms | 6.683ms | 20.557ms | 1291.5MiB |
| TCP / flying | 546.1 | 1.336ms | 3.928ms | 7.258ms | 112.568ms | 1863.1MiB |

两份 `remote_gpu_complete` 都包含 2048 个样本：

| transport | p50 | p95 | p99 | max |
| --- | ---: | ---: | ---: | ---: |
| Memory | 1.283875ms | 1.306250ms | 2.518542ms | 25.061500ms |
| TCP | 1.283833ms | 1.291625ms | 1.304458ms | 2.555083ms |

### 旧 M4F 正式失败证据

旧冻结 HEAD `01d28d9f5b4eeedee4200bb62f35a42ca7c1d83c` 的唯一一次 Memory 正式运行因 flying p99 `31.152ms >= 12ms` 停止，未生成正式 JSON、未运行 TCP、未覆盖基线。日志 `/tmp/mcgo-m5-v10-01d28d9f5b4e-memory.log` 的 SHA-256 为 `4d4f4fe62e3de6c053b3f5ddf292b7057b35e2d12229fb18440da003575a5201`。同 HEAD 后续非正式诊断报告/日志 SHA-256 分别为 `3fa70f241ad367b2de9be595b483a9d67790e179c9edf22e5495748486cc77bd` 和 `a65383a2a6d2e69866b14955b0279588bb2ff1d5534b6b601be440ca7786a073`，仅用于定位宿主负载污染，不得提升为基线。

## 历史 scenario v9 基线

- 正式提交：`96deb04ed9f9c396b4df8dbeed145be872ac9af7`
- scenario：`9`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON SHA-256：`70488080e09eb9fa52ce16f162a15768fd8d2bef85511c5e629a663e76140283`
- TCP JSON SHA-256：`0ad12f022882159090115873678e4d7b7b3b7a489f40e870e8de0f4197b34b9e`

Memory 与 TCP 报告各采集一次，分别通过 v8→v9 绝对门禁和同场景跨 transport 门禁。采集从电池 79% 放电开始，结束时为 73%；完整命令、报告路径和结果见 `perf-baseline.md`。该 Memory 报告曾是 `perf-baseline-m5.json` 的精确字节，现已被上方 scenario v10 基线替代。

## 历史 scenario v8 基线身份

- 正式提交：`b912c9f06a085dda9c8a3d7f14a9836152246f2c`
- 阶段屏障修复提交：`b912c9f06a085dda9c8a3d7f14a9836152246f2c`
- 被替代的 scenario v7 基线提交：`d1c383102a28082753eec7657116101c8ae6a28b`
- scenario：`8`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`（build `25F80`）
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON SHA-256：`f5d7420535f41d88497cd91178eef9baf138bfa7f73efb487efc06bc02322c8e`
- TCP JSON SHA-256：`cb34b821fa5b0788fe37c626fe61aff86ac3c0e67fa67f5f53d89b43d2ce645f`

本文件与 `perf-baseline-m5.json` 只适用于报告硬件标识相同的 M5。现有无后缀 M2 scenario v6 基线保持原路径和原内容，不能与本基线跨硬件或跨场景比较。

## 升级与失败证据

scenario v7 的 `remote_gpu_complete` 把标签准备、命令编码和资源释放计入了声称仅测量 `Submit + Poll(true)` 的区间，且 256 样本的 p99 容易被少数调度尖峰支配。M4D 验收中，相同 M5 上该 p99 从基线 `2.618166ms` 变为 `4.908833ms`，因此没有提升该失败报告，而是升级到固定 2048 样本、只覆盖提交与阻塞轮询的 scenario v8。

首轮 v8 正式链在 `4bda1bf309b4dfe3dbbc4d64c58772a5bbf6d48c` 上各执行一次。Memory/TCP SHA-256 分别为 `a2156dde788e35f26d47fd3b1ed5e0b81ac047761114e8d4b9b1598a50ffd005` 与 `e427a24d493a90d762ae15cea329aa6325093248d1e9ae3afa05ad66d361500f`；GPU p99 从 `1.338333ms` 变为 `2.549958ms`，跨 transport 门禁以 `90.5%` 退化失败。该链立即停止且未重跑，两份报告只保留为诊断证据。

根因是 GPU 探针开始前只关闭客户端 endpoint，服务端 trusted observer 仍依赖异步 writer 失败卸载。提交 `b912c9f06a085dda9c8a3d7f14a9836152246f2c` 增加服务端同步、幂等的 observer 收尾屏障，并锁定“服务端卸载 → 客户端关闭 → 首个 GPU 时钟读取”的顺序；阈值、样本数和场景版本均未放宽。

## 正式授权与预检

阶段屏障修复提交后，重新完成全仓 race、vet、archcheck、gofmt、OpenSpec strict 与 diff 检查。随后向用户报告精确 HEAD、两个全新路径以及“Memory 一次、通过后 TCP 一次、任一步失败停止且不得重跑”的边界，并取得明确授权。

预检结果：

- 隔离分支 tracked state 干净，精确 HEAD 为 `b912c9f06a085dda9c8a3d7f14a9836152246f2c`。
- benchmark 使用 headless Metal 与离屏纹理，没有启动或聚焦窗口。
- 没有遗留 `mcgo`、`mcgod` 或 benchmark 进程。
- 两个目标 `/tmp/mcgo-m5-v8-b912c9f06a08-{memory,tcp}.json` 均不存在。
- M2 JSON/Markdown SHA-256 分别为 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` 与 `6335a86c6cfc3c1271d897019a12be5ed5bb1b5fb977b94344b58bff541caa4d`。
- 现实环境为 AC 电源、电量 100%、低功耗模式关闭；执行前负载为 `4.18/3.99/3.87`。未通过人工清理、重跑或筛选改善结果。

## 单次正式报告链

新 HEAD 的 Memory 与 TCP 命令各执行恰好一次；首轮失败链没有重跑，全程没有前台窗口：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m5-v8-b912c9f06a08-memory.json'

zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m5-v8-b912c9f06a08-tcp.json'
```

| transport / 阶段 | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 265.4 | 3.501ms | 4.770ms | 7.358ms | 37.267ms | 997.2MiB |
| Memory / flying | 579.6 | 1.254ms | 3.652ms | 5.717ms | 102.423ms | 1032.4MiB |
| TCP / still | 275.6 | 3.474ms | 4.263ms | 6.075ms | 16.031ms | 1030.7MiB |
| TCP / flying | 607.8 | 1.224ms | 3.470ms | 5.517ms | 31.982ms | 1041.5MiB |

`remote_gpu_complete` 均为 2048 样本：

| transport | p50 | p95 | p99 | max |
| --- | ---: | ---: | ---: | ---: |
| Memory | 1.279959ms | 1.325000ms | 2.556750ms | 16.609292ms |
| TCP | 1.282792ms | 1.293125ms | 1.331917ms | 3.801500ms |

Memory 自比较与 Memory→TCP 比较分别执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m5-v8-b912c9f06a08-memory.json --current /tmp/mcgo-m5-v8-b912c9f06a08-memory.json --max-regression 0.20'

zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m5-v8-b912c9f06a08-memory.json --current /tmp/mcgo-m5-v8-b912c9f06a08-tcp.json --max-regression 0.20'
```

两次输出均为：

```text
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

## 后续使用

在同一 M5 硬件上生成 scenario v14 当前报告后，显式选择本基线：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/<current-report>.json --max-regression 0.20'
```

未知硬件必须建立自己的独立基线；不得自动选择、归一化或覆盖 M2/M5 文件。

# 性能基线

## 当前 producer 与迁移规则

当前 benchmark producer 为 scenario v17：`fluid-presentation-survival` 改变了被测进程本身（mesh registry 条目由 16 字节扩到 18、quad 位布局改写、光照 BFS 改为按方块查衰减表、渲染器多出一条 water pass 与一块 32 MiB 的固定水面实例缓冲、`StepInput` 头版本升到 v2），即便 benchmark 世界内容未变——它把 `FluidEnabled` 钉死为 `false`；固定 benchmark 输入仍为七名远端玩家、零伙伴。当前唯一显式跨 workload 迁移为 `16:17`，v6..v16 历史报告仍可同版本读取。历史的 `15:16` 已退役，只作本文的归档证据，工具不再接受它。

## M5A scenario v16 记录（record-only，非新基线）

M5A 段落记录的是 producer 还停在 scenario v16 时的证据。M5A 已在同一 Apple M5 / 24GiB、2560×1440 和同一提交上独立生成完整 Memory/TCP v16 报告，分别通过自比较，并通过一次显式跨 transport 比较；Linux GPU source-set 修复后的最新 Rust kernel 累计门禁已在 2026-08-15 从冻结 producer `931c57a7d4d017e37a94baf19eee833b042c68ce` 完整重跑，输出见 [Apple M5 性能记录](perf-baseline-m5.md)。这些报告只作可重复生成的 record-only 证据，不提升基线。当前已接受的 M2 Memory v15 与 M5 Memory v14 baseline JSON 路径、字节和 SHA-256 均保持不变；性能数值只记录，报告结构、身份、真实 overflow、数据丢失和 I/O 错误仍失败。

## 当前已接受的 M2 scenario v15 基线

2026-08-09 在 clean HEAD `59754fc06f96da413184169589d090c85935bd82` 上完成 M4N 无窗口 record-only 链；该提交中的 producer 已是 scenario v15。报告身份为 `Apple M2 / 16GiB`、`macOS 26.5.1`（build `25F80`）、`go1.26.0 darwin/arm64`、`2560x1440`。

- 当前 Memory 基线：`docs/notes/perf-baseline.json`，SHA-256 `9691d9752f309795e77176c6f959c357c4c97f1f7daaa4a5a6fddff8bf164d78`
- Memory 报告：`/private/tmp/mcgo-m4n-v15/memory-v15.json`，SHA-256 同上
- TCP 报告：`/private/tmp/mcgo-m4n-v15/tcp-v15.json`，SHA-256 `131de104f8cedac5f8dbd3a56cbfcbdd0a1da5268b774ce07b10c9167d2371ca`
- 被替代的 M2 scenario v6：提交 `38c90a93cc1f03f0a1adb00b4bf97b0131e7d0ef`，SHA-256 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`
- 未改动的 M5 scenario v14：`docs/notes/perf-baseline-m5.json`，SHA-256 `5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`

M2 v15 是独立基线，不使用 `6:15` 或跨硬件迁移。完整 Memory 报告先以自身作为 baseline/current 通过同场景完整性、硬件身份和数据门禁，再精确复制到 M2 基线路径；TCP 随后独立生成并自比较。两次自比较均只记录 flying p99 超过 `12ms`，返回“同场景性能记录完成”。没有自动执行 Memory↔TCP 比较。M5 基线仍停留 v14 且不提升；跨 workload 始终只允许同硬件、同 transport 的相邻一跳（本节记录的当时是 `15:16`，当前规则见首节），不得用 M5 v14 直接迁移到更高场景。

此前从含未提交 scenario v15 改动的 `86ae0cc160732e597ad1ace7497feed091d324e4` 工作树生成的两份报告只保存在 `/private/tmp/mcgo-m4n-v15-dirty-86ae0cc/` 作为不可提升的 dirty provenance 历史；它们不是当前正式基线。

```sh
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/private/tmp/mcgo-m4n-v15/memory-v15.json'"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '/private/tmp/mcgo-m4n-v15/memory-v15.json' --current '/private/tmp/mcgo-m4n-v15/memory-v15.json' --max-regression 0.20"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/private/tmp/mcgo-m4n-v15/tcp-v15.json'"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline '/private/tmp/mcgo-m4n-v15/tcp-v15.json' --current '/private/tmp/mcgo-m4n-v15/tcp-v15.json' --max-regression 0.20"
```

| transport / 阶段 | frames | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 11366 | 189.4 | 5.158ms | 5.781ms | 6.479ms | 83.249ms | 1397.1MiB |
| Memory / flying | 46794 | 390.1 | 1.698ms | 8.005ms | 18.790ms | 109.113ms | 1446.8MiB |
| TCP / still | 11383 | 189.7 | 5.145ms | 5.799ms | 6.459ms | 80.071ms | 1406.5MiB |
| TCP / flying | 47350 | 394.7 | 1.687ms | 8.466ms | 17.605ms | 132.344ms | 1464.4MiB |

Memory/TCP load 为 `40.230770167/39.616086208s`，snapshot 为 `22.475446666/22.710151750s`，cooldown 均为 `30s`。tick p99 为 `0.372917/0.445458ms`；persistence p99 为 `25.085750/23.187833ms`；`remote_gpu_complete` 均为 `128` 样本、每样本 `256` 次绘制，p99 为 `0.193331/0.188559ms`；多人探针 peak RSS 为 `1517.1/1500.2MiB`。完整字段保存在两份 JSON，Task 8 报告逐项列出。

## M4L 任务组 5：探针玩家摔落伤害风险实测（非新基线）

2026-08-07 在 `m4l-authoritative-health` 分支（提交前工作树，已包含死亡结算与背包掉落）上，
在同一台 Apple M5 机器执行任务组 5 要求的风险实测：

```text
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/m4l-fall.json'
```

这是**诊断性测量**，用来回答"权威摔落伤害与死亡结算上线后，八人探针玩家是否会在测量窗口内摔伤甚至摔死，
从而扰动既有性能门禁"。它**不是新基线**：scenario 保持 v12，`docs/notes/perf-baseline.json` 与
`docs/notes/perf-baseline-m5.json` 均未改动，也没有建立新的 scenario 版本。

### 测量方法

第一次按原样运行，报告写入 `/tmp/m4l-fall.json`（临时文件，未提交）。
由于性能报告本身不携带生命值，第二次运行在 `internal/sim/player.go` 的 `applyFallDamage` 中
临时插入一行 `println("PROBE_FALL_DAMAGE", ...)`（只在伤害为正、即真正扣血时打印），
把整个进程输出重定向到日志后统计该标记的出现次数。测量结束后这行临时打印已被移除，
`git diff internal/sim/player.go` 为空。

### 结果

| 运行 | 摔落伤害事件数 | still p99 / RSS | flying p99 / RSS | tick p99 | 多人探针 peak RSS | 进程退出码 |
|---|---|---|---|---|---|---|
| 运行 1（原样） | 未观测（无插桩） | 4.987ms / 1359.8MiB | 9.993ms / 1792.4MiB | 0.470ms | 1792.4MiB | 0 |
| 运行 2（插桩） | **0** | 6.364ms / 1275.4MiB | 10.448ms / 1669.7MiB | 1.167ms | 1669.7MiB | 0 |

两次运行都完整写出了 scenario v12 / memory 报告并以退出码 0 结束，
即 `cmd/mcgo` 内置的全部绝对门禁通过。

### 结论

- 八人探针玩家在加载、预热与整个测量窗口内**一次摔落伤害都没有发生**，
  因此探针生命值全程保持 20，既不会触发死亡结算，也不会因重生造成位置跳变或额外区块订阅。
- **无需调整** `cmd/mcgo/multiplayer_benchmark.go` 的探针输入脚本：现有脚本的跳跃与移动
  不会在固定场景中制造超过 3 格的落差。
- 没有放宽任何门禁阈值，也没有覆盖任何基线 JSON。

## M4K 任务组 2：区块固定箱子负载诊断性测量（非新基线）

2026-08-06 在 `m4k-authoritative-chests` 分支（提交前工作树，`internal/core`/`internal/world` 已加入 `core.ChestsPerChunk=16`、`core.ChestSlots=27`、`world.ChestSlot` 与对应的 `Chunk.PayloadBytes()` 增量）上，用现有 `--benchmark` 无窗口路径在同一台 Apple M5 机器上跑了三次 `go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output ...`。这是**诊断性测量**，用于回答"16 个箱子槽是否突破既有绝对门禁"，**不是新基线**：不覆盖 `docs/notes/perf-baseline-m5.json`，也不建立新的 scenario 版本。

比较对象是 `docs/notes/perf-baseline-m5.json`（scenario v12），比较口径按任务要求只看 `cmd/perfcheck` 内置的**绝对门禁**（`still`/`flying` p99 < 12ms、peak RSS < 2GiB；tick p99 < 10ms；multiplayer peak RSS < 2GiB 等），不使用 `perfcheck` 默认的 20% 相对回归比较。

### 三次运行结果

| 运行 | still p99 | still RSS | flying p99 | flying RSS | 多人探针 peak RSS | 结果 |
|---|---|---|---|---|---|---|
| 基线（M5 v12） | 5.956ms | 1311.7MiB | 9.488ms | 1672.8MiB | 1672.8MiB | — |
| 运行 1 | 3.998ms | 1371.5MiB | 8.277ms | 1936.7MiB | 2101.7MiB（**探针内部门禁 `peakRSS>=2GiB` 被触发，benchmark 中止未写出报告**） | 探针失败 |
| 运行 2（完整报告，见下） | 4.269ms | 1368.4MiB | 8.411ms | 1983.6MiB | 2012.2MiB | 全部绝对门禁通过 |
| 运行 3 | 4.894ms | 1354.0MiB | 8.349ms | 1820.5MiB | 2054.99MiB（探针内部门禁再次被触发，未写出报告） | 探针失败 |

运行 2 的完整报告已写入 `/tmp/m4k-chest-load.json`（临时文件，未提交）：`still` p50/p95/p99/RSS = `3.389/3.611/4.269ms/1434828800B`，`flying` p50/p95/p99/RSS = `1.171/3.087/8.411ms/2079948800B`，`multiplayer.peak_rss_bytes=2109898752`。

### 结论

- **本任务组明确要求的 `still`/`flying` 阶段 p99 与 RSS**，三次运行全部低于绝对门禁（p99 < 12ms，RSS < 2GiB），且与基线相比只有个位数毫秒与两位数 MiB 的正常波动。`ChestsPerChunk=16` 带来的固定负载增量是 `16*144=2304` 字节/区块（`world.ChestSlot` 的运行时内存略高于线上编码估算，但同数量级），乘以本次固定种子世界加载的 `4489` 个区块，总计约 `10MiB`，不足以解释观测到的波动。
- 三次运行中有两次在**晚于 still/flying 的多人服务端探针阶段**（`cmd/mcgo/multiplayer_benchmark.go` 内 `peakRSS >= 2<<30` 的既有硬编码检查）触发了内部有效性检查，benchmark 提前退出、未写出报告。运行期间系统负载均值在 `2.6~4.0`，同机常驻多个 iOS Simulator/CoreSimulator 进程，与 `docs/notes/perf-baseline-m5.md` 中记录的"授权前一小时机器上另有 iOS 模拟器等进程使负载达到 9.20/12.15"是同一类环境噪声。
- `go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/m4k-chest-load.json --max-regression 0.20`（相对回归比较，非本任务组要求的判据）报出 `ticks p50_ms` 与 `multiplayer peak_rss_bytes` 超过 20% 相对阈值，但**绝对门禁全部通过**（该命令的失败只来自相对回归判据，perfcheck 没有报出任何绝对门禁失败）。
- 遗留风险：`flying` 阶段相对基线的 RSS 余量从基线的约 `375MiB`（18.3%）收窄到本次运行 2 的约 `64MiB`（3.1%），多人探针阶段的绝对余量更薄（两次运行超过 2GiB）。

> **修正（修复轮次 1）**：上一版本在这里写"这不属于本任务组要求核对的 still/flying 的 RSS 与 p99 指标，因此不据此下调容量"——这是事后收窄比较范围，`multiplayer peak RSS < 2GiB` 与 `still`/`flying` 的 RSS/p99 是 `cmd/perfcheck` 里同一套绝对门禁，brief/design.md/proposal.md 从未把范围限定为仅 still/flying。正确的做法不是重新定义比较范围，而是用对照实验证明突破是否真的与本次改动无关，见下一节。

### 修复轮次 1：背靠背 A/B 对照（`6f3e2f1` 箱子代码前 vs `a42b418` 箱子代码后）

同一会话内用 `git checkout <commit> --detach` 连续切换两次并各跑一次同样的无窗口 producer，之间不做任何其他操作，用来在机器不空闲的前提下抵消大部分背景负载差异：

| 指标 | A（`6f3e2f1`，无箱子） | B（`a42b418`，有箱子） | Δ(B-A) |
|---|---|---|---|
| still RSS | 1307.9MiB | 1370.5MiB | +62.6MiB |
| still p99 | 3.886ms | 4.111ms | +0.225ms |
| flying RSS | 2050.7MiB（**突破 2048MiB 门禁**） | 2041.7MiB（未突破） | -9.0MiB |
| flying p99 | 8.102ms | 8.275ms | +0.173ms |
| 多人探针 peak RSS | 2227.27MiB（突破） | 2281.27MiB（突破） | +54.0MiB |

两次运行都在多人探针阶段触发 `peakRSS >= 2GiB` 失败，因此都没有写出 JSON 报告；以上数值取自 stdout 的阶段汇总行，不是从多次重跑里挑选的有利结果。关键证据：**A（完全不含箱子代码、`Chunk.Clone()` 也不包含任何箱子字段）在这次测量中就已经突破了比多人探针更严格的 `flying`-阶段 RSS 门禁（2050.7MiB > 2048MiB），而 B 反而没有突破同一个 `flying` 门禁**；多人探针阶段两个提交都突破，差值 `54MiB` 远小于同一提交此前三次重跑之间的波动幅度（`2012~2281MiB`，跨度 `270MiB`）。

按"两个提交都突破同一绝对门禁即为环境噪声"的判定规则：**结论是环境噪声，不是箱子改动的代价**；审查提出的"`Clone()` 热路径因箱子字段变重、在高频调用场景放大出额外开销"这一机制，作为唯一/主导原因不成立——因为完全不含该开销的 A 已经独立触发了更严格的门禁。测量期间 `uptime` 15 分钟负载均值从 `2.6` 升到 `4.35~5.05`，与本机长期存在的 iOS Simulator 背景负载一致。

**最终结论：`core.ChestsPerChunk` 保持 16**，`proposal.md`/`design.md`/`tasks.md` 无需改动。建议仍然是后续在系统空闲、负载低于 `2.61` 时补一次干净复测作为收尾验证，但不因此改变本次结论或容量取值。

## 当前已接受的 M5 scenario v14 基线

2026-08-08 在提交 `eb1a07a196ff948adde08e37d9af24ceb1988a14` 上完成无窗口 record-only 链。报告身份为 `Apple M5 / 24GiB`、`macOS 26.5.1`、`go1.26.0 darwin/arm64`、`2560x1440`。

- 当前 Memory 基线：`docs/notes/perf-baseline-m5.json`，SHA-256 `5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`
- Memory 报告：`/private/tmp/mcgo-m4m-v14-eb1a07a196ff/memory-v14.json`，SHA-256 同上
- TCP 报告：`/private/tmp/mcgo-m4m-v14-eb1a07a196ff/tcp-v14.json`，SHA-256 `ed222025d8bcd0b7cdc6aa608155439695ea56a7e9703a8b10c93d7cc2f40f9e`
- 被替代的 M5 scenario v13 基线：SHA-256 `452a1916cafa36a6383c1c6e2a7b3c125eab4623f21636b46db1bfe9b315f6f6`
- 未改动的 M2 scenario v6 基线：SHA-256 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`

Memory producer 后运行 `--allow-scenario-upgrade 13:14`，输出“场景迁移性能记录完成：报告完整、硬件一致，当前 v14”，随即把 Memory JSON 精确复制为 M5 基线；该提升发生在 TCP producer 之前。TCP 只独立记录，未运行 Memory↔TCP perfcheck。record-only 只放行性能数值：报告结构、样本、迁移、身份、真实 overflow、数据丢失和 I/O 仍会失败；无需静稳预检、绑定路径、一次性授权、失败即停或禁止重跑。

以下命令均使用无窗口离屏 benchmark：

```sh
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/private/tmp/mcgo-m4m-v14-eb1a07a196ff/memory-v14.json'"
zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/private/tmp/mcgo-m4m-v14-eb1a07a196ff/memory-v14.json' --max-regression 0.20 --allow-scenario-upgrade 13:14"
TERM=xterm-256color zsh -ic "gvm use go1.26 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/private/tmp/mcgo-m4m-v14-eb1a07a196ff/tcp-v14.json'"
```

Memory/TCP still p99 为 `3.860/3.896ms`，flying p99 为 `8.879/9.017ms`，`remote_gpu_complete` p50 为 `0.089216/0.090346ms`，各含 `128` 个样本、每样本摊薄 `256` 次绘制；峰值 RSS 为 `1643.7/1673.7MiB`。回退时恢复 scenario v13 的 producer、比较器和 M5 JSON；协议 v13、玩家 schema v5、区块 schema v6、metadata v2 和 M2 文件均不迁移。

## 历史 scenario v13 基线

2026-08-07 在冻结提交 `659de4859b4b78024c9b3157c2ce484bae26383e` 上完成一次性无窗口正式链。报告身份为 `Apple M5 / 24GiB`、`macOS 26.5.1`、`go1.26.0 darwin/arm64`、`2560x1440`。

- 当前 Memory 基线：`docs/notes/perf-baseline-m5.json`，SHA-256 `452a1916cafa36a6383c1c6e2a7b3c125eab4623f21636b46db1bfe9b315f6f6`
- 正式 Memory 报告：`/private/tmp/mcgo-m4i-659de485.o6XDzK/memory-v13.json`，SHA-256 同上
- 正式 TCP 报告：`/private/tmp/mcgo-m4i-659de485.o6XDzK/tcp-v13.json`，SHA-256 `f9d07c8ec0c629272c4d05ba81286366132c4b24620bdbdcdefa220309b9db17`
- 被替代的 M5 scenario v12 基线：SHA-256 `9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f`，提交 `a35be7f206dea52954716e6ca156b25b2622fb41`
- 未改动的 M2 scenario v6 基线：SHA-256 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`

下列静稳预检、正式授权绑定、一次性路径、失败即停和禁止重跑均为 v13 历史流程，不是 v14 当前要求。完整门禁退出后从 `2026-08-07T08:20:23-0700` 自然冷却超过 5 分钟。正式授权绑定前的两次有效快照分别在 `08:30:23` 与 `08:31:19`：load 1m/5m/15m 为 `2.14/2.59/3.47` 与 `2.52/2.62/3.42`，均为 AC 供电、电量 `80%`、低功耗模式关闭、无遗留 `mcgo`/`perfcheck`、tracked 工作树干净且 HEAD 不变。两个正式输出路径在启动前均不存在；没有终止用户进程、清理缓存、调整供电状态或筛选结果。

以下四条正式命令按顺序各执行一次且均为 exit 0，全程没有启动或聚焦前台窗口：

```sh
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/private/tmp/mcgo-m4i-659de485.o6XDzK/memory-v13.json'"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/private/tmp/mcgo-m4i-659de485.o6XDzK/memory-v13.json' --max-regression 0.20 --allow-scenario-upgrade 12:13"
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/private/tmp/mcgo-m4i-659de485.o6XDzK/tcp-v13.json'"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline '/private/tmp/mcgo-m4i-659de485.o6XDzK/memory-v13.json' --current '/private/tmp/mcgo-m4i-659de485.o6XDzK/tcp-v13.json' --max-regression 0.20"
```

Memory 先通过 v12→v13 报告完整性、同硬件与全部绝对门禁；TCP 随后通过相对该 Memory 报告的同场景跨 transport 比较。Memory/TCP 的 still p99 为 `5.486/4.688ms`，flying p99 为 `8.445/8.498ms`，`remote_gpu_complete` p50 为 `0.092049/0.086326ms`，各含 `128` 个样本、每样本摊薄 `256` 次绘制；多人探针 peak RSS 为 `1856.4/1818.1MiB`，均低于既有 `2GiB` 上限。

首个 v13 候选 `f7d8f261e910863e189666f6e2181e606996f42f` 的失败报告仍只作不可提升证据；本次没有复用旧 HEAD、授权或路径，也没有放宽门禁。

## 被替代的 M5 scenario v12 基线

2026-08-05 在冻结提交 `a35be7f206dea52954716e6ca156b25b2622fb41` 上完成一次性无窗口正式链。报告身份为 `Apple M5 / 24GiB`、`macOS 26.5.1`、`go1.26.0 darwin/arm64`、`2560x1440`。

- 当时接受的 Memory 基线字节：SHA-256 `9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f`
- 正式 Memory 报告：`/tmp/mcgo-m5-v12-a35be7f-memory.json`，SHA-256 同上
- Memory 日志：`/tmp/mcgo-m5-v12-a35be7f-memory.log`，SHA-256 `4da8b75123db58272d839e9d5cda28352c68da87ca3c47d15b9d6015e7112c69`
- 正式 TCP 报告：`/tmp/mcgo-m5-v12-a35be7f-tcp.json`，SHA-256 `0e36342a81b0877b2fa6d247beff5cd76a457675610e52eeb251b8939da384b5`
- TCP 日志：`/tmp/mcgo-m5-v12-a35be7f-tcp.log`，SHA-256 `5f27c4af93818f7165eac38122354e9c33588566f072998534d6b51eb4a56d0a`
- 被替代的 M5 scenario v10 基线：SHA-256 `f681a888032bb3da6c96c854f66415d4268d26cada3bf407136b9a4adfc5a8b4`，提交 `8fa7c08f327286223fb812c2f0f65f2aa2dcba03`
- 未改动的 M2 scenario v6 基线：SHA-256 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`

启动前的环境：AC 供电、电量 `100%`、低电量模式关闭、无遗留 `mcgo`/`perfcheck` 进程、两个输出路径均不存在、tracked 工作树干净。授权前一小时机器上另有 iOS 模拟器等进程使负载达到 `9.20/12.15`，等这些进程退出、负载回落到 `2.61` 后才启动；没有终止任何用户进程，也没有筛选或重跑结果。

以下四条正式命令各执行一次且均为 exit 0，全程没有启动或聚焦前台窗口：

```sh
set -o pipefail
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output '/tmp/mcgo-m5-v12-a35be7f-memory.json'"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current '/tmp/mcgo-m5-v12-a35be7f-memory.json' --max-regression 0.20 --allow-scenario-upgrade 10:12"
TERM=xterm-256color zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output '/tmp/mcgo-m5-v12-a35be7f-tcp.json'"
zsh -ic "gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline '/tmp/mcgo-m5-v12-a35be7f-memory.json' --current '/tmp/mcgo-m5-v12-a35be7f-tcp.json' --max-regression 0.20"
```

Memory 先通过 v10→v12 报告完整性、同硬件与全部当前绝对门禁；TCP 随后通过相对该 Memory 报告的同场景跨 transport 门禁。Memory/TCP 的 still p99 为 `5.956/4.586ms`，flying p99 为 `9.488/9.872ms`，`remote_gpu_complete` 每次绘制摊薄成本 p50 为 `0.1057/0.0918ms`，各含 `128` 个样本、每样本摊薄 `256` 次绘制。进程 RSS 峰值为 `1672.8/1644.7MiB`，相对 `2GiB` 上限留有 `375/403MiB` 余量。

## 被终止的 M4G scenario v11 正式链

M4G 曾在冻结提交 `5eea1310be620f28d8894329086f27b4a12ec546` 上执行 v11 正式链：Memory 报告通过显式 `10:11` 迁移与全部绝对门禁，但 Memory→TCP 跨 transport 比较报出 `remote_gpu_complete p95_ms` 退化 `94.4%`（`1.300` 对 `2.527`），而同一对报告的 p50（`1.279` 对 `1.279`）与 p99（`2.547` 对 `2.552`）几乎不变。

根因与 M4G 无关：该指标当时用「提交到阻塞轮询返回」的墙钟差逐次计时，实测提交空 command buffer 与提交一次 2560x1440 clear pass 的 p50 相同（`1.276ms` 与 `1.284ms`），取值被宿主轮询实现量化到约 `1.28ms` 的整数倍。按规则该正式链立即停止，两份报告只保留为诊断证据、未被提升；v11 从未成为任何硬件的基线，其 workload 变化并入本页上方的 scenario v12。用修复后的判据回放那对报告，比较通过，印证该次失败确实是门禁缺陷。

## 历史 scenario v12 批量计时与 v13 比较规则

`remote_gpu_complete` 此前用「提交命令到阻塞轮询返回」的墙钟差逐次计时。实测该量几乎不含绘制信息：提交空 command buffer 与提交一次 2560x1440 clear pass 的 p50 相同（`1.276ms` 与 `1.284ms`），且所有取值都被量化到约 `1.28ms` 的整数倍，节拍位于 wgpu-native 内部无法调整。分位数因此在相邻整数倍之间跳变，`20%` 相对阈值套在量化步长为 `100%` 的指标上无法稳定。

scenario v12 起改为批量分摊：一个样本是一批 `256` 次远端角色与昵称绘制拆进若干 command buffer、一次提交只等待一次完成的总耗时除以 `256`。节拍在样本内只出现一次并被摊薄到每次绘制成本（实测约 `0.09ms`）的约 `5.6%`。批次不取更大是因为同时存活的 command buffer 携带的原生内存会直接推高进程 RSS 峰值。实测每次摊薄成本稳定在约 `0.06ms`，p95/p50 从 `1.976` 降到 `1.079`，p99/p50 降到 `1.143`。

比较器同时引入指标分辨率规则：当单次测量的最小可分辨增量相对基线值超过判定阈值时跳过相对判定，只保留完整性与绝对上限门禁。因此 v8–v11 的逐次计时 GPU 指标不再参与相对回归判定，v12 起的批量分摊指标恢复参与。

benchmark 还在预热与 still、still 与 flying、flying 与 GPU 采样之间以及 GPU 采样之后各加入 `30` 秒冷却，降低持续满载与热节流；冷却写入报告的 `cooldown_seconds`，各阶段时长、样本数与统计口径完全不变。客户端另设 `1500MiB` 的 Go 堆软上限，避免高周转阶段把尚未回收的空闲堆累积进 RSS 峰值。

固定 `2560x1440` 离屏目标、still/flying 阶段时长、RSS、200 个 tick 样本、既有绝对门禁与 `20%` 相对退化阈值均未改变。M4I 在每帧加入程序化天空 draw 后，producer 标记为 scenario v13；`remote_gpu_complete` 仍沿用 v12 的批量分摊定义（`128` 个样本、每样本摊薄 `256` 次绘制），天空成本由真实 still/flying 帧覆盖，不污染该指标。当时 `perfcheck` 只接受唯一的显式迁移参数 `--allow-scenario-upgrade 12:13`。该参数反映真实的基线历史：scenario v11 的正式链因上述 GPU 计时缺陷失败，v11 从未成为任何硬件的基线；v12 曾是 M5 已接受基线，本次正式链已从 v12 直接提升到 v13。默认跨场景比较、反向、跨更多级和 `11:13`、`10:13`、`10:12`、`11:12`、`10:11`、`9:10` 参数均被拒绝；v6–v12 历史报告仍可读取，同版本报告仍可比较。

无后缀的 M2 baseline `docs/notes/perf-baseline.json` 内容与路径保持不变。

## 历史 scenario v13 回退说明

回退到 M4L 时需要同时回退天空 draw、scenario v13 的 producer/比较器与 M5 v13 基线，恢复 M5 scenario v12 基线；协议 v13、玩家 schema v5、区块 schema v6 与全部世界/玩家数据无需回退或迁移。

## M4I scenario v13 冻结失败候选与不可提升诊断

以下全部是**不可提升**证据：不得传给 `perfcheck`、复制为基线、覆盖 `docs/notes/perf-baseline-m5.json` 或 `docs/notes/perf-baseline.json`，也不消耗新候选的正式运行机会。

候选 `f7d8f261e910863e189666f6e2181e606996f42f` 完成既有完整门禁后，自然冷却从 `2026-08-05T23:51:51+0800` 至 `23:56:51+0800`，并在 `23:56:59`、`23:57:47` 完成两次只读静稳预检：均为 AC、100% 电量、低电量模式关闭且无遗留 `mcgo`/`perfcheck`；load 1m/5m 分别为 `2.81/2.78` 和 `2.25/2.63`。绑定两个全新 v13 路径后获得一次性正式授权。唯一的 Memory producer 以 exit `1` 停止：still 为 `196.4 FPS`、p99 `5.702ms`、RSS `1378.9MiB`；flying 为 `309.9 FPS`、p99 `12.175ms`、RSS `2280.9MiB`；GPU 采样后 RSS 峰值为 `2452.2MiB`。八会话服务端探针因 `rss=2571304960` 超过既有 `2GiB` 上限拒绝结果。正式 JSON 未生成，未运行迁移 `perfcheck` 或 TCP，且未重跑；M5/M2 baseline 哈希继续分别为 `9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f` 与 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`。

### 不可提升的诊断运行

诊断均在 HEAD `34a03b57e585b827bbd96ce5548501f9c6be899a`，仅使用 Memory transport、独立 `/tmp/mcgo-m4i-diag-34a03b5-*` 路径与 `GODEBUG=gctrace=1`；未运行 TCP 或 `perfcheck`，每次临时 mutation 后均恢复源码。短时三路命令（`warmup/still/flying/cooldown` 暂为 `2s/10s/20s/5s`）如下：

```sh
GODEBUG=gctrace=1 go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m4i-diag-34a03b5-full.json > /tmp/mcgo-m4i-diag-34a03b5-full.log 2>&1
GODEBUG=gctrace=1 go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m4i-diag-34a03b5-nosky.json > /tmp/mcgo-m4i-diag-34a03b5-nosky.log 2>&1
GODEBUG=gctrace=1 go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m4i-diag-34a03b5-nostars.json > /tmp/mcgo-m4i-diag-34a03b5-nostars.log 2>&1
```

| 变体（均不可提升） | 单一临时差异 | exit | flying p99 | GPU 采样后 RSS | 输出 |
| --- | --- | ---: | ---: | ---: | --- |
| full | 无 sky 差异 | 1 | `12.092ms` | `1588.8MiB` | `/tmp/mcgo-m4i-diag-34a03b5-full.log`；JSON 缺失 |
| nosky | 跳过一次 sky draw | 1 | `12.145ms` | `1634.0MiB` | `/tmp/mcgo-m4i-diag-34a03b5-nosky.log`；JSON 缺失 |
| nostars | 保留 draw，仅把 `sky.wgsl::fs_main` 的星光表达式改为 `let stars = 0.0;` | 0 | `11.555ms` | `1664.1MiB` | `/tmp/mcgo-m4i-diag-34a03b5-nostars.log`、`/tmp/mcgo-m4i-diag-34a03b5-nostars.json` |

为确认短时观察，保持生产 `10s/60s/120s/30s` 时长，只以 `let stars = 0.0;` 替换星光表达式，唯一 producer 为：

```sh
GODEBUG=gctrace=1 go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m4i-diag-34a03b5-confirm.json > /tmp/mcgo-m4i-diag-34a03b5-confirm.log 2>&1
```

该运行同样**不可提升**，exit `1`，`/tmp/mcgo-m4i-diag-34a03b5-confirm.json` 缺失，`/tmp/mcgo-m4i-diag-34a03b5-confirm.log` 存在。still p99 为 `5.432ms`，flying p99 为 `12.024ms`，仍未低于 `12ms`；GPU 采样后 RSS 为 `2353.3MiB`，八会话服务端探针记录 `rss=2467577856` 并拒绝结果。该运行未进行 TCP、`perfcheck` 或基线写入。

### 不可提升的结论与下一步

A/B 只支持以下边界：短时 no-sky 未改善 flying p99（相对 full `+0.053ms`），因此“完整 sky draw 本身”不是已观察到的短时改善来源；短时 no-stars 的 flying p99 降至 `11.555ms`，但完整时长 no-stars 仍为 `12.024ms`，故**未确认** `star_light` 是 flying p99 根因，也不存在生产修复。RSS 同样不可归因：短时 no-sky/no-stars 的 GPU 采样后 RSS 分别为 `1634.0/1664.1MiB`，高于 full 的 `1588.8MiB`；完整时长 no-stars 虽显示 Go 堆与 runtime 增长及大额非 Go 部分，却没有同 HEAD、同完整时长的 full-stars 对照，不能隔离 Go 堆、Go runtime 或原生图形资源的 RSS 根因。

因此否决把阈值放宽、重跑冻结正式 producer、把完整 sky draw 或 `star_light` 直接当作根因。该轮 A/B 本身没有支持生产修复；随后按 active OpenSpec 约束完成了以下唯一一次 benchmark-only heap profiling。

### 完整时长 full-stars heap profile 隔离

判定为 `GO_HEAP_ISOLATED`。诊断 HEAD 为 `b932d579d3e09fe5af7284baa72294606a9fbee1`，只含临时 heap instrumentation 提交 `b932d57`；相对 instrumentation 前的 `76adf10eafd939e6387d461705fdceb6fcef9e7a`，运行时代码差异只有标准库 helper、私有环境变量入口和 post-still/post-flying/post-GPU 三个既有边界调用。运行使用 Memory transport、scenario v13 full-stars、`2560x1440` 与生产 `10s/60s/120s/30s` 阶段，精确执行一次且没有重跑：

```sh
MCGO_BENCHMARK_HEAP_PROFILE_PREFIX=/tmp/mcgo-m4i-heapdiag-v13-20260806 GODEBUG=gctrace=1 go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m4i-heapdiag-v13-20260806.json > /tmp/mcgo-m4i-heapdiag-v13-20260806.log 2>&1
```

运行日志从 `2026-08-06T08:18:46+0800` 持续到 `08:24:51+0800`。运行前只读 sidecar 的 SHA-256 为 `093e35ad3fbdea8300379c6152e226b8c0d9aa8b3af19daca690f3b12e622daf`，运行后 sidecar 为 `83557c3d3ca9b185d7421226c638f095f7ae02deaf7956c36f60b22b07178acc`。原始日志和三个 profile 的 SHA-256 分别为：

```text
8fce2aa98b8170b90df3132fc6730308eb0de03f15fbd2c071a3375326b13497  /tmp/mcgo-m4i-heapdiag-v13-20260806.log
4b562b889eeb34e6a9a6d485fdd34487d96d71661d82627bc4bdee8b4a08eba7  /tmp/mcgo-m4i-heapdiag-v13-20260806-post-still.pprof
50499ba288324ed3f696c3f0321662a73c8a27db4fbe101540088327bb56b69e  /tmp/mcgo-m4i-heapdiag-v13-20260806-post-flying.pprof
8178c5fb54f351aa1b786906e925e37441dd426c07a3373af7c8d84af632ee26  /tmp/mcgo-m4i-heapdiag-v13-20260806-post-gpu.pprof
```

关键日志原文：

```text
固定场景加载完成，用时 27.59 秒；开始预热
still: fps=200.3 p50=4.954ms p95=5.256ms p99=5.645ms max=19.534ms RSS=1336.6MiB
still 后 内存：RSS 峰值 1336.6MiB｜Go 堆在用 360.3MiB｜Go 堆保留 515.2MiB｜Go 运行时合计 534.4MiB｜非 Go 802.2MiB
flying: fps=305.3 p50=2.714ms p95=6.752ms p99=12.584ms max=30.174ms RSS=2056.9MiB
flying 后 内存：RSS 峰值 2056.9MiB｜Go 堆在用 1048.1MiB｜Go 堆保留 1403.1MiB｜Go 运行时合计 1447.3MiB｜非 Go 609.6MiB
GPU 采样后 内存：RSS 峰值 2295.0MiB｜Go 堆在用 779.1MiB｜Go 堆保留 1563.3MiB｜Go 运行时合计 1607.5MiB｜非 Go 687.5MiB
2026/08/06 08:24:51 mcgo: 性能门禁失败: 测量八会话服务端: 多人服务端探针不完整: overflow=false outbound=610735 interest={Samples:1600 P50MS:0.027375 P95MS:0.079625 P99MS:0.112584 MaxMS:0.16675} ticks={Frames:200 FPS:0 P50MS:0.555167 P95MS:0.64675 P99MS:0.707875 MaxMS:0.901708 PeakRSSBytes:0 MeanCandidateSections:0 MeanCandidateBytes:0 MeanCandidateFaces:0 MaxPendingUploads:0 DroppedRingBufferSamples:0} queues=1/6/2 rss=2406432768
exit status 1
```

阶段边界内存增量如下。`printMemoryBreakdown` 位于 profile 的两次强制 GC 之前，因此这里的 `HeapAlloc` 与强制 GC 后的 profile total 是相邻但不同的观测点。

| 阶段增量 | RSS 峰值 | HeapAlloc | HeapSys | runtime Sys | 非 Go 估算 |
| --- | ---: | ---: | ---: | ---: | ---: |
| still → flying | `+720.3MiB` | `+687.8MiB` | `+887.9MiB` | `+912.9MiB` | `-192.6MiB` |
| flying → GPU | `+238.1MiB` | `-269.0MiB` | `+160.2MiB` | `+160.2MiB` | `+77.9MiB` |
| still → GPU | `+958.4MiB` | `+418.8MiB` | `+1048.1MiB` | `+1073.1MiB` | `-114.7MiB` |

三个 profile 的 totals 与增量为：

| profile | inuse_space total | inuse_objects total | alloc_space total |
| --- | ---: | ---: | ---: |
| post-still | `231.63MB` | `716,597` | `9,078.32MB` |
| post-flying | `754.88MB` | `2,702,133` | `59,743.28MB` |
| post-GPU | `776.40MB` | `2,529,736` | `60,840.27MB` |
| still → flying | `+523.25MB` | `+1,985,536` | `+50,664.96MB` |
| flying → GPU | `+21.52MB` | `-172,397` | `+1,096.99MB` |
| still → GPU | `+544.77MB` | `+1,813,139` | `+51,761.95MB` |

post-flying 的 live Go heap 增量已解释 RSS 增量的主要部分；profile 前 `HeapAlloc` 增量约为 RSS 增量的 `95.5%`，且非 Go 估算反而减少。唯一最大的实际保留链为：

```text
server.(*Server).saveWorker
  → storage.(*MemoryStore).SaveBatch
    → world.(*Chunk).Clone
      → world.(*Section).Clone
        → world.(*PalettedContainer).Clone
```

post-flying 时该链累计保留 `402.35MB`、`1,492,526` 个对象，占 profile live space 的 `53.30%`；post-GPU 时增加到 `622.23MB`、`2,179,047` 个对象，占 `80.14%`。调用方与所有权已经由源码闭合：benchmark 在 `cmd/mcgo/app.go` 创建 `storage.NewMemory`；flying 相机更新 trusted observer，使离开 wanted union 的 dirty chunk 进入持久化；权威 tick 的 `schedulePersistence` 经 `PersistenceSnapshots` 取得第一份快照并把 job 交给 `saveWorker`；`saveWorker` 调用 `SaveBatch`，后者为更新 revision 再深拷贝一次，并把结果放进进程生命周期的 `MemoryStore.chunks` map。该 map 是最终 owner，不随 sim 卸载确认、客户端 forget 或 renderer drop 删除；flying 持续访问新的 `ChunkKey`，所以保留集合持续增长。

以下来源被排除为本次 flying RSS 的单一根因：

- client mirror 在 post-flying/post-GPU 的 live cumulative 仅为 `75.64/77.14MB`，并由 `ForgetChunks` 删除视距外 chunk。
- mesher 的 `cloneNeighborhood` 在 post-flying 累计分配 `43,534.94MB`，但 live 仅 `13.03MB`（post-GPU `23.05MB`），属于有界 jobs/results 通道中的 churn，不是持续 owner。
- renderer upload live 从 post-still `7.09MB` 降到 post-flying `6.67MB`，且 `DropOutside` 会回收视距外 section；render cache 没有同量增长。
- full-stars shader 在 GPU 执行，不产生这条 Go heap 保留链；此前 no-sky/no-stars A/B 未隔离 RSS，而本次 Go live delta 已解释 flying RSS 的主要变化，所以无需把 sky/WebGPU/Metal 猜作该阶段根因。

这次 producer 因八会话服务端探针 `rss=2406432768` 超过既有 `2GiB` 门禁而 exit `1`；失败发生在报告写入前，所以 JSON 不存在。profile 前的强制 GC 与序列化改变后续时序，因此本次帧时、RSS、日志和 profile 全部只作不可提升的诊断证据，不能传给 `perfcheck`、复制为 baseline 或用于新候选验收。没有运行 TCP 或 `perfcheck`；M5/M2 baseline 哈希仍为 `9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f` 与 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`。Task 8.1 的诊断闭合；8.2 的修复仍保持 pending，本次不进入实现。

### encoded MemoryStore 单次完整 RSS 诊断闭合

判定为 `ENCODED_STORE_RSS_CLOSED`。在已评审的 Task 1 HEAD `5e08ad839cd9271ada6770a2cca9992c908acfc3`，先冻结只读 pre-run provenance（SHA-256 `f39ec8520d2b8405d53a98dffdd984451e5d59854aa7f0475bffda3ba388064f`），确认 scenario v13 的 full-stars `Draw(3, 1)` 和精确 `10s/60s/120s/30s` 未变、M5/M2 baseline 哈希仍为 `9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f`/`b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`，并通过 `TestMemoryStoreOwnsSavedAndLoadedChunks` 与 `TestMemoryStoreRetainedHeapIsBounded`。

唯一一次不可提升的 producer 为：

```sh
GODEBUG=gctrace=1 go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m4i-encoded-store-diag-v13-20260806.json > /tmp/mcgo-m4i-encoded-store-diag-v13-20260806.log 2>&1
```

它 exit `0`，JSON 已在报告写入后生成；日志/JSON SHA-256 分别为 `d24c03778f737321d3711c486b93458353c51442938e089cd5fbe8060b27fd09`/`09755335ca08b59f408aefcc36a2b37a71b4813c52cb177d3613308d40b53e3e`。相对 8.1 的完整时长 full-stars 诊断，still 为 RSS `1347.6MiB`（`+11.0MiB`）、HeapAlloc `360.6MiB`（`+0.3MiB`）、p99 `5.337ms`；flying 为 RSS `1459.7MiB`（`-597.2MiB`）、HeapAlloc `562.5MiB`（`-485.6MiB`）、p99 `11.585ms`；GPU 采样后为 RSS `1652.0MiB`（`-643.0MiB`）、HeapAlloc `164.3MiB`（`-614.8MiB`）、`remote_gpu_complete` p99 `0.168247ms`。所有阶段的 HeapSys/runtime Sys/非 Go 估算分别为 still `515.2/534.4/813.2MiB`、flying `755.1/777.7/682.0MiB`、GPU `851.3/873.7/778.3MiB`。八会话 server probe RSS 为 `1732214784` bytes（`1652.0MiB`，相对 `2406432768` bytes 下降 `674217984` bytes），完整且未触发 RSS 失败；运行后无遗留 `mcgo` 或匹配 `go run` 进程。

此输出只证明 encoded MemoryStore 移除了 8.1 的 retained-heap owner 并闭合 RSS 诊断；它不得传给 `perfcheck`、TCP、正式链或任何 baseline，未复制或改写 M5/M2 baseline，且不进入 8.3+。

### 星空短路单次完整 p99 诊断闭合

判定为 `STAR_SHORT_CIRCUIT_P99_CLOSED`。在干净的 detached HEAD `aa45a3032771dbf6d9f2b232ef4d1748482edc34`，pre-run provenance SHA-256 为 `001b4f9d232e916a36e830fd005f96a1d80c38d99858747928745c5f6fcc1ec0`；唯一一次不可提升的 full-stars、Memory、scenario v13 producer 使用生产 `10s/60s/120s/30s` 阶段与 `2560x1440` 目标，未启用 heap instrumentation，exit `0` 并写出完整 JSON。still 为 `275.9 FPS`、p99 `5.904ms`、RSS `1199.8MiB`；flying 为 `544.5 FPS`、p99 `9.312ms`、RSS `1274.9MiB`；GPU 采样后 RSS 为 `1562.6MiB`，`remote_gpu_complete` p99 为 `0.188827ms`，八会话 server probe RSS 为 `1638531072` bytes。日志/JSON SHA-256 分别为 `40223db1e4e0d6551f66e28ae01e34026b5d94646ec933fa5e2ad2944360ad88`/`7ebe3a160dc7395b40859c5044f4b8a8bcbe71fe078a4d043bb220145157ff67`。

该运行没有执行 `perfcheck` 或 TCP，也没有生成 heap profile、复制或改写 baseline；运行后无遗留 `mcgo`/`perfcheck` 进程。M5/M2 baseline 哈希仍为 `9eef96e0f4b9000d74ccc34214203f8256f11b36dca1361aa7b0b36da6e5313f`/`b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93`。此结果只闭合 Task 8.3，不得提升为正式报告或基线。

## M4F scenario v10 历史比较规则

M4F 扩展固定长度玩家输入与状态、废止即时破坏消息，并在权威 tick 增加有界采掘判定，因此当时的 benchmark producer 标记为 scenario v10，并通过已退役的 `9:10` 迁移建立了上方记录的 M5 v10 基线。v10 报告仍可单独读取与同场景比较。

## M4E scenario v9 历史升级规则

M4E 在固定种子世界中加入煤矿与铁矿，因此 benchmark 报告的 `scenario_version` 从 8 升为 9。帧率、tick、RSS、队列、2048 个 GPU 完成样本及 20% 退化阈值都保持不变。无后缀的 M2 scenario v6 基线保持冻结；M5 scenario v8 证据保留在 `perf-baseline-m5.md` 与 Git 历史中，`perf-baseline-m5.json` 当时保存 scenario v9，现已被上方 scenario v10 基线替代，版本之间不得静默混比。

M4E 当时的 `perfcheck` 只接受显式 `--allow-scenario-upgrade 8:9`。上面的正式链在覆盖 M5 文件前执行了一次迁移验证；建立 v9 基线后，同硬件的后续 v9 报告直接执行同场景比较：

```sh
go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/mcgo-m5-v9-current.json --max-regression 0.20
```

本节的 `8:9` 以及本文后续的 `5:6` 历史命令只记录它们在当时提交上的审计轨迹，不代表当前工具仍接受这些参数。

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

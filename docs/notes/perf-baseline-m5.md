# Apple M5 性能基线

## M4N scenario v15 状态

M4N 当前基线在 Apple M2 上独立建立为 scenario v15；本文件对应的 M5 当前基线仍是下方 scenario v14，`docs/notes/perf-baseline-m5.json` 字节未改，SHA-256 仍为 `5a34fe091cb1aacfee0172db90b5a7f66571202d230e7542660dd8e703132483`。未来只有在相同 M5 硬件生成完整 v15 报告后，才可使用唯一显式 `14:15` 迁移；不得使用 M2 报告、`6:15` 或跨硬件例外替代。

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

# Apple M5 scenario v7 性能基线

## 基线身份

- 正式提交：`d1c383102a28082753eec7657116101c8ae6a28b`
- scenario：`7`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`（build `25F80`）
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON SHA-256：`a5bac956b19946a9ddb182277adce773a04636f068229855eb89c8035139c64e`
- TCP JSON SHA-256：`dd1620c60f848d0f9ec911eb17397d965b5e06023ebba7c12525a72ce2fb36e8`

本文件与 `perf-baseline-m5.json` 只适用于报告硬件标识相同的 M5。现有无后缀 M2 scenario v6 基线保持原路径和原内容，不能与本基线跨硬件或跨场景比较。

## 正式授权与预检

更新后的 v7 规划和 checkpoint 已分别提交。正式执行前向用户报告精确 HEAD、全新路径以及“Memory/TCP 各一次、任一步失败停止且不得重跑”的边界，并取得明确授权。

预检结果：

- tracked state 干净；用户已有未跟踪目录 `midscene_run/` 未改动。
- `cmd/mcgo/benchmark.go` 生产 scenario v7，benchmark 使用 headless Metal 与离屏纹理，不创建窗口。
- 没有遗留 `mcgo`、`mcgod` 或 benchmark 进程。
- 两个目标 `/tmp/mcgo-m5-v7-d1c383102a28-{memory,tcp}.json` 均不存在。
- M2 JSON/Markdown SHA-256 分别冻结为 `b2d04877004c0cfae5884416d1ef7dbe1d6d5daed95dbda1a392604520cb7f93` 与 `6335a86c6cfc3c1271d897019a12be5ed5bb1b5fb977b94344b58bff541caa4d`。
- 现实环境为 AC 电源、97% 充电中、低功耗模式关闭；执行前负载为 `2.93/3.75/4.06`，系统报告空闲内存 43%。本机无法保证理想空闲，未以人工清理或重跑筛选结果。

## 单次正式报告链

Memory 与 TCP 命令各执行恰好一次，没有重跑，也没有前台窗口：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m5-v7-d1c383102a28-memory.json'

zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport tcp --perf-output /tmp/mcgo-m5-v7-d1c383102a28-tcp.json'
```

| transport / 阶段 | FPS | p50 | p95 | p99 | max | Peak RSS |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Memory / still | 286.0 | 3.397ms | 3.928ms | 4.978ms | 14.493ms | 996.3MiB |
| Memory / flying | 585.4 | 1.270ms | 3.711ms | 5.806ms | 51.293ms | 1032.6MiB |
| TCP / still | 279.8 | 3.461ms | 4.110ms | 5.301ms | 17.528ms | 991.7MiB |
| TCP / flying | 577.1 | 1.242ms | 3.807ms | 5.895ms | 40.113ms | 1032.7MiB |

Memory 自比较与 Memory→TCP 比较分别执行：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m5-v7-d1c383102a28-memory.json --current /tmp/mcgo-m5-v7-d1c383102a28-memory.json --max-regression 0.20'

zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline /tmp/mcgo-m5-v7-d1c383102a28-memory.json --current /tmp/mcgo-m5-v7-d1c383102a28-tcp.json --max-regression 0.20'
```

两次输出均为：

```text
同场景性能比较通过：适用的稳定指标退化均未超过阈值且绝对门禁通过
```

## 后续使用

在同一 M5 硬件上生成 scenario v7 当前报告后，显式选择本基线：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/<current-report>.json --max-regression 0.20'
```

未知硬件必须建立自己的独立基线；不得自动选择、归一化或覆盖 M2/M5 文件。

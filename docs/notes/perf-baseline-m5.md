# Apple M5 性能基线

## 当前 scenario v9 基线

- 正式提交：`96deb04ed9f9c396b4df8dbeed145be872ac9af7`
- scenario：`9`
- transport：`memory`
- framebuffer：`2560x1440`
- hardware：`Apple M5 / 24GiB`
- OS：`macOS 26.5.1`
- Go：`go1.26.0 darwin/arm64`，由 GVM 已安装工具链提供
- Memory JSON SHA-256：`70488080e09eb9fa52ce16f162a15768fd8d2bef85511c5e629a663e76140283`
- TCP JSON SHA-256：`0ad12f022882159090115873678e4d7b7b3b7a489f40e870e8de0f4197b34b9e`

Memory 与 TCP 报告各采集一次，分别通过 v8→v9 绝对门禁和同场景跨 transport 门禁。采集从电池 79% 放电开始，结束时为 73%；完整命令、报告路径和结果见 `perf-baseline.md`。`perf-baseline-m5.json` 是 Memory 报告的精确字节副本。

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

在同一 M5 硬件上生成 scenario v9 当前报告后，显式选择本基线：

```bash
zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/perfcheck --baseline docs/notes/perf-baseline-m5.json --current /tmp/<current-report>.json --max-regression 0.20'
```

未知硬件必须建立自己的独立基线；不得自动选择、归一化或覆盖 M2/M5 文件。

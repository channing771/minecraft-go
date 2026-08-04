## 1. 重建 v7 正式链边界

- [ ] 1.1 提交并严格校验本次规划更新后，记录精确 HEAD 与现有 M2 JSON/Markdown SHA-256；确认 tracked state 干净、`cmd/mcgo/benchmark.go` 生产 scenario v7、benchmark 分支只创建 headless device、`mcgo`/`mcgod`/benchmark 均未运行，且两个全新 `/tmp/mcgo-m5-v7-<HEAD12>-{memory,tcp}.json` 路径不存在。
- [ ] 1.2 记录 M5 硬件、OS、Go、电源状态和可见系统负载；不要求无法实现的人工清空。向用户报告精确 HEAD、路径和“Memory/TCP 各一次、任一步失败停止”的边界，并取得针对该正式链的一次性明确授权。

## 2. 执行单次无窗口报告链

- [ ] 2.1 使用 `zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m5-v7-<HEAD12>-memory.json'` 恰好执行一次正式 Memory 报告；随后以该报告同时作为 baseline/current 运行 `cmd/perfcheck --max-regression 0.20`，验证 scenario v7 完整性和全部绝对门禁。任一步失败立即停止且不得重跑。
- [ ] 2.2 使用相同 Go selector 和全新 TCP 路径恰好执行一次 `--benchmark-transport tcp`；随后以 Memory 为 baseline、TCP 为 current 运行同硬件、同场景 `cmd/perfcheck --max-regression 0.20`。任一步失败立即停止且不得重跑。

## 3. 提升独立基线并收尾

- [ ] 3.1 仅在 2.1–2.2 全部通过后，把 Memory 报告的精确 JSON 写入 `docs/notes/perf-baseline-m5.json`，新增中文 `docs/notes/perf-baseline-m5.md`，记录硬件、OS、Go、commit、命令、Memory/TCP SHA-256、门禁输出、现实环境限制和显式选择命令；不得修改 M2 基线。
- [ ] 3.2 验证 M5 JSON 与临时 Memory 报告逐字一致、硬件为 `Apple M5 / 24GiB`、scenario 为 7、transport 为 Memory、framebuffer 为 2560x1440，并再次用 M5 文件自比较；验证 M2 JSON/Markdown SHA-256 与 1.1 完全一致。
- [ ] 3.3 使用 GVM 依次运行 `go test ./... -race -count=1`、`go vet ./...`、`go test ./internal/archcheck -count=1`，再运行 `gofmt -l .`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`；确认没有前台窗口和遗留 benchmark 进程，只暂存本 change 与两个 M5 基线文件，提交 `chore: 建立 Apple M5 v7 性能基线`。

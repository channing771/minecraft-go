## 1. 建立 M5 正式报告链

- [x] 1.1 在提交本变更规划后记录精确 HEAD 和现有 M2 JSON/Markdown SHA-256；确认 tracked state 干净、`mcgo`/`mcgod`/benchmark 均未运行、两个全新 `/tmp/mcgo-m5-baseline-<HEAD12>-{memory,tcp}.json` 路径不存在，并核对 `cmd/mcgo/app.go` 的 benchmark 分支只创建 headless device。
- [ ] 1.2 使用 `zsh -ic 'gvm use go1.26.0 >/dev/null && go run ./cmd/mcgo --benchmark --benchmark-transport memory --perf-output /tmp/mcgo-m5-baseline-<HEAD12>-memory.json'` 恰好执行一次正式 Memory 报告；随后以该报告同时作为 baseline/current 运行 `cmd/perfcheck --max-regression 0.20`，验证 scenario v6 完整性和全部绝对门禁。任一步失败立即停止且不得重跑。
- [ ] 1.3 使用相同 Go selector 和全新 TCP 路径恰好执行一次 `--benchmark-transport tcp`；随后以 Memory 为 baseline、TCP 为 current 运行 `cmd/perfcheck --max-regression 0.20`。任一步失败立即停止且不得重跑。

## 2. 提升独立基线并收尾

- [ ] 2.1 仅在 1.2–1.3 全部通过后，把 Memory 报告的精确 JSON 写入 `docs/notes/perf-baseline-m5.json`，新增中文 `docs/notes/perf-baseline-m5.md`，记录硬件、OS、Go、commit、命令、Memory/TCP SHA-256、门禁输出和显式选择命令；不得修改 M2 基线。
- [ ] 2.2 验证 M5 JSON 与临时 Memory 报告逐字一致、硬件为 `Apple M5 / 24GiB`、scenario 为 6、transport 为 Memory、framebuffer 为 2560x1440，并再次用 M5 文件自比较；验证 M2 JSON/Markdown SHA-256 与 1.1 完全一致。
- [ ] 2.3 使用 GVM 依次运行 `go test ./... -race -count=1`、`go vet ./...`、`go test ./internal/archcheck -count=1`，再运行 `gofmt -l .`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`；确认没有前台窗口和遗留 benchmark 进程，只暂存本 change 与两个 M5 基线文件，提交 `chore: 建立 Apple M5 性能基线`。

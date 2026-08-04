## 1. 修正同 transport 比较契约

- [ ] 1.1 在编辑 `appendV6MultiplayerRegressions` 前执行 GitNexus upstream impact；若工具仍不可用，使用 `rg` 精确列出定义、全部调用者、比较模式选择和既有变异测试，向用户报告影响范围与 fallback，不修改其他 symbol。
- [ ] 1.2 在 `cmd/perfcheck/main_test.go` 先写失败测试：scenario v8 同 transport 报告的 `interest_diff` p50、p95、p99 各自退化 20.1% 均不得产生 failure；样本不足、非正、非单调以及 server tick p99 退化 20.1% 仍必须失败。用 GVM 执行对应 `go test ./cmd/perfcheck -run '<新增测试>' -count=1`，确认旧实现因 `interest_diff` 断言转红。
- [ ] 1.3 在 `cmd/perfcheck/main.go` 只从同 transport server-probe 相对 profile 移除 `interest_diff` 条目，不修改通用回归算法、跨 transport profile、20% 阈值、完整性或绝对门禁；执行 `gofmt` 后让新增测试转绿。
- [ ] 1.4 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -race -count=1 && go test ./internal/archcheck -count=1'`、`gofmt -l cmd/perfcheck` 与 `git diff --check`；运行 `detect_changes`，不可用时用精确 diff/caller fallback，确认只改变预期 profile 与测试。只暂存本组代码、测试和任务勾选，提交 `fix: 移除不稳定 interest 相对门禁`。

## 2. 复判不可变 Memory 并继续一次 TCP

- [ ] 2.1 确认 `/tmp/mcgo-m4d-v8-6d275a81688e-memory.json` 仍存在且 SHA-256 严格等于 `a878195ac2e37bf5d1fe19cc67b833fcd7371073586d768f7cdf6834b2e84d95`；用修复后的 `cmd/perfcheck` 对 `docs/notes/perf-baseline-m5.json` 重新判定，不启动 Memory benchmark。判定后再次核对哈希；任何剩余失败立即停止。
- [ ] 2.2 仅在 2.1 通过后，确认 `/tmp/mcgo-m4d-v8-6d275a81688e-tcp.json` 不存在、没有遗留 `mcgo`/`mcgod`/benchmark 进程，并在隔离 worktree 核对精确 HEAD `6d275a81688e8b53263ae17ecc7754b02c9ba601`、tracked state 干净、benchmark 使用 `newHeadlessDevice` 和离屏 framebuffer；不打开或聚焦前台窗口。
- [ ] 2.3 使用 GVM 在该隔离 worktree 恰好执行一次 TCP scenario v8 benchmark，写入 `/tmp/mcgo-m4d-v8-6d275a81688e-tcp.json`；核对 transport、git commit、M5/24GiB、2560x1440、GPU 2048 样本、完整 server probe 和报告 SHA-256。生成或完整性失败立即停止且不得重跑。
- [ ] 2.4 用修复后的 `cmd/perfcheck` 将 TCP 报告对 `docs/notes/perf-baseline-m5.json` 执行 20% 门禁；通过后把 Memory 复判命令/哈希和 TCP 命令/哈希/结果写入 `openspec/changes/m4d-authoritative-crafting/tasks.md` 并勾选 5.5。失败立即停止，不修改阈值、报告或 baseline。

## 3. 全量验证与阶段收尾

- [ ] 3.1 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`；`gofmt -l .` 必须无输出，自动验证不得启动或聚焦游戏窗口。
- [ ] 3.2 执行 `openspec validate --all --strict --no-interactive`、`git diff --check`，核对 proposal、delta spec、design、tasks、比较器实现与 M4D 证据一致；确认 scenario v8、20% 阈值、producer、报告 schema 和 M5 baseline 均未改变。
- [ ] 3.3 运行 `detect_changes`；不可用时记录精确 diff/caller fallback。只暂存本 change 产物、M4D 5.5 证据和尚未提交的任务勾选，排除 `midscene_run/` 及其他用户改动，提交 `chore: 关闭 interest 性能门禁修复`，然后自动返回 M4D 5.6。

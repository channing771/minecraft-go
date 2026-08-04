## 1. 隔离基点与 TDD 修复

- [ ] 1.1 在用户确认本规划后，从 M4D 实现前基点 `0eace21` 创建独立 worktree 和 `codex/stabilize-remote-gpu-completion-gate` 分支，带入本 change 的规划提交；确认 `git diff --name-only 0eace21..HEAD` 此时只包含本 change，当前 `main` 的 M4D 第五组改动与 `midscene_run/` 保持原样。
- [ ] 1.2 修改任何 Go symbol 前按仓库规则对 `measureGPUCompletion`、`runBenchmark` 和 `validateV6Report` 执行 upstream impact；GitNexus 不可用时记录不可用证据，并用 `rg`/调用者清单报告 blast radius。先在 `cmd/mcgo` 写 headless 失败测试，锁定 2048 次 `now → Submit → Poll(true) → now → Release` 事件顺序和 scenario v8 报告样本数。
- [ ] 1.3 在 `internal/client/perf.go` 定义唯一的 scenario v8 2048 样本常量，并在 `cmd/mcgo/benchmark.go` 与 `cmd/mcgo/multiplayer_benchmark.go` 最小实现 scenario v8 和探针内部测试时钟；标签准备、命令编码与资源释放保持计时区间外，不修改 still/flying workload、分辨率或交互路径。
- [ ] 1.4 在 `cmd/perfcheck` 先写失败测试，覆盖 v8 少于 2048 个 GPU 样本时拒绝、完整 v8 同场景比较、v7 仍接受 256 个历史样本，以及 v7/v8 不静默混比；最小实现按 scenario 选择样本下限，不修改 20% 或任何绝对阈值。
- [ ] 1.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/client -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l cmd/mcgo cmd/perfcheck internal/client`、`git diff --check` 和 `openspec validate --all --strict --no-interactive` 通过；运行 `detect_changes` 或记录同等只读 fallback，只暂存本 change、代码、测试与本组勾选，提交 `fix: 稳定 GPU 完成性能门禁`。

## 2. M5 v8 正式链预检与执行

- [ ] 2.1 在隔离分支完成 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`、OpenSpec strict 和 `git diff --check`；确认 HEAD 不含 M4D 生产提交、benchmark 仍使用 headless device/离屏纹理且没有遗留 `mcgo`/`mcgod`/benchmark 进程。
- [ ] 2.2 记录精确 HEAD、`Apple M5 / 24GiB`、OS、GVM Go 1.26.0、电源/低功耗状态、可见系统负载、M2 JSON/Markdown 哈希，并用当时 HEAD 的 12 位短哈希派生两个不存在的全新 Memory/TCP 临时 JSON 路径；向用户报告展开后的绝对路径和“Memory/TCP 各一次、任一步失败停止且不得重跑”的边界，并取得针对该 HEAD 与路径的明确确认。
- [ ] 2.3 使用 GVM 在无窗口模式恰好执行一次 Memory v8 报告，随后以该报告同时作为 baseline/current 运行 `cmd/perfcheck --max-regression 0.20`；确认 scenario=8、transport=memory、framebuffer=2560x1440、GPU samples=2048，任一步失败立即停止且不得重跑。
- [ ] 2.4 仅在 2.3 通过后恰好执行一次 TCP v8 报告，并以 Memory 为 baseline、TCP 为 current 运行相同 20% 门禁；确认硬件、场景和 framebuffer 相同，任一步失败立即停止且不得重跑。

## 3. 提升 v8 基线

- [ ] 3.1 仅在 2.3–2.4 全部通过后，把 Memory 报告的精确字节更新为 `docs/notes/perf-baseline-m5.json`，更新中文 `docs/notes/perf-baseline-m5.md`，记录 v7 被替代原因、v7 失败证据、v8 提交/命令、Memory/TCP SHA-256、门禁输出和现实环境限制；不得修改 M2 基线。
- [ ] 3.2 验证 M5 JSON 与临时 Memory 报告逐字一致并通过自比较，M2 JSON/Markdown 哈希与 2.2 一致；执行受影响测试、OpenSpec strict、`gofmt -l .` 和 `git diff --check`，运行 `detect_changes` 或记录 fallback，只暂存 M5 基线文档与本组勾选，提交 `chore: 建立 M5 scenario v8 基线`。

## 4. 线性集成与收尾

- [ ] 4.1 回到当前 `main`，按顺序带入隔离分支中规划提交之后的 v8 代码提交和基线提交；保留 M4D 第五组未提交文件及 `midscene_run/`，确认没有覆盖、暂存或混入无关改动。
- [ ] 4.2 在 `main` 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`；确认 scenario v8、2048 样本、计时边界、M5 v8 基线、M2 哈希与实现一致。
- [ ] 4.3 运行 `detect_changes` 或记录 fallback，只暂存本文件的最终勾选，提交 `chore: 关闭 scenario v8 GPU 门禁修复`；停止本 change 实现，随后返回 M4D，将其 5.5 更新为使用 M5 v8 基线继续一次性 Memory/TCP 当前报告验收。

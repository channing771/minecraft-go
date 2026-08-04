## 1. 隔离基点与 TDD 修复

- [x] 1.1 在用户确认本规划后，从 M4D 实现前基点 `0eace21` 创建独立 worktree 和 `codex/stabilize-remote-gpu-completion-gate` 分支，带入本 change 的规划提交；确认 `git diff --name-only 0eace21..HEAD` 此时只包含本 change，当前 `main` 的 M4D 第五组改动与 `midscene_run/` 保持原样。
- [x] 1.2 修改任何 Go symbol 前按仓库规则对 `measureGPUCompletion`、`runBenchmark` 和 `validateV6Report` 执行 upstream impact；GitNexus 不可用时记录不可用证据，并用 `rg`/调用者清单报告 blast radius。先在 `cmd/mcgo` 写 headless 失败测试，锁定 2048 次 `now → Submit → Poll(true) → now → Release` 事件顺序和 scenario v8 报告样本数。
- [x] 1.3 在 `internal/client/perf.go` 定义唯一的 scenario v8 2048 样本常量，并在 `cmd/mcgo/benchmark.go` 与 `cmd/mcgo/multiplayer_benchmark.go` 最小实现 scenario v8 和探针内部测试时钟；标签准备、命令编码与资源释放保持计时区间外，不修改 still/flying workload、分辨率或交互路径。
- [x] 1.4 在 `cmd/perfcheck` 先写失败测试，覆盖 v8 少于 2048 个 GPU 样本时拒绝、完整 v8 同场景比较、v7 仍接受 256 个历史样本，以及 v7/v8 不静默混比；最小实现按 scenario 选择样本下限，不修改 20% 或任何绝对阈值。
- [x] 1.5 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck ./internal/client -race -count=1 && go test ./internal/archcheck -count=1'`，确认 `gofmt -l cmd/mcgo cmd/perfcheck internal/client`、`git diff --check` 和 `openspec validate --all --strict --no-interactive` 通过；运行 `detect_changes` 或记录同等只读 fallback，只暂存本 change、代码、测试与本组勾选，提交 `fix: 稳定 GPU 完成性能门禁`。
- [ ] 1.6 对 `runBenchmark`、`closeClientSession`、`detachTrustedObserverLocked` 和新 `CloseTrustedObserver` 的相邻调用链执行 upstream impact 或等价 fallback；先在 `internal/server` 写失败测试，证明显式关闭同步移除 observer、关闭 endpoint 且重复调用安全，再在 `cmd/mcgo` 写 headless 失败测试，证明首个 GPU 时钟读取前已完成 observer 收尾。
- [ ] 1.7 在 `internal/server` 最小实现幂等 `CloseTrustedObserver`，复用 `stepMu` 与 `detachTrustedObserverLocked`；在 `cmd/mcgo/benchmark.go` 中先服务端关闭 observer、再关闭客户端 receiver、最后进入 `measureGPUCompletion`，不增加 sleep、轮询、重试、依赖或场景版本。
- [ ] 1.8 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./cmd/mcgo ./cmd/perfcheck ./internal/client -race -count=1 && go test ./internal/archcheck -count=1'`、相关关闭竞态压力测试、`gofmt -l`、OpenSpec strict 和 `git diff --check`；完成 `detect_changes` 或 fallback，只暂存屏障代码、测试、本 change 与本组勾选，提交 `fix: 隔离 GPU 探针传输收尾`。

## 2. M5 v8 正式链预检与执行

- [x] 2.1 在隔离分支完成 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`、OpenSpec strict 和 `git diff --check`；确认 HEAD 不含 M4D 生产提交、benchmark 仍使用 headless device/离屏纹理且没有遗留 `mcgo`/`mcgod`/benchmark 进程。
- [x] 2.2 记录精确 HEAD、`Apple M5 / 24GiB`、OS、GVM Go 1.26.0、电源/低功耗状态、可见系统负载、M2 JSON/Markdown 哈希，并用当时 HEAD 的 12 位短哈希派生两个不存在的全新 Memory/TCP 临时 JSON 路径；向用户报告展开后的绝对路径和“Memory/TCP 各一次、任一步失败停止且不得重跑”的边界，并取得针对该 HEAD 与路径的明确确认。
- [x] 2.3 使用 GVM 在无窗口模式恰好执行一次 Memory v8 报告，随后以该报告同时作为 baseline/current 运行 `cmd/perfcheck --max-regression 0.20`；确认 scenario=8、transport=memory、framebuffer=2560x1440、GPU samples=2048，任一步失败立即停止且不得重跑。
- [x] 2.4 在 `4bda1bf309b4dfe3dbbc4d64c58772a5bbf6d48c` 上恰好执行一次 TCP v8 报告；Memory/TCP SHA-256 分别为 `a2156dde788e35f26d47fd3b1ed5e0b81ac047761114e8d4b9b1598a50ffd005` 与 `e427a24d493a90d762ae15cea329aa6325093248d1e9ae3afa05ad66d361500f`。跨 transport 门禁因 GPU p99 `1.338333ms → 2.549958ms`（`90.5%`）失败后立即停止，未重跑、未修改基线；两份报告只保留为诊断证据。
- [ ] 2.5 屏障修复提交后重新执行全仓 race、vet、archcheck、gofmt、OpenSpec strict 和 `git diff --check`；记录新的精确 HEAD、环境、M2 哈希与两个全新不存在的 Memory/TCP 路径，确认无遗留进程，并针对新 HEAD/路径重新取得“一次 Memory、通过后一次 TCP、任一步失败停止”的明确授权。
- [ ] 2.6 使用新的已授权路径恰好执行一次 Memory v8 报告并自比较；确认 scenario=8、transport=memory、framebuffer=2560x1440、GPU samples=2048，失败立即停止且不得执行 TCP。
- [ ] 2.7 仅在 2.6 通过后恰好执行一次 TCP v8 报告并以新 Memory 报告执行 20% 跨 transport 门禁；确认硬件、场景和 framebuffer 相同，失败立即停止且不得重跑。

## 3. 提升 v8 基线

- [ ] 3.1 仅在 2.6–2.7 全部通过后，把新 Memory 报告的精确字节更新为 `docs/notes/perf-baseline-m5.json`，更新中文 `docs/notes/perf-baseline-m5.md`，记录 v7 被替代原因、首轮 v8 失败证据、屏障修复提交、新正式链命令、Memory/TCP SHA-256、门禁输出和现实环境限制；不得修改 M2 基线。
- [ ] 3.2 验证 M5 JSON 与临时 Memory 报告逐字一致并通过自比较，M2 JSON/Markdown 哈希与 2.2 一致；执行受影响测试、OpenSpec strict、`gofmt -l .` 和 `git diff --check`，运行 `detect_changes` 或记录 fallback，只暂存 M5 基线文档与本组勾选，提交 `chore: 建立 M5 scenario v8 基线`。

## 4. 线性集成与收尾

- [ ] 4.1 回到当前 `main`，按顺序带入隔离分支中规划提交之后的 v8 代码提交和基线提交；保留 M4D 第五组未提交文件及 `midscene_run/`，确认没有覆盖、暂存或混入无关改动。
- [ ] 4.2 在 `main` 执行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race -count=1 && go vet ./... && go test ./internal/archcheck -count=1 && gofmt -l .'`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`；确认 scenario v8、2048 样本、计时边界、M5 v8 基线、M2 哈希与实现一致。
- [ ] 4.3 运行 `detect_changes` 或记录 fallback，只暂存本文件的最终勾选，提交 `chore: 关闭 scenario v8 GPU 门禁修复`；停止本 change 实现，随后返回 M4D，将其 5.5 更新为使用 M5 v8 基线继续一次性 Memory/TCP 当前报告验收。

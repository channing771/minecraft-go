## 1. 前置确认

- [ ] 1.1 确认 M4G 候选 `5eea131` 保持冻结、未被改写，其 v11 失败报告只作诊断证据；确认 M5 当前基线仍是 scenario v10 且 `docs/notes/perf-baseline-m5.json` 哈希未变。
- [ ] 1.2 重新读取本 change 的 proposal、两份 delta specs、design 和 tasks，与代码核对后运行 `openspec validate stabilize-benchmark-gpu-timing --strict --no-interactive`。

## 2. gfx 时间戳查询能力

- [ ] 2.1 在 `internal/gfx` 先写失败测试：headless 设备可声明并获得 timestamp query feature；固定容量查询集合可写入、解析并读回单调递增的时间戳；设备不支持时构造返回明确错误。
- [ ] 2.2 修改 `internal/gfx/gfx.go` 与 `wgpu.go`，增加查询集合抽象、render pass 时间戳写入与结果解析，并在请求设备时按需声明 feature；保持 gfx 是唯一直接 import WebGPU 绑定的包。
- [ ] 2.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/gfx -race -count=1'`、`go test ./internal/archcheck -count=1`、`gofmt -l .` 与 `git diff --check`，通过后提交 `feat: 增加 GPU 时间戳查询能力`。

## 3. GPU 完成探针改用时间戳

- [ ] 3.1 在 `cmd/mcgo` 先写失败测试：`remote_gpu_complete` 样本取自 GPU 时间戳差、样本数仍为 `2048`、缺少 feature 时明确失败且不写报告、空绘制与完整远端绘制的中位数可区分。
- [ ] 3.2 修改 `cmd/mcgo/multiplayer_benchmark.go` 的 `measureGPUCompletion`，用查询集合记录每次绘制的 GPU 执行时间；保持既有 observer 卸载屏障、计时区间不含标签准备/编码/释放。
- [ ] 3.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./internal/gfx -race -count=1'`、archcheck、gofmt 与 diff check，确认无窗口，通过后提交 `fix: GPU 完成探针改用 GPU 时间戳`。

## 4. 门禁分辨率规则

- [ ] 4.1 在 `cmd/perfcheck` 先写失败测试：量化指标不施加相对门禁但保留绝对上限；高分辨率指标退化超过 `20%` 仍失败；失败信息指明判定类型。
- [ ] 4.2 修改 `cmd/perfcheck/main.go`，为每个受检指标显式声明判定类型；不放宽任何既有绝对上限。
- [ ] 4.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -race -count=1'`、gofmt 与 diff check，通过后提交 `fix: 相对门禁只作用于高分辨率指标`。

## 5. 阶段间冷却窗口

- [ ] 5.1 在 `cmd/mcgo` 先写失败测试：三个冷却窗口存在、冷却期间不提交渲染工作、各阶段时长与样本数不变、冷却时长写入报告。
- [ ] 5.2 修改 `cmd/mcgo/benchmark.go` 加入固定冷却，并在 `internal/client` 的报告结构中记录冷却时长。
- [ ] 5.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./internal/client -race -count=1'`、gofmt 与 diff check，通过后提交 `feat: benchmark 阶段之间加入固定冷却`。

## 6. scenario v12 与文档

- [ ] 6.1 在 `cmd/mcgo` 与 `cmd/perfcheck` 先写失败测试：producer 标记 v12、v11/v12 默认拒绝、仅 `11:12` 可显式迁移、`10:11` 退役、历史 v6-v11 可读取、v12 同场景与跨 transport 继续执行原门禁。
- [ ] 6.2 修改 producer 与比较器为 scenario v12，不改变分辨率、样本数、阶段时长、绝对阈值或 `20%` 相对阈值。
- [ ] 6.3 更新 `README.md` 与 `docs/notes/perf-baseline.md`，说明 GPU 时间戳计时、量化指标不做相对判定、冷却窗口、scenario v12 与 `11:12` 迁移。
- [ ] 6.4 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo ./cmd/perfcheck -race -count=1'`、`openspec validate --all --strict --no-interactive`、gofmt 与 diff check，通过后提交 `feat: 升级 benchmark scenario v12`。

## 7. 候选版本完整门禁

- [ ] 7.1 对 proposal、两份 delta specs、design、tasks 与实现逐项映射；确认没有通用 GPU profiling 框架、逐 pass 计时、墙钟降级路径，也没有改变 still/flying 时长、分辨率或样本数。
- [ ] 7.2 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`、`go vet ./...`、`go test ./internal/archcheck -count=1`、`gofmt -l .`、`git diff --check` 和 `openspec validate --all --strict --no-interactive`；任何失败只修根因。
- [ ] 7.3 用诊断运行确认修复生效：空绘制与完整远端绘制的中位数明显可区分，且 `remote_gpu_complete` 的分位数不再被量化到固定步长。
- [ ] 7.4 勾选已完成任务并提交冻结候选 `chore: 冻结 GPU 计时稳定化候选`；提交后不修改 producer、场景、阈值或探针，除非新建修复提交并重新完成本组门禁。

## 8. 一次性 M5 scenario v12 基线

- [ ] 8.1 在冻结候选上记录精确 HEAD、M2/M5 哈希、硬件/系统/Go、供电与负载，确认两个全新输出路径不存在且无遗留进程；向用户报告并取得 Memory/TCP 各一次、失败即停且不得重跑的明确授权。
- [ ] 8.2 仅通过现有无窗口 benchmark 生成一次 M5 Memory v12 报告；用 v10 M5 基线和显式 `11:12` 执行完整性与绝对门禁，失败立即停止。
- [ ] 8.3 Memory 通过后生成一次同 HEAD 的 M5 TCP v12 报告，并执行 Memory→TCP 同场景比较；失败立即停止。
- [ ] 8.4 两步都通过后，把 Memory 报告精确字节写入 `docs/notes/perf-baseline-m5.json`，更新性能记录的 HEAD、命令、哈希、环境和被替代场景身份；验证 M2 文件哈希未变后提交 `chore: 建立 M5 scenario v12 基线`。

## 9. 同步与归档

- [ ] 9.1 重新运行全仓 race、vet、archcheck、gofmt、diff check 与 OpenSpec strict，确认全部任务勾选且 tracked 工作树干净。
- [ ] 9.2 把两份 delta specs 同步到主规格，核对 GPU 探针语义、门禁分辨率规则、冷却窗口与 scenario v12 边界准确。
- [ ] 9.3 归档 `stabilize-benchmark-gpu-timing`，再次运行 `openspec validate --all --strict --no-interactive` 与 `git diff --check`，提交 `chore: 归档 GPU 计时稳定化`。
- [ ] 9.4 回到 M4G：确认其 v11 候选仍冻结，按新的 v12 基线继续完成 M4G 的第 12 组同步与归档。

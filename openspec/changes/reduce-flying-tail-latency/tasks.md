## 1. 性能场景 v7 兼容

- [x] 1.1 在 `cmd/perfcheck` 先补充失败测试，覆盖 v7 同版本比较、未授权 v6/v7 拒绝、显式 `6:7` 同硬件迁移与跨硬件拒绝。
- [x] 1.2 最小扩展场景完整性校验和迁移参数，不修改现有阈值、v5/v6 行为或报告 schema。
- [x] 1.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/perfcheck -race -count=1'`，提交本组。

## 2. 有界 benchmark 帧工作

- [x] 2.1 在 `cmd/mcgo` 先补充失败测试，证明消息排空上限不会扩大预热和计时帧的网格工作上限。
- [x] 2.2 拆分帧函数的两个上限：载入阶段使用 `4096/4096`，预热和计时使用 `4096/64`，并把生产报告标记为 scenario v7。
- [x] 2.3 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./cmd/mcgo -race -count=1'` 及相关无窗口 benchmark 检查，提交本组。

## 3. 无窗口性能验收与收尾

- [x] 3.1 在新的临时路径运行一次无窗口 Memory 性能诊断，确认 flying p99 低于 `12ms`；失败则保留证据并停止，不降低阈值、不自动重跑。
- [x] 3.2 运行 `zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race && go vet ./... && test -z "$(gofmt -l .)"'`、`go test ./internal/archcheck -count=1` 和 `openspec validate --all --strict --no-interactive`。
- [x] 3.3 记录诊断结果和正式 M5 基线恢复条件，提交本组；不在本变更中生成正式基线文件。

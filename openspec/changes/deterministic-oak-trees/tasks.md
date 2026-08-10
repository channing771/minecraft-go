## 1. 确定性橡树世界生成

- [x] 1.1 先在 `internal/worldgen/tree_test.go` 写固定种子、正负坐标候选格的 RED 测试，独立断言 `oreHash` 的 8×8 候选、`hash&1 == 0` 的 50% 门槛、4..6 树高、草地根、原始空气树干及固定四层树冠；同一测试还须逐点比对跨 chunk 的 `BaseBlockAt` 与 `GenerateChunk`。验证：`go test ./internal/worldgen -run 'Test.*Oak' -count=1` 必须因尚无树木规则失败。
- [x] 1.2 仅在 1.1 RED 后，于 `internal/worldgen` 用既有 `oreHash` 增加 8×8 候选与树木覆盖纯判断，并让 `BaseBlockAt` 和 `GenerateChunk` 复用它；树叶只覆盖原始空气、原木优先，且不改既有树叶碰撞语义、ID 或生成格式。验证：`go test ./internal/worldgen -run 'Test.*Oak' -race -count=1`。
- [x] 1.3 在 GREEN 后临时 mutation 候选 parity、负坐标格划分、树高/树冠层或原木优先级；每个 mutation MUST 使 1.1 的测试失败，再立即恢复。永久测试须覆盖负坐标、世界上界和原木优先；验证：`go test ./internal/worldgen -run 'Test.*Oak' -race -count=1`。
- [x] 1.4 在 `internal/server/persistence_integration_test.go` 扩展既有 `TestWorldPersistsAcrossRestartAndGeneratorUpgrade`：把已保存区块中的橡木原木与树叶作为旧内容保存，使用会生成不同内容的升级 generator 重启 server，并断言已保存 chunk 的 hash/revision 与橡树方块保持不变且升级 generator 对已探索区块调用数为零。复用或最小扩展同文件 `migrationRewriteStore` 风格的 `Store` 包装，在首次保存后隔离计数，并断言第二次正常 restart/acquire/关闭的完整生命周期对该已保存旧 chunk 的 `SaveBatch` 调用数为零；可同时核对 `NeedsRewrite == false` 与 `PersistedRevision` 未变化。此测试必须经过 storage 的磁盘保存/加载和 server acquire 路径，不得以纯 `internal/worldgen` 测试替代。验证：`go test ./internal/server -run TestWorldPersistsAcrossRestartAndGeneratorUpgrade -race -count=1`。

## 2. 无窗口橡树林视觉验收

- [x] 2.1 先在 `cmd/mcgo/capture_test.go` 为表驱动场景写 `oak-grove` RED：它必须位于所有既有场景之后，并以固定 seed 与固定生成 chunk 经 mirror、mesher、renderer、upload 夹具收敛，固定正午和相机；验证：`go test ./cmd/mcgo -run TestCaptureOakGrove -count=1` 必须因场景未注册或夹具缺失失败。
- [x] 2.2 仅在 2.1 RED 后，在 `cmd/mcgo/capture.go` 注册 `oak-grove` 并实现固定夹具与 Apply；不得新增渲染旁路、前台窗口或可调输入。验证：`go test ./cmd/mcgo -run TestCaptureOakGrove -race -count=1`。
- [x] 2.3 在 GREEN 后临时删除场景、改动其末尾顺序、seed/chunk/正午/相机之一或绕过 mirror/mesher；每个 mutation MUST 使 2.1 测试失败，再立即恢复。验证：`go test ./cmd/mcgo -run TestCaptureOakGrove -race -count=1`。
- [x] 2.4 在受支持的无窗口图形环境显式更新并逐张复核实际变化的视觉基线，只写入 `oak-grove` 以及经复核确认由新增橡树改变共享地形背景的既有 golden；既有 HUD、远端玩家、背包与目标提示语义 MUST 保持不变。验证：`go run ./cmd/mcgo --capture <输出目录> --update-golden`，随后 `go run ./cmd/mcgo --capture <输出目录>`。

## 3. 收尾验证

- [x] 3.1 格式化并运行受影响包、架构与全量并发测试；验证：`gofmt -w internal/worldgen/*.go internal/worldgen/*_test.go cmd/mcgo/capture.go cmd/mcgo/capture_test.go`、`go test ./internal/worldgen -race -count=1`、`go test ./cmd/mcgo -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`gofmt -l .`。
- [x] 3.2 核对实现、golden 与 delta specs 一致，并运行严格 OpenSpec 校验；验证：`openspec validate deterministic-oak-trees --strict --no-interactive`、`openspec validate --all --strict --no-interactive`、`git diff --check`。

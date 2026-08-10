## 1. 确定性橡树世界生成

- [ ] 1.1 在 `internal/worldgen` 为 8×8 候选格、固定 salt、候选位置和树高增加最小纯计算，并以 `oreHash` 保持正负坐标的确定性；验证：`go test ./internal/worldgen -race -count=1`。
- [ ] 1.2 让 `Generator.BaseBlockAt` 在既有自然材料和矿石判断基础上合并草地根、原始空气树干、固定四层树冠和原木优先级，并让 `GenerateChunk` 复用同一判断；验证：`go test ./internal/worldgen -race -count=1`。
- [ ] 1.3 在 `internal/worldgen` 测试中覆盖候选与 50% 门槛、草地限制、树干原始空气、世界上界、树冠形状、树叶无碰撞、原木优先、负坐标、跨 chunk、单点/整块一致与旧区块不迁移；验证：`go test ./internal/worldgen -race -count=1`。

## 2. 无窗口橡树林视觉验收

- [ ] 2.1 在 `cmd/mcgo/capture.go` 与 `cmd/mcgo/capture_test.go` 的现有表驱动场景机制中，以固定种子生成固定区块的方式在全部现有场景末尾加入 `oak-grove`，并固定正午、相机、mirror、mesher、renderer 与 upload 收敛路径；验证：`go test ./cmd/mcgo -race -count=1`。
- [ ] 2.2 在受支持的无窗口图形环境显式更新并逐张复核实际变化的视觉基线，只加入 `oak-grove` 所需 golden；验证：`go run ./cmd/mcgo --capture <输出目录> --update-golden`，随后 `go run ./cmd/mcgo --capture <输出目录>`。

## 3. 收尾验证

- [ ] 3.1 格式化并运行受影响包、架构与全量并发测试；验证：`gofmt -w internal/worldgen/*.go internal/worldgen/*_test.go cmd/mcgo/capture.go cmd/mcgo/capture_test.go`、`go test ./internal/worldgen -race -count=1`、`go test ./cmd/mcgo -race -count=1`、`go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`gofmt -l .`。
- [ ] 3.2 核对实现、golden 与 delta specs 一致，并运行严格 OpenSpec 校验；验证：`openspec validate deterministic-oak-trees --strict --no-interactive`、`openspec validate --all --strict --no-interactive`、`git diff --check`。

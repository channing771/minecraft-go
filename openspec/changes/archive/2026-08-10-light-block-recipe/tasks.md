## 1. 固定发光方块配方

- [x] 1.1 在 `internal/core/recipe_test.go` 先写失败测试，冻结 recipe ID `1..7` 不变、ID `8` 为 `4 Glass -> 4 LightBlock`、ID `0` 与 `>8` 拒绝，并覆盖原料不足、产物无空间时完整物品状态原子不变；运行 `go test ./internal/core -run 'Recipe|Craft|Light' -count=1` 确认 RED。
- [x] 1.2 仅在 `internal/core/recipe.go` 的既有固定配方 switch 末尾追加 ID `8` case，不修改 `Inventory.Craft` 或网络消息；运行 `gofmt -w internal/core/recipe.go internal/core/recipe_test.go` 与 `go test ./internal/core -race -count=1` 确认 GREEN，并以临时移除该 case 的 mutation 证明新增测试失败后恢复实现。

## 2. 权威传输语义回归

- [x] 2.1 在 `internal/server` 的既有 Memory/TCP 合成测试装配中加入 recipe ID `8` 的成功与原子拒绝场景，断言相同初始状态和命令序列得到相同最终完整物品状态，且客户端仍只发送既有 recipe ID 字段；不得新增 transport 抽象或修改协议生产代码。运行 `go test ./internal/server ./internal/network -run 'Craft|Recipe|Protocol' -race -count=1`。
- [x] 2.2 核对本 change 没有修改协议 v15、玩家 schema v6、区块 schema v8、世界 metadata v2 或任何 wire 字段，并运行 `go test ./internal/network ./internal/storage -run 'Protocol|Schema|Future|Golden' -race -count=1`。

## 3. 八行固定合成 HUD

- [x] 3.1 在 `internal/render/hotbar_test.go` 与 `cmd/mcgo/app_test.go` 先写失败测试，覆盖固定数组末项为 recipe ID `8`、`recipeQuads=73`、`recipeGlyphs=20`、`openHUDHeight=670`、640×360 第八行完整可见、按钮绘制与命中几何共源、最坏固定容量无真实 overflow 且预热后零分配；运行 `go test ./internal/render ./cmd/mcgo -run 'Recipe|Inventory|HUD|Craft' -count=1` 确认 RED。
- [x] 3.2 在 `internal/render/hotbar.go` 仅追加第八条固定 recipe ID 并机械更新三个固定容量值，不增加分页、滚动、自适应目录或新 UI 抽象；运行 `gofmt -w internal/render/hotbar.go internal/render/hotbar_test.go cmd/mcgo/app_test.go` 与 `go test ./internal/render ./cmd/mcgo -race -count=1` 确认 GREEN，并以临时恢复任一旧容量值的 mutation 证明容量测试失败后恢复实现。

## 4. 无窗口视觉证据

- [x] 4.1 在 `cmd/mcgo/capture.go` 的既有 `inventory-crafting` fixture 中加入足量玻璃，使第八行可合成；运行 `gofmt -w cmd/mcgo/capture.go`，在 fresh `VISUAL_OUT` 用 `make visual-check` 仅生成 candidate 且不得覆盖 golden，预期因 `inventory-crafting.png` 唯一差异 exit 2；逐张 `cmp` 十个场景只允许该图变化，并只读检查 candidate 中八行完整、末行为玻璃到发光方块且无重叠或裁切，等待用户明确确认，不得启动或聚焦前台窗口。
- [x] 4.2 收到确认后只复制该 candidate 的 `inventory-crafting.png` 到 golden，机械断言 golden diff 只有该文件；在新的 `VISUAL_OUT` 运行一次 `make visual-check`，要求十个场景通过且 exit 0，并运行 `git diff --check`。

## 5. 收尾验证与归档

- [x] 5.1 对改动的 Go 文件运行 `gofmt -w`，再运行 `go test ./internal/archcheck -count=1`、`go test ./... -race`、`go vet ./...`、`gofmt -l .` 与 `git diff --check`；`gofmt -l .` 必须无输出，性能数值只记录且不得放宽真实 overflow 或数据丢失门禁。
- [x] 5.2 运行 `openspec validate light-block-recipe --strict --no-interactive` 与 `openspec validate --all --strict --no-interactive`，确认 proposal、三份 delta specs、design、tasks 与实现一致；完成独立评审后同步主规格并归档 change。

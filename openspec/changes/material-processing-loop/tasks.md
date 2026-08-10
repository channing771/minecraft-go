## 2. 固定合成配方

- [x] 2.1 在 `internal/core/recipe.go` 追加稳定 recipe ID `7` 的 `1 OakLog -> 4 OakPlanks`，保持 ID `1..6` 不变；在 `internal/core/recipe_test.go` 覆盖查询、原料不足、产物无空间与原子不变；运行 `go test ./internal/core -race -count=1`。

## 3. 熔炼映射与栏位校验

- [x] 3.1 在 `internal/core` 增加唯一的固定 `SmeltingOutput` 映射，覆盖粗铁到铁锭、沙子到玻璃、黏土块到砖块，并以单元测试冻结未知输入拒绝；运行 `go test ./internal/core -race -count=1`。
- [x] 3.2 修改 `internal/world/furnace.go`、`internal/world/furnace_test.go`、`internal/network/message.go` 与 `internal/network/furnace_test.go`，让 world/network 只通过 `core.SmeltingOutput` 校验熔炉栏位集合；正向接受 `ItemRawIron`、`ItemSand`、`ItemClay` 输入和 `ItemIronIngot`、`ItemGlass`、`ItemBrick` 输出，反向拒绝输入/输出集合之外的物品，并保留煤炭燃料与输出格只作来源的限制；运行 `go test ./internal/world ./internal/network -race -count=1`，再运行 codec/golden 定向命令 `go test ./internal/network -run 'Test(ProtocolV1SmallPacketGolden|ProtocolV7FurnacePacketIDsAreFrozen|ProtocolV12ContainerPayloadsAreFixedLength|FurnaceMessagesRejectInvalidValues|FurnaceDecodeRejectsUnknownWireValues)$' -count=1`。

## 4. 权威熔炉推进

- [x] 4.1 在 `internal/sim` 复用 `core.SmeltingOutput` 决定推进和完成产物；覆盖三种输入的点火、持续推进、完成、输出冲突与输出已满，并保持单一权威 tick；运行 `go test ./internal/sim -race -count=1`。
- [x] 4.2 在 `internal/sim` 的权威容器移动提交点实现输入种类切换清零 `ProgressTicks`、保留 `BurnTicks`；覆盖移除并放回同种输入的重置，并验证 Memory/TCP 与重启恢复一致；运行 `go test ./internal/sim ./internal/server ./internal/network -race -count=1`。

## 5. 熔炉性能与持久化回归

- [x] 5.1 更新 `internal/storage`、`internal/client` 与 `internal/server` 的存档、镜像、重启和 Memory/TCP 纵向测试，确认三种完整熔炉状态使用既有 `FurnaceSlot` 与协议字段无损往返，非法镜像状态整包拒绝，且无 schema 或协议版本升级；运行 `go test ./internal/storage ./internal/client -run 'Furnace|ChunkV|Future' -race -count=1`、`go test ./internal/server -run 'MaterialProcessing|FurnaceRestart' -race -count=1` 与 `go test ./internal/server -race -count=1`。
- [ ] 5.2 运行现有熔炉 benchmark 并仅记录数值，确认固定容量与热路径零分配门禁不退化；运行 `go test ./internal/sim -run '^$' -bench Furnace -benchmem -count=1`。

## 6. 七行固定合成 HUD

- [ ] 6.1 在 `internal/render/hotbar.go` 将固定 recipe ID 列表扩为七条并从其长度同步更新面板、绘制、命中和固定容量；在 `internal/render/hotbar_test.go` 与 `cmd/mcgo/app_test.go` 覆盖 640×360 的第七行、命中矩形、容量和应用层点击；运行 `go test ./internal/render ./cmd/mcgo -race -count=1`。
- [ ] 6.2 通过 `cmd/mcgo/capture.go` 的现有无窗口 `inventory-crafting` 场景更新且只更新 `cmd/mcgo/testdata/golden/inventory-crafting.png`；运行 `make visual-update`，再用 `test "$(git diff --name-only -- cmd/mcgo/testdata/golden)" = "cmd/mcgo/testdata/golden/inventory-crafting.png"` 断言没有其他 golden 变化，随后运行 `make visual-check` 与 `git diff --check`；不得启动或聚焦前台窗口。

## 7. 收尾验证

- [ ] 7.1 对改动的 Go 文件运行 `gofmt -w`，并运行 `gofmt -l .`，输出必须为空。
- [ ] 7.2 运行 `go test ./internal/archcheck -count=1`、`go test ./... -race` 与 `go vet ./...`，修复由本 change 引入的失败。
- [ ] 7.3 运行 `openspec validate material-processing-loop --strict --no-interactive`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`，确认契约、工作树和格式通过。

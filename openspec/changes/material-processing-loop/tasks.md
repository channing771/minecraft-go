## 2. 固定合成配方

- [ ] 2.1 在 `internal/core/recipe.go` 追加稳定 recipe ID `7` 的 `1 OakLog -> 4 OakPlanks`，保持 ID `1..6` 不变；在 `internal/core/recipe_test.go` 覆盖查询、原料不足、产物无空间与原子不变；运行 `go test ./internal/core -race -count=1`。

## 3. 熔炼映射与栏位校验

- [ ] 3.1 在 `internal/core` 增加唯一的固定 `SmeltingOutput` 映射，覆盖粗铁到铁锭、沙子到玻璃、黏土块到砖块，并以单元测试冻结未知输入拒绝；运行 `go test ./internal/core -race -count=1`。
- [ ] 3.2 在 `internal/world` 让熔炉输入、输出栏位只校验已注册熔炼输入和产物集合，保留燃料与输出来源限制；更新栏位与区块存档测试，运行 `go test ./internal/world -race -count=1`。

## 4. 权威熔炉推进

- [ ] 4.1 在 `internal/sim` 复用 `core.SmeltingOutput` 决定推进和完成产物；覆盖三种输入的点火、持续推进、完成、输出冲突与输出已满，并保持单一权威 tick；运行 `go test ./internal/sim -race -count=1`。
- [ ] 4.2 在 `internal/sim` 的权威容器移动提交点实现输入种类切换清零 `ProgressTicks`、保留 `BurnTicks`；覆盖移除并放回同种输入的重置，并验证 Memory/TCP 与重启恢复一致；运行 `go test ./internal/sim ./internal/server ./internal/network -race -count=1`。

## 5. 熔炉性能与持久化回归

- [ ] 5.1 更新受影响的 `internal/world`、`internal/sim` 存档和故障路径测试，确认既有 `FurnaceSlot` 字段可往返且无 schema 或协议版本升级；运行 `go test ./internal/world ./internal/sim -race -count=1`。
- [ ] 5.2 运行现有熔炉 benchmark 并仅记录数值，确认固定容量与热路径零分配门禁不退化；运行 `go test ./internal/sim -run '^$' -bench Furnace -benchmem -count=1`。

## 6. 七行固定合成 HUD

- [ ] 6.1 在 `internal/render/hotbar.go` 将固定 recipe ID 列表扩为七条，并从其长度同步更新面板、绘制、命中和固定容量；在 640×360 下覆盖第七行、命中矩形和容量；运行 `go test ./internal/render -race -count=1`。
- [ ] 6.2 更新并以无窗口方式复核 `inventory-crafting` golden，确认七条配方完整可见且没有 overflow；运行对应 `materials-showcase`/render 场景测试，不启动或聚焦前台窗口。

## 7. 收尾验证

- [ ] 7.1 对改动的 Go 文件运行 `gofmt -w`，并运行 `gofmt -l .`，输出必须为空。
- [ ] 7.2 运行 `go test ./internal/archcheck -count=1`、`go test ./... -race` 与 `go vet ./...`，修复由本 change 引入的失败。
- [ ] 7.3 运行 `openspec validate material-processing-loop --strict --no-interactive`、`openspec validate --all --strict --no-interactive` 和 `git diff --check`，确认契约、工作树和格式通过。

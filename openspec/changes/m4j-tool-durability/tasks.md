## 1. 冻结起点并核对契约

- [x] 1.1 核对五个冻结值（协议 v10、玩家 schema v3、区块 schema v4、metadata v2、benchmark scenario v12）与 M4H 归档状态，记录两条 baseline 文件的哈希供收尾任务比对：`internal/network/packet.go`、`internal/storage/player_codec.go`、`internal/storage/chunk_codec.go`、`internal/storage/metadata.go`、`cmd/mcgo/benchmark.go`。
  - 验证：`openspec validate --all --strict --no-interactive`；`rg -n 'ProtocolVersion uint32 = 10|currentPlayerSchema.*= 3|currentChunkSchema.*= 4|currentMetadataVersion.*= 2|scenarioVersion[[:space:]]*= 12' internal cmd | rg -v '_test'`；`shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json`

## 2. 修复掉落物合并不遵守单格上限（耐久的阻塞性前置）

- [x] 2.1 在 `internal/world/drop_test.go` 写失败测试，证明工具掉落物按单格上限 `1` 各占独立槽、可堆叠物品合并不受影响。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world -run "PrepareDropRespectsPerItemStackLimit|PrepareDropStillMergesStackableItems" -count=1'`
- [x] 2.2 `internal/world/drop.go` 的 `prepareDropSlot` 与 `PrepareDropBatch` 改用 `core.ItemStackLimit` 判定合并，取代硬编码 `core.MaxStackCount`。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/world ./internal/sim ./internal/server -race -count=1'`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`

## 3. 在 `core` 定义耐久语义

- [x] 3.1 在 `internal/core/item_test.go` 写失败测试：`ItemMaxDurability` 只覆盖两种镐、`ItemBrokenForm` 映射到对应损坏物品、损坏物品已注册且单格上限为 `1`、收紧后的 `ItemStack.Valid()` 校验耐久域。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "Durability|BrokenForm|BrokenTools" -count=1'`
- [x] 3.2 `internal/core/item.go`：在 `ItemIronPickaxe` 之后追加 `ItemBrokenStonePickaxe`、`ItemBrokenIronPickaxe`（不得插入既有 `iota` 中间）；`ItemStack` 新增 `Durability uint16` 字段；新增 `ItemMaxDurability`、`ItemBrokenForm`；`ItemStackLimit` 追加两个损坏物品到上限 `1` 分支；收紧 `ItemStack.Valid()`。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -race -count=1'`
- [x] 3.3 修完全仓因 `Valid()` 收紧而变为非法值的既有工具构造点（`internal/sim`、`internal/core`、`internal/server`、`internal/storage`、`internal/network` 的测试），统一补满耐久；同时让背包、掉落物、逻辑快照与客户端镜像保留完整耐久。协议 v10、玩家 schema v3、区块 schema v4 对无法表示的磨损工具显式拒绝，并在旧格式解码时补满；遗留 v4 多件工具堆按槽稳定拆分并标记重写，容量不足时无损失败。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`；`go vet ./...`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`

## 4. 合成产出满耐久

- [x] 4.1 在 `internal/core/recipe_test.go` 写失败测试：石镐/铁镐配方产出满耐久且是合法物品堆，非工具配方产出耐久恒为 `0`。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core -run "RecipesOutput" -count=1'`
- [x] 4.2 `internal/core/recipe.go` 的 `RecipeStonePickaxe`、`RecipeIronPickaxe` 产出携带满耐久字面量（`131`/`250`）。该项因 `AddStack` 开始严格校验完整物品堆而折叠进 3.3 完成。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/core ./internal/sim -race -count=1'`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`

## 5. 协议 v11 与共享物品堆编解码

- [ ] 5.1 在 `internal/network/codec_test.go`、`worldtime_test.go`、`packet_test.go`、`drop_test.go` 写失败测试：版本 golden 推进到 `11`、旧版本（含 v10）在 Play 前被拒绝、`ItemDrop` 耐久往返、非工具携带耐久被 `Validate` 拒绝。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -run "ProtocolVersion|RejectsVersion|Durability" -count=1'`
- [ ] 5.2 `internal/network/packet.go` 的 `ProtocolVersion` 升为 `11`；复用已经贯通 `sim.DropSnapshot`、`network.ItemDrop`、服务端发布与客户端镜像的 `Durability` 字段；`codec.go` 新增共享 `encodeItemStack` 并改造既有 `decodeItemStack`，`InventoryState`、`FurnaceState`、`ItemDrop` 三处编解码统一改用 5 字节物品堆。删除 v10 的 Inventory/ItemDrop“工具必须满耐久”门禁及两处解码补满 shim，改为读取真实字段；同步更新固定长度常量与 golden 十六进制串。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/network -race -count=1'`；`go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s`；`go test ./internal/network -run "^$" -bench SmallPacketCodec -benchmem -count=3 -benchtime=200x`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`

## 6. 玩家 schema v4 与区块 schema v5

- [ ] 6.1 在 `internal/storage/player_migration_test.go`、`migration_test.go` 写失败测试：v3→v4 玩家迁移、v4→v5 区块迁移都把旧工具补为满耐久，非工具耐久保持 `0`；v4 遗留多件工具堆稳定拆为单件槽并标记重写，容量不足时返回 `ErrCorrupt` 而不截断。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage -run "MigrationFillsFullToolDurability" -count=1'`
- [ ] 6.2 `internal/storage/player_codec.go` 的 `currentPlayerSchema` 升为 `4`；`chunk_codec.go` 的 `currentChunkSchema` 升为 `5`；`internal/world/drop.go` 的 `DropSlotBytes` 由 17 增至 19；`internal/storage/migration.go` 新增共用的 `fillFullDurability` 并在 `playerMigrations`/`chunkMigrations` 注册对应键。把当前 v4 解码阶段的“补满+拆分”过渡逻辑正式移入 v4→v5 migration，删除玩家 v3/区块 v4 的满耐久编码门禁和解码 shim；编解码逐处从「u16+u8」改为「u16+u8+u16」。既有 v3 玩家 golden 与 v4 区块 golden 字节保持不变，新增一份 v4 玩家与 v5 区块 golden，并用磨损工具往返测试证明真实耐久不会被重置。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/storage ./internal/world -race -count=1'`；`go test ./internal/storage -run "^$" -fuzz FuzzDecodeChunkPayload -fuzztime=10s`；`go test ./internal/storage -run "^$" -fuzz FuzzDecodePlayer -fuzztime=10s`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`

## 7. 权威扣减与损坏转换

- [ ] 7.1 在 `internal/sim/mining_test.go` 写失败测试：成功破坏扣减一点耐久、最后一点耐久破坏仍然生效并转为损坏物品、三条拒绝路径不消耗耐久、损坏工具采掘等同空手（遍历全部方块）、换栏/丢弃/损坏三种方式各自重置采掘进度。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim -run "Durability|BrokenForm|BrokenTool|ResetsProgressWhenHeldToolLeavesHand" -count=1'`
- [ ] 7.2 `internal/sim/mining.go` 的 `miningRule` 把两个损坏物品并入石头方块的空手分支；新增 `consumeToolDurability(player *playerState) bool`；在 `completeMining`/`advanceMining` 未被拒绝的分支调用它并标记 `inventoryDirty`。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/sim ./internal/world ./internal/core -race -count=1'`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`；`go test ./internal/sim -run "^$" -bench "." -benchmem -count=1 -benchtime=100x`（仅确认无分配回归，不设新阈值）

## 8. 快捷栏耐久条

- [ ] 8.1 在 `internal/render/hotbar_test.go` 写失败测试：`appendDurabilityBar` 只对磨损中的耐久工具产生 quad，满耐久/损坏物品/普通物品/空栏位不产生；填充宽度随剩余耐久变化。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render -run "DurabilityBar" -count=1'`
- [ ] 8.2 `internal/render/hotbar.go` 新增 `appendDurabilityBar`，在 `layoutInventory` 中对九个快捷栏格调用；提高 `maxHotbarQuads` 以覆盖新增 quad 上限；修正满界面/关闭界面的既有 quad 数量断言（按用例实际内容调整，不放宽上限比较）。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/render ./cmd/mcgo -race -count=1'`；`go test ./internal/archcheck -count=1`；`gofmt -l .`；`git diff --check`（自动验证不得创建或聚焦前台窗口）

## 9. 纵向闭环、文档与全门禁

- [ ] 9.1 在 `internal/server/drop_restart_test.go` 写 TCP/重启纵向测试，证明工具耐久跨 TCP 传输与正常关服重启精确保留；在 `internal/server/tcp_integration_test.go` 的版本拒绝矩阵中补入上一版本（v10）。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./internal/server ./internal/network ./internal/storage ./cmd/mcgod -race -count=1'`；`CGO_ENABLED=0 GOOS=linux go build -o /private/tmp/mcgod-m4j ./cmd/mcgod`
- [ ] 9.2 更新 `README.md` 与 `docs/notes/lan-server.md`：工具耐久上限、消耗与损坏行为、协议 v11（v10 拒绝）、玩家 schema v4、区块 schema v5、metadata v2 不变、升级前需备份世界目录、回退到 v10 程序必须恢复该备份。
  - 验证：`git diff --exit-code origin/main -- docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json`；`rg -n 'scenarioVersion[[:space:]]*= 12' cmd/mcgo/benchmark.go`；`shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline-m5.json`（与 1.1 记录的哈希比对，必须完全一致）
- [ ] 9.3 全仓门禁与最终范围核对。
  - 验证：`zsh -ic 'gvm use go1.26.0 >/dev/null && go test ./... -race'`；`go vet ./...`；`go test ./internal/archcheck -count=1`；`go test ./internal/network -run "^$" -fuzz FuzzSmallPacketCodec -fuzztime=10s`；`gofmt -l .`；`git diff --check`；`openspec validate --all --strict --no-interactive`；`git diff --stat origin/main...HEAD`

## 10. 主规格同步与归档

- [ ] 10.1 把 `tool-durability` 新 capability 同步到 `openspec/specs/`，用 `openspec validate --all --strict --no-interactive` 核对协议 v11、玩家 schema v4、区块 schema v5、metadata v2、scenario v12 全文一致，更新 `AGENTS.md`/`openspec/config.yaml` 的当前基线为 M4J。
- [ ] 10.2 全部任务和验证通过后归档 `m4j-tool-durability`，再次运行 `openspec validate --all --strict --no-interactive`、`git diff --check` 与最终 `git status`。

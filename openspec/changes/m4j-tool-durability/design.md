## Context

动机见 `proposal.md`，行为契约见 `specs/tool-durability/spec.md`，实现顺序见 `tasks.md`。上游设计背景在 `docs/superpowers/specs/2026-08-06-m4j-tool-durability-design.md`——本文件是它在 OpenSpec 结构下的裁剪与固化，二者不一致时以本文件与 delta spec 为准。

M4H 基线已经具备：协议 v10、玩家 schema v3、区块 schema v4、世界 metadata v2、benchmark scenario v12、固定 36 格权威背包、每区块 32 个持久掉落物槽、权威计时采掘（`internal/sim/mining.go` 的 `completeMining`/`miningRule`）、权威单件原地丢弃。`core.ItemStack` 目前只有 `Item`/`Count` 两个字段，`ItemStackLimit` 已经区分可堆叠物品（上限 64）与工具（上限 1），但掉落物合并判据未使用它。

## Goals / Non-Goals

**Goals:**

- 石镐与铁镐带有耐久，成功破坏方块时权威扣减一点，耗尽后变为不可用的损坏物品。
- 耐久随工具走遍背包、掉落物、存档与线上协议，不存在丢失耐久的路径。
- 玩家能在快捷栏看到剩余耐久。
- 既有玩家存档与区块存档可无损升级，旧工具视为满耐久。
- 修复 `prepareDropSlot` 的单格上限缺陷，作为耐久系统的阻塞性前置。

**Non-Goals:**

- 工具修复、附魔、回收损坏工具换回材料。
- 耐久附加到镐以外的物品（方块、材料、熔炉）。
- 采掘速度随耐久衰减。
- 新工具材质等级（木、金、钻石）。
- benchmark 场景升级或性能基线重建。

## Decisions

### 1. 耐久是 `ItemStack` 的字段

```go
type ItemStack struct {
    Item       ItemID
    Count      uint8
    Durability uint16 // 只对工具有意义；其余物品恒为 0
}
```

`ItemID` 是 `uint16`，因此含内存对齐后 `ItemStack` 由 4 字节变为 6 字节；其线上/存档编码由 3 字节（`Item` 2 + `Count` 1）变为 5 字节（追加 `Durability` 2 字节）。

配套两个纯查表函数，与既有 `ItemStackLimit` 并列，数据所有权都在 `internal/core`：

- `ItemMaxDurability(item) (uint16, bool)`：石镐 `131`、铁镐 `250`，其余返回 `0, false`。
- `ItemBrokenForm(item) (ItemID, bool)`：石镐与铁镐各映射到自己的损坏形态。

物品注册表在 `ItemIronPickaxe` 之后**追加** `ItemBrokenStonePickaxe` 与 `ItemBrokenIronPickaxe`，不得插入既有 `iota` 中间——插入会平移后续物品 ID，破坏既有存档与线上字节。

**否决「耐久存在并行表」**：并行数组必须在背包、掉落物、存档、协议四个面各自维护一份索引对应关系，任何一处遗漏都会让耐久与物品错位，而错位不会立刻暴露；作为 `ItemStack` 自身字段则错位在类型层面不可能发生。

**否决「耐久只存在玩家背包，不随掉落物传播」**：该方案不必改区块存档与 `ItemDrop` 协议，范围明显更小，但留下刷耐久漏洞——丢出再捡回即可把耐久重置为满。M4H 刚让丢弃变成一键可达，这个漏洞会让耐久系统失去意义。

**否决「把耐久编码进 `Count` 高位」**：工具 `Count` 恒为 1，高位确实空着，且零 payload 变化。但 `Count` 是全项目高频使用的字段，污染它意味着此后每一处比较、合并、拆分都要记得工具是特例。省下的字节不值这个长期负担。

### 2. 四条不变量

1. **非工具的 `Durability` 恒为 0**。否则同物品的两个栈会因一个无意义字段而拒绝合并。
2. **工具与损坏物品的单格上限恒为 1**。`ItemStackLimit` 已保证工具，损坏物品归入同一类。
3. **工具永不合并**（由不变量 2 推出）。
4. **损坏物品不满足任何采掘等级**，等同空手。

### 3. 前置缺陷修复：掉落物合并不遵守单格上限

`internal/world/drop.go` 的 `prepareDropSlot` 与 `PrepareDropBatch` 用硬编码的 `core.MaxStackCount`（64）判断能否合并，而不是查 `ItemStackLimit`。因此两把镐丢在同一方块位置会合并成 `Count = 2` 的一堆，违反镐的单格上限 1。这是既有缺陷，M4H 让主动丢弃变得一键可达之后成为玩家能轻易触发的路径。

对耐久而言这是阻塞性的：两把不同耐久的镐一旦合并，耐久信息必然丢失。修复是把判据改为查 `core.ItemStackLimit`，作为本批次独立的前置提交（`tasks.md` Task 2），先于耐久字段本身落地。

### 4. 消耗规则与状态转移，数据所有权在 `sim`

**扣减点**：`completeMining` 中全部预检通过、方块实际被移除之后扣 1。三条既有拒绝路径（受保护方块、区块未就绪、掉落物容量已满）一点耐久都不扣。扣减不看 `harvestable`——用错工具时方块照样被破坏（只是不掉落），工具确实干了活，就该磨损。

**归零转换**：扣到 0 时把该栏位整体替换为对应损坏物品（`Count: 1, Durability: 0`），标记 `inventoryDirty` 走既有发布路径。最后一次采掘仍然生效：方块正常破坏、掉落物正常产生，工具在该 tick 结束时才变为损坏品。

**工具离手即中断采掘**：既有采掘状态机已经跟踪 `held`（手持物品 ID），并在其变化时重置进度。换栏、丢弃与工具损坏都会改变 `held`，进度因此自动清零，无需新增状态机代码，只需测试钉住。

**已知边角，刻意不处理**：`held` 只比较 `ItemID`，因此玩家在两个栏位各放一把石镐、中途切换时进度会接力。引入「工具实例身份」的代价高于收益，且不影响耐久正确性——扣减的永远是完成时刻的选中格。

**新工具满耐久**：`RecipeStonePickaxe` 与 `RecipeIronPickaxe` 的产出置为该物品耐久上限。合成是唯一的耐久来源。

**损坏物品的行为**：不满足任何采掘等级；可丢弃、可在背包内搬运；不能合成、不能熔炼、不能修复。

### 5. 三处格式同时升版，依赖方向不变

| 面 | 从 → 到 | 变化 | 所属包 |
|---|---|---|---|
| 玩家存档 | schema v3 → v4 | 背包每格追加 `uint16` 耐久 | `internal/storage` |
| 区块存档 | schema v4 → v5 | 掉落物槽 17 → 19 字节 | `internal/storage`、`internal/world` |
| 线上协议 | v10 → v11 | `InventoryState`、`FurnaceState`、`ItemDrop` 携带耐久 | `internal/network` |

三者都只消费 `internal/core` 定义的 `ItemStack`/`ItemMaxDurability`/`ItemBrokenForm`，不反向依赖 `sim`/`storage`/`network`，符合既有依赖方向（`core` 在依赖图最底层，`sim`/`storage`/`network` 各自向上组合）。

**迁移规则统一**：旧存档中的工具一律视为满耐久，两处迁移共用 `internal/storage` 的同一条 `fillFullDurability` 规则。v3 玩家档与 v4 区块档均可无损单向升级，不需要备份即可读取。反向不兼容：回退到 v10 程序必须恢复升级前的备份，与 M4G 的 metadata v2 边界一致，须写入 README 与 `docs/notes/lan-server.md`。

**golden 策略**：v3 玩家档与 v4 区块档的既有 golden 字节保持不变，用于证明迁移能读旧数据；v4 玩家档与 v5 区块档各新增一份 golden。

**协议边界**：packet ID 全部不变、不重排，只有 `InventoryState`、`FurnaceState`、`ItemDrop` 的 payload 变长；v10 客户端在握手阶段、进入 Play 前拒绝。

**新增校验**：`ItemDrop.validate` 需补一条——非工具物品的耐久必须为 0，否则伪造 payload 可让泥土带着耐久混入掉落物。

**共享编解码**：`internal/network/codec.go` 新增 `encodeItemStack`/`decodeItemStack` 一对函数，`InventoryState`、`FurnaceState`、`ItemDrop` 三处编解码统一改用，避免某条消息漏带耐久。熔炉当前只接受煤炭与粗铁、耐久恒为 0；统一编码是为了让「携带物品堆的消息」只有一种字节布局，而不是让熔炉支持工具。

### 6. 快捷栏耐久条，数据所有权在 `render`

在快捷栏图标下沿绘制耐久条，仅对**有耐久上限的物品**（即 `ItemMaxDurability` 返回 `true` 的物品）且**当前耐久小于上限**时显示。损坏物品没有耐久上限，因此不显示耐久条——它的不可用状态由自身的物品图标表达。呈现完全由 `layoutInventory` 从已有的 `core.Inventory` 推导，不新增 pipeline 或纹理，`maxHotbarQuads` 相应提高以覆盖新增 quad 上限。这是本设计唯一触及 `internal/render` 的地方（`hotbar.go`），与 M4I 的天空/地形着色器是不同文件。

### 7. benchmark 场景保持 v12

benchmark 的协议指标只对 `PlayerInput` 采样，其固定 workload 既不发布背包也不产生掉落物，因此耐久不改变被测工作负载。`ItemStack` 变大对内存的影响可忽略：每区块 32 个掉落物槽多 64 字节，4489 区块合计约 287 KB，而 RSS 门禁尚有 300 MB 以上余量。scenario 保持 v12，不重建性能基线，不放宽任何门禁。

## Affected Files

- `internal/core/item.go`、`item_test.go`：新增物品 ID、`Durability` 字段、两个查表函数、`Valid()` 收紧。
- `internal/core/recipe.go`、`recipe_test.go`：两条镐配方产出满耐久。
- `internal/world/drop.go`、`drop_test.go`：`prepareDropSlot`/`PrepareDropBatch` 改用 `ItemStackLimit`；`DropSlotBytes` 17→19。
- `internal/sim/mining.go`、`mining_test.go`：`consumeToolDurability`、`miningRule` 损坏物品分支、完成分支接线。
- `internal/network/codec.go`、`message.go`、`packet.go` 及对应测试：`ProtocolVersion` 11、共享编解码、`ItemDrop.validate`。
- `internal/storage/player_codec.go`、`player_migration.go`、`chunk_codec.go`、`migration.go` 及对应测试与 `testdata`：schema v4/v5、迁移函数、golden。
- `internal/render/hotbar.go`、`hotbar_test.go`、`cmd/mcgo/app.go`：耐久条渲染、quad 预算。
- `internal/server/drop_restart_test.go`、`tcp_integration_test.go`：TCP/重启纵向证明。
- `README.md`、`docs/notes/lan-server.md`：兼容性与升级/回退说明。

## Risks / Trade-offs

- [三处格式同时升版] → 单次变更同时动玩家档、区块档与协议，回退窗口比前几批更窄。缓解方式是迁移只做单向补齐（旧值视为满耐久），并保留既有 golden 证明旧数据可读；回退到 v10 必须恢复升级前备份，已写入文档。
- [`ItemStack` 是高频值类型] → 全项目大量使用 `==` 比较与零值判断。加字段后零值语义仍然正确，但 `Valid()` 收紧后任何构造工具栈却不给满耐久的既有测试点都要在 Task 3 内一次性修完，不允许留到后续任务。
- [与 M4I 并行] → 唯一交叠是 `internal/render/hotbar.go`。若 M4I 意外扩展到 HUD，耐久条可以最后合并或临时下放为纯逻辑实现。
- [协议 v11 要求客户端与服务端同时升级] → 握手在 Play 前稳定拒绝 v10，局域网部署文档列出升级顺序；不做版本协商。

## Migration Plan

1. 先完成 `core`/`world`/`sim`/`network`/`storage`/`render` 的自动测试与协议 v11、schema v4/v5 固定值，再更新文档；迁移函数只做单向补齐（旧工具视为满耐久），不生成额外状态文件。
2. 部署时先正常停止 v10 客户端和服务端，并备份整个世界目录，再同时换成 v11 二进制打开该世界；旧客户端会在握手阶段得到版本不匹配。
3. 回退时必须先恢复升级前的世界目录备份，再换回匹配的 v10 客户端/服务端；v4 玩家档、v5 区块档不可被 v10 程序读取。
4. 若实现或验证显示默认 benchmark 工作负载发生变化，或 `docs/notes/perf-baseline.json`/`perf-baseline-m5.json` 字节需要改变，停止本 change 并先更新 proposal/design；不得静默改 scenario 或提升基线。

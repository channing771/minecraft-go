## Why

M4F 引入石镐与铁镐并让采掘按工具等级计时，M4H 让工具可以被主动丢弃，但工具至今没有损耗：一把镐可以无限使用。这让采集与合成之间缺少消耗闭环——玩家没有理由再次合成工具。本批次为两种镐加入耐久，使工具成为消耗品。范围刻意限制在权威逻辑、持久化、协议与快捷栏呈现，不触及光照与天空渲染链路（M4I 正在并行开发天空渲染）。

## What Changes

- 石镐与铁镐带有耐久（`ItemStack.Durability uint16`），成功破坏方块时权威扣减一点；耗尽后该栏位整体替换为对应的损坏物品，最后一次破坏仍然生效（方块正常移除、掉落物正常产生）。
- 三条既有拒绝路径（受保护方块、区块未就绪、掉落物容量已满）不消耗耐久——方块没被破坏，工具就不该磨损。
- 损坏物品不满足任何采掘等级，采掘行为等同空手；可丢弃、可在背包内搬运，不能合成、不能熔炼、不能修复。
- 工具离手（换栏、丢弃、损坏）复用既有采掘状态机的 `held` 变化检测，自动中断采掘进度，不新增状态机代码。
- 耐久随工具走遍背包、掉落物、存档与线上协议：`InventoryState`、`FurnaceState`、`ItemDrop` 统一改用共享的 5 字节物品堆编解码；`ItemDrop.validate` 新增校验，非工具物品的耐久必须为 0。
- 修复前置缺陷：`internal/world/drop.go` 的 `prepareDropSlot`/`PrepareDropBatch` 目前用硬编码 `core.MaxStackCount` 判断掉落物能否合并，而不是查 `ItemStackLimit`；这会让两把不同耐久的镐在地面合并、丢失耐久信息。修复后掉落物合并统一遵守单格上限，工具永不合并。
- 新工具（`RecipeStonePickaxe`、`RecipeIronPickaxe`）的合成产出为满耐久；合成是耐久的唯一来源，本批不实现修复、附魔或损坏回收。
- 快捷栏在有耐久上限且当前耐久小于上限的物品图标下沿绘制耐久条；损坏物品没有耐久上限，不显示耐久条。
- **BREAKING**：线上协议 v10 → v11，`InventoryState`、`FurnaceState`、`ItemDrop` payload 变长；既有 packet ID 不重排、不复用，v10 客户端在握手阶段、进入 Play 前被拒绝。
- **BREAKING**：玩家存档 schema v3 → v4（背包每格追加 `uint16` 耐久），区块存档 schema v4 → v5（掉落物槽 17 → 19 字节）；世界 metadata 保持 v2，benchmark scenario 保持 v12。
- 旧存档单向无损升级：v3 玩家档与 v4 区块档中的工具一律视为满耐久，不需要备份即可读取；既有 v3/v4 golden 字节保持不变，用于证明迁移能读旧数据。回退到 v10 程序**必须先恢复升级前的世界目录备份**，本次存档格式变化不可逆向读取。
- benchmark scenario 保持 v12，不重建、不放宽任何性能基线；`docs/notes/perf-baseline.json` 与 `docs/notes/perf-baseline-m5.json` 字节不变——默认 benchmark workload 不发布背包、不产生掉落物，耐久不改变被测工作负载。
- 非目标：工具修复、附魔、损坏物品回收换回材料、耐久影响采掘速度、新工具材质等级（木/金/钻石）、耐久附加到镐以外的物品。

## Capabilities

### New Capabilities

- `tool-durability`：定义工具耐久字段、消耗规则、损坏转换、跨背包/掉落物/存档/协议的无损传递、掉落物单格合并上限修复、旧存档迁移与快捷栏耐久呈现。

## Impact

- 物品语义：`internal/core` 追加 `ItemBrokenStonePickaxe`、`ItemBrokenIronPickaxe`（追加到 `ItemID` 的 `iota` 末尾，不插入既有值中间）；`ItemStack` 新增 `Durability` 字段；新增 `ItemMaxDurability`、`ItemBrokenForm` 两个纯查表函数；`ItemStack.Valid()` 收紧耐久域校验；`RecipeStonePickaxe`/`RecipeIronPickaxe` 产出满耐久。
- 世界与掉落物：`internal/world/drop.go` 的 `prepareDropSlot`/`PrepareDropBatch` 改用 `ItemStackLimit` 判定合并；`DropSlotBytes` 因物品堆变长从 17 增至 19 字节。
- 权威模拟：`internal/sim/mining.go` 新增 `consumeToolDurability`，在 `completeMining` 全部预检通过、方块实际移除之后扣减；`miningRule` 把两个损坏物品并入空手分支。
- 协议与传输：`internal/network` 的 `ProtocolVersion` 升为 11；新增共享的 `encodeItemStack`/`decodeItemStack`，`InventoryState`、`FurnaceState`、`ItemDrop` 统一改用；`ItemDrop.validate` 新增耐久域校验；golden/fuzz/small-packet benchmark 同步更新；v10 及更早版本在进入 Play 前稳定拒绝。
- 存档：`internal/storage` 的玩家 schema 升为 v4、区块 schema 升为 v5，新增两条单向迁移把旧工具补为满耐久；既有 v3/v4 golden 字节不变，各新增一份 v4/v5 golden。
- 渲染：`internal/render/hotbar.go` 新增 `appendDurabilityBar`，`maxHotbarQuads` 相应提高；不新增 pipeline 或纹理；不触及天空/地形/昼夜渲染链路。
- 兼容与回退：协议 v10 与 v11 不互通；玩家/区块存档从 v10 基线单向升级，升级前必须备份世界目录，回退到 v10 程序必须恢复该备份。
- 性能与并发：不新增 goroutine、worker、队列、锁或第三方依赖；benchmark scenario 保持 v12，两条 baseline 文件字节不变。

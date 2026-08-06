# M4J 工具耐久 Design

## 背景

M4F 引入石镐与铁镐并让采掘按工具等级计时，M4H 让工具可以被主动丢弃，但工具至今没有损耗：一把镐可以无限使用。这让采集与合成之间缺少消耗闭环——玩家没有理由再次合成工具。

本批次为两种镐加入耐久，使工具成为消耗品。范围刻意限制在权威逻辑、持久化、协议与快捷栏呈现，不触及光照与天空渲染链路（M4I 正在并行开发天空渲染）。

## 目标

- 石镐与铁镐带有耐久，成功破坏方块时消耗，耗尽后变为不可用的损坏物品。
- 耐久随工具走遍背包、掉落物、存档与线上协议，不存在丢失耐久的路径。
- 玩家能在快捷栏看到剩余耐久。
- 既有玩家存档与区块存档可无损升级，旧工具视为满耐久。

## 非目标

- 工具修复、附魔、回收损坏工具换回材料。
- 耐久附加到镐以外的物品（方块、材料、熔炉）。
- 采掘速度随耐久衰减。
- 新工具材质等级（木、金、钻石）。
- benchmark 场景升级或性能基线重建。

## 决策

### 1. 耐久是 `ItemStack` 的字段

```go
type ItemStack struct {
    Item       ItemID
    Count      uint8
    Durability uint16 // 只对工具有意义；其余物品恒为 0
}
```

`ItemID` 是 `uint16`，因此含内存对齐后 `ItemStack` 由 4 字节变为 6 字节；其线上/存档编码由 3 字节（`Item` 2 + `Count` 1）变为 5 字节。

配套两个纯查表函数，与既有 `ItemStackLimit` 并列：

- `ItemMaxDurability(item) (uint16, bool)`：石镐 `131`、铁镐 `250`，其余返回 `0, false`。
- `ItemBrokenForm(item) (ItemID, bool)`：石镐与铁镐各映射到自己的损坏形态。

物品注册表**追加** `ItemBrokenStonePickaxe` 与 `ItemBrokenIronPickaxe`，不得插入既有 `iota` 中间——插入会平移后续物品 ID，破坏既有存档与线上字节。

**否决「耐久存在并行表」**：并行数组必须在背包、掉落物、存档、协议四个面各自维护一份索引对应关系，任何一处遗漏都会让耐久与物品错位，而错位不会立刻暴露。

**否决「耐久只存在玩家背包，不随掉落物传播」**：该方案不必改区块存档与 `ItemDrop` 协议，范围明显更小，但留下刷耐久漏洞——丢出再捡回即可把耐久重置为满。M4H 刚让丢弃变成一键可达，这个漏洞会让耐久系统失去意义。

**否决「把耐久编码进 `Count` 高位」**：工具 `Count` 恒为 1，高位确实空着，且零 payload 变化。但 `Count` 是全项目高频使用的字段，污染它意味着此后每一处比较、合并、拆分都要记得工具是特例。省下的字节不值这个长期负担。

### 2. 四条不变量

1. **非工具的 `Durability` 恒为 0**。否则同物品的两个栈会因一个无意义字段而拒绝合并。
2. **工具与损坏物品的单格上限恒为 1**。`ItemStackLimit` 已保证工具，损坏物品归入同一类。
3. **工具永不合并**。
4. **损坏物品不满足任何采掘等级**，等同空手。

### 3. 前置缺陷：掉落物合并不遵守单格上限

`internal/world/drop.go` 的 `prepareDropSlot` 用硬编码的 `core.MaxStackCount`（64）判断能否合并，而不是查 `ItemStackLimit`：

```go
if drop.Active && drop.Stack.Item == item && drop.BlockIndex == blockIndex &&
    drop.Stack.Count < core.MaxStackCount {
```

因此两把镐丢在同一方块位置会合并成 `Count = 2` 的一堆，违反镐的单格上限 1。这是既有缺陷，M4H 让主动丢弃变得一键可达之后成为玩家能轻易触发的路径。

对耐久而言这是阻塞性的：两把不同耐久的镐一旦合并，耐久信息必然丢失。修复是把判据改为查 `ItemStackLimit`，作为本批次的独立前置提交。

### 4. 消耗规则与状态转移

**扣减点**：`completeMining` 中全部预检通过、方块实际被移除之后扣 1。三条既有拒绝路径（受保护方块、区块未就绪、掉落物容量已满）一点耐久都不扣——方块没被破坏，工具就不该磨损。

**归零转换**：扣到 0 时把该栏位整体替换为对应损坏物品（`Count: 1, Durability: 0`），标记 `inventoryDirty` 走既有发布路径。最后一次采掘仍然生效：方块正常破坏、掉落物正常产生，工具在该 tick 结束时才变为损坏品。

**工具离手即中断采掘**：既有采掘状态机已经跟踪 `held`（手持物品 ID），并在其变化时重置进度：

```go
if player.mining.target == hit.Block && player.mining.block == block &&
    player.mining.held == held && player.mining.requiredTicks != 0 {
    player.mining.progressTicks++
} else {
    player.mining = playerMiningState{}
}
```

换栏、丢弃与工具损坏都会改变 `held`，进度因此自动清零。该行为无需新增状态机代码，只需测试钉住。

**已知边角，刻意不处理**：`held` 只比较 `ItemID`，因此玩家在两个栏位各放一把石镐、中途切换时进度会接力。为此引入「工具实例身份」的代价高于收益，且不影响耐久正确性——扣减的永远是完成时刻的选中格。

**新工具满耐久**：`RecipeStonePickaxe` 与 `RecipeIronPickaxe` 的产出置为该物品耐久上限。合成是唯一的耐久来源。

**损坏物品的行为**：不满足任何采掘等级；可丢弃、可在背包内搬运；不能合成、不能熔炼。

### 5. 三处格式同时升版

| 面 | 从 → 到 | 变化 |
|---|---|---|
| 玩家存档 | schema v3 → v4 | 背包每格追加 `uint16` 耐久 |
| 区块存档 | schema v4 → v5 | 掉落物槽 17 → 19 字节 |
| 线上协议 | v10 → v11 | `InventoryState` 与 `ItemDrop` 携带耐久 |

**迁移规则统一**：旧存档中的工具一律视为满耐久。v3 玩家档与 v4 区块档均可无损升级，不需要备份恢复。反向不兼容：回退到 v10 程序必须恢复升级前的备份，与 M4G 的 metadata v2 边界一致，须写入 README。

**golden 策略**：v3 玩家档与 v4 区块档的既有 golden 字节保持不变，用于证明迁移能读旧数据；v4/v5 各新增一份 golden。

**协议边界**：packet ID 全部不变、不重排，只有 payload 变长；v10 客户端在握手阶段、进入 Play 前拒绝。

**新增校验**：`ItemDrop.validate` 需补一条——非工具物品的耐久必须为 0，否则伪造 payload 可让泥土带着耐久混入掉落物。

### 6. 快捷栏耐久条

在快捷栏图标下沿绘制耐久条，仅对**有耐久上限的物品**（即 `ItemMaxDurability` 返回 `true` 的物品）且**当前耐久小于上限**时显示。损坏物品没有耐久上限，因此不显示耐久条——它的不可用状态由自身的物品图标表达。

这是本设计唯一触及 `internal/render` 的地方（`hotbar.go`），与 M4I 的天空/地形着色器是不同文件。

### 7. benchmark 场景保持 v12

benchmark 的协议指标只对 `PlayerInput` 采样，其固定 workload 既不发布背包也不产生掉落物，因此耐久不改变被测工作负载。`ItemStack` 变大对内存的影响可忽略：每区块 32 个掉落物槽多 64 字节，4489 区块合计约 287 KB，而 RSS 门禁尚有 300 MB 以上余量。

**scenario 保持 v12，不重建性能基线，不放宽任何门禁。**

## 与 M4I 的冲突面

| 包 | 本设计 | 风险 |
|---|---|---|
| `core` / `sim` / `storage` / `network` | 大量改动 | 无 |
| `internal/render/hotbar.go` | 新增耐久条 | 低 |
| render 的天空/地形/昼夜、`world` 光照、`mesh` | 完全不触及 | 无 |

## 验证

- 四条不变量各自有独立测试。
- 扣减只在成功破坏时发生；三条拒绝路径各有原子性证明（背包、掉落物、revision、persistence 均不变）。
- 工具离手中断采掘：换栏、丢弃、损坏三种触发方式各有用例。
- 迁移：v3→v4 与 v4→v5 各有 golden；旧 golden 字节不变。
- Memory/TCP 观察一致；正常关服重启后耐久精确保留。
- 全仓 race、vet、archcheck、gofmt、`git diff --check`、OpenSpec strict。
- 协议 fuzz 与 small-packet benchmark；确认 scenario 与两条 baseline 文件字节未变。

## 任务切分

1. 前置验证与契约冻结
2. 修复 `prepareDropSlot` 的合并上限缺陷
3. `core` 数据模型与查表函数
4. 协议 v11
5. 玩家 schema v4、区块 schema v5 与迁移
6. `sim` 扣减与损坏转换
7. 快捷栏耐久条
8. TCP/重启纵向闭环与文档
9. 全门禁、主规格同步、归档

## 风险

- **三处格式同时升版**：单次变更同时动玩家档、区块档与协议，回退窗口比前几批更窄。缓解方式是迁移只做单向补齐（旧值视为满耐久），并保留既有 golden 证明旧数据可读。
- **`ItemStack` 是高频值类型**：全项目大量使用 `==` 比较与零值判断。加字段后零值语义仍然正确，但需要逐处确认没有编码路径依赖它此前的固定宽度。
- **与 M4I 并行**：唯一交叠是 `internal/render/hotbar.go`。若 M4I 意外扩展到 HUD，耐久条可以最后合并或临时下放为纯逻辑实现。

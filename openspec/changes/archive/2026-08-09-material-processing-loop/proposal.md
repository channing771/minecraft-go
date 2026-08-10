## Why

十四种常见材料已有稳定物品 ID，却尚不能完成原木加工、沙子熔玻璃或黏土块熔砖块。把这三条固定规则接入现有权威合成、熔炉和 HUD，可形成最小的基础材料加工闭环。

## What Changes

- 在既有固定配方末尾追加 `1 OakLog -> 4 OakPlanks`，稳定 recipe ID 为 `7`。
- 将共享权威熔炉的固定映射扩展为粗铁到铁锭、沙子到玻璃、黏土块到砖块；输出冲突时整炉暂停，输入种类切换时清零进度并保留燃烧时间。
- 把普通背包的固定合成列表扩为七行，在 640×360 内保持完整可见且命中区域与绘制一致。
- 不新增燃料、经验、离线进度补算、数据驱动配方或通用注册表。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `authoritative-crafting`: 固定配方扩展为 ID `1..7`，并固定 ID `7` 的原木到木板转换。
- `authoritative-furnaces`: 固定熔炼映射、栏位准入和输入切换后的推进语义扩展为三种材料。
- `voxel-visual-presentation`: 固定合成区域扩为七行并保持 640×360 可见和可点击。

## Impact

受影响实现范围为 `internal/core`、`internal/world`、`internal/network`、`internal/sim` 与 `internal/render/hotbar.go`，以及上述三份 OpenSpec 主规格的 delta。服务端继续是唯一权威，客户端只呈现确认状态；Memory 与 TCP 继续共用语义。所有输入、产物和既有 `ItemStack`、`FurnaceSlot` 字段已经稳定存在，因此协议、玩家 schema、区块 schema 与世界 metadata 均不升级；熔炉仍由单一权威 tick 推进并复用固定容量与既有性能边界。

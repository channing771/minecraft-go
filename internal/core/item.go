package core

// ItemID 是跨客户端/服务端稳定的全局物品编号。
type ItemID uint16

// 物品 ID 是协议稳定值，不能重排。
const (
	ItemNone ItemID = iota
	ItemStone
	ItemDirt
	ItemGrass
	ItemStoneBrick
	ItemCoal
	ItemRawIron
	ItemIronIngot
	ItemFurnace
	ItemIronBlock
	ItemStonePickaxe
	ItemIronPickaxe
	// 以下是工具耐久耗尽后的形态，只能追加在末尾：
	// 插入会平移后续物品 ID，破坏既有存档与线上字节。
	ItemBrokenStonePickaxe
	ItemBrokenIronPickaxe
	// ItemChest 是可堆叠的存储方块物品，没有耐久。
	ItemChest
	ItemLightBlock
	ItemCobblestone
	ItemSmoothStone
	ItemSand
	ItemGravel
	ItemOakLog
	ItemOakPlanks
	ItemLeaves
	ItemGlass
	ItemBrick
	ItemWhiteWool
	ItemRoofTile
	ItemClay
	ItemSnowBlock
	ItemMossyCobblestone
	// 以下 6 个是农业物品，只能追加在既有序列末尾（ItemIDMax 哨兵之前）：
	// 插入会平移后续物品 ID，破坏既有存档与线上字节。
	//
	// ItemStoneHoe / ItemIronHoe 是翻地工具，沿用镐的耐久与损坏形态模式；
	// 两个损坏形态紧随两把锄头，与 ItemBrokenStonePickaxe/ItemBrokenIronPickaxe
	// 的排布同形。
	ItemStoneHoe
	ItemIronHoe
	ItemBrokenStoneHoe
	ItemBrokenIronHoe
	// ItemWheatSeeds 是唯一能写出农业方块的物品（放置成 WheatStage0ID）；
	// ItemWheat 是成熟小麦的收获产物，本身不可放置。
	ItemWheatSeeds
	ItemWheat
	// ItemBread 是唯一的食物，只能由小麦合成（RecipeBread），本身不可放置、
	// 不是任何方块的掉落物。恢复值见 FoodValue。同样只能追加在 ItemIDMax
	// 哨兵之前。
	ItemBread
	// ItemIDMax 是合法物品编号的独占上界（最后一个合法 ItemID + 1），本身不是
	// 物品枚举成员。它供测试以「item < ItemIDMax」穷举全部物品，替代依赖
	//「某个具体物品恰为枚举末项」的脆弱写法；放在 core 是因为物品注册表归属
	// core。新物品只能追加在本哨兵之前（哨兵始终保持紧随末项），否则以哨兵
	// 为界的穷举测试会静默漏掉新物品——item_test.go 的枚举末项守护断言负责
	// 在追加时报警。
	ItemIDMax
)

const (
	// HotbarSlots 是快捷栏的固定栏位数。
	HotbarSlots = 9
	// MaxStackCount 是单个栏位可容纳的同类物品上限。
	MaxStackCount = 64
)

// ItemStack 是一个快捷栏栏位的值；空栏位是零值。
type ItemStack struct {
	Item  ItemID
	Count uint8
	// Durability 只对有耐久上限的工具有意义，其余物品恒为 0。
	Durability uint16
}

// Hotbar 是玩家的固定容量快捷栏，Selected 取值 0..HotbarSlots-1。
type Hotbar struct {
	Selected uint8
	Slots    [HotbarSlots]ItemStack
}

// Valid 报告栏位值是否规范：空栏位数量必须为零，非空栏位必须是已注册物品且数量不超过物品上限；
// 有耐久上限的工具，耐久必须落在 1..上限，没有耐久概念的物品耐久必须保持零值。
func (s ItemStack) Valid() bool {
	limit, ok := ItemStackLimit(s.Item)
	if !ok {
		return s.Item == ItemNone && s.Count == 0 && s.Durability == 0
	}
	if s.Count == 0 || s.Count > limit {
		return false
	}
	maxDurability, hasDurability := ItemMaxDurability(s.Item)
	if !hasDurability {
		// 没有耐久概念的物品必须保持零值，否则同物品的两个栈会因无意义字段拒绝合并。
		return s.Durability == 0
	}
	return s.Durability >= 1 && s.Durability <= maxDurability
}

// Valid 报告整个快捷栏是否规范。
func (h Hotbar) Valid() bool {
	if h.Selected >= HotbarSlots {
		return false
	}
	for _, s := range h.Slots {
		if !s.Valid() {
			return false
		}
	}
	return true
}

// Add 把一个物品放入快捷栏副本：先补最低索引的同类未满栏位，否则用最低索引的空栏位。
// 没有空间或物品未注册时返回原值和 false。
func (h Hotbar) Add(item ItemID) (Hotbar, bool) {
	limit, ok := ItemStackLimit(item)
	if !ok {
		return h, false
	}
	if _, ok := ItemMaxDurability(item); ok {
		return h, false
	}
	for i := range h.Slots {
		if h.Slots[i].Item == item && h.Slots[i].Count < limit {
			h.Slots[i].Count++
			return h, true
		}
	}
	for i := range h.Slots {
		if h.Slots[i].Item == ItemNone {
			h.Slots[i] = ItemStack{Item: item, Count: 1}
			return h, true
		}
	}
	return h, false
}

// Consume 从指定栏位扣除一个物品并规范化清空后的栏位。
// 栏位越界或为空时返回原值和 false。
func (h Hotbar) Consume(slot uint8) (Hotbar, bool) {
	if slot >= HotbarSlots {
		return h, false
	}
	stack := h.Slots[slot]
	if stack.Item == ItemNone || stack.Count == 0 {
		return h, false
	}
	stack.Count--
	if stack.Count == 0 {
		stack = ItemStack{}
	}
	h.Slots[slot] = stack
	return h, true
}

// BlockDrop 返回成功挖掘该方块得到的物品；不可采集的方块返回 false。
func BlockDrop(block BlockID) (ItemID, bool) {
	switch block {
	case StoneID:
		return ItemStone, true
	case DirtID:
		return ItemDirt, true
	case GrassID:
		return ItemGrass, true
	case StoneBrickID:
		return ItemStoneBrick, true
	case CoalOreID:
		return ItemCoal, true
	case IronOreID:
		return ItemRawIron, true
	case FurnaceID:
		return ItemFurnace, true
	case IronBlockID:
		return ItemIronBlock, true
	case ChestID:
		return ItemChest, true
	case LightBlockID:
		return ItemLightBlock, true
	case CobblestoneID:
		return ItemCobblestone, true
	case SmoothStoneID:
		return ItemSmoothStone, true
	case SandID:
		return ItemSand, true
	case GravelID:
		return ItemGravel, true
	case OakLogID:
		return ItemOakLog, true
	case OakPlanksID:
		return ItemOakPlanks, true
	case LeavesID:
		return ItemLeaves, true
	case GlassID:
		return ItemGlass, true
	case BrickID:
		return ItemBrick, true
	case WhiteWoolID:
		return ItemWhiteWool, true
	case RoofTileID:
		return ItemRoofTile, true
	case ClayID:
		return ItemClay, true
	case SnowBlockID:
		return ItemSnowBlock, true
	case MossyCobblestoneID:
		return ItemMossyCobblestone, true
	// 耕地两态都还原成 1 泥土：耕地不是可携带的方块物品，挖掉它只是把翻地
	// 这一步撤销。
	case FarmlandDryID, FarmlandWetID:
		return ItemDirt, true
	// 未成熟小麦掉 1 种子：误挖不亏种子，耕种循环因此不会死。
	case WheatStage0ID, WheatStage1ID, WheatStage2ID, WheatStage3ID,
		WheatStage4ID, WheatStage5ID, WheatStage6ID:
		return ItemWheatSeeds, true
	// 成熟小麦的完整产出是 1 小麦 + 2 种子，但本函数的返回形状只能表达单一
	// 产物，因此这里只给出主产物小麦；额外的种子由收获路径按方块编号分支补发
	// （变更 authoritative-farming 的任务组 5）。不在这里发明新的多产物形状。
	case WheatStage7ID:
		return ItemWheat, true
	default:
		return ItemNone, false
	}
}

// ItemStackLimit 返回物品的单格上限；未知物品返回 false。
func ItemStackLimit(item ItemID) (uint8, bool) {
	switch item {
	case ItemStone, ItemDirt, ItemGrass, ItemStoneBrick, ItemCoal,
		ItemRawIron, ItemIronIngot, ItemFurnace, ItemIronBlock, ItemChest, ItemLightBlock,
		ItemCobblestone, ItemSmoothStone, ItemSand, ItemGravel, ItemOakLog,
		ItemOakPlanks, ItemLeaves, ItemGlass, ItemBrick, ItemWhiteWool,
		ItemRoofTile, ItemClay, ItemSnowBlock, ItemMossyCobblestone,
		ItemWheatSeeds, ItemWheat, ItemBread:
		return MaxStackCount, true
	case ItemStonePickaxe, ItemIronPickaxe,
		ItemBrokenStonePickaxe, ItemBrokenIronPickaxe,
		ItemStoneHoe, ItemIronHoe,
		ItemBrokenStoneHoe, ItemBrokenIronHoe:
		return 1, true
	default:
		return 0, false
	}
}

// ItemMaxDurability 返回工具的耐久上限；没有耐久的物品返回 0 与 false。
func ItemMaxDurability(item ItemID) (uint16, bool) {
	switch item {
	case ItemStonePickaxe:
		return 131, true
	case ItemIronPickaxe:
		return 250, true
	// 锄头取与同材质镐相同的耐久：两者都是「每次成功动作恰好扣 1 点」的工具
	// （采掘破坏方块扣 1、翻地成功扣 1），同一材质给两种工具不同数值只会制造
	// 第二套没有来源的数字，也会让「石器换代到铁器」的手感在采掘与耕种两条线
	// 上不一致。
	case ItemStoneHoe:
		return 131, true
	case ItemIronHoe:
		return 250, true
	default:
		return 0, false
	}
}

// ItemBrokenForm 返回工具耐久耗尽后的形态；不会损坏的物品返回 ItemNone 与 false。
func ItemBrokenForm(item ItemID) (ItemID, bool) {
	switch item {
	case ItemStonePickaxe:
		return ItemBrokenStonePickaxe, true
	case ItemIronPickaxe:
		return ItemBrokenIronPickaxe, true
	case ItemStoneHoe:
		return ItemBrokenStoneHoe, true
	case ItemIronHoe:
		return ItemBrokenIronHoe, true
	default:
		return ItemNone, false
	}
}

// RegisteredItem 报告该物品是否是已注册的合法物品。
// 合法性与放置映射分离：煤炭、粗铁和铁锭合法但不可直接放置。
func RegisteredItem(item ItemID) bool {
	_, ok := ItemStackLimit(item)
	return ok
}

// ItemPlacement 返回该物品放置后写入世界的方块；不可放置的物品返回 false。
func ItemPlacement(item ItemID) (BlockID, bool) {
	switch item {
	case ItemStone:
		return StoneID, true
	case ItemDirt:
		return DirtID, true
	case ItemGrass:
		return GrassID, true
	case ItemStoneBrick:
		return StoneBrickID, true
	case ItemFurnace:
		return FurnaceID, true
	case ItemIronBlock:
		return IronBlockID, true
	case ItemChest:
		return ChestID, true
	case ItemLightBlock:
		return LightBlockID, true
	case ItemCobblestone:
		return CobblestoneID, true
	case ItemSmoothStone:
		return SmoothStoneID, true
	case ItemSand:
		return SandID, true
	case ItemGravel:
		return GravelID, true
	case ItemOakLog:
		return OakLogID, true
	case ItemOakPlanks:
		return OakPlanksID, true
	case ItemLeaves:
		return LeavesID, true
	case ItemGlass:
		return GlassID, true
	case ItemBrick:
		return BrickID, true
	case ItemWhiteWool:
		return WhiteWoolID, true
	case ItemRoofTile:
		return RoofTileID, true
	case ItemClay:
		return ClayID, true
	case ItemSnowBlock:
		return SnowBlockID, true
	case ItemMossyCobblestone:
		return MossyCobblestoneID, true
	// 种子放置成刚种下的阶段 0。耕地与作物方块本身没有对应的方块物品，
	// 因此不出现在本表里：耕地只能由锄头翻出，作物只能由种子长成。
	case ItemWheatSeeds:
		return WheatStage0ID, true
	default:
		return AirID, false
	}
}

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
}

// Hotbar 是玩家的固定容量快捷栏，Selected 取值 0..HotbarSlots-1。
type Hotbar struct {
	Selected uint8
	Slots    [HotbarSlots]ItemStack
}

// Valid 报告栏位值是否规范：空栏位数量必须为零，非空栏位必须是已注册物品且数量在 1..MaxStackCount。
func (s ItemStack) Valid() bool {
	if s.Item == ItemNone {
		return s.Count == 0
	}
	if !RegisteredItem(s.Item) {
		return false
	}
	return s.Count >= 1 && s.Count <= MaxStackCount
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
	if !RegisteredItem(item) {
		return h, false
	}
	for i := range h.Slots {
		if h.Slots[i].Item == item && h.Slots[i].Count < MaxStackCount {
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
	default:
		return ItemNone, false
	}
}

// RegisteredItem 报告该物品是否是已注册的合法物品。
// 合法性与放置映射分离：煤炭、粗铁和铁锭合法但不可直接放置。
func RegisteredItem(item ItemID) bool {
	return item >= ItemStone && item <= ItemIronBlock
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
	default:
		return AirID, false
	}
}

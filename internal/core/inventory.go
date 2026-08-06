package core

const (
	// BackpackSlots 是快捷栏之外的固定背包格数。
	BackpackSlots = 27
	// InventorySlots 是玩家完整物品状态的统一索引数：0..8 是快捷栏，9..35 是背包。
	InventorySlots = HotbarSlots + BackpackSlots
)

// Inventory 是玩家的完整权威物品状态。
type Inventory struct {
	Hotbar   Hotbar
	Backpack [BackpackSlots]ItemStack
}

// Valid 报告完整物品状态是否规范。
func (inventory Inventory) Valid() bool {
	if !inventory.Hotbar.Valid() {
		return false
	}
	for _, stack := range inventory.Backpack {
		if !stack.Valid() {
			return false
		}
	}
	return true
}

// Slot 按统一索引读取一格；越界返回 false。
func (inventory Inventory) Slot(slot uint8) (ItemStack, bool) {
	if slot >= InventorySlots {
		return ItemStack{}, false
	}
	if slot < HotbarSlots {
		return inventory.Hotbar.Slots[slot], true
	}
	return inventory.Backpack[slot-HotbarSlots], true
}

// setSlot 按统一索引写入一格；调用方保证索引有效。
func (inventory *Inventory) setSlot(slot uint8, stack ItemStack) {
	if slot < HotbarSlots {
		inventory.Hotbar.Slots[slot] = stack
		return
	}
	inventory.Backpack[slot-HotbarSlots] = stack
}

// AddStack 把已注册且数量为 1..64 的来源堆按 ItemStackLimit 拆分，依次装入
// 快捷栏同类、快捷栏空格、背包同类、背包空格；返回新状态和余量，非法来源原样返回。
func (inventory Inventory) AddStack(stack ItemStack) (Inventory, ItemStack) {
	if _, ok := ItemStackLimit(stack.Item); !ok || stack.Count == 0 || stack.Count > MaxStackCount {
		return inventory, stack
	}
	for _, phase := range [4]struct {
		backpack bool
		merge    bool
	}{
		{backpack: false, merge: true},
		{backpack: false, merge: false},
		{backpack: true, merge: true},
		{backpack: true, merge: false},
	} {
		stack = inventory.fillPhase(stack, phase.backpack, phase.merge)
		if stack.Count == 0 {
			return inventory, ItemStack{}
		}
	}
	return inventory, stack
}

// fillPhase 在一段固定栏位上合并或占用空格，返回剩余堆。
func (inventory *Inventory) fillPhase(stack ItemStack, backpack, merge bool) ItemStack {
	limit, _ := ItemStackLimit(stack.Item)
	first, last := uint8(0), uint8(HotbarSlots)
	if backpack {
		first, last = HotbarSlots, InventorySlots
	}
	for slot := first; slot < last; slot++ {
		if stack.Count == 0 {
			return ItemStack{}
		}
		current, _ := inventory.Slot(slot)
		if merge {
			if current.Item != stack.Item || current.Count >= limit {
				continue
			}
		} else if current.Item != ItemNone {
			continue
		}
		if !merge {
			current = ItemStack{Item: stack.Item}
		}
		space := limit - current.Count
		moved := min(space, stack.Count)
		current.Count += moved
		stack.Count -= moved
		inventory.setSlot(slot, current)
	}
	if stack.Count == 0 {
		return ItemStack{}
	}
	return stack
}

// MoveStack 在完整状态的副本上整堆移动：空目标接收整堆，同类目标尽量合并并保留余量，
// 异类非空目标交换。同格、越界、空来源或同类满目标返回原值和 false。
func (inventory Inventory) MoveStack(from, to uint8) (Inventory, bool) {
	if from == to {
		return inventory, false
	}
	source, ok := inventory.Slot(from)
	if !ok || source.Item == ItemNone {
		return inventory, false
	}
	target, ok := inventory.Slot(to)
	if !ok {
		return inventory, false
	}
	switch {
	case target.Item == ItemNone:
		inventory.setSlot(to, source)
		inventory.setSlot(from, ItemStack{})
	case target.Item == source.Item:
		limit, _ := ItemStackLimit(source.Item)
		space := limit - target.Count
		if space == 0 {
			return inventory, false
		}
		moved := min(space, source.Count)
		target.Count += moved
		source.Count -= moved
		if source.Count == 0 {
			source = ItemStack{}
		}
		inventory.setSlot(to, target)
		inventory.setSlot(from, source)
	default:
		inventory.setSlot(to, source)
		inventory.setSlot(from, target)
	}
	return inventory, true
}

// SetSlot 返回把统一索引 slot 写为 stack 后的新值；
// 索引越界或物品未注册时返回原值和 false。
func (inventory Inventory) SetSlot(slot uint8, stack ItemStack) (Inventory, bool) {
	if slot >= InventorySlots || !stack.Valid() {
		return inventory, false
	}
	next := inventory
	next.setSlot(slot, stack)
	return next, true
}

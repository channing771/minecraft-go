package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestInventoryShapeIsFixed(t *testing.T) {
	if core.BackpackSlots != 27 {
		t.Fatalf("BackpackSlots = %d，契约要求 27", core.BackpackSlots)
	}
	if core.InventorySlots != core.HotbarSlots+core.BackpackSlots {
		t.Fatalf("InventorySlots = %d，想要 %d", core.InventorySlots, core.HotbarSlots+core.BackpackSlots)
	}
	var inventory core.Inventory
	if len(inventory.Backpack) != core.BackpackSlots {
		t.Fatalf("len(Backpack) = %d，想要 %d", len(inventory.Backpack), core.BackpackSlots)
	}
	if !inventory.Valid() {
		t.Fatal("零值 Inventory 应当有效")
	}
}

func TestInventoryValidRejectsBadSlots(t *testing.T) {
	badHotbar := core.Inventory{}
	badHotbar.Hotbar.Selected = core.HotbarSlots
	badBackpackItem := core.Inventory{}
	badBackpackItem.Backpack[3] = core.ItemStack{Item: core.ItemID(4242), Count: 1}
	badBackpackCount := core.Inventory{}
	badBackpackCount.Backpack[0] = core.ItemStack{
		Item: core.ItemStone, Count: core.MaxStackCount + 1,
	}
	ghost := core.Inventory{}
	ghost.Backpack[26] = core.ItemStack{Item: core.ItemNone, Count: 2}

	for name, inventory := range map[string]core.Inventory{
		"越界选中栏位":  badHotbar,
		"未知背包物品":  badBackpackItem,
		"背包数量超限":  badBackpackCount,
		"空物品非零数量": ghost,
	} {
		if inventory.Valid() {
			t.Fatalf("%s：非法 Inventory 被接受", name)
		}
	}
}

func TestInventorySlotIndexMapsHotbarThenBackpack(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemStone, Count: 1}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemDirt, Count: 2}
	inventory.Backpack[core.BackpackSlots-1] = core.ItemStack{Item: core.ItemGrass, Count: 3}

	cases := []struct {
		slot uint8
		want core.ItemStack
	}{
		{2, core.ItemStack{Item: core.ItemStone, Count: 1}},
		{core.HotbarSlots, core.ItemStack{Item: core.ItemDirt, Count: 2}},
		{core.InventorySlots - 1, core.ItemStack{Item: core.ItemGrass, Count: 3}},
	}
	for _, tc := range cases {
		got, ok := inventory.Slot(tc.slot)
		if !ok || got != tc.want {
			t.Fatalf("Slot(%d) = %+v, %v，想要 %+v, true", tc.slot, got, ok, tc.want)
		}
	}
	if _, ok := inventory.Slot(core.InventorySlots); ok {
		t.Fatal("越界索引被接受")
	}
}

func TestInventoryAddStackFillsHotbarBeforeBackpack(t *testing.T) {
	var inventory core.Inventory
	// 快捷栏同类未满优先，其次快捷栏空格。
	inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemStone, Count: 62}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemStone, Count: 60}

	next, remainder := inventory.AddStack(core.ItemStack{Item: core.ItemStone, Count: 3})
	if remainder != (core.ItemStack{}) {
		t.Fatalf("余量 = %+v，想要全部装入", remainder)
	}
	if next.Hotbar.Slots[4].Count != core.MaxStackCount {
		t.Fatalf("快捷栏同类格 = %+v，想要补满", next.Hotbar.Slots[4])
	}
	if next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemStone, Count: 1}) {
		t.Fatalf("快捷栏空格 = %+v，想要接收剩余 1 个", next.Hotbar.Slots[0])
	}
	if next.Backpack[0].Count != 60 {
		t.Fatalf("背包同类格被提前使用: %+v", next.Backpack[0])
	}
	if inventory.Hotbar.Slots[4].Count != 62 {
		t.Fatal("AddStack 必须在值副本上完成")
	}
}

func TestInventoryAddStackFallsBackToBackpack(t *testing.T) {
	var inventory core.Inventory
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemDirt, Count: core.MaxStackCount,
		}
	}
	inventory.Backpack[5] = core.ItemStack{Item: core.ItemStone, Count: 63}

	next, remainder := inventory.AddStack(core.ItemStack{Item: core.ItemStone, Count: 4})
	if remainder != (core.ItemStack{}) {
		t.Fatalf("余量 = %+v，想要全部装入", remainder)
	}
	if next.Backpack[5].Count != core.MaxStackCount {
		t.Fatalf("背包同类格 = %+v，想要补满", next.Backpack[5])
	}
	if next.Backpack[0] != (core.ItemStack{Item: core.ItemStone, Count: 3}) {
		t.Fatalf("背包最低空格 = %+v，想要接收剩余 3 个", next.Backpack[0])
	}
}

func TestInventoryAddStackKeepsRemainderWhenFull(t *testing.T) {
	var inventory core.Inventory
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemDirt, Count: core.MaxStackCount,
		}
	}
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = core.ItemStack{
			Item: core.ItemDirt, Count: core.MaxStackCount,
		}
	}

	next, remainder := inventory.AddStack(core.ItemStack{Item: core.ItemStone, Count: 5})
	if next != inventory {
		t.Fatal("全满时 Inventory 必须保持不变")
	}
	if remainder != (core.ItemStack{Item: core.ItemStone, Count: 5}) {
		t.Fatalf("余量 = %+v，想要原样保留", remainder)
	}
}

func TestInventoryAddStackPartiallyFills(t *testing.T) {
	var inventory core.Inventory
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemDirt, Count: core.MaxStackCount,
		}
	}
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = core.ItemStack{
			Item: core.ItemDirt, Count: core.MaxStackCount,
		}
	}
	inventory.Backpack[9] = core.ItemStack{Item: core.ItemStone, Count: 62}

	next, remainder := inventory.AddStack(core.ItemStack{Item: core.ItemStone, Count: 5})
	if next.Backpack[9].Count != core.MaxStackCount {
		t.Fatalf("部分装入后背包格 = %+v", next.Backpack[9])
	}
	if remainder != (core.ItemStack{Item: core.ItemStone, Count: 3}) {
		t.Fatalf("余量 = %+v，想要保留 3 个", remainder)
	}
}

func TestInventoryAddStackRejectsInvalidStack(t *testing.T) {
	var inventory core.Inventory
	invalid := []core.ItemStack{
		{},
		{Item: core.ItemStone, Count: 0},
		{Item: core.ItemNone, Count: 3},
		{Item: core.ItemID(4242), Count: 1},
		{Item: core.ItemStone, Count: core.MaxStackCount + 1},
	}
	for _, stack := range invalid {
		next, remainder := inventory.AddStack(stack)
		if next != inventory || remainder != stack {
			t.Fatalf("非法堆 %+v 被处理: next=%+v remainder=%+v", stack, next, remainder)
		}
	}
}

func TestInventoryMoveStackIntoEmptySlot(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 7}

	next, ok := inventory.MoveStack(1, core.HotbarSlots+4)
	if !ok {
		t.Fatal("向空格移动应当成功")
	}
	if next.Hotbar.Slots[1] != (core.ItemStack{}) {
		t.Fatalf("来源格 = %+v，想要清空", next.Hotbar.Slots[1])
	}
	if next.Backpack[4] != (core.ItemStack{Item: core.ItemStone, Count: 7}) {
		t.Fatalf("目标格 = %+v，想要整堆移入", next.Backpack[4])
	}
	if inventory.Hotbar.Slots[1].Count != 7 {
		t.Fatal("MoveStack 必须在值副本上完成")
	}
}

func TestInventoryMoveStackMergesAndKeepsRemainder(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 10}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemStone, Count: 60}

	next, ok := inventory.MoveStack(0, core.HotbarSlots)
	if !ok {
		t.Fatal("同类合并应当成功")
	}
	if next.Backpack[0].Count != core.MaxStackCount {
		t.Fatalf("目标格 = %+v，想要补满到 64", next.Backpack[0])
	}
	if next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemStone, Count: 6}) {
		t.Fatalf("来源格 = %+v，想要保留 6 个", next.Hotbar.Slots[0])
	}
}

func TestInventoryMoveStackSwapsDifferentItems(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[3] = core.ItemStack{Item: core.ItemStone, Count: 2}
	inventory.Backpack[7] = core.ItemStack{Item: core.ItemGrass, Count: 5}

	next, ok := inventory.MoveStack(3, core.HotbarSlots+7)
	if !ok {
		t.Fatal("异类交换应当成功")
	}
	if next.Hotbar.Slots[3] != (core.ItemStack{Item: core.ItemGrass, Count: 5}) ||
		next.Backpack[7] != (core.ItemStack{Item: core.ItemStone, Count: 2}) {
		t.Fatalf("交换结果 = %+v / %+v", next.Hotbar.Slots[3], next.Backpack[7])
	}
}

func TestInventoryMoveStackRejectsInvalidRequests(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}

	cases := []struct {
		name     string
		from, to uint8
	}{
		{"同格", 0, 0},
		{"来源越界", core.InventorySlots, 0},
		{"目标越界", 0, core.InventorySlots},
		{"空来源", 5, 0},
		{"同类目标已满", 0, 1},
	}
	for _, tc := range cases {
		next, ok := inventory.MoveStack(tc.from, tc.to)
		if ok {
			t.Fatalf("%s：非法移动被接受", tc.name)
		}
		if next != inventory {
			t.Fatalf("%s：非法移动修改了原值", tc.name)
		}
	}
}

func BenchmarkInventoryAddStack(b *testing.B) {
	var inventory core.Inventory
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemDirt, Count: core.MaxStackCount,
		}
	}
	stack := core.ItemStack{Item: core.ItemStone, Count: 64}
	b.ReportAllocs()
	for b.Loop() {
		inventory.AddStack(stack)
	}
}

func BenchmarkInventoryMoveStack(b *testing.B) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 32}
	inventory.Backpack[26] = core.ItemStack{Item: core.ItemGrass, Count: 32}
	b.ReportAllocs()
	for b.Loop() {
		inventory.MoveStack(0, core.InventorySlots-1)
	}
}

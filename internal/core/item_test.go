package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestCanonicalItemIDsStayStable(t *testing.T) {
	got := []core.ItemID{
		core.ItemNone,
		core.ItemStone,
		core.ItemDirt,
		core.ItemGrass,
	}
	for i, id := range got {
		if id != core.ItemID(i) {
			t.Fatalf("ItemID[%d] = %d，协议要求固定为 %d", i, id, i)
		}
	}
}

func TestHotbarShapeIsFixed(t *testing.T) {
	var h core.Hotbar
	if core.HotbarSlots != 9 {
		t.Fatalf("HotbarSlots = %d，契约要求 9", core.HotbarSlots)
	}
	if core.MaxStackCount != 64 {
		t.Fatalf("MaxStackCount = %d，契约要求 64", core.MaxStackCount)
	}
	if len(h.Slots) != core.HotbarSlots {
		t.Fatalf("len(Slots) = %d，想要 %d", len(h.Slots), core.HotbarSlots)
	}
	if !h.Valid() {
		t.Fatal("零值快捷栏应当有效：9 个空栏位且选中栏位 0")
	}
}

func TestItemStackValid(t *testing.T) {
	cases := []struct {
		name  string
		stack core.ItemStack
		want  bool
	}{
		{"空栏位", core.ItemStack{}, true},
		{"单个石头", core.ItemStack{Item: core.ItemStone, Count: 1}, true},
		{"满堆叠", core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}, true},
		{"超出堆叠上限", core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount + 1}, false},
		{"非空物品零数量", core.ItemStack{Item: core.ItemGrass, Count: 0}, false},
		{"空物品非零数量", core.ItemStack{Item: core.ItemNone, Count: 3}, false},
		{"未知物品", core.ItemStack{Item: core.ItemID(9999), Count: 1}, false},
	}
	for _, tc := range cases {
		if got := tc.stack.Valid(); got != tc.want {
			t.Fatalf("%s：Valid() = %v，想要 %v", tc.name, got, tc.want)
		}
	}
}

func TestHotbarValidRejectsBadState(t *testing.T) {
	valid := core.Hotbar{Selected: 8}
	valid.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
	if !valid.Valid() {
		t.Fatal("合法快捷栏被拒绝")
	}

	outOfRange := valid
	outOfRange.Selected = core.HotbarSlots
	if outOfRange.Valid() {
		t.Fatal("越界选中栏位必须被拒绝")
	}

	badSlot := valid
	badSlot.Slots[3] = core.ItemStack{Item: core.ItemNone, Count: 1}
	if badSlot.Valid() {
		t.Fatal("空物品与非零数量的组合必须被拒绝")
	}

	unknownItem := valid
	unknownItem.Slots[5] = core.ItemStack{Item: core.ItemID(200), Count: 1}
	if unknownItem.Valid() {
		t.Fatal("未知物品必须被拒绝")
	}
}

func TestHotbarAddPrefersSameItemBeforeEmptySlot(t *testing.T) {
	var h core.Hotbar
	h.Slots[2] = core.ItemStack{Item: core.ItemDirt, Count: 63}

	got, ok := h.Add(core.ItemDirt)
	if !ok {
		t.Fatal("同类未满栏位应当可以接收物品")
	}
	if got.Slots[2] != (core.ItemStack{Item: core.ItemDirt, Count: 64}) {
		t.Fatalf("栏位 2 = %+v，想要 64 个泥土", got.Slots[2])
	}
	if got.Slots[0] != (core.ItemStack{}) {
		t.Fatalf("栏位 0 = %+v，应当保持为空", got.Slots[0])
	}
	if h.Slots[2].Count != 63 {
		t.Fatal("Add 必须在值副本上完成，原值不得被修改")
	}
}

func TestHotbarAddUsesLowestEmptySlot(t *testing.T) {
	var h core.Hotbar
	h.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	h.Slots[2] = core.ItemStack{Item: core.ItemGrass, Count: 1}
	h.Slots[3] = core.ItemStack{Item: core.ItemStone, Count: 1}
	h.Slots[4] = core.ItemStack{Item: core.ItemStone, Count: 1}
	h.Slots[6] = core.ItemStack{Item: core.ItemStone, Count: 1}
	h.Slots[7] = core.ItemStack{Item: core.ItemStone, Count: 1}
	h.Slots[8] = core.ItemStack{Item: core.ItemStone, Count: 1}

	got, ok := h.Add(core.ItemDirt)
	if !ok {
		t.Fatal("存在空栏位时 Add 应当成功")
	}
	if got.Slots[1] != (core.ItemStack{Item: core.ItemDirt, Count: 1}) {
		t.Fatalf("栏位 1 = %+v，想要 1 个泥土", got.Slots[1])
	}
	if got.Slots[5] != (core.ItemStack{}) {
		t.Fatalf("栏位 5 = %+v，应当保持为空", got.Slots[5])
	}
}

func TestHotbarAddSkipsFullSameItemStack(t *testing.T) {
	var h core.Hotbar
	h.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}

	got, ok := h.Add(core.ItemDirt)
	if !ok {
		t.Fatal("满栏位之后仍有空栏位时应当成功")
	}
	if got.Slots[0].Count != core.MaxStackCount {
		t.Fatalf("满栏位数量 = %d，不得超过 %d", got.Slots[0].Count, core.MaxStackCount)
	}
	if got.Slots[1] != (core.ItemStack{Item: core.ItemDirt, Count: 1}) {
		t.Fatalf("栏位 1 = %+v，想要 1 个泥土", got.Slots[1])
	}
}

func TestHotbarAddFailsWhenFull(t *testing.T) {
	var h core.Hotbar
	for i := range h.Slots {
		h.Slots[i] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	}

	got, ok := h.Add(core.ItemDirt)
	if ok {
		t.Fatal("没有空间时 Add 必须失败")
	}
	if got != h {
		t.Fatal("Add 失败时快捷栏必须保持不变")
	}
}

func TestHotbarAddRejectsUnknownItem(t *testing.T) {
	var h core.Hotbar
	for _, item := range []core.ItemID{core.ItemNone, core.ItemID(77)} {
		got, ok := h.Add(item)
		if ok {
			t.Fatalf("Add(%d) 必须失败", item)
		}
		if got != h {
			t.Fatalf("Add(%d) 失败时快捷栏必须保持不变", item)
		}
	}
}

func TestHotbarConsumeNormalizesEmptySlot(t *testing.T) {
	var h core.Hotbar
	h.Slots[4] = core.ItemStack{Item: core.ItemDirt, Count: 1}

	got, ok := h.Consume(4)
	if !ok {
		t.Fatal("非空栏位应当可以消耗")
	}
	if got.Slots[4] != (core.ItemStack{}) {
		t.Fatalf("栏位 4 = %+v，想要规范空栏位", got.Slots[4])
	}
	if h.Slots[4].Count != 1 {
		t.Fatal("Consume 必须在值副本上完成，原值不得被修改")
	}
}

func TestHotbarConsumeDecrementsCount(t *testing.T) {
	var h core.Hotbar
	h.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 2}

	got, ok := h.Consume(1)
	if !ok {
		t.Fatal("非空栏位应当可以消耗")
	}
	if got.Slots[1] != (core.ItemStack{Item: core.ItemStone, Count: 1}) {
		t.Fatalf("栏位 1 = %+v，想要 1 个石头", got.Slots[1])
	}
}

func TestHotbarConsumeRejectsEmptyOrInvalidSlot(t *testing.T) {
	var h core.Hotbar
	h.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}

	for _, slot := range []uint8{1, core.HotbarSlots, 255} {
		got, ok := h.Consume(slot)
		if ok {
			t.Fatalf("Consume(%d) 必须失败", slot)
		}
		if got != h {
			t.Fatalf("Consume(%d) 失败时快捷栏必须保持不变", slot)
		}
	}
}

func TestBlockDropMapping(t *testing.T) {
	cases := []struct {
		block core.BlockID
		item  core.ItemID
		ok    bool
	}{
		{core.StoneID, core.ItemStone, true},
		{core.DirtID, core.ItemDirt, true},
		{core.GrassID, core.ItemGrass, true},
		{core.AirID, core.ItemNone, false},
		{core.BarrierID, core.ItemNone, false},
		{core.BedrockID, core.ItemNone, false},
		{core.BlockID(4242), core.ItemNone, false},
	}
	for _, tc := range cases {
		item, ok := core.BlockDrop(tc.block)
		if ok != tc.ok || item != tc.item {
			t.Fatalf("BlockDrop(%d) = (%d, %v)，想要 (%d, %v)", tc.block, item, ok, tc.item, tc.ok)
		}
	}
}

func TestItemPlacementMapping(t *testing.T) {
	cases := []struct {
		item  core.ItemID
		block core.BlockID
		ok    bool
	}{
		{core.ItemStone, core.StoneID, true},
		{core.ItemDirt, core.DirtID, true},
		{core.ItemGrass, core.GrassID, true},
		{core.ItemNone, core.AirID, false},
		{core.ItemID(4242), core.AirID, false},
	}
	for _, tc := range cases {
		block, ok := core.ItemPlacement(tc.item)
		if ok != tc.ok || block != tc.block {
			t.Fatalf("ItemPlacement(%d) = (%d, %v)，想要 (%d, %v)", tc.item, block, ok, tc.block, tc.ok)
		}
	}
}

func TestBlockDropAndItemPlacementRoundTrip(t *testing.T) {
	for _, block := range []core.BlockID{core.StoneID, core.DirtID, core.GrassID} {
		item, ok := core.BlockDrop(block)
		if !ok {
			t.Fatalf("BlockDrop(%d) 应当有掉落物", block)
		}
		got, ok := core.ItemPlacement(item)
		if !ok || got != block {
			t.Fatalf("ItemPlacement(BlockDrop(%d)) = (%d, %v)，想要 (%d, true)", block, got, ok, block)
		}
	}
}

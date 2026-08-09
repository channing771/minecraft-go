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

func TestCommonBlockMaterialsAreFixedAndRoundTrip(t *testing.T) {
	tests := []struct {
		block core.BlockID
		item  core.ItemID
	}{
		{core.CobblestoneID, core.ItemCobblestone},
		{core.SmoothStoneID, core.ItemSmoothStone},
		{core.SandID, core.ItemSand},
		{core.GravelID, core.ItemGravel},
		{core.OakLogID, core.ItemOakLog},
		{core.OakPlanksID, core.ItemOakPlanks},
		{core.LeavesID, core.ItemLeaves},
		{core.GlassID, core.ItemGlass},
		{core.BrickID, core.ItemBrick},
		{core.WhiteWoolID, core.ItemWhiteWool},
		{core.RoofTileID, core.ItemRoofTile},
		{core.ClayID, core.ItemClay},
		{core.SnowBlockID, core.ItemSnowBlock},
		{core.MossyCobblestoneID, core.ItemMossyCobblestone},
	}
	for i, tc := range tests {
		if tc.block != core.ChestID+1+core.BlockID(i) || tc.item != core.ItemChest+1+core.ItemID(i) {
			t.Fatalf("材料 %d 的稳定编号不连续: block=%d item=%d", i, tc.block, tc.item)
		}
		if !core.RegisteredBlock(tc.block) || !core.RegisteredItem(tc.item) {
			t.Fatalf("材料 %d 未注册", i)
		}
		if got, ok := core.ItemPlacement(tc.item); !ok || got != tc.block {
			t.Fatalf("ItemPlacement(%d)=(%d,%v)，想要 (%d,true)", tc.item, got, ok, tc.block)
		}
		if got, ok := core.BlockDrop(tc.block); !ok || got != tc.item {
			t.Fatalf("BlockDrop(%d)=(%d,%v)，想要 (%d,true)", tc.block, got, ok, tc.item)
		}
		if limit, ok := core.ItemStackLimit(tc.item); !ok || limit != 64 {
			t.Fatalf("ItemStackLimit(%d)=(%d,%v)，想要 (64,true)", tc.item, limit, ok)
		}
	}
	if core.RegisteredBlock(core.MossyCobblestoneID + 1) {
		t.Fatal("未知方块被注册")
	}
}

func TestToolIDsAndStackLimitsAreStable(t *testing.T) {
	if core.ItemStonePickaxe != 10 || core.ItemIronPickaxe != 11 {
		t.Fatalf("工具 ID 发生变化: stone=%d iron=%d", core.ItemStonePickaxe, core.ItemIronPickaxe)
	}
	tests := []struct {
		item core.ItemID
		want uint8
		ok   bool
	}{
		{core.ItemStone, 64, true},
		{core.ItemIronBlock, 64, true},
		{core.ItemStonePickaxe, 1, true},
		{core.ItemIronPickaxe, 1, true},
		{core.ItemNone, 0, false},
		{core.ItemID(4242), 0, false},
	}
	for _, test := range tests {
		got, ok := core.ItemStackLimit(test.item)
		if got != test.want || ok != test.ok {
			t.Errorf("ItemStackLimit(%d)=(%d,%v)，想要 (%d,%v)",
				test.item, got, ok, test.want, test.ok)
		}
	}
	for _, tool := range []core.ItemID{core.ItemStonePickaxe, core.ItemIronPickaxe} {
		full, _ := core.ItemMaxDurability(tool)
		if !(core.ItemStack{Item: tool, Count: 1, Durability: full}).Valid() {
			t.Errorf("单个工具 %d 应当有效", tool)
		}
		if (core.ItemStack{Item: tool, Count: 2, Durability: full}).Valid() {
			t.Errorf("两个工具 %d 必须无效", tool)
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

func TestHotbarAddRejectsToolsWithoutDurability(t *testing.T) {
	var hotbar core.Hotbar
	for _, item := range []core.ItemID{core.ItemStonePickaxe, core.ItemIronPickaxe} {
		got, ok := hotbar.Add(item)
		if ok || got != hotbar {
			t.Fatalf("Add(%d) = %+v, %v，没有耐久参数时必须拒绝工具", item, got, ok)
		}
	}

	got, ok := hotbar.Add(core.ItemDirt)
	if !ok || got.Slots[0] != (core.ItemStack{Item: core.ItemDirt, Count: 1}) {
		t.Fatalf("普通物品 Add = %+v, %v", got, ok)
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

func TestItemMaxDurabilityCoversToolsOnly(t *testing.T) {
	for _, test := range []struct {
		item core.ItemID
		want uint16
	}{
		{core.ItemStonePickaxe, 131},
		{core.ItemIronPickaxe, 250},
	} {
		got, ok := core.ItemMaxDurability(test.item)
		if !ok || got != test.want {
			t.Fatalf("物品 %d 耐久上限 = (%d,%v)，想要 (%d,true)", test.item, got, ok, test.want)
		}
	}
	// 非工具、损坏物品与未注册物品都没有耐久上限。
	for _, item := range []core.ItemID{
		core.ItemStone, core.ItemCoal, core.ItemIronIngot,
		core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
		core.ItemNone,
	} {
		if got, ok := core.ItemMaxDurability(item); ok || got != 0 {
			t.Fatalf("物品 %d 耐久上限 = (%d,%v)，想要 (0,false)", item, got, ok)
		}
	}
}

func TestItemBrokenFormMapsEachTool(t *testing.T) {
	for _, test := range []struct {
		item core.ItemID
		want core.ItemID
	}{
		{core.ItemStonePickaxe, core.ItemBrokenStonePickaxe},
		{core.ItemIronPickaxe, core.ItemBrokenIronPickaxe},
	} {
		got, ok := core.ItemBrokenForm(test.item)
		if !ok || got != test.want {
			t.Fatalf("物品 %d 损坏形态 = (%d,%v)，想要 (%d,true)", test.item, got, ok, test.want)
		}
	}
	// 损坏物品不会再损坏一次。
	for _, item := range []core.ItemID{
		core.ItemStone, core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
	} {
		if got, ok := core.ItemBrokenForm(item); ok || got != core.ItemNone {
			t.Fatalf("物品 %d 损坏形态 = (%d,%v)，想要 (ItemNone,false)", item, got, ok)
		}
	}
}

func TestBrokenToolsAreRegisteredAndUnstackable(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe,
	} {
		if !core.RegisteredItem(item) {
			t.Fatalf("损坏物品 %d 未注册", item)
		}
		if limit, ok := core.ItemStackLimit(item); !ok || limit != 1 {
			t.Fatalf("损坏物品 %d 单格上限 = (%d,%v)，想要 (1,true)", item, limit, ok)
		}
	}
}

func TestItemStackValidEnforcesDurabilityDomain(t *testing.T) {
	// 有耐久上限的物品：耐久必须落在 1..上限。
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for _, test := range []struct {
		name  string
		stack core.ItemStack
		want  bool
	}{
		{"满耐久工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}, true},
		{"半耐久工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 1}, true},
		{"零耐久工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 0}, false},
		{"超上限工具", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full + 1}, false},
		{"非工具带耐久", core.ItemStack{Item: core.ItemStone, Count: 1, Durability: 1}, false},
		{"非工具零耐久", core.ItemStack{Item: core.ItemStone, Count: 1}, true},
		{"损坏物品带耐久", core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1, Durability: 1}, false},
		{"损坏物品零耐久", core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1}, true},
		{"空栏位", core.ItemStack{}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.stack.Valid(); got != test.want {
				t.Fatalf("Valid() = %v，想要 %v", got, test.want)
			}
		})
	}
}

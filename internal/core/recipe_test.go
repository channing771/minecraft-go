package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

func TestStoneBrickIDsStayStable(t *testing.T) {
	if core.StoneBrickID != core.BedrockID+1 {
		t.Fatalf("StoneBrickID = %d，必须追加在既有方块 ID 之后", core.StoneBrickID)
	}
	if core.ItemStoneBrick != core.ItemGrass+1 {
		t.Fatalf("ItemStoneBrick = %d，必须追加在既有物品 ID 之后", core.ItemStoneBrick)
	}
	if core.RecipeStoneBricks != 1 {
		t.Fatalf("RecipeStoneBricks = %d，契约要求 1", core.RecipeStoneBricks)
	}
}

func TestStoneBrickIsPlaceableAndDrops(t *testing.T) {
	block, ok := core.ItemPlacement(core.ItemStoneBrick)
	if !ok || block != core.StoneBrickID {
		t.Fatalf("ItemPlacement(石砖) = (%d, %v)，想要 (%d, true)", block, ok, core.StoneBrickID)
	}
	item, ok := core.BlockDrop(core.StoneBrickID)
	if !ok || item != core.ItemStoneBrick {
		t.Fatalf("BlockDrop(石砖) = (%d, %v)，想要 (%d, true)", item, ok, core.ItemStoneBrick)
	}
}

func TestRecipeStoneBricksIsFixed(t *testing.T) {
	recipe, ok := core.Recipe(core.RecipeStoneBricks)
	if !ok {
		t.Fatal("石砖配方不可用")
	}
	if recipe.Input != (core.ItemStack{Item: core.ItemStone, Count: 4}) {
		t.Fatalf("配方输入 = %+v，想要 4 个石头", recipe.Input)
	}
	if recipe.Output != (core.ItemStack{Item: core.ItemStoneBrick, Count: 4}) {
		t.Fatalf("配方输出 = %+v，想要 4 个石砖", recipe.Output)
	}
	if _, ok := core.Recipe(0); ok {
		t.Fatal("recipe ID 0 被接受")
	}
	if _, ok := core.Recipe(core.RecipeID(200)); ok {
		t.Fatal("未知 recipe ID 被接受")
	}
}

func TestCraftConsumesLowestSlotsAcrossHotbarAndBackpack(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemStone, Count: 1}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemStone, Count: 3}
	inventory.Backpack[5] = core.ItemStack{Item: core.ItemStone, Count: 9}

	next, ok := inventory.Craft(core.RecipeStoneBricks)
	if !ok {
		t.Fatal("原料充足时合成失败")
	}
	if next.Hotbar.Slots[2] != (core.ItemStack{}) {
		t.Fatalf("最低索引原料格未清空: %+v", next.Hotbar.Slots[2])
	}
	if next.Backpack[0] != (core.ItemStack{}) {
		t.Fatalf("次低索引原料格未清空: %+v", next.Backpack[0])
	}
	if next.Backpack[5].Count != 9 {
		t.Fatalf("多余原料被扣除: %+v", next.Backpack[5])
	}
	// 扣料释放出的最低空格接收产物。
	if next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemStoneBrick, Count: 4}) {
		t.Fatalf("产物落点 = %+v，想要栏位 0 得到 4 个石砖", next.Hotbar.Slots[0])
	}
	if inventory.Hotbar.Slots[2].Count != 1 {
		t.Fatal("Craft 必须在值副本上完成")
	}
}

func TestCraftMergesOutputIntoExistingStack(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStoneBrick, Count: 60}

	next, ok := inventory.Craft(core.RecipeStoneBricks)
	if !ok {
		t.Fatal("合成失败")
	}
	if next.Hotbar.Slots[1].Count != core.MaxStackCount {
		t.Fatalf("产物未优先合并到同类格: %+v", next.Hotbar.Slots[1])
	}
}

func TestCraftRejectsWithoutMutating(t *testing.T) {
	insufficient := core.Inventory{}
	insufficient.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 3}

	// 原料刚好，但扣料后仍无处安放产物。
	noRoom := core.Inventory{}
	for slot := range noRoom.Hotbar.Slots {
		noRoom.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	for slot := range noRoom.Backpack {
		noRoom.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	noRoom.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}

	invalid := core.Inventory{Hotbar: core.Hotbar{Selected: core.HotbarSlots}}
	invalid.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 8}

	cases := []struct {
		name      string
		inventory core.Inventory
		recipe    core.RecipeID
	}{
		{"原料不足", insufficient, core.RecipeStoneBricks},
		{"产物无容量", noRoom, core.RecipeStoneBricks},
		{"非法物品状态", invalid, core.RecipeStoneBricks},
		{"未知配方", insufficient, core.RecipeID(200)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, ok := tc.inventory.Craft(tc.recipe)
			if ok {
				t.Fatalf("非法请求被接受: %+v", next)
			}
			if next != tc.inventory {
				t.Fatalf("失败的合成修改了原值: %+v", next)
			}
		})
	}
}

func TestCraftKeepsFullStackWhenOutputMergesBack(t *testing.T) {
	// 唯一空间来自被扣光的原料格：产物必须能放回该格。
	var inventory core.Inventory
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemStone, Count: 4}

	next, ok := inventory.Craft(core.RecipeStoneBricks)
	if !ok {
		t.Fatal("扣料后应当有空间接收产物")
	}
	if next.Hotbar.Slots[4] != (core.ItemStack{Item: core.ItemStoneBrick, Count: 4}) {
		t.Fatalf("产物未放回释放出的格: %+v", next.Hotbar.Slots[4])
	}
}

func BenchmarkInventoryCraftWorstCase(b *testing.B) {
	var inventory core.Inventory
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	// 原料分散在最高索引，产物只能落在扣空的格里。
	inventory.Backpack[core.BackpackSlots-1] = core.ItemStack{Item: core.ItemStone, Count: 4}
	b.ReportAllocs()
	for b.Loop() {
		inventory.Craft(core.RecipeStoneBricks)
	}
}

func TestM4EResourceIDsAreStable(t *testing.T) {
	if core.CoalOreID != 7 || core.IronOreID != 8 ||
		core.FurnaceID != 9 || core.IronBlockID != 10 {
		t.Fatal("M4E 方块 ID 漂移")
	}
	if core.ItemCoal != 5 || core.ItemRawIron != 6 || core.ItemIronIngot != 7 ||
		core.ItemFurnace != 8 || core.ItemIronBlock != 9 {
		t.Fatal("M4E 物品 ID 漂移")
	}
}

func TestRegisteredItemSeparatesValidityFromPlacement(t *testing.T) {
	registered := []core.ItemID{
		core.ItemStone, core.ItemDirt, core.ItemGrass, core.ItemStoneBrick,
		core.ItemCoal, core.ItemRawIron, core.ItemIronIngot,
		core.ItemFurnace, core.ItemIronBlock,
	}
	for _, item := range registered {
		if !core.RegisteredItem(item) {
			t.Fatalf("物品 %d 未被登记为合法", item)
		}
	}
	if core.RegisteredItem(core.ItemNone) || core.RegisteredItem(core.ItemID(4242)) {
		t.Fatal("空物品或未知物品被登记为合法")
	}
	// 煤炭、粗铁、铁锭合法但没有放置映射。
	for _, item := range []core.ItemID{core.ItemCoal, core.ItemRawIron, core.ItemIronIngot} {
		if _, ok := core.ItemPlacement(item); ok {
			t.Fatalf("物品 %d 不应可放置", item)
		}
	}
	for _, tc := range []struct {
		item  core.ItemID
		block core.BlockID
	}{
		{core.ItemFurnace, core.FurnaceID},
		{core.ItemIronBlock, core.IronBlockID},
	} {
		if block, ok := core.ItemPlacement(tc.item); !ok || block != tc.block {
			t.Fatalf("物品 %d 放置映射 = (%d, %v)", tc.item, block, ok)
		}
	}
}

func TestM4EBlockDrops(t *testing.T) {
	for _, tc := range []struct {
		block core.BlockID
		item  core.ItemID
	}{
		{core.CoalOreID, core.ItemCoal},
		{core.IronOreID, core.ItemRawIron},
		{core.FurnaceID, core.ItemFurnace},
		{core.IronBlockID, core.ItemIronBlock},
	} {
		item, ok := core.BlockDrop(tc.block)
		if !ok || item != tc.item {
			t.Fatalf("方块 %d 掉落 = (%d, %v)，想要 %d", tc.block, item, ok, tc.item)
		}
	}
}

func TestFurnaceAndIronBlockRecipes(t *testing.T) {
	for _, tc := range []struct {
		id     core.RecipeID
		input  core.ItemStack
		output core.ItemStack
	}{
		{core.RecipeFurnace, core.ItemStack{Item: core.ItemStone, Count: 8},
			core.ItemStack{Item: core.ItemFurnace, Count: 1}},
		{core.RecipeIronBlock, core.ItemStack{Item: core.ItemIronIngot, Count: 9},
			core.ItemStack{Item: core.ItemIronBlock, Count: 1}},
	} {
		recipe, ok := core.Recipe(tc.id)
		if !ok || recipe.Input != tc.input || recipe.Output != tc.output {
			t.Fatalf("配方 %d = %+v, %v", tc.id, recipe, ok)
		}
	}
	if core.RecipeFurnace != 2 || core.RecipeIronBlock != 3 {
		t.Fatal("M4E 配方 ID 漂移")
	}
}

func TestCraftFurnaceConsumesLowestSlots(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 5}
	inventory.Backpack[2] = core.ItemStack{Item: core.ItemStone, Count: 5}

	next, ok := inventory.Craft(core.RecipeFurnace)
	if !ok {
		t.Fatal("石头充足时熔炉合成失败")
	}
	if next.Hotbar.Slots[1] != (core.ItemStack{}) {
		t.Fatalf("最低索引原料格未清空: %+v", next.Hotbar.Slots[1])
	}
	if next.Backpack[2].Count != 2 {
		t.Fatalf("次低索引扣料 = %+v，想要剩 2", next.Backpack[2])
	}
	if next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemFurnace, Count: 1}) {
		t.Fatalf("产物落点 = %+v", next.Hotbar.Slots[0])
	}
}

func TestCraftIronBlockRejectsWithoutMutating(t *testing.T) {
	var short core.Inventory
	short.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemIronIngot, Count: 8}
	if next, ok := short.Craft(core.RecipeIronBlock); ok || next != short {
		t.Fatalf("铁锭不足仍合成: ok=%v", ok)
	}

	// 铁锭刚好 9 个但其余格全满，扣空的格必须能接收产物。
	full := core.Inventory{}
	for slot := range full.Hotbar.Slots {
		full.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	for slot := range full.Backpack {
		full.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	full.Backpack[7] = core.ItemStack{Item: core.ItemIronIngot, Count: 9}
	next, ok := full.Craft(core.RecipeIronBlock)
	if !ok {
		t.Fatal("扣空的格应当能接收铁块")
	}
	if next.Backpack[7] != (core.ItemStack{Item: core.ItemIronBlock, Count: 1}) {
		t.Fatalf("产物未放回释放出的格: %+v", next.Backpack[7])
	}
}

func TestToolRecipesAreFixedAndAtomic(t *testing.T) {
	if core.RecipeStonePickaxe != 4 || core.RecipeIronPickaxe != 5 {
		t.Fatalf("工具配方 ID 发生变化: stone=%d iron=%d", core.RecipeStonePickaxe, core.RecipeIronPickaxe)
	}
	for _, tc := range []struct {
		name   string
		id     core.RecipeID
		input  core.ItemID
		output core.ItemID
		full   uint16
	}{
		{"石镐", core.RecipeStonePickaxe, core.ItemStone, core.ItemStonePickaxe, 131},
		{"铁镐", core.RecipeIronPickaxe, core.ItemIronIngot, core.ItemIronPickaxe, 250},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantOutput := core.ItemStack{Item: tc.output, Count: 1, Durability: tc.full}
			recipe, ok := core.Recipe(tc.id)
			if !ok || recipe.Input != (core.ItemStack{Item: tc.input, Count: 3}) ||
				recipe.Output != wantOutput {
				t.Fatalf("配方 = %+v, %v", recipe, ok)
			}

			var inventory core.Inventory
			worn := core.ItemStack{Item: tc.output, Count: 1, Durability: tc.full - 1}
			inventory.Hotbar.Slots[0] = worn
			inventory.Hotbar.Slots[2] = core.ItemStack{Item: tc.input, Count: 1}
			inventory.Backpack[0] = core.ItemStack{Item: tc.input, Count: 2}
			next, ok := inventory.Craft(tc.id)
			if !ok {
				t.Fatal("跨栏位的三份原料应当可以合成")
			}
			if next.Hotbar.Slots[2] != (core.ItemStack{}) || next.Backpack[0] != (core.ItemStack{}) {
				t.Fatalf("原料未被跨栏位扣除: %+v / %+v", next.Hotbar.Slots[2], next.Backpack[0])
			}
			if next.Hotbar.Slots[0] != worn {
				t.Fatalf("旧工具 = %+v，想要耐久保持 %+v", next.Hotbar.Slots[0], worn)
			}
			if next.Hotbar.Slots[1] != wantOutput {
				t.Fatalf("新产物 = %+v，想要满耐久工具 %+v", next.Hotbar.Slots[1], wantOutput)
			}
		})
	}

	full := core.Inventory{}
	for slot := range full.Hotbar.Slots {
		full.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	for slot := range full.Backpack {
		full.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	full.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	next, ok := full.Craft(core.RecipeStonePickaxe)
	if ok || next != full {
		t.Fatalf("36 格都被占用时合成必须原子失败: %+v, %v", next, ok)
	}
}

func TestPickaxeRecipesOutputFullDurability(t *testing.T) {
	for _, test := range []struct {
		id   core.RecipeID
		want core.ItemStack
	}{
		{core.RecipeStonePickaxe, core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: 131}},
		{core.RecipeIronPickaxe, core.ItemStack{Item: core.ItemIronPickaxe, Count: 1, Durability: 250}},
	} {
		recipe, ok := core.Recipe(test.id)
		if !ok || recipe.Output != test.want || !recipe.Output.Valid() {
			t.Fatalf("Recipe(%d) 产物 = %+v, %v，想要合法满耐久工具 %+v", test.id, recipe.Output, ok, test.want)
		}
	}
}

func TestNonToolRecipesOutputZeroDurability(t *testing.T) {
	for _, id := range []core.RecipeID{
		core.RecipeStoneBricks,
		core.RecipeFurnace,
		core.RecipeIronBlock,
	} {
		recipe, ok := core.Recipe(id)
		if !ok || recipe.Output.Durability != 0 {
			t.Fatalf("Recipe(%d) 产物 = %+v, %v，非工具耐久必须为 0", id, recipe.Output, ok)
		}
	}
}

func TestRecipeOakPlanksIsFixed(t *testing.T) {
	if core.RecipeOakPlanks != core.RecipeChest+1 {
		t.Fatalf("RecipeOakPlanks = %d，必须紧随 RecipeChest(%d)",
			core.RecipeOakPlanks, core.RecipeChest)
	}
	if core.RecipeOakPlanks != 7 {
		t.Fatalf("RecipeOakPlanks = %d，必须稳定为 7", core.RecipeOakPlanks)
	}
	recipe, ok := core.Recipe(core.RecipeOakPlanks)
	if !ok || recipe.Input != (core.ItemStack{Item: core.ItemOakLog, Count: 1}) ||
		recipe.Output != (core.ItemStack{Item: core.ItemOakPlanks, Count: 4}) {
		t.Fatalf("橡木木板配方 = %+v, %v", recipe, ok)
	}
	if _, ok := core.Recipe(core.RecipeID(8)); ok {
		t.Fatal("未知 recipe ID 8 被接受")
	}
}

func TestCraftOakPlanksIsAtomic(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemOakLog, Count: 1}

	next, ok := inventory.Craft(core.RecipeOakPlanks)
	if !ok {
		t.Fatal("原木充足时木板合成失败")
	}
	if next.Hotbar.Slots[2] != (core.ItemStack{}) ||
		next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemOakPlanks, Count: 4}) {
		t.Fatalf("木板合成结果 = %+v", next)
	}
	if inventory.Hotbar.Slots[2].Count != 1 {
		t.Fatal("Craft 修改了原物品状态")
	}

	insufficient := core.Inventory{}
	if got, ok := insufficient.Craft(core.RecipeOakPlanks); ok || got != insufficient {
		t.Fatalf("原料不足时木板合成未原子拒绝: %+v, %v", got, ok)
	}

	full := core.Inventory{}
	for slot := range full.Hotbar.Slots {
		full.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	for slot := range full.Backpack {
		full.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	full.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemOakLog, Count: 2}
	if got, ok := full.Craft(core.RecipeOakPlanks); ok || got != full {
		t.Fatalf("产物无容量时木板合成未原子拒绝: %+v, %v", got, ok)
	}
}

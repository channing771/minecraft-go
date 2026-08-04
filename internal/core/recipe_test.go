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

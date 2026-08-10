package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

// TestChestIDsAppendAtEnd 锁定箱子的三个稳定编号都追加在既有编号末尾，
// 插入到中间会平移后续 ID 并破坏既有存档与线上字节。
func TestChestIDsAppendAtEnd(t *testing.T) {
	if core.ChestID != core.IronBlockID+1 {
		t.Fatalf("ChestID = %d，必须紧随 IronBlockID(%d) 之后", core.ChestID, core.IronBlockID)
	}
	if core.ItemChest != core.ItemBrokenIronPickaxe+1 {
		t.Fatalf("ItemChest = %d，必须紧随 ItemBrokenIronPickaxe(%d) 之后", core.ItemChest, core.ItemBrokenIronPickaxe)
	}
	if core.RecipeChest != core.RecipeIronPickaxe+1 {
		t.Fatalf("RecipeChest = %d，必须紧随 RecipeIronPickaxe(%d) 之后", core.RecipeChest, core.RecipeIronPickaxe)
	}
	if core.RecipeOakPlanks != core.RecipeChest+1 {
		t.Fatalf("RecipeOakPlanks = %d，必须追加在 RecipeChest(%d) 之后", core.RecipeOakPlanks, core.RecipeChest)
	}
}

// TestChestIsPlaceableAndDrops 覆盖箱子物品放置后写入 ChestID，
// 破坏箱子方块掉落箱子本身。
func TestChestIsPlaceableAndDrops(t *testing.T) {
	block, ok := core.ItemPlacement(core.ItemChest)
	if !ok || block != core.ChestID {
		t.Fatalf("ItemPlacement(箱子) = (%d, %v)，想要 (%d, true)", block, ok, core.ChestID)
	}
	item, ok := core.BlockDrop(core.ChestID)
	if !ok || item != core.ItemChest {
		t.Fatalf("BlockDrop(箱子) = (%d, %v)，想要 (%d, true)", item, ok, core.ItemChest)
	}
}

// TestRegisteredItemAcceptsChestStack 覆盖箱子是没有耐久的可堆叠方块物品，
// 单格上限与其余方块物品一致为 MaxStackCount。
func TestRegisteredItemAcceptsChestStack(t *testing.T) {
	if !core.RegisteredItem(core.ItemChest) {
		t.Fatal("箱子物品未被登记为合法")
	}
	limit, ok := core.ItemStackLimit(core.ItemChest)
	if !ok || limit != core.MaxStackCount {
		t.Fatalf("ItemStackLimit(箱子) = (%d, %v)，想要 (%d, true)", limit, ok, core.MaxStackCount)
	}
	if _, hasDurability := core.ItemMaxDurability(core.ItemChest); hasDurability {
		t.Fatal("箱子不应该有耐久上限")
	}
	stack := core.ItemStack{Item: core.ItemChest, Count: 1}
	if !stack.Valid() {
		t.Fatalf("满足堆叠规则的箱子物品栈被判定非法: %+v", stack)
	}
}

// TestRecipeChestIsFixed 锁定箱子配方的输入输出：8 个石头合成 1 个箱子。
func TestRecipeChestIsFixed(t *testing.T) {
	recipe, ok := core.Recipe(core.RecipeChest)
	if !ok {
		t.Fatal("箱子配方不可用")
	}
	if recipe.Input != (core.ItemStack{Item: core.ItemStone, Count: 8}) {
		t.Fatalf("配方输入 = %+v，想要 8 个石头", recipe.Input)
	}
	if recipe.Output != (core.ItemStack{Item: core.ItemChest, Count: 1}) {
		t.Fatalf("配方输出 = %+v，想要 1 个箱子", recipe.Output)
	}
}

// TestCraftChestConsumesLowestSlots 覆盖合成箱子按统一索引从低到高原子扣料，
// 与既有熔炉配方的验证方式一致。
func TestCraftChestConsumesLowestSlots(t *testing.T) {
	var inventory core.Inventory
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 5}
	inventory.Backpack[2] = core.ItemStack{Item: core.ItemStone, Count: 5}

	next, ok := inventory.Craft(core.RecipeChest)
	if !ok {
		t.Fatal("石头充足时箱子合成失败")
	}
	if next.Hotbar.Slots[1] != (core.ItemStack{}) {
		t.Fatalf("最低索引原料格未清空: %+v", next.Hotbar.Slots[1])
	}
	if next.Backpack[2].Count != 2 {
		t.Fatalf("次低索引扣料 = %+v，想要剩 2", next.Backpack[2])
	}
	if next.Hotbar.Slots[0] != (core.ItemStack{Item: core.ItemChest, Count: 1}) {
		t.Fatalf("产物落点 = %+v", next.Hotbar.Slots[0])
	}
}

// TestCraftChestRejectsWithoutMutatingOnFullInventory 覆盖原料充足但扣料后仍无处安放产物时，
// 合成必须原子失败且不修改原值：石头格保留多于所需数量，扣料后不会清空，
// 其余格全被无关物品占满，因此产物没有可用空格或同类格。
func TestCraftChestRejectsWithoutMutatingOnFullInventory(t *testing.T) {
	full := core.Inventory{}
	for slot := range full.Hotbar.Slots {
		full.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	for slot := range full.Backpack {
		full.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	}
	full.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 16}

	next, ok := full.Craft(core.RecipeChest)
	if ok || next != full {
		t.Fatalf("产物无容量时合成箱子必须原子失败: %+v, %v", next, ok)
	}
}

// TestCraftChestRejectsInsufficientStone 覆盖石头不足时合成失败且不修改原值。
func TestCraftChestRejectsInsufficientStone(t *testing.T) {
	short := core.Inventory{}
	short.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 7}
	if next, ok := short.Craft(core.RecipeChest); ok || next != short {
		t.Fatalf("石头不足仍合成: ok=%v", ok)
	}
}

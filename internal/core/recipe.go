package core

// RecipeID 是稳定的配方编号；0 保留为无效值。
type RecipeID uint8

const (
	// RecipeStoneBricks 用 4 个石头合成 4 个石砖。
	RecipeStoneBricks RecipeID = iota + 1
	// RecipeFurnace 用 8 个石头合成 1 个熔炉。
	RecipeFurnace
	// RecipeIronBlock 用 9 个铁锭合成 1 个铁块。
	RecipeIronBlock
	// RecipeStonePickaxe 用 3 个石头合成 1 个石镐。
	RecipeStonePickaxe
	// RecipeIronPickaxe 用 3 个铁锭合成 1 个铁镐。
	RecipeIronPickaxe
	// RecipeChest 用 8 个石头合成 1 个箱子。
	RecipeChest
)

// CraftingRecipe 是一条固定的单输入、单输出配方。
type CraftingRecipe struct {
	Input  ItemStack
	Output ItemStack
}

// Recipe 返回已注册配方；未知 ID 返回 false。
func Recipe(id RecipeID) (CraftingRecipe, bool) {
	switch id {
	case RecipeStoneBricks:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemStone, Count: 4},
			Output: ItemStack{Item: ItemStoneBrick, Count: 4},
		}, true
	case RecipeFurnace:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemStone, Count: 8},
			Output: ItemStack{Item: ItemFurnace, Count: 1},
		}, true
	case RecipeIronBlock:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemIronIngot, Count: 9},
			Output: ItemStack{Item: ItemIronBlock, Count: 1},
		}, true
	case RecipeStonePickaxe:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemStone, Count: 3},
			Output: ItemStack{Item: ItemStonePickaxe, Count: 1, Durability: 131},
		}, true
	case RecipeIronPickaxe:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemIronIngot, Count: 3},
			Output: ItemStack{Item: ItemIronPickaxe, Count: 1, Durability: 250},
		}, true
	case RecipeChest:
		return CraftingRecipe{
			Input:  ItemStack{Item: ItemStone, Count: 8},
			Output: ItemStack{Item: ItemChest, Count: 1},
		}, true
	default:
		return CraftingRecipe{}, false
	}
}

// Craft 在完整物品状态的副本上原子执行一次合成：
// 按统一索引从低到高扣除原料，再用现有 AddStack 规则放入完整产物。
// 配方未知、状态非法、原料不足或产物放不下时返回原值和 false。
func (inventory Inventory) Craft(id RecipeID) (Inventory, bool) {
	recipe, ok := Recipe(id)
	if !ok || !inventory.Valid() {
		return inventory, false
	}
	next := inventory
	remaining := recipe.Input.Count
	for slot := uint8(0); slot < InventorySlots && remaining > 0; slot++ {
		stack, _ := next.Slot(slot)
		if stack.Item != recipe.Input.Item {
			continue
		}
		taken := min(stack.Count, remaining)
		stack.Count -= taken
		remaining -= taken
		if stack.Count == 0 {
			stack = ItemStack{}
		}
		next.setSlot(slot, stack)
	}
	if remaining > 0 {
		return inventory, false
	}
	next, leftover := next.AddStack(recipe.Output)
	if leftover.Count > 0 {
		return inventory, false
	}
	return next, true
}

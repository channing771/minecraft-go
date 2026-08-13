package core_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// TestLightBlockIDsMappingsStayStable 杀死 ID 未追加、放置或掉落映射缺失、
// 堆叠上限错误的变异。
func TestLightBlockIDsMappingsStayStable(t *testing.T) {
	if core.LightBlockID != core.ChestID+1 {
		t.Fatalf("LightBlockID=%d，必须追加在 ChestID 之后", core.LightBlockID)
	}
	if core.ItemLightBlock != core.ItemChest+1 {
		t.Fatalf("ItemLightBlock=%d，必须追加在 ItemChest 之后", core.ItemLightBlock)
	}
	if core.ItemBrokenStonePickaxe != 12 || core.ItemBrokenIronPickaxe != 13 ||
		core.ItemChest != 14 || core.ItemLightBlock != 15 {
		t.Fatalf("物品 wire ID 漂移: brokenStone=%d brokenIron=%d chest=%d light=%d",
			core.ItemBrokenStonePickaxe, core.ItemBrokenIronPickaxe, core.ItemChest, core.ItemLightBlock)
	}
	if limit, ok := core.ItemStackLimit(core.ItemLightBlock); !ok || limit != 64 {
		t.Fatalf("发光块堆叠上限=(%d,%v)，想要 (64,true)", limit, ok)
	}
	if block, ok := core.ItemPlacement(core.ItemLightBlock); !ok || block != core.LightBlockID {
		t.Fatalf("发光块放置映射=(%d,%v)", block, ok)
	}
	if item, ok := core.BlockDrop(core.LightBlockID); !ok || item != core.ItemLightBlock {
		t.Fatalf("发光块掉落映射=(%d,%v)", item, ok)
	}
	if core.RecipeOakPlanks != 7 {
		t.Fatalf("RecipeOakPlanks=%d，想要 7", core.RecipeOakPlanks)
	}
	if core.RecipeLightBlock != 8 {
		t.Fatalf("RecipeLightBlock=%d，想要 8", core.RecipeLightBlock)
	}
	if core.RecipeLightBlock != core.RecipeOakPlanks+1 {
		t.Fatalf("RecipeLightBlock=%d，必须紧随 RecipeOakPlanks(%d)", core.RecipeLightBlock, core.RecipeOakPlanks)
	}
	if _, ok := core.Recipe(core.RecipeLightBlock); !ok {
		t.Fatal("发光方块配方未注册")
	}
	if _, ok := core.Recipe(core.RecipeLightBlock + 1); ok {
		t.Fatal("未知配方不得被接受")
	}
}

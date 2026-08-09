package core_test

import (
	"testing"

	"minecraft-go/internal/core"
)

// TestLightBlockIDsMappingsAndNoRecipeStayStable 杀死 ID 未追加、放置或掉落映射缺失、
// 堆叠上限错误，以及意外新增配方的变异。
func TestLightBlockIDsMappingsAndNoRecipeStayStable(t *testing.T) {
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
	if core.RecipeChest != 6 {
		t.Fatalf("最后一个固定配方 ID=%d，想要 6", core.RecipeChest)
	}
	if _, ok := core.Recipe(core.RecipeChest + 1); ok {
		t.Fatal("发光块不得增加配方")
	}
}

package hud

import (
	"testing"

	"minecraft-go/internal/core"
)

// 杀死变异：遗漏任一配方行、错放按钮或忽略已确认背包都会改变实例布局。
func TestInventoryLayoutDrawsAllFixedRecipeRows(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	if recipeQuads != 73 || recipeGlyphs != 20 {
		t.Fatalf("八行配方容量 quads/glyphs=%d/%d，想要 73/20", recipeQuads, recipeGlyphs)
	}
	if got := inventoryRecipeIDs[len(inventoryRecipeIDs)-1]; got != core.RecipeLightBlock {
		t.Fatalf("固定配方末项=%d，想要发光方块配方 %d", got, core.RecipeLightBlock)
	}

	open := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, nil, MiningOverlay{}, 1280, 720)
	// source=-1：四层背包分组面板加选中框，没有来源高亮；空背包没有物品色块。
	if len(open.quads) != openInventoryPanelQuads+1+core.InventorySlots+recipeQuads {
		t.Fatalf("空背包 quads=%d，想要分组面板、选中框、36 格和 %d 个配方实例共 %d",
			len(open.quads), recipeQuads, openInventoryPanelQuads+1+core.InventorySlots+recipeQuads)
	}
	if len(open.glyphs) != 20 {
		t.Fatalf("八条配方数字=%d，想要隐藏数量 1 并为其余数字绘制阴影共 20", len(open.glyphs))
	}
	overlay := open.quads[len(open.quads)-recipeQuads:]
	wantItems := [][2]core.ItemID{
		{core.ItemStone, core.ItemStoneBrick},
		{core.ItemStone, core.ItemFurnace},
		{core.ItemIronIngot, core.ItemIronBlock},
		{core.ItemStone, core.ItemStonePickaxe},
		{core.ItemIronIngot, core.ItemIronPickaxe},
		{core.ItemStone, core.ItemChest},
		{core.ItemOakLog, core.ItemOakPlanks},
		{core.ItemGlass, core.ItemLightBlock},
	}
	for row, y := range []float32{420, 368, 316, 264, 212, 160, 108, 56} {
		start := 1 + row*9
		input, output := overlay[start], overlay[start+3]
		inputFace, outputFace := overlay[start+2], overlay[start+5]
		if input.X != 408 || output.X != 460 || input.Y != y || output.Y != y {
			t.Fatalf("配方行 %d 位置错误: input=%+v output=%+v", row, input, output)
		}
		assertHotbarItemFace(t, inputFace, wantItems[row][0])
		assertHotbarItemFace(t, outputFace, wantItems[row][1])
	}
	disabled := hotbarRecipeButtonQuads(open)
	if len(disabled) != len(inventoryRecipeIDs) {
		t.Fatalf("配方按钮=%d，想要 %d", len(disabled), len(inventoryRecipeIDs))
	}

	var stone core.Inventory
	stone.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	stoneButtons := hotbarRecipeButtonQuads(layoutInventory(&layout, atlas, stone, true, -1, nil, nil, MiningOverlay{}, 1280, 720))
	if disabled[0].Color == stoneButtons[0].Color {
		t.Fatal("石砖可合成时按钮颜色未改变")
	}
	if disabled[1].Color != stoneButtons[1].Color || disabled[2].Color != stoneButtons[2].Color {
		t.Fatal("石砖原料错误启用了其他配方")
	}

	stone.Hotbar.Slots[0].Count = 8
	furnaceButtons := hotbarRecipeButtonQuads(layoutInventory(&layout, atlas, stone, true, -1, nil, nil, MiningOverlay{}, 1280, 720))
	if disabled[1].Color == furnaceButtons[1].Color || disabled[2].Color != furnaceButtons[2].Color {
		t.Fatal("熔炉配方可用颜色不独立")
	}
	stone.Hotbar.Slots[0].Count = 3
	stonePickaxeButtons := hotbarRecipeButtonQuads(layoutInventory(&layout, atlas, stone, true, -1, nil, nil, MiningOverlay{}, 1280, 720))
	if disabled[3].Color == stonePickaxeButtons[3].Color || disabled[4].Color != stonePickaxeButtons[4].Color {
		t.Fatal("石镐配方可用颜色不独立")
	}

	var iron core.Inventory
	iron.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemIronIngot, Count: 9}
	ironButtons := hotbarRecipeButtonQuads(layoutInventory(&layout, atlas, iron, true, -1, nil, nil, MiningOverlay{}, 1280, 720))
	if disabled[2].Color == ironButtons[2].Color || disabled[0].Color != ironButtons[0].Color ||
		disabled[1].Color != ironButtons[1].Color {
		t.Fatal("铁块配方可用颜色不独立")
	}
	iron.Hotbar.Slots[0].Count = 3
	ironPickaxeButtons := hotbarRecipeButtonQuads(layoutInventory(&layout, atlas, iron, true, -1, nil, nil, MiningOverlay{}, 1280, 720))
	if disabled[4].Color == ironPickaxeButtons[4].Color || disabled[3].Color != ironPickaxeButtons[3].Color {
		t.Fatal("铁镐配方可用颜色不独立")
	}

	var glass core.Inventory
	glass.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemGlass, Count: 4}
	glassButtons := hotbarRecipeButtonQuads(layoutInventory(&layout, atlas, glass, true, -1, nil, nil, MiningOverlay{}, 1280, 720))
	for index := range glassButtons {
		if index == len(glassButtons)-1 {
			if disabled[index].Color == glassButtons[index].Color {
				t.Fatal("四个玻璃未启用发光方块配方")
			}
			continue
		}
		if disabled[index].Color != glassButtons[index].Color {
			t.Fatalf("四个玻璃错误启用了配方 %d", inventoryRecipeIDs[index])
		}
	}
}
func TestRecipeButtonHitTestMatchesDrawnGeometry(t *testing.T) {
	for _, test := range []struct {
		name   string
		y      float64
		recipe core.RecipeID
	}{
		{"石砖", 421, core.RecipeStoneBricks},
		{"熔炉", 369, core.RecipeFurnace},
		{"铁块", 317, core.RecipeIronBlock},
		{"石镐", 265, core.RecipeStonePickaxe},
		{"铁镐", 213, core.RecipeIronPickaxe},
		{"箱子", 161, core.RecipeChest},
		{"橡木木板", 109, core.RecipeOakPlanks},
		{"发光方块", 57, core.RecipeLightBlock},
	} {
		got, ok := RecipeButtonAt(513, test.y, 1280, 720)
		if !ok || got != test.recipe {
			t.Fatalf("%s按钮命中 = %d, %v，想要 %d", test.name, got, ok, test.recipe)
		}
		if _, ok := InventorySlotAt(513, test.y, 1280, 720); ok {
			t.Fatalf("%s按钮与背包格重叠", test.name)
		}
	}
	if _, ok := RecipeButtonAt(511, 421, 1280, 720); ok {
		t.Fatal("按钮左侧 1 像素被判为命中")
	}
	if _, ok := RecipeButtonAt(513, 421, 0, 0); ok {
		t.Fatal("零尺寸 framebuffer 被判为命中")
	}
}

// 杀死变异：丢失熔炉格、放错进度条或忽略权威计时都会改变实例布局。
func TestFurnaceOverlayDrawsThreeSlotsAndTwoBars(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout

	empty := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, &FurnaceOverlay{}, nil, MiningOverlay{}, 1280, 720)
	// 空熔炉：面板、3 个栏位背景与 2 条进度条底，没有物品色块或填充。
	emptyQuads := len(empty.quads)
	if len(empty.glyphs) != 0 {
		t.Fatalf("空熔炉数字 = %d，想要 0", len(empty.glyphs))
	}

	full := layoutInventory(
		&layout, atlas, core.Inventory{}, true, -1, fullFurnaceOverlay(), nil, MiningOverlay{}, 1280, 720,
	)
	if len(full.quads) != emptyQuads+3*2+2 {
		t.Fatalf("满熔炉 quads = %d，想要比空熔炉多 3 个双层色块和 2 条填充", len(full.quads))
	}
	if len(full.glyphs) != 12 {
		t.Fatalf("满熔炉数字 = %d，想要三组两位数含阴影共 12", len(full.glyphs))
	}

	// 进度条宽度必须随权威计时按比例变化。
	half := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, &FurnaceOverlay{
		BurnTicks: core.FurnaceBurnTicks / 2,
	}, nil, MiningOverlay{}, 1280, 720)
	// 满布局末尾是 [燃烧底, 燃烧填充, 熔炼底, 熔炼填充]；
	// 半满布局的熔炼进度为 0 所以没有填充，末尾是 [燃烧底, 燃烧填充, 熔炼底]。
	fullBar := full.quads[len(full.quads)-3]
	halfBar := half.quads[len(half.quads)-2]
	if halfBar.Width >= fullBar.Width || halfBar.Width <= 0 {
		t.Fatalf("半满燃烧条宽度 = %f，满条 = %f", halfBar.Width, fullBar.Width)
	}
}
func TestFurnaceOverlayReplacesRecipeRow(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}

	recipe := layoutInventory(&layout, atlas, stocked, true, -1, nil, nil, MiningOverlay{}, 1280, 720)
	recipeButtons := len(hotbarRecipeButtonQuads(recipe))
	furnace := layoutInventory(&layout, atlas, stocked, true, -1, &FurnaceOverlay{}, nil, MiningOverlay{}, 1280, 720)
	if recipeButtons != len(inventoryRecipeIDs) || len(hotbarRecipeButtonQuads(furnace)) != 0 {
		t.Fatalf("配方视图按钮=%d，熔炉视图按钮=%d，想要 %d 和 0",
			recipeButtons, len(hotbarRecipeButtonQuads(furnace)), len(inventoryRecipeIDs))
	}
}
func TestFurnaceSlotAtCoversUnifiedIndices(t *testing.T) {
	width, height := uint32(1280), uint32(720)
	// 0..35 与背包命中一致。
	for _, slot := range []int{0, 8, 9, 35} {
		x, y := inventorySlotOrigin(slot, true, float32(width), float32(height))
		got, ok := FurnaceSlotAt(float64(x)+1, float64(y)+1, width, height)
		if !ok || int(got) != slot {
			t.Fatalf("统一索引 %d 命中 = %d, %v", slot, got, ok)
		}
	}
	// 36、37、38 落在熔炉三格上。
	for index := range 3 {
		x, y := recipeSlotOrigin(index, float32(width), float32(height))
		got, ok := FurnaceSlotAt(float64(x), float64(y), width, height)
		if !ok || got != core.InventorySlots+uint8(index) {
			t.Fatalf("熔炉格 %d 命中 = %d, %v", index, got, ok)
		}
		if _, ok := FurnaceSlotAt(
			float64(x+hotbarSlotSize), float64(y+hotbarSlotSize/2), width, height,
		); ok {
			t.Fatalf("熔炉格 %d 右边界外仍被命中", index)
		}
	}
	if _, ok := FurnaceSlotAt(0, 0, width, height); ok {
		t.Fatal("界外命中被接受")
	}
	if _, ok := FurnaceSlotAt(100, 100, 0, 0); ok {
		t.Fatal("零尺寸 framebuffer 被判为命中")
	}
}
func TestFurnaceSourceHighlightCoversFurnaceSlots(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var layout hotbarLayout
	for source := range core.FurnaceViewSlots {
		got := layoutInventory(
			&layout, atlas, core.Inventory{}, true, source,
			&FurnaceOverlay{}, nil, MiningOverlay{}, 1280, 720,
		)
		// 面板和当前选中框之后是来源高亮。
		highlight := got.quads[openInventoryPanelQuads+1]
		wantX, wantY := inventorySlotOrigin(source, true, 1280, 720)
		if source >= core.InventorySlots {
			wantX, wantY = recipeSlotOrigin(source-core.InventorySlots, 1280, 720)
		}
		if highlight.X != wantX-hotbarSelectBorder || highlight.Y != wantY-hotbarSelectBorder {
			t.Fatalf("来源 %d 高亮 = %+v，想要包住 (%f,%f)",
				source, highlight, wantX, wantY)
		}
	}
}

// 杀死变异：漏画任一格背景、错放物品或忽略空格都会改变实例布局。
func TestChestOverlayDraws27SlotsWithItemsAndCounts(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout

	empty := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, &ChestOverlay{}, MiningOverlay{}, 1280, 720)
	// 空箱子：27 个栏位背景，没有色块也没有数字。
	if len(empty.glyphs) != 0 {
		t.Fatalf("空箱子数字 = %d，想要 0", len(empty.glyphs))
	}
	emptyQuads := len(empty.quads)

	sparse := ChestOverlay{}
	sparse.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
	sparse.Items[13] = core.ItemStack{Item: core.ItemCoal, Count: 5}
	sparse.Items[26] = core.ItemStack{Item: core.ItemIronIngot, Count: 1}
	got := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, &sparse, MiningOverlay{}, 1280, 720)
	if len(got.quads) != emptyQuads+3*2 {
		t.Fatalf("三格占用 quads=%d，想要比空箱子多 3 个双层色块", len(got.quads))
	}
	if len(got.glyphs) != 6 {
		t.Fatalf("数字数量 = %d，想要 64/5 含阴影且隐藏 1，共 6 个实例", len(got.glyphs))
	}
	tiles := got.quads[emptyQuads:]
	wantItems := []core.ItemID{core.ItemStone, core.ItemCoal, core.ItemIronIngot}
	for index, item := range wantItems {
		face := tiles[index*2+1]
		assertHotbarItemFace(t, face, item)
	}

	full := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720)
	if len(full.quads) != emptyQuads+core.ChestSlots*2 {
		t.Fatalf("满箱子 quads = %d，想要比空箱子多 %d 个双层色块", len(full.quads), core.ChestSlots)
	}
	if len(full.glyphs) != 108 {
		t.Fatalf("满箱子数字 = %d，想要 27 组两位数含阴影共 108", len(full.glyphs))
	}
}
func TestChestOverlayReplacesRecipeRow(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	var stocked core.Inventory
	stocked.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}

	recipe := layoutInventory(&layout, atlas, stocked, true, -1, nil, nil, MiningOverlay{}, 1280, 720)
	recipeButtons := len(hotbarRecipeButtonQuads(recipe))
	chest := layoutInventory(&layout, atlas, stocked, true, -1, nil, &ChestOverlay{}, MiningOverlay{}, 1280, 720)
	if recipeButtons != len(inventoryRecipeIDs) || len(hotbarRecipeButtonQuads(chest)) != 0 {
		t.Fatalf("配方视图按钮=%d，箱子视图按钮=%d，想要 %d 和 0",
			recipeButtons, len(hotbarRecipeButtonQuads(chest)), len(inventoryRecipeIDs))
	}
}

// 杀死变异：熔炉与箱子叠加值理论上互斥，但函数必须有确定的优先级而不是 panic 或漏画。
func TestChestOverlayTakesPriorityOverFurnaceOverlay(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var layout hotbarLayout
	both := layoutInventory(
		&layout, atlas, core.Inventory{}, true, -1, &FurnaceOverlay{}, &ChestOverlay{}, MiningOverlay{}, 1280, 720,
	)
	chestOnly := layoutInventory(
		&layout, atlas, core.Inventory{}, true, -1, nil, &ChestOverlay{}, MiningOverlay{}, 1280, 720,
	)
	if len(both.quads) != len(chestOnly.quads) {
		t.Fatalf("两者都非 nil 时 quads=%d，想要与仅箱子相同 %d", len(both.quads), len(chestOnly.quads))
	}
}
func TestChestSlotAtCoversUnifiedIndices(t *testing.T) {
	width, height := uint32(1280), uint32(720)
	// 0..35 与背包命中一致。
	for _, slot := range []int{0, 8, 9, 35} {
		x, y := inventorySlotOrigin(slot, true, float32(width), float32(height))
		got, ok := ChestSlotAt(float64(x)+1, float64(y)+1, width, height)
		if !ok || int(got) != slot {
			t.Fatalf("统一索引 %d 命中 = %d, %v", slot, got, ok)
		}
	}
	// 36..62 落在箱子 27 格上。
	for index := range core.ChestSlots {
		x, y := chestSlotOrigin(index, float32(width), float32(height))
		got, ok := ChestSlotAt(float64(x), float64(y), width, height)
		if !ok || got != core.InventorySlots+uint8(index) {
			t.Fatalf("箱子格 %d 命中 = %d, %v", index, got, ok)
		}
		if _, ok := ChestSlotAt(
			float64(x+hotbarSlotSize), float64(y+hotbarSlotSize/2), width, height,
		); ok {
			t.Fatalf("箱子格 %d 右边界外仍被命中", index)
		}
	}
	if _, ok := ChestSlotAt(0, 0, width, height); ok {
		t.Fatal("界外命中被接受")
	}
	if _, ok := ChestSlotAt(100, 100, 0, 0); ok {
		t.Fatal("零尺寸 framebuffer 被判为命中")
	}
}
func TestChestSourceHighlightCoversChestSlots(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var layout hotbarLayout
	for source := range core.ChestViewSlots {
		got := layoutInventory(
			&layout, atlas, core.Inventory{}, true, source,
			nil, &ChestOverlay{}, MiningOverlay{}, 1280, 720,
		)
		// 面板和当前选中框之后是来源高亮。
		highlight := got.quads[openInventoryPanelQuads+1]
		wantX, wantY := inventorySlotOrigin(source, true, 1280, 720)
		if source >= core.InventorySlots {
			wantX, wantY = chestSlotOrigin(source-core.InventorySlots, 1280, 720)
		}
		if highlight.X != wantX-hotbarSelectBorder || highlight.Y != wantY-hotbarSelectBorder {
			t.Fatalf("来源 %d 高亮 = %+v，想要包住 (%f,%f)",
				source, highlight, wantX, wantY)
		}
	}
}

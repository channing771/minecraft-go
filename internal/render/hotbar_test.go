package render

import (
	"reflect"
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

// fullFurnaceOverlay 是熔炉视图的最坏布局：三格都有物品且两条进度条都非空。
func fullFurnaceOverlay() *FurnaceOverlay {
	return &FurnaceOverlay{
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: core.MaxStackCount},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: core.MaxStackCount},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: core.MaxStackCount},
		ProgressTicks: core.FurnaceSmeltTicks - 1,
		BurnTicks:     core.FurnaceBurnTicks,
	}
}

// fullChestOverlay 是箱子视图的最坏布局：27 格全部占用且都是两位数量。
func fullChestOverlay() *ChestOverlay {
	var overlay ChestOverlay
	items := [3]core.ItemID{core.ItemStone, core.ItemCoal, core.ItemIronIngot}
	for slot := range overlay.Items {
		overlay.Items[slot] = core.ItemStack{Item: items[slot%len(items)], Count: core.MaxStackCount}
	}
	return &overlay
}

func fullTestInventory() core.Inventory {
	var inventory core.Inventory
	inventory.Hotbar.Selected = 4
	items := [core.HotbarSlots]core.ItemID{
		core.ItemStone, core.ItemDirt, core.ItemGrass,
		core.ItemStone, core.ItemDirt, core.ItemGrass,
		core.ItemStone, core.ItemDirt, core.ItemGrass,
	}
	for slot, item := range items {
		inventory.Hotbar.Slots[slot] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	}
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = core.ItemStack{
			Item: items[slot%len(items)], Count: core.MaxStackCount,
		}
	}
	return inventory
}

// maxQuadTestInventory 是合法的 quad 上限见证：九格磨损工具各自数量为 1，
// 背包仍填满普通可堆叠物品。
func maxQuadTestInventory() core.Inventory {
	inventory := fullTestInventory()
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = core.ItemStack{
			Item: core.ItemStonePickaxe, Count: 1, Durability: full / 2,
		}
	}
	return inventory
}

// Mutation killed: dropping the selection frame, mislaying slots, or letting the
// layout depend on anything but framebuffer size and hotbar value changes the
// exact instance rectangles below.
func TestHotbarLayoutIsFixedNineSlotsWithSelection(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var inventory core.Inventory
	inventory.Hotbar.Selected = 2
	var layout hotbarLayout
	got := layoutInventory(&layout, atlas, inventory, false, -1, nil, nil, MiningOverlay{}, 800, 600)

	if len(got.quads) != 2+core.HotbarSlots {
		t.Fatalf("空物品状态 quads=%d，想要面板、选中框加 9 个栏位", len(got.quads))
	}
	if len(got.glyphs) != 0 {
		t.Fatalf("空快捷栏数字=%d，想要 0", len(got.glyphs))
	}

	total := core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap
	originX := (800 - total) * 0.5
	originY := 600 - hotbarBottomMargin - hotbarSlotSize
	for slot := range core.HotbarSlots {
		quad := got.quads[2+slot]
		wantX := originX + float32(slot)*(hotbarSlotSize+hotbarSlotGap)
		if quad.X != wantX || quad.Y != originY ||
			quad.Width != hotbarSlotSize || quad.Height != hotbarSlotSize {
			t.Fatalf("栏位 %d = %+v，想要 (%f,%f,%f,%f)",
				slot, quad, wantX, originY, hotbarSlotSize, hotbarSlotSize)
		}
	}
	selection := got.quads[1]
	wantSelectionX := originX + 2*(hotbarSlotSize+hotbarSlotGap) - hotbarSelectBorder
	if selection.X != wantSelectionX || selection.Y != originY-hotbarSelectBorder ||
		selection.Width != hotbarSlotSize+2*hotbarSelectBorder {
		t.Fatalf("选中框 = %+v，想要包住栏位 2", selection)
	}
}

// Mutation killed: swapping item colors, drawing swatches for empty slots, or
// emitting more than two digits per slot breaks the fixed HUD budget.
func TestHotbarLayoutDrawsItemSwatchesAndCounts(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
	inventory.Hotbar.Slots[3] = core.ItemStack{Item: core.ItemDirt, Count: 9}
	inventory.Hotbar.Slots[8] = core.ItemStack{Item: core.ItemGrass, Count: 1}

	var layout hotbarLayout
	got := layoutInventory(&layout, atlas, inventory, false, -1, nil, nil, MiningOverlay{}, 1280, 720)
	if len(got.quads) != 2+core.HotbarSlots+3*2 {
		t.Fatalf("quads=%d，想要面板、选中框、9 个栏位和 3 个双层色块", len(got.quads))
	}
	tiles := got.quads[2+core.HotbarSlots:]
	wantItems := []core.ItemID{core.ItemStone, core.ItemDirt, core.ItemGrass}
	for index, item := range wantItems {
		border, face := tiles[index*2], tiles[index*2+1]
		assertHotbarItemFace(t, face, item)
		if border.Width != hotbarSlotSize-2*hotbarSwatchInset ||
			face.Width != border.Width-2*hotbarSwatchBorder {
			t.Fatalf("色块 %d 尺寸 = %f/%f", index, border.Width, face.Width)
		}
	}
	if len(got.glyphs) != 6 {
		t.Fatalf("数字数量 = %d，想要 64/9 各含阴影且隐藏 1，共 6 个实例", len(got.glyphs))
	}
}

// 杀死变异：继续显示单件数量、漏掉阴影、错序或失去右下对齐都会改变这些实例。
func TestHotbarCountsHideOneAndUseShadowedBottomRightDigits(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	appendHotbarCount(&layout, atlas, 1, 100, 200)
	if len(layout.glyphs) != 0 {
		t.Fatalf("单件数量 glyphs=%d，想要隐藏冗余数字 1", len(layout.glyphs))
	}

	appendHotbarCount(&layout, atlas, 64, 100, 200)
	if len(layout.glyphs) != 4 {
		t.Fatalf("数量 64 glyphs=%d，想要两个阴影加两个前景", len(layout.glyphs))
	}
	want := []hotbarInstance{
		{X: 134, Y: 236, Width: 8, Height: 12, U0: 0.1, V0: 0.2, U1: 0.3, V1: 0.4, Color: [4]float32{0.02, 0.025, 0.03, 0.95}},
		{X: 139, Y: 236, Width: 8, Height: 12, U0: 0.1, V0: 0.2, U1: 0.3, V1: 0.4, Color: [4]float32{0.02, 0.025, 0.03, 0.95}},
		{X: 133, Y: 235, Width: 8, Height: 12, U0: 0.1, V0: 0.2, U1: 0.3, V1: 0.4, Color: [4]float32{1, 0.94, 0.78, 1}},
		{X: 138, Y: 235, Width: 8, Height: 12, U0: 0.1, V0: 0.2, U1: 0.3, V1: 0.4, Color: [4]float32{1, 0.94, 0.78, 1}},
	}
	if !reflect.DeepEqual(layout.glyphs, want) {
		t.Fatalf("数量 64 glyphs=%+v，想要右下阴影/前景 %+v", layout.glyphs, want)
	}

	layout.glyphs = layout.glyphs[:0]
	appendHotbarCountScaled(&layout, atlas, 64, 100, 200, 0.5)
	if first, second := layout.glyphs[2], layout.glyphs[3]; first.X != 116.5 || second.X != 119 {
		t.Fatalf("0.5x 两位前景 X=%v/%v，想要 tracking 同步缩放且右边缘不动", first.X, second.X)
	}
}

// 杀死变异：移除区域面板、物品暗边或退回平面色块会破坏 HUD 的统一层级。
func TestHotbarLayoutUsesPanelAndInsetItemTiles(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemGrass, Count: 1}

	var layout hotbarLayout
	got := layoutInventory(&layout, atlas, inventory, false, -1, nil, nil, MiningOverlay{}, 1280, 720)
	if len(got.quads) != 13 {
		t.Fatalf("快捷栏 quads=%d，想要面板、选中框、9 个栏位和双层物品色块共 13", len(got.quads))
	}
	panel := got.quads[0]
	if panel.Color != ([4]float32{0.025, 0.03, 0.035, 0.88}) ||
		panel.Width <= core.HotbarSlots*hotbarSlotSize || panel.Height <= hotbarSlotSize {
		t.Fatalf("快捷栏面板=%+v", panel)
	}
	border, face := got.quads[len(got.quads)-2], got.quads[len(got.quads)-1]
	if border.Width <= face.Width || border.Height <= face.Height || border.Color == face.Color {
		t.Fatalf("物品双层色块 border=%+v face=%+v", border, face)
	}
	assertHotbarItemFace(t, face, core.ItemGrass)
}

// 杀死变异：重新用近似色块或复制错误方块面的像素，都无法通过逐像素来源核对。
func TestHotbarTextureAtlasCopiesRegisteredBlockTopFaces(t *testing.T) {
	registry := assets.NewRegistry()
	pixels := buildHotbarTextureAtlas(registry)
	for _, item := range []core.ItemID{
		core.ItemStone, core.ItemDirt, core.ItemGrass, core.ItemStoneBrick,
		core.ItemFurnace, core.ItemIronBlock, core.ItemChest,
		core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
		core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
		core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
		core.ItemSnowBlock, core.ItemMossyCobblestone,
	} {
		block, ok := core.ItemPlacement(item)
		if !ok {
			t.Fatalf("测试物品 %d 不可放置", item)
		}
		source := registry.LayerRGBA(int(registry.Material(block, mesh.FacePosY)))
		column := hotbarBlockColumnOffset + int(item)
		for _, point := range [][2]int{{0, 0}, {5, 7}, {15, 15}} {
			x, y := point[0], point[1]
			src := (y*hotbarTextureSize + x) * 4
			dst := (y*hotbarTextureWidth + column*hotbarTextureSize + x) * 4
			if got, want := [4]byte(pixels[dst:dst+4]), [4]byte(source[src:src+4]); got != want {
				t.Fatalf("物品 %d 像素 (%d,%d)=%v，想要注册表材质 %v", item, x, y, got, want)
			}
		}
	}
}

// 杀死变异：不可放置物品误采样空白图集会让工具和材料消失。
func TestNonBlockItemsKeepProgrammaticTiles(t *testing.T) {
	for _, item := range []core.ItemID{core.ItemCoal, core.ItemIronIngot, core.ItemStonePickaxe} {
		var layout hotbarLayout
		appendItemTile(&layout, item, 0, 0, 1)
		if len(layout.quads) != 2 {
			t.Fatalf("物品 %d quads=%d，想要暗边和内层色块", item, len(layout.quads))
		}
		assertHotbarItemFace(t, layout.quads[1], item)
	}
}

func assertHotbarItemFace(t *testing.T, face hotbarInstance, item core.ItemID) {
	t.Helper()
	uv, textured := hotbarItemUV(item)
	gotUV := [4]float32{face.U0, face.V0, face.U1, face.V1}
	if textured {
		if gotUV != uv || face.Color != ([4]float32{1, 1, 1, 1}) {
			t.Fatalf("方块物品 %d face=%+v，想要真实材质 UV=%v", item, face, uv)
		}
		return
	}
	if gotUV != ([4]float32{}) || face.Color != hotbarItemColor(item) {
		t.Fatalf("非方块物品 %d face=%+v，想要程序化色块 %v", item, face, hotbarItemColor(item))
	}
}

// 杀死变异：超过固定 HUD 容量会溢出预分配上传区。
func TestHotbarLayoutStaysWithinFixedCapacity(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	// 箱子视图（27 格）比配方行与熔炉视图都大，因此固定容量的见证必须用满箱子叠加值，
	// 再叠加已确认的满血十段生命条才是真正的最坏布局。
	layoutInventory(
		&layout, atlas, maxQuadTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720,
	)
	appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 1280, 720)
	if len(layout.quads) != maxHotbarQuads {
		t.Fatalf("quad 上限见证 quads=%d，想要 %d", len(layout.quads), maxHotbarQuads)
	}
	layoutInventory(
		&layout, atlas, fullTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720,
	)
	appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 1280, 720)
	if len(layout.glyphs) != 252 {
		t.Fatalf("glyph 上限见证数字=%d，想要背包与箱子两位数阴影共 252", len(layout.glyphs))
	}
	if len(layout.quads) > maxHotbarQuads {
		t.Fatalf("glyph 上限见证 quads=%d，超过固定上限 %d", len(layout.quads), maxHotbarQuads)
	}
	closed := layoutInventory(
		&layout, atlas, fullTestInventory(), false, -1, nil, nil,
		MiningOverlay{Active: true, ProgressTicks: 6, RequiredTicks: 15, Harvestable: true},
		1280, 720,
	)
	if len(closed.quads) != 2+core.HotbarSlots*3+2 || len(closed.quads) > maxHotbarQuads {
		t.Fatalf("关闭界面加采掘条 quads=%d，固定上限=%d", len(closed.quads), maxHotbarQuads)
	}
}

// 杀死变异：放宽显示条件会让满耐久、损坏形态或普通物品多出 quad。
func TestDurabilityBarAppearsOnlyForWornTools(t *testing.T) {
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for _, test := range []struct {
		name  string
		stack core.ItemStack
		want  int
	}{
		{"满耐久工具不显示", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}, 0},
		{"磨损工具显示两个 quad", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full / 2}, 2},
		{"损坏物品不显示", core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1}, 0},
		{"普通方块不显示", core.ItemStack{Item: core.ItemStone, Count: 64}, 0},
		{"空栏位不显示", core.ItemStack{}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendDurabilityBar(&layout, 0, test.stack, 1920, 1080)
			if got := len(layout.quads); got != test.want {
				t.Fatalf("quad 数量 = %d，想要 %d", got, test.want)
			}
		})
	}
}

// 杀死变异：固定宽度或整数相除会让低耐久填充条不再正且短于高耐久。
func TestDurabilityBarFillTracksRemaining(t *testing.T) {
	full, _ := core.ItemMaxDurability(core.ItemIronPickaxe)
	var low, high hotbarLayout
	appendDurabilityBar(&low, 0, core.ItemStack{
		Item: core.ItemIronPickaxe, Count: 1, Durability: 1,
	}, 1920, 1080)
	appendDurabilityBar(&high, 0, core.ItemStack{
		Item: core.ItemIronPickaxe, Count: 1, Durability: full - 1,
	}, 1920, 1080)

	if len(low.quads) != 2 || len(high.quads) != 2 {
		t.Fatalf("quad 数量 = %d / %d，想要各 2", len(low.quads), len(high.quads))
	}
	if low.quads[1].Width >= high.quads[1].Width {
		t.Fatalf("低耐久填充宽度 %v 不小于高耐久 %v", low.quads[1].Width, high.quads[1].Width)
	}
	if low.quads[1].Width <= 0 {
		t.Fatalf("填充宽度 = %v，想要正值", low.quads[1].Width)
	}
}

// 杀死变异：遍历全部 36 格或使用 open 几何会让背包工具也出条或位置漂移。
func TestDurabilityBarLayoutUsesOnlyHotbarGeometry(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	base := core.Inventory{}
	base.Hotbar.Slots[3] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}
	base.Backpack[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full}
	hotbarWorn := base
	hotbarWorn.Hotbar.Slots[3].Durability--
	backpackWorn := base
	backpackWorn.Backpack[0].Durability--

	var layout hotbarLayout
	closedBase := len(layoutInventory(
		&layout, atlas, base, false, -1, nil, nil, MiningOverlay{}, 1280, 720,
	).quads)
	closed := layoutInventory(
		&layout, atlas, hotbarWorn, false, -1, nil, nil, MiningOverlay{}, 1280, 720,
	)
	if len(closed.quads) != closedBase+2 {
		t.Fatalf("关闭背包的磨损工具 quads=%d，想要 %d", len(closed.quads), closedBase+2)
	}
	closedBars := [2]hotbarInstance{closed.quads[len(closed.quads)-2], closed.quads[len(closed.quads)-1]}

	openBase := len(layoutInventory(
		&layout, atlas, base, true, -1, nil, nil, MiningOverlay{}, 1280, 720,
	).quads)
	open := layoutInventory(
		&layout, atlas, hotbarWorn, true, -1, nil, nil, MiningOverlay{}, 1280, 720,
	)
	if len(open.quads) != openBase+2 {
		t.Fatalf("打开背包的快捷栏磨损工具 quads=%d，想要 %d", len(open.quads), openBase+2)
	}
	barStart := len(open.quads) - recipeQuads - 2
	openBars := [2]hotbarInstance{open.quads[barStart], open.quads[barStart+1]}
	if openBars != closedBars {
		t.Fatalf("打开/关闭背包的耐久条几何不同: open=%+v closed=%+v", openBars, closedBars)
	}
	if got := len(layoutInventory(
		&layout, atlas, backpackWorn, true, -1, nil, nil, MiningOverlay{}, 1280, 720,
	).quads); got != openBase {
		t.Fatalf("背包栏磨损工具 quads=%d，想要 %d", got, openBase)
	}
}

// 杀死变异：损坏工具落入默认分支会得到全零色，与完好工具同色则无法表达损坏状态。
func TestBrokenToolColorsAreVisibleAndDistinct(t *testing.T) {
	pairs := [][2]core.ItemID{
		{core.ItemBrokenStonePickaxe, core.ItemStonePickaxe},
		{core.ItemBrokenIronPickaxe, core.ItemIronPickaxe},
	}
	var brokenColors [2][4]float32
	for index, pair := range pairs {
		brokenColors[index] = hotbarItemColor(pair[0])
		if brokenColors[index] == ([4]float32{}) || brokenColors[index][3] != 1 {
			t.Fatalf("损坏工具 %d 颜色=%v，想要可见且 alpha=1", pair[0], brokenColors[index])
		}
		if brokenColors[index] == hotbarItemColor(pair[1]) {
			t.Fatalf("损坏工具 %d 与完好工具颜色相同", pair[0])
		}
	}
	if brokenColors[0] == brokenColors[1] {
		t.Fatal("两种损坏工具颜色相同")
	}
}

// 杀死变异：箱子物品落入默认分支会得到全零色，无法在 HUD 与掉落物中可见。
func TestChestItemColorIsVisible(t *testing.T) {
	color := hotbarItemColor(core.ItemChest)
	if color == ([4]float32{}) || color[3] != 1 {
		t.Fatalf("箱子颜色 = %v，想要可见且 alpha=1", color)
	}
	if color == hotbarItemColor(core.ItemDirt) {
		t.Fatal("箱子颜色与泥土相同")
	}
}

// 杀死变异：预测进度、错误比例/颜色或容器打开仍绘制都会改变固定实例。
func TestMiningOverlayUsesOnlyConfirmedFixedGeometry(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var layout hotbarLayout
	baseQuads := len(layoutInventory(
		&layout, atlas, core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, 1280, 720,
	).quads)
	if baseQuads != 2+core.HotbarSlots {
		t.Fatalf("inactive quads=%d，想要仅面板、选中框和快捷栏", baseQuads)
	}
	requiredZero := layoutInventory(
		&layout, atlas, core.Inventory{}, false, -1, nil, nil,
		MiningOverlay{Active: true, ProgressTicks: 6}, 1280, 720,
	)
	if len(requiredZero.quads) != baseQuads {
		t.Fatalf("required=0 quads=%d，想要 %d", len(requiredZero.quads), baseQuads)
	}

	green := layoutInventory(
		&layout, atlas, core.Inventory{}, false, -1, nil, nil,
		MiningOverlay{Active: true, ProgressTicks: 6, RequiredTicks: 15, Harvestable: true},
		1280, 720,
	)
	if len(green.quads) != baseQuads+2 {
		t.Fatalf("active 6/15 quads=%d，想要背景加填充", len(green.quads))
	}
	background, fill := green.quads[len(green.quads)-2], green.quads[len(green.quads)-1]
	if background.X != 520 || background.Y != 620 ||
		background.Width != 240 || background.Height != 12 ||
		background.Color != ([4]float32{0.05, 0.05, 0.06, 0.78}) {
		t.Fatalf("采掘条背景=%+v", background)
	}
	if fill.X != background.X || fill.Y != background.Y ||
		fill.Width != 96 || fill.Height != background.Height ||
		fill.Color != ([4]float32{0.30, 0.78, 0.36, 0.95}) {
		t.Fatalf("可掉落 6/15 填充=%+v", fill)
	}

	orange := layoutInventory(
		&layout, atlas, core.Inventory{}, false, -1, nil, nil,
		MiningOverlay{Active: true, ProgressTicks: 6, RequiredTicks: 15}, 1280, 720,
	)
	if got := orange.quads[len(orange.quads)-1].Color; got != ([4]float32{0.95, 0.55, 0.15, 0.95}) {
		t.Fatalf("不可掉落颜色=%v", got)
	}

	openWithoutMining := len(layoutInventory(
		&layout, atlas, core.Inventory{}, true, -1, nil, nil, MiningOverlay{}, 1280, 720,
	).quads)
	openWithMining := len(layoutInventory(
		&layout, atlas, core.Inventory{}, true, -1, nil, nil,
		MiningOverlay{Active: true, ProgressTicks: 6, RequiredTicks: 15, Harvestable: true},
		1280, 720,
	).quads)
	if openWithMining != openWithoutMining {
		t.Fatalf("打开背包仍绘制采掘条: quads=%d，想要 %d", openWithMining, openWithoutMining)
	}
}

// 杀死变异：忽略 Confirmed 标记或画出预测值，会让 HUD 在收到权威状态前显示猜测值。
func TestAppendHealthBarDrawsOnlyConfirmedValues(t *testing.T) {
	atlas := newFakeNameTagAtlas()

	var unconfirmed hotbarLayout
	appendHealthBar(&unconfirmed, atlas, HealthOverlay{Confirmed: false, Value: 12}, 1280, 720)
	if len(unconfirmed.quads) != 0 || len(unconfirmed.glyphs) != 0 {
		t.Fatalf("未确认生命值 quads=%d glyphs=%d，想要都为 0", len(unconfirmed.quads), len(unconfirmed.glyphs))
	}

	for _, test := range []struct {
		name      string
		value     uint8
		wantQuads int
	}{
		{"零血", 0, 10},
		{"一点生命", 1, 11},
		{"满血", core.MaxHealth, 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: test.value}, 1280, 720)
			if len(layout.quads) != test.wantQuads || len(layout.glyphs) != 0 {
				t.Fatalf("确认生命值 quads/glyphs=%d/%d，想要 %d/0",
					len(layout.quads), len(layout.glyphs), test.wantQuads)
			}
			first := layout.quads[0]
			if first.X != 8 || first.Y != 696 || first.Width != 16 || first.Height != 16 {
				t.Fatalf("左下第一颗爱心=%+v，想要锚定 (8,696) 且无前置背景", first)
			}
		})
	}
}

// 杀死变异：零尺寸 framebuffer 时仍绘制生命值会产生越界或退化几何。
func TestAppendHealthBarRejectsDegenerateFramebuffer(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var layout hotbarLayout
	appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: 12}, 0, 720)
	if len(layout.quads) != 0 {
		t.Fatalf("零宽 framebuffer quads=%d，想要 0", len(layout.quads))
	}
}

// 杀死变异：继续依附快捷栏、保留面板或沿用打开背包 scale 都会让两组实例不同。
func TestHealthHeartsStayBottomLeftWithoutBackgroundAt640x360(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var closed, open hotbarLayout
	layoutInventory(&closed, atlas, core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, 640, 360)
	closedStart := len(closed.quads)
	appendHealthBar(&closed, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 640, 360)
	layoutInventory(&open, atlas, core.Inventory{}, true, -1, nil, nil, MiningOverlay{}, 640, 360)
	openStart := len(open.quads)
	appendHealthBar(&open, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 640, 360)
	closedHearts, openHearts := closed.quads[closedStart:], open.quads[openStart:]
	if len(closedHearts) != 20 || len(openHearts) != 20 {
		t.Fatalf("关闭/打开背包爱心=%d/%d，想要无背景的 10 空心加 10 满心", len(closedHearts), len(openHearts))
	}
	if !reflect.DeepEqual(closedHearts, openHearts) {
		t.Fatalf("打开背包移动或缩放了生命栏: closed=%+v open=%+v", closedHearts, openHearts)
	}
	for index, heart := range closedHearts {
		if heart.X < 8 || heart.Y < 0 || heart.X+heart.Width > 640 || heart.Y+heart.Height > 352 {
			t.Fatalf("爱心 %d 未保持左/下 8px 安全边距: %+v", index, heart)
		}
	}
	if first := closedHearts[0]; first.X != 8 || first.Y != 336 || first.Width != 16 || first.Height != 16 {
		t.Fatalf("第一颗爱心=%+v，想要 (8,336,16,16)", first)
	}
}

// 杀死变异：退回矩形段、漏掉空心爱心或把奇数生命画成整颗都会改变 UV 与宽度。
func TestHealthBarUsesTenTwoPointHearts(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, test := range []struct {
		name      string
		health    uint8
		wantQuads int
		lastHalf  bool
	}{
		{"零血", 0, 10, false},
		{"九点生命", 9, 15, true},
		{"满血", core.MaxHealth, 20, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: test.health}, 1280, 720)
			if len(layout.quads) != test.wantQuads || len(layout.glyphs) != 0 {
				t.Fatalf("quads/glyphs=%d/%d，想要 %d/0", len(layout.quads), len(layout.glyphs), test.wantQuads)
			}
			emptyUV := hotbarTextureUV(hotbarEmptyHeartColumn)
			for index, heart := range layout.quads[:10] {
				if got := [4]float32{heart.U0, heart.V0, heart.U1, heart.V1}; got != emptyUV {
					t.Fatalf("空心爱心 %d UV=%v，想要 %v", index, got, emptyUV)
				}
				if heart.Width != healthHeartSize || heart.Height != healthHeartSize {
					t.Fatalf("空心爱心 %d 尺寸=%v×%v", index, heart.Width, heart.Height)
				}
			}
			if test.health > 0 {
				last := layout.quads[len(layout.quads)-1]
				fullUV := hotbarTextureUV(hotbarFullHeartColumn)
				wantU1 := fullUV[2]
				if test.lastHalf {
					wantU1 = (fullUV[0] + fullUV[2]) * 0.5
				}
				if got := [4]float32{last.U0, last.V0, last.U1, last.V1}; got != ([4]float32{fullUV[0], fullUV[1], wantU1, fullUV[3]}) {
					t.Fatalf("最后填充爱心 UV=%v，想要完整/半颗材质", got)
				}
			}
			if test.lastHalf {
				last := layout.quads[len(layout.quads)-1]
				if last.Width != healthHeartSize/2 || last.Height != healthHeartSize {
					t.Fatalf("奇数生命末颗=%+v，想要半颗爱心", last)
				}
			}
		})
	}
}

func TestHotbarBufferRegionsDoNotOverlap(t *testing.T) {
	if hotbarQuadOffset%256 != 0 || hotbarGlyphOffset%256 != 0 {
		t.Fatalf("buffer offset 未按 256 字节对齐: quad=%d glyph=%d", hotbarQuadOffset, hotbarGlyphOffset)
	}
	quadEnd := hotbarQuadOffset + hotbarQuadSize
	if hotbarGlyphOffset < quadEnd {
		t.Fatalf("glyph offset=%d 落入 quad 区间 [%d,%d)", hotbarGlyphOffset, hotbarQuadOffset, quadEnd)
	}
}

// Mutation killed: accepting an invalid authoritative value or a degenerate
// framebuffer would emit instances for a state the server never confirmed.
func TestHotbarLayoutRejectsInvalidStateAndEmptyFramebuffer(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	invalid := core.Inventory{Hotbar: core.Hotbar{Selected: core.HotbarSlots}}
	var layout hotbarLayout
	if got := layoutInventory(&layout, atlas, invalid, false, -1, nil, nil, MiningOverlay{}, 800, 600); len(got.quads) != 0 {
		t.Fatalf("非法物品状态 quads=%d，想要 0", len(got.quads))
	}
	if got := layoutInventory(&layout, atlas, core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, 0, 600); len(got.quads) != 0 {
		t.Fatalf("零宽 framebuffer quads=%d，想要 0", len(got.quads))
	}
}

// Mutation killed: adding depth attachments, dropping alpha blending, splitting
// the upload, or reordering quads before glyphs changes the captured descriptors.
func TestHotbarRendererUsesSingleUploadAndFixedDraws(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	dev := &nameTagTestDevice{}
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas, assets.NewRegistry())
	defer renderer.Release()

	upload := dev.bufferByLabel(t, "hotbar dynamic upload")
	if got, want := upload.desc.Size, uint64(hotbarUploadBytes); got != want {
		t.Fatalf("dynamic upload size=%d want=%d", got, want)
	}
	if got, want := len(dev.buffers), 1; got != want {
		t.Fatalf("constructor buffers=%d want=%d", got, want)
	}
	if got, want := len(dev.pipelineDescs), 2; got != want {
		t.Fatalf("pipeline descriptors=%d want=%d", got, want)
	}
	for _, desc := range dev.pipelineDescs {
		if desc.Blend != gfx.BlendAlpha || desc.DepthFormat != gfx.FormatUndefined || desc.DepthWrite {
			t.Fatalf("pipeline %q=%+v；想要无深度附件的 alpha 混合", desc.Label, desc)
		}
	}

	// 传入满箱子叠加值与已确认的满血生命值，让实例数量达到最坏布局。
	if err := renderer.Prepare(
		fullTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{},
		HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 1280, 720, NewUploadBudget(1024),
	); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if len(upload.lastWrite) != 0 {
		t.Fatalf("Prepare 写入 %d GPU 字节，想要 0", len(upload.lastWrite))
	}

	encoder := &nameTagTestEncoder{}
	renderer.Render(encoder, &nameTagTestView{})
	if got, want := len(encoder.passes), 1; got != want {
		t.Fatalf("passes=%d want=%d", got, want)
	}
	pass := encoder.passes[0]
	if pass.desc.Label != "hotbar pass" || pass.desc.LoadClear || pass.desc.DepthView != nil {
		t.Fatalf("pass 描述=%+v；想要保留已有颜色且不挂深度", pass.desc)
	}
	if got, want := pass.pipelineLabels, []string{"hotbar quad", "hotbar glyph"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pipeline 顺序=%v want=%v", got, want)
	}
	// fullTestInventory 里的物品都没有耐久上限；分组面板、两种高亮、栏位与双层物品
	// 加满箱子和满生命条形成当前实例数，数字数量由背包与箱子共同达到上限。
	want := []uint32{openInventoryPanelQuads + 2 + core.InventorySlots*3 + chestQuads + healthQuads, maxHotbarGlyphs}
	if got := pass.drawInstances; !reflect.DeepEqual(got, want) {
		t.Fatalf("draw 实例数=%v want=%v", got, want)
	}
	if !pass.ended {
		t.Fatal("hotbar pass 未结束")
	}
	if got, want := upload.writes, 1; got != want {
		t.Fatalf("每帧 dynamic upload 次数=%d want=%d", got, want)
	}
	if got := float32At(upload.lastWrite, 0); got != 1280 {
		t.Fatalf("viewport 宽=%f want=1280", got)
	}
	if got, want := len(upload.lastWrite), hotbarUploadBytes; got != want {
		t.Fatalf("满 HUD 上传字节=%d want=%d", got, want)
	}
}

// Mutation killed: rendering an empty prepared layout emits an observable pass.
func TestHotbarRendererSkipsEmptyPreparedLayout(t *testing.T) {
	renderer := NewHotbarRenderer(&nameTagTestDevice{}, gfx.FormatRGBA8Unorm, newFakeNameTagAtlas(), assets.NewRegistry())
	defer renderer.Release()
	if err := renderer.Prepare(core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, HealthOverlay{}, 0, 0, NewUploadBudget(1024)); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	encoder := &nameTagTestEncoder{}
	renderer.Render(encoder, &nameTagTestView{})
	if len(encoder.passes) != 0 {
		t.Fatalf("空布局 passes=%d want=0", len(encoder.passes))
	}
}

// Mutation killed: releasing the borrowed atlas view, leaking an owned handle,
// or failing idempotency changes at least one exact release count.
func TestHotbarRendererReleaseOwnsOnlyItsHandles(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	dev := &nameTagTestDevice{}
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas, assets.NewRegistry())
	renderer.Release()
	renderer.Release()

	for _, buffer := range dev.buffers {
		if buffer.releases != 1 {
			t.Errorf("buffer %q releases=%d want=1", buffer.desc.Label, buffer.releases)
		}
	}
	for _, pipeline := range dev.pipelines {
		if pipeline.releases != 1 {
			t.Errorf("pipeline %q releases=%d want=1", pipeline.label, pipeline.releases)
		}
	}
	if dev.bind.releases != 1 || dev.sampler.releases != 1 {
		t.Errorf("bind/sampler releases=%d/%d want=1/1", dev.bind.releases, dev.sampler.releases)
	}
	for _, texture := range dev.textures {
		if texture.releases != 1 || texture.view.releases != 1 {
			t.Errorf("texture/view %q releases=%d/%d want=1/1",
				texture.desc.Label, texture.releases, texture.view.releases)
		}
	}
	if atlas.view.releases != 0 || atlas.releases != 0 {
		t.Fatalf("借用的 atlas/view releases=%d/%d want=0/0", atlas.releases, atlas.view.releases)
	}
}

// Mutation killed: reallocating layout or upload storage per frame would make
// the warmed HUD path allocate.
func TestHotbarPrepareReusesLayoutAndUploadStorage(t *testing.T) {
	source := &allocationGlyphSource{}
	renderer := &HotbarRenderer{
		atlas: source,
		layout: hotbarLayout{
			quads:  make([]hotbarInstance, 0, maxHotbarQuads),
			glyphs: make([]hotbarInstance, 0, maxHotbarGlyphs),
		},
		upload: make([]byte, hotbarUploadBytes),
	}
	inventory := fullTestInventory()
	health := HealthOverlay{Confirmed: true, Value: 7}
	budget := NewUploadBudget(1024)
	if err := renderer.Prepare(inventory, true, 3, nil, nil, MiningOverlay{}, health, 1280, 720, budget); err != nil {
		t.Fatalf("warm Prepare: %v", err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		source.requestCount = 0
		if err := renderer.Prepare(inventory, true, 3, nil, nil, MiningOverlay{}, health, 1280, 720, budget); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warmed hotbar Prepare allocations=%v want=0", allocations)
	}
}

// Mutation killed: invalid WGSL bindings, buffer layouts, or attachment formats
// trigger a WebGPU validation panic during submit/poll.
func TestHotbarRendererHeadlessBlendOverExistingColor(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("本机无可用 GPU 适配器: %v", err)
	}
	defer dev.Release()

	color := dev.CreateTexture(gfx.TextureDesc{
		Label: "hotbar test color", Width: 128, Height: 128,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()

	atlas, err := NewGlyphAtlas(dev)
	if err != nil {
		t.Fatalf("NewGlyphAtlas: %v", err)
	}
	defer atlas.Release()
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas, assets.NewRegistry())
	defer renderer.Release()
	if err := renderer.Prepare(
		fullTestInventory(), true, 0, nil, nil, MiningOverlay{},
		HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 128, 128, NewUploadBudget(1<<20),
	); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	encoder := dev.CreateCommandEncoder()
	clear := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "hotbar test clear", ColorView: colorView, LoadClear: true,
	})
	clear.End()
	renderer.Render(encoder, colorView)
	commands := encoder.Finish()
	dev.Submit(commands)
	commands.Release()
	dev.Poll(true)
}

// Mutation killed: mislaying the backpack rows, dropping the source highlight,
// or letting hit-testing drift from the drawn geometry.
func TestInventoryLayoutOpensThreeBackpackRows(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	var inventory core.Inventory
	got := layoutInventory(&layout, atlas, inventory, true, 12, nil, nil, MiningOverlay{}, 1280, 720)

	// 外框、背包区、快捷栏区与分隔线 + 选中框 + 来源高亮 + 36 格 + 固定配方行。
	if len(got.quads) != openInventoryPanelQuads+2+core.InventorySlots+recipeQuads {
		t.Fatalf("打开时 quads=%d，想要 %d", len(got.quads), openInventoryPanelQuads+2+core.InventorySlots+recipeQuads)
	}
	panels := got.quads[:openInventoryPanelQuads]
	if panels[1].Y >= panels[2].Y || panels[1].Color == panels[2].Color || panels[3].Height <= 0 {
		t.Fatalf("背包分组面板不清晰: %+v", panels)
	}
	hotbarY := float32(720) - hotbarBottomMargin - hotbarSlotSize
	for slot := range core.InventorySlots {
		x, y := inventorySlotOrigin(slot, true, 1280, 720)
		if slot < core.HotbarSlots && y != hotbarY {
			t.Fatalf("快捷栏格 %d 不在底行: y=%f", slot, y)
		}
		if slot >= core.HotbarSlots && y >= hotbarY {
			t.Fatalf("背包格 %d 未排在快捷栏之上: y=%f", slot, y)
		}
		// 命中函数必须与绘制几何一致。
		if got, ok := InventorySlotAt(float64(x)+1, float64(y)+1, 1280, 720); !ok ||
			got != uint8(slot) {
			t.Fatalf("InventorySlotAt 命中 %d, %v，想要 %d", got, ok, slot)
		}
	}
}

func TestInventorySlotAtRejectsOutsideHits(t *testing.T) {
	if _, ok := InventorySlotAt(0, 0, 1280, 720); ok {
		t.Fatal("界外命中被接受")
	}
	x, y := inventorySlotOrigin(0, true, 1280, 720)
	if _, ok := InventorySlotAt(float64(x)-1, float64(y)+1, 1280, 720); ok {
		t.Fatal("格子左侧 1 像素被判为命中")
	}
	if _, ok := InventorySlotAt(float64(x)+1, float64(y)+1, 0, 0); ok {
		t.Fatal("零尺寸 framebuffer 被判为命中")
	}
}

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

// 杀死变异：小窗口保持固定 48px 会让上方配方行落出 framebuffer；独立缩放命中则会漂移。
func TestOpenInventoryFitsAndHitsAt640x360(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	got := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, nil, MiningOverlay{}, 640, 360)
	for index, quad := range got.quads {
		if quad.X < 0 || quad.Y < 0 || quad.X+quad.Width > 640 || quad.Y+quad.Height > 360 {
			t.Fatalf("quad %d 越界: %+v", index, quad)
		}
	}
	for index, glyph := range got.glyphs {
		if glyph.X < 0 || glyph.Y < 0 || glyph.X+glyph.Width > 640 || glyph.Y+glyph.Height > 360 {
			t.Fatalf("glyph %d 越界: %+v", index, glyph)
		}
	}

	var firstButton hotbarInstance
	for _, quad := range got.quads {
		if quad.Height > 0 && quad.Width/quad.Height > 1.9 && quad.Width/quad.Height < 2.1 {
			firstButton = quad
			break
		}
	}
	if firstButton.Width == 0 {
		t.Fatal("未找到缩放后的合成按钮")
	}
	recipe, ok := RecipeButtonAt(
		float64(firstButton.X+firstButton.Width/2),
		float64(firstButton.Y+firstButton.Height/2),
		640, 360,
	)
	if !ok || recipe != core.RecipeStoneBricks {
		t.Fatalf("缩放按钮命中=%d,%v，想要石砖配方", recipe, ok)
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

func hotbarRecipeButtonQuads(layout hotbarLayout) []hotbarInstance {
	buttons := make([]hotbarInstance, 0, 5)
	for _, quad := range layout.quads {
		if quad.Width == recipeButtonWidth && quad.Height == hotbarSlotSize {
			buttons = append(buttons, quad)
		}
	}
	return buttons
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

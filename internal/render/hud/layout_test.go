package hud

import (
	"reflect"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

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
	// 氧气条只在未满时出现，因此最坏布局取一个未满值；满氧反而是零 quad。
	appendOxygenBar(&layout, OxygenOverlay{Confirmed: true, Value: 0}, 1280, 720)
	chatLine := strings.Repeat("中", maxChatRunes)
	appendChatOverlay(&layout, atlas, ChatOverlay{
		Open: true, Input: chatLine,
		Lines: []string{chatLine, chatLine, chatLine, chatLine, chatLine, chatLine},
	}, 1280, 720)
	if len(layout.quads) != maxHotbarQuads {
		t.Fatalf("quad 上限见证 quads=%d，想要 %d", len(layout.quads), maxHotbarQuads)
	}
	layoutInventory(
		&layout, atlas, fullTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720,
	)
	appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 1280, 720)
	appendOxygenBar(&layout, OxygenOverlay{Confirmed: true, Value: 0}, 1280, 720)
	appendChatOverlay(&layout, atlas, ChatOverlay{
		Open: true, Input: chatLine,
		Lines: []string{chatLine, chatLine, chatLine, chatLine, chatLine, chatLine},
	}, 1280, 720)
	if len(layout.glyphs) != maxHotbarGlyphs {
		t.Fatalf("glyph 上限见证=%d，想要 %d", len(layout.glyphs), maxHotbarGlyphs)
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

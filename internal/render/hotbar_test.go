package render

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
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

	if len(got.quads) != 1+core.HotbarSlots {
		t.Fatalf("空物品状态 quads=%d，想要选中框加 9 个栏位", len(got.quads))
	}
	if len(got.glyphs) != 0 {
		t.Fatalf("空快捷栏数字=%d，想要 0", len(got.glyphs))
	}

	total := core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap
	originX := (800 - total) * 0.5
	originY := 600 - hotbarBottomMargin - hotbarSlotSize
	for slot := range core.HotbarSlots {
		quad := got.quads[1+slot]
		wantX := originX + float32(slot)*(hotbarSlotSize+hotbarSlotGap)
		if quad.X != wantX || quad.Y != originY ||
			quad.Width != hotbarSlotSize || quad.Height != hotbarSlotSize {
			t.Fatalf("栏位 %d = %+v，想要 (%f,%f,%f,%f)",
				slot, quad, wantX, originY, hotbarSlotSize, hotbarSlotSize)
		}
	}
	selection := got.quads[0]
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
	if len(got.quads) != 1+core.HotbarSlots+3 {
		t.Fatalf("quads=%d，想要选中框、9 个栏位和 3 个色块", len(got.quads))
	}
	swatches := got.quads[1+core.HotbarSlots:]
	wantColors := [][4]float32{
		hotbarItemColor(core.ItemStone),
		hotbarItemColor(core.ItemDirt),
		hotbarItemColor(core.ItemGrass),
	}
	for index, swatch := range swatches {
		if swatch.Color != wantColors[index] {
			t.Fatalf("色块 %d 颜色 = %v，想要 %v", index, swatch.Color, wantColors[index])
		}
		if swatch.Width != hotbarSlotSize-2*hotbarSwatchInset {
			t.Fatalf("色块 %d 尺寸 = %f", index, swatch.Width)
		}
	}
	if len(got.glyphs) != 4 {
		t.Fatalf("数字数量 = %d，想要 64/9/1 共 4 位", len(got.glyphs))
	}
}

// 杀死变异：超过固定 HUD 容量会溢出预分配上传区。
func TestHotbarLayoutStaysWithinFixedCapacity(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	// 箱子视图（27 格）比配方行与熔炉视图都大，因此固定容量的见证必须用满箱子叠加值。
	quadWitness := layoutInventory(
		&layout, atlas, maxQuadTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720,
	)
	if len(quadWitness.quads) != maxHotbarQuads {
		t.Fatalf("quad 上限见证 quads=%d，想要 %d", len(quadWitness.quads), maxHotbarQuads)
	}
	glyphWitness := layoutInventory(
		&layout, atlas, fullTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720,
	)
	if len(glyphWitness.glyphs) != maxHotbarGlyphs {
		t.Fatalf("glyph 上限见证数字=%d，想要 %d", len(glyphWitness.glyphs), maxHotbarGlyphs)
	}
	if len(glyphWitness.quads) > maxHotbarQuads {
		t.Fatalf("glyph 上限见证 quads=%d，超过固定上限 %d", len(glyphWitness.quads), maxHotbarQuads)
	}
	closed := layoutInventory(
		&layout, atlas, fullTestInventory(), false, -1, nil, nil,
		MiningOverlay{Active: true, ProgressTicks: 6, RequiredTicks: 15, Harvestable: true},
		1280, 720,
	)
	if len(closed.quads) != 1+core.HotbarSlots*2+2 || len(closed.quads) > maxHotbarQuads {
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
	if baseQuads != 1+core.HotbarSlots {
		t.Fatalf("inactive quads=%d，想要仅选中框和快捷栏", baseQuads)
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
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas)
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

	// 传入满箱子叠加值，让本用例的数字数量真正达到 maxHotbarGlyphs，
	// 从而让下面的“满 HUD 上传字节”断言有意义。
	if err := renderer.Prepare(
		fullTestInventory(), true, 5, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720, NewUploadBudget(1024),
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
	// fullTestInventory 里的物品都没有耐久上限，因此没有耐久条 quad；
	// 数字数量则因为满箱子叠加值恰好达到全局上限 maxHotbarGlyphs。
	want := []uint32{2 + core.InventorySlots*2 + chestQuads, maxHotbarGlyphs}
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
	renderer := NewHotbarRenderer(&nameTagTestDevice{}, gfx.FormatRGBA8Unorm, newFakeNameTagAtlas())
	defer renderer.Release()
	if err := renderer.Prepare(core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, 0, 0, NewUploadBudget(1024)); err != nil {
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
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas)
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
	overlay := fullFurnaceOverlay()
	budget := NewUploadBudget(1024)
	if err := renderer.Prepare(inventory, true, 3, overlay, nil, MiningOverlay{}, 1280, 720, budget); err != nil {
		t.Fatalf("warm Prepare: %v", err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		source.requestCount = 0
		if err := renderer.Prepare(inventory, true, 3, overlay, nil, MiningOverlay{}, 1280, 720, budget); err != nil {
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
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas)
	defer renderer.Release()
	if err := renderer.Prepare(fullTestInventory(), true, 0, nil, nil, MiningOverlay{}, 128, 128, NewUploadBudget(1<<20)); err != nil {
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

	// 选中框 + 来源高亮 + 36 个栏位背景 + 固定配方行。
	if len(got.quads) != 2+core.InventorySlots+recipeQuads {
		t.Fatalf("打开时 quads=%d，想要 %d", len(got.quads), 2+core.InventorySlots+recipeQuads)
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

	open := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, nil, MiningOverlay{}, 1280, 720)
	if len(open.quads) != 52 {
		t.Fatalf("空背包 quads=%d，想要选中框、36 格和 15 个配方实例共 52", len(open.quads))
	}
	if len(open.glyphs) != 10 {
		t.Fatalf("五条配方数字=%d，想要每条输入输出各一位共 10", len(open.glyphs))
	}
	overlay := open.quads[len(open.quads)-15:]
	wantItems := [][4]float32{
		{128.0 / 255, 128.0 / 255, 128.0 / 255, 1},
		{122.0 / 255, 118.0 / 255, 112.0 / 255, 1},
		{128.0 / 255, 128.0 / 255, 128.0 / 255, 1},
		{88.0 / 255, 86.0 / 255, 88.0 / 255, 1},
		{220.0 / 255, 220.0 / 255, 224.0 / 255, 1},
		{214.0 / 255, 214.0 / 255, 216.0 / 255, 1},
		{128.0 / 255, 128.0 / 255, 128.0 / 255, 1},
		{104.0 / 255, 112.0 / 255, 120.0 / 255, 1},
		{220.0 / 255, 220.0 / 255, 224.0 / 255, 1},
		{190.0 / 255, 198.0 / 255, 210.0 / 255, 1},
	}
	for row, y := range []float32{420, 368, 316, 264, 212} {
		input, output := overlay[row*3], overlay[row*3+1]
		if input.X != 408 || output.X != 460 || input.Y != y || output.Y != y {
			t.Fatalf("配方行 %d 位置错误: input=%+v output=%+v", row, input, output)
		}
		if input.Color != wantItems[row*2] || output.Color != wantItems[row*2+1] {
			t.Fatalf("配方行 %d 物品错误: input=%v output=%v", row, input.Color, output.Color)
		}
	}
	disabled := hotbarRecipeButtonQuads(open)
	if len(disabled) != 5 {
		t.Fatalf("配方按钮=%d，想要 5", len(disabled))
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
	// 空熔炉：3 个栏位背景 + 2 条进度条底，没有色块也没有填充。
	emptyQuads := len(empty.quads)
	if len(empty.glyphs) != 0 {
		t.Fatalf("空熔炉数字 = %d，想要 0", len(empty.glyphs))
	}

	full := layoutInventory(
		&layout, atlas, core.Inventory{}, true, -1, fullFurnaceOverlay(), nil, MiningOverlay{}, 1280, 720,
	)
	if len(full.quads) != emptyQuads+3+2 {
		t.Fatalf("满熔炉 quads = %d，想要比空熔炉多 3 个色块和 2 条填充", len(full.quads))
	}
	if len(full.glyphs) != furnaceGlyphs {
		t.Fatalf("满熔炉数字 = %d，想要 %d", len(full.glyphs), furnaceGlyphs)
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
	if recipeButtons != 5 || len(hotbarRecipeButtonQuads(furnace)) != 0 {
		t.Fatalf("配方视图按钮=%d，熔炉视图按钮=%d，想要 5 和 0",
			recipeButtons, len(hotbarRecipeButtonQuads(furnace)))
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
		// 第二个 quad 是来源高亮。
		highlight := got.quads[1]
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
	if len(got.quads) != emptyQuads+3 {
		t.Fatalf("三格占用 quads=%d，想要比空箱子多 3 个色块", len(got.quads))
	}
	if len(got.glyphs) != 4 {
		t.Fatalf("数字数量 = %d，想要 64/5/1 共 4 位", len(got.glyphs))
	}
	swatches := got.quads[emptyQuads:]
	wantColors := [][4]float32{
		hotbarItemColor(core.ItemStone),
		hotbarItemColor(core.ItemCoal),
		hotbarItemColor(core.ItemIronIngot),
	}
	for index, swatch := range swatches {
		if swatch.Color != wantColors[index] {
			t.Fatalf("色块 %d 颜色 = %v，想要 %v", index, swatch.Color, wantColors[index])
		}
	}

	full := layoutInventory(&layout, atlas, core.Inventory{}, true, -1, nil, fullChestOverlay(), MiningOverlay{}, 1280, 720)
	if len(full.quads) != emptyQuads+core.ChestSlots {
		t.Fatalf("满箱子 quads = %d，想要比空箱子多 %d 个色块", len(full.quads), core.ChestSlots)
	}
	if len(full.glyphs) != chestGlyphs {
		t.Fatalf("满箱子数字 = %d，想要 %d", len(full.glyphs), chestGlyphs)
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
	if recipeButtons != 5 || len(hotbarRecipeButtonQuads(chest)) != 0 {
		t.Fatalf("配方视图按钮=%d，箱子视图按钮=%d，想要 5 和 0",
			recipeButtons, len(hotbarRecipeButtonQuads(chest)))
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
		// 第二个 quad 是来源高亮。
		highlight := got.quads[1]
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

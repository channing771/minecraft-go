package render

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
)

func fullTestHotbar() core.Hotbar {
	var hotbar core.Hotbar
	hotbar.Selected = 4
	items := [core.HotbarSlots]core.ItemID{
		core.ItemStone, core.ItemDirt, core.ItemGrass,
		core.ItemStone, core.ItemDirt, core.ItemGrass,
		core.ItemStone, core.ItemDirt, core.ItemGrass,
	}
	for slot, item := range items {
		hotbar.Slots[slot] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	}
	return hotbar
}

// Mutation killed: dropping the selection frame, mislaying slots, or letting the
// layout depend on anything but framebuffer size and hotbar value changes the
// exact instance rectangles below.
func TestHotbarLayoutIsFixedNineSlotsWithSelection(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var hotbar core.Hotbar
	hotbar.Selected = 2
	var layout hotbarLayout
	got := layoutHotbar(&layout, atlas, hotbar, 800, 600)

	if len(got.quads) != 1+core.HotbarSlots {
		t.Fatalf("空快捷栏 quads=%d，想要选中框加 9 个栏位", len(got.quads))
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
	var hotbar core.Hotbar
	hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 64}
	hotbar.Slots[3] = core.ItemStack{Item: core.ItemDirt, Count: 9}
	hotbar.Slots[8] = core.ItemStack{Item: core.ItemGrass, Count: 1}

	var layout hotbarLayout
	got := layoutHotbar(&layout, atlas, hotbar, 1280, 720)
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

// Mutation killed: exceeding the fixed HUD capacity would overflow the
// preallocated upload regions.
func TestHotbarLayoutStaysWithinFixedCapacity(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, char := range hotbarDigits {
		atlas.glyphs[char] = fakeNameTagGlyph(7)
	}
	var layout hotbarLayout
	got := layoutHotbar(&layout, atlas, fullTestHotbar(), 1280, 720)
	if len(got.quads) != maxHotbarQuads {
		t.Fatalf("满快捷栏 quads=%d，想要 %d", len(got.quads), maxHotbarQuads)
	}
	if len(got.glyphs) != maxHotbarGlyphs {
		t.Fatalf("满快捷栏数字=%d，想要 %d", len(got.glyphs), maxHotbarGlyphs)
	}
}

// Mutation killed: accepting an invalid authoritative value or a degenerate
// framebuffer would emit instances for a state the server never confirmed.
func TestHotbarLayoutRejectsInvalidStateAndEmptyFramebuffer(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	invalid := core.Hotbar{Selected: core.HotbarSlots}
	var layout hotbarLayout
	if got := layoutHotbar(&layout, atlas, invalid, 800, 600); len(got.quads) != 0 {
		t.Fatalf("非法快捷栏 quads=%d，想要 0", len(got.quads))
	}
	if got := layoutHotbar(&layout, atlas, core.Hotbar{}, 0, 600); len(got.quads) != 0 {
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

	if err := renderer.Prepare(fullTestHotbar(), 1280, 720, NewUploadBudget(1024)); err != nil {
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
	want := []uint32{maxHotbarQuads, maxHotbarGlyphs}
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
	if err := renderer.Prepare(core.Hotbar{}, 0, 0, NewUploadBudget(1024)); err != nil {
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
	hotbar := fullTestHotbar()
	budget := NewUploadBudget(1024)
	if err := renderer.Prepare(hotbar, 1280, 720, budget); err != nil {
		t.Fatalf("warm Prepare: %v", err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		source.requestCount = 0
		if err := renderer.Prepare(hotbar, 1280, 720, budget); err != nil {
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
	if err := renderer.Prepare(fullTestHotbar(), 128, 128, NewUploadBudget(1<<20)); err != nil {
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

package hud

import (
	"reflect"
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/render"
)

func TestHotbarBufferRegionsDoNotOverlap(t *testing.T) {
	if hotbarQuadOffset%256 != 0 || hotbarGlyphOffset%256 != 0 {
		t.Fatalf("buffer offset 未按 256 字节对齐: quad=%d glyph=%d", hotbarQuadOffset, hotbarGlyphOffset)
	}
	quadEnd := hotbarQuadOffset + hotbarQuadSize
	if hotbarGlyphOffset < quadEnd {
		t.Fatalf("glyph offset=%d 落入 quad 区间 [%d,%d)", hotbarGlyphOffset, hotbarQuadOffset, quadEnd)
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
		HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 1280, 720, render.NewUploadBudget(1024),
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
	if err := renderer.Prepare(core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, HealthOverlay{}, 0, 0, render.NewUploadBudget(1024)); err != nil {
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
	budget := render.NewUploadBudget(1024)
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

	atlas, err := render.NewGlyphAtlas(dev)
	if err != nil {
		t.Fatalf("NewGlyphAtlas: %v", err)
	}
	defer atlas.Release()
	renderer := NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas, assets.NewRegistry())
	defer renderer.Release()
	if err := renderer.Prepare(
		fullTestInventory(), true, 0, nil, nil, MiningOverlay{},
		HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 128, 128, render.NewUploadBudget(1<<20),
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

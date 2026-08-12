package hud

import (
	"encoding/binary"
	"math"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/render"
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
	if gotUV != ([4]float32{}) || face.Color != render.ItemColor(item) {
		t.Fatalf("非方块物品 %d face=%+v，想要程序化色块 %v", item, face, render.ItemColor(item))
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

func fakeNameTagGlyph(advance float32) render.Glyph {
	return render.Glyph{
		U0: 0.1, V0: 0.2, U1: 0.3, V1: 0.4,
		Advance: advance, BearingY: 10, Width: 8, Height: 12,
	}
}

type fakeNameTagAtlas struct {
	glyphs   map[rune]render.Glyph
	tofu     render.Glyph
	view     *nameTagTestView
	releases int
}

func newFakeNameTagAtlas() *fakeNameTagAtlas {
	glyphs := make(map[rune]render.Glyph)
	for _, char := range []rune{'A', 'V', ' ', '中', '文'} {
		glyphs[char] = fakeNameTagGlyph(10)
	}
	return &fakeNameTagAtlas{
		glyphs: glyphs,
		tofu:   fakeNameTagGlyph(13),
		view:   &nameTagTestView{},
	}
}

func (*fakeNameTagAtlas) Request(string) {}

func (*fakeNameTagAtlas) FlushUploads(*render.UploadBudget) error { return nil }

func (atlas *fakeNameTagAtlas) Glyph(char rune) render.Glyph {
	if glyph, ok := atlas.glyphs[char]; ok {
		return glyph
	}
	return atlas.tofu
}

func (*fakeNameTagAtlas) Kern(rune, rune) float32 { return 0 }

func (atlas *fakeNameTagAtlas) TextureView() gfx.TextureView { return atlas.view }

type allocationGlyphSource struct {
	requestCount int
	flushErr     error
}

func (source *allocationGlyphSource) Request(string) {
	source.requestCount++
}

func (source *allocationGlyphSource) FlushUploads(*render.UploadBudget) error {
	return source.flushErr
}

func (*allocationGlyphSource) Glyph(rune) render.Glyph {
	return render.Glyph{Advance: 8, BearingX: 1, BearingY: 10, Width: 7, Height: 12}
}

func (*allocationGlyphSource) Kern(rune, rune) float32      { return 0.25 }
func (*allocationGlyphSource) TextureView() gfx.TextureView { return nil }

type nameTagTestDevice struct {
	buffers       []*nameTagTestBuffer
	pipelineDescs []gfx.RenderPipelineDesc
	pipelines     []*nameTagTestPipeline
	textures      []*nameTagTestTexture
	bind          *nameTagTestBindGroup
	sampler       *nameTagTestSampler
}

func (d *nameTagTestDevice) CreateBuffer(desc gfx.BufferDesc) gfx.Buffer {
	buffer := &nameTagTestBuffer{desc: desc}
	d.buffers = append(d.buffers, buffer)
	return buffer
}

func (*nameTagTestDevice) CreateShaderModule(string) gfx.ShaderModule { return &nameTagTestShader{} }

func (d *nameTagTestDevice) CreateRenderPipeline(desc gfx.RenderPipelineDesc) gfx.RenderPipeline {
	pipeline := &nameTagTestPipeline{label: desc.Label}
	d.pipelineDescs = append(d.pipelineDescs, desc)
	d.pipelines = append(d.pipelines, pipeline)
	return pipeline
}

func (*nameTagTestDevice) CreateComputePipeline(gfx.ComputePipelineDesc) gfx.ComputePipeline {
	panic("unexpected compute pipeline")
}

func (d *nameTagTestDevice) CreateBindGroup(gfx.BindGroupDesc) gfx.BindGroup {
	d.bind = &nameTagTestBindGroup{}
	return d.bind
}

func (d *nameTagTestDevice) CreateTexture(desc gfx.TextureDesc) gfx.Texture {
	texture := &nameTagTestTexture{desc: desc, view: &nameTagTestView{}}
	d.textures = append(d.textures, texture)
	return texture
}

func (d *nameTagTestDevice) CreateSampler(gfx.SamplerDesc) gfx.Sampler {
	d.sampler = &nameTagTestSampler{}
	return d.sampler
}

func (*nameTagTestDevice) CreateCommandEncoder() gfx.CommandEncoder {
	panic("unexpected encoder")
}
func (*nameTagTestDevice) Submit(...gfx.CommandBuffer) {}
func (*nameTagTestDevice) Poll(bool)                   {}
func (*nameTagTestDevice) Release()                    {}

func (d *nameTagTestDevice) bufferByLabel(t *testing.T, label string) *nameTagTestBuffer {
	t.Helper()
	for _, buffer := range d.buffers {
		if buffer.desc.Label == label {
			return buffer
		}
	}
	t.Fatalf("buffer %q was not created", label)
	return nil
}

type nameTagTestBuffer struct {
	desc      gfx.BufferDesc
	lastWrite []byte
	writes    int
	releases  int
}

func (b *nameTagTestBuffer) Size() uint64 { return b.desc.Size }

func (b *nameTagTestBuffer) Write(_ uint64, data []byte) {
	b.lastWrite = append(b.lastWrite[:0], data...)
	b.writes++
}

func (*nameTagTestBuffer) ReadBack() []byte { panic("unexpected readback") }
func (b *nameTagTestBuffer) Release()       { b.releases++ }

type nameTagTestShader struct{}

func (*nameTagTestShader) Release() {}

type nameTagTestPipeline struct {
	label    string
	releases int
}

func (pipeline *nameTagTestPipeline) Release() { pipeline.releases++ }

type nameTagTestBindGroup struct{ releases int }

func (group *nameTagTestBindGroup) Release() { group.releases++ }

type nameTagTestSampler struct{ releases int }

func (sampler *nameTagTestSampler) Release() { sampler.releases++ }

type nameTagTestTexture struct {
	desc     gfx.TextureDesc
	view     *nameTagTestView
	pixels   []byte
	releases int
}

func (texture *nameTagTestTexture) View(gfx.TextureViewDesc) gfx.TextureView {
	return texture.view
}

func (texture *nameTagTestTexture) WriteLayer(_ uint32, _ uint32, pixels []byte) {
	texture.pixels = append(texture.pixels[:0], pixels...)
}

func (*nameTagTestTexture) WriteRegion(uint32, uint32, uint32, uint32, uint32, uint32, []byte) {
	panic("unexpected texture region")
}

func (*nameTagTestTexture) ReadLayer(uint32, uint32) []byte {
	panic("unexpected texture read")
}

func (texture *nameTagTestTexture) Release() { texture.releases++ }

type nameTagTestEncoder struct {
	passes []*nameTagTestPass
}

func (encoder *nameTagTestEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	pass := &nameTagTestPass{desc: desc}
	encoder.passes = append(encoder.passes, pass)
	return pass
}

func (*nameTagTestEncoder) BeginComputePass(string) gfx.ComputePass {
	panic("unexpected compute pass")
}

func (*nameTagTestEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("unexpected buffer copy")
}

func (*nameTagTestEncoder) Finish() gfx.CommandBuffer {
	panic("unexpected finish")
}

type nameTagTestPass struct {
	desc           gfx.RenderPassDesc
	pipelineLabels []string
	drawInstances  []uint32
	ended          bool
}

func (pass *nameTagTestPass) SetPipeline(pipeline gfx.RenderPipeline) {
	pass.pipelineLabels = append(pass.pipelineLabels, pipeline.(*nameTagTestPipeline).label)
}

func (*nameTagTestPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*nameTagTestPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*nameTagTestPass) SetIndexBuffer(gfx.Buffer, uint64) {
	panic("unexpected index buffer")
}
func (*nameTagTestPass) DrawIndexedIndirect(gfx.Buffer, uint64) {
	panic("unexpected indirect draw")
}

func (pass *nameTagTestPass) Draw(vertices, instances uint32) {
	if vertices != 6 {
		panic("hotbar quad did not use six vertices")
	}
	pass.drawInstances = append(pass.drawInstances, instances)
}

func (pass *nameTagTestPass) End() { pass.ended = true }

type nameTagTestView struct {
	releases int
}

func (view *nameTagTestView) Release() { view.releases++ }

func float32At(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
}

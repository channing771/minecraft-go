package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
)

const (
	// HUD 固定容量：1 个选中框 + 9 个栏位背景 + 最多 9 个物品色块。
	maxHotbarQuads = 1 + core.HotbarSlots*2
	// 数量最多两位数（1..64），每格最多两个数字。
	maxHotbarGlyphs = core.HotbarSlots * 2

	hotbarInstanceBytes  = 48
	hotbarViewportOffset = 0
	hotbarViewportBytes  = 16
	hotbarQuadOffset     = 256
	hotbarQuadSize       = maxHotbarQuads * hotbarInstanceBytes
	hotbarGlyphOffset    = 1280
	hotbarGlyphSize      = maxHotbarGlyphs * hotbarInstanceBytes
	hotbarUploadBytes    = hotbarGlyphOffset + hotbarGlyphSize

	hotbarSlotSize     = float32(48)
	hotbarSlotGap      = float32(4)
	hotbarBottomMargin = float32(24)
	hotbarSelectBorder = float32(3)
	hotbarSwatchInset  = float32(10)
	hotbarDigitMargin  = float32(3)
)

//go:embed shader/hotbar.wgsl
var hotbarShader string

// hotbarDigits 是 HUD 需要的全部字形，登录后不再增长。
const hotbarDigits = "0123456789"

type hotbarInstance struct {
	X, Y, Width, Height float32
	U0, V0, U1, V1      float32
	Color               [4]float32
}

type hotbarLayout struct {
	quads  []hotbarInstance
	glyphs []hotbarInstance
}

// HotbarRenderer 以固定容量绘制 9 格快捷栏 HUD。
// 它只消费已确认的权威快捷栏值，不做任何本地预测。
type HotbarRenderer struct {
	atlas GlyphSource

	dynamic       gfx.Buffer
	quadPipeline  gfx.RenderPipeline
	glyphPipeline gfx.RenderPipeline
	bind          gfx.BindGroup
	sampler       gfx.Sampler

	layout hotbarLayout
	upload []byte
}

func NewHotbarRenderer(
	dev gfx.Device,
	colorFormat gfx.TextureFormat,
	atlas GlyphSource,
) *HotbarRenderer {
	renderer := &HotbarRenderer{
		atlas:  atlas,
		upload: make([]byte, hotbarUploadBytes),
		layout: hotbarLayout{
			quads:  make([]hotbarInstance, 0, maxHotbarQuads),
			glyphs: make([]hotbarInstance, 0, maxHotbarGlyphs),
		},
	}
	renderer.dynamic = dev.CreateBuffer(gfx.BufferDesc{
		Label: "hotbar dynamic upload",
		Size:  hotbarUploadBytes,
		Usage: gfx.BufferUsageUniform | gfx.BufferUsageStorage | gfx.BufferUsageCopyDst,
	})
	layout := gfx.BindGroupLayout{
		Label: "hotbar layout",
		Entries: []gfx.BindGroupLayoutEntry{
			{Binding: 0, Type: gfx.BindingUniformBuffer, VisibleIn: gfx.StageVertex},
			{Binding: 1, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{Binding: 2, Type: gfx.BindingStorageBufferRO, VisibleIn: gfx.StageVertex},
			{
				Binding: 3, Type: gfx.BindingSampledTextureFloat,
				VisibleIn: gfx.StageFragment, ViewDimension: gfx.TextureViewDimension2D,
			},
			{Binding: 4, Type: gfx.BindingSampler, VisibleIn: gfx.StageFragment},
		},
	}
	module := dev.CreateShaderModule(hotbarShader)
	renderer.quadPipeline = dev.CreateRenderPipeline(hotbarPipelineDesc(
		"hotbar quad", module, colorFormat, layout, "quad_vs", "quad_fs",
	))
	renderer.glyphPipeline = dev.CreateRenderPipeline(hotbarPipelineDesc(
		"hotbar glyph", module, colorFormat, layout, "glyph_vs", "glyph_fs",
	))
	module.Release()
	renderer.sampler = dev.CreateSampler(gfx.SamplerDesc{
		Label: "hotbar glyph sampler", MagFilter: gfx.FilterLinear, MinFilter: gfx.FilterLinear,
		MipFilter: gfx.FilterNearest, Address: gfx.AddressClampToEdge,
	})
	renderer.bind = dev.CreateBindGroup(gfx.BindGroupDesc{
		Label:  "hotbar resources",
		Layout: layout,
		Entries: []gfx.BindGroupEntry{
			{
				Binding: 0, Buffer: renderer.dynamic,
				Offset: hotbarViewportOffset, Size: hotbarViewportBytes,
			},
			{
				Binding: 1, Buffer: renderer.dynamic,
				Offset: hotbarQuadOffset, Size: hotbarQuadSize,
			},
			{
				Binding: 2, Buffer: renderer.dynamic,
				Offset: hotbarGlyphOffset, Size: hotbarGlyphSize,
			},
			{Binding: 3, Texture: atlas.TextureView()},
			{Binding: 4, Sampler: renderer.sampler},
		},
	})
	return renderer
}

func hotbarPipelineDesc(
	label string,
	module gfx.ShaderModule,
	colorFormat gfx.TextureFormat,
	layout gfx.BindGroupLayout,
	vertexEntry, fragmentEntry string,
) gfx.RenderPipelineDesc {
	return gfx.RenderPipelineDesc{
		Label: label, Shader: module,
		VertexEntry: vertexEntry, FragmentEntry: fragmentEntry,
		BindGroups:  []gfx.BindGroupLayout{layout},
		ColorFormat: colorFormat,
		Blend:       gfx.BlendAlpha,
	}
}

// Prepare 按 framebuffer 尺寸与完整快捷栏值重建固定布局。
func (renderer *HotbarRenderer) Prepare(
	hotbar core.Hotbar,
	width, height uint32,
	budget *UploadBudget,
) error {
	renderer.atlas.Request(hotbarDigits)
	if err := renderer.atlas.FlushUploads(budget); err != nil {
		return err
	}
	layoutHotbar(&renderer.layout, renderer.atlas, hotbar, float32(width), float32(height))
	encodeHotbarViewport(
		renderer.upload[hotbarViewportOffset:hotbarViewportOffset+hotbarViewportBytes],
		float32(width), float32(height),
	)
	encodeHotbarInstances(
		renderer.upload[hotbarQuadOffset:hotbarQuadOffset+hotbarQuadSize],
		renderer.layout.quads,
	)
	encodeHotbarInstances(
		renderer.upload[hotbarGlyphOffset:hotbarUploadBytes],
		renderer.layout.glyphs,
	)
	return nil
}

// Render 在 terrain、avatar 与 name tag 之后以屏幕空间透明 pass 绘制 HUD。
func (renderer *HotbarRenderer) Render(encoder gfx.CommandEncoder, target gfx.TextureView) {
	if len(renderer.layout.quads) == 0 {
		return
	}
	uploadBytes := hotbarGlyphOffset + len(renderer.layout.glyphs)*hotbarInstanceBytes
	renderer.dynamic.Write(0, renderer.upload[:uploadBytes])
	pass := encoder.BeginRenderPass(gfx.RenderPassDesc{
		Label: "hotbar pass", ColorView: target, LoadClear: false,
	})
	pass.SetBindGroup(0, renderer.bind)
	pass.SetPipeline(renderer.quadPipeline)
	pass.Draw(6, uint32(len(renderer.layout.quads)))
	if len(renderer.layout.glyphs) != 0 {
		pass.SetPipeline(renderer.glyphPipeline)
		pass.Draw(6, uint32(len(renderer.layout.glyphs)))
	}
	pass.End()
}

// layoutHotbar 只依赖 framebuffer 尺寸与快捷栏值，产出固定上限的实例。
func layoutHotbar(
	dst *hotbarLayout,
	atlas GlyphSource,
	hotbar core.Hotbar,
	width, height float32,
) hotbarLayout {
	if dst == nil {
		dst = &hotbarLayout{
			quads:  make([]hotbarInstance, 0, maxHotbarQuads),
			glyphs: make([]hotbarInstance, 0, maxHotbarGlyphs),
		}
	}
	dst.quads = dst.quads[:0]
	dst.glyphs = dst.glyphs[:0]
	if width <= 0 || height <= 0 || !hotbar.Valid() {
		return *dst
	}

	total := core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap
	originX := (width - total) * 0.5
	originY := height - hotbarBottomMargin - hotbarSlotSize
	slotX := func(slot int) float32 {
		return originX + float32(slot)*(hotbarSlotSize+hotbarSlotGap)
	}

	// 选中框先绘制，随后的栏位背景只覆盖内部，留下可见边框。
	selected := slotX(int(hotbar.Selected))
	dst.quads = append(dst.quads, hotbarInstance{
		X:      selected - hotbarSelectBorder,
		Y:      originY - hotbarSelectBorder,
		Width:  hotbarSlotSize + 2*hotbarSelectBorder,
		Height: hotbarSlotSize + 2*hotbarSelectBorder,
		Color:  [4]float32{1, 1, 1, 0.92},
	})
	for slot := range core.HotbarSlots {
		dst.quads = append(dst.quads, hotbarInstance{
			X: slotX(slot), Y: originY,
			Width: hotbarSlotSize, Height: hotbarSlotSize,
			Color: [4]float32{0.05, 0.05, 0.06, 0.62},
		})
	}
	for slot, stack := range hotbar.Slots {
		if stack.Item == core.ItemNone {
			continue
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X:      slotX(slot) + hotbarSwatchInset,
			Y:      originY + hotbarSwatchInset,
			Width:  hotbarSlotSize - 2*hotbarSwatchInset,
			Height: hotbarSlotSize - 2*hotbarSwatchInset,
			Color:  hotbarItemColor(stack.Item),
		})
	}
	for slot, stack := range hotbar.Slots {
		if stack.Item == core.ItemNone {
			continue
		}
		appendHotbarCount(dst, atlas, stack.Count, slotX(slot), originY)
	}
	return *dst
}

// appendHotbarCount 在栏位右下角排布最多两位数量数字。
func appendHotbarCount(
	dst *hotbarLayout,
	atlas GlyphSource,
	count uint8,
	slotX, slotY float32,
) {
	var digits [2]rune
	length := 0
	if count >= 10 {
		digits[length] = rune('0' + count/10)
		length++
	}
	digits[length] = rune('0' + count%10)
	length++

	advance := float32(0)
	for index := range length {
		advance += atlas.Glyph(digits[index]).Advance
	}
	penX := slotX + hotbarSlotSize - hotbarDigitMargin - advance
	baseline := slotY + hotbarSlotSize - hotbarDigitMargin
	for index := range length {
		glyph := atlas.Glyph(digits[index])
		dst.glyphs = append(dst.glyphs, hotbarInstance{
			X:      penX + glyph.BearingX,
			Y:      baseline - glyph.BearingY,
			Width:  glyph.Width,
			Height: glyph.Height,
			U0:     glyph.U0, V0: glyph.V0, U1: glyph.U1, V1: glyph.V1,
			Color: [4]float32{1, 1, 1, 1},
		})
		penX += glyph.Advance
	}
}

// hotbarItemColor 与三个程序化方块的基色保持一致。
func hotbarItemColor(item core.ItemID) [4]float32 {
	switch item {
	case core.ItemStone:
		return [4]float32{128.0 / 255, 128.0 / 255, 128.0 / 255, 1}
	case core.ItemDirt:
		return [4]float32{134.0 / 255, 96.0 / 255, 67.0 / 255, 1}
	case core.ItemGrass:
		return [4]float32{88.0 / 255, 140.0 / 255, 60.0 / 255, 1}
	default:
		return [4]float32{}
	}
}

func encodeHotbarViewport(dst []byte, width, height float32) []byte {
	out := dst[:hotbarViewportBytes]
	for index, value := range [4]float32{width, height, 0, 0} {
		binary.LittleEndian.PutUint32(out[index*4:], math.Float32bits(value))
	}
	return out
}

func encodeHotbarInstances(dst []byte, instances []hotbarInstance) []byte {
	out := dst[:len(instances)*hotbarInstanceBytes]
	for index, instance := range instances {
		values := [12]float32{
			instance.X, instance.Y, instance.Width, instance.Height,
			instance.U0, instance.V0, instance.U1, instance.V1,
			instance.Color[0], instance.Color[1], instance.Color[2], instance.Color[3],
		}
		base := index * hotbarInstanceBytes
		for offset, value := range values {
			binary.LittleEndian.PutUint32(out[base+offset*4:], math.Float32bits(value))
		}
	}
	return out
}

// Release 只释放渲染器自有句柄；字形 atlas 与其视图由应用持有。
func (renderer *HotbarRenderer) Release() {
	if renderer.bind != nil {
		renderer.bind.Release()
		renderer.bind = nil
	}
	if renderer.glyphPipeline != nil {
		renderer.glyphPipeline.Release()
		renderer.glyphPipeline = nil
	}
	if renderer.quadPipeline != nil {
		renderer.quadPipeline.Release()
		renderer.quadPipeline = nil
	}
	if renderer.sampler != nil {
		renderer.sampler.Release()
		renderer.sampler = nil
	}
	if renderer.dynamic != nil {
		renderer.dynamic.Release()
		renderer.dynamic = nil
	}
}

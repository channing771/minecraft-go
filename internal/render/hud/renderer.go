package hud

import (
	_ "embed"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/render"
)

//go:embed shader/hotbar.wgsl
var hotbarShader string

// HotbarRenderer 以固定容量绘制 9 格快捷栏 HUD。
// 它只消费已确认的权威快捷栏值，不做任何本地预测。
type HotbarRenderer struct {
	atlas render.GlyphSource

	dynamic       gfx.Buffer
	quadPipeline  gfx.RenderPipeline
	glyphPipeline gfx.RenderPipeline
	bind          gfx.BindGroup
	sampler       gfx.Sampler
	hudTexture    gfx.Texture
	hudView       gfx.TextureView

	layout hotbarLayout
	upload []byte
}

func NewHotbarRenderer(
	dev gfx.Device,
	colorFormat gfx.TextureFormat,
	atlas render.GlyphSource,
	blocks *assets.Registry,
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
			{
				Binding: 5, Type: gfx.BindingSampledTextureFloat,
				VisibleIn: gfx.StageFragment, ViewDimension: gfx.TextureViewDimension2D,
			},
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
		Label: "hotbar atlas sampler", MagFilter: gfx.FilterNearest, MinFilter: gfx.FilterNearest,
		MipFilter: gfx.FilterNearest, Address: gfx.AddressClampToEdge,
	})
	renderer.hudTexture = dev.CreateTexture(gfx.TextureDesc{
		Label: "hotbar texture atlas", Width: uint32(hotbarTextureWidth), Height: hotbarTextureSize,
		Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageBinding | gfx.TextureUsageCopyDst,
	})
	renderer.hudTexture.WriteLayer(0, 0, buildHotbarTextureAtlas(blocks))
	renderer.hudView = renderer.hudTexture.View(gfx.TextureViewDesc{})
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
			{Binding: 5, Texture: renderer.hudView},
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

// Prepare 按 framebuffer 尺寸与完整物品状态重建固定布局。
// open 为 false 时只布局底部 9 格 HUD；为 true 时布局 3×9 背包加 1×9 快捷栏。
// source 是已选中的来源格（背包 0..35，容器打开时可以是统一栏位），-1 表示没有来源高亮。
// overlay 与 chest 至多一个非 nil：分别画熔炉三格或箱子 27 格取代配方行。
// health 是服务端已确认的生命值；它的显示与 inventory 无关——即便背包尚未确认，
// 只要生命值本身已确认就会绘制，反之亦然。
func (renderer *HotbarRenderer) Prepare(
	inventory core.Inventory,
	inventoryConfirmed bool,
	open bool,
	source int,
	overlay *FurnaceOverlay,
	chest *ChestOverlay,
	mining MiningOverlay,
	health HealthOverlay,
	chat ChatOverlay,
	width, height uint32,
	budget *render.UploadBudget,
) error {
	textRequested := false
	if inventoryConfirmed {
		renderer.atlas.Request(hotbarDigits)
		textRequested = true
	}
	textRequested = requestChatText(renderer.atlas, chat) || textRequested
	if textRequested {
		if err := renderer.atlas.FlushUploads(budget); err != nil {
			return err
		}
	}
	if inventoryConfirmed {
		layoutInventory(
			&renderer.layout, renderer.atlas, inventory, open, source, overlay, chest, mining,
			float32(width), float32(height),
		)
	} else {
		renderer.layout.quads = renderer.layout.quads[:0]
		renderer.layout.glyphs = renderer.layout.glyphs[:0]
		renderer.layout.scale = hudScale(open, float32(width), float32(height))
		renderer.layout.open = open
	}
	appendHealthBar(&renderer.layout, renderer.atlas, health, float32(width), float32(height))
	appendChatOverlay(&renderer.layout, renderer.atlas, chat, float32(width), float32(height))
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
	if renderer.hudView != nil {
		renderer.hudView.Release()
		renderer.hudView = nil
	}
	if renderer.hudTexture != nil {
		renderer.hudTexture.Release()
		renderer.hudTexture = nil
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

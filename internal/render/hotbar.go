package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/mesh"
)

const (
	// 固定容量按最坏布局：背包分组面板、两种高亮、36 个栏位、双层物品内容、
	// 九格快捷栏耐久条，再加最大的容器叠加层与十段生命条。
	maxHotbarQuads = openInventoryPanelQuads + 2 + core.InventorySlots + core.InventorySlots*2 +
		core.HotbarSlots*2 + maxOverlayQuads + healthQuads
	// 数量最多两位数（2..64），每个数字包含阴影与前景两个实例。
	maxHotbarGlyphs = core.InventorySlots*4 + maxOverlayGlyphs

	// 六条固定配方（含箱子）：面板 + 每行两个栏位与双层物品色块、按钮和加号。
	recipeQuads  = 1 + 6*9
	recipeGlyphs = 14
	// 熔炉视图：面板、三个栏位、双层物品色块、两条进度条底与填充。
	furnaceQuads = 1 + 3 + 3*2 + 4
	// 三个熔炉格各最多两位数量。
	furnaceGlyphs = 12
	// 箱子视图：面板、27 格背景，加最多 27 个双层物品色块。
	chestQuads = 1 + core.ChestSlots + core.ChestSlots*2
	// 箱子每格最多两位数量。
	chestGlyphs = core.ChestSlots * 4

	maxOverlayQuads  = max(recipeQuads, furnaceQuads, chestQuads)
	maxOverlayGlyphs = max(recipeGlyphs, furnaceGlyphs, chestGlyphs)

	// 生命值 HUD：十颗空心和最多十颗填充爱心，不绘制背景面板。
	healthQuads = healthSegmentCount * 2
	// 打开背包时依次绘制外框、背包区、快捷栏区和分隔线。
	openInventoryPanelQuads = 4

	hotbarInstanceBytes  = 48
	hotbarViewportOffset = 0
	hotbarViewportBytes  = 16
	hotbarQuadOffset     = 256
	hotbarQuadSize       = maxHotbarQuads * hotbarInstanceBytes
	hotbarGlyphOffset    = (hotbarQuadOffset + hotbarQuadSize + 255) &^ 255
	hotbarGlyphSize      = maxHotbarGlyphs * hotbarInstanceBytes
	hotbarUploadBytes    = hotbarGlyphOffset + hotbarGlyphSize

	hotbarSlotSize      = float32(48)
	hotbarSlotGap       = float32(4)
	hotbarBottomMargin  = float32(24)
	hotbarSelectBorder  = float32(3)
	hotbarPanelPadding  = float32(6)
	hotbarSwatchInset   = float32(10)
	hotbarSwatchBorder  = float32(2)
	hotbarDigitMargin   = float32(3)
	hotbarDigitTracking = float32(-2)
	durabilityBarHeight = float32(3)
	durabilityBarInset  = float32(4)
	miningBarWidth      = float32(240)
	miningBarHeight     = float32(12)
	miningBarGap        = float32(16)
	// 背包界面在快捷栏之上再放 3 行，并与快捷栏留出一段间隔。
	inventoryRowGap = float32(12)
	// 合成行位于背包最上一行之上。
	recipeRowGap      = float32(16)
	recipeButtonWidth = float32(96)
	// 熔炉三格与两条进度条排在背包最上一行之上。
	furnaceBarHeight = float32(10)
	furnaceBarGap    = float32(6)

	healthSegmentCount = 10
	healthHeartSize    = float32(16)
	healthHeartGap     = float32(1)

	// HUD 图集前两格是代码生成的空心/实心爱心，后续格按 ItemID 放置真实方块顶面。
	hotbarTextureSize       = 16
	hotbarEmptyHeartColumn  = 0
	hotbarFullHeartColumn   = 1
	hotbarBlockColumnOffset = 2
	hotbarTextureColumns    = hotbarBlockColumnOffset + int(core.ItemChest) + 1
	hotbarTextureWidth      = hotbarTextureColumns * hotbarTextureSize

	hudEdgeMargin = float32(8)
	// 固定合成最上沿到快捷栏下沿（含面板边距）的设计高度。
	openHUDHeight = float32(566)
)

// ponytail: 当前只有六条固定配方；需要分页或分类时再引入共享目录。
var inventoryRecipeIDs = [...]core.RecipeID{
	core.RecipeStoneBricks,
	core.RecipeFurnace,
	core.RecipeIronBlock,
	core.RecipeStonePickaxe,
	core.RecipeIronPickaxe,
	core.RecipeChest,
}

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
	scale  float32
	open   bool
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
	hudTexture    gfx.Texture
	hudView       gfx.TextureView

	layout hotbarLayout
	upload []byte
}

func NewHotbarRenderer(
	dev gfx.Device,
	colorFormat gfx.TextureFormat,
	atlas GlyphSource,
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
	open bool,
	source int,
	overlay *FurnaceOverlay,
	chest *ChestOverlay,
	mining MiningOverlay,
	health HealthOverlay,
	width, height uint32,
	budget *UploadBudget,
) error {
	renderer.atlas.Request(hotbarDigits)
	if err := renderer.atlas.FlushUploads(budget); err != nil {
		return err
	}
	layoutInventory(
		&renderer.layout, renderer.atlas, inventory, open, source, overlay, chest, mining,
		float32(width), float32(height),
	)
	appendHealthBar(&renderer.layout, renderer.atlas, health, float32(width), float32(height))
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

// layoutInventory 只依赖 framebuffer 尺寸、完整物品状态、界面开关与容器叠加值，
// 产出固定上限的实例；关闭时只有底部 9 格 HUD。overlay 与 chest 至多一个非 nil：
// overlay 非 nil 时画熔炉三格与两条进度条，chest 非 nil 时画箱子 27 格，
// 两者都为 nil 时画固定合成行。
func layoutInventory(
	dst *hotbarLayout,
	atlas GlyphSource,
	inventory core.Inventory,
	open bool,
	source int,
	overlay *FurnaceOverlay,
	chest *ChestOverlay,
	mining MiningOverlay,
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
	dst.scale = hudScale(open, width, height)
	dst.open = open
	if width <= 0 || height <= 0 || !inventory.Valid() {
		return *dst
	}

	scale := dst.scale
	slotSize := hotbarSlotSize * scale
	selectBorder := hotbarSelectBorder * scale
	slots := core.HotbarSlots
	if open {
		slots = core.InventorySlots
	}
	appendInventoryPanel(dst, open, width, height, scale)
	// 高亮先于栏位表面绘制，栏位只覆盖内部并留下像素边框。
	selectedX, selectedY := inventorySlotOrigin(int(inventory.Hotbar.Selected), open, width, height)
	dst.quads = append(dst.quads, hotbarInstance{
		X:      selectedX - selectBorder,
		Y:      selectedY - selectBorder,
		Width:  slotSize + 2*selectBorder,
		Height: slotSize + 2*selectBorder,
		Color:  [4]float32{1, 0.72, 0.24, 0.98},
	})
	if open && source >= 0 {
		if sourceX, sourceY, ok := containerSourceOrigin(source, overlay, chest, width, height); ok {
			dst.quads = append(dst.quads, hotbarInstance{
				X:      sourceX - selectBorder,
				Y:      sourceY - selectBorder,
				Width:  slotSize + 2*selectBorder,
				Height: slotSize + 2*selectBorder,
				Color:  [4]float32{0.25, 0.72, 1, 0.98},
			})
		}
	}
	for slot := range slots {
		x, y := inventorySlotOrigin(slot, open, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: x, Y: y,
			Width: slotSize, Height: slotSize,
			Color: [4]float32{0.12, 0.13, 0.14, 0.90},
		})
	}
	for slot := range slots {
		stack, _ := inventory.Slot(uint8(slot))
		if stack.Item == core.ItemNone {
			continue
		}
		x, y := inventorySlotOrigin(slot, open, width, height)
		appendItemTile(dst, stack.Item, x, y, scale)
	}
	for slot := range slots {
		stack, _ := inventory.Slot(uint8(slot))
		if stack.Item == core.ItemNone {
			continue
		}
		x, y := inventorySlotOrigin(slot, open, width, height)
		appendHotbarCountScaled(dst, atlas, stack.Count, x, y, scale)
	}
	for slot, stack := range inventory.Hotbar.Slots {
		appendDurabilityBarScaled(dst, slot, stack, open, width, height, scale)
	}
	if open {
		switch {
		case chest != nil:
			appendChestGrid(dst, atlas, *chest, width, height)
		case overlay != nil:
			appendFurnaceRow(dst, atlas, *overlay, width, height)
		default:
			appendRecipeRows(dst, atlas, inventory, width, height)
		}
	} else {
		appendMiningBar(dst, mining, width, height)
	}
	return *dst
}

func appendInventoryPanel(dst *hotbarLayout, open bool, width, height, scale float32) {
	left, hotbarY := inventorySlotOrigin(0, open, width, height)
	top := hotbarY
	if open {
		_, top = inventorySlotOrigin(core.HotbarSlots, true, width, height)
	}
	padding := hotbarPanelPadding * scale
	totalWidth := (core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap) * scale
	dst.quads = append(dst.quads, hotbarInstance{
		X: left - padding, Y: top - padding,
		Width: totalWidth + 2*padding, Height: hotbarY + hotbarSlotSize*scale - top + 2*padding,
		Color: [4]float32{0.025, 0.03, 0.035, 0.88},
	})
	if !open {
		return
	}
	_, backpackBottomY := inventorySlotOrigin(core.InventorySlots-1, true, width, height)
	innerPadding := padding * 0.5
	dst.quads = append(dst.quads,
		hotbarInstance{
			X: left - innerPadding, Y: top - innerPadding,
			Width:  totalWidth + 2*innerPadding,
			Height: backpackBottomY + hotbarSlotSize*scale - top + 2*innerPadding,
			Color:  [4]float32{0.045, 0.052, 0.06, 0.94},
		},
		hotbarInstance{
			X: left - innerPadding, Y: hotbarY - innerPadding,
			Width: totalWidth + 2*innerPadding, Height: hotbarSlotSize*scale + 2*innerPadding,
			Color: [4]float32{0.06, 0.052, 0.04, 0.94},
		},
		hotbarInstance{
			X: left, Y: (backpackBottomY + hotbarSlotSize*scale + hotbarY) * 0.5,
			Width: totalWidth, Height: max(scale*2, 1),
			Color: [4]float32{0.25, 0.30, 0.34, 0.92},
		},
	)
}

// appendItemTile 用已有矩形画出带暗边的物品；可放置方块采样真实注册表材质，
// 其他物品继续使用程序化色块。
func appendItemTile(dst *hotbarLayout, item core.ItemID, x, y, scale float32) {
	color := hotbarItemColor(item)
	inset := hotbarSwatchInset * scale
	border := hotbarSwatchBorder * scale
	size := (hotbarSlotSize - 2*hotbarSwatchInset) * scale
	face := hotbarInstance{
		X: x + inset + border, Y: y + inset + border,
		Width: size - 2*border, Height: size - 2*border,
		Color: color,
	}
	if uv, ok := hotbarItemUV(item); ok {
		face.U0, face.V0, face.U1, face.V1 = uv[0], uv[1], uv[2], uv[3]
		face.Color = [4]float32{1, 1, 1, 1}
	}
	dst.quads = append(dst.quads, hotbarInstance{
		X: x + inset, Y: y + inset, Width: size, Height: size,
		Color: [4]float32{color[0] * 0.35, color[1] * 0.35, color[2] * 0.35, color[3]},
	}, face)
}

// containerSourceOrigin 返回来源高亮格的左上角像素坐标；索引落在当前打开的容器视图之外
// 时返回 false。overlay 与 chest 至多一个非 nil，分别把索引 36 之后解释为熔炉三格或
// 箱子 27 格。
func containerSourceOrigin(
	source int,
	overlay *FurnaceOverlay,
	chest *ChestOverlay,
	width, height float32,
) (float32, float32, bool) {
	switch {
	case source < core.InventorySlots:
		x, y := inventorySlotOrigin(source, true, width, height)
		return x, y, true
	case chest != nil && source < core.ChestViewSlots:
		x, y := chestSlotOrigin(source-core.InventorySlots, width, height)
		return x, y, true
	case overlay != nil && source < core.FurnaceViewSlots:
		x, y := recipeSlotOrigin(source-core.InventorySlots, width, height)
		return x, y, true
	default:
		return 0, 0, false
	}
}

// appendDurabilityBar 在快捷栏栏位下沿绘制背景和剩余耐久比例填充。
// 只有存在耐久上限且尚未满耐久的物品才显示。
func appendDurabilityBar(
	dst *hotbarLayout,
	slot int,
	stack core.ItemStack,
	width, height float32,
) {
	appendDurabilityBarScaled(dst, slot, stack, false, width, height, hudScale(false, width, height))
}

func appendDurabilityBarScaled(
	dst *hotbarLayout,
	slot int,
	stack core.ItemStack,
	open bool,
	width, height, scale float32,
) {
	maxDurability, ok := core.ItemMaxDurability(stack.Item)
	if !ok || maxDurability == 0 || stack.Durability == 0 || stack.Durability >= maxDurability {
		return
	}
	slotX, slotY := inventorySlotOrigin(slot, open, width, height)
	barWidth := (hotbarSlotSize - durabilityBarInset*2) * scale
	x := slotX + durabilityBarInset*scale
	y := slotY + (hotbarSlotSize-durabilityBarInset-durabilityBarHeight)*scale
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth, Height: durabilityBarHeight * scale,
		Color: [4]float32{0.05, 0.05, 0.06, 0.85},
	})
	fraction := float32(stack.Durability) / float32(maxDurability)
	color := [4]float32{0.30, 0.78, 0.36, 0.95}
	if fraction < 0.25 {
		color = [4]float32{0.90, 0.35, 0.25, 0.95}
	}
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth * fraction, Height: durabilityBarHeight * scale,
		Color: color,
	})
}

// MiningOverlay 是最后确认的权威采掘状态；渲染器不会自行推进它。
type MiningOverlay struct {
	Active        bool
	ProgressTicks uint16
	RequiredTicks uint16
	Harvestable   bool
}

// appendMiningBar 在快捷栏上方绘制固定背景和权威比例填充。
func appendMiningBar(dst *hotbarLayout, overlay MiningOverlay, width, height float32) {
	if !overlay.Active || overlay.RequiredTicks == 0 {
		return
	}
	scale := hudScale(false, width, height)
	barWidth := miningBarWidth * scale
	barHeight := miningBarHeight * scale
	x := (width - barWidth) * 0.5
	_, hotbarY := inventorySlotOrigin(0, false, width, height)
	y := hotbarY - (miningBarGap+miningBarHeight)*scale
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth, Height: barHeight,
		Color: [4]float32{0.05, 0.05, 0.06, 0.78},
	})
	fraction := float32(overlay.ProgressTicks) / float32(overlay.RequiredTicks)
	if fraction <= 0 {
		return
	}
	color := [4]float32{0.95, 0.55, 0.15, 0.95}
	if overlay.Harvestable {
		color = [4]float32{0.30, 0.78, 0.36, 0.95}
	}
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth * min(fraction, 1), Height: barHeight,
		Color: color,
	})
}

// HealthOverlay 是服务端已确认的生命值。它是 render 本地值，由 app 从
// Predictor 的已确认镜像转换；Confirmed 为 false 时表示尚未收到权威状态，
// 渲染器不会画出任何生命值——绝不显示预测或陈旧的数值。
type HealthOverlay struct {
	Confirmed bool
	Value     uint8
}

// appendHealthBar 在 framebuffer 左下角绘制一排无背景的服务端确认爱心；
// 每颗两点，奇数值画半颗，打开背包不会改变其尺度或位置。
func appendHealthBar(dst *hotbarLayout, atlas GlyphSource, health HealthOverlay, width, height float32) {
	if !health.Confirmed || width <= 0 || height <= 0 {
		return
	}
	_ = atlas
	scale := hudScale(false, width, height)
	x := hudEdgeMargin * scale
	heartSize := healthHeartSize * scale
	heartGap := healthHeartGap * scale
	y := height - (hudEdgeMargin+healthHeartSize)*scale
	emptyUV := hotbarTextureUV(hotbarEmptyHeartColumn)
	for segment := range healthSegmentCount {
		dst.quads = append(dst.quads, hotbarInstance{
			X: x + float32(segment)*(heartSize+heartGap), Y: y,
			Width: heartSize, Height: heartSize,
			U0: emptyUV[0], V0: emptyUV[1], U1: emptyUV[2], V1: emptyUV[3],
			Color: [4]float32{1, 1, 1, 1},
		})
	}
	value := min(health.Value, uint8(core.MaxHealth))
	filled := (int(value) + 1) / 2
	fullUV := hotbarTextureUV(hotbarFullHeartColumn)
	for segment := range filled {
		fillWidth := heartSize
		fillU1 := fullUV[2]
		if segment == filled-1 && value%2 != 0 {
			fillWidth *= 0.5
			fillU1 = (fullUV[0] + fullUV[2]) * 0.5
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X: x + float32(segment)*(heartSize+heartGap), Y: y,
			Width: fillWidth, Height: heartSize,
			U0: fullUV[0], V0: fullUV[1], U1: fillU1, V1: fullUV[3],
			Color: [4]float32{1, 1, 1, 1},
		})
	}
}

// FurnaceOverlay 是熔炉界面需要显示的全部权威值。
// 它是 render 本地值，由 app 从已确认镜像转换，渲染层不依赖协议类型。
type FurnaceOverlay struct {
	Input         core.ItemStack
	Fuel          core.ItemStack
	Output        core.ItemStack
	ProgressTicks uint8
	BurnTicks     uint16
}

// appendFurnaceRow 绘制熔炉的输入、燃料、输出三格与燃烧、熔炼两条进度条。
func appendFurnaceRow(
	dst *hotbarLayout,
	atlas GlyphSource,
	overlay FurnaceOverlay,
	width, height float32,
) {
	scale := hudScale(true, width, height)
	padding := hotbarPanelPadding * scale
	panelX, slotY := recipeSlotOrigin(0, width, height)
	_, barTop := furnaceBarOrigin(width, height)
	panelWidth := (3*hotbarSlotSize+2*hotbarSlotGap)*scale + 2*padding
	dst.quads = append(dst.quads, hotbarInstance{
		X: panelX - padding, Y: barTop - padding,
		Width: panelWidth, Height: slotY + hotbarSlotSize*scale - barTop + 2*padding,
		Color: [4]float32{0.025, 0.03, 0.035, 0.88},
	})
	stacks := [3]core.ItemStack{overlay.Input, overlay.Fuel, overlay.Output}
	for index, stack := range stacks {
		x, y := recipeSlotOrigin(index, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: x, Y: y,
			Width: hotbarSlotSize * scale, Height: hotbarSlotSize * scale,
			Color: [4]float32{0.12, 0.13, 0.14, 0.90},
		})
		if stack.Item == core.ItemNone {
			continue
		}
		appendItemTile(dst, stack.Item, x, y, scale)
		appendHotbarCountScaled(dst, atlas, stack.Count, x, y, scale)
	}

	// 两条进度条分别显示剩余燃烧量与当前熔炼进度。
	bars := [2]struct {
		fraction float32
		color    [4]float32
	}{
		{float32(overlay.BurnTicks) / float32(core.FurnaceBurnTicks),
			[4]float32{0.95, 0.55, 0.15, 0.95}},
		{float32(overlay.ProgressTicks) / float32(core.FurnaceSmeltTicks),
			[4]float32{0.35, 0.75, 1, 0.95}},
	}
	barX, barTop := furnaceBarOrigin(width, height)
	barWidth := (3*hotbarSlotSize + 2*hotbarSlotGap) * scale
	for index, bar := range bars {
		y := barTop + float32(index)*(furnaceBarHeight+furnaceBarGap)*scale
		dst.quads = append(dst.quads, hotbarInstance{
			X: barX, Y: y, Width: barWidth, Height: furnaceBarHeight * scale,
			Color: [4]float32{0.05, 0.05, 0.06, 0.62},
		})
		if bar.fraction <= 0 {
			continue
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X: barX, Y: y,
			Width: barWidth * min(bar.fraction, 1), Height: furnaceBarHeight * scale,
			Color: bar.color,
		})
	}
}

// furnaceBarOrigin 返回两条进度条的左上角像素坐标。
func furnaceBarOrigin(width, height float32) (float32, float32) {
	x, y := recipeSlotOrigin(0, width, height)
	scale := hudScale(true, width, height)
	return x, y - (2*furnaceBarGap+2*furnaceBarHeight)*scale
}

// FurnaceSlotAt 把光标像素坐标映射为熔炉界面的统一索引 0..38。
// 它与绘制共用同一套几何常量；界外返回 false。
func FurnaceSlotAt(cursorX, cursorY float64, width, height uint32) (uint8, bool) {
	if slot, ok := InventorySlotAt(cursorX, cursorY, width, height); ok {
		return slot, true
	}
	if width == 0 || height == 0 {
		return 0, false
	}
	x, y := float32(cursorX), float32(cursorY)
	slotSize := hotbarSlotSize * hudScale(true, float32(width), float32(height))
	for index := range 3 {
		left, top := recipeSlotOrigin(index, float32(width), float32(height))
		if x >= left && x < left+slotSize && y >= top && y < top+slotSize {
			return core.InventorySlots + uint8(index), true
		}
	}
	return 0, false
}

// ChestOverlay 是箱子界面需要显示的全部权威值：27 个格子的物品。
// 它是 render 本地值，由 app 从已确认镜像转换，渲染层不依赖协议类型。
type ChestOverlay struct {
	Items [core.ChestSlots]core.ItemStack
}

// appendChestGrid 绘制箱子 27 格背景、物品色块与数量，按统一栏位 36..62 排布成 3 行 9 列。
func appendChestGrid(
	dst *hotbarLayout,
	atlas GlyphSource,
	overlay ChestOverlay,
	width, height float32,
) {
	scale := hudScale(true, width, height)
	padding := hotbarPanelPadding * scale
	left, bottomY := chestSlotOrigin(0, width, height)
	_, top := chestSlotOrigin(core.ChestSlots-core.HotbarSlots, width, height)
	totalWidth := (core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap) * scale
	dst.quads = append(dst.quads, hotbarInstance{
		X: left - padding, Y: top - padding,
		Width: totalWidth + 2*padding, Height: bottomY + hotbarSlotSize*scale - top + 2*padding,
		Color: [4]float32{0.025, 0.03, 0.035, 0.88},
	})
	for index := range core.ChestSlots {
		x, y := chestSlotOrigin(index, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: x, Y: y,
			Width: hotbarSlotSize * scale, Height: hotbarSlotSize * scale,
			Color: [4]float32{0.12, 0.13, 0.14, 0.90},
		})
	}
	for index, stack := range overlay.Items {
		if stack.Item == core.ItemNone {
			continue
		}
		x, y := chestSlotOrigin(index, width, height)
		appendItemTile(dst, stack.Item, x, y, scale)
		appendHotbarCountScaled(dst, atlas, stack.Count, x, y, scale)
	}
}

// chestSlotOrigin 返回箱子统一索引 0..26 对应格子的左上角像素坐标：3 行 9 列，
// 紧贴在背包最上一行之上，index 0 在最下面一行、与熔炉/配方行共用同一起点。
func chestSlotOrigin(index int, width, height float32) (float32, float32) {
	row := index / core.HotbarSlots
	column := index % core.HotbarSlots
	x, y := recipeSlotOrigin(column, width, height)
	return x, y - float32(row)*(hotbarSlotSize+hotbarSlotGap)*hudScale(true, width, height)
}

// ChestSlotAt 把光标像素坐标映射为箱子界面的统一索引 0..62。
// 它与绘制共用同一套几何常量；界外返回 false。
func ChestSlotAt(cursorX, cursorY float64, width, height uint32) (uint8, bool) {
	if slot, ok := InventorySlotAt(cursorX, cursorY, width, height); ok {
		return slot, true
	}
	if width == 0 || height == 0 {
		return 0, false
	}
	x, y := float32(cursorX), float32(cursorY)
	slotSize := hotbarSlotSize * hudScale(true, float32(width), float32(height))
	for index := range core.ChestSlots {
		left, top := chestSlotOrigin(index, float32(width), float32(height))
		if x >= left && x < left+slotSize && y >= top && y < top+slotSize {
			return core.InventorySlots + uint8(index), true
		}
	}
	return 0, false
}

// appendRecipeRows 绘制六条固定配方及各自的一次合成按钮。
func appendRecipeRows(
	dst *hotbarLayout,
	atlas GlyphSource,
	inventory core.Inventory,
	width, height float32,
) {
	scale := hudScale(true, width, height)
	padding := hotbarPanelPadding * scale
	left, bottomY := craftingRecipeSlotOrigin(0, 0, width, height)
	_, top := craftingRecipeSlotOrigin(len(inventoryRecipeIDs)-1, 0, width, height)
	buttonX, _ := craftingRecipeButtonOrigin(0, width, height)
	dst.quads = append(dst.quads, hotbarInstance{
		X: left - padding, Y: top - padding,
		Width:  buttonX + recipeButtonWidth*scale - left + 2*padding,
		Height: bottomY + hotbarSlotSize*scale - top + 2*padding,
		Color:  [4]float32{0.025, 0.03, 0.035, 0.88},
	})
	for row, recipeID := range inventoryRecipeIDs {
		recipe, ok := core.Recipe(recipeID)
		if !ok {
			continue
		}
		inputX, inputY := craftingRecipeSlotOrigin(row, 0, width, height)
		outputX, outputY := craftingRecipeSlotOrigin(row, 1, width, height)
		for _, entry := range [2]struct {
			stack core.ItemStack
			x, y  float32
		}{
			{recipe.Input, inputX, inputY},
			{recipe.Output, outputX, outputY},
		} {
			dst.quads = append(dst.quads, hotbarInstance{
				X: entry.x, Y: entry.y,
				Width: hotbarSlotSize * scale, Height: hotbarSlotSize * scale,
				Color: [4]float32{0.12, 0.13, 0.14, 0.90},
			})
			appendItemTile(dst, entry.stack.Item, entry.x, entry.y, scale)
			appendHotbarCountScaled(dst, atlas, entry.stack.Count, entry.x, entry.y, scale)
		}

		// 按钮颜色只表示是否可合成；服务端每次仍重新验证。
		color := [4]float32{0.18, 0.19, 0.20, 0.94}
		markColor := [4]float32{0.55, 0.57, 0.60, 0.96}
		if _, craftable := inventory.Craft(recipeID); craftable {
			color = [4]float32{0.22, 0.64, 0.32, 0.98}
			markColor = [4]float32{0.90, 1, 0.90, 1}
		}
		buttonX, buttonY := craftingRecipeButtonOrigin(row, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: buttonX, Y: buttonY,
			Width: recipeButtonWidth * scale, Height: hotbarSlotSize * scale,
			Color: color,
		})
		centerX := buttonX + recipeButtonWidth*scale*0.5
		centerY := buttonY + hotbarSlotSize*scale*0.5
		dst.quads = append(dst.quads, hotbarInstance{
			X: centerX - 7*scale, Y: centerY - 2*scale,
			Width: 14 * scale, Height: 4 * scale, Color: markColor,
		}, hotbarInstance{
			X: centerX - 2*scale, Y: centerY - 7*scale,
			Width: 4 * scale, Height: 14 * scale, Color: markColor,
		})
	}
}

// recipeSlotOrigin 返回配方行第 index 个格子的左上角像素坐标。
func recipeSlotOrigin(index int, width, height float32) (float32, float32) {
	x, _ := inventorySlotOrigin(index, true, width, height)
	_, topRowY := inventorySlotOrigin(core.HotbarSlots, true, width, height)
	scale := hudScale(true, width, height)
	return x, topRowY - (recipeRowGap+hotbarSlotSize)*scale
}

// craftingRecipeSlotOrigin 返回第 row 条配方中第 index 个格子的左上角像素坐标。
func craftingRecipeSlotOrigin(row, index int, width, height float32) (float32, float32) {
	x, y := recipeSlotOrigin(index, width, height)
	return x, y - float32(row)*(hotbarSlotSize+hotbarSlotGap)*hudScale(true, width, height)
}

// craftingRecipeButtonOrigin 返回第 row 条配方按钮的左上角像素坐标。
func craftingRecipeButtonOrigin(row int, width, height float32) (float32, float32) {
	return craftingRecipeSlotOrigin(row, 2, width, height)
}

// RecipeButtonAt 报告光标是否命中任一固定合成按钮，命中时返回配方 ID。
// 它与绘制共用同一套几何常量。
func RecipeButtonAt(cursorX, cursorY float64, width, height uint32) (core.RecipeID, bool) {
	if width == 0 || height == 0 {
		return 0, false
	}
	x, y := float32(cursorX), float32(cursorY)
	scale := hudScale(true, float32(width), float32(height))
	for row, recipe := range inventoryRecipeIDs {
		left, top := craftingRecipeButtonOrigin(row, float32(width), float32(height))
		if x >= left && x < left+recipeButtonWidth*scale && y >= top && y < top+hotbarSlotSize*scale {
			return recipe, true
		}
	}
	return 0, false
}

// inventorySlotOrigin 返回统一索引对应格子的左上角像素坐标。
// 索引 0..8 是底部快捷栏行，9..35 是其上方自上而下的三行背包。
func inventorySlotOrigin(slot int, open bool, width, height float32) (float32, float32) {
	scale := hudScale(open, width, height)
	column := slot % core.HotbarSlots
	total := (core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap) * scale
	x := (width-total)*0.5 + float32(column)*(hotbarSlotSize+hotbarSlotGap)*scale
	hotbarY := height - (hotbarBottomMargin+hotbarSlotSize)*scale
	if !open || slot < core.HotbarSlots {
		return x, hotbarY
	}
	// 背包第 0 行在最上方，第 2 行紧邻快捷栏。
	row := (slot - core.HotbarSlots) / core.HotbarSlots
	rowsAbove := float32(2 - row)
	y := hotbarY - (inventoryRowGap+(rowsAbove+1)*hotbarSlotSize+rowsAbove*hotbarSlotGap)*scale
	return x, y
}

func hudScale(open bool, width, height float32) float32 {
	if width <= 0 || height <= 0 {
		return 1
	}
	scale := float32(1)
	contentWidth := core.HotbarSlots*hotbarSlotSize +
		(core.HotbarSlots-1)*hotbarSlotGap + 2*hotbarPanelPadding
	if available := width - 2*hudEdgeMargin; available < contentWidth {
		scale = max(available/contentWidth, 0)
	}
	if open {
		if available := height - 2*hudEdgeMargin; available < openHUDHeight {
			scale = min(scale, max(available/openHUDHeight, 0))
		}
	}
	return scale
}

// InventorySlotAt 把光标像素坐标映射为背包界面中的统一索引 0..35。
// 命中格子之外返回 false，与绘制共用同一套几何常量。
func InventorySlotAt(cursorX, cursorY float64, width, height uint32) (uint8, bool) {
	if width == 0 || height == 0 {
		return 0, false
	}
	x, y := float32(cursorX), float32(cursorY)
	slotSize := hotbarSlotSize * hudScale(true, float32(width), float32(height))
	for slot := range core.InventorySlots {
		left, top := inventorySlotOrigin(slot, true, float32(width), float32(height))
		if x >= left && x < left+slotSize && y >= top && y < top+slotSize {
			return uint8(slot), true
		}
	}
	return 0, false
}

// appendHotbarCount 在栏位右下角排布最多两位数量数字。
func appendHotbarCount(
	dst *hotbarLayout,
	atlas GlyphSource,
	count uint8,
	slotX, slotY float32,
) {
	appendHotbarCountScaled(dst, atlas, count, slotX, slotY, 1)
}

func appendHotbarCountScaled(
	dst *hotbarLayout,
	atlas GlyphSource,
	count uint8,
	slotX, slotY, scale float32,
) {
	if count <= 1 {
		return
	}
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
		advance += atlas.Glyph(digits[index]).Advance * scale
	}
	tracking := float32(0)
	if length == 2 {
		tracking = hotbarDigitTracking * scale
		advance += tracking
	}
	penX := slotX + (hotbarSlotSize-hotbarDigitMargin)*scale - advance
	baseline := slotY + (hotbarSlotSize-hotbarDigitMargin)*scale
	for index := range length {
		glyph := atlas.Glyph(digits[index])
		dst.glyphs = append(dst.glyphs, hotbarInstance{
			X:      penX + glyph.BearingX*scale + scale,
			Y:      baseline - glyph.BearingY*scale + scale,
			Width:  glyph.Width * scale,
			Height: glyph.Height * scale,
			U0:     glyph.U0, V0: glyph.V0, U1: glyph.U1, V1: glyph.V1,
			Color: [4]float32{0.02, 0.025, 0.03, 0.95},
		})
		penX += glyph.Advance * scale
		if index+1 < length {
			penX += tracking
		}
	}
	penX = slotX + (hotbarSlotSize-hotbarDigitMargin)*scale - advance
	for index := range length {
		glyph := atlas.Glyph(digits[index])
		dst.glyphs = append(dst.glyphs, hotbarInstance{
			X:      penX + glyph.BearingX*scale,
			Y:      baseline - glyph.BearingY*scale,
			Width:  glyph.Width * scale,
			Height: glyph.Height * scale,
			U0:     glyph.U0, V0: glyph.V0, U1: glyph.U1, V1: glyph.V1,
			Color: [4]float32{1, 0.94, 0.78, 1},
		})
		penX += glyph.Advance * scale
		if index+1 < length {
			penX += tracking
		}
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
	case core.ItemStoneBrick:
		return [4]float32{122.0 / 255, 118.0 / 255, 112.0 / 255, 1}
	case core.ItemCoal:
		return [4]float32{38.0 / 255, 38.0 / 255, 40.0 / 255, 1}
	case core.ItemRawIron:
		return [4]float32{196.0 / 255, 154.0 / 255, 118.0 / 255, 1}
	case core.ItemIronIngot:
		return [4]float32{220.0 / 255, 220.0 / 255, 224.0 / 255, 1}
	case core.ItemFurnace:
		return [4]float32{88.0 / 255, 86.0 / 255, 88.0 / 255, 1}
	case core.ItemIronBlock:
		return [4]float32{214.0 / 255, 214.0 / 255, 216.0 / 255, 1}
	case core.ItemChest:
		return [4]float32{156.0 / 255, 108.0 / 255, 58.0 / 255, 1}
	case core.ItemStonePickaxe:
		return [4]float32{104.0 / 255, 112.0 / 255, 120.0 / 255, 1}
	case core.ItemIronPickaxe:
		return [4]float32{190.0 / 255, 198.0 / 255, 210.0 / 255, 1}
	case core.ItemBrokenStonePickaxe:
		return [4]float32{66.0 / 255, 60.0 / 255, 58.0 / 255, 1}
	case core.ItemBrokenIronPickaxe:
		return [4]float32{96.0 / 255, 88.0 / 255, 92.0 / 255, 1}
	default:
		return [4]float32{}
	}
}

func buildHotbarTextureAtlas(registry *assets.Registry) []byte {
	pixels := make([]byte, hotbarTextureWidth*hotbarTextureSize*4)
	paintHotbarHeart(pixels, hotbarEmptyHeartColumn, false)
	paintHotbarHeart(pixels, hotbarFullHeartColumn, true)
	for item := core.ItemStone; item <= core.ItemChest; item++ {
		block, ok := core.ItemPlacement(item)
		if !ok {
			continue
		}
		layer := registry.Material(block, mesh.FacePosY)
		copyHotbarTextureCell(pixels, hotbarBlockColumnOffset+int(item), registry.LayerRGBA(int(layer)))
	}
	return pixels
}

func copyHotbarTextureCell(dst []byte, column int, src []byte) {
	for y := range hotbarTextureSize {
		dstStart := (y*hotbarTextureWidth + column*hotbarTextureSize) * 4
		srcStart := y * hotbarTextureSize * 4
		copy(dst[dstStart:dstStart+hotbarTextureSize*4], src[srcStart:srcStart+hotbarTextureSize*4])
	}
}

func paintHotbarHeart(dst []byte, column int, full bool) {
	for y := range hotbarTextureSize {
		for x := range hotbarTextureSize {
			if !hotbarHeartPixel(x, y) {
				continue
			}
			border := !hotbarHeartPixel(x-1, y) || !hotbarHeartPixel(x+1, y) ||
				!hotbarHeartPixel(x, y-1) || !hotbarHeartPixel(x, y+1)
			color := [4]byte{44, 20, 24, 255}
			if border {
				color = [4]byte{96, 28, 36, 255}
			} else if full {
				color = [4]byte{226, 42, 52, 255}
				if x <= 5 && y >= 4 && y <= 6 {
					color = [4]byte{255, 105, 112, 255}
				}
			}
			if full && border {
				color = [4]byte{128, 22, 30, 255}
			}
			offset := (y*hotbarTextureWidth + column*hotbarTextureSize + x) * 4
			copy(dst[offset:offset+4], color[:])
		}
	}
}

func hotbarHeartPixel(x, y int) bool {
	switch y {
	case 2:
		return x >= 2 && x <= 6 || x >= 9 && x <= 13
	case 3:
		return x >= 1 && x <= 14
	case 4, 5, 6, 7:
		return x >= 0 && x <= 15
	case 8:
		return x >= 1 && x <= 14
	case 9:
		return x >= 2 && x <= 13
	case 10:
		return x >= 3 && x <= 12
	case 11:
		return x >= 4 && x <= 11
	case 12:
		return x >= 5 && x <= 10
	case 13:
		return x >= 6 && x <= 9
	default:
		return false
	}
}

func hotbarTextureUV(column int) [4]float32 {
	left := float32(column*hotbarTextureSize) / float32(hotbarTextureWidth)
	right := float32((column+1)*hotbarTextureSize) / float32(hotbarTextureWidth)
	return [4]float32{left, 0, right, 1}
}

func hotbarItemUV(item core.ItemID) ([4]float32, bool) {
	if _, ok := core.ItemPlacement(item); !ok {
		return [4]float32{}, false
	}
	return hotbarTextureUV(hotbarBlockColumnOffset + int(item)), true
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

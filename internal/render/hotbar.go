package render

import (
	_ "embed"
	"encoding/binary"
	"math"

	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
)

const (
	// 固定容量按最坏布局：选中框 + 来源高亮 + 36 个栏位背景 + 36 个色块，
	// 九格快捷栏耐久条，再加固定合成行与熔炉视图中较大的一个。
	maxHotbarQuads = 2 + core.InventorySlots*2 + core.HotbarSlots*2 + maxOverlayQuads
	// 数量最多两位数（1..64），每格最多两个数字。
	maxHotbarGlyphs = core.InventorySlots*2 + maxOverlayGlyphs

	// 五条固定配方，每条包含输入格、输出格、合成按钮和两个数量。
	recipeQuads  = 5 * 3
	recipeGlyphs = 5 * 2
	// 熔炉视图：三个栏位背景、三个物品色块、两条进度条底与两条进度条填充。
	furnaceQuads = 3 + 3 + 4
	// 三个熔炉格各最多两位数量。
	furnaceGlyphs = 6

	maxOverlayQuads  = max(recipeQuads, furnaceQuads)
	maxOverlayGlyphs = max(recipeGlyphs, furnaceGlyphs)

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
	hotbarSwatchInset   = float32(10)
	hotbarDigitMargin   = float32(3)
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
)

// ponytail: 当前只有五条固定配方；需要分页或分类时再引入共享目录。
var inventoryRecipeIDs = [...]core.RecipeID{
	core.RecipeStoneBricks,
	core.RecipeFurnace,
	core.RecipeIronBlock,
	core.RecipeStonePickaxe,
	core.RecipeIronPickaxe,
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

// Prepare 按 framebuffer 尺寸与完整物品状态重建固定布局。
// open 为 false 时只布局底部 9 格 HUD；为 true 时布局 3×9 背包加 1×9 快捷栏。
// source 是已选中的来源格 0..35，-1 表示没有来源高亮。
func (renderer *HotbarRenderer) Prepare(
	inventory core.Inventory,
	open bool,
	source int,
	overlay *FurnaceOverlay,
	mining MiningOverlay,
	width, height uint32,
	budget *UploadBudget,
) error {
	renderer.atlas.Request(hotbarDigits)
	if err := renderer.atlas.FlushUploads(budget); err != nil {
		return err
	}
	layoutInventory(
		&renderer.layout, renderer.atlas, inventory, open, source, overlay, mining,
		float32(width), float32(height),
	)
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

// layoutInventory 只依赖 framebuffer 尺寸、完整物品状态、界面开关与熔炉叠加值，
// 产出固定上限的实例；关闭时只有底部 9 格 HUD，overlay 非 nil 时画熔炉视图而不是合成行。
func layoutInventory(
	dst *hotbarLayout,
	atlas GlyphSource,
	inventory core.Inventory,
	open bool,
	source int,
	overlay *FurnaceOverlay,
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
	if width <= 0 || height <= 0 || !inventory.Valid() {
		return *dst
	}

	slots := core.HotbarSlots
	if open {
		slots = core.InventorySlots
	}
	// 选中框先绘制，随后的栏位背景只覆盖内部，留下可见边框。
	selectedX, selectedY := inventorySlotOrigin(int(inventory.Hotbar.Selected), open, width, height)
	dst.quads = append(dst.quads, hotbarInstance{
		X:      selectedX - hotbarSelectBorder,
		Y:      selectedY - hotbarSelectBorder,
		Width:  hotbarSlotSize + 2*hotbarSelectBorder,
		Height: hotbarSlotSize + 2*hotbarSelectBorder,
		Color:  [4]float32{1, 1, 1, 0.92},
	})
	if open && source >= 0 &&
		(source < core.InventorySlots || overlay != nil && source < core.FurnaceViewSlots) {
		sourceX, sourceY := inventorySlotOrigin(source, open, width, height)
		if source >= core.InventorySlots {
			sourceX, sourceY = recipeSlotOrigin(source-core.InventorySlots, width, height)
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X:      sourceX - hotbarSelectBorder,
			Y:      sourceY - hotbarSelectBorder,
			Width:  hotbarSlotSize + 2*hotbarSelectBorder,
			Height: hotbarSlotSize + 2*hotbarSelectBorder,
			Color:  [4]float32{0.35, 0.75, 1, 0.95},
		})
	}
	for slot := range slots {
		x, y := inventorySlotOrigin(slot, open, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: x, Y: y,
			Width: hotbarSlotSize, Height: hotbarSlotSize,
			Color: [4]float32{0.05, 0.05, 0.06, 0.62},
		})
	}
	for slot := range slots {
		stack, _ := inventory.Slot(uint8(slot))
		if stack.Item == core.ItemNone {
			continue
		}
		x, y := inventorySlotOrigin(slot, open, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X:      x + hotbarSwatchInset,
			Y:      y + hotbarSwatchInset,
			Width:  hotbarSlotSize - 2*hotbarSwatchInset,
			Height: hotbarSlotSize - 2*hotbarSwatchInset,
			Color:  hotbarItemColor(stack.Item),
		})
	}
	for slot := range slots {
		stack, _ := inventory.Slot(uint8(slot))
		if stack.Item == core.ItemNone {
			continue
		}
		x, y := inventorySlotOrigin(slot, open, width, height)
		appendHotbarCount(dst, atlas, stack.Count, x, y)
	}
	for slot, stack := range inventory.Hotbar.Slots {
		appendDurabilityBar(dst, slot, stack, width, height)
	}
	if open {
		if overlay != nil {
			appendFurnaceRow(dst, atlas, *overlay, width, height)
		} else {
			appendRecipeRows(dst, atlas, inventory, width, height)
		}
	} else {
		appendMiningBar(dst, mining, width, height)
	}
	return *dst
}

// appendDurabilityBar 在快捷栏栏位下沿绘制背景和剩余耐久比例填充。
// 只有存在耐久上限且尚未满耐久的物品才显示。
func appendDurabilityBar(
	dst *hotbarLayout,
	slot int,
	stack core.ItemStack,
	width, height float32,
) {
	maxDurability, ok := core.ItemMaxDurability(stack.Item)
	if !ok || maxDurability == 0 || stack.Durability == 0 || stack.Durability >= maxDurability {
		return
	}
	slotX, slotY := inventorySlotOrigin(slot, false, width, height)
	barWidth := hotbarSlotSize - durabilityBarInset*2
	x := slotX + durabilityBarInset
	y := slotY + hotbarSlotSize - durabilityBarInset - durabilityBarHeight
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth, Height: durabilityBarHeight,
		Color: [4]float32{0.05, 0.05, 0.06, 0.85},
	})
	fraction := float32(stack.Durability) / float32(maxDurability)
	color := [4]float32{0.30, 0.78, 0.36, 0.95}
	if fraction < 0.25 {
		color = [4]float32{0.90, 0.35, 0.25, 0.95}
	}
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: barWidth * fraction, Height: durabilityBarHeight,
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
	x := (width - miningBarWidth) * 0.5
	_, hotbarY := inventorySlotOrigin(0, false, width, height)
	y := hotbarY - miningBarGap - miningBarHeight
	dst.quads = append(dst.quads, hotbarInstance{
		X: x, Y: y, Width: miningBarWidth, Height: miningBarHeight,
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
		X: x, Y: y, Width: miningBarWidth * min(fraction, 1), Height: miningBarHeight,
		Color: color,
	})
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
	stacks := [3]core.ItemStack{overlay.Input, overlay.Fuel, overlay.Output}
	for index, stack := range stacks {
		x, y := recipeSlotOrigin(index, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: x, Y: y,
			Width: hotbarSlotSize, Height: hotbarSlotSize,
			Color: [4]float32{0.05, 0.05, 0.06, 0.62},
		})
		if stack.Item == core.ItemNone {
			continue
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X:      x + hotbarSwatchInset,
			Y:      y + hotbarSwatchInset,
			Width:  hotbarSlotSize - 2*hotbarSwatchInset,
			Height: hotbarSlotSize - 2*hotbarSwatchInset,
			Color:  hotbarItemColor(stack.Item),
		})
		appendHotbarCount(dst, atlas, stack.Count, x, y)
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
	barWidth := 3*hotbarSlotSize + 2*hotbarSlotGap
	for index, bar := range bars {
		y := barTop + float32(index)*(furnaceBarHeight+furnaceBarGap)
		dst.quads = append(dst.quads, hotbarInstance{
			X: barX, Y: y, Width: barWidth, Height: furnaceBarHeight,
			Color: [4]float32{0.05, 0.05, 0.06, 0.62},
		})
		if bar.fraction <= 0 {
			continue
		}
		dst.quads = append(dst.quads, hotbarInstance{
			X: barX, Y: y,
			Width: barWidth * min(bar.fraction, 1), Height: furnaceBarHeight,
			Color: bar.color,
		})
	}
}

// furnaceBarOrigin 返回两条进度条的左上角像素坐标。
func furnaceBarOrigin(width, height float32) (float32, float32) {
	x, y := recipeSlotOrigin(0, width, height)
	return x, y - furnaceBarGap - 2*furnaceBarHeight - furnaceBarGap
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
	for index := range 3 {
		left, top := recipeSlotOrigin(index, float32(width), float32(height))
		if x >= left && x < left+hotbarSlotSize && y >= top && y < top+hotbarSlotSize {
			return core.InventorySlots + uint8(index), true
		}
	}
	return 0, false
}

// appendRecipeRows 绘制五条固定配方及各自的一次合成按钮。
func appendRecipeRows(
	dst *hotbarLayout,
	atlas GlyphSource,
	inventory core.Inventory,
	width, height float32,
) {
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
				Width: hotbarSlotSize, Height: hotbarSlotSize,
				Color: hotbarItemColor(entry.stack.Item),
			})
			appendHotbarCount(dst, atlas, entry.stack.Count, entry.x, entry.y)
		}

		// 按钮颜色只表示是否可合成；服务端每次仍重新验证。
		color := [4]float32{0.28, 0.28, 0.30, 0.85}
		if _, craftable := inventory.Craft(recipeID); craftable {
			color = [4]float32{0.30, 0.68, 0.36, 0.95}
		}
		buttonX, buttonY := craftingRecipeButtonOrigin(row, width, height)
		dst.quads = append(dst.quads, hotbarInstance{
			X: buttonX, Y: buttonY,
			Width: recipeButtonWidth, Height: hotbarSlotSize,
			Color: color,
		})
	}
}

// recipeSlotOrigin 返回配方行第 index 个格子的左上角像素坐标。
func recipeSlotOrigin(index int, width, height float32) (float32, float32) {
	x, _ := inventorySlotOrigin(index, true, width, height)
	_, topRowY := inventorySlotOrigin(core.HotbarSlots, true, width, height)
	return x, topRowY - recipeRowGap - hotbarSlotSize
}

// craftingRecipeSlotOrigin 返回第 row 条配方中第 index 个格子的左上角像素坐标。
func craftingRecipeSlotOrigin(row, index int, width, height float32) (float32, float32) {
	x, y := recipeSlotOrigin(index, width, height)
	return x, y - float32(row)*(hotbarSlotSize+hotbarSlotGap)
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
	for row, recipe := range inventoryRecipeIDs {
		left, top := craftingRecipeButtonOrigin(row, float32(width), float32(height))
		if x >= left && x < left+recipeButtonWidth && y >= top && y < top+hotbarSlotSize {
			return recipe, true
		}
	}
	return 0, false
}

// inventorySlotOrigin 返回统一索引对应格子的左上角像素坐标。
// 索引 0..8 是底部快捷栏行，9..35 是其上方自上而下的三行背包。
func inventorySlotOrigin(slot int, open bool, width, height float32) (float32, float32) {
	column := slot % core.HotbarSlots
	total := core.HotbarSlots*hotbarSlotSize + (core.HotbarSlots-1)*hotbarSlotGap
	x := (width-total)*0.5 + float32(column)*(hotbarSlotSize+hotbarSlotGap)
	hotbarY := height - hotbarBottomMargin - hotbarSlotSize
	if !open || slot < core.HotbarSlots {
		return x, hotbarY
	}
	// 背包第 0 行在最上方，第 2 行紧邻快捷栏。
	row := (slot - core.HotbarSlots) / core.HotbarSlots
	rowsAbove := float32(2 - row)
	y := hotbarY - inventoryRowGap - (rowsAbove+1)*hotbarSlotSize - rowsAbove*hotbarSlotGap
	return x, y
}

// InventorySlotAt 把光标像素坐标映射为背包界面中的统一索引 0..35。
// 命中格子之外返回 false，与绘制共用同一套几何常量。
func InventorySlotAt(cursorX, cursorY float64, width, height uint32) (uint8, bool) {
	if width == 0 || height == 0 {
		return 0, false
	}
	x, y := float32(cursorX), float32(cursorY)
	for slot := range core.InventorySlots {
		left, top := inventorySlotOrigin(slot, true, float32(width), float32(height))
		if x >= left && x < left+hotbarSlotSize && y >= top && y < top+hotbarSlotSize {
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

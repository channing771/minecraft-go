package hud

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/render"
)

const (
	// 固定容量按最坏布局：背包分组面板、两种高亮、36 个栏位、双层物品内容、
	// 九格快捷栏耐久条，再加最大的容器叠加层与十段生命条。
	maxHotbarQuads = openInventoryPanelQuads + 2 + core.InventorySlots + core.InventorySlots*2 +
		core.HotbarSlots*2 + maxOverlayQuads + healthQuads
	// 数量最多两位数（2..64），每个数字包含阴影与前景两个实例。
	maxHotbarGlyphs = core.InventorySlots*4 + maxOverlayGlyphs

	// 打开背包时依次绘制外框、背包区、快捷栏区和分隔线。
	openInventoryPanelQuads = 4

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

	hudEdgeMargin = float32(8)
	// 固定合成最上沿到快捷栏下沿（含面板边距）的设计高度。
	openHUDHeight = float32(670)
)

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

// layoutInventory 只依赖 framebuffer 尺寸、完整物品状态、界面开关与容器叠加值，
// 产出固定上限的实例；关闭时只有底部 9 格 HUD。overlay 与 chest 至多一个非 nil：
// overlay 非 nil 时画熔炉三格与两条进度条，chest 非 nil 时画箱子 27 格，
// 两者都为 nil 时画固定合成行。
func layoutInventory(
	dst *hotbarLayout,
	atlas render.GlyphSource,
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
	color := render.ItemColor(item)
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
	atlas render.GlyphSource,
	count uint8,
	slotX, slotY float32,
) {
	appendHotbarCountScaled(dst, atlas, count, slotX, slotY, 1)
}
func appendHotbarCountScaled(
	dst *hotbarLayout,
	atlas render.GlyphSource,
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

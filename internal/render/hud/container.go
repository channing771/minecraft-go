package hud

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/render"
)

const (
	// 固定配方：面板 + 每行两个栏位与双层物品色块、按钮和加号。
	recipeQuads = 1 + len(inventoryRecipeIDs)*9
	// 数量为 1 的栏位不画数字，其余每位数字画阴影与前景两个实例。
	// 当前十条配方共 12 位数字：石砖 4/4、发光方块 4/4 各两位，其余各一位。
	recipeGlyphs = 24
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

	// 合成行位于背包最上一行之上。
	recipeRowGap      = float32(16)
	recipeButtonWidth = float32(96)
	// 熔炉三格与两条进度条排在背包最上一行之上。
	furnaceBarHeight = float32(10)
	furnaceBarGap    = float32(6)
)

// ponytail: 当前只有十条固定配方；需要分页或分类时再引入共享目录。
var inventoryRecipeIDs = [...]core.RecipeID{
	core.RecipeStoneBricks,
	core.RecipeFurnace,
	core.RecipeIronBlock,
	core.RecipeStonePickaxe,
	core.RecipeIronPickaxe,
	core.RecipeChest,
	core.RecipeOakPlanks,
	core.RecipeLightBlock,
	// 两条锄头配方按 ID 顺序追加在末尾；没有它们玩家在 UI 里拿不到锄头，
	// 也就翻不了地，整条农业闭环在客户端不可达。
	core.RecipeStoneHoe,
	core.RecipeIronHoe,
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
	atlas render.GlyphSource,
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
	atlas render.GlyphSource,
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

// appendRecipeRows 绘制全部固定配方及各自的一次合成按钮。
func appendRecipeRows(
	dst *hotbarLayout,
	atlas render.GlyphSource,
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

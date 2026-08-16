package hud

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/render"
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

func (*allocationGlyphSource) Kern(rune, rune) float32 { return 0.25 }

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

type nameTagTestView struct {
	releases int
}

func (view *nameTagTestView) Release() { view.releases++ }

func float32At(data []byte, offset int) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(data[offset : offset+4]))
}

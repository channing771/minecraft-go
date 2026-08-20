package hud

import (
	"testing"

	"github.com/channing771/mornlea/internal/render"
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
	// 氧气取未满值：只有这样预热路径才会真的走到氧气条，"零每帧分配、零新上传缓冲"
	// 这条断言才对它成立。传零值（未确认）会让氧气条整条被跳过、断言退化成空转。
	oxygen := OxygenOverlay{Confirmed: true, Value: 120}
	budget := render.NewUploadBudget(1024)
	if err := renderer.Prepare(inventory, true, true, 3, nil, nil, MiningOverlay{}, health, oxygen, ChatOverlay{}, 1280, 720, budget); err != nil {
		t.Fatalf("warm Prepare: %v", err)
	}
	allocations := testing.AllocsPerRun(1000, func() {
		source.requestCount = 0
		if err := renderer.Prepare(inventory, true, true, 3, nil, nil, MiningOverlay{}, health, oxygen, ChatOverlay{}, 1280, 720, budget); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("warmed hotbar Prepare allocations=%v want=0", allocations)
	}
}

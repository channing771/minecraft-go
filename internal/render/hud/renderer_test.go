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

// TestHotbarFixedUploadLayoutMatchesScenarioVersion 把 Hotbar HUD 的**固定上传
// 布局**钉成数值断言。
//
// 这三个数不是内部细节：bounded-benchmark-workload 主规格用「固定 GPU 上传
// 布局、offset 与每帧写入字节数是否移动」来判定 benchmark scenario 要不要升版
// （v15→v16 与 v17→v18 都是因它而升）。没有这条断言，改动 HUD 布局时无人会
// 注意到 scenario 身份已经该动了——而那正是 v16 加氧气条那次发生过的事
// （quad 236→238 恰好没跨过 256 字节对齐边界，offset 与总容量才没变）。
//
// 数值随 HUD 结构增长是正常的；改动本测试的期望值时必须同时判定 scenario
// 版本要不要升，并把结论写进变更产物。
func TestHotbarFixedUploadLayoutMatchesScenarioVersion(t *testing.T) {
	for _, test := range []struct {
		name      string
		got, want int
	}{
		{"quad 容量", maxHotbarQuads, 247},
		{"glyph 容量", maxHotbarGlyphs, 700},
		{"glyph offset", hotbarGlyphOffset, 12288},
		{"总容量", hotbarUploadBytes, 45888},
	} {
		if test.got != test.want {
			t.Errorf("%s=%d，想要 %d（改这个数就要重新判定 benchmark scenario 版本）",
				test.name, test.got, test.want)
		}
	}
}

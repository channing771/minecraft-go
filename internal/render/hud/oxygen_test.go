package hud

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// oxygenQuadsFor 返回给定氧气叠加值追加进 hotbarLayout 的 quad（不含任何既有内容）。
func oxygenQuadsFor(overlay OxygenOverlay, width, height float32) []hotbarInstance {
	var layout hotbarLayout
	appendOxygenBar(&layout, overlay, width, height)
	return layout.quads
}

// TestOxygenBarOnlyAppearsBelowFull 覆盖 spec Scenario「满氧时不占用界面」与
// 「氧气耗损时可见」。
//
// 两侧对照刻意落在规则明显分歧的地方——**quad 数**，而不是"画在某处的像素"：
// 满氧一个 quad 都不追加，未满恰好追加两个。若两侧都走同一条绘制路径、只是把条
// 画到看不见的位置，两次读数会相同、两条断言同时绿，本变更历轮评审已多次抓到
// 这种空转。
func TestOxygenBarOnlyAppearsBelowFull(t *testing.T) {
	for _, test := range []struct {
		name      string
		overlay   OxygenOverlay
		wantQuads int
	}{
		{"未确认", OxygenOverlay{Confirmed: false, Value: 10}, 0},
		{"满氧", OxygenOverlay{Confirmed: true, Value: core.MaxOxygenTicks}, 0},
		{"越界高值按满氧处理", OxygenOverlay{Confirmed: true, Value: core.MaxOxygenTicks + 50}, 0},
		{"差一 tick 就满", OxygenOverlay{Confirmed: true, Value: core.MaxOxygenTicks - 1}, oxygenQuads},
		{"半氧", OxygenOverlay{Confirmed: true, Value: core.MaxOxygenTicks / 2}, oxygenQuads},
		{"氧气归零", OxygenOverlay{Confirmed: true, Value: 0}, oxygenQuads},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendOxygenBar(&layout, test.overlay, 1280, 720)
			if len(layout.quads) != test.wantQuads {
				t.Fatalf("quads=%d，想要 %d", len(layout.quads), test.wantQuads)
			}
			// 氧气条是纯色 quad，不占图集列也不产生字形：这是"复用同一 HUD 图集
			// 与同一 pass、零新管线"在布局侧唯一可观察的形状。
			if len(layout.glyphs) != 0 {
				t.Fatalf("glyphs=%d，想要 0（氧气条不得引入第二个字体图集）", len(layout.glyphs))
			}
		})
	}
}

// TestOxygenBarPresentationTracksAuthoritativeValue 覆盖 spec Scenario
// 「氧气耗损时可见并随之变化」的第二条：呈现必须随权威值变化。
//
// 断言不是"非空"，而是**两个不同的非满值给出不同的填充宽度**，并且宽度随权威值
// 单调、与比例逐位吻合。只断言"非空"会让"把填充宽度写死成常数"这种变异全绿。
func TestOxygenBarPresentationTracksAuthoritativeValue(t *testing.T) {
	const width, height = 1280, 720
	values := []uint16{0, 1, core.MaxOxygenTicks / 4, core.MaxOxygenTicks / 2, core.MaxOxygenTicks - 1}
	widths := make([]float32, len(values))
	for index, value := range values {
		quads := oxygenQuadsFor(OxygenOverlay{Confirmed: true, Value: value}, width, height)
		if len(quads) != oxygenQuads {
			t.Fatalf("氧气 %d：quads=%d，想要 %d", value, len(quads), oxygenQuads)
		}
		background, fill := quads[0], quads[1]
		if background.Width != oxygenBarWidth || background.Height != oxygenBarHeight {
			t.Fatalf("氧气 %d：背景 %v×%v，想要 %v×%v",
				value, background.Width, background.Height, oxygenBarWidth, oxygenBarHeight)
		}
		want := oxygenBarWidth * float32(value) / float32(core.MaxOxygenTicks)
		if fill.Width != want {
			t.Fatalf("氧气 %d：填充宽度=%v，想要 %v", value, fill.Width, want)
		}
		widths[index] = fill.Width
	}
	for index := 1; index < len(widths); index++ {
		if widths[index] <= widths[index-1] {
			t.Fatalf("氧气 %d 与 %d 的填充宽度 %v/%v 没有随权威值变化",
				values[index-1], values[index], widths[index-1], widths[index])
		}
	}
}

// TestOxygenBarSitsAboveHealthBarInSameLayout 覆盖「画在生命值条上方」与
// 「写进现有 hotbar 布局」：两者进同一份 hotbarLayout.quads（因而同一个 pass
// 与同一块实例缓冲），左边沿对齐，氧气条整体位于生命条之上且不重叠。
func TestOxygenBarSitsAboveHealthBarInSameLayout(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	const width, height = 1280, 720
	var layout hotbarLayout
	layoutInventory(&layout, atlas, core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, width, height)
	base := len(layout.quads)
	appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: 10}, width, height)
	healthQuadCount := len(layout.quads) - base
	appendOxygenBar(&layout, OxygenOverlay{Confirmed: true, Value: 100}, width, height)
	if healthQuadCount == 0 {
		t.Fatal("夹具无效：生命条一个 quad 都没画，「上方」无从比较")
	}
	if len(layout.quads)-base-healthQuadCount != oxygenQuads {
		t.Fatalf("氧气条没有追加进同一份布局：新增 %d 个 quad",
			len(layout.quads)-base-healthQuadCount)
	}
	heart := layout.quads[base]
	bar := layout.quads[base+healthQuadCount]
	if bar.X != heart.X {
		t.Fatalf("氧气条 X=%v 与生命条 X=%v 未对齐", bar.X, heart.X)
	}
	// Y 轴向下增大：氧气条的下沿必须严格高于（数值上小于）生命条的上沿。
	if bar.Y+bar.Height > heart.Y {
		t.Fatalf("氧气条 (Y=%v,H=%v) 压到了生命条 (Y=%v) 上", bar.Y, bar.Height, heart.Y)
	}
	if bar.Y < 0 {
		t.Fatalf("氧气条 Y=%v 越出了 framebuffer 上沿", bar.Y)
	}
}

// TestOxygenBarRejectsDegenerateFramebuffer 与生命条同形：零尺寸 framebuffer
// 不得产生越界或退化几何。
func TestOxygenBarRejectsDegenerateFramebuffer(t *testing.T) {
	for _, size := range [][2]float32{{0, 720}, {1280, 0}} {
		if quads := oxygenQuadsFor(OxygenOverlay{Confirmed: true, Value: 10}, size[0], size[1]); len(quads) != 0 {
			t.Fatalf("framebuffer %v：quads=%d，想要 0", size, len(quads))
		}
	}
}

package hud

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
)

// 杀死变异：忽略 Confirmed 标记或画出预测值，会让 HUD 在收到权威状态前显示猜测值。
func TestAppendHealthBarDrawsOnlyConfirmedValues(t *testing.T) {
	atlas := newFakeNameTagAtlas()

	var unconfirmed hotbarLayout
	appendHealthBar(&unconfirmed, atlas, HealthOverlay{Confirmed: false, Value: 12}, 1280, 720)
	if len(unconfirmed.quads) != 0 || len(unconfirmed.glyphs) != 0 {
		t.Fatalf("未确认生命值 quads=%d glyphs=%d，想要都为 0", len(unconfirmed.quads), len(unconfirmed.glyphs))
	}

	for _, test := range []struct {
		name      string
		value     uint8
		wantQuads int
	}{
		{"零血", 0, 10},
		{"一点生命", 1, 11},
		{"满血", core.MaxHealth, 20},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: test.value}, 1280, 720)
			if len(layout.quads) != test.wantQuads || len(layout.glyphs) != 0 {
				t.Fatalf("确认生命值 quads/glyphs=%d/%d，想要 %d/0",
					len(layout.quads), len(layout.glyphs), test.wantQuads)
			}
			first := layout.quads[0]
			if first.X != 8 || first.Y != 696 || first.Width != 16 || first.Height != 16 {
				t.Fatalf("左下第一颗爱心=%+v，想要锚定 (8,696) 且无前置背景", first)
			}
		})
	}
}

// 杀死变异：零尺寸 framebuffer 时仍绘制生命值会产生越界或退化几何。
func TestAppendHealthBarRejectsDegenerateFramebuffer(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var layout hotbarLayout
	appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: 12}, 0, 720)
	if len(layout.quads) != 0 {
		t.Fatalf("零宽 framebuffer quads=%d，想要 0", len(layout.quads))
	}
}

// 杀死变异：继续依附快捷栏、保留面板或沿用打开背包 scale 都会让两组实例不同。
func TestHealthHeartsStayBottomLeftWithoutBackgroundAt640x360(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	var closed, open hotbarLayout
	layoutInventory(&closed, atlas, core.Inventory{}, false, -1, nil, nil, MiningOverlay{}, 640, 360)
	closedStart := len(closed.quads)
	appendHealthBar(&closed, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 640, 360)
	layoutInventory(&open, atlas, core.Inventory{}, true, -1, nil, nil, MiningOverlay{}, 640, 360)
	openStart := len(open.quads)
	appendHealthBar(&open, atlas, HealthOverlay{Confirmed: true, Value: core.MaxHealth}, 640, 360)
	closedHearts, openHearts := closed.quads[closedStart:], open.quads[openStart:]
	if len(closedHearts) != 20 || len(openHearts) != 20 {
		t.Fatalf("关闭/打开背包爱心=%d/%d，想要无背景的 10 空心加 10 满心", len(closedHearts), len(openHearts))
	}
	if !reflect.DeepEqual(closedHearts, openHearts) {
		t.Fatalf("打开背包移动或缩放了生命栏: closed=%+v open=%+v", closedHearts, openHearts)
	}
	for index, heart := range closedHearts {
		if heart.X < 8 || heart.Y < 0 || heart.X+heart.Width > 640 || heart.Y+heart.Height > 352 {
			t.Fatalf("爱心 %d 未保持左/下 8px 安全边距: %+v", index, heart)
		}
	}
	if first := closedHearts[0]; first.X != 8 || first.Y != 336 || first.Width != 16 || first.Height != 16 {
		t.Fatalf("第一颗爱心=%+v，想要 (8,336,16,16)", first)
	}
}

// 杀死变异：退回矩形段、漏掉空心爱心或把奇数生命画成整颗都会改变 UV 与宽度。
func TestHealthBarUsesTenTwoPointHearts(t *testing.T) {
	atlas := newFakeNameTagAtlas()
	for _, test := range []struct {
		name      string
		health    uint8
		wantQuads int
		lastHalf  bool
	}{
		{"零血", 0, 10, false},
		{"九点生命", 9, 15, true},
		{"满血", core.MaxHealth, 20, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var layout hotbarLayout
			appendHealthBar(&layout, atlas, HealthOverlay{Confirmed: true, Value: test.health}, 1280, 720)
			if len(layout.quads) != test.wantQuads || len(layout.glyphs) != 0 {
				t.Fatalf("quads/glyphs=%d/%d，想要 %d/0", len(layout.quads), len(layout.glyphs), test.wantQuads)
			}
			emptyUV := hotbarTextureUV(hotbarEmptyHeartColumn)
			for index, heart := range layout.quads[:10] {
				if got := [4]float32{heart.U0, heart.V0, heart.U1, heart.V1}; got != emptyUV {
					t.Fatalf("空心爱心 %d UV=%v，想要 %v", index, got, emptyUV)
				}
				if heart.Width != healthHeartSize || heart.Height != healthHeartSize {
					t.Fatalf("空心爱心 %d 尺寸=%v×%v", index, heart.Width, heart.Height)
				}
			}
			if test.health > 0 {
				last := layout.quads[len(layout.quads)-1]
				fullUV := hotbarTextureUV(hotbarFullHeartColumn)
				wantU1 := fullUV[2]
				if test.lastHalf {
					wantU1 = (fullUV[0] + fullUV[2]) * 0.5
				}
				if got := [4]float32{last.U0, last.V0, last.U1, last.V1}; got != ([4]float32{fullUV[0], fullUV[1], wantU1, fullUV[3]}) {
					t.Fatalf("最后填充爱心 UV=%v，想要完整/半颗材质", got)
				}
			}
			if test.lastHalf {
				last := layout.quads[len(layout.quads)-1]
				if last.Width != healthHeartSize/2 || last.Height != healthHeartSize {
					t.Fatalf("奇数生命末颗=%+v，想要半颗爱心", last)
				}
			}
		})
	}
}

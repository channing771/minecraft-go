//go:build darwin

package main

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

// 12 点生命新增十颗空心爱心和六颗填充爱心，不包含背景面板。
const healthQuadInstancesForHUDTest = 16

// Mutation killed: forwarding a predicted/stale health value, swapping the
// Confirmed flag computed from Predictor.Health(), or failing to clear health
// after an authoritative player-state reset would let the HUD show a health
// number the server never confirmed.
func TestHUDHealthReflectsOnlyConfirmedPredictorState(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
		t.Fatal(err)
	}
	hudQuadCount := func() int {
		_, quads, _ := app.hotbarRenderer.FrameStreams()
		return len(quads) / 48
	}

	// 收到权威状态之前：Predictor 尚未就绪，HUD 不得画出任何生命值数字。
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("未确认生命值 renderFrame=(%v,%v)", rendered, err)
	}
	baseline := hudQuadCount()

	// 收到生命值为 12 的权威状态：HUD 必须显示十颗空心和六颗填充爱心。
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true,
		Ready: true, Health: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("已确认生命值 renderFrame=(%v,%v)", rendered, err)
	}
	confirmed := hudQuadCount()
	if got, want := confirmed-baseline, int(healthQuadInstancesForHUDTest); got != want {
		t.Fatalf("确认生命值 12 后 quad 增量=%d，想要 %d（无背景的空心与填充爱心）", got, want)
	}

	// 权威玩家状态 reset（Ready=false）：即使背包镜像仍然确认，生命值也必须
	// 清空，不能继续显示断线前的陈旧数值。
	if _, err := app.predictor.ApplyPlayerState(network.PlayerState{
		ServerTick: 2, Dimension: core.Overworld, Ready: false,
	}, client.MirrorCollisionSource{Mirror: app.mirror, Dimension: core.Overworld}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("玩家状态 reset 后 renderFrame=(%v,%v)", rendered, err)
	}
	afterReset := hudQuadCount()
	if afterReset != baseline {
		t.Fatalf("玩家状态 reset 后 quad=%d，想要回到未确认基线 %d", afterReset, baseline)
	}
}

// Mutation killed: keeping the hotbar pass (and therefore the stale health
// number) alive after the client session closes would show a number the
// current session never confirmed.
func TestHUDHealthHiddenAfterDisconnect(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
		t.Fatal(err)
	}
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true,
		Ready: true, Health: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("已确认生命值 renderFrame=(%v,%v)", rendered, err)
	}
	flushesWithHUD := glyphs.flushes
	if flushesWithHUD < 2 {
		t.Fatalf("已确认生命值时 flush=%d,想要名牌+HUD 两次 Prepare", flushesWithHUD)
	}

	app.closeClientSession(nil)
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("断线后 renderFrame=(%v,%v)", rendered, err)
	}
	// 断线帧 hudVisible 为假:只有名牌 Prepare 冲刷一次,HUD 不再准备。
	if got := glyphs.flushes - flushesWithHUD; got != 1 {
		t.Fatalf("断线后新增 flush=%d,想要仅名牌 1 次(HUD 不得准备)", got)
	}
}

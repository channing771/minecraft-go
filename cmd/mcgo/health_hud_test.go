//go:build darwin

package main

import (
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

// 12 点生命新增十颗空心爱心和六颗填充爱心，不包含背景面板。
const healthQuadInstancesForHUDTest = 16

// Mutation killed: forwarding a predicted/stale health value, swapping the
// Confirmed flag computed from Predictor.Health(), or failing to clear health
// after an authoritative player-state reset would let the HUD show a health
// number the server never confirmed.
func TestHUDHealthReflectsOnlyConfirmedPredictorState(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
	if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{}}); err != nil {
		t.Fatal(err)
	}

	// 收到权威状态之前：Predictor 尚未就绪，HUD 不得画出任何生命值数字。
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("未确认生命值 renderFrame=(%v,%v)", rendered, err)
	}
	baseline := dev.lastDrawInstanceCount()

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
	confirmed := dev.lastDrawInstanceCount()
	if got, want := confirmed-baseline, uint32(healthQuadInstancesForHUDTest); got != want {
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
	afterReset := dev.lastDrawInstanceCount()
	if afterReset != baseline {
		t.Fatalf("玩家状态 reset 后 quad=%d，想要回到未确认基线 %d", afterReset, baseline)
	}
}

// Mutation killed: keeping the hotbar pass (and therefore the stale health
// number) alive after the client session closes would show a number the
// current session never confirmed.
func TestHUDHealthHiddenAfterDisconnect(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
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
	if !slices.Contains(dev.lastPasses(), "hotbar pass") {
		t.Fatalf("已确认生命值时 passes=%v，想要包含 hotbar pass", dev.lastPasses())
	}

	app.closeClientSession(nil)
	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("断线后 renderFrame=(%v,%v)", rendered, err)
	}
	if slices.Contains(dev.lastPasses(), "hotbar pass") {
		t.Fatalf("断线后 passes=%v，不应再绘制 HUD（含生命值）", dev.lastPasses())
	}
}

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

// hotbarGlyphInstanceBytesForHealthHUDTest 与 internal/render 的
// hotbarInstanceBytes 保持一致：每个 quad/glyph 实例在动态上传区中占 48 字节
// （X/Y/Width/Height、UV 四个分量、RGBA 颜色，均为 float32）。cmd/mcgo 只做黑盒
// 集成测试，不导入 render 包的未导出常量，因此把这个字节数在此显式钉住。
const hotbarGlyphInstanceBytesForHealthHUDTest = 48

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
	baseline := len(dev.bufferByLabel(t, "hotbar dynamic upload").lastWrite)

	// 收到生命值为 12 的权威状态：HUD 必须显示该确认值——两位数字，
	// 因此上传字节应恰好增加两个实例的字节数。
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
	confirmed := len(dev.bufferByLabel(t, "hotbar dynamic upload").lastWrite)
	if got, want := confirmed-baseline, 2*hotbarGlyphInstanceBytesForHealthHUDTest; got != want {
		t.Fatalf("确认生命值 12 后上传字节增量=%d，想要 %d（两位数字）", got, want)
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
	afterReset := len(dev.bufferByLabel(t, "hotbar dynamic upload").lastWrite)
	if afterReset != baseline {
		t.Fatalf("玩家状态 reset 后上传字节=%d，想要回到未确认基线 %d", afterReset, baseline)
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

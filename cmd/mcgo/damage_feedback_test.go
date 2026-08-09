//go:build darwin

package main

import (
	"encoding/binary"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
	"minecraft-go/internal/render"
)

func TestDamageFeedbackUsesOnlyConfirmedDecrease(t *testing.T) {
	var feedback damageFeedback
	if got := feedback.Update(12, true, time.Second); got != 0 {
		t.Fatalf("首次确认强度=%v，想要 0", got)
	}
	if got := feedback.Update(7, true, time.Second); got != 1 {
		t.Fatalf("确认下降当帧强度=%v，想要 1", got)
	}
	if got := feedback.Update(8, true, 90*time.Millisecond); got != 0.5 {
		t.Fatalf("回复且淡出 90ms 强度=%v，想要 0.5", got)
	}
	if got := feedback.Update(8, true, 90*time.Millisecond); got != 0 {
		t.Fatalf("完整 180ms 后强度=%v，想要 0", got)
	}
}

func TestDamageFeedbackRepeatedDamageRestartsFullDuration(t *testing.T) {
	var feedback damageFeedback
	feedback.Update(20, true, 0)
	feedback.Update(15, true, 0)
	if got := feedback.Update(15, true, 90*time.Millisecond); got != 0.5 {
		t.Fatalf("首次伤害淡出强度=%v，想要 0.5", got)
	}
	if got := feedback.Update(10, true, time.Second); got != 1 {
		t.Fatalf("连续伤害当帧强度=%v，想要重新为 1", got)
	}
	if got := feedback.Update(10, true, 179*time.Millisecond); got <= 0 {
		t.Fatalf("重置后 179ms 强度=%v，想要仍大于 0", got)
	}
}

func TestDamageFeedbackElapsedBoundsAndReset(t *testing.T) {
	var feedback damageFeedback
	feedback.Update(20, true, 0)
	feedback.Update(10, true, 0)
	if got := feedback.Update(10, true, -time.Second); got != 1 {
		t.Fatalf("负 elapsed 强度=%v，想要保持 1", got)
	}
	if got := feedback.Update(10, false, 0); got != 0 {
		t.Fatalf("not-ready 强度=%v，想要 0", got)
	}
	if got := feedback.Update(4, true, 0); got != 0 {
		t.Fatalf("reset 后首次 ready 强度=%v，想要 0", got)
	}
	feedback.Update(2, true, 0)
	feedback.Reset()
	if feedback != (damageFeedback{}) {
		t.Fatalf("显式 Reset 后状态=%+v，想要零值", feedback)
	}
}

func applyDamageFeedbackHealth(t *testing.T, app *application, tick uint64, health uint8, ready bool) {
	t.Helper()
	if _, err := app.predictor.ApplyPlayerState(network.PlayerState{
		ServerTick: tick, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true,
		Ready: ready, Health: health,
	}, client.MirrorCollisionSource{Mirror: app.mirror, Dimension: core.Overworld}); err != nil {
		t.Fatalf("应用 health=%d ready=%v: %v", health, ready, err)
	}
}

func TestApplicationDamageOverlayUsesConfirmedHealthAndStaysBelowHUD(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, device := newRemoteRenderApplication(t, glyphs)
	app.debugPanelRenderer = render.NewDebugPanelRenderer(device, gfx.FormatRGBA8Unorm, glyphs)
	app.panel = newPanelStateFromActive(config.Defaults().Render)
	app.panel.visible = true
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
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
	if rendered, err := app.frame(0, 1, 0); err != nil || !rendered {
		t.Fatalf("建立基线 frame=(%v,%v)", rendered, err)
	}
	if contains := slices.Contains(device.lastPasses(), "damage overlay pass"); contains {
		t.Fatalf("首次确认误画 overlay: %v", device.lastPasses())
	}

	applyDamageFeedbackHealth(t, app, 2, 7, true)
	device.resetPasses()
	if rendered, err := app.frame(0, 1, time.Second); err != nil || !rendered {
		t.Fatalf("确认受伤 frame=(%v,%v)", rendered, err)
	}
	want := []string{
		"terrain pass", "avatar pass", "name-tag pass", "damage overlay pass",
		"hotbar pass", "debug panel pass",
	}
	if got := device.lastPasses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v，想要 %v", got, want)
	}
	strength := math.Float32frombits(binary.LittleEndian.Uint32(
		device.bufferByLabel(t, "damage overlay uniform").lastWrite[:4],
	))
	if strength != 1 {
		t.Fatalf("受伤当帧 strength=%v，想要 1", strength)
	}

	applyDamageFeedbackHealth(t, app, 3, 8, true)
	device.resetPasses()
	if rendered, err := app.frame(0, 1, 90*time.Millisecond); err != nil || !rendered {
		t.Fatalf("回复期间淡出 frame=(%v,%v)", rendered, err)
	}
	strength = math.Float32frombits(binary.LittleEndian.Uint32(
		device.bufferByLabel(t, "damage overlay uniform").lastWrite[:4],
	))
	if strength != 0.5 {
		t.Fatalf("回复不得重启反馈，90ms strength=%v，想要 0.5", strength)
	}

	applyDamageFeedbackHealth(t, app, 4, 0, false)
	device.resetPasses()
	if rendered, err := app.frame(0, 1, 0); err != nil || !rendered {
		t.Fatalf("not-ready frame=(%v,%v)", rendered, err)
	}
	if slices.Contains(device.lastPasses(), "damage overlay pass") {
		t.Fatalf("not-ready 后仍画 overlay: %v", device.lastPasses())
	}
}

func TestDamageFeedbackResetsWithClientSession(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	app.damageFeedback.Update(20, true, 0)
	app.damageStrength = app.damageFeedback.Update(10, true, 0)
	app.closeClientSession(nil)
	if app.damageFeedback != (damageFeedback{}) || app.damageStrength != 0 {
		t.Fatalf("会话清理后 feedback=%+v strength=%v，想要零值", app.damageFeedback, app.damageStrength)
	}
}

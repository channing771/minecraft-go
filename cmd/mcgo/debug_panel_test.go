//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/config"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/sim"
)

// selectFieldForTest 按 Group+"."+Name 定位字段并设置 selected，找不到时 Fatal。
// 与 internal/client mesher_test.go 的 BlockForTest 等一脉相承的
// "XxxForTest" 测试专用辅助方法命名约定。
func (s *panelState) selectFieldForTest(t *testing.T, name string) {
	t.Helper()
	for i, field := range config.Fields() {
		if field.Group+"."+field.Name == name {
			s.selected = i
			return
		}
	}
	t.Fatalf("未找到字段 %s", name)
}

func TestPanelRowsMarkAuthoritativeGroupsReadOnlyWhenRemote(t *testing.T) {
	state := newPanelState(config.Defaults())
	for _, row := range state.rows(true) {
		if strings.HasPrefix(row.Label, "physics.") || strings.HasPrefix(row.Label, "sim.") {
			if !row.ReadOnly {
				t.Fatalf("联机时 %s 必须只读", row.Label)
			}
		}
	}
}

func TestPanelRowsAllowAuthoritativeGroupsWhenLocal(t *testing.T) {
	state := newPanelState(config.Defaults())
	editable := 0
	for _, row := range state.rows(false) {
		if strings.HasPrefix(row.Label, "physics.") && !row.ReadOnly {
			editable++
		}
	}
	if editable == 0 {
		t.Fatal("单机时物理组必须可编辑")
	}
}

func TestPanelViewDistanceIsAlwaysReadOnly(t *testing.T) {
	state := newPanelState(config.Defaults())
	for _, remote := range []bool{false, true} {
		for _, row := range state.rows(remote) {
			if row.Label == "render.viewDistance" && !row.ReadOnly {
				t.Fatalf("viewDistance 在 remote=%v 下也必须只读（重启生效）", remote)
			}
		}
	}
}

func TestPanelArrowAdjustsSelectedValue(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")

	before := state.effective.Physics.Gravity
	state.handleKeys(panelKeys{Right: true}, false)
	if state.effective.Physics.Gravity <= before {
		t.Fatalf("右方向键必须增大取值：%v -> %v", before, state.effective.Physics.Gravity)
	}
	state.handleKeys(panelKeys{Left: true}, false)
	if state.effective.Physics.Gravity != before {
		t.Fatalf("左方向键必须还原一步：%v，want %v", state.effective.Physics.Gravity, before)
	}
}

func TestPanelShiftCoarseAndAltFine(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	base := state.effective.Physics.Gravity

	state.handleKeys(panelKeys{Right: true}, false)
	fine := state.effective.Physics.Gravity - base
	state.effective.Physics.Gravity = base

	state.handleKeys(panelKeys{Right: true, Shift: true}, false)
	coarse := state.effective.Physics.Gravity - base
	if coarse <= fine {
		t.Fatalf("Shift 必须是粗调：coarse=%v fine=%v", coarse, fine)
	}
}

func TestPanelRejectsEditsOnReadOnlyRow(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	before := state.effective.Physics.Gravity
	state.handleKeys(panelKeys{Right: true}, true) // remote=true
	if state.effective.Physics.Gravity != before {
		t.Fatal("联机时不得修改权威参数")
	}
}

func TestPanelEnterResetsRowToDefault(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	state.handleKeys(panelKeys{Right: true}, false)
	state.handleKeys(panelKeys{Enter: true}, false)
	if state.effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("Enter 必须把当前行重置为默认值")
	}
}

func TestPanelClampsAtBounds(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "sim.spawnRadius")
	for i := 0; i < 10000; i++ {
		state.handleKeys(panelKeys{Right: true, Shift: true}, false)
	}
	if state.effective.Sim.SpawnRadius > 64 {
		t.Fatalf("SpawnRadius = %v，必须钳在上界 64", state.effective.Sim.SpawnRadius)
	}
}

func TestPanelNavigationSkipsReadOnlyRows(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selected = 0
	for i := 0; i < 200; i++ {
		state.handleKeys(panelKeys{Down: true}, true)
		if state.rows(true)[state.selected].ReadOnly {
			t.Fatal("导航必须跳过只读行")
		}
	}
}

func TestPanelSaveWritesFile(t *testing.T) {
	state := newPanelState(config.Defaults())
	path := filepath.Join(t.TempDir(), "config.json")
	if err := state.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("保存后文件必须存在: %v", err)
	}
}

// TestPanelClampsHitsUpperBoundExactly 证明 TestPanelClampsAtBounds 的样本量真的
// 触到了上界，而不是恰好停在界内看起来像通过：10000 次 ×10 步长的粗调足以让
// spawnRadius(1..64,step1) 在几步内越过 64，必须被钳成恰好 64。
func TestPanelClampsHitsUpperBoundExactly(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "sim.spawnRadius")
	for i := 0; i < 10; i++ {
		state.handleKeys(panelKeys{Right: true, Shift: true}, false)
	}
	if state.effective.Sim.SpawnRadius != 64 {
		t.Fatalf("SpawnRadius = %v，want 恰好命中上界 64", state.effective.Sim.SpawnRadius)
	}
}

func TestPanelToggleDoesNotReportChanged(t *testing.T) {
	state := newPanelState(config.Defaults())
	if changed := state.handleKeys(panelKeys{Toggle: true}, false); changed {
		t.Fatal("仅切换可见性不应视为改动")
	}
	if !state.visible {
		t.Fatal("Toggle 必须切换可见性")
	}
}

// TestApplyPanelChangeWritesCameraFovY 证明面板改 FOV 会同时写 a.camera.FovY，
// 而不是只改 a.render.FovDegrees。a.camera.FovY 在构造相机时被一次性烘焙、
// 之后不再每帧重读（不同于鼠标灵敏度那样每帧读 a.render），单靠肉眼看不出
// 遗漏，必须有测试锁住。
func TestApplyPanelChangeWritesCameraFovY(t *testing.T) {
	originalPhysics := physics.ActiveTunables()
	originalSim := sim.ActiveTunables()
	t.Cleanup(func() {
		physics.SetTunables(originalPhysics)
		sim.SetTunables(originalSim)
	})

	app := &application{panel: newPanelState(config.Defaults())}
	app.panel.effective.Render.FovDegrees = 42
	app.applyPanelChange()

	want := mgl32.DegToRad(42)
	if app.camera.FovY != want {
		t.Fatalf("camera.FovY = %v, want %v（FOV 未写回相机）", app.camera.FovY, want)
	}
	if app.render.FovDegrees != 42 {
		t.Fatalf("a.render.FovDegrees = %v, want 42", app.render.FovDegrees)
	}
}

func TestPanelResetAllSkipsAuthoritativeGroupsWhenRemote(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.effective.Physics.Gravity = 99
	state.effective.Render.MouseSensitivity = 4.5
	state.handleKeys(panelKeys{ResetAll: true}, true)
	if state.effective.Physics.Gravity != 99 {
		t.Fatalf("联机时 F6 不得改动 physics 组：%v", state.effective.Physics.Gravity)
	}
	if state.effective.Render.MouseSensitivity != config.Defaults().Render.MouseSensitivity {
		t.Fatalf("render 组仍应被 F6 重置：%v", state.effective.Render.MouseSensitivity)
	}
}

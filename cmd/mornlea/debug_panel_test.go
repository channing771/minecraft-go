//go:build darwin

package main

import (
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/sim"
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

// dataRowsForTest 从 rows() 输出里过滤掉段头行（panelSectionHeaderRow：
// ReadOnly 且 Value 为空），返回与 config.Fields() 顺序一一对应的数据行。
// rows() 现在按 Group+"."+Name 与 config.Fields() 下标是一一对应的关系
// 不再成立——rows() 里插入了分组段头，标签也从 "Group.Name" 改成裸
// field.Name（见 Finding 4：列宽/rune 数不匹配导致标签在 180px 列宽下
// 越界，改成"段头 + 裸字段名"而不是继续拉长每行标签）——测试要按
// field.Group/Name 断言，只能先把段头过滤掉，再按下标对齐 config.Fields()。
func dataRowsForTest(t *testing.T, rows []render.PanelRow) []render.PanelRow {
	t.Helper()
	data := make([]render.PanelRow, 0, len(rows))
	for _, row := range rows {
		if row.ReadOnly && row.Value == "" {
			continue // 段头行
		}
		data = append(data, row)
	}
	fields := config.Fields()
	if len(data) != len(fields) {
		t.Fatalf("data rows = %d, want %d(=len(config.Fields()))", len(data), len(fields))
	}
	return data
}

// selectedRowForTest 返回 rows 中被标记 Selected 的那一行，找不到时 Fatal。
// rows() 插入段头后，state.selected（config.Fields() 下标）不再等于
// rows() 切片自身的下标，不能再直接 rows[state.selected]。
func selectedRowForTest(t *testing.T, rows []render.PanelRow) render.PanelRow {
	t.Helper()
	for _, row := range rows {
		if row.Selected {
			return row
		}
	}
	t.Fatal("没有任何一行被标记为 Selected")
	return render.PanelRow{}
}

func TestPanelRowsMarkAuthoritativeGroupsReadOnlyWhenRemote(t *testing.T) {
	state := newPanelState(config.Defaults())
	fields := config.Fields()
	rows := dataRowsForTest(t, state.rows(true))
	for i, field := range fields {
		if field.Group == "physics" || field.Group == "sim" {
			if !rows[i].ReadOnly {
				t.Fatalf("联机时 %s.%s 必须只读", field.Group, field.Name)
			}
		}
	}
}

func TestPanelRowsAllowAuthoritativeGroupsWhenLocal(t *testing.T) {
	state := newPanelState(config.Defaults())
	fields := config.Fields()
	rows := dataRowsForTest(t, state.rows(false))
	editable := 0
	for i, field := range fields {
		if field.Group == "physics" && !rows[i].ReadOnly {
			editable++
		}
	}
	if editable == 0 {
		t.Fatal("单机时物理组必须可编辑")
	}
}

func TestPanelViewDistanceIsAlwaysReadOnly(t *testing.T) {
	state := newPanelState(config.Defaults())
	fields := config.Fields()
	viewDistanceIndex := -1
	for i, field := range fields {
		if field.Group == "render" && field.Name == "viewDistance" {
			viewDistanceIndex = i
		}
	}
	if viewDistanceIndex < 0 {
		t.Fatal("config.Fields() 缺少 render.viewDistance")
	}
	for _, remote := range []bool{false, true} {
		rows := dataRowsForTest(t, state.rows(remote))
		if !rows[viewDistanceIndex].ReadOnly {
			t.Fatalf("viewDistance 在 remote=%v 下也必须只读（重启生效）", remote)
		}
	}
}

// TestPanelRowsInsertSectionHeadersWithoutGroupPrefix 锁住 Finding 4 的修法：
// 标签列宽 170px 装不下"分组前缀+字段名"（最长的 sim.playerDropPickupDelayTicks
// 有 30 个 ASCII 字符），改成每组一个段头行 + 裸字段名（最长
// playerDropPickupDelayTicks 仍有 26 个字符，但至少不再需要在每一行里
// 重复分组名）。
func TestPanelRowsInsertSectionHeadersWithoutGroupPrefix(t *testing.T) {
	state := newPanelState(config.Defaults())
	rows := state.rows(false)
	headers := 0
	for _, row := range rows {
		if strings.Contains(row.Label, ".") {
			t.Fatalf("裸标签不应再带分组前缀: %q", row.Label)
		}
		if row.ReadOnly && row.Value == "" {
			headers++
		}
	}
	if headers != 3 {
		t.Fatalf("段头行数 = %d, want 3（physics/sim/render 各一个）", headers)
	}
	if len(rows) != len(config.Fields())+3 {
		t.Fatalf("rows 总数 = %d, want %d", len(rows), len(config.Fields())+3)
	}
}

// TestPanelSelectedRowTracksSelectedField 钉住"高亮行就是 Fields()[selected]
// 那一行"。
//
// rows() 会插入三个段头行，因此 s.selected（Fields() 下标）与 rows 切片下标
// 不再一一对应。既有测试只断言"存在一行被标记 Selected"或"被选中的行不是
// 只读行"，用 rows 自身的下标去标记 Selected 的实现（高亮行随段头逐组下移）
// 一样能通过——面板上高亮的是一个字段，方向键改的却是另一个。
func TestPanelSelectedRowTracksSelectedField(t *testing.T) {
	state := newPanelState(config.Defaults())
	for _, name := range []string{"physics.gravity", "sim.spawnRadius", "render.fovDegrees"} {
		t.Run(name, func(t *testing.T) {
			state.selectFieldForTest(t, name)
			want := name[strings.Index(name, ".")+1:]
			if got := selectedRowForTest(t, state.rows(false)).Label; got != want {
				t.Fatalf("高亮行 Label = %q，want %q（高亮必须落在 config.Fields()[selected] 上，"+
					"不能被段头行挤偏）", got, want)
			}
		})
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

// TestPanelAltIsFineAdjustment 单独覆盖 Alt（×0.1 细调）：
// TestPanelShiftCoarseAndAltFine 的名字承诺了 Alt，但函数体从未设置过
// Alt:true，删掉 handleKeys 里 `if keys.Alt { step *= 0.1 }` 那一行，
// 原有测试套件照样全绿。这里直接断言 Alt 增量严格小于不带修饰键的普通增量。
func TestPanelAltIsFineAdjustment(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.visible = true
	state.selectFieldForTest(t, "physics.gravity")
	base := state.effective.Physics.Gravity

	state.handleKeys(panelKeys{Right: true}, false)
	normal := state.effective.Physics.Gravity - base
	state.effective.Physics.Gravity = base

	state.handleKeys(panelKeys{Right: true, Alt: true}, false)
	fine := state.effective.Physics.Gravity - base
	if fine <= 0 {
		t.Fatalf("Alt 细调仍必须增大取值：fine=%v", fine)
	}
	if fine >= normal {
		t.Fatalf("Alt 必须是细调：fine=%v normal=%v", fine, normal)
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
		if selectedRowForTest(t, state.rows(true)).ReadOnly {
			t.Fatal("导航必须跳过只读行")
		}
	}
}

// TestPanelSaveWritesFile 证明 save 真的把 effective 里的值落了盘，而不是
// 例如写 config.Defaults().Save(path) 那种完全丢弃 s.effective 却也能让
// os.Stat 成功的实现——原来的版本只断言文件存在，测不出这种退化。
func TestPanelSaveWritesFile(t *testing.T) {
	state := newPanelState(config.Defaults())
	state.effective.Physics.Gravity = 55
	state.effective.Sim.SpawnRadius = 9
	state.effective.Render.FovDegrees = 88

	path := filepath.Join(t.TempDir(), "config.json")
	if err := state.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("重新读取保存的配置: %v", err)
	}
	if saved.Physics.Gravity != 55 {
		t.Fatalf("Physics.Gravity 未落盘: %v，want 55", saved.Physics.Gravity)
	}
	if saved.Sim.SpawnRadius != 9 {
		t.Fatalf("Sim.SpawnRadius 未落盘: %v，want 9", saved.Sim.SpawnRadius)
	}
	if saved.Render.FovDegrees != 88 {
		t.Fatalf("Render.FovDegrees 未落盘: %v，want 88", saved.Render.FovDegrees)
	}
}

// TestPanelSavePreservesExistingLoggingSection 证明 save 不会把磁盘上已有的
// logging 段清空——例如把实现换成 config.Defaults().Save(path) 或者丢掉
// save() 里 config.Load 那一步直接整体覆盖，都会让这里的模块级日志等级消失。
func TestPanelSavePreservesExistingLoggingSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	preexisting := config.Defaults()
	preexisting.Logging.Modules = map[string]slog.Level{"render": slog.LevelDebug}
	if err := preexisting.Save(path); err != nil {
		t.Fatalf("准备已有配置文件: %v", err)
	}

	state := newPanelState(config.Defaults())
	if err := state.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("重新读取保存的配置: %v", err)
	}
	if got := saved.Logging.Modules["render"]; got != slog.LevelDebug {
		t.Fatalf("logging.modules.render = %v, want LevelDebug（save 不得清空已有 logging 段）", got)
	}
}

func TestPanelSavePreservesExistingAICompanions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	id, err := companion.ParseID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	preexisting := config.Defaults()
	// M5B 起非空伙伴必须携带完整模型设置才能通过 config.Load；这里用免密钥的
	// loopback 形态（超时未设置走默认值），保持本测试"面板保存完全保留 ai 组"
	// 的主题不变，顺带锁定模型字段随 Load→改 render→Save 往返不丢。
	preexisting.AI = &config.AI{
		ModelSettings: companion.ModelSettings{
			Endpoint: "http://127.0.0.1:1/v1",
			Model:    "test-model",
		},
		Companions: []companion.Definition{{ID: id, Name: "阿木"}},
	}
	if err := preexisting.Save(path); err != nil {
		t.Fatalf("准备已有配置文件: %v", err)
	}

	state := newPanelState(config.Defaults())
	state.effective.Render.FovDegrees = 88
	if err := state.save(path); err != nil {
		t.Fatalf("save: %v", err)
	}

	saved, err := config.Load(path)
	if err != nil {
		t.Fatalf("重新读取保存的配置: %v", err)
	}
	if !reflect.DeepEqual(saved.AI, preexisting.AI) {
		t.Fatalf("AI = %+v，want 完全保留 %+v", saved.AI, preexisting.AI)
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

// TestPanelFrameInputIsAllocationFreeWhenHidden 锁住"面板关闭时渲染热路径零
// 分配"这条性质。
//
// 原实现把 a.panel.rows(a.remote()) 直接写成 Prepare 的实参，Prepare 内部
// 的 visible 提前返回拦不住实参求值：只要开了 --dev，即使面板关着，每帧也会
// 分配一个 20 余行的切片、三处段头字符串与十余个格式化后的数值字符串。
// internal/render 的 BenchmarkDebugPanelHidden 只测 Prepare 自身，看不到调用
// 方这一侧的构造开销，因此这条断言必须留在 cmd/mornlea。
func TestPanelFrameInputIsAllocationFreeWhenHidden(t *testing.T) {
	app := &application{panel: newPanelState(config.Defaults())}
	now := time.Now()
	if allocations := testing.AllocsPerRun(100, func() {
		app.panelFrameInput(now)
	}); allocations != 0 {
		t.Fatalf("面板关闭时每帧分配 %v 次；读数与参数行必须在 visible 判定之后才构造", allocations)
	}

	app.panel.visible = true
	readout, rows := app.panelFrameInput(now)
	if len(rows) != len(config.Fields())+3 {
		t.Fatalf("面板打开时参数行 = %d，want %d（含三个段头行）", len(rows), len(config.Fields())+3)
	}
	if readout.Mode == "" {
		t.Fatal("面板打开时必须填出运行模式读数")
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

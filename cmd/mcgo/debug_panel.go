//go:build darwin

package main

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/config"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/render"
	"minecraft-go/internal/sim"
)

// remote 报告本进程是单纯的联机客户端——既没有内嵌单机 Host，也不是
// benchmark 场景下同进程运行的可信服务端。physics 与 sim 的运行时快照是
// 进程级全局状态：只有 Host 或 benchmark server 与当前进程共享该状态时，
// 面板改写它们才等价于改写权威模拟；真正联机到远端服务端时，改写这里的
// 快照只会让本地预测偏离服务端，因此必须只读。
func (a *application) remote() bool {
	return a.host == nil && a.server == nil
}

// panelModeLabel 返回面板顶部读数区展示的连接模式文案。
func (a *application) panelModeLabel() string {
	switch {
	case a.host != nil:
		return "单机"
	case a.server != nil:
		return "benchmark"
	default:
		return "联机"
	}
}

// applyPanelChange 把面板改动后的 effective 配置同步进运行时状态：
// physics/sim 的包级原子快照、a.render 渲染配置快照，以及相机的 FovY。
//
// FovY 是特例：a.camera.FovY 只在构造相机时由 FovDegrees 一次性算出
// （见 newApplicationWithDependencies），此后不再每帧重新读取——鼠标灵敏度
// 那样的"每帧读 a.render"对它不成立。不在这里显式回写，面板上的数字变了，
// 实际视野角度会纹丝不动。
func (a *application) applyPanelChange() {
	physics.SetTunables(a.panel.effective.Physics)
	sim.SetTunables(a.panel.effective.Sim)
	a.render = a.panel.effective.Render
	a.camera.FovY = mgl32.DegToRad(a.render.FovDegrees)
}

// panelKeys 是本帧的面板按键边沿状态。以结构体注入而非直接查询窗口，
// 使面板逻辑可以在不创建窗口、不初始化 GPU 的情况下被测试。
type panelKeys struct {
	Toggle                bool // F3
	Up, Down, Left, Right bool
	Enter                 bool // 重置当前行
	Save                  bool // F5（由调用方在 handleKeys 之外读取并触发 save）
	ResetAll              bool // F6
	Shift                 bool // ×10 粗调
	Alt                   bool // ×0.1 细调
}

// panelState 是调试面板的交互状态：是否可见、当前选中行，以及正在编辑的生效配置。
// 它只做纯逻辑运算，不接触窗口或 GPU，因此可以在普通单元测试中直接构造和驱动。
type panelState struct {
	visible   bool
	selected  int
	effective config.Config
}

func newPanelState(effective config.Config) *panelState {
	return &panelState{effective: effective}
}

// newPanelStateFromActive 用当前已生效的 physics/sim 快照与调用方传入的
// render 配置构造面板初始状态。
//
// 之所以不在 app.go 的 newApplicationWithDependencies 里直接写
// config.Config{...}，是因为那个函数里有一个同名局部变量 config
// （server.Config），会遮蔽 config 包，写 config.Config{...} 会编译失败；
// 放在本文件（没有这个局部变量）里则没有这个问题。
func newPanelStateFromActive(renderConfig config.Render) *panelState {
	return newPanelState(config.Config{
		Physics: physics.ActiveTunables(),
		Sim:     sim.ActiveTunables(),
		Render:  renderConfig,
	})
}

// rows 把当前生效配置渲染成面板可绘制的行；remote 为真时 physics/sim 两组连同
// render.viewDistance 一起标记为只读——服务端是唯一权威，联机时客户端不得写
// physics/sim，viewDistance 则无论是否联机都需要重启才能生效。
func (s *panelState) rows(remote bool) []render.PanelRow {
	fields := config.Fields()
	rows := make([]render.PanelRow, len(fields))
	for i, field := range fields {
		rows[i] = render.PanelRow{
			Label:    field.Group + "." + field.Name,
			Value:    formatFieldValue(fieldValue(&s.effective, field)),
			ReadOnly: s.fieldReadOnly(field, remote),
			Selected: i == s.selected,
		}
	}
	return rows
}

// fieldReadOnly 判断字段在给定连接模式下是否只读：字段自身声明只读，
// 或者联机时命中 physics/sim 这两个权威分组。
func (s *panelState) fieldReadOnly(field config.Field, remote bool) bool {
	return field.ReadOnly || (remote && (field.Group == "physics" || field.Group == "sim"))
}

// handleKeys 消费本帧按键边沿，更新选中行与生效配置，返回 effective 是否被改动。
// 调用方应在 changed 为真时把 effective.Physics/Sim 重新写回
// physics.SetTunables/sim.SetTunables，并自行消费 effective.Render（尤其是
// FovDegrees——它不是每帧读取的，必须调用方显式同步进相机）。
func (s *panelState) handleKeys(keys panelKeys, remote bool) (changed bool) {
	if keys.Toggle {
		s.visible = !s.visible
	}
	if !s.visible {
		return false
	}
	fields := config.Fields()
	if len(fields) == 0 {
		return false
	}
	if s.selected < 0 || s.selected >= len(fields) {
		s.selected = 0
	}
	if keys.Down {
		s.moveSelection(fields, remote, 1)
	}
	if keys.Up {
		s.moveSelection(fields, remote, -1)
	}
	if keys.ResetAll {
		defaults := config.Defaults()
		for _, field := range fields {
			if s.fieldReadOnly(field, remote) {
				continue // 联机时不得整体重置也会改写 physics/sim。
			}
			setFieldFloat(fieldValue(&s.effective, field), fieldFloat(fieldValue(&defaults, field)))
		}
		changed = true
	}

	field := fields[s.selected]
	if s.fieldReadOnly(field, remote) {
		return changed
	}
	if keys.Left || keys.Right {
		step := field.Step
		if keys.Shift {
			step *= 10
		}
		if keys.Alt {
			step *= 0.1
		}
		if keys.Left {
			step = -step
		}
		target := fieldValue(&s.effective, field)
		next := clampFloat(fieldFloat(target)+step, field.Min, field.Max)
		setFieldFloat(target, next)
		changed = true
	}
	if keys.Enter {
		defaults := config.Defaults()
		setFieldFloat(fieldValue(&s.effective, field), fieldFloat(fieldValue(&defaults, field)))
		changed = true
	}
	return changed
}

// moveSelection 把 selected 移动到下一个/上一个非只读行，跳过只读行；
// 至多遍历一整圈，因为渲染组内至少 fovDegrees、mouseSensitivity 两项
// 始终可编辑，不会出现全部只读导致的死循环。
func (s *panelState) moveSelection(fields []config.Field, remote bool, direction int) {
	for range fields {
		s.selected = (s.selected + direction + len(fields)) % len(fields)
		if !s.fieldReadOnly(fields[s.selected], remote) {
			return
		}
	}
}

// save 把当前生效的 physics/sim/render 值合并进 path 处已有的配置文件再落盘：
// 先 Load（文件不存在时得到全默认配置，不报错），只覆盖这三组，其余字段
// （如 logging 的模块级别）原样保留，不因为打开一次调试面板就被清空。
func (s *panelState) save(path string) error {
	onDisk, err := config.Load(path)
	if err != nil {
		return err
	}
	onDisk.Physics = s.effective.Physics
	onDisk.Sim = s.effective.Sim
	onDisk.Render = s.effective.Render
	return onDisk.Save(path)
}

// fieldValue 返回 field 在 cfg 中对应的可寻址反射值。命名规则与
// config.Fields() 保持一致：Name 是小写开头的驼峰名，对应的结构体字段名只是
// 首字母大写（例如 spawnRadius -> SpawnRadius）。
func fieldValue(cfg *config.Config, field config.Field) reflect.Value {
	var group reflect.Value
	switch field.Group {
	case "physics":
		group = reflect.ValueOf(&cfg.Physics).Elem()
	case "sim":
		group = reflect.ValueOf(&cfg.Sim).Elem()
	case "render":
		group = reflect.ValueOf(&cfg.Render).Elem()
	default:
		panic("debug_panel: 未知字段分组 " + field.Group)
	}
	name := strings.ToUpper(field.Name[:1]) + field.Name[1:]
	value := group.FieldByName(name)
	if !value.IsValid() {
		// 不应该发生：说明 config.Fields() 里的 Name 与结构体字段拼写不一致。
		panic("debug_panel: config.Fields() 声明的字段在结构体中不存在: " + field.Group + "." + field.Name)
	}
	return value
}

// fieldFloat 把反射值统一读成 float64，供钳制与步进计算使用。
func fieldFloat(v reflect.Value) float64 {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return v.Float()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(v.Uint())
	default:
		panic("debug_panel: 不支持的字段类型 " + v.Kind().String())
	}
}

// setFieldFloat 把钳制后的 float64 写回原字段的实际数值类型，与 fieldFloat 对称。
func setFieldFloat(v reflect.Value, value float64) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		v.SetFloat(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(value))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(value))
	default:
		panic("debug_panel: 不支持的字段类型 " + v.Kind().String())
	}
}

// formatFieldValue 把字段值格式化为面板展示用的字符串：浮点数最多 4 位有效数字，
// 整数不带小数点。
func formatFieldValue(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'g', 4, 64)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	default:
		panic("debug_panel: 不支持的字段类型 " + v.Kind().String())
	}
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

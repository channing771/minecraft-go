package physics

import "sync/atomic"

// Tunables 是可在运行时调整的物理参数。
//
// 它按值传递并整体替换。读取方在函数入口取一次快照后全程使用该快照，因此单次固定步
// 内参数不会中途变化，模拟仍然确定性。写入只做一次原子指针交换，读写之间无锁无竞争。
//
// 只有 cmd 的启动装配与调试面板应当调用 SetTunables。
//
// json tag 与 config.Fields() 的 Name 逐字对应，保证配置文件写出的键名就是
// 设计文档与 README 里写的小写驼峰；读取侧大小写不敏感，加 tag 之前写出的
// 文件仍可正常读入。
type Tunables struct {
	EyeHeight          float32 `json:"eyeHeight"`
	StepHeight         float32 `json:"stepHeight"`
	WalkSpeed          float32 `json:"walkSpeed"`
	GroundAcceleration float32 `json:"groundAcceleration"`
	GroundDeceleration float32 `json:"groundDeceleration"`
	AirAcceleration    float32 `json:"airAcceleration"`
	JumpSpeed          float32 `json:"jumpSpeed"`
	Gravity            float32 `json:"gravity"`
	TerminalFallSpeed  float32 `json:"terminalFallSpeed"`
}

// DefaultTunables 返回编译期默认参数。它是配置文件缺省时的取值，
// 也是调试面板“重置”的目标值。
func DefaultTunables() Tunables {
	return Tunables{
		EyeHeight:          defaultEyeHeight,
		StepHeight:         defaultStepHeight,
		WalkSpeed:          defaultWalkSpeed,
		GroundAcceleration: defaultGroundAcceleration,
		GroundDeceleration: defaultGroundDeceleration,
		AirAcceleration:    defaultAirAcceleration,
		JumpSpeed:          defaultJumpSpeed,
		Gravity:            defaultGravity,
		TerminalFallSpeed:  defaultTerminalFallSpeed,
	}
}

var active atomic.Pointer[Tunables]

func init() {
	defaults := DefaultTunables()
	active.Store(&defaults)
}

// SetTunables 整体替换生效参数。
func SetTunables(tunables Tunables) { active.Store(&tunables) }

// ActiveTunables 返回当前生效参数的快照。
func ActiveTunables() Tunables { return *active.Load() }

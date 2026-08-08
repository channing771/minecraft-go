package physics

import "sync/atomic"

// Tunables 是可在运行时调整的物理参数。
//
// 它按值传递并整体替换。读取方在函数入口取一次快照后全程使用该快照，因此单次固定步
// 内参数不会中途变化，模拟仍然确定性。写入只做一次原子指针交换，读写之间无锁无竞争。
//
// 只有 cmd 的启动装配与调试面板应当调用 SetTunables。
type Tunables struct {
	EyeHeight          float32
	StepHeight         float32
	WalkSpeed          float32
	GroundAcceleration float32
	GroundDeceleration float32
	AirAcceleration    float32
	JumpSpeed          float32
	Gravity            float32
	TerminalFallSpeed  float32
}

// DefaultTunables 返回编译期默认参数。它是配置文件缺省时的取值，
// 也是调试面板“重置”的目标值。
func DefaultTunables() Tunables {
	return Tunables{
		EyeHeight:          EyeHeight,
		StepHeight:         StepHeight,
		WalkSpeed:          WalkSpeed,
		GroundAcceleration: GroundAcceleration,
		GroundDeceleration: GroundDeceleration,
		AirAcceleration:    AirAcceleration,
		JumpSpeed:          JumpSpeed,
		Gravity:            Gravity,
		TerminalFallSpeed:  TerminalFallSpeed,
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

// Package physics 提供客户端与服务端共享的确定性玩家物理。
package physics

import (
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

const (
	FixedDelta        = 50 * time.Millisecond
	FixedDeltaSeconds = float32(0.05)
	PlayerWidth       = float32(0.6)
	PlayerHeight      = float32(1.8)
	CollisionEpsilon  = float32(1e-5)
	GroundProbe       = float32(1e-4)

	// 以下是可调参数的编译期默认值。唯一读取入口是 Tunables 快照，
	// 不得再以导出常量暴露——见 internal/archcheck 的 TestTunableConstantsAreNotExported。
	defaultEyeHeight          = float32(1.62)
	defaultStepHeight         = float32(0.6)
	defaultWalkSpeed          = float32(4.3)
	defaultGroundAcceleration = float32(40)
	defaultGroundDeceleration = float32(50)
	defaultAirAcceleration    = float32(8)
	defaultJumpSpeed          = float32(8.4)
	defaultGravity            = float32(32)
	defaultTerminalFallSpeed  = float32(78.4)

	// 以下四项是身体浸没时替换掉空气常量的水中积分参数。取值理由：
	//   - defaultFluidGravity 取空气重力 32 的 1/5：水的浮力抵消掉大部分重力，
	//     玩家仍会下沉但加速度明显更小。
	//   - defaultFluidSinkSpeed 是水中垂直终端速度，约为空气 78.4 的 1/26：
	//     入水后不到半秒即收敛，观感是「缓慢下沉」而不是继续加速。
	//   - defaultFluidAscendSpeed 取得比下沉终端速度略大，保证持续按住上升键
	//     一定能净上升并浮出水面，而不是与下沉打平停在水中。
	//   - defaultFluidHorizontalDrag 是每 tick 乘在水平速度上的阻力系数。0.8
	//     配合地面加速度得到约 3.44 m/s 的稳态（空气中是 4.3），配合空中加速度
	//     得到约 1.6 m/s；两者都严格低于陆地行走速度，且仍留有可操控的机动性。
	defaultFluidGravity        = float32(6.4)
	defaultFluidSinkSpeed      = float32(3)
	defaultFluidAscendSpeed    = float32(4)
	defaultFluidHorizontalDrag = float32(0.8)
)

// State 是玩家在固定步开始时的物理状态；位置表示脚底中心。
type State struct {
	Position mgl32.Vec3
	Velocity mgl32.Vec3
	OnGround bool
}

// ValidState 报告状态的位置与速度是否都为有限值。
func ValidState(state State) bool {
	return validStateComponent(state.Position.X()) &&
		validStateComponent(state.Position.Y()) &&
		validStateComponent(state.Position.Z()) &&
		validStateComponent(state.Velocity.X()) &&
		validStateComponent(state.Velocity.Y()) &&
		validStateComponent(state.Velocity.Z())
}

func validStateComponent(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}

// Input 是单个固定步的玩家控制意图，外加本步开始时的两个浸没标志。
//
// 浸没标志不是「意图」，但它与意图同属「一步的外部输入」：流体没有碰撞盒，
// prism 只携带碰撞几何，Rust 因此无法自行区分水与空气。由调用方用
// SubmersionFlags 从各自的方块镜像算好传入，比在 prism 里塞一份逐格流体数组
// 便宜得多，也不会让 prism 的语义从「碰撞几何」滑向「通用方块视图」
// （见 change fluid-presentation-survival 的 design D4）。
type Input struct {
	MoveX int8
	MoveZ int8
	Jump  bool
	Yaw   float32
	// BodyInFluid 为真时本步走水中积分：重力衰减、垂直终端速度压低、
	// 水平速度乘阻力、Jump 变为持续上浮。
	BodyInFluid bool
	// EyeInFluid 只供调用方（水下视觉与氧气结算）使用，不影响积分，
	// 因此刻意不进入 StepInput 编码——ABI 里不放 Rust 永远不读的字节。
	EyeInFluid bool
}

// CollisionBoxSet 是一个方块局部坐标内的碰撞体集合。
type CollisionBoxSet struct {
	Loaded bool
	Count  uint8
	Boxes  [8]core.AABB
}

// CollisionSource 查询世界方块的局部碰撞体。
type CollisionSource interface {
	CollisionBoxes(core.BlockPos) CollisionBoxSet
}

// StepResult 是一个固定物理步的结果。
type StepResult struct {
	State      State
	UsedStep   bool
	HitUnknown bool
}

// PlayerBounds 返回以脚底中心为原点的完整玩家包围盒。
func PlayerBounds(position mgl32.Vec3) core.AABB {
	halfWidth := PlayerWidth / 2
	return core.AABB{
		Min: position.Sub(mgl32.Vec3{halfWidth, 0, halfWidth}),
		Max: position.Add(mgl32.Vec3{halfWidth, PlayerHeight, halfWidth}),
	}
}

// BlockCollisionBoxes 返回当前方块的局部碰撞体。
// 流体（core.IsFluid）与空气同形状——已加载但零碰撞体：spec Requirement
// 「流体方块编码」要求流体 MUST NOT 提供碰撞体，实体必须能自由穿行。
func BlockCollisionBoxes(id core.BlockID, loaded bool) CollisionBoxSet {
	if !loaded {
		return CollisionBoxSet{}
	}
	if id == core.AirID || core.IsFluid(id) {
		return CollisionBoxSet{Loaded: true}
	}
	return CollisionBoxSet{
		Loaded: true,
		Count:  1,
		Boxes: [8]core.AABB{{
			Min: mgl32.Vec3{},
			Max: mgl32.Vec3{1, 1, 1},
		}},
	}
}

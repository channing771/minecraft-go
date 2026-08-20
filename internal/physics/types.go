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

// Input 是单个固定步的玩家控制意图。
type Input struct {
	MoveX int8
	MoveZ int8
	Jump  bool
	Yaw   float32
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

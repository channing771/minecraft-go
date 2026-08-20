package physics

import (
	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// FluidSource 查询世界方块是否为流体。
//
// 它与 CollisionSource 刻意分开：CollisionSource 只交付碰撞盒，而流体没有碰撞盒
// （BlockCollisionBoxes 对流体返回「已加载但零碰撞体」），因此浸没判定拿不到任何
// 可用信息。两个接口各自只暴露自己需要的那一份方块视图，服务端 Dimension 与客户端
// Mirror 同时实现二者。
type FluidSource interface {
	// IsFluidAt 报告该格是否为流体。未加载、已失同步或超出世界高度的格 MUST 返回
	// false——浸没标志宁可漏判也不能凭空捏造，否则客户端会在区块尚未到达时预测出
	// 一段服务端不存在的水中物理。
	IsFluidAt(core.BlockPos) bool
}

// SubmersionFlags 计算玩家的两个浸没标志：
//
//   - bodyInFluid：玩家 AABB 与任意流体格有正体积相交；
//   - eyeInFluid：眼睛所在格是流体。
//
// 服务端权威 tick 与客户端预测 MUST 调用本函数，而不是各自镜像同一套规则——
// 两侧独立实现同一规则时，「一起写错」不会被任何 parity 断言抓到（差值恒等），
// 这正是本项目历轮评审反复抓到的假绿模式。两侧唯一允许不同的是 FluidSource
// 背后的方块镜像。
//
// 眼睛高度取自当前生效的 Tunables 快照，与相机、射线判定读的是同一个值。
//
// position 是脚底中心，MUST 为有限值且落在 int32 可表示的范围内（调用方在权威
// tick 里已由 ValidState 保证）；否则与 Step 一样 panic，不静默返回假值。
func SubmersionFlags(position mgl32.Vec3, source FluidSource) (bodyInFluid, eyeInFluid bool) {
	eyeHeight := ActiveTunables().EyeHeight
	eyeInFluid = source.IsFluidAt(core.BlockPos{
		X: collisionCheckedFloor(position.X()),
		Y: collisionCheckedFloor(position.Y() + eyeHeight),
		Z: collisionCheckedFloor(position.Z()),
	})
	bounds := PlayerBounds(position)
	minimum := core.BlockPos{
		X: collisionCheckedFloor(bounds.Min.X()),
		Y: collisionCheckedFloor(bounds.Min.Y()),
		Z: collisionCheckedFloor(bounds.Min.Z()),
	}
	maximum := core.BlockPos{
		X: fluidCellUpperBound(bounds.Max.X(), minimum.X),
		Y: fluidCellUpperBound(bounds.Max.Y(), minimum.Y),
		Z: fluidCellUpperBound(bounds.Max.Z(), minimum.Z),
	}
	for y := minimum.Y; y <= maximum.Y; y++ {
		for x := minimum.X; x <= maximum.X; x++ {
			for z := minimum.Z; z <= maximum.Z; z++ {
				if source.IsFluidAt(core.BlockPos{X: x, Y: y, Z: z}) {
					return true, eyeInFluid
				}
			}
		}
	}
	return false, eyeInFluid
}

// fluidCellUpperBound 返回 AABB 上界所覆盖的最后一格。
//
// 取 ceil(maximum)−1 而不是 floor(maximum)：AABB 恰好贴在格边界上（例如玩家
// 中心 x=0.7、半宽 0.3 时上界正好是 1.0）时，相邻格的相交体积为零，不算浸没。
// 用 floor 会把这种「只是贴着」误判为身体入水，且在水岸边缘每一步都会触发。
// 退化到 lower 之下时钳回 lower，保证至少扫描一格。
func fluidCellUpperBound(maximum float32, lower int32) int32 {
	bound := collisionCheckedCeil(maximum) - 1
	if bound < lower {
		return lower
	}
	return bound
}

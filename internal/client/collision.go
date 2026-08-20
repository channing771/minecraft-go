package client

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// MirrorCollisionSource 把指定维度的客户端世界镜像适配为共享物理碰撞查询。
type MirrorCollisionSource struct {
	Mirror    *Mirror
	Dimension core.DimensionID
}

// CollisionBoxes 返回镜像方块的碰撞体；缺失区块保持为未加载状态。
func (source MirrorCollisionSource) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return physics.BlockCollisionBoxes(core.AirID, true)
	}
	chunk, loaded := source.Mirror.Chunk(source.Dimension, position.Chunk())
	if !loaded || chunk.Desynced {
		return physics.CollisionBoxSet{}
	}
	x, _, z := position.Local()
	return physics.BlockCollisionBoxes(chunk.Chunk.BlockAt(x, position.Y, z), true)
}

// IsFluidAt 让客户端镜像充当 physics.FluidSource：与权威侧
// dimensionCollisionSource.IsFluidAt 逐条对应——缺失或已失同步的区块返回
// false，超出世界高度的格视为空气。判定规则本身不在这里，而在两侧共用的
// physics.SubmersionFlags。
func (source MirrorCollisionSource) IsFluidAt(position core.BlockPos) bool {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return false
	}
	chunk, loaded := source.Mirror.Chunk(source.Dimension, position.Chunk())
	if !loaded || chunk.Desynced {
		return false
	}
	x, _, z := position.Local()
	return core.IsFluid(chunk.Chunk.BlockAt(x, position.Y, z))
}

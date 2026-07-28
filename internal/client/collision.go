package client

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
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

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
	block, loaded := source.Mirror.BlockAt(source.Dimension, position)
	return physics.BlockCollisionBoxes(block, loaded)
}

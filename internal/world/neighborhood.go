package world

import "minecraft-go/internal/core"

// Neighborhood 是网格化的输入：一个中心区段加周围 3×3×3 邻域。
//
// Around 的下标是 [dx+1][dy+1][dz+1]。棱角邻居不仅用于面剔除，
// 也用于 AO 在区段边缘的三格采样；缺失会形成永久暗缝。
// 邻居为 nil 表示尚未加载，At 会返回 BarrierID（按实心处理）——
// 这样不会在未加载边界上生成一批注定被遮住、且邻居到位后必须重做的面。
type Neighborhood struct {
	Center *Section
	Around [3][3][3]*Section
}

// At 读取局部坐标处的方块，三个分量各自允许 -1..16。
// 越界分量会映射到 Around 中对应的面、棱或角邻居。
func (n *Neighborhood) At(x, y, z int) BlockID {
	c := [3]int{x, y, z}
	cell := [3]int{1, 1, 1}
	outside := false
	for i, v := range c {
		if v < -1 || v > 16 {
			return BarrierID
		}
		switch v {
		case -1:
			cell[i], c[i], outside = 0, 15, true
		case 16:
			cell[i], c[i], outside = 2, 0, true
		}
	}

	if !outside {
		return n.Center.Blocks.Get(x, y, z)
	}

	nb := n.Around[cell[0]][cell[1]][cell[2]]
	if nb == nil {
		return BarrierID
	}
	return nb.Blocks.Get(c[0], c[1], c[2])
}

// NeighborhoodAt 组装一个区段的网格化邻域。
//
// get 返回给定区块，不存在时返回 nil。Around 会填满所有已加载的
// 面、棱、角邻居；越出世界高度或邻居未加载时留 nil，
// At 会按 BarrierID（实心）处理。
func NeighborhoodAt(get func(core.ChunkPos) *Chunk, pos core.ChunkPos, si int) *Neighborhood {
	self := get(pos)
	if self == nil {
		return nil
	}
	n := &Neighborhood{Center: self.Section(si)}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			ch := get(core.ChunkPos{X: pos.X + int32(dx), Z: pos.Z + int32(dz)})
			if ch == nil {
				continue
			}
			for dy := -1; dy <= 1; dy++ {
				nsi := si + dy
				if nsi < 0 || nsi >= core.SectionsPerChunk {
					continue
				}
				n.Around[dx+1][dy+1][dz+1] = ch.Section(nsi)
			}
		}
	}
	return n
}

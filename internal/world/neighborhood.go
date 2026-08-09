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

	// SectionY 是中心区段的索引，用于把局部 Y 还原成世界 Y。
	SectionY int
	// Heights 是九个水平邻区列顶表的不可变快照，下标为 [dx+1][dz+1]；
	// HeightsPresent 标记该邻区是否已加载，未加载按遮挡处理。
	Heights        [3][3]HeightMap
	HeightsPresent [3][3]bool
}

// SkyLight 返回局部坐标处的直射天空光，每轴允许 -16..31。
// 采样位置严格高于所在列的最高非空气方块时为 15，否则为 0；
// 邻区未加载时按遮挡返回 0。
func (n *Neighborhood) SkyLight(x, y, z int) uint8 {
	if cy, _ := neighborCell(y); cy < 0 {
		return 0
	}
	cx, lx := neighborCell(x)
	cz, lz := neighborCell(z)
	if cx < 0 || cz < 0 || !n.HeightsPresent[cx][cz] {
		return 0
	}
	worldY := int32(core.MinY + n.SectionY*core.SectionSize + y)
	if worldY > n.Heights[cx][cz].Highest(lx, lz) {
		return 15
	}
	return 0
}

const neighborhoodHaloMin = -core.SectionSize
const neighborhoodHaloMax = 2*core.SectionSize - 1

// neighborCell 把 -16..31 的局部分量拆成邻区下标与区块内局部坐标。
// 超出该范围时返回 -1。
func neighborCell(v int) (cell, local int) {
	if v < neighborhoodHaloMin || v > neighborhoodHaloMax {
		return -1, 0
	}
	shifted := v + core.SectionSize
	return shifted >> core.SectionShift, shifted & core.SectionMask
}

// At 读取局部坐标处的方块，三个分量各自允许 -16..31。
// 越界分量会映射到 Around 中对应的面、棱或角邻居。
func (n *Neighborhood) At(x, y, z int) BlockID {
	c := [3]int{x, y, z}
	var cell [3]int
	outside := false
	for i, v := range c {
		cell[i], c[i] = neighborCell(v)
		if cell[i] < 0 {
			return BarrierID
		}
		if cell[i] != 1 {
			outside = true
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
	n := &Neighborhood{Center: self.Section(si), SectionY: si}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			ch := get(core.ChunkPos{X: pos.X + int32(dx), Z: pos.Z + int32(dz)})
			if ch == nil {
				continue
			}
			n.Heights[dx+1][dz+1] = ch.Heights()
			n.HeightsPresent[dx+1][dz+1] = true
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

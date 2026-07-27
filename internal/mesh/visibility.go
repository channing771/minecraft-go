package mesh

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// Connectivity 记录一个区段的 6 个面之间哪些两两可达，共 15 对，占 15 位。
type Connectivity uint16

// pairBit 返回面对 (a,b) 在位掩码中的位号，要求 a < b。
func pairBit(a, b Face) uint {
	return uint(a)*5 - uint(a)*(uint(a)-1)/2 + uint(b) - uint(a) - 1
}

// Connected 返回两个面之间是否可达。a == b 时返回 true。
func (c Connectivity) Connected(a, b Face) bool {
	if a == b {
		return true
	}
	if a > b {
		a, b = b, a
	}
	return c&(1<<pairBit(a, b)) != 0
}

// faceOf 把局部坐标贴到的所有区段面传给 add。
func faceOf(x, y, z int, add func(Face)) {
	if x == 0 {
		add(FaceNegX)
	}
	if x == 15 {
		add(FacePosX)
	}
	if y == 0 {
		add(FaceNegY)
	}
	if y == 15 {
		add(FacePosY)
	}
	if z == 0 {
		add(FaceNegZ)
	}
	if z == 15 {
		add(FacePosZ)
	}
}

// ComputeConnectivity 用洪水填充算出区段的面连通性。
func ComputeConnectivity(s *world.Section, reg Registry) Connectivity {
	var visited [core.BlocksPerSection]bool
	var out Connectivity
	queue := make([]int32, 0, core.BlocksPerSection)

	at := func(i int32) (x, y, z int) {
		return int(i & 15), int((i >> 8) & 15), int((i >> 4) & 15)
	}
	idxOf := func(x, y, z int) int32 { return int32(y<<8 | z<<4 | x) }

	for start := int32(0); start < core.BlocksPerSection; start++ {
		if visited[start] {
			continue
		}
		sx, sy, sz := at(start)
		if reg.Opaque(s.Blocks.Get(sx, sy, sz)) {
			visited[start] = true
			continue
		}

		var touched uint8
		queue = queue[:0]
		queue = append(queue, start)
		visited[start] = true

		for len(queue) > 0 {
			cur := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			x, y, z := at(cur)
			faceOf(x, y, z, func(f Face) { touched |= 1 << f })

			for _, d := range [6][3]int{
				{-1, 0, 0}, {1, 0, 0},
				{0, -1, 0}, {0, 1, 0},
				{0, 0, -1}, {0, 0, 1},
			} {
				nx, ny, nz := x+d[0], y+d[1], z+d[2]
				if nx < 0 || nx > 15 || ny < 0 || ny > 15 || nz < 0 || nz > 15 {
					continue
				}
				ni := idxOf(nx, ny, nz)
				if visited[ni] {
					continue
				}
				visited[ni] = true
				if reg.Opaque(s.Blocks.Get(nx, ny, nz)) {
					continue
				}
				queue = append(queue, ni)
			}
		}

		for a := Face(0); a < 6; a++ {
			if touched&(1<<a) == 0 {
				continue
			}
			for b := a + 1; b < 6; b++ {
				if touched&(1<<b) != 0 {
					out |= 1 << pairBit(a, b)
				}
			}
		}
	}
	return out
}

func opposite(f Face) Face { return f ^ 1 }

func stepOf(f Face) (dx, dy, dz int32) {
	d := int32(1)
	if !f.Positive() {
		d = -1
	}
	switch f.Axis() {
	case 0:
		return d, 0, 0
	case 1:
		return 0, d, 0
	default:
		return 0, 0, d
	}
}

// EverythingVisible 返回一个不剔除任何东西的视锥，供测试使用。
func EverythingVisible() core.Frustum {
	var f core.Frustum
	for i := range f {
		f[i] = [4]float32{0, 0, 0, 1}
	}
	return f
}

// VisibleSections 从相机所在区段做广度优先遍历，返回可见候选区段。
func VisibleSections(
	origin core.SectionPos,
	radius int,
	frustum core.Frustum,
	lookup func(core.SectionPos) (Connectivity, bool),
) []core.SectionPos {
	type node struct {
		pos      core.SectionPos
		entry    Face // 上一个区段离开时使用的面；其对面是本区段的进入面。
		isOrigin bool
	}

	out := make([]core.SectionPos, 0, 512)
	emitted := make(map[core.SectionPos]bool, 1024)
	enqueued := make(map[core.SectionPos]uint8, 1024)

	queue := []node{{pos: origin, isOrigin: true}}
	emitted[origin] = true
	out = append(out, origin)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		conn, loaded := lookup(cur.pos)
		if !loaded {
			continue
		}

		for exit := Face(0); exit < 6; exit++ {
			if !cur.isOrigin && !conn.Connected(opposite(cur.entry), exit) {
				continue
			}
			dx, dy, dz := stepOf(exit)
			np := core.SectionPos{X: cur.pos.X + dx, Y: cur.pos.Y + dy, Z: cur.pos.Z + dz}

			if np.Y < 0 || np.Y >= core.SectionsPerChunk {
				continue
			}
			if abs32(np.X-origin.X) > int32(radius) || abs32(np.Z-origin.Z) > int32(radius) {
				continue
			}
			if !frustum.IntersectsAABB(sectionAABB(np)) {
				continue
			}

			if !emitted[np] {
				emitted[np] = true
				out = append(out, np)
			}

			bit := uint8(1) << exit
			if enqueued[np]&bit != 0 {
				continue
			}
			enqueued[np] |= bit
			queue = append(queue, node{pos: np, entry: exit})
		}
	}
	return out
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func sectionAABB(p core.SectionPos) core.AABB {
	c := p.MinCorner()
	return core.AABB{
		Min: [3]float32{float32(c.X), float32(c.Y), float32(c.Z)},
		Max: [3]float32{float32(c.X + 16), float32(c.Y + 16), float32(c.Z + 16)},
	}
}

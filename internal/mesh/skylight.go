package mesh

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	skyLightMin    = -core.SectionSize
	skyLightSide   = 3 * core.SectionSize
	skyLightVolume = skyLightSide * skyLightSide * skyLightSide
)

// SkyLightScratch 保存一次区段网格化复用的有界天空光传播状态。
type SkyLightScratch struct {
	levels [skyLightVolume]uint8
	queue  [skyLightVolume]uint32
	head   int
	tail   int
}

// NewSkyLightScratch 创建固定容量的天空光传播 scratch。
func NewSkyLightScratch() *SkyLightScratch { return new(SkyLightScratch) }

func skyLightIndex(x, y, z int) int {
	return ((x-skyLightMin)*skyLightSide+(y-skyLightMin))*skyLightSide + z - skyLightMin
}

func (s *SkyLightScratch) at(x, y, z int) uint8 {
	if x < skyLightMin || x >= skyLightMin+skyLightSide ||
		y < skyLightMin || y >= skyLightMin+skyLightSide ||
		z < skyLightMin || z >= skyLightMin+skyLightSide {
		return 0
	}
	return s.levels[skyLightIndex(x, y, z)]
}

func (s *SkyLightScratch) enqueue(index int) {
	if s.tail == len(s.queue) {
		panic("mesh: 天空光内部队列溢出")
	}
	s.queue[s.tail] = uint32(index)
	s.tail++
}

func (s *SkyLightScratch) build(n *world.Neighborhood, reg Registry) {
	clear(s.levels[:])
	s.head = 0
	s.tail = 0
	for x := skyLightMin; x < skyLightMin+skyLightSide; x++ {
		for y := skyLightMin; y < skyLightMin+skyLightSide; y++ {
			for z := skyLightMin; z < skyLightMin+skyLightSide; z++ {
				if n.SkyLight(x, y, z) != 15 || reg.Opaque(n.At(x, y, z)) {
					continue
				}
				index := skyLightIndex(x, y, z)
				s.levels[index] = 15
				s.enqueue(index)
			}
		}
	}

	directions := [...]struct{ x, y, z int }{
		{-1, 0, 0}, {1, 0, 0},
		{0, -1, 0}, {0, 1, 0},
		{0, 0, -1}, {0, 0, 1},
	}
	for s.head < s.tail {
		index := int(s.queue[s.head])
		s.head++
		z := index%skyLightSide + skyLightMin
		index /= skyLightSide
		y := index%skyLightSide + skyLightMin
		x := index/skyLightSide + skyLightMin
		candidate := s.at(x, y, z) - 1
		if candidate == 0 {
			continue
		}
		for _, direction := range directions {
			nx, ny, nz := x+direction.x, y+direction.y, z+direction.z
			if nx < skyLightMin || nx >= skyLightMin+skyLightSide ||
				ny < skyLightMin || ny >= skyLightMin+skyLightSide ||
				nz < skyLightMin || nz >= skyLightMin+skyLightSide ||
				reg.Opaque(n.At(nx, ny, nz)) {
				continue
			}
			next := skyLightIndex(nx, ny, nz)
			if s.levels[next] >= candidate {
				continue
			}
			s.levels[next] = candidate
			s.enqueue(next)
		}
	}
}

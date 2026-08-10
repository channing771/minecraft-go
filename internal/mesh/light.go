package mesh

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

const (
	lightMin    = -core.SectionSize
	lightSide   = 3 * core.SectionSize
	lightVolume = lightSide * lightSide * lightSide
	skyMask     = uint8(0xf0)
	blockMask   = uint8(0x0f)
)

var lightDirections = [...]struct{ x, y, z int }{
	{-1, 0, 0}, {1, 0, 0},
	{0, -1, 0}, {0, 1, 0},
	{0, 0, -1}, {0, 0, 1},
}

// LightScratch 保存一次区段网格化复用的有界光照传播状态。
type LightScratch struct {
	levels [lightVolume]uint8
	queue  [lightVolume]uint32
	head   int
	tail   int
}

// NewLightScratch 创建固定容量的光照传播 scratch。
func NewLightScratch() *LightScratch { return new(LightScratch) }

func lightIndex(x, y, z int) int {
	return ((x-lightMin)*lightSide+(y-lightMin))*lightSide + z - lightMin
}

func (s *LightScratch) at(x, y, z int) uint8 {
	if x < lightMin || x >= lightMin+lightSide ||
		y < lightMin || y >= lightMin+lightSide ||
		z < lightMin || z >= lightMin+lightSide {
		return 0
	}
	return s.levels[lightIndex(x, y, z)]
}

func (s *LightScratch) enqueue(index int) {
	if s.tail == len(s.queue) {
		panic("mesh: 光照内部队列溢出")
	}
	s.queue[s.tail] = uint32(index)
	s.tail++
}

func (s *LightScratch) build(n *world.Neighborhood, reg Registry) {
	clear(s.levels[:])
	s.head, s.tail = 0, 0
	s.buildSky(n, reg)
	s.head, s.tail = 0, 0
	s.buildBlock(n, reg)
}

func (s *LightScratch) buildSky(n *world.Neighborhood, reg Registry) {
	for x := lightMin; x < lightMin+lightSide; x++ {
		for y := lightMin; y < lightMin+lightSide; y++ {
			for z := lightMin; z < lightMin+lightSide; z++ {
				if n.SkyLight(x, y, z) != 15 || reg.Opaque(n.At(x, y, z)) {
					continue
				}
				index := lightIndex(x, y, z)
				s.levels[index] = skyMask
				s.enqueue(index)
			}
		}
	}

	for s.head < s.tail {
		index := int(s.queue[s.head])
		s.head++
		z := index%lightSide + lightMin
		index /= lightSide
		y := index%lightSide + lightMin
		x := index/lightSide + lightMin
		current := s.at(x, y, z) >> 4
		if current <= 1 {
			continue
		}
		candidate := current - 1
		for _, direction := range lightDirections {
			nx, ny, nz := x+direction.x, y+direction.y, z+direction.z
			if nx < lightMin || nx >= lightMin+lightSide ||
				ny < lightMin || ny >= lightMin+lightSide ||
				nz < lightMin || nz >= lightMin+lightSide {
				continue
			}
			next := lightIndex(nx, ny, nz)
			if s.levels[next]>>4 >= candidate || reg.Opaque(n.At(nx, ny, nz)) {
				continue
			}
			s.levels[next] = s.levels[next]&blockMask | candidate<<4
			s.enqueue(next)
		}
	}
}

func (s *LightScratch) buildBlock(n *world.Neighborhood, reg Registry) {
	for x := lightMin; x < lightMin+lightSide; x++ {
		for y := lightMin; y < lightMin+lightSide; y++ {
			for z := lightMin; z < lightMin+lightSide; z++ {
				level := reg.Emission(n.At(x, y, z))
				if level == 0 {
					continue
				}
				if level > 15 {
					panic("mesh: 方块发光等级超过 15")
				}
				index := lightIndex(x, y, z)
				s.levels[index] = s.levels[index]&skyMask | level
				s.enqueue(index)
			}
		}
	}

	for s.head < s.tail {
		index := int(s.queue[s.head])
		s.head++
		z := index%lightSide + lightMin
		index /= lightSide
		y := index%lightSide + lightMin
		x := index/lightSide + lightMin
		current := s.at(x, y, z) & blockMask
		if current <= 1 {
			continue
		}
		candidate := current - 1
		for _, direction := range lightDirections {
			nx, ny, nz := x+direction.x, y+direction.y, z+direction.z
			if nx < lightMin || nx >= lightMin+lightSide ||
				ny < lightMin || ny >= lightMin+lightSide ||
				nz < lightMin || nz >= lightMin+lightSide {
				continue
			}
			next := lightIndex(nx, ny, nz)
			if s.levels[next]&blockMask >= candidate || n.At(nx, ny, nz) != world.AirID {
				continue
			}
			s.levels[next] = s.levels[next]&skyMask | candidate
			s.enqueue(next)
		}
	}
}

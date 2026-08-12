package mesh_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
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

// goLightScratch 保存一次区段网格化复用的有界光照传播 oracle 状态。
type goLightScratch struct {
	levels [lightVolume]uint8
	queue  [lightVolume]uint32
	head   int
	tail   int
}

// newGoLightScratch 创建固定容量的光照传播 oracle scratch。
func newGoLightScratch() *goLightScratch { return new(goLightScratch) }

func lightIndex(x, y, z int) int {
	return ((x-lightMin)*lightSide+(y-lightMin))*lightSide + z - lightMin
}

func (s *goLightScratch) at(x, y, z int) uint8 {
	if x < lightMin || x >= lightMin+lightSide ||
		y < lightMin || y >= lightMin+lightSide ||
		z < lightMin || z >= lightMin+lightSide {
		return 0
	}
	return s.levels[lightIndex(x, y, z)]
}

func (s *goLightScratch) enqueue(index int) {
	if s.tail == len(s.queue) {
		panic("mesh: 光照内部队列溢出")
	}
	s.queue[s.tail] = uint32(index)
	s.tail++
}

func (s *goLightScratch) build(n *world.Neighborhood, reg mesh.Registry) {
	clear(s.levels[:])
	s.head, s.tail = 0, 0
	s.buildSky(n, reg)
	s.head, s.tail = 0, 0
	s.buildBlock(n, reg)
}

func (s *goLightScratch) buildSky(n *world.Neighborhood, reg mesh.Registry) {
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

func (s *goLightScratch) buildBlock(n *world.Neighborhood, reg mesh.Registry) {
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

type oracleCountingRegistry struct {
	opaqueQueries   int
	emissionQueries int
}

func (r *oracleCountingRegistry) Opaque(world.BlockID) bool {
	r.opaqueQueries++
	return false
}

func (*oracleCountingRegistry) FaceVisible(world.BlockID, world.BlockID) bool { return false }
func (*oracleCountingRegistry) Material(world.BlockID, mesh.Face) uint16      { return 0 }
func (r *oracleCountingRegistry) Emission(world.BlockID) uint8 {
	r.emissionQueries++
	return 0
}
func (*oracleCountingRegistry) MeshSnapshot() mesh.RegistrySnapshot {
	panic("oracleCountingRegistry.MeshSnapshot 不应被调用")
}

type oracleOverbrightRegistry struct{ testRegistry }

func (oracleOverbrightRegistry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 16
	}
	return 0
}

func fullyLoadedAirNeighborhoodOracle() *world.Neighborhood {
	n := &world.Neighborhood{Center: world.NewSection(), SectionY: 8}
	for dx := range n.Around {
		for dy := range n.Around[dx] {
			for dz := range n.Around[dx][dy] {
				n.Around[dx][dy][dz] = world.NewSection()
			}
		}
	}
	for dx := range n.HeightsPresent {
		for dz := range n.HeightsPresent[dx] {
			n.HeightsPresent[dx][dz] = true
		}
	}
	return n
}

func fillOracleSection(section *world.Section, id world.BlockID) {
	for y := 0; y < core.SectionSize; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				section.Blocks.Set(x, y, z, id)
			}
		}
	}
}

func TestGoLightOracleExactCapacityAndStableBuildDoesNotAllocate(t *testing.T) {
	if got, want := len(new(goLightScratch).levels), 48*48*48; got != want {
		t.Fatalf("levels=%d，想要 %d", got, want)
	}
	if got, want := len(new(goLightScratch).queue), 48*48*48; got != want {
		t.Fatalf("queue=%d，想要 %d", got, want)
	}
	n := fullyLoadedAirNeighborhoodOracle()
	scratch := newGoLightScratch()
	scratch.build(n, testRegistry{})
	if got := testing.AllocsPerRun(100, func() { scratch.build(n, testRegistry{}) }); got != 0 {
		t.Fatalf("稳定传播分配=%v，想要 0", got)
	}
}

func TestGoLightOracleDoesNotSampleSettledNeighbors(t *testing.T) {
	n := fullyLoadedAirNeighborhoodOracle()
	reg := new(oracleCountingRegistry)

	newGoLightScratch().build(n, reg)

	if got, want := reg.opaqueQueries, lightVolume; got != want {
		t.Fatalf("稳定全直射输入的不透明查询=%d，想要仅种子扫描的 %d", got, want)
	}
}

func TestGoLightOracleWorstCaseMultipleSourcesFitsExactQueue(t *testing.T) {
	n := fullyLoadedAirNeighborhoodOracle()
	fillOracleSection(n.Center, core.LightBlockID)
	for dx := range n.Around {
		for dy := range n.Around[dx] {
			for dz, section := range n.Around[dx][dy] {
				if dx == 1 && dy == 1 && dz == 1 {
					continue
				}
				fillOracleSection(section, core.LightBlockID)
			}
		}
	}

	scratch := newGoLightScratch()
	scratch.build(n, testRegistry{})
	if got, want := scratch.tail, 48*48*48; got != want {
		t.Fatalf("全邻域多光源入队=%d，想要精确容量 %d", got, want)
	}
}

func TestGoLightOracleBuildScansEachCellOnceForEmission(t *testing.T) {
	n := fullyLoadedAirNeighborhoodOracle()
	reg := new(oracleCountingRegistry)

	newGoLightScratch().build(n, reg)

	if got, want := reg.emissionQueries, 48*48*48; got != want {
		t.Fatalf("Emission 扫描=%d，想要精确 %d", got, want)
	}
}

func TestGoLightOracleReusesQueueBetweenSkyAndBlockPasses(t *testing.T) {
	n := fullyLoadedAirNeighborhoodOracle()
	n.Center.Blocks.Set(8, 8, 8, core.LightBlockID)
	scratch := newGoLightScratch()

	scratch.build(n, testRegistry{})

	if got, want := scratch.tail, 4089; got != want {
		t.Fatalf("方块光 pass 入队=%d，想要复用清空后的队列得到 %d", got, want)
	}
	if got := scratch.at(9, 8, 8) & 0x0f; got != 14 {
		t.Fatalf("光源相邻方块光=%d，想要 14", got)
	}
}

func TestGoLightOracleRejectsEmissionAboveFifteen(t *testing.T) {
	n := fullyLoadedAirNeighborhoodOracle()
	n.Center.Blocks.Set(8, 8, 8, core.LightBlockID)
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("Emission=16 未触发 panic")
		}
	}()
	newGoLightScratch().build(n, oracleOverbrightRegistry{})
}

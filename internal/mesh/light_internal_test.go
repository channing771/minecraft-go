package mesh

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

type internalTestRegistry struct{}

func (internalTestRegistry) Opaque(id world.BlockID) bool { return id != world.AirID }
func (internalTestRegistry) FaceVisible(id, adjacent world.BlockID) bool {
	return id != world.AirID && adjacent == world.AirID
}
func (internalTestRegistry) Material(world.BlockID, Face) uint16 {
	return 0
}
func (internalTestRegistry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 15
	}
	return 0
}

type countingRegistry struct {
	opaqueQueries   int
	emissionQueries int
}

type overbrightRegistry struct{ internalTestRegistry }

func (overbrightRegistry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 16
	}
	return 0
}

func (r *countingRegistry) Opaque(world.BlockID) bool {
	r.opaqueQueries++
	return false
}

func (*countingRegistry) FaceVisible(world.BlockID, world.BlockID) bool { return false }
func (*countingRegistry) Material(world.BlockID, Face) uint16           { return 0 }
func (r *countingRegistry) Emission(world.BlockID) uint8 {
	r.emissionQueries++
	return 0
}

func fullyLoadedAirNeighborhood() *world.Neighborhood {
	n := &world.Neighborhood{
		Center:   world.NewSection(),
		SectionY: 8,
	}
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

func TestLightScratchExactCapacityAndStableBuildDoesNotAllocate(t *testing.T) {
	if got, want := len(new(LightScratch).levels), 48*48*48; got != want {
		t.Fatalf("levels=%d，想要 %d", got, want)
	}
	if got, want := len(new(LightScratch).queue), 48*48*48; got != want {
		t.Fatalf("queue=%d，想要 %d", got, want)
	}
	n := fullyLoadedAirNeighborhood()
	scratch := NewLightScratch()
	scratch.build(n, internalTestRegistry{})
	if got := testing.AllocsPerRun(100, func() { scratch.build(n, internalTestRegistry{}) }); got != 0 {
		t.Fatalf("稳定传播分配=%v，想要 0", got)
	}
}

func TestLightScratchDoesNotSampleSettledNeighbors(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	reg := new(countingRegistry)

	NewLightScratch().build(n, reg)

	if got, want := reg.opaqueQueries, lightVolume; got != want {
		t.Fatalf("稳定全直射输入的不透明查询=%d，想要仅种子扫描的 %d", got, want)
	}
}

func TestMeshSectionSkipsSingleAirWork(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	reg := new(countingRegistry)

	if quads := MeshSection(n, reg, NewLightScratch()); len(quads) != 0 {
		t.Fatalf("single-air 区段产生了 %d 个面，想要 0", len(quads))
	}
	if reg.opaqueQueries != 0 || reg.emissionQueries != 0 {
		t.Fatalf("single-air 区段执行了 opaque=%d emission=%d 次查询，想要都为 0", reg.opaqueQueries, reg.emissionQueries)
	}
}

func fillSection(section *world.Section, id world.BlockID) {
	for y := 0; y < core.SectionSize; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				section.Blocks.Set(x, y, z, id)
			}
		}
	}
}

func TestLightScratchWorstCaseMultipleSourcesFitsExactQueue(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	fillSection(n.Center, core.LightBlockID)
	for dx := range n.Around {
		for dy := range n.Around[dx] {
			for dz, section := range n.Around[dx][dy] {
				if dx == 1 && dy == 1 && dz == 1 {
					continue
				}
				fillSection(section, core.LightBlockID)
			}
		}
	}

	scratch := NewLightScratch()
	scratch.build(n, internalTestRegistry{})
	if got, want := scratch.tail, 48*48*48; got != want {
		t.Fatalf("全邻域多光源入队=%d，想要精确容量 %d", got, want)
	}
}

func TestLightScratchBuildScansEachCellOnceForEmission(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	reg := new(countingRegistry)

	NewLightScratch().build(n, reg)

	if got, want := reg.emissionQueries, 48*48*48; got != want {
		t.Fatalf("Emission 扫描=%d，想要精确 %d", got, want)
	}
}

func TestLightScratchReusesQueueBetweenSkyAndBlockPasses(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	n.Center.Blocks.Set(8, 8, 8, core.LightBlockID)
	scratch := NewLightScratch()

	scratch.build(n, internalTestRegistry{})

	if got, want := scratch.tail, 4089; got != want {
		t.Fatalf("方块光 pass 入队=%d，想要复用清空后的队列得到 %d", got, want)
	}
	if got := scratch.at(9, 8, 8) & 0x0f; got != 14 {
		t.Fatalf("光源相邻方块光=%d，想要 14", got)
	}
}

func TestLightScratchRejectsEmissionAboveFifteen(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	n.Center.Blocks.Set(8, 8, 8, core.LightBlockID)
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("Emission=16 未触发 panic")
		}
	}()
	NewLightScratch().build(n, overbrightRegistry{})
}

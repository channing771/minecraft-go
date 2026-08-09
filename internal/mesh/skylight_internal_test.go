package mesh

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

type internalTestRegistry struct{}

func (internalTestRegistry) Opaque(id world.BlockID) bool { return id != world.AirID }
func (internalTestRegistry) Material(world.BlockID, Face) uint16 {
	return 0
}

type countingOpaqueRegistry struct {
	queries int
}

func (r *countingOpaqueRegistry) Opaque(world.BlockID) bool {
	r.queries++
	return false
}

func (*countingOpaqueRegistry) Material(world.BlockID, Face) uint16 { return 0 }

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

func TestSkyLightScratchExactCapacityAndStableBuildDoesNotAllocate(t *testing.T) {
	if got, want := len(new(SkyLightScratch).levels), 48*48*48; got != want {
		t.Fatalf("levels=%d，想要 %d", got, want)
	}
	if got, want := len(new(SkyLightScratch).queue), 48*48*48; got != want {
		t.Fatalf("queue=%d，想要 %d", got, want)
	}
	n := fullyLoadedAirNeighborhood()
	scratch := NewSkyLightScratch()
	scratch.build(n, internalTestRegistry{})
	if got, want := scratch.tail, 3*core.SectionSize*3*core.SectionSize*3*core.SectionSize; got != want {
		t.Fatalf("最坏输入入队=%d，想要 %d", got, want)
	}
	if got := testing.AllocsPerRun(100, func() { scratch.build(n, internalTestRegistry{}) }); got != 0 {
		t.Fatalf("稳定传播分配=%v，想要 0", got)
	}
}

func TestSkyLightScratchDoesNotSampleSettledNeighbors(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	reg := new(countingOpaqueRegistry)

	NewSkyLightScratch().build(n, reg)

	if got, want := reg.queries, skyLightVolume; got != want {
		t.Fatalf("稳定全直射输入的不透明查询=%d，想要仅种子扫描的 %d", got, want)
	}
}

func TestMeshSectionSkipsSingleAirWork(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	reg := new(countingOpaqueRegistry)

	if quads := MeshSection(n, reg, NewSkyLightScratch()); len(quads) != 0 {
		t.Fatalf("single-air 区段产生了 %d 个面，想要 0", len(quads))
	}
	if reg.queries != 0 {
		t.Fatalf("single-air 区段执行了 %d 次不透明查询，想要 0", reg.queries)
	}
}

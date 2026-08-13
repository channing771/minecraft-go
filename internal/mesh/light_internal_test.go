package mesh

import (
	"fmt"
	"strings"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

func TestMeshSectionRejectsNativeABIMismatch(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	n.Center.Blocks.Set(8, 8, 8, core.StoneID)
	scratch := NewLightScratch()
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(fmt.Sprint(recovered), "ABI") {
			t.Fatalf("recovered=%v，想要 ABI mismatch panic", recovered)
		}
	}()
	meshSectionNativeVersionForTest(n, internalTestRegistry{}, scratch, nativeABIVersionCurrent+1)
}

func TestNativeStatusPanicText(t *testing.T) {
	tests := []struct {
		status nativeStatus
		want   string
	}{
		{nativeStatusABIVersion, "mesh: native ABI 版本不匹配"},
		{nativeStatusInvalidArgument, "mesh: native 参数非法"},
		{nativeStatusInput, "mesh: native 输入非法"},
		{nativeStatusScratch, "mesh: native scratch 非法"},
		{nativeStatusRegistry, "mesh: registry snapshot 非法"},
		{nativeStatusEmission, "mesh: 方块发光等级超过 15"},
		{nativeStatusOutputOverflow, "mesh: 四边形输出溢出"},
		{nativeStatusQueueOverflow, "mesh: 光照内部队列溢出"},
		{nativeStatusPanic, "mesh: Rust 网格内部 panic"},
		{nativeStatus(99), "mesh: native 返回未知状态"},
	}
	for _, tt := range tests {
		if got := nativeStatusPanicText(tt.status); got != tt.want {
			t.Fatalf("status=%d panic=%q，想要 %q", tt.status, got, tt.want)
		}
	}
}

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

func (r internalTestRegistry) MeshSnapshot() RegistrySnapshot {
	snapshot, err := BuildRegistrySnapshot([]world.BlockID{
		core.AirID,
		core.BarrierID,
		core.StoneID,
		core.LightBlockID,
	}, r)
	if err != nil {
		panic(err)
	}
	return snapshot
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

func (overbrightRegistry) MeshSnapshot() RegistrySnapshot {
	snapshot := (internalTestRegistry{}).MeshSnapshot()
	for i := range snapshot.Blocks {
		if snapshot.Blocks[i].ID == core.LightBlockID {
			snapshot.Blocks[i].Emission = 16
		}
	}
	return snapshot
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

func (*countingRegistry) MeshSnapshot() RegistrySnapshot {
	panic("countingRegistry.MeshSnapshot 不应被调用")
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

func TestMeshSectionNilScratchPanicsBeforeUniformAirFastPath(t *testing.T) {
	defer func() {
		if recovered := recover(); fmt.Sprint(recovered) != "mesh: nil light scratch" {
			t.Fatalf("recovered=%v，想要 mesh: nil light scratch", recovered)
		}
	}()
	MeshSection(fullyLoadedAirNeighborhood(), internalTestRegistry{}, nil)
}

func TestLightScratchRejectsEmissionAboveFifteen(t *testing.T) {
	n := fullyLoadedAirNeighborhood()
	n.Center.Blocks.Set(8, 8, 8, core.LightBlockID)
	defer func() {
		if recovered := recover(); fmt.Sprint(recovered) != "mesh: 方块发光等级超过 15" {
			t.Fatalf("recovered=%v，想要 mesh: 方块发光等级超过 15", recovered)
		}
	}()
	MeshSection(n, overbrightRegistry{}, NewLightScratch())
}

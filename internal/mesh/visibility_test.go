package mesh_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

func allFacePairs(t *testing.T, c mesh.Connectivity, want bool) {
	t.Helper()
	for a := mesh.Face(0); a < 6; a++ {
		for b := a + 1; b < 6; b++ {
			if got := c.Connected(a, b); got != want {
				t.Fatalf("Connected(%d,%d) = %v，想要 %v", a, b, got, want)
			}
		}
	}
}

func TestConnectivityEmptySectionIsFullyConnected(t *testing.T) {
	allFacePairs(t, mesh.ComputeConnectivity(world.NewSection(), testRegistry{}), true)
}

func TestConnectivitySolidSectionIsFullyBlocked(t *testing.T) {
	s := filledSection(world.BlockID(2))
	allFacePairs(t, mesh.ComputeConnectivity(s, testRegistry{}), false)
}

func TestConnectivityTunnelConnectsOnlyItsAxis(t *testing.T) {
	s := filledSection(world.BlockID(2))
	for x := 0; x < 16; x++ {
		s.Blocks.Set(x, 8, 8, world.AirID)
	}

	c := mesh.ComputeConnectivity(s, testRegistry{})
	for a := mesh.Face(0); a < 6; a++ {
		for b := a + 1; b < 6; b++ {
			want := a == mesh.FaceNegX && b == mesh.FacePosX
			if got := c.Connected(a, b); got != want {
				t.Fatalf("Connected(%d,%d) = %v，想要 %v", a, b, got, want)
			}
		}
	}
}

func TestVisibleSectionsStopsAtSolidWall(t *testing.T) {
	open := mesh.ComputeConnectivity(world.NewSection(), testRegistry{})
	solid := mesh.ComputeConnectivity(filledSection(world.BlockID(2)), testRegistry{})

	origin := core.SectionPos{X: 0, Y: 4, Z: 0}
	lookup := func(p core.SectionPos) (mesh.Connectivity, bool) {
		if p.X == 2 {
			return solid, true
		}
		return open, true
	}

	got := mesh.VisibleSections(origin, 5, mesh.EverythingVisible(), lookup)
	seen := map[core.SectionPos]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen[core.SectionPos{X: 2, Y: 4, Z: 0}] {
		t.Fatal("墙本身应当可见")
	}
	if seen[core.SectionPos{X: 3, Y: 4, Z: 0}] {
		t.Fatal("墙后的区段应被可见性图剔除")
	}
}

func TestVisibleSectionsHasNoDuplicates(t *testing.T) {
	open := mesh.ComputeConnectivity(world.NewSection(), testRegistry{})
	lookup := func(core.SectionPos) (mesh.Connectivity, bool) { return open, true }

	got := mesh.VisibleSections(core.SectionPos{X: 0, Y: 4, Z: 0}, 3,
		mesh.EverythingVisible(), lookup)

	seen := map[core.SectionPos]int{}
	for _, p := range got {
		seen[p]++
		if seen[p] > 1 {
			t.Fatalf("区段 %+v 在结果中出现 %d 次", p, seen[p])
		}
	}
}

func TestVisibleSectionsDoesNotExpandUnloadedSection(t *testing.T) {
	open := mesh.ComputeConnectivity(world.NewSection(), testRegistry{})
	origin := core.SectionPos{X: 0, Y: 4, Z: 0}
	lookup := func(p core.SectionPos) (mesh.Connectivity, bool) {
		if p.X == 1 {
			return 0, false
		}
		return open, true
	}
	got := mesh.VisibleSections(origin, 3, mesh.EverythingVisible(), lookup)
	seen := map[core.SectionPos]bool{}
	for _, p := range got {
		seen[p] = true
	}
	if !seen[core.SectionPos{X: 1, Y: 4, Z: 0}] {
		t.Fatal("未加载区段本身应进入候选结果")
	}
	if seen[core.SectionPos{X: 2, Y: 4, Z: 0}] {
		t.Fatal("不应穿过未加载区段继续扩展")
	}
}

func filledSection(id world.BlockID) *world.Section {
	s := world.NewSection()
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				s.Blocks.Set(x, y, z, id)
			}
		}
	}
	return s
}

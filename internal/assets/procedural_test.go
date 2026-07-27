package assets_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

func TestRegistryAirIsTransparent(t *testing.T) {
	r := assets.NewRegistry()
	if r.Opaque(world.AirID) {
		t.Fatal("空气不应是不透明的")
	}
	if !r.Opaque(core.StoneID) {
		t.Fatal("石头应是不透明的")
	}
}

func TestGrassHasDistinctTopAndSide(t *testing.T) {
	r := assets.NewRegistry()
	top := r.Material(core.GrassID, mesh.FacePosY)
	side := r.Material(core.GrassID, mesh.FaceNegX)
	bottom := r.Material(core.GrassID, mesh.FaceNegY)
	if top == side {
		t.Fatal("草方块的顶面与侧面材质相同")
	}
	if bottom == top {
		t.Fatal("草方块的底面与顶面材质相同")
	}
	if bottom != r.Material(core.DirtID, mesh.FacePosY) {
		t.Fatal("草方块底面应复用泥土材质")
	}
}

func TestEveryLayerIsFullSize(t *testing.T) {
	r := assets.NewRegistry()
	if r.LayerCount() == 0 {
		t.Fatal("材质层数为 0")
	}
	for i := 0; i < r.LayerCount(); i++ {
		if got := len(r.LayerRGBA(i)); got != 16*16*4 {
			t.Fatalf("第 %d 层大小 = %d 字节，想要 %d", i, got, 16*16*4)
		}
	}
}

func TestProceduralTexturesAreDeterministic(t *testing.T) {
	a, b := assets.NewRegistry(), assets.NewRegistry()
	for i := 0; i < a.LayerCount(); i++ {
		pa, pb := a.LayerRGBA(i), b.LayerRGBA(i)
		for j := range pa {
			if pa[j] != pb[j] {
				t.Fatalf("第 %d 层第 %d 字节不一致: %d vs %d", i, j, pa[j], pb[j])
			}
		}
	}
}

func TestTexturesAreNotFlat(t *testing.T) {
	r := assets.NewRegistry()
	for i := 0; i < r.LayerCount(); i++ {
		px := r.LayerRGBA(i)
		distinct := map[[3]byte]struct{}{}
		for j := 0; j < len(px); j += 4 {
			distinct[[3]byte{px[j], px[j+1], px[j+2]}] = struct{}{}
		}
		if len(distinct) < 4 {
			t.Fatalf("第 %d 层只有 %d 种颜色，材质太平", i, len(distinct))
		}
	}
}

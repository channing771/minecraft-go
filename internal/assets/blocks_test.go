package assets_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
)

func TestStoneBrickHasOwnMaterialLayer(t *testing.T) {
	registry := assets.NewRegistry()
	layer := registry.Material(core.StoneBrickID, mesh.FacePosY)
	if layer != assets.LayerStoneBrick {
		t.Fatalf("石砖材质层 = %d，想要 %d", layer, assets.LayerStoneBrick)
	}
	pixels := registry.LayerRGBA(int(layer))
	stone := registry.LayerRGBA(int(assets.LayerStone))
	if len(pixels) != len(stone) {
		t.Fatalf("石砖材质长度 = %d，想要与石头一致 %d", len(pixels), len(stone))
	}
	if string(pixels) == string(stone) {
		t.Fatal("石砖材质与石头完全相同")
	}
}

func TestM4EBlocksHaveDistinctMaterialLayers(t *testing.T) {
	registry := assets.NewRegistry()
	seen := map[uint32]core.BlockID{}
	for _, block := range []core.BlockID{
		core.StoneID, core.StoneBrickID,
		core.CoalOreID, core.IronOreID, core.FurnaceID, core.IronBlockID,
	} {
		layer := registry.Material(block, mesh.FacePosY)
		if other, dup := seen[uint32(layer)]; dup {
			t.Fatalf("方块 %d 与 %d 共用材质层 %d", block, other, layer)
		}
		seen[uint32(layer)] = block
		if len(registry.LayerRGBA(int(layer))) == 0 {
			t.Fatalf("方块 %d 的材质层为空", block)
		}
	}
}

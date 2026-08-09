package assets_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
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
		core.ChestID,
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

// TestChestHasOwnMaterialLayer 覆盖箱子拥有独立于木质褐色相邻方块的材质层。
func TestChestHasOwnMaterialLayer(t *testing.T) {
	registry := assets.NewRegistry()
	layer := registry.Material(core.ChestID, mesh.FacePosY)
	if layer != assets.LayerChest {
		t.Fatalf("箱子材质层 = %d，想要 %d", layer, assets.LayerChest)
	}
	pixels := registry.LayerRGBA(int(layer))
	dirt := registry.LayerRGBA(int(assets.LayerDirt))
	if len(pixels) != len(dirt) {
		t.Fatalf("箱子材质长度 = %d，想要与泥土一致 %d", len(pixels), len(dirt))
	}
	if string(pixels) == string(dirt) {
		t.Fatal("箱子材质与泥土完全相同")
	}
}

// TestLightBlockUsesIndependentLayerAndFixedEmission 杀死复用任一既有层、
// 发光块边框或中心颜色错误，以及发光等级或非光源默认值错误的变异。
func TestLightBlockUsesIndependentLayerAndFixedEmission(t *testing.T) {
	registry := assets.NewRegistry()
	layer := registry.Material(core.LightBlockID, mesh.FacePosY)
	if layer != assets.LayerLightBlock {
		t.Fatalf("发光块材质层=%d，想要 %d", layer, assets.LayerLightBlock)
	}
	pixels := registry.LayerRGBA(int(layer))
	if len(pixels) != 16*16*4 {
		t.Fatalf("发光块材质长度=%d，想要 %d", len(pixels), 16*16*4)
	}
	for i := 3; i < len(pixels); i += 4 {
		if pixels[i] != 255 {
			t.Fatalf("像素 %d alpha=%d，想要 255", i/4, pixels[i])
		}
	}
	if got := [4]byte{pixels[0], pixels[1], pixels[2], pixels[3]}; got != [4]byte{164, 106, 30, 255} {
		t.Fatalf("发光块边框 RGBA=%v，想要 [164 106 30 255]", got)
	}
	center := (7*16 + 7) * 4
	if got := [4]byte{pixels[center], pixels[center+1], pixels[center+2], pixels[center+3]}; got != [4]byte{255, 226, 112, 255} {
		t.Fatalf("发光块中心 RGBA=%v，想要 [255 226 112 255]", got)
	}
	if got := registry.Emission(core.LightBlockID); got != 15 {
		t.Fatalf("发光块 Emission=%d，想要 15", got)
	}
	for _, id := range []world.BlockID{core.StoneID, core.ChestID, world.BlockID(999)} {
		if got := registry.Emission(id); got != 0 {
			t.Fatalf("非光源 %d Emission=%d，想要 0", id, got)
		}
	}
}

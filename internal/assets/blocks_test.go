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

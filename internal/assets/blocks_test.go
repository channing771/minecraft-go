package assets_test

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

func TestRegistryFaceVisible(t *testing.T) {
	r := assets.NewRegistry()
	tests := []struct {
		name         string
		id, adjacent core.BlockID
		want         bool
	}{
		{"空气不出面", core.AirID, core.AirID, false},
		{"未知当前方块不出面", core.MossyCobblestoneID + 1, core.AirID, false},
		{"石头面向空气", core.StoneID, core.AirID, true},
		{"石头面向未知方块关闭", core.StoneID, core.MossyCobblestoneID + 1, false},
		{"石头被石头遮住", core.StoneID, core.StoneID, false},
		{"石头面向玻璃保留", core.StoneID, core.GlassID, true},
		{"玻璃被石头遮住", core.GlassID, core.StoneID, false},
		{"玻璃同类内部面剔除", core.GlassID, core.GlassID, false},
		{"树叶同类内部面剔除", core.LeavesID, core.LeavesID, false},
		{"不同 cutout 内部面剔除", core.GlassID, core.LeavesID, false},
		{"玻璃面向空气", core.GlassID, core.AirID, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.FaceVisible(world.BlockID(tt.id), world.BlockID(tt.adjacent)); got != tt.want {
				t.Fatalf("FaceVisible(%d, %d) = %v，想要 %v", tt.id, tt.adjacent, got, tt.want)
			}
		})
	}
}

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

func TestCutoutBlocksHaveOwnMaterialLayers(t *testing.T) {
	registry := assets.NewRegistry()
	for _, tt := range []struct {
		block core.BlockID
		want  uint16
	}{
		{core.LeavesID, assets.LayerLeaves},
		{core.GlassID, assets.LayerGlass},
	} {
		if got := registry.Material(tt.block, mesh.FacePosY); got != tt.want {
			t.Fatalf("方块 %d 材质层 = %d，想要 %d", tt.block, got, tt.want)
		}
	}
}

func TestCommonMaterialFaceMappings(t *testing.T) {
	r := assets.NewRegistry()
	if r.Material(core.OakLogID, mesh.FacePosY) != assets.LayerOakLogTop ||
		r.Material(core.OakLogID, mesh.FaceNegY) != assets.LayerOakLogTop ||
		r.Material(core.OakLogID, mesh.FacePosX) != assets.LayerOakLogSide {
		t.Fatal("竖向原木顶底/侧面映射错误")
	}
	for _, tt := range []struct {
		block core.BlockID
		layer uint16
	}{
		{core.CobblestoneID, assets.LayerCobblestone},
		{core.SmoothStoneID, assets.LayerSmoothStone},
		{core.SandID, assets.LayerSand},
		{core.GravelID, assets.LayerGravel},
		{core.OakPlanksID, assets.LayerOakPlanks},
		{core.LeavesID, assets.LayerLeaves},
		{core.GlassID, assets.LayerGlass},
		{core.BrickID, assets.LayerBrick},
		{core.WhiteWoolID, assets.LayerWhiteWool},
		{core.RoofTileID, assets.LayerRoofTile},
		{core.ClayID, assets.LayerClay},
		{core.MossyCobblestoneID, assets.LayerMossyCobblestone},
	} {
		if got := r.Material(tt.block, mesh.FacePosX); got != tt.layer {
			t.Fatalf("方块 %d 材质层=%d，想要 %d", tt.block, got, tt.layer)
		}
		if r.Material(tt.block, mesh.FacePosY) != tt.layer {
			t.Fatalf("方块 %d 不应按面变化", tt.block)
		}
	}
	if r.Material(core.SnowBlockID, mesh.FacePosY) != assets.LayerSnowTop ||
		r.Material(core.SnowBlockID, mesh.FacePosX) != assets.LayerSnowSide {
		t.Fatal("雪块顶面与侧面应使用不同材质")
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

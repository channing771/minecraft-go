package assets_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

func TestRegistryFaceVisible(t *testing.T) {
	r := assets.NewRegistry()
	tests := []struct {
		name         string
		id, adjacent core.BlockID
		want         bool
	}{
		{"空气不出面", core.AirID, core.AirID, false},
		// MossyCobblestoneID+1 现在是 WaterSourceID（已注册流体），真正越界的
		// 未知方块编号改为 WaterLevel7ID+1。
		{"未知当前方块不出面", core.WaterLevel7ID + 1, core.AirID, false},
		{"石头面向空气", core.StoneID, core.AirID, true},
		{"石头面向未知方块关闭", core.StoneID, core.WaterLevel7ID + 1, false},
		// 流体已纳入 mesh snapshot 的 ids 范围：水面对空气出面，水下地形
		// 也要透过水出面。id 侧和 adjacent 侧都要覆盖到。
		{"流体面向空气出面", core.WaterSourceID, core.AirID, true},
		{"石头面向流体出面", core.StoneID, core.WaterLevel1ID, true},
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

// TestFluidBlocksAreTransparentAndDark 锁定「mesh 注册表登记流体」：8 个流体
// 编号 Opaque 一律为 false（沿用既有透明方块路径，不被当作不透明遮挡体），
// Emission 一律为 0（不发光）。
func TestFluidBlocksAreTransparentAndDark(t *testing.T) {
	registry := assets.NewRegistry()
	for id := core.WaterSourceID; id <= core.WaterLevel7ID; id++ {
		if registry.Opaque(id) {
			t.Fatalf("流体方块 %d 的 Opaque 应为 false", id)
		}
		if got := registry.Emission(id); got != 0 {
			t.Fatalf("流体方块 %d 的 Emission = %d，想要 0", id, got)
		}
	}
}

// TestFluidHeightMapsLevelToRawHeightExhaustively 对 8 个流体编号穷举断言
// h_raw(level) = 14 - level：源方块 14（即 15/16），最弱的 level 7 得 7（即
// 半格），并断言 0 这个「非流体」哨兵在全部已注册非流体编号上成立——这是
// 「0 不会与合法流体高度冲突」这条位布局前提的可执行守卫。
func TestFluidHeightMapsLevelToRawHeightExhaustively(t *testing.T) {
	registry := assets.NewRegistry()
	for level := uint8(0); level <= 7; level++ {
		id := core.WaterSourceID + world.BlockID(level)
		if got, want := registry.FluidHeight(id), 14-level; got != want {
			t.Fatalf("FluidHeight(level=%d) = %d，想要 %d", level, got, want)
		}
		if got := registry.LightAttenuation(id); got != 1 {
			t.Fatalf("LightAttenuation(level=%d) = %d，想要 1", level, got)
		}
	}
	// 高度必须严格随等级递减，且最弱等级仍有半格（7 即 8/16），不会退化成零面。
	if got, want := registry.FluidHeight(core.WaterLevel7ID), uint8(7); got != want {
		t.Fatalf("最弱流体 FluidHeight=%d，想要 %d", got, want)
	}
	for id := core.AirID; id < core.WaterSourceID; id++ {
		if got := registry.FluidHeight(id); got != 0 {
			t.Fatalf("非流体 %d 的 FluidHeight=%d，想要哨兵 0", id, got)
		}
		if got := registry.LightAttenuation(id); got != 0 {
			t.Fatalf("非流体 %d 的 LightAttenuation=%d，想要 0", id, got)
		}
	}
}

func TestRegistryMeshSnapshotMatchesRegistry(t *testing.T) {
	registry := assets.NewRegistry()
	snapshot := registry.MeshSnapshot()
	if got, want := len(snapshot.Blocks), int(core.WaterLevel7ID)+1; got != want {
		t.Fatalf("snapshot block 数=%d，想要 %d", got, want)
	}
	for id := core.AirID; id <= core.WaterLevel7ID; id++ {
		block := snapshot.Blocks[int(id)]
		if block.FluidHeight != registry.FluidHeight(id) || block.LightAttenuation != registry.LightAttenuation(id) {
			t.Fatalf("block %d snapshot 的流体/衰减字段=%+v", id, block)
		}
		if block.ID != id || block.Opaque != registry.Opaque(id) || block.Emission != registry.Emission(id) {
			t.Fatalf("block %d snapshot=%+v", id, block)
		}
		for face := mesh.Face(0); face < 6; face++ {
			if got, want := block.Materials[face], registry.Material(id, face); got != want {
				t.Fatalf("block %d face %d material=%d，想要 %d", id, face, got, want)
			}
		}
		for adjacent := core.AirID; adjacent <= core.WaterLevel7ID; adjacent++ {
			if got, want := snapshot.FaceVisible(id, adjacent), registry.FaceVisible(id, adjacent); got != want {
				t.Fatalf("FaceVisible(%d, %d)=%v，想要 %v", id, adjacent, got, want)
			}
		}
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

// TestFluidFaceVisibilityRules 穷举流体与全部已注册方块的两个方向组合，锁定三条
// 流体出面规则。它们是 Rust 侧 `RegistryView::face_visible` 实际生效的规则来源：
// Rust 只做位图查表，位图由 BuildRegistrySnapshot 用本函数烘焙，所以规则写在这里、
// 只能在这里被验证。
//
//   - 同为流体的相邻面不可见（水体内部不产生面）；
//   - 流体对空气可见（水面出几何）；
//   - 流体紧邻不透明方块的面不可见（含「流体在实心方块下方」）；
//   - 反方向：不透明方块朝向流体的面可见（水下地形不会消失）。
func TestFluidFaceVisibilityRules(t *testing.T) {
	registry := assets.NewRegistry()
	// 计数用于最后的防空转守卫：三类结论各自都必须真的被走到过。
	var fluidToFluid, fluidToAir, fluidToOpaque, opaqueToFluid int
	for id := core.WaterSourceID; id <= core.WaterLevel7ID; id++ {
		for adjacent := core.AirID; adjacent <= core.WaterLevel7ID; adjacent++ {
			got := registry.FaceVisible(id, adjacent)
			switch {
			case core.IsFluid(adjacent):
				if got {
					t.Fatalf("流体 %d 朝向流体 %d 出面了：水体内部不得产生面", id, adjacent)
				}
				fluidToFluid++
			case adjacent == core.AirID:
				if !got {
					t.Fatalf("流体 %d 朝向空气没有出面：水面画不出来", id)
				}
				fluidToAir++
			case registry.Opaque(adjacent):
				if got {
					t.Fatalf("流体 %d 朝向不透明方块 %d 出面了：该面被完全遮住", id, adjacent)
				}
				fluidToOpaque++
				// 反方向必须可见，否则水下地形整片消失。
				if !registry.FaceVisible(adjacent, id) {
					t.Fatalf("不透明方块 %d 朝向流体 %d 没有出面：水下地形会消失", adjacent, id)
				}
				opaqueToFluid++
			}
		}
	}
	// 防空转守卫排在真实故障断言之后：若某一类组合一次都没走到，上面的断言
	// 对该类规则就是恒真的，此时红的应当是这里。
	if fluidToFluid != 8*8 || fluidToAir != 8 || fluidToOpaque == 0 || opaqueToFluid == 0 {
		t.Fatalf("覆盖不足：流体-流体=%d（想要 64）、流体-空气=%d（想要 8）、"+
			"流体-不透明=%d、不透明-流体=%d（后两者均须大于 0）",
			fluidToFluid, fluidToAir, fluidToOpaque, opaqueToFluid)
	}
}

// TestFluidBlocksUseDedicatedWaterMaterialLayer 锁定「流体有独立材质层」：
// 8 个流体编号的全部 6 个面都必须返回 assets.LayerWater，且该层不得与任何
// 非流体方块的任何面共用。
//
// 这条断言承重在于：流体曾经落在 Material 的 `default: return LayerStone`，
// 于是水面会顶着石头纹理混进**不透明** terrain pass。上传路径按 material
// 分流水面 quad，没有独立材质层就没有分流依据。
func TestFluidBlocksUseDedicatedWaterMaterialLayer(t *testing.T) {
	registry := assets.NewRegistry()
	faces := []mesh.Face{
		mesh.FaceNegX, mesh.FacePosX, mesh.FaceNegY,
		mesh.FacePosY, mesh.FaceNegZ, mesh.FacePosZ,
	}
	for id := core.WaterSourceID; id <= core.WaterLevel7ID; id++ {
		for _, face := range faces {
			if got := registry.Material(id, face); got != assets.LayerWater {
				t.Fatalf("Material(%d, face=%d) = %d，想要 LayerWater(%d)",
					id, face, got, assets.LayerWater)
			}
		}
	}
	// 反向守卫：任何非流体的已注册方块都不得落到水层，否则「按 material
	// 分流」会把不透明几何一起拖进半透明 pass。
	for id := core.AirID; id <= core.MossyCobblestoneID; id++ {
		if core.IsFluid(id) {
			continue
		}
		for _, face := range faces {
			if registry.Material(id, face) == assets.LayerWater {
				t.Fatalf("非流体方块 %d 的 face=%d 落到了水材质层", id, face)
			}
		}
	}
}

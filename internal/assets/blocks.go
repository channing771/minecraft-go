package assets

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

const (
	LayerStone uint16 = iota
	LayerDirt
	LayerGrassTop
	LayerGrassSide
	LayerBedrock
	LayerStoneBrick
	LayerCoalOre
	LayerIronOre
	LayerFurnace
	LayerIronBlock
	LayerChest
	LayerLightBlock
	LayerLeaves
	LayerGlass
	LayerCobblestone
	LayerSmoothStone
	LayerSand
	LayerGravel
	LayerOakLogSide
	LayerOakLogTop
	LayerOakPlanks
	LayerBrick
	LayerWhiteWool
	LayerRoofTile
	LayerClay
	LayerSnowTop
	LayerSnowSide
	LayerMossyCobblestone
	layerCount
)

// Registry 是方块属性与材质的注册表。
type Registry struct {
	layers       [layerCount][]byte
	meshSnapshot mesh.RegistrySnapshot
}

// NewRegistry 构造注册表并生成全部占位材质。
func NewRegistry() *Registry {
	r := &Registry{}
	r.layers[LayerStone] = stoneTexture()
	r.layers[LayerDirt] = dirtTexture()
	r.layers[LayerGrassTop] = grassTopTexture()
	r.layers[LayerGrassSide] = grassSideTexture()
	r.layers[LayerBedrock] = noisyTexture(rgb{R: 60, G: 60, B: 64}, 28, 0x3F19)
	r.layers[LayerStoneBrick] = stoneBrickTexture()
	r.layers[LayerCoalOre] = oreTexture(rgb{R: 38, G: 40, B: 44})
	r.layers[LayerIronOre] = oreTexture(rgb{R: 194, G: 140, B: 104})
	r.layers[LayerFurnace] = furnaceTexture()
	r.layers[LayerIronBlock] = ironBlockTexture()
	r.layers[LayerChest] = chestTexture()
	r.layers[LayerLightBlock] = lightBlockTexture()
	r.layers[LayerLeaves] = leavesTexture()
	r.layers[LayerGlass] = glassTexture()
	r.layers[LayerCobblestone] = cobblestoneTexture()
	r.layers[LayerSmoothStone] = smoothStoneTexture()
	r.layers[LayerSand] = sandTexture()
	r.layers[LayerGravel] = gravelTexture()
	r.layers[LayerOakLogSide] = oakLogSideTexture()
	r.layers[LayerOakLogTop] = oakLogTopTexture()
	r.layers[LayerOakPlanks] = oakPlanksTexture()
	r.layers[LayerBrick] = brickTexture()
	r.layers[LayerWhiteWool] = whiteWoolTexture()
	r.layers[LayerRoofTile] = roofTileTexture()
	r.layers[LayerClay] = clayTexture()
	r.layers[LayerSnowTop] = snowTopTexture()
	r.layers[LayerSnowSide] = snowSideTexture()
	r.layers[LayerMossyCobblestone] = mossyCobblestoneTexture()
	ids := make([]world.BlockID, 0, int(core.MossyCobblestoneID)+1)
	for id := core.AirID; id <= core.MossyCobblestoneID; id++ {
		ids = append(ids, id)
	}
	snapshot, err := mesh.BuildRegistrySnapshot(ids, r)
	if err != nil {
		panic("assets: 构建 mesh registry snapshot: " + err.Error())
	}
	r.meshSnapshot = snapshot
	return r
}

// Opaque 返回方块是否完全不透明。实现 mesh.Registry。
// 流体（IsFluid）与玻璃、树叶一样是透明方块：本任务组只登记流体的方块属性，
// 不新增流体材质与渲染 pass，流体沿用既有的透明方块判定路径。
func (r *Registry) Opaque(id world.BlockID) bool {
	return core.RegisteredBlock(id) && id != core.AirID && id != core.GlassID &&
		id != core.LeavesID && !core.IsFluid(id)
}

// FaceVisible 返回当前方块朝向相邻方块的面是否可绘制。实现 mesh.Registry。
// 流体编号虽然已注册（core.RegisteredBlock），但本任务组没有把它们纳入
// NewRegistry 构建 mesh snapshot 时使用的 ids 范围（仍止于
// MossyCobblestoneID），因此原生 Rust 侧的 registry 条目表里没有流体条目。
// Rust 的 face_visible 只要 id 或 adjacent 任一方不在条目表里就直接判不可见
// （engine/crates/mornlea_engine/src/input.rs 的 RegistryView::face_visible），
// 这里对 id 和 adjacent 两侧都显式排除流体，与 Rust 的「缺条目即不出面」
// 保持一致，否则 native_parity_test.go 会因 Go/Rust 对流体邻格的判定分歧而
// 报告 quad 数不一致。
func (r *Registry) FaceVisible(id, adjacent world.BlockID) bool {
	if !core.RegisteredBlock(id) || id == core.AirID || core.IsFluid(id) ||
		!core.RegisteredBlock(adjacent) || core.IsFluid(adjacent) || r.Opaque(adjacent) {
		return false
	}
	if adjacent == core.AirID {
		return true
	}
	return r.Opaque(id)
}

// Material 返回方块某个面的材质层号。实现 mesh.Registry。
func (r *Registry) Material(id world.BlockID, f mesh.Face) uint16 {
	switch id {
	case core.StoneID:
		return LayerStone
	case core.DirtID:
		return LayerDirt
	case core.BedrockID:
		return LayerBedrock
	case core.StoneBrickID:
		return LayerStoneBrick
	case core.CoalOreID:
		return LayerCoalOre
	case core.IronOreID:
		return LayerIronOre
	case core.FurnaceID:
		return LayerFurnace
	case core.IronBlockID:
		return LayerIronBlock
	case core.ChestID:
		return LayerChest
	case core.LightBlockID:
		return LayerLightBlock
	case core.LeavesID:
		return LayerLeaves
	case core.GlassID:
		return LayerGlass
	case core.CobblestoneID:
		return LayerCobblestone
	case core.SmoothStoneID:
		return LayerSmoothStone
	case core.SandID:
		return LayerSand
	case core.GravelID:
		return LayerGravel
	case core.OakLogID:
		if f == mesh.FacePosY || f == mesh.FaceNegY {
			return LayerOakLogTop
		}
		return LayerOakLogSide
	case core.OakPlanksID:
		return LayerOakPlanks
	case core.BrickID:
		return LayerBrick
	case core.WhiteWoolID:
		return LayerWhiteWool
	case core.RoofTileID:
		return LayerRoofTile
	case core.ClayID:
		return LayerClay
	case core.SnowBlockID:
		if f == mesh.FacePosY {
			return LayerSnowTop
		}
		return LayerSnowSide
	case core.MossyCobblestoneID:
		return LayerMossyCobblestone
	case core.GrassID:
		switch f {
		case mesh.FacePosY:
			return LayerGrassTop
		case mesh.FaceNegY:
			return LayerDirt
		default:
			return LayerGrassSide
		}
	default:
		return LayerStone
	}
}

// Emission 返回方块固定发出的方块光等级。实现 mesh.Registry。
func (r *Registry) Emission(id world.BlockID) uint8 {
	if id == core.LightBlockID {
		return 15
	}
	return 0
}

// MeshSnapshot 返回构造时冻结的网格 registry 快照。
func (r *Registry) MeshSnapshot() mesh.RegistrySnapshot { return r.meshSnapshot }

func (r *Registry) LayerCount() int { return int(layerCount) }

func (r *Registry) LayerRGBA(layer int) []byte { return r.layers[layer] }

var _ mesh.Registry = (*Registry)(nil)

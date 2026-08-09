package assets

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
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
	layerCount
)

// Registry 是方块属性与材质的注册表。
type Registry struct {
	layers [layerCount][]byte
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
	return r
}

// Opaque 返回方块是否完全不透明。实现 mesh.Registry。
func (r *Registry) Opaque(id world.BlockID) bool {
	return core.RegisteredBlock(id) && id != core.AirID && id != core.GlassID && id != core.LeavesID
}

// FaceVisible 返回当前方块朝向相邻方块的面是否可绘制。实现 mesh.Registry。
func (r *Registry) FaceVisible(id, adjacent world.BlockID) bool {
	if !core.RegisteredBlock(id) || id == core.AirID || !core.RegisteredBlock(adjacent) || r.Opaque(adjacent) {
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

func (r *Registry) LayerCount() int { return int(layerCount) }

func (r *Registry) LayerRGBA(layer int) []byte { return r.layers[layer] }

var _ mesh.Registry = (*Registry)(nil)

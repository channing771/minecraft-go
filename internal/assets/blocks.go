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
	layerCount
)

// Registry 是方块属性与材质的注册表。
type Registry struct {
	layers [layerCount][]byte
}

// NewRegistry 构造注册表并生成全部占位材质。
func NewRegistry() *Registry {
	r := &Registry{}
	r.layers[LayerStone] = noisyTexture(rgb{R: 128, G: 128, B: 128}, 18, 0x2545)
	r.layers[LayerDirt] = noisyTexture(rgb{R: 134, G: 96, B: 67}, 12, 0x1B87)
	r.layers[LayerGrassTop] = grassTopTexture()
	r.layers[LayerGrassSide] = grassSideTexture()
	r.layers[LayerBedrock] = noisyTexture(rgb{R: 60, G: 60, B: 64}, 28, 0x3F19)
	r.layers[LayerStoneBrick] = noisyTexture(rgb{R: 122, G: 118, B: 112}, 10, 0x77B1)
	return r
}

// Opaque 返回方块是否完全不透明。实现 mesh.Registry。
func (r *Registry) Opaque(id world.BlockID) bool {
	return id != world.AirID
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

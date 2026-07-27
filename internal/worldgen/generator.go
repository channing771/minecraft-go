package worldgen

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// M1 用到的方块 ID。完整的方块注册表在 M4 建立（spec §6.3）。
const (
	IDStone   world.BlockID = 2
	IDDirt    world.BlockID = 3
	IDGrass   world.BlockID = 4
	IDBedrock world.BlockID = 5
)

// 地形参数。M1 只做高度图地形。
const (
	seaLevel     = 64
	terrainAmp   = 48.0
	terrainScale = 1.0 / 256.0
	octaves      = 5
	lacunarity   = 2.0
	gain         = 0.5
	soilDepth    = 4
)

// Generator 按种子生成地形。无内部可变状态，可并发调用。
type Generator struct {
	noise *perlin
}

// New 创建一个地形生成器。
func New(seed int64) *Generator {
	return &Generator{noise: newPerlin(seed)}
}

// HeightAt 返回世界坐标 (wx,wz) 处最高实心方块的 Y。
func (g *Generator) HeightAt(wx, wz int32) int32 {
	n := g.noise.fbm(float64(wx)*terrainScale, float64(wz)*terrainScale,
		octaves, lacunarity, gain)
	return int32(seaLevel + n*terrainAmp)
}

// GenerateChunk 生成一个完整区块。
func (g *Generator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	c := world.NewChunk(pos)
	baseX := pos.X << core.SectionShift
	baseZ := pos.Z << core.SectionShift

	for lz := 0; lz < core.SectionSize; lz++ {
		for lx := 0; lx < core.SectionSize; lx++ {
			h := g.HeightAt(baseX+int32(lx), baseZ+int32(lz))
			if h >= core.MaxY {
				h = core.MaxY - 1
			}
			for y := int32(core.MinY); y <= h; y++ {
				var id world.BlockID
				switch {
				case y == core.MinY:
					id = IDBedrock
				case y == h:
					id = IDGrass
				case y > h-soilDepth:
					id = IDDirt
				default:
					id = IDStone
				}
				c.SetBlock(lx, y, lz, id)
			}
		}
	}
	c.Compact()
	return c
}

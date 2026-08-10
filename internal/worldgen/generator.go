package worldgen

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// M1 用到的方块 ID。完整的方块注册表在 M4 建立（spec §6.3）。
const (
	IDStone   = core.StoneID
	IDDirt    = core.DirtID
	IDGrass   = core.GrassID
	IDBedrock = core.BedrockID
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

const (
	snowLine             int32 = 88
	sandLine             int32 = 62
	clayNoiseScale             = 1.0 / 96.0
	clayNoiseOffsetX     int32 = 417
	clayNoiseOffsetZ     int32 = -193
	clayNoiseThreshold         = 0.18
	gravelNoiseScale           = 1.0 / 72.0
	gravelNoiseOffsetX   int32 = -271
	gravelNoiseOffsetZ   int32 = 613
	gravelNoiseThreshold       = 0.22
	gravelMaxDepth       int32 = 10
)

// Generator 按种子生成地形。无内部可变状态，可并发调用。
type Generator struct {
	noise *perlin
	seed  int64
}

// New 创建一个地形生成器。
func New(seed int64) *Generator {
	return &Generator{noise: newPerlin(seed), seed: seed}
}

// HeightAt 返回世界坐标 (wx,wz) 处最高实心方块的 Y。
func (g *Generator) HeightAt(wx, wz int32) int32 {
	n := g.noise.fbm(float64(wx)*terrainScale, float64(wz)*terrainScale,
		octaves, lacunarity, gain)
	return int32(seaLevel + n*terrainAmp)
}

// BaseBlockAt 返回不应用会话修改时指定世界位置的确定性方块。
func (g *Generator) BaseBlockAt(pos core.BlockPos) core.BlockID {
	if pos.Y < core.MinY || pos.Y >= core.MaxY {
		return core.AirID
	}
	height := g.HeightAt(pos.X, pos.Z)
	if height >= core.MaxY {
		height = core.MaxY - 1
	}
	return g.generatedBlockAt(pos, height)
}

// generatedBlockAt 是单点查询与整区块生成共用的纯判断，
// 矿石只替换本应为石头的方块，铁矿判断优先于煤矿。
func (g *Generator) generatedBlockAt(pos core.BlockPos, height int32) core.BlockID {
	base := g.naturalBlockAt(pos, height)
	if base != IDStone {
		return base
	}
	if pos.Y < ironMaxY && oreHash(g.seed, pos, ironSalt)%ironOdds == 0 {
		return core.IronOreID
	}
	if pos.Y < coalMaxY && oreHash(g.seed, pos, coalSalt)%coalOdds == 0 {
		return core.CoalOreID
	}
	return base
}

func (g *Generator) naturalBlockAt(pos core.BlockPos, height int32) core.BlockID {
	base := terrainBlockAt(pos.Y, height)
	if base == core.AirID || base == IDBedrock {
		return base
	}

	depth := height - pos.Y
	if depth == 0 && height >= snowLine {
		return core.SnowBlockID
	}
	if height <= sandLine && depth >= 0 && depth < soilDepth {
		if depth >= 2 && g.noise.at(
			float64(pos.X+clayNoiseOffsetX)*clayNoiseScale,
			float64(pos.Z+clayNoiseOffsetZ)*clayNoiseScale,
		) > clayNoiseThreshold {
			return core.ClayID
		}
		return core.SandID
	}
	if base == IDStone && depth <= gravelMaxDepth && g.noise.at(
		float64(pos.X+gravelNoiseOffsetX)*gravelNoiseScale,
		float64(pos.Z+gravelNoiseOffsetZ)*gravelNoiseScale,
	) > gravelNoiseThreshold {
		return core.GravelID
	}
	return base
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
				c.SetBlock(lx, y, lz, g.generatedBlockAt(core.BlockPos{
					X: baseX + int32(lx), Y: y, Z: baseZ + int32(lz),
				}, h))
			}
		}
	}
	c.Compact()
	return c
}

func terrainBlockAt(y, height int32) core.BlockID {
	switch {
	case y < core.MinY || y >= core.MaxY || y > height:
		return core.AirID
	case y == core.MinY:
		return IDBedrock
	case y == height:
		return IDGrass
	case y > height-soilDepth:
		return IDDirt
	default:
		return IDStone
	}
}

package worldgen_test

// 本文件是 worldgen 的 Go oracle:旧 Go 生产实现的独立副本,只存在于测试。
// 生产路径迁入 Rust engine 后,本 oracle 是"同种子逐位一致"差分门禁的对照物;
// 它刻意不依赖任何生产内部标识符,只通过公共 API 与生产结果比较。
//
// oracle 采用纯 pointwise 形式(逐坐标求方块),不复刻区块写入过程:
// 旧实现中 applyOakTrees 的"原木优先、树叶仅覆盖空气"合并结果与 pointwise
// 的 BaseBlockAt 判定收敛一致,TestOracleMatchesProduction 会对整块逐位验证。

import (
	"math"
	"math/rand"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/worldgen"
)

// 地形与噪声常量,与旧生产实现保持逐字一致。
const (
	oracleSeaLevel     = 64
	oracleTerrainAmp   = 48.0
	oracleTerrainScale = 1.0 / 256.0
	oracleOctaves      = 5
	oracleLacunarity   = 2.0
	oracleGain         = 0.5
	oracleSoilDepth    = 4

	oracleSnowLine             int32 = 88
	oracleSandLine             int32 = 62
	oracleClayNoiseScale             = 1.0 / 96.0
	oracleClayNoiseOffsetX     int32 = 417
	oracleClayNoiseOffsetZ     int32 = -193
	oracleClayNoiseThreshold         = 0.18
	oracleGravelNoiseScale           = 1.0 / 72.0
	oracleGravelNoiseOffsetX   int32 = -271
	oracleGravelNoiseOffsetZ   int32 = 613
	oracleGravelNoiseThreshold       = 0.22
	oracleGravelMaxDepth       int32 = 10

	oracleCoalMaxY int32  = 96
	oracleIronMaxY int32  = 48
	oracleCoalOdds        = 2048
	oracleIronOdds        = 4096
	oracleCoalSalt uint64 = 0x9E3779B97F4A7C15
	oracleIronSalt uint64 = 0xC2B2AE3D27D4EB4F

	oracleOakTreeCellShift        = 3
	oracleOakTreeSalt      uint64 = 0xA24BAED4963EE407
)

// oraclePerlin 是经典 2D Perlin 噪声的 oracle 副本。
type oraclePerlin struct {
	perm [512]int
}

// newOraclePerlin 用与生产相同的 Go math/rand 语义播种 perm 表。
func newOraclePerlin(seed int64) *oraclePerlin {
	var p oraclePerlin
	base := make([]int, 256)
	for i := range base {
		base[i] = i
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(256, func(i, j int) { base[i], base[j] = base[j], base[i] })
	for i := 0; i < 512; i++ {
		p.perm[i] = base[i&255]
	}
	return &p
}

func oracleFade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func oracleLerp(a, b, t float64) float64 { return a + t*(b-a) }

func oracleGrad2(h int, x, y float64) float64 {
	switch h & 3 {
	case 0:
		return x + y
	case 1:
		return -x + y
	case 2:
		return x - y
	default:
		return -x - y
	}
}

func (p *oraclePerlin) at(x, z float64) float64 {
	fx, fz := math.Floor(x), math.Floor(z)
	xi, zi := int(fx)&255, int(fz)&255
	xf, zf := x-fx, z-fz
	u, v := oracleFade(xf), oracleFade(zf)

	aa := p.perm[p.perm[xi]+zi]
	ab := p.perm[p.perm[xi]+zi+1]
	ba := p.perm[p.perm[xi+1]+zi]
	bb := p.perm[p.perm[xi+1]+zi+1]

	x1 := oracleLerp(oracleGrad2(aa, xf, zf), oracleGrad2(ba, xf-1, zf), u)
	x2 := oracleLerp(oracleGrad2(ab, xf, zf-1), oracleGrad2(bb, xf-1, zf-1), u)
	return oracleLerp(x1, x2, v)
}

func (p *oraclePerlin) fbm(x, z float64, octaves int, lacunarity, gain float64) float64 {
	var sum, norm float64
	amp := 1.0
	freq := 1.0
	for i := 0; i < octaves; i++ {
		sum += p.at(x*freq, z*freq) * amp
		norm += amp
		freq *= lacunarity
		amp *= gain
	}
	return sum / norm
}

// oracleGenerator 是旧 Go 生产实现的 pointwise oracle。
type oracleGenerator struct {
	noise *oraclePerlin
	seed  int64
}

func newOracleGenerator(seed int64) *oracleGenerator {
	return &oracleGenerator{noise: newOraclePerlin(seed), seed: seed}
}

func (g *oracleGenerator) heightAt(wx, wz int32) int32 {
	n := g.noise.fbm(float64(wx)*oracleTerrainScale, float64(wz)*oracleTerrainScale,
		oracleOctaves, oracleLacunarity, oracleGain)
	return int32(oracleSeaLevel + n*oracleTerrainAmp)
}

func oracleHash(seed int64, pos core.BlockPos, salt uint64) uint64 {
	hash := uint64(seed) ^ salt
	for _, value := range [3]int64{int64(pos.X), int64(pos.Y), int64(pos.Z)} {
		hash ^= uint64(value) + 0x9E3779B97F4A7C15 + hash<<6 + hash>>2
		hash *= 0xFF51AFD7ED558CCD
		hash ^= hash >> 33
	}
	hash *= 0xC4CEB9FE1A85EC53
	hash ^= hash >> 33
	return hash
}

func oracleTerrainLayer(y, height int32) core.BlockID {
	switch {
	case y < core.MinY || y >= core.MaxY || y > height:
		return core.AirID
	case y == core.MinY:
		return core.BedrockID
	case y == height:
		return core.GrassID
	case y > height-oracleSoilDepth:
		return core.DirtID
	default:
		return core.StoneID
	}
}

func (g *oracleGenerator) naturalBlockAt(pos core.BlockPos, height int32) core.BlockID {
	base := oracleTerrainLayer(pos.Y, height)
	if base == core.AirID || base == core.BedrockID {
		return base
	}

	depth := height - pos.Y
	if depth == 0 && height >= oracleSnowLine {
		return core.SnowBlockID
	}
	if height <= oracleSandLine && depth >= 0 && depth < oracleSoilDepth {
		if depth >= 2 && g.noise.at(
			float64(pos.X+oracleClayNoiseOffsetX)*oracleClayNoiseScale,
			float64(pos.Z+oracleClayNoiseOffsetZ)*oracleClayNoiseScale,
		) > oracleClayNoiseThreshold {
			return core.ClayID
		}
		return core.SandID
	}
	if base == core.StoneID && depth <= oracleGravelMaxDepth && g.noise.at(
		float64(pos.X+oracleGravelNoiseOffsetX)*oracleGravelNoiseScale,
		float64(pos.Z+oracleGravelNoiseOffsetZ)*oracleGravelNoiseScale,
	) > oracleGravelNoiseThreshold {
		return core.GravelID
	}
	return base
}

func (g *oracleGenerator) generatedBlockAt(pos core.BlockPos, height int32) core.BlockID {
	base := g.naturalBlockAt(pos, height)
	if base != core.StoneID {
		return base
	}
	if pos.Y < oracleIronMaxY && oracleHash(g.seed, pos, oracleIronSalt)%oracleIronOdds == 0 {
		return core.IronOreID
	}
	if pos.Y < oracleCoalMaxY && oracleHash(g.seed, pos, oracleCoalSalt)%oracleCoalOdds == 0 {
		return core.CoalOreID
	}
	return base
}

// terrainBlockAt 是 TerrainProbe 语义的 oracle:含高度上限截断与 Y 界判定。
func (g *oracleGenerator) terrainBlockAt(pos core.BlockPos) core.BlockID {
	if pos.Y < core.MinY || pos.Y >= core.MaxY {
		return core.AirID
	}
	height := g.heightAt(pos.X, pos.Z)
	if height >= core.MaxY {
		height = core.MaxY - 1
	}
	return g.generatedBlockAt(pos, height)
}

type oracleOakTree struct {
	root   core.BlockPos
	height int32
}

func (g *oracleGenerator) oakTreeForCell(cellX, cellZ int32) (oracleOakTree, bool) {
	hash := oracleHash(g.seed, core.BlockPos{X: cellX, Z: cellZ}, oracleOakTreeSalt)
	if hash&1 != 0 {
		return oracleOakTree{}, false
	}
	x := (cellX << oracleOakTreeCellShift) + int32((hash>>1)&7)
	z := (cellZ << oracleOakTreeCellShift) + int32((hash>>4)&7)
	height := int32(4 + (hash>>7)%3)
	surface := g.heightAt(x, z)
	root := core.BlockPos{X: x, Y: surface + 1, Z: z}
	if g.generatedBlockAt(core.BlockPos{X: x, Y: surface, Z: z}, surface) != core.GrassID ||
		root.Y+height >= core.MaxY {
		return oracleOakTree{}, false
	}
	for y := root.Y; y < root.Y+height; y++ {
		if g.generatedBlockAt(core.BlockPos{X: x, Y: y, Z: z}, surface) != core.AirID {
			return oracleOakTree{}, false
		}
	}
	return oracleOakTree{root: root, height: height}, true
}

func oracleOakTreeBlockAt(tree oracleOakTree, pos core.BlockPos) core.BlockID {
	if tree.root.Y < core.MinY || tree.root.Y+tree.height >= core.MaxY {
		return core.AirID
	}
	topY := tree.root.Y + tree.height - 1
	if pos.X == tree.root.X && pos.Z == tree.root.Z && pos.Y >= tree.root.Y && pos.Y <= topY {
		return core.OakLogID
	}
	dx := pos.X - tree.root.X
	dz := pos.Z - tree.root.Z
	switch pos.Y - topY {
	case -2, -1:
		if oracleAbs(dx) <= 2 && oracleAbs(dz) <= 2 && !(oracleAbs(dx) == 2 && oracleAbs(dz) == 2) {
			return core.LeavesID
		}
	case 0:
		if oracleAbs(dx) <= 1 && oracleAbs(dz) <= 1 {
			return core.LeavesID
		}
	case 1:
		if oracleAbs(dx)+oracleAbs(dz) <= 1 {
			return core.LeavesID
		}
	}
	return core.AirID
}

func (g *oracleGenerator) treeBlockAt(pos core.BlockPos) core.BlockID {
	leaf := false
	for cellZ := (pos.Z - 2) >> oracleOakTreeCellShift; cellZ <= (pos.Z+2)>>oracleOakTreeCellShift; cellZ++ {
		for cellX := (pos.X - 2) >> oracleOakTreeCellShift; cellX <= (pos.X+2)>>oracleOakTreeCellShift; cellX++ {
			tree, ok := g.oakTreeForCell(cellX, cellZ)
			if !ok {
				continue
			}
			switch oracleOakTreeBlockAt(tree, pos) {
			case core.OakLogID:
				return core.OakLogID
			case core.LeavesID:
				leaf = true
			}
		}
	}
	if leaf {
		return core.LeavesID
	}
	return core.AirID
}

// baseBlockAt 是 BaseBlockAt 语义的 oracle:地形优先,空气处叠加橡树。
func (g *oracleGenerator) baseBlockAt(pos core.BlockPos) core.BlockID {
	base := g.terrainBlockAt(pos)
	if base != core.AirID {
		return base
	}
	return g.treeBlockAt(pos)
}

func oracleAbs(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}

// oracleDiffSeeds/oracleDiffChunks 是差分语料:覆盖正负种子与正负区块坐标。
var oracleDiffSeeds = []int64{42, 0, -1, 1234, 987654321}

var oracleDiffChunks = []core.ChunkPos{
	{X: 0, Z: 0}, {X: 1, Z: 0}, {X: -1, Z: -1}, {X: 37, Z: -104}, {X: -8, Z: 5},
}

// TestOracleMatchesProduction 逐位比较生产 GenerateChunk 与 pointwise oracle。
func TestOracleMatchesProduction(t *testing.T) {
	for _, seed := range oracleDiffSeeds {
		production := worldgen.New(seed)
		oracle := newOracleGenerator(seed)
		for _, pos := range oracleDiffChunks {
			chunk := production.GenerateChunk(pos)
			baseX := pos.X << core.SectionShift
			baseZ := pos.Z << core.SectionShift
			for y := int32(core.MinY); y < core.MaxY; y++ {
				for z := 0; z < core.SectionSize; z++ {
					for x := 0; x < core.SectionSize; x++ {
						world := core.BlockPos{X: baseX + int32(x), Y: y, Z: baseZ + int32(z)}
						got := chunk.BlockAt(x, y, z)
						want := oracle.baseBlockAt(world)
						if got != want {
							t.Fatalf("seed=%d chunk=%+v (%d,%d,%d): 生产=%d oracle=%d",
								seed, pos, world.X, y, world.Z, got, want)
						}
					}
				}
			}
		}
	}
}

// TestOraclePointQueriesMatchProduction 差分单点入口:HeightAt/TerrainBlockAt/BaseBlockAt。
func TestOraclePointQueriesMatchProduction(t *testing.T) {
	for _, seed := range oracleDiffSeeds {
		production := worldgen.New(seed)
		oracle := newOracleGenerator(seed)
		for wx := int32(-24); wx <= 24; wx += 3 {
			for wz := int32(-24); wz <= 24; wz += 3 {
				if got, want := production.HeightAt(wx, wz), oracle.heightAt(wx, wz); got != want {
					t.Fatalf("seed=%d HeightAt(%d,%d): 生产=%d oracle=%d", seed, wx, wz, got, want)
				}
				for _, y := range []int32{core.MinY - 1, core.MinY, -20, 0, 63, 64, 90, core.MaxY - 1, core.MaxY} {
					pos := core.BlockPos{X: wx, Y: y, Z: wz}
					if got, want := production.TerrainBlockAt(pos), oracle.terrainBlockAt(pos); got != want {
						t.Fatalf("seed=%d TerrainBlockAt(%+v): 生产=%d oracle=%d", seed, pos, got, want)
					}
					if got, want := production.BaseBlockAt(pos), oracle.baseBlockAt(pos); got != want {
						t.Fatalf("seed=%d BaseBlockAt(%+v): 生产=%d oracle=%d", seed, pos, got, want)
					}
				}
			}
		}
	}
}

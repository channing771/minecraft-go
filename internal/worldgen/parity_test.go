package worldgen_test

// 本文件是 rust-engine-worldgen 的差分门禁:随机种子×区块的 dense 逐位
// 对比、Go fuzz 驱动的单点差分,以及跨区块橡树拼合一致性。对照物是
// oracle_test.go 中旧 Go 实现的逐字副本。

import (
	"math/rand"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/worldgen"
)

// assertChunkMatchesOracle 逐位比较生产 GenerateChunk 与 pointwise oracle。
//
// fluidEnabled 同时传给生产与 oracle:跨实现一致性必须在门控两态下都成立。
func assertChunkMatchesOracle(t *testing.T, seed int64, pos core.ChunkPos, fluidEnabled bool) {
	t.Helper()
	production := worldgen.New(seed, fluidEnabled)
	oracle := newOracleGenerator(seed, fluidEnabled)
	chunk := production.GenerateChunk(pos)
	baseX := pos.X << core.SectionShift
	baseZ := pos.Z << core.SectionShift
	for y := int32(core.MinY); y < core.MaxY; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				world := core.BlockPos{X: baseX + int32(x), Y: y, Z: baseZ + int32(z)}
				if got, want := chunk.BlockAt(x, y, z), oracle.baseBlockAt(world); got != want {
					t.Fatalf("seed=%d fluid=%t chunk=%+v (%d,%d,%d): 生产=%d oracle=%d",
						seed, fluidEnabled, pos, world.X, y, world.Z, got, want)
				}
			}
		}
	}
}

// TestRandomSeedChunkParity 用固定种子的伪随机语料扩大差分覆盖:
// 语料本身确定(测试可复现),覆盖大坐标与负种子。
func TestRandomSeedChunkParity(t *testing.T) {
	rng := rand.New(rand.NewSource(20260815))
	for i := 0; i < 6; i++ {
		seed := rng.Int63() - rng.Int63()
		pos := core.ChunkPos{
			X: int32(rng.Intn(4096) - 2048),
			Z: int32(rng.Intn(4096) - 2048),
		}
		for _, fluidEnabled := range []bool{false, true} {
			assertChunkMatchesOracle(t, seed, pos, fluidEnabled)
		}
	}
}

// FuzzWorldgenOracleParity 对任意 (seed, 坐标) 做单点差分:
// HeightAt/TerrainBlockAt/BaseBlockAt 必须与 oracle 逐位一致。
func FuzzWorldgenOracleParity(f *testing.F) {
	f.Add(int64(42), int32(0), int32(64), int32(0))
	f.Add(int64(-1), int32(-1000), int32(-64), int32(1000))
	f.Add(int64(987654321), int32(2147480000), int32(319), int32(-2147480000))
	f.Add(int64(0), int32(16), int32(88), int32(-16))
	f.Fuzz(func(t *testing.T, seed int64, wx, wy, wz int32) {
		// 门控两态都要跨实现一致:关闭态锁基线,开启态锁注水规则。
		for _, fluidEnabled := range []bool{false, true} {
			production := worldgen.New(seed, fluidEnabled)
			oracle := newOracleGenerator(seed, fluidEnabled)
			if got, want := production.HeightAt(wx, wz), oracle.heightAt(wx, wz); got != want {
				t.Fatalf("fluid=%t HeightAt(%d,%d)=%d，oracle=%d", fluidEnabled, wx, wz, got, want)
			}
			pos := core.BlockPos{X: wx, Y: wy, Z: wz}
			if got, want := production.TerrainBlockAt(pos), oracle.terrainBlockAt(pos); got != want {
				t.Fatalf("fluid=%t TerrainBlockAt(%+v)=%d，oracle=%d", fluidEnabled, pos, got, want)
			}
			if got, want := production.BaseBlockAt(pos), oracle.baseBlockAt(pos); got != want {
				t.Fatalf("fluid=%t BaseBlockAt(%+v)=%d，oracle=%d", fluidEnabled, pos, got, want)
			}
		}
	})
}

// TestOakTreeSpansChunkBorderConsistently 锁定跨区块橡树拼合:
// seed 42 cell (2,2) 的橡树 root=(16,*,18)、height=6,树冠 x∈[14,18]
// 横跨 chunk (0,1) 与 (1,1)。两个区块独立生成后,树冠包围盒内每一格都
// 必须与 oracle 一致,且两侧都必须真实落下树块。
func TestOakTreeSpansChunkBorderConsistently(t *testing.T) {
	const seed = 42
	oracle := newOracleGenerator(seed, false)
	tree, ok := oracle.oakTreeForCell(2, 2)
	if !ok {
		t.Fatal("seed 42 cell (2,2) 应有橡树")
	}
	if tree.root.X != 16 || tree.root.Z != 18 {
		t.Fatalf("候选树 root=%+v,语料前提失效", tree.root)
	}

	production := worldgen.New(seed, false)
	left := production.GenerateChunk(core.ChunkPos{X: 0, Z: 1})
	right := production.GenerateChunk(core.ChunkPos{X: 1, Z: 1})
	chunks := map[core.ChunkPos]interface {
		BlockAt(x int, y int32, z int) core.BlockID
	}{
		{X: 0, Z: 1}: left,
		{X: 1, Z: 1}: right,
	}

	treeBlocksPerChunk := map[core.ChunkPos]int{}
	for y := tree.root.Y; y <= tree.root.Y+tree.height; y++ {
		for z := tree.root.Z - 2; z <= tree.root.Z+2; z++ {
			for x := tree.root.X - 2; x <= tree.root.X+2; x++ {
				pos := core.BlockPos{X: x, Y: y, Z: z}
				chunk, covered := chunks[pos.Chunk()]
				if !covered {
					continue
				}
				lx, _, lz := pos.Local()
				got := chunk.BlockAt(lx, y, lz)
				if want := oracle.baseBlockAt(pos); got != want {
					t.Fatalf("跨界树 %+v: 生产=%d oracle=%d", pos, got, want)
				}
				if got == core.OakLogID || got == core.LeavesID {
					treeBlocksPerChunk[pos.Chunk()]++
				}
			}
		}
	}
	for _, chunkPos := range []core.ChunkPos{{X: 0, Z: 1}, {X: 1, Z: 1}} {
		if treeBlocksPerChunk[chunkPos] == 0 {
			t.Fatalf("chunk %+v 内没有该树的方块,拼合语料失效", chunkPos)
		}
	}
}

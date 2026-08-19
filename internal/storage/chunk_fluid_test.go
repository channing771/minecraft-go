package storage

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// fluidBlockIDs 是全部 8 个流体编号，覆盖源方块与 7 个流动等级。
var fluidBlockIDs = []core.BlockID{
	core.WaterSourceID,
	core.WaterLevel1ID, core.WaterLevel2ID, core.WaterLevel3ID, core.WaterLevel4ID,
	core.WaterLevel5ID, core.WaterLevel6ID, core.WaterLevel7ID,
}

// fluidFixtureChunk 在既有箱子/熔炉/掉落物夹具之上叠加全部 8 个流体编号，
// 使 v9 golden 与迁移测试都覆盖含流体的区块。
func fluidFixtureChunk(t *testing.T, pos core.ChunkPos) *world.Chunk {
	t.Helper()
	chunk := chestFixtureChunk(t, pos)
	for index, id := range fluidBlockIDs {
		// 固定落在第 5 个 section 的一条 X 线上，避开夹具已占用的坐标。
		chunk.SetBlock(index, int32(core.MinY+5*core.SectionSize), 3, id)
	}
	return chunk
}

// countFluidCells 统计整块中的流体格数，用于「含流体」与「不含流体」两个方向的断言。
func countFluidCells(chunk *world.Chunk) int {
	total := 0
	forEachChunkCell(func(x int, y int32, z int) {
		if core.IsFluid(chunk.BlockAt(x, y, z)) {
			total++
		}
	})
	return total
}

// forEachChunkCell 遍历一个区块的全部格坐标。
func forEachChunkCell(visit func(x int, y int32, z int)) {
	for y := int32(core.MinY); y < core.MaxY; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				visit(x, y, z)
			}
		}
	}
}

// TestChunkPayloadRoundTripsFluidCellByCell 覆盖「流体跨保存与加载保真」场景：
// 含全部 8 个流体编号的区块经一次编解码后逐格一致。
func TestChunkPayloadRoundTripsFluidCellByCell(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := fluidFixtureChunk(t, key.Pos)

	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: want})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != currentChunkSchema || got.Migrated {
		t.Fatalf("当前 schema 往返 schema=%d migrated=%v，想要 %d/false", got.Schema, got.Migrated, currentChunkSchema)
	}

	mismatches := 0
	forEachChunkCell(func(x int, y int32, z int) {
		if got.Chunk.BlockAt(x, y, z) != want.BlockAt(x, y, z) {
			if mismatches < 4 {
				t.Errorf("流体往返格 (%d,%d,%d) = %d，想要 %d",
					x, y, z, got.Chunk.BlockAt(x, y, z), want.BlockAt(x, y, z))
			}
			mismatches++
		}
	})
	if mismatches != 0 {
		t.Fatalf("流体往返共有 %d 格不一致", mismatches)
	}

	// 夹具前提守卫排在真实断言之后：真实故障必须先报出自己的诊断。
	if fluids := countFluidCells(got.Chunk); fluids != len(fluidBlockIDs) {
		t.Fatalf("往返后流体格数 = %d，想要 %d（夹具失效）", fluids, len(fluidBlockIDs))
	}
}

// TestChunkV8FixtureMigratesToV9AsIdentity 覆盖「旧区块按恒等迁移」场景：
// 冻结的 v8 字节加载成功、逐格与写入时一致、不含任何流体，且被标记为需要改写。
func TestChunkV8FixtureMigratesToV9AsIdentity(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := chestFixtureChunk(t, key.Pos)
	encoded := readChunkFixture(t, "chunk-v8.bin")

	got, err := decodeChunkPayload(key, 19, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != currentChunkSchema || !got.Migrated || got.Revision != 19 || got.Key != key {
		t.Fatalf("v8 迁移 schema=%d migrated=%v revision=%d key=%+v，想要 %d/true/19/%+v",
			got.Schema, got.Migrated, got.Revision, got.Key, currentChunkSchema, key)
	}

	mismatches := 0
	forEachChunkCell(func(x int, y int32, z int) {
		if got.Chunk.BlockAt(x, y, z) != want.BlockAt(x, y, z) {
			if mismatches < 4 {
				t.Errorf("v8 恒等迁移格 (%d,%d,%d) = %d，想要 %d",
					x, y, z, got.Chunk.BlockAt(x, y, z), want.BlockAt(x, y, z))
			}
			mismatches++
		}
	})
	if mismatches != 0 {
		t.Errorf("v8 恒等迁移共有 %d 格被改写", mismatches)
	}
	if fluids := countFluidCells(got.Chunk); fluids != 0 {
		t.Fatalf("v8 恒等迁移注入了 %d 格流体，想要 0", fluids)
	}
	if got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v8 恒等迁移改变了掉落物状态")
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("v8 恒等迁移改变熔炉槽 %d", slot)
		}
	}
	for slot := range core.ChestsPerChunk {
		if got.Chunk.Chest(slot) != want.Chest(slot) {
			t.Fatalf("v8 恒等迁移改变箱子槽 %d", slot)
		}
	}

	// 夹具前提守卫排在真实断言之后：v8 夹具必须真的含有可被改写的非空气方块，
	// 否则上面的逐格比较会因两侧同为全空气而空洞地通过。
	nonAir := 0
	forEachChunkCell(func(x int, y int32, z int) {
		if want.BlockAt(x, y, z) != core.AirID {
			nonAir++
		}
	})
	if nonAir < 256 {
		t.Fatalf("v8 夹具非空气格数 = %d，想要至少 256（夹具失效）", nonAir)
	}
}

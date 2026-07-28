package sim

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestSpawnCandidatesOrderByDistanceThenXZ(t *testing.T) {
	got := spawnCandidates(core.ChunkPos{})
	wantFirst := []spawnColumn{
		{X: 0, Z: 0},
		{X: -1, Z: 0},
		{X: 0, Z: -1},
		{X: 0, Z: 1},
		{X: 1, Z: 0},
		{X: -1, Z: -1},
		{X: -1, Z: 1},
		{X: 1, Z: -1},
		{X: 1, Z: 1},
	}
	wantLast := []spawnColumn{
		{X: -16, Z: -16},
		{X: -16, Z: 16},
		{X: 16, Z: -16},
		{X: 16, Z: 16},
	}
	if len(got) != 33*33 || !reflect.DeepEqual(got[:len(wantFirst)], wantFirst) ||
		!reflect.DeepEqual(got[len(got)-len(wantLast):], wantLast) {
		t.Fatalf("候选顺序或半径错误: len=%d first=%+v last=%+v", len(got), got[:len(wantFirst)], got[len(got)-len(wantLast):])
	}

	offset := spawnCandidates(core.ChunkPos{X: 2, Z: -3})
	if offset[0] != (spawnColumn{X: 32, Z: -48}) {
		t.Fatalf("anchor 偏移后的首候选=%+v", offset[0])
	}
}

func TestSpawnWaitsForEarlierUnknownCandidate(t *testing.T) {
	engine := NewEngine(spawnTestBase, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	engine.Step()

	laterChunk := world.NewChunk(core.ChunkPos{X: -1})
	laterChunk.SetBlock(15, 0, 0, core.GrassID)
	loadSpawnTestChunk(t, engine.dimensions[core.Overworld], laterChunk)
	if player := onlyInternalPlayer(t, engine.Step()); player.Ready {
		t.Fatalf("较早候选仍 unknown 时跳到了较晚 surface: %+v", player)
	}

	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     spawnTestChunk(core.ChunkPos{}, core.BlockPos{}),
	})
	player := onlyInternalPlayer(t, engine.Step())
	if !player.Ready || player.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("较早候选 Ready 后 spawn=%+v", player)
	}
}

func TestExhaustedSpawnRetriesOnlyAfterRevisionChange(t *testing.T) {
	engine := NewEngine(spawnTestBase, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	dimension := engine.dimensions[core.Overworld]
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			loadSpawnTestChunk(t, dimension, world.NewChunk(core.ChunkPos{X: x, Z: z}))
		}
	}

	if player := onlyInternalPlayer(t, engine.Step()); player.Ready {
		t.Fatalf("全空气候选不应 Ready: %+v", player)
	}
	record := dimension.records[core.ChunkPos{}]
	record.Chunk.SetBlock(0, 0, 0, core.GrassID)
	if player := onlyInternalPlayer(t, engine.Step()); player.Ready {
		t.Fatalf("revision 未变却重新扫描: %+v", player)
	}

	record.Revision++
	player := onlyInternalPlayer(t, engine.Step())
	if !player.Ready || player.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("revision 改变后未从首候选重试: %+v", player)
	}
}

func onlyInternalPlayer(t *testing.T, result TickResult) PlayerUpdate {
	t.Helper()
	if len(result.Players) != 1 {
		t.Fatalf("Players=%+v，想要恰好一个", result.Players)
	}
	return result.Players[0]
}

func loadSpawnTestChunk(t *testing.T, dimension *Dimension, chunk *world.Chunk) {
	t.Helper()
	if !dimension.BeginGeneration(chunk.Pos) {
		t.Fatalf("区块 %+v 未开始生成", chunk.Pos)
	}
	if err := dimension.ApplyGenerated(chunk.Pos, chunk); err != nil {
		t.Fatal(err)
	}
}

func spawnTestChunk(pos core.ChunkPos, support core.BlockPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	x, _, z := support.Local()
	chunk.SetBlock(x, support.Y, z, core.GrassID)
	return chunk
}

func spawnTestBase(core.BlockPos) core.BlockID { return core.AirID }

package sim

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestSpawnCandidatesOrderByDistanceThenXZ(t *testing.T) {
	got := spawnCandidates(core.ChunkPos{}, defaultSpawnRadius)
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

	offset := spawnCandidates(core.ChunkPos{X: 2, Z: -3}, defaultSpawnRadius)
	if offset[0] != (spawnColumn{X: 32, Z: -48}) {
		t.Fatalf("anchor 偏移后的首候选=%+v", offset[0])
	}
}

func TestSpawnWaitsForEarlierUnknownCandidate(t *testing.T) {
	engine := NewEngine(0, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	for _, key := range requested.Acquire {
		engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
	}
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

func TestPendingSpawnGenerateRetainActivateAndForget(t *testing.T) {
	engine := NewEngine(0, 0)
	const sessionID = SessionID(1)
	anchor := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	target := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1}}
	engine.RegisterSession(sessionID, core.Overworld, anchor.Pos)

	acquiredAnchor := engine.Step()
	if player := onlyInternalPlayer(t, acquiredAnchor); player.Ready {
		t.Fatalf("生成 anchor 前 player=%+v，想要 PendingSpawn", player)
	}
	if !reflect.DeepEqual(acquiredAnchor.Acquire, []core.ChunkKey{anchor}) {
		t.Fatalf("首次 Acquire=%+v，想要 [%+v]", acquiredAnchor.Acquire, anchor)
	}
	engine.SubmitAcquired(AcquiredChunk{Key: anchor, Missing: true})
	generatedAnchor := engine.Step()
	if !reflect.DeepEqual(generatedAnchor.Generate, []core.ChunkKey{anchor}) {
		t.Fatalf("首次 Generate=%+v，想要 [%+v]", generatedAnchor.Generate, anchor)
	}

	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       anchor.Pos,
		Chunk:     world.NewChunk(anchor.Pos),
	})
	retained := engine.Step()
	if player := onlyInternalPlayer(t, retained); player.Ready {
		t.Fatalf("空 anchor 后 player=%+v，想要继续 PendingSpawn", player)
	}
	if !reflect.DeepEqual(retained.Ready, []core.ChunkKey{anchor}) ||
		!reflect.DeepEqual(retained.Acquire, []core.ChunkKey{target}) {
		t.Fatalf("retain tick Ready=%+v Acquire=%+v", retained.Ready, retained.Acquire)
	}
	session := engine.sessions[sessionID]
	if _, ok := session.wanted[anchor]; !ok {
		t.Fatalf("PendingSpawn 未保留 anchor: wanted=%+v", session.wanted)
	}
	if _, ok := session.wanted[target]; !ok {
		t.Fatalf("PendingSpawn 未保留 target: wanted=%+v", session.wanted)
	}
	if _, ok := session.player.spawnWanted[anchor.Pos]; !ok {
		t.Fatalf("spawnWanted 未记录 anchor: %+v", session.player.spawnWanted)
	}
	if _, ok := session.player.spawnWanted[target.Pos]; !ok {
		t.Fatalf("spawnWanted 未记录 target: %+v", session.player.spawnWanted)
	}

	engine.SubmitAcquired(AcquiredChunk{Key: target, Missing: true})
	generatedTarget := engine.Step()
	if !reflect.DeepEqual(generatedTarget.Generate, []core.ChunkKey{target}) {
		t.Fatalf("target Generate=%+v", generatedTarget.Generate)
	}
	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       target.Pos,
		Chunk: spawnTestChunk(target.Pos, core.BlockPos{
			X: -1,
			Y: 0,
			Z: 0,
		}),
	})
	activated := engine.Step()
	player := onlyInternalPlayer(t, activated)
	if !player.Ready || !player.Reset ||
		player.State.Position != (mgl32.Vec3{-0.5, 1, 0.5}) {
		t.Fatalf("target Ready 后 player=%+v，想要 Active reset", player)
	}
	if !reflect.DeepEqual(activated.Ready, []core.ChunkKey{target}) ||
		!reflect.DeepEqual(activated.Forget[sessionID], []core.ChunkKey{anchor}) {
		t.Fatalf("activate tick Ready=%+v Forget=%+v", activated.Ready, activated.Forget)
	}
	if !reflect.DeepEqual(session.wanted, map[core.ChunkKey]struct{}{target: {}}) {
		t.Fatalf("Active subscription wanted=%+v，想要仅 target", session.wanted)
	}
	if !reflect.DeepEqual(engine.wanted, map[core.ChunkKey]struct{}{target: {}}) {
		t.Fatalf("Active union wanted=%+v，想要仅 target", engine.wanted)
	}
	record := engine.dimensions[core.Overworld].records[anchor.Pos]
	if record == nil || record.State != ChunkUnloading || record.Chunk == nil ||
		!record.UnloadRequested || !record.Dirty() {
		t.Fatalf("activate forget 后未保留待持久 anchor: %+v", record)
	}
}

func TestExhaustedSpawnRetriesOnlyAfterRevisionChange(t *testing.T) {
	engine := NewEngine(0, 0)
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

package sim

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

func TestCompanionsAreSeparateSortedIdleActors(t *testing.T) {
	engine := NewEngine(0, 0)
	loadCompanionFlatChunks(t, engine, core.ChunkPos{}, 1)
	ids := []companion.ID{companionTestID(3), companionTestID(1), companionTestID(2)}
	positions := map[companion.ID]mgl32.Vec3{}
	wantBodies := map[companion.ID]companion.Body{}
	for index, id := range ids {
		position := mgl32.Vec3{float32(index) + 0.5, 1, 0.5}
		inventory := core.Inventory{}
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: byte(index + 1)}
		positions[id] = position
		wantBodies[id] = companion.Body{
			ID: id, Dimension: core.Overworld, Position: position,
			Yaw: float32(index), Pitch: float32(index) / 10, Inventory: inventory,
		}
		engine.RegisterCompanion(CompanionRestore{
			ID: id,
			Body: &companion.Body{
				ID: id, Dimension: core.Overworld, Position: position,
				Yaw: float32(index), Pitch: float32(index) / 10, Inventory: inventory,
			},
			SpawnDimension: core.Overworld,
		})
	}

	first := engine.Step().Companions
	second := engine.Step().Companions
	if len(first) != len(ids) || len(second) != len(ids) {
		t.Fatalf("Companions=%d/%d，想要 %d/%d", len(first), len(second), len(ids), len(ids))
	}
	for index, id := range []companion.ID{companionTestID(1), companionTestID(2), companionTestID(3)} {
		if first[index].ID != id || second[index].ID != id ||
			first[index].State.Position != positions[id] || second[index].State.Position != positions[id] ||
			!first[index].Reset || second[index].Reset {
			t.Fatalf("伙伴 %d 状态不稳定: first=%+v second=%+v", index, first[index], second[index])
		}
	}
	assertCompanionPanic(t, func() {
		engine.RegisterCompanion(CompanionRestore{
			ID: companionTestID(1), SpawnDimension: core.Overworld,
		})
	})
	bodies := engine.CompanionBodies()
	if len(bodies) != len(ids) {
		t.Fatalf("CompanionBodies=%d，想要 %d", len(bodies), len(ids))
	}
	for index, id := range []companion.ID{companionTestID(1), companionTestID(2), companionTestID(3)} {
		if bodies[index] != wantBodies[id] {
			t.Fatalf("Body[%d]=%+v", index, bodies[index])
		}
	}
}

func TestCompanionRestoreWaitsForAllCollisionChunksBeforeFallback(t *testing.T) {
	engine := NewEngine(0, 0)
	loadCompanionFlatChunk(t, engine, core.ChunkPos{})
	loadCompanionFlatChunk(t, engine, core.ChunkPos{X: 2})
	position := mgl32.Vec3{15.9, 1, 0.5}
	id := companionTestID(1)
	engine.RegisterCompanion(CompanionRestore{
		ID: id,
		Body: &companion.Body{
			ID: id, Dimension: core.Overworld, Position: position,
		},
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{X: 2},
	})
	setRestoreBlock(t, engine, core.BlockPos{X: 15, Y: 1, Z: 0}, core.StoneID)

	waiting := engine.Step()
	if len(waiting.Companions) != 0 || engine.companions[id].nextRestore != 0 {
		t.Fatalf("碰撞邻区未就绪时越过 restore: updates=%+v nextRestore=%d",
			waiting.Companions, engine.companions[id].nextRestore)
	}
	loadCompanionFlatChunk(t, engine, core.ChunkPos{X: 1})
	restored := engine.Step().Companions
	if len(restored) != 1 || restored[0].State.Position != (mgl32.Vec3{32.5, 1, 0.5}) {
		t.Fatalf("碰撞 restore 未回退出生点: %+v", restored)
	}
}

func TestCompanionRestoreChunkInitiallyMissingThenRestores(t *testing.T) {
	engine := NewEngine(0, 0)
	id := companionTestID(1)
	position := mgl32.Vec3{0.5, 1, 0.5}
	key := core.ChunkKey{Dimension: core.Overworld}
	engine.RegisterCompanion(CompanionRestore{
		ID: id,
		Body: &companion.Body{
			ID: id, Dimension: core.Overworld, Position: position,
		},
		SpawnDimension: core.Overworld,
	})

	requested := engine.Step()
	if !reflect.DeepEqual(requested.Acquire, []core.ChunkKey{key}) || len(requested.Companions) != 0 {
		t.Fatalf("首次请求=%+v updates=%+v", requested.Acquire, requested.Companions)
	}
	engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
	missing := engine.Step()
	if !reflect.DeepEqual(missing.Generate, []core.ChunkKey{key}) || len(missing.Companions) != 0 {
		t.Fatalf("missing 处理=%+v updates=%+v", missing.Generate, missing.Companions)
	}
	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Chunk:     companionFlatChunk(core.ChunkPos{}),
	})
	restored := engine.Step().Companions
	if len(restored) != 1 || restored[0].ID != id || restored[0].State.Position != position {
		t.Fatalf("区块就绪后未恢复原位置: %+v", restored)
	}
}

func TestCompanionSpawnRetriesAfterFailedChunkRevisionChanges(t *testing.T) {
	engine := NewEngine(0, 0)
	loadCompanionAirChunks(t, engine, core.ChunkPos{}, 1)
	id := companionTestID(1)
	engine.RegisterCompanion(CompanionRestore{
		ID: id, SpawnDimension: core.Overworld, SpawnAnchor: core.ChunkPos{},
	})

	if updates := engine.Step().Companions; len(updates) != 0 || !engine.companions[id].exhausted {
		t.Fatalf("全空气出生扫描=%+v exhausted=%v", updates, engine.companions[id].exhausted)
	}
	record := engine.dimensions[core.Overworld].records[core.ChunkPos{}]
	record.Chunk.SetBlock(0, 0, 0, core.GrassID)
	if updates := engine.Step().Companions; len(updates) != 0 {
		t.Fatalf("revision 未变却重试出生: %+v", updates)
	}
	record.Revision++
	updates := engine.Step().Companions
	if len(updates) != 1 || updates[0].State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("revision 改变后未重试: %+v", updates)
	}
}

func TestCompanionSpawnSearchRetainsOnlyThreeByThreeCandidateChunks(t *testing.T) {
	engine := NewEngine(0, 0)
	anchor := core.ChunkPos{X: 4, Z: -3}
	loadCompanionAirChunks(t, engine, anchor, 1)
	id := companionTestID(1)
	engine.RegisterCompanion(CompanionRestore{
		ID: id, SpawnDimension: core.Overworld, SpawnAnchor: anchor,
	})
	engine.Step()

	state := engine.companions[id]
	if len(state.spawnCandidates) != 33*33 || len(state.spawnChunks) != 9 ||
		len(state.spawnWanted) != 9 || len(engine.wanted) != 9 {
		t.Fatalf("出生候选/兴趣/wanted/union=%d/%d/%d/%d，想要 1089/9/9/9",
			len(state.spawnCandidates), len(state.spawnChunks), len(state.spawnWanted), len(engine.wanted))
	}
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			key := core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       core.ChunkPos{X: anchor.X + dx, Z: anchor.Z + dz},
			}
			if !engine.WantsChunk(key) {
				t.Fatalf("缺少出生候选区块 %+v", key)
			}
		}
	}
	if engine.WantsChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: anchor.X + 2, Z: anchor.Z},
	}) {
		t.Fatal("出生兴趣越过 3x3")
	}
}

func TestCompanionInterestIsThreeByThreeAndDoesNotConsumeSessions(t *testing.T) {
	engine := NewEngine(0, 0)
	loadCompanionFlatChunk(t, engine, core.ChunkPos{})
	id := companionTestID(1)
	position := mgl32.Vec3{8.5, 1, 8.5}
	engine.RegisterCompanion(CompanionRestore{
		ID: id,
		Body: &companion.Body{
			ID: id, Dimension: core.Overworld, Position: position,
		},
		SpawnDimension: core.Overworld,
	})
	result := engine.Step()

	if len(engine.sessions) != 0 || len(result.Forget) != 0 {
		t.Fatalf("伙伴污染玩家 session: sessions=%d forget=%+v", len(engine.sessions), result.Forget)
	}
	if len(result.Companions) != 1 || len(engine.wanted) != 9 {
		t.Fatalf("active updates/interest=%d/%d，想要 1/9", len(result.Companions), len(engine.wanted))
	}
	if len(result.Acquire) != 8 || result.Acquire[0].Pos != (core.ChunkPos{X: -1}) {
		t.Fatalf("伙伴独占区块未按伙伴距离排序: %+v", result.Acquire)
	}
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			if !engine.WantsChunk(core.ChunkKey{
				Dimension: core.Overworld,
				Pos:       core.ChunkPos{X: dx, Z: dz},
			}) {
				t.Fatalf("active 兴趣缺少 (%d,%d)", dx, dz)
			}
		}
	}
	if engine.WantsChunk(core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2}}) {
		t.Fatal("active 兴趣越过 3x3")
	}
}

func TestEightPlayersAndFourCompanionsRemainIndependentInEngine(t *testing.T) {
	engine := NewEngine(0, 0)
	loadCompanionFlatChunks(t, engine, core.ChunkPos{}, 1)
	for session := SessionID(1); session <= 8; session++ {
		engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	}
	for suffix := byte(1); suffix <= companion.MaxActive; suffix++ {
		id := companionTestID(suffix)
		engine.RegisterCompanion(CompanionRestore{
			ID: id,
			Body: &companion.Body{
				ID: id, Dimension: core.Overworld, Position: [3]float32{float32(suffix) + 0.5, 1, 0.5},
			},
			SpawnDimension: core.Overworld,
		})
	}
	result := engine.Step()
	if len(result.Players) != 8 || len(result.Companions) != companion.MaxActive ||
		len(engine.sessions) != 8 || len(engine.companions) != companion.MaxActive {
		t.Fatalf("players/companions/sessions/state=%d/%d/%d/%d",
			len(result.Players), len(result.Companions), len(engine.sessions), len(engine.companions))
	}
	assertCompanionPanic(t, func() {
		engine.RegisterCompanion(CompanionRestore{
			ID: companionTestID(9), SpawnDimension: core.Overworld,
		})
	})
}

func companionTestID(suffix byte) companion.ID {
	return companion.ID{6: 0x40, 8: 0x80, 15: suffix}
}

func assertCompanionPanic(t *testing.T, operation func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("操作未 panic")
		}
	}()
	operation()
}

func loadCompanionFlatChunks(t *testing.T, engine *Engine, center core.ChunkPos, radius int32) {
	t.Helper()
	for dz := -radius; dz <= radius; dz++ {
		for dx := -radius; dx <= radius; dx++ {
			loadCompanionFlatChunk(t, engine, core.ChunkPos{X: center.X + dx, Z: center.Z + dz})
		}
	}
}

func loadCompanionAirChunks(t *testing.T, engine *Engine, center core.ChunkPos, radius int32) {
	t.Helper()
	for dz := -radius; dz <= radius; dz++ {
		for dx := -radius; dx <= radius; dx++ {
			position := core.ChunkPos{X: center.X + dx, Z: center.Z + dz}
			loadSpawnTestChunk(t, engine.dimensions[core.Overworld], world.NewChunk(position))
		}
	}
}

func loadCompanionFlatChunk(t *testing.T, engine *Engine, position core.ChunkPos) {
	t.Helper()
	chunk := companionFlatChunk(position)
	dimension := engine.dimensions[core.Overworld]
	if info, exists := dimension.Info(position); !exists {
		loadSpawnTestChunk(t, dimension, chunk)
	} else if info.State == ChunkLoading {
		if !dimension.MarkGenerating(position) {
			t.Fatalf("区块 %+v 未转入 Generating", position)
		}
		if err := dimension.ApplyGenerated(position, chunk); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatalf("区块 %+v 状态=%d，不能装入 fixture", position, info.State)
	}
}

func companionFlatChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for x := 0; x < core.SectionSize; x++ {
		for z := 0; z < core.SectionSize; z++ {
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	return chunk
}

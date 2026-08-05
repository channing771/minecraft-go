package sim

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

func TestPlayerRestoreUsesValidCurrentBeforeSafe(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(7)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		Yaw:            1.25,
		Pitch:          -0.25,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	makeRestoreWorldReady(t, engine, current, safe)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || !update.Reset ||
		update.Dimension != core.Overworld ||
		update.State.Position != current.Position ||
		update.State.Velocity != (mgl32.Vec3{}) ||
		!update.State.OnGround ||
		update.Yaw != 1.25 || update.Pitch != -0.25 {
		t.Fatalf("current restore update=%+v", update)
	}
}

func TestPlayerRestoreKeepsAirborneCurrentExact(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(8)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 5, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || update.State.Position != (mgl32.Vec3{2.5, 5, 0.5}) ||
		update.State.Velocity != (mgl32.Vec3{}) || update.State.OnGround {
		t.Fatalf("airborne current restore update=%+v", update)
	}
}

func TestPlayerRestoreRejectsCurrentOutsideWorldHeight(t *testing.T) {
	tests := []struct {
		name string
		y    float32
	}{
		{name: "feet below minimum", y: float32(core.MinY) - 0.25},
		{name: "head above maximum", y: float32(core.MaxY) - physics.PlayerHeight + 0.25},
		{name: "extreme positive", y: math.MaxFloat32},
		{name: "extreme negative", y: -math.MaxFloat32},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := NewEngine(0, 0)
			id := SessionID(30 + index)
			current := PlayerLocation{
				Dimension: core.Overworld,
				Position:  mgl32.Vec3{2.5, test.y, 0.5},
			}
			safe := PlayerLocation{
				Dimension: core.Overworld,
				Position:  mgl32.Vec3{16.5, 1, 0.5},
			}
			engine.RegisterPlayer(id, PlayerRestore{
				Current:        &current,
				Safe:           &safe,
				SpawnDimension: core.Overworld,
			})
			makeRestoreWorldReady(t, engine, current, safe)

			update := onlyPlayerUpdate(t, engine.Step(), id)
			if !update.Ready || update.State.Position != safe.Position {
				t.Fatalf("out-of-height current restored: update=%+v, want safe %+v", update, safe.Position)
			}
		})
	}
}

func TestPlayerRestoreRejectsOutOfHeightSafeBeforeSpawnFallback(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(34)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, -math.MaxFloat32, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	setRestoreBlock(t, engine, core.BlockPos{X: 2, Y: 1, Z: 0}, core.StoneID)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || update.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("out-of-height safe restored: update=%+v, want spawn fallback", update)
	}
}

func TestPlayerRestoreFallsBackFromBlockedCurrentToSafe(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(9)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		Yaw:            0.75,
		Pitch:          0.125,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	setRestoreBlock(t, engine, core.BlockPos{X: 2, Y: 1, Z: 0}, core.StoneID)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || !update.Reset ||
		update.State.Position != safe.Position ||
		update.State.Velocity != (mgl32.Vec3{}) ||
		!update.State.OnGround ||
		update.Yaw != 0.75 || update.Pitch != 0.125 {
		t.Fatalf("safe fallback update=%+v", update)
	}
}

func TestPlayerRestoreFallsBackFromUnsupportedSafeToSpawn(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(10)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 2, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	makeRestoreWorldReady(t, engine, current, safe)
	setRestoreBlock(t, engine, core.BlockPos{X: 2, Y: 1, Z: 0}, core.StoneID)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || !update.Reset ||
		update.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) ||
		!update.State.OnGround {
		t.Fatalf("spawn fallback update=%+v", update)
	}
}

func TestPlayerRestoreRejectsPartiallySupportedSafe(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(12)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.05, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	setRestoreBlock(t, engine, core.BlockPos{X: 2, Y: 1, Z: 0}, core.StoneID)
	setRestoreBlock(t, engine, core.BlockPos{X: 16, Y: 0, Z: 0}, core.AirID)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready ||
		update.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) {
		t.Fatalf("partially supported safe was accepted: %+v", update)
	}
}

func TestPlayerRestoreCurrentOnGroundMatchesPhysicsContact(t *testing.T) {
	position := mgl32.Vec3{0.5, 1, 0.5}
	narrowWorld := narrowRestoreSupportWorld{}
	physicsState := physics.Step(
		physics.State{Position: position},
		physics.Input{},
		narrowWorld,
	).State
	if !physicsState.OnGround || physicsState.Position != position {
		t.Fatalf("physics ground probe state=%+v, want exact grounded position", physicsState)
	}
	completeSupport, anyGroundContact := playerSupport(position, narrowWorld)
	if completeSupport || !anyGroundContact {
		t.Fatalf("support=(complete=%v, any=%v), want false,true",
			completeSupport, anyGroundContact)
	}

	engine := NewEngine(0, 0)
	id := SessionID(14)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{0.95, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	setRestoreBlock(t, engine, core.BlockPos{X: 1, Y: 0, Z: 0}, core.AirID)

	update := onlyPlayerUpdate(t, engine.Step(), id)
	if !update.Ready || update.State.Position != current.Position ||
		!update.State.OnGround {
		t.Fatalf("partially supported current restore=%+v", update)
	}
}

func TestPlayerRestoreWaitsForEveryCandidateChunkBeforeFallingBack(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(11)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{15.9, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{32.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
	})
	loadRestoreFlatChunk(t, engine, core.ChunkPos{})
	loadRestoreFlatChunk(t, engine, core.ChunkPos{X: 2})
	setRestoreBlock(t, engine, core.BlockPos{X: 15, Y: 1, Z: 0}, core.StoneID)

	waiting := onlyPlayerUpdate(t, engine.Step(), id)
	if waiting.Ready || engine.sessions[id].player.nextRestore != 0 {
		t.Fatalf("candidate crossed unknown neighbor: update=%+v nextRestore=%d",
			waiting, engine.sessions[id].player.nextRestore)
	}

	loadRestoreFlatChunk(t, engine, core.ChunkPos{X: 1})
	restored := onlyPlayerUpdate(t, engine.Step(), id)
	if !restored.Ready || restored.State.Position != safe.Position {
		t.Fatalf("restore after neighbor Ready=%+v", restored)
	}
}

func TestPlayerRestoreReplayProducesIdenticalPlayerAndWorldHashes(t *testing.T) {
	type replayState struct {
		playerHash [32]byte
		worldHash  [32]byte
		revision   uint64
	}
	run := func() replayState {
		engine := NewEngine(0, 0)
		id := SessionID(13)
		current := PlayerLocation{
			Dimension: core.Overworld,
			Position:  mgl32.Vec3{2.5, 1, 0.5},
		}
		safe := PlayerLocation{
			Dimension: core.Overworld,
			Position:  mgl32.Vec3{16.5, 1, 0.5},
		}
		engine.RegisterPlayer(id, PlayerRestore{
			Current:        &current,
			Safe:           &safe,
			Yaw:            0.25,
			Pitch:          -0.1,
			SpawnDimension: core.Overworld,
		})
		makeRestoreWorldReady(t, engine, current, safe)
		engine.Step()
		engine.Enqueue(Command{
			Session: id, Sequence: 1, Kind: CommandPlayerInput,
			MoveX: 1, Yaw: 0.5, Pitch: 0.125,
		})
		engine.Step()
		playerHash, ok := engine.PlayerHash(id)
		if !ok {
			t.Fatal("restored player hash unavailable")
		}
		worldHash, revision, ok := engine.ChunkHash(core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       core.ChunkPos{},
		})
		if !ok {
			t.Fatal("restore world hash unavailable")
		}
		return replayState{
			playerHash: playerHash,
			worldHash:  worldHash,
			revision:   revision,
		}
	}

	first, second := run(), run()
	if first != second {
		t.Fatalf("restore replay differs: first=%+v second=%+v", first, second)
	}
}

func TestRuntimeResetDoesNotReplayLoginRestoreCandidates(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(15)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{31.9, 1, 0.5},
	}
	safe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{64.5, 1, 0.5},
	}
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &safe,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	loadRestoreFlatChunk(t, engine, core.ChunkPos{})
	loadRestoreFlatChunk(t, engine, core.ChunkPos{X: 1})

	waiting := onlyPlayerUpdate(t, engine.Step(), id)
	if waiting.Ready {
		t.Fatalf("restore crossed unknown chunk: %+v", waiting)
	}
	loadRestoreFlatChunk(t, engine, core.ChunkPos{X: 2})
	activatedResult := engine.Step()
	activated := onlyPlayerUpdate(t, activatedResult, id)
	if !activated.Ready || activated.State.Position != current.Position {
		t.Fatalf("login restore=%+v", activated)
	}
	wantForget := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2}},
	}
	gotForget := activatedResult.Forget[id]
	if len(gotForget) != len(wantForget) ||
		gotForget[0] != wantForget[0] || gotForget[1] != wantForget[1] {
		t.Fatalf("restore subscription shrink=%+v, want %+v", gotForget, wantForget)
	}

	engine.sessions[id].player.state.Position[1] = float32(core.MinY - 17)
	resetting := onlyPlayerUpdate(t, engine.Step(), id)
	if resetting.Ready {
		t.Fatalf("invalid state did not enter PendingSpawn: %+v", resetting)
	}
	respawned := onlyPlayerUpdate(t, engine.Step(), id)
	player := engine.sessions[id].player
	if !respawned.Ready ||
		respawned.State.Position != (mgl32.Vec3{0.5, 1, 0.5}) ||
		len(player.restoreCandidates) != 0 ||
		len(player.restoreWanted) != 0 {
		t.Fatalf("runtime reset replayed login restore: update=%+v candidates=%+v wanted=%+v",
			respawned, player.restoreCandidates, player.restoreWanted)
	}
}

func makeRestoreWorldReady(
	t *testing.T,
	engine *Engine,
	current PlayerLocation,
	safe PlayerLocation,
) {
	t.Helper()
	positions := []PlayerLocation{current, safe}
	loaded := make(map[core.ChunkKey]struct{})
	for _, location := range positions {
		center := (core.BlockPos{
			X: int32(location.Position.X()),
			Z: int32(location.Position.Z()),
		}).Chunk()
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				key := core.ChunkKey{
					Dimension: location.Dimension,
					Pos: core.ChunkPos{
						X: center.X + dx,
						Z: center.Z + dz,
					},
				}
				if _, exists := loaded[key]; exists {
					continue
				}
				loaded[key] = struct{}{}
				chunk := world.NewChunk(key.Pos)
				for x := 0; x < core.SectionSize; x++ {
					for z := 0; z < core.SectionSize; z++ {
						chunk.SetBlock(x, 0, z, core.GrassID)
					}
				}
				loadSpawnTestChunk(t, engine.dimensions[key.Dimension], chunk)
			}
		}
	}
}

func onlyPlayerUpdate(
	t *testing.T,
	result TickResult,
	id SessionID,
) PlayerUpdate {
	t.Helper()
	if len(result.Players) != 1 || result.Players[0].Session != id {
		t.Fatalf("Players=%+v, want session %d only", result.Players, id)
	}
	return result.Players[0]
}

func setRestoreBlock(
	t *testing.T,
	engine *Engine,
	position core.BlockPos,
	block core.BlockID,
) {
	t.Helper()
	record := engine.dimensions[core.Overworld].records[position.Chunk()]
	if record == nil || record.State != ChunkReady {
		t.Fatalf("chunk %+v is not Ready", position.Chunk())
	}
	x, _, z := position.Local()
	record.Chunk.SetBlock(x, position.Y, z, block)
	record.Revision++
}

func loadRestoreFlatChunk(
	t *testing.T,
	engine *Engine,
	position core.ChunkPos,
) {
	t.Helper()
	chunk := world.NewChunk(position)
	for x := 0; x < core.SectionSize; x++ {
		for z := 0; z < core.SectionSize; z++ {
			chunk.SetBlock(x, 0, z, core.GrassID)
		}
	}
	dimension := engine.dimensions[core.Overworld]
	if info, exists := dimension.Info(position); !exists {
		loadSpawnTestChunk(t, dimension, chunk)
	} else if info.State == ChunkLoading {
		if !dimension.MarkGenerating(position) {
			t.Fatalf("chunk %+v did not transition Loading -> Generating", position)
		}
		if err := dimension.ApplyGenerated(position, chunk); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Fatalf("chunk %+v has unexpected state %d", position, info.State)
	}
}

type narrowRestoreSupportWorld struct{}

func (narrowRestoreSupportWorld) CollisionBoxes(
	position core.BlockPos,
) physics.CollisionBoxSet {
	if position != (core.BlockPos{}) {
		return physics.CollisionBoxSet{Loaded: true}
	}
	return physics.CollisionBoxSet{
		Loaded: true,
		Count:  1,
		Boxes: [8]core.AABB{{
			Min: mgl32.Vec3{0.4, 0, 0.4},
			Max: mgl32.Vec3{0.6, 1, 0.6},
		}},
	}
}

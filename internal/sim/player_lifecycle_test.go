package sim

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

func TestSafeLocationUpdatesOnlyAfterCompleteReadyGroundContact(t *testing.T) {
	tests := []struct {
		name       string
		position   mgl32.Vec3
		onGround   bool
		prepare    func(*testing.T, *Engine)
		wantUpdate bool
	}{
		{
			name:       "complete grounded support",
			position:   mgl32.Vec3{3.5, 1, 0.5},
			onGround:   true,
			wantUpdate: true,
		},
		{
			name:     "airborne",
			position: mgl32.Vec3{3.5, 5, 0.5},
		},
		{
			name:     "unknown adjacent chunk",
			position: mgl32.Vec3{63.9, 1, 0.5},
			onGround: true,
		},
		{
			name:     "partial support",
			position: mgl32.Vec3{3.95, 1, 0.5},
			onGround: true,
			prepare: func(t *testing.T, engine *Engine) {
				setRestoreBlock(t, engine, core.BlockPos{X: 4, Y: 0, Z: 0}, core.AirID)
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine := NewEngine(0, 0)
			id := SessionID(20 + index)
			current := PlayerLocation{
				Dimension: core.Overworld,
				Position:  mgl32.Vec3{2.5, 1, 0.5},
			}
			originalSafe := PlayerLocation{
				Dimension: core.Overworld,
				Position:  mgl32.Vec3{16.5, 1, 0.5},
			}
			engine.RegisterPlayer(id, PlayerRestore{
				Current:        &current,
				Safe:           &originalSafe,
				SpawnDimension: core.Overworld,
			})
			makeRestoreWorldReady(t, engine, current, originalSafe)
			if update := onlyPlayerUpdate(t, engine.Step(), id); !update.Ready {
				t.Fatalf("initial restore=%+v", update)
			}
			if test.prepare != nil {
				test.prepare(t, engine)
			}
			engine.sessions[id].player.state = physics.State{
				Position: test.position,
				OnGround: test.onGround,
			}

			engine.Step()
			safe := engine.sessions[id].player.safe
			want := originalSafe.Position
			if test.wantUpdate {
				want = test.position
			}
			if safe == nil || safe.Dimension != core.Overworld || safe.Position != want {
				t.Fatalf("safe=%+v, want position %v", safe, want)
			}
			if test.wantUpdate {
				snapshot, ok := engine.PlayerSnapshot(id)
				if !ok || snapshot.Safe == nil || snapshot.Safe.Position != want {
					t.Fatalf("PlayerSnapshot=(%+v,%v), want safe %v", snapshot, ok, want)
				}
			}
		})
	}
}

func TestUnregisterActiveReturnsLastSnapshotAndDropsOldCommands(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(31)
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
		Yaw:            0.625,
		Pitch:          -0.125,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	active := onlyPlayerUpdate(t, engine.Step(), id)
	if !active.Ready || len(engine.wanted) == 0 {
		t.Fatalf("active=%+v wanted=%+v", active, engine.wanted)
	}
	lastPosition := mgl32.Vec3{3.5, 1, 0.5}
	engine.sessions[id].player.state = physics.State{
		Position: lastPosition,
		OnGround: true,
	}
	engine.sessions[id].player.yaw = 0.875
	engine.sessions[id].player.pitch = 0.25
	engine.Enqueue(Command{
		Session: id, Sequence: 1, Kind: CommandPlayerInput, MoveX: 1,
	})

	snapshot, ok := engine.UnregisterSession(id)
	if !ok ||
		snapshot.Current != (PlayerLocation{
			Dimension: core.Overworld,
			Position:  lastPosition,
		}) ||
		snapshot.Yaw != 0.875 || snapshot.Pitch != 0.25 ||
		snapshot.Safe == nil || snapshot.Safe.Position != safe.Position {
		t.Fatalf("UnregisterSession=(%+v,%v)", snapshot, ok)
	}
	engine.Enqueue(Command{
		Session: id, Sequence: 2, Kind: CommandPlayerInput, MoveZ: 1,
	})
	result := engine.Step()
	if len(result.Players) != 0 || len(result.Rejected) != 0 ||
		len(engine.wanted) != 0 {
		t.Fatalf("old command or subscription survived: result=%+v wanted=%+v",
			result, engine.wanted)
	}
	if _, exists := engine.sessions[id]; exists {
		t.Fatal("old command recreated unregistered session")
	}
	if _, repeated := engine.UnregisterSession(id); repeated {
		t.Fatal("repeated unregister returned a snapshot")
	}
}

func TestUnregisterPendingReturnsNoSnapshotAndShrinksSubscriptions(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(32)
	engine.RegisterPlayer(id, PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	requested := engine.Step()
	if len(requested.Acquire) == 0 || len(engine.wanted) == 0 {
		t.Fatalf("pending subscription not established: %+v", requested)
	}
	if snapshot, ok := engine.PlayerSnapshot(id); ok || snapshot != (PlayerSnapshot{}) {
		t.Fatalf("pending PlayerSnapshot=(%+v,%v), want zero,false", snapshot, ok)
	}

	snapshot, ok := engine.UnregisterSession(id)
	if ok || snapshot != (PlayerSnapshot{}) {
		t.Fatalf("pending UnregisterSession=(%+v,%v), want zero,false", snapshot, ok)
	}
	result := engine.Step()
	if len(result.Players) != 0 || len(engine.wanted) != 0 {
		t.Fatalf("pending session survived unregister: result=%+v wanted=%+v",
			result, engine.wanted)
	}
	if _, exists := engine.sessions[id]; exists {
		t.Fatal("pending session still registered")
	}
	if _, repeated := engine.UnregisterSession(id); repeated {
		t.Fatal("repeated pending unregister returned true")
	}
}

func TestOnlyRegisteredObserverAcceptsObserverCommands(t *testing.T) {
	engine := NewEngine(0, 0)
	engine.Enqueue(Command{
		Session: 40, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{X: 7},
	})
	engine.Enqueue(Command{
		Session: 40, Sequence: 2, Kind: CommandResync,
		Dimension: core.Overworld,
	})
	unknown := engine.Step()
	if len(unknown.Acquire) != 0 || len(unknown.Resync) != 0 ||
		len(unknown.Rejected) != 0 {
		t.Fatalf("unknown commands were not silent: %+v", unknown)
	}
	if _, exists := engine.sessions[40]; exists {
		t.Fatal("unknown observer command registered a session")
	}

	const observer = SessionID(41)
	engine.RegisterObserverSession(observer)
	engine.Enqueue(Command{
		Session: observer, Sequence: 99, Kind: CommandPlayerInput, MoveX: 1,
	})
	engine.Enqueue(Command{
		Session: observer, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{X: 3, Z: -2},
	})
	registered := engine.Step()
	want := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 3, Z: -2},
	}
	if len(registered.Rejected) != 0 || len(registered.Acquire) != 1 ||
		registered.Acquire[0] != want {
		t.Fatalf("registered observer result=%+v, want acquire %+v",
			registered, want)
	}
}

func TestUnregisterObserverRemovesSubscriptionAndAllowsReregister(t *testing.T) {
	engine := NewEngine(0, 0)
	const observer = SessionID(71)
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 3, Z: -2},
	}
	engine.RegisterObserverSession(observer)
	engine.Enqueue(Command{
		Session: observer, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: key.Dimension, Center: key.Pos,
	})
	if result := engine.Step(); len(result.Acquire) != 1 ||
		result.Acquire[0] != key || !engine.WantsChunk(key) {
		t.Fatalf("observer subscription = %+v", result)
	}

	if snapshot, ok := engine.UnregisterSession(observer); ok ||
		snapshot != (PlayerSnapshot{}) {
		t.Fatalf("observer UnregisterSession = (%+v, %v)", snapshot, ok)
	}
	engine.Step()
	if engine.WantsChunk(key) {
		t.Fatal("observer unregister 后 union 仍需要旧区块")
	}
	engine.RegisterObserverSession(observer)
}

func TestPlayerRestoreAndSnapshotDeepCopySafeLocation(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(42)
	current := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{2.5, 1, 0.5},
	}
	persistedSafe := PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{16.5, 1, 0.5},
	}
	suppliedSafe := persistedSafe
	engine.RegisterPlayer(id, PlayerRestore{
		Current:        &current,
		Safe:           &suppliedSafe,
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, persistedSafe)
	suppliedSafe.Position = mgl32.Vec3{99.5, 99, 99.5}
	if update := onlyPlayerUpdate(t, engine.Step(), id); !update.Ready {
		t.Fatalf("restore=%+v", update)
	}

	first, ok := engine.PlayerSnapshot(id)
	if !ok || first.Safe == nil || *first.Safe != persistedSafe {
		t.Fatalf("first snapshot=(%+v,%v), want safe %+v", first, ok, persistedSafe)
	}
	first.Safe.Position = mgl32.Vec3{77.5, 77, 77.5}
	second, ok := engine.PlayerSnapshot(id)
	if !ok || second.Safe == nil || *second.Safe != persistedSafe {
		t.Fatalf("snapshot alias polluted authority: (%+v,%v)", second, ok)
	}
}

func TestUpdateSafeLocationDoesNotAllocateAfterInitialization(t *testing.T) {
	engine := NewEngine(0, 0)
	id := SessionID(43)
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
		SpawnDimension: core.Overworld,
	})
	makeRestoreWorldReady(t, engine, current, safe)
	if update := onlyPlayerUpdate(t, engine.Step(), id); !update.Ready {
		t.Fatalf("restore=%+v", update)
	}
	session := engine.sessions[id]
	allocations := testing.AllocsPerRun(1000, func() {
		engine.updateSafeLocation(session)
	})
	if allocations != 0 {
		t.Fatalf("updateSafeLocation allocs/op=%f, want 0", allocations)
	}
}

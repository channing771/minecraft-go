package sim_test

import (
	"cmp"
	"context"
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func TestSubscriptionBeginsLoadingAndAcquiresInDistanceOrder(t *testing.T) {
	engine := sim.NewEngine(1, 0, 0)
	engine.RegisterObserverSession(1)
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{X: 5, Z: -4},
	})

	result := engine.Step()
	want := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 5, Z: -4}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4, Z: -4}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 5, Z: -5}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 5, Z: -3}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: -4}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4, Z: -5}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4, Z: -3}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: -5}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: -3}},
	}
	if !reflect.DeepEqual(result.Acquire, want) || len(result.Generate) != 0 {
		t.Fatalf("Acquire=%+v Generate=%+v, want Acquire=%+v only", result.Acquire, result.Generate, want)
	}
	for _, key := range want {
		info, ok := engine.ChunkInfo(key)
		if !ok || info.State != sim.ChunkLoading {
			t.Fatalf("chunk %+v info=%+v ok=%v, want Loading", key, info, ok)
		}
	}
}

func TestAcquiredMissGeneratesExactlyOnceAndLoadErrorFails(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	engine.RegisterObserverSession(1)
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -3}}
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: key.Dimension, Center: key.Pos,
	})
	engine.Step()
	engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})

	missing := engine.Step()
	if !reflect.DeepEqual(missing.Generate, []core.ChunkKey{key}) {
		t.Fatalf("Generate=%+v, want exactly [%+v]", missing.Generate, key)
	}
	if info, ok := engine.ChunkInfo(key); !ok || info.State != sim.ChunkGenerating {
		t.Fatalf("miss info=%+v ok=%v, want Generating", info, ok)
	}

	failedKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 9, Z: 4}}
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: failedKey.Dimension, Center: failedKey.Pos,
	})
	engine.Step()
	wantErr := errors.New("permission denied")
	engine.SubmitAcquired(sim.AcquiredChunk{Key: failedKey, Err: wantErr})
	failed := engine.Step()
	info, ok := engine.ChunkInfo(failedKey)
	if len(failed.Acquire) != 0 || len(failed.Generate) != 0 || !ok ||
		info.State != sim.ChunkFailed || !errors.Is(info.Err, wantErr) {
		t.Fatalf("failed result=%+v info=%+v ok=%v", failed, info, ok)
	}
}

func TestAcquiredResultsApplyInChunkKeyOrderWithExactLoadState(t *testing.T) {
	engine := sim.NewEngine(1, 0, 0)
	engine.RegisterObserverSession(1)
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{},
	})
	requested := engine.Step()
	for index := len(requested.Acquire) - 1; index >= 0; index-- {
		key := requested.Acquire[index]
		engine.SubmitAcquired(sim.AcquiredChunk{
			Key:               key,
			Chunk:             world.NewChunk(key.Pos),
			Revision:          uint64(index + 10),
			PersistedRevision: uint64(index + 10),
		})
	}

	loaded := engine.Step()
	wantReady := append([]core.ChunkKey(nil), requested.Acquire...)
	sortChunkKeysForTest(wantReady)
	if !reflect.DeepEqual(loaded.Ready, wantReady) {
		t.Fatalf("Ready=%+v, want ChunkKey order %+v", loaded.Ready, wantReady)
	}
	for index := len(requested.Acquire) - 1; index >= 0; index-- {
		key := requested.Acquire[index]
		_, revision, ready := engine.ChunkHash(key)
		if !ready || revision != uint64(index+10) {
			t.Fatalf("chunk %+v revision=%d ready=%v", key, revision, ready)
		}
	}
}

func TestAcquiredResultsAfterForgetDoNotCreateCleanAuthority(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	engine.RegisterObserverSession(1)
	oldKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2}}
	newKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 20}}
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: oldKey.Dimension, Center: oldKey.Pos,
	})
	engine.Step()
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: newKey.Dimension, Center: newKey.Pos,
	})
	engine.Step()

	engine.SubmitAcquired(sim.AcquiredChunk{Key: oldKey, Missing: true})
	if result := engine.Step(); len(result.Generate) != 0 {
		t.Fatalf("forgotten miss generated: %+v", result.Generate)
	}
	if info, ok := engine.ChunkInfo(oldKey); ok {
		t.Fatalf("forgotten miss retained info=%+v", info)
	}

	engine.SubmitAcquired(sim.AcquiredChunk{
		Key: oldKey, Chunk: world.NewChunk(oldKey.Pos), Revision: 7, PersistedRevision: 7,
	})
	if result := engine.Step(); len(result.Ready) != 0 {
		t.Fatalf("late hit published Ready: %+v", result.Ready)
	}
	if info, ok := engine.ChunkInfo(oldKey); ok {
		t.Fatalf("late hit recreated authority: %+v", info)
	}
}

func TestForgottenDirtyLoadedChunkRemainsUnloading(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	engine.RegisterObserverSession(1)
	oldKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -2}}
	newKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 30}}
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: oldKey.Dimension, Center: oldKey.Pos,
	})
	engine.Step()
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: newKey.Dimension, Center: newKey.Pos,
	})
	engine.Step()
	engine.SubmitAcquired(sim.AcquiredChunk{
		Key: oldKey, Chunk: world.NewChunk(oldKey.Pos),
		Revision: 7, PersistedRevision: 7, NeedsRewrite: true, Recovered: true,
	})

	result := engine.Step()
	info, ok := engine.ChunkInfo(oldKey)
	if len(result.Ready) != 0 || !ok || info.State != sim.ChunkUnloading || info.Revision != 7 {
		t.Fatalf("result=%+v info=%+v ok=%v, want retained Unloading", result, info, ok)
	}
	snapshots := engine.PersistenceSnapshots(1, 1<<20, sim.SaveUrgent)
	if len(snapshots) != 1 || snapshots[0].Key != oldKey || snapshots[0].Revision != 7 {
		t.Fatalf("rewrite/recovered snapshot=%+v", snapshots)
	}
}

func TestSameTickCenterMoveDropsOldAcquisitionMiss(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	const session = sim.SessionID(81)
	engine.RegisterObserverSession(session)
	oldKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -3}}
	newKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 20, Z: 30}}
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: oldKey.Dimension, Center: oldKey.Pos,
	})
	engine.Step()

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: newKey.Dimension, Center: newKey.Pos,
	})
	engine.SubmitAcquired(sim.AcquiredChunk{Key: oldKey, Missing: true})
	result := engine.Step()

	if len(result.Generate) != 0 {
		t.Fatalf("same-tick forgotten miss generated=%+v", result.Generate)
	}
	if _, ok := engine.ChunkInfo(oldKey); ok {
		t.Fatalf("same-tick forgotten miss retained old authority")
	}
	if !reflect.DeepEqual(result.Forget[session], []core.ChunkKey{oldKey}) ||
		!reflect.DeepEqual(result.Acquire, []core.ChunkKey{newKey}) {
		t.Fatalf("Forget=%+v Acquire=%+v", result.Forget[session], result.Acquire)
	}
}

func TestSameTickCenterMoveDoesNotPublishOldCleanHit(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	const session = sim.SessionID(82)
	engine.RegisterObserverSession(session)
	oldKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -4, Z: 7}}
	newKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 40, Z: -70}}
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: oldKey.Dimension, Center: oldKey.Pos,
	})
	engine.Step()

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: newKey.Dimension, Center: newKey.Pos,
	})
	engine.SubmitAcquired(sim.AcquiredChunk{
		Key: oldKey, Chunk: world.NewChunk(oldKey.Pos),
		Revision: 5, PersistedRevision: 5,
	})
	result := engine.Step()

	if len(result.Ready) != 0 {
		t.Fatalf("same-tick forgotten clean hit published Ready=%+v", result.Ready)
	}
	if _, ok := engine.ChunkInfo(oldKey); ok {
		t.Fatal("same-tick forgotten clean hit retained old authority")
	}
	if !reflect.DeepEqual(result.Forget[session], []core.ChunkKey{oldKey}) ||
		!reflect.DeepEqual(result.Acquire, []core.ChunkKey{newKey}) {
		t.Fatalf("Forget=%+v Acquire=%+v", result.Forget[session], result.Acquire)
	}
}

func TestSameTickCenterMoveRetainsLateGeneratedWithoutReady(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	const session = sim.SessionID(83)
	engine.RegisterObserverSession(session)
	oldKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: 9}}
	newKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 60, Z: 90}}
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: oldKey.Dimension, Center: oldKey.Pos,
	})
	engine.Step()
	engine.SubmitAcquired(sim.AcquiredChunk{Key: oldKey, Missing: true})
	generated := engine.Step()
	if !reflect.DeepEqual(generated.Generate, []core.ChunkKey{oldKey}) {
		t.Fatalf("Generate=%+v", generated.Generate)
	}

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: newKey.Dimension, Center: newKey.Pos,
	})
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: oldKey.Dimension,
		Pos:       oldKey.Pos,
		Chunk:     world.NewChunk(oldKey.Pos),
	})
	result := engine.Step()

	info, ok := engine.ChunkInfo(oldKey)
	if len(result.Ready) != 0 || !ok || info.State != sim.ChunkUnloading || info.Revision != 1 {
		t.Fatalf("Ready=%+v old info=%+v ok=%v", result.Ready, info, ok)
	}
	if !reflect.DeepEqual(result.Forget[session], []core.ChunkKey{oldKey}) ||
		!reflect.DeepEqual(result.Acquire, []core.ChunkKey{newKey}) {
		t.Fatalf("Forget=%+v Acquire=%+v", result.Forget[session], result.Acquire)
	}
}

func sortChunkKeysForTest(keys []core.ChunkKey) {
	slices.SortFunc(keys, func(left, right core.ChunkKey) int {
		if left.Dimension != right.Dimension {
			return cmp.Compare(left.Dimension, right.Dimension)
		}
		if left.Pos.X != right.Pos.X {
			return cmp.Compare(left.Pos.X, right.Pos.X)
		}
		return cmp.Compare(left.Pos.Z, right.Pos.Z)
	})
}

func TestEngineSortsCommandsAndDeduplicatesSequence(t *testing.T) {
	var stocked core.Hotbar
	stocked.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	stocked.Slots[1] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
	engine, session, chunkPos := readyFlatEngineStocked(t, stocked)
	yaw := float32(math.Pi)

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 4, Kind: sim.CommandPlaceBlock,
		Yaw: yaw, Slot: 1,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
		Yaw: yaw, Slot: 0,
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandSelectHotbar,
		Slot: 1,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("命令被拒绝: %+v", result.Rejected)
	}
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 2 {
		t.Fatalf("Changes = %+v", result.Changes)
	}

	chunk, revision, ok := engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       chunkPos,
	})
	if !ok || revision != 2 {
		t.Fatalf("CloneReadyChunk revision = %d, ok=%v", revision, ok)
	}
	if got := chunk.BlockAt(0, 2, 4); got != core.StoneID {
		t.Fatalf("较早命令 block = %d，想要 stone", got)
	}
	if got := chunk.BlockAt(0, 2, 3); got != core.DirtID {
		t.Fatalf("较晚命令 block = %d，想要 dirt", got)
	}

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 4, Kind: sim.CommandPlaceBlock,
		Yaw: yaw, Slot: 1,
	})
	duplicate := engine.Step()
	if len(duplicate.Changes) != 0 || len(duplicate.Rejected) != 0 {
		t.Fatalf("重复 sequence 产生了结果: %+v", duplicate)
	}
	_, revision, _ = engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       chunkPos,
	})
	if revision != 2 {
		t.Fatalf("重复 sequence 把 revision 改为 %d", revision)
	}
}

func TestEngineBatchesChunkRevisionOncePerTick(t *testing.T) {
	engine, session, chunkPos := readyFlatEngineStocked(t, stockedHotbar(core.ItemStone))
	for sequence := uint64(2); sequence <= 4; sequence++ {
		engine.Enqueue(sim.Command{
			Session: session, Sequence: sequence, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Slot: 0,
		})
	}
	result := engine.Step()
	if len(result.Changes) != 1 {
		t.Fatalf("change batches = %d，想要 1", len(result.Changes))
	}
	batch := result.Changes[0]
	if batch.BaseRevision != 1 || batch.NewRevision != 2 {
		t.Fatalf("revision = %d→%d，想要 1→2", batch.BaseRevision, batch.NewRevision)
	}
	if len(batch.Changes) != 3 {
		t.Fatalf("changes = %+v", batch.Changes)
	}
	wantPositions := []core.BlockPos{
		{X: 0, Y: 2, Z: 2},
		{X: 0, Y: 2, Z: 3},
		{X: 0, Y: 2, Z: 4},
	}
	for index, change := range batch.Changes {
		if change.Position != wantPositions[index] {
			t.Fatalf("changes 未按 block index 排序: %+v", batch.Changes)
		}
	}
	_, revision, ok := engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       chunkPos,
	})
	if !ok || revision != 2 {
		t.Fatalf("authoritative revision = %d, ok=%v", revision, ok)
	}
}

func TestEngineReplayIsDeterministic(t *testing.T) {
	type replayState struct {
		hash     [32]byte
		revision uint64
		tick     uint64
	}
	run := func() replayState {
		engine, session, chunkPos := readyFlatEngineStocked(t, stockedHotbar(core.ItemGrass))
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 2, Kind: sim.CommandPlaceBlock,
			Yaw: float32(math.Pi), Slot: 0,
		})
		engine.Step()
		hash, revision, ok := engine.ChunkHash(core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       chunkPos,
		})
		if !ok {
			t.Fatal("权威区块 hash 不可用")
		}
		return replayState{
			hash:     hash,
			revision: revision,
			tick:     engine.TickCount(),
		}
	}
	if first, second := run(), run(); !reflect.DeepEqual(first, second) {
		t.Fatalf("两次 replay 不同: %v != %v", first, second)
	}
}

func TestPlayerCommandsRejectRegisteredPendingPlayer(t *testing.T) {
	tests := []struct {
		name    string
		command sim.Command
	}{
		{
			name: "movement input",
			command: sim.Command{
				Kind: sim.CommandPlayerInput, MoveX: 1,
			},
		},
		{
			name: "placement",
			command: sim.Command{
				Kind: sim.CommandPlaceBlock,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			engine := sim.NewEngine(0, 0, 0)
			const session = sim.SessionID(1)
			engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
			command := tc.command
			command.Session = session
			command.Sequence = 1
			engine.Enqueue(command)

			result := engine.Step()
			if len(result.Rejected) != 1 || result.Rejected[0] != (sim.Rejection{
				Session: session, Sequence: 1, Reason: sim.RejectPlayerNotReady,
			}) {
				t.Fatalf("Rejected=%+v", result.Rejected)
			}
			if player := onlyPlayer(t, result); player.LastInputSequence != 0 {
				t.Fatalf("PendingSpawn input 被错误确认: %+v", player)
			}
		})
	}
}

func TestPendingPlacementStaysRejectedWhenPlayerActivatesSameTick(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	requested := engine.Step()
	submitAcquiredMisses(engine, requested.Acquire)
	engine.Step()
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Chunk:     generateFlatChunk(core.ChunkPos{}),
	})
	engine.Enqueue(sim.Command{
		Session: session, Sequence: 1, Kind: sim.CommandPlaceBlock,
	})

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectPlayerNotReady ||
		len(result.Changes) != 0 {
		t.Fatalf("PendingSpawn 期间摄取的交互在激活后执行: %+v", result)
	}
}

func TestEngineRunConsumesClockAndStopsIt(t *testing.T) {
	engine := sim.NewEngine(0, 0, 0)
	clock := &oneTickClock{
		ticks:   make(chan time.Time, 1),
		stopped: make(chan struct{}),
	}
	clock.ticks <- time.Now()
	close(clock.ticks)

	if err := engine.Run(context.Background(), clock); err != nil {
		t.Fatal(err)
	}
	if got := engine.TickCount(); got != 1 {
		t.Fatalf("TickCount = %d，想要 1", got)
	}
	select {
	case <-clock.stopped:
	default:
		t.Fatal("Run 没有 Stop clock")
	}
}

type oneTickClock struct {
	ticks   chan time.Time
	stopped chan struct{}
}

func (clock *oneTickClock) C() <-chan time.Time {
	return clock.ticks
}

func (clock *oneTickClock) Stop() {
	close(clock.stopped)
}

func readyFlatEngine(t *testing.T) (*sim.Engine, sim.SessionID, core.ChunkPos) {
	t.Helper()
	return readyFlatEngineStocked(t, core.Hotbar{})
}

func readyFlatEngineStocked(
	t *testing.T,
	hotbar core.Hotbar,
) (*sim.Engine, sim.SessionID, core.ChunkPos) {
	t.Helper()
	engine := sim.NewEngine(0, 0, 0)
	session := sim.SessionID(1)
	chunkPos := core.ChunkPos{}
	engine.RegisterPlayer(session, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    chunkPos,
		Inventory:      core.Inventory{Hotbar: hotbar},
	})
	requested := engine.Step()
	wantKey := core.ChunkKey{Dimension: core.Overworld, Pos: chunkPos}
	if !reflect.DeepEqual(requested.Acquire, []core.ChunkKey{wantKey}) {
		t.Fatalf("Acquire = %+v，想要 %+v", requested.Acquire, wantKey)
	}
	submitAcquiredMisses(engine, requested.Acquire)
	generated := engine.Step()
	if !reflect.DeepEqual(generated.Generate, []core.ChunkKey{wantKey}) {
		t.Fatalf("Generate = %+v，想要 %+v", generated.Generate, wantKey)
	}
	chunk := generateFlatChunk(chunkPos)
	chunk.SetBlock(0, 2, 5, core.StoneID)
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       chunkPos,
		Chunk:     chunk,
	})
	ready := engine.Step()
	if !reflect.DeepEqual(ready.Ready, []core.ChunkKey{wantKey}) ||
		len(ready.Players) != 1 || !ready.Players[0].Ready {
		t.Fatalf("Ready = %+v Players=%+v，想要 %+v 与 active player", ready.Ready, ready.Players, wantKey)
	}
	return engine, session, chunkPos
}

func submitAcquiredMisses(engine *sim.Engine, keys []core.ChunkKey) {
	for _, key := range keys {
		engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	}
}

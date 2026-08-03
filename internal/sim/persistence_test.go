package sim

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// emptyChunkEstimateBytes 是全空区块的存档估算：512 信封 + 32 个固定掉落物槽。
const emptyChunkEstimateBytes = 512 + core.DropsPerChunk*world.DropSlotBytes

func TestPersistenceSnapshotBudgetAndStaleAck(t *testing.T) {
	engine := dirtyPersistenceEngine(t, []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 0}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1}},
	})
	dimension := engine.dimensions[core.Overworld]
	if dimension.RequestUnload(core.ChunkPos{X: 1}) {
		t.Fatal("dirty chunk unloaded before persistence")
	}

	snapshots := engine.PersistenceSnapshots(1, 1, SaveAll)
	if len(snapshots) != 1 || snapshots[0].Key.Pos.X != 1 {
		t.Fatalf("priority snapshots=%+v", snapshots)
	}
	if snapshots[0].EstimatedBytes != emptyChunkEstimateBytes {
		t.Fatalf("oversized snapshot bytes=%d, want %d",
			snapshots[0].EstimatedBytes, emptyChunkEstimateBytes)
	}
	snapshots[0].Chunk.SetBlock(0, 0, 0, core.DirtID)
	record := dimension.records[snapshots[0].Key.Pos]
	if got := record.Chunk.BlockAt(0, 0, 0); got == core.DirtID {
		t.Fatal("save snapshot aliases authority")
	}

	record.Chunk.SetBlock(0, 0, 0, core.GrassID)
	record.Revision++
	engine.ApplyPersisted([]PersistedChunk{{
		Key: snapshots[0].Key, Revision: snapshots[0].Revision,
	}})
	if !record.Dirty() || record.PersistedRevision != snapshots[0].Revision ||
		record.SaveInFlightRevision != 0 {
		t.Fatalf("stale ack cleared newer dirty state: %+v", record)
	}
}

func TestPersistenceSnapshotsRespectModeOrderAndBudgets(t *testing.T) {
	keys := []core.ChunkKey{
		{Dimension: 1, Pos: core.ChunkPos{X: -2, Z: 4}},
		{Dimension: -1, Pos: core.ChunkPos{X: 5, Z: 3}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -3}},
	}
	engine := dirtyPersistenceEngine(t, keys)
	for _, key := range []core.ChunkKey{keys[0], keys[2]} {
		if engine.dimensions[key.Dimension].RequestUnload(key.Pos) {
			t.Fatalf("dirty chunk %+v unloaded before persistence", key)
		}
	}

	urgent := engine.PersistenceSnapshots(10, 1<<20, SaveUrgent)
	if got, want := snapshotKeys(urgent), []core.ChunkKey{keys[2], keys[0]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("urgent keys=%+v, want %+v", got, want)
	}
	engine.FailPersistence(urgent)

	all := engine.PersistenceSnapshots(10, 1<<20, SaveAll)
	wantAll := []core.ChunkKey{keys[2], keys[0], keys[1], keys[3]}
	if got := snapshotKeys(all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("all keys=%+v, want %+v", got, wantAll)
	}
	engine.FailPersistence(all)

	byChunks := engine.PersistenceSnapshots(1, 1<<20, SaveAll)
	if got := snapshotKeys(byChunks); !reflect.DeepEqual(got, wantAll[:1]) {
		t.Fatalf("chunk-budget keys=%+v, want %+v", got, wantAll[:1])
	}
	engine.FailPersistence(byChunks)

	byBytes := engine.PersistenceSnapshots(10, 512, SaveAll)
	if got := snapshotKeys(byBytes); !reflect.DeepEqual(got, wantAll[:1]) {
		t.Fatalf("byte-budget keys=%+v, want %+v", got, wantAll[:1])
	}
}

func TestPersistenceKeepsOneSnapshotPerKeyAndFailureMustMatch(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4}}
	engine := dirtyPersistenceEngine(t, []core.ChunkKey{key})
	record := engine.dimensions[key.Dimension].records[key.Pos]

	selected := engine.PersistenceSnapshots(10, 1<<20, SaveAll)
	if len(selected) != 1 || len(engine.PersistenceSnapshots(10, 1<<20, SaveAll)) != 0 {
		t.Fatalf("one key acquired more than once: %+v", selected)
	}
	wantInFlight := PersistenceStats{
		DirtyChunks: 1, EstimatedBytes: 2 * emptyChunkEstimateBytes, InFlightChunks: 1,
	}
	engine.FailPersistence([]ChunkSaveSnapshot{{
		Key: key, Revision: selected[0].Revision + 1, Chunk: selected[0].Chunk,
	}})
	if got := engine.PersistenceStats(); record.SaveInFlightRevision != selected[0].Revision || got != wantInFlight {
		t.Fatalf("mismatched failure cleared in-flight state: record=%+v stats=%+v", record, got)
	}
	engine.FailPersistence(selected)
	if got, want := engine.PersistenceStats(), (PersistenceStats{
		DirtyChunks: 1, EstimatedBytes: emptyChunkEstimateBytes,
	}); record.SaveInFlightRevision != 0 || record.PersistedRevision != 0 ||
		!record.Dirty() || got != want {
		t.Fatalf("matching failure advanced or retained in-flight state: record=%+v stats=%+v", record, got)
	}
	if retried := engine.PersistenceSnapshots(10, 1<<20, SaveAll); len(retried) != 1 {
		t.Fatalf("failed chunk was not selectable again: %+v", retried)
	}
}

func TestPersistenceStaleAckCannotClearNewerInFlightSnapshot(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{Z: 6}}
	engine := dirtyPersistenceEngine(t, []core.ChunkKey{key})
	record := engine.dimensions[key.Dimension].records[key.Pos]

	old := engine.PersistenceSnapshots(1, 1<<20, SaveAll)[0]
	engine.FailPersistence([]ChunkSaveSnapshot{old})
	record.Chunk.SetBlock(0, 0, 0, core.StoneID)
	record.Revision++
	current := engine.PersistenceSnapshots(1, 1<<20, SaveAll)[0]
	wantInFlight := PersistenceStats{
		DirtyChunks: 1, EstimatedBytes: 4136 + 2*emptyChunkEstimateBytes, InFlightChunks: 1,
	}

	engine.ApplyPersisted([]PersistedChunk{{Key: key, Revision: old.Revision}})
	if record.PersistedRevision != old.Revision ||
		record.SaveInFlightRevision != current.Revision || !record.Dirty() {
		t.Fatalf("old ack cleared newer in-flight save: %+v", record)
	}
	if got := engine.PersistenceStats(); got != wantInFlight {
		t.Fatalf("old ack changed newer in-flight accounting: got %+v, want %+v", got, wantInFlight)
	}
	if snapshots := engine.PersistenceSnapshots(1, 1<<20, SaveAll); len(snapshots) != 0 {
		t.Fatalf("newer in-flight key selected twice: %+v", snapshots)
	}
}

func TestPersistenceSuccessClearsRewriteAndRejectsFutureRevision(t *testing.T) {
	engine := NewEngine(0)
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 5}}
	dimension := engine.dimensions[key.Dimension]
	if !dimension.BeginLoading(key.Pos) {
		t.Fatal("load not started")
	}
	if err := dimension.ApplyLoaded(key.Pos, world.NewChunk(key.Pos), 7, 7, true, false); err != nil {
		t.Fatal(err)
	}
	record := dimension.records[key.Pos]
	snapshot := engine.PersistenceSnapshots(1, 1<<20, SaveAll)
	if len(snapshot) != 1 || snapshot[0].Revision != 7 {
		t.Fatalf("rewrite snapshots=%+v", snapshot)
	}
	engine.ApplyPersisted([]PersistedChunk{{Key: key, Revision: 7}})
	if stats := engine.PersistenceStats(); record.NeedsRewrite || record.Dirty() ||
		record.SaveInFlightRevision != 0 || stats != (PersistenceStats{}) {
		t.Fatalf("current rewrite ack did not clear persistence state: record=%+v stats=%+v", record, stats)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("ack above current revision did not panic")
		}
	}()
	engine.ApplyPersisted([]PersistedChunk{{Key: key, Revision: 8}})
}

func TestPersistenceAckDeletesOnlyStillUnloadingChunk(t *testing.T) {
	t.Run("unloading", func(t *testing.T) {
		key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 8}}
		engine := dirtyPersistenceEngine(t, []core.ChunkKey{key})
		dimension := engine.dimensions[key.Dimension]
		if dimension.RequestUnload(key.Pos) {
			t.Fatal("dirty chunk unloaded before persistence")
		}
		snapshot := engine.PersistenceSnapshots(1, 1<<20, SaveUrgent)
		engine.ApplyPersisted([]PersistedChunk{{Key: key, Revision: snapshot[0].Revision}})
		if _, exists := dimension.records[key.Pos]; exists {
			t.Fatal("clean unloading chunk retained after ack")
		}
	})

	t.Run("resubscribed", func(t *testing.T) {
		key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 9}}
		engine := dirtyPersistenceEngine(t, []core.ChunkKey{key})
		dimension := engine.dimensions[key.Dimension]
		record := dimension.records[key.Pos]
		authority := record.Chunk
		if dimension.RequestUnload(key.Pos) {
			t.Fatal("dirty chunk unloaded before persistence")
		}
		snapshot := engine.PersistenceSnapshots(1, 1<<20, SaveUrgent)
		if !dimension.CancelUnload(key.Pos) {
			t.Fatal("unload cancellation failed")
		}
		engine.ApplyPersisted([]PersistedChunk{{Key: key, Revision: snapshot[0].Revision}})
		got := dimension.records[key.Pos]
		if got == nil || got.State != ChunkReady || got.UnloadRequested ||
			got.Chunk != authority || got.Dirty() || got.SaveInFlightRevision != 0 {
			t.Fatalf("resubscribed chunk was not retained cleanly: %+v", got)
		}
	})
}

func TestPersistenceStatsKeepSelectionTimeInFlightBytesStable(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 11}}
	engine := dirtyPersistenceEngine(t, []core.ChunkKey{key})
	dimension := engine.dimensions[key.Dimension]
	if dimension.RequestUnload(key.Pos) {
		t.Fatal("dirty chunk unloaded before persistence")
	}

	if got, want := engine.PersistenceStats(), (PersistenceStats{
		DirtyChunks: 1, EstimatedBytes: emptyChunkEstimateBytes, UnloadWaiting: 1,
	}); got != want {
		t.Fatalf("stats before save=%+v, want %+v", got, want)
	}
	snapshot := engine.PersistenceSnapshots(1, 1<<20, SaveUrgent)
	selectedStats := PersistenceStats{
		DirtyChunks: 1, EstimatedBytes: 2 * emptyChunkEstimateBytes,
		InFlightChunks: 1, UnloadWaiting: 1,
	}
	if got := engine.PersistenceStats(); got != selectedStats {
		t.Fatalf("stats at selection=%+v, want %+v", got, selectedStats)
	}
	snapshot[0].Chunk.SetBlock(0, 0, 0, core.DirtID)
	if got := engine.PersistenceStats(); got != selectedStats {
		t.Fatalf("consumer mutation changed sim accounting: got %+v, want %+v", got, selectedStats)
	}
	if got := dimension.records[key.Pos].Chunk.BlockAt(0, 0, 0); got != core.AirID {
		t.Fatalf("consumer mutation changed authority block to %d", got)
	}
	engine.FailPersistence(snapshot)
	if got, want := engine.PersistenceStats(), (PersistenceStats{
		DirtyChunks: 1, EstimatedBytes: emptyChunkEstimateBytes, UnloadWaiting: 1,
	}); got != want {
		t.Fatalf("stats after failure=%+v, want %+v", got, want)
	}
}

func dirtyPersistenceEngine(t *testing.T, keys []core.ChunkKey) *Engine {
	t.Helper()
	engine := NewEngine(0)
	for _, key := range keys {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			dimension = NewDimension(key.Dimension)
			engine.dimensions[key.Dimension] = dimension
		}
		if !dimension.BeginGeneration(key.Pos) {
			t.Fatalf("generation not started for %+v", key)
		}
		if err := dimension.ApplyGenerated(key.Pos, world.NewChunk(key.Pos)); err != nil {
			t.Fatal(err)
		}
	}
	return engine
}

func snapshotKeys(snapshots []ChunkSaveSnapshot) []core.ChunkKey {
	keys := make([]core.ChunkKey, len(snapshots))
	for index, snapshot := range snapshots {
		keys[index] = snapshot.Key
	}
	return keys
}

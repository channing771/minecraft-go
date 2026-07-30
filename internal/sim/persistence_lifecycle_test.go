package sim

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestGeneratedChunkIsDirtyUntilPersisted(t *testing.T) {
	dimension := NewDimension(core.Overworld)
	pos := core.ChunkPos{X: 2, Z: -4}
	if !dimension.BeginGeneration(pos) {
		t.Fatal("generation not started")
	}
	if err := dimension.ApplyGenerated(pos, world.NewChunk(pos)); err != nil {
		t.Fatal(err)
	}
	record := dimension.records[pos]
	if record.Revision != 1 || record.PersistedRevision != 0 || !record.Dirty() {
		t.Fatalf("generated record=%+v", record)
	}
	if unloaded := dimension.RequestUnload(pos); unloaded ||
		record.State != ChunkUnloading || record.Chunk == nil || !record.UnloadRequested {
		t.Fatalf("dirty chunk was discarded: %+v", record)
	}
}

func TestLoadedChunkKeepsPersistedRevisionAndCancelsUnload(t *testing.T) {
	pos := core.ChunkPos{}
	clean := NewDimension(core.Overworld)
	if !clean.BeginLoading(pos) {
		t.Fatal("load not started")
	}
	if err := clean.ApplyLoaded(pos, world.NewChunk(pos), 7, 7, false, false); err != nil {
		t.Fatal(err)
	}
	if !clean.RequestUnload(pos) {
		t.Fatal("clean loaded chunk should unload immediately")
	}
	if _, exists := clean.records[pos]; exists {
		t.Fatal("clean loaded chunk was retained")
	}

	dirty := NewDimension(core.Overworld)
	if !dirty.BeginLoading(pos) {
		t.Fatal("dirty load not started")
	}
	if err := dirty.ApplyLoaded(pos, world.NewChunk(pos), 8, 7, false, false); err != nil {
		t.Fatal(err)
	}
	if unloaded := dirty.RequestUnload(pos); unloaded {
		t.Fatal("dirty loaded chunk was discarded")
	}
	record := dirty.records[pos]
	chunk := record.Chunk
	if record.State != ChunkUnloading || chunk == nil || !record.UnloadRequested {
		t.Fatalf("dirty unload=%+v", record)
	}
	if !dirty.CancelUnload(pos) || dirty.records[pos].State != ChunkReady ||
		dirty.records[pos].Chunk != chunk || dirty.records[pos].UnloadRequested {
		t.Fatalf("cancel unload=%+v", dirty.records[pos])
	}
}

func TestPersistenceLifecyclePreservesRecoveryAndRewriteMetadata(t *testing.T) {
	tests := []struct {
		name              string
		revision          uint64
		persistedRevision uint64
		needsRewrite      bool
		recovered         bool
	}{
		{
			name:              "recovered older payload",
			revision:          3,
			persistedRevision: 1,
			needsRewrite:      true,
			recovered:         true,
		},
		{
			name:              "format migration rewrite",
			revision:          7,
			persistedRevision: 7,
			needsRewrite:      true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dimension := NewDimension(core.Overworld)
			pos := core.ChunkPos{X: -3, Z: 5}
			if !dimension.BeginLoading(pos) {
				t.Fatal("load not started")
			}
			if err := dimension.ApplyLoaded(
				pos,
				world.NewChunk(pos),
				test.revision,
				test.persistedRevision,
				test.needsRewrite,
				test.recovered,
			); err != nil {
				t.Fatal(err)
			}
			record := dimension.records[pos]
			if record.Revision != test.revision ||
				record.PersistedRevision != test.persistedRevision ||
				record.NeedsRewrite != test.needsRewrite ||
				record.Recovered != test.recovered || !record.Dirty() {
				t.Fatalf("loaded record=%+v", record)
			}
		})
	}
}

func TestRecoveredOnlyLoadedChunkRequiresRewriteOnUnload(t *testing.T) {
	dimension := NewDimension(core.Overworld)
	pos := core.ChunkPos{X: 7, Z: -11}
	if !dimension.BeginLoading(pos) {
		t.Fatal("load not started")
	}
	if err := dimension.ApplyLoaded(pos, world.NewChunk(pos), 6, 6, false, true); err != nil {
		t.Fatal(err)
	}
	record := dimension.records[pos]
	if !record.Recovered || !record.NeedsRewrite || !record.Dirty() {
		t.Fatalf("recovered-only record=%+v", record)
	}
	if dimension.RequestUnload(pos) || record.State != ChunkUnloading ||
		!record.UnloadRequested || record.Chunk == nil {
		t.Fatalf("recovered-only unload=%+v", record)
	}
}

func TestEngineAcquiredHitPropagatesExactPersistenceState(t *testing.T) {
	engine := NewEngine(0)
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -6, Z: 3}}
	engine.RegisterObserverSession(71)
	engine.Enqueue(Command{
		Session: 71, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: key.Dimension, Center: key.Pos,
	})
	requested := engine.Step()
	if !reflect.DeepEqual(requested.Acquire, []core.ChunkKey{key}) {
		t.Fatalf("Acquire=%+v", requested.Acquire)
	}
	chunk := world.NewChunk(key.Pos)
	engine.SubmitAcquired(AcquiredChunk{
		Key:               key,
		Chunk:             chunk,
		Revision:          12,
		PersistedRevision: 9,
		NeedsRewrite:      true,
		Recovered:         true,
	})

	loaded := engine.Step()
	record := engine.dimensions[key.Dimension].records[key.Pos]
	if !reflect.DeepEqual(loaded.Ready, []core.ChunkKey{key}) || record == nil ||
		record.State != ChunkReady || record.Chunk != chunk ||
		record.Revision != 12 || record.PersistedRevision != 9 ||
		!record.NeedsRewrite || !record.Recovered {
		t.Fatalf("Ready=%+v record=%+v", loaded.Ready, record)
	}
}

func TestEngineForgottenCleanAcquiredHitIsDeleted(t *testing.T) {
	engine := NewEngine(0)
	oldKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 3}}
	newKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 40}}
	engine.RegisterObserverSession(72)
	engine.Enqueue(Command{
		Session: 72, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: oldKey.Dimension, Center: oldKey.Pos,
	})
	engine.Step()
	engine.Enqueue(Command{
		Session: 72, Sequence: 2, Kind: CommandTrustedObserverCenter,
		Dimension: newKey.Dimension, Center: newKey.Pos,
	})
	engine.Step()
	engine.SubmitAcquired(AcquiredChunk{
		Key: oldKey, Chunk: world.NewChunk(oldKey.Pos),
		Revision: 5, PersistedRevision: 5,
	})

	loaded := engine.Step()
	if len(loaded.Ready) != 0 {
		t.Fatalf("forgotten clean hit published Ready=%+v", loaded.Ready)
	}
	if _, exists := engine.dimensions[oldKey.Dimension].records[oldKey.Pos]; exists {
		t.Fatal("forgotten clean hit retained authority")
	}
}

func TestPersistenceLifecycleRejectsPersistedRevisionAboveCurrent(t *testing.T) {
	dimension := NewDimension(core.Overworld)
	pos := core.ChunkPos{X: 1, Z: 1}
	if !dimension.BeginLoading(pos) {
		t.Fatal("load not started")
	}
	if err := dimension.ApplyLoaded(pos, world.NewChunk(pos), 4, 5, false, false); err == nil {
		t.Fatal("persisted revision above current was accepted")
	}
	if record := dimension.records[pos]; record.State != ChunkLoading || record.Chunk != nil {
		t.Fatalf("invalid load changed authority: %+v", record)
	}
}

func TestPersistenceLifecycleLoadTransitionsSupportMissDropAndRetry(t *testing.T) {
	dimension := NewDimension(core.Overworld)
	generatePos := core.ChunkPos{X: 3}
	if !dimension.BeginLoading(generatePos) || !dimension.MarkGenerating(generatePos) {
		t.Fatal("load miss did not transition Loading to Generating")
	}
	if err := dimension.ApplyGenerated(generatePos, world.NewChunk(generatePos)); err != nil {
		t.Fatal(err)
	}

	droppedPos := core.ChunkPos{X: 4}
	if !dimension.BeginLoading(droppedPos) {
		t.Fatal("drop load not started")
	}
	dimension.DropLoading(droppedPos)
	if _, exists := dimension.records[droppedPos]; exists {
		t.Fatal("dropped loading record was retained")
	}

	failedPos := core.ChunkPos{X: 5}
	wantErr := errors.New("load failed")
	if !dimension.BeginLoading(failedPos) {
		t.Fatal("failed load not started")
	}
	dimension.MarkLoadFailed(failedPos, wantErr)
	record := dimension.records[failedPos]
	if record.State != ChunkFailed || !errors.Is(record.Err, wantErr) {
		t.Fatalf("failed load record=%+v", record)
	}
	if !dimension.BeginLoading(failedPos) || dimension.records[failedPos].Err != nil {
		t.Fatalf("failed load did not retry cleanly: %+v", dimension.records[failedPos])
	}
}

func TestPersistenceLifecycleInFlightCleanChunkIsRetainedOnUnload(t *testing.T) {
	dimension := NewDimension(core.Overworld)
	pos := core.ChunkPos{Z: -9}
	chunk := world.NewChunk(pos)
	if !dimension.BeginLoading(pos) {
		t.Fatal("load not started")
	}
	if err := dimension.ApplyLoaded(pos, chunk, 7, 7, false, false); err != nil {
		t.Fatal(err)
	}
	record := dimension.records[pos]
	record.SaveInFlightRevision = 7
	if dimension.RequestUnload(pos) || record.State != ChunkUnloading ||
		record.Chunk != chunk || !record.UnloadRequested {
		t.Fatalf("in-flight clean chunk was discarded: %+v", record)
	}
}

func TestPersistenceLifecycleBlockChangeAdvancesDirtyRevision(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	engine.Enqueue(Command{
		Session:  session,
		Sequence: 1,
		Kind:     CommandBreakBlock,
		Pitch:    -float32(math.Pi)/2 + 0.01,
	})
	result := engine.Step()
	if len(result.Changes) != 1 {
		t.Fatalf("block change batches=%+v", result.Changes)
	}
	record := engine.dimensions[core.Overworld].records[core.ChunkPos{}]
	if record.Revision != 2 || record.PersistedRevision != 0 || !record.Dirty() {
		t.Fatalf("changed record=%+v", record)
	}
}

func TestPersistenceLifecycleRetainsLateGeneratedChunkForSaving(t *testing.T) {
	engine := NewEngine(0)
	const session = SessionID(41)
	engine.RegisterObserverSession(session)
	first := core.ChunkPos{X: 2, Z: -4}
	second := core.ChunkPos{X: 20, Z: 20}
	engine.Enqueue(Command{
		Session: session, Sequence: 1, Kind: CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: first,
	})
	acquired := engine.Step()
	engine.SubmitAcquired(AcquiredChunk{
		Key: core.ChunkKey{Dimension: core.Overworld, Pos: first}, Missing: true,
	})
	generated := engine.Step()
	if len(acquired.Acquire) != 1 || len(generated.Generate) != 1 {
		t.Fatalf("acquire=%+v generate=%+v", acquired.Acquire, generated.Generate)
	}
	engine.Enqueue(Command{
		Session: session, Sequence: 2, Kind: CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: second,
	})
	engine.Step()

	engine.SubmitGenerated(GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       first,
		Chunk:     world.NewChunk(first),
	})
	result := engine.Step()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: first}
	for _, ready := range result.Ready {
		if ready == key {
			t.Fatalf("forgotten chunk published ready: %+v", result.Ready)
		}
	}
	record := engine.dimensions[core.Overworld].records[first]
	if record == nil || record.State != ChunkUnloading || record.Chunk == nil ||
		record.Revision != 1 || record.PersistedRevision != 0 || !record.Dirty() {
		t.Fatalf("late generated chunk was not retained: %+v", record)
	}

	engine.Enqueue(Command{
		Session: session, Sequence: 3, Kind: CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: first,
	})
	resubscribed := engine.Step()
	record = engine.dimensions[core.Overworld].records[first]
	if record.State != ChunkReady || record.UnloadRequested ||
		!reflect.DeepEqual(resubscribed.Ready, []core.ChunkKey{key}) {
		t.Fatalf("retained chunk was not reused: record=%+v ready=%+v", record, resubscribed.Ready)
	}
}

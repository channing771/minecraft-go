package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

func TestAutosaveBeginsAtConfiguredTickWithoutBlockingStep(t *testing.T) {
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	running := newPersistenceServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 2

	running.StepForTest()
	assertNoSaveStarted(t, store)

	stepDone := make(chan struct{})
	go func() {
		running.StepForTest()
		close(stepDone)
	}()
	select {
	case <-stepDone:
	case <-time.After(waitDeadline):
		t.Fatal("Step blocked on gated Store.SaveBatch")
	}
	call := receiveSaveCall(t, store)
	if got, want := saveKeys(call), []core.ChunkKey{chunkKey(0, 0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("autosave keys=%+v, want %+v", got, want)
	}
}

func TestUrgentSaveDispatchesDirtyUnloadingBeforeAutosave(t *testing.T) {
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	running := newPersistenceServer(t, store)
	want := chunkKey(3, -2)
	running.engine = dirtyUnloadingEngine(t, want)
	running.config.AutosaveTicks = running.engine.TickCount() + 100

	running.StepForTest()
	call := receiveSaveCall(t, store)
	if got := saveKeys(call); !reflect.DeepEqual(got, []core.ChunkKey{want}) {
		t.Fatalf("urgent keys=%+v, want %+v", got, []core.ChunkKey{want})
	}
}

func TestSaveJobsGroupRegionsAndSortDeterministically(t *testing.T) {
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	running := newPersistenceServer(t, store)
	keys := []core.ChunkKey{
		chunkKey(2, 5),
		chunkKey(-1, 7),
		chunkKey(1, 4),
	}
	running.engine = dirtyReadyEngine(t, keys)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	wantFirst := []core.ChunkKey{chunkKey(-1, 7)}
	if got := saveKeys(first); !reflect.DeepEqual(got, wantFirst) {
		t.Fatalf("first region keys=%+v, want %+v", got, wantFirst)
	}
	store.gate <- struct{}{}

	second := receiveSaveCall(t, store)
	wantSecond := []core.ChunkKey{chunkKey(1, 4), chunkKey(2, 5)}
	if got := saveKeys(second); !reflect.DeepEqual(got, wantSecond) {
		t.Fatalf("second region keys=%+v, want %+v", got, wantSecond)
	}
	store.gate <- struct{}{}
}

func TestSaveCompletionIsAcknowledgedOnlyAtNextStepStart(t *testing.T) {
	store := newPersistenceTestStore()
	observed := make(chan time.Duration, 1)
	store.respond = func(_ int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1
	running.config.SaveObserver = func(elapsed time.Duration) { observed <- elapsed }

	running.StepForTest()
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	select {
	case elapsed := <-observed:
		if elapsed < 0 {
			t.Fatalf("SaveObserver duration=%v", elapsed)
		}
	case <-time.After(waitDeadline):
		t.Fatal("SaveObserver was not called")
	}
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("completion changed sim before next Step: %+v", got)
	}

	running.StepForTest()
	if got := running.engine.PersistenceStats(); got != (sim.PersistenceStats{}) {
		t.Fatalf("next Step did not acknowledge save: %+v", got)
	}
	if running.autosaveActive {
		t.Fatal("autosave stayed active after dirty and in-flight chunks drained")
	}
}

func TestSaveErrorAcknowledgesOnlyCommittedAndRetainsUncommitted(t *testing.T) {
	keys := []core.ChunkKey{chunkKey(0, 0), chunkKey(1, 0)}
	store := newPersistenceTestStore()
	wantErr := errors.New("partial save")
	store.respond = func(_ int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		return storage.SaveResult{Committed: map[core.ChunkKey]uint64{
			saves[0].Key: saves[0].Revision,
		}}, wantErr
	}
	running := newPersistenceServer(t, store)
	running.engine = dirtyReadyEngine(t, keys)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 2 || got.InFlightChunks != 2 {
		t.Fatalf("partial completion changed sim before next Step: %+v", got)
	}
	running.autosaveActive = false
	running.config.AutosaveTicks = running.engine.TickCount() + 100
	running.StepForTest()

	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("partial failure stats=%+v, want one retained in-flight chunk", got)
	}
	if duplicate := running.engine.PersistenceSnapshots(2, 1<<20, sim.SaveAll); len(duplicate) != 0 {
		t.Fatalf("retained retry became selectable again: %+v", duplicate)
	}
	region, _ := storage.RegionFor(keys[1])
	retained := running.retry[region]
	if len(retained) != 1 {
		t.Fatalf("retained retry cohorts=%d, want 1", len(retained))
	}
	if got, want := snapshotKeys(retained[0].Job.Snapshots), keys[1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained retry snapshots=%+v, want %+v", got, want)
	}
}

func TestSaveCompletionAboveCurrentReleasesSnapshotWithoutFalseAck(t *testing.T) {
	key := chunkKey(0, 0)
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	store.respond = func(_ int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		return storage.SaveResult{Committed: map[core.ChunkKey]uint64{
			saves[0].Key: saves[0].Revision + 1,
		}}, nil
	}
	running := newPersistenceServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{key})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	call := receiveSaveCall(t, store)
	if len(call) != 1 || call[0].Revision != 1 {
		t.Fatalf("save call=%+v, want revision 1", call)
	}
	running.autosaveActive = false
	running.config.AutosaveTicks = running.engine.TickCount() + 100
	store.gate <- struct{}{}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)

	running.StepForTest()
	info, ok := running.engine.ChunkInfo(key)
	if !ok || info.Revision != 1 {
		t.Fatalf("authority info=%+v,%v, want current revision 1", info, ok)
	}
	if got := persistenceRevisionsForTest(t, running.engine, key); got.persisted != 0 {
		t.Fatalf("persisted revision=%d, want 0 after impossible committed revision", got.persisted)
	}
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 0 {
		t.Fatalf("high committed revision stats=%+v, want dirty retryable authority", got)
	}
	retry := running.engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(retry) != 1 || retry[0].Key != key || retry[0].Revision != 1 {
		t.Fatalf("retry snapshots=%+v, want key at revision 1", retry)
	}
	running.engine.FailPersistence(retry)
}

func TestSaveCompletionAheadOfSnapshotAcceptsBoundedPersistedRevision(t *testing.T) {
	key := chunkKey(0, 0)
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	store.respond = func(_ int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		return storage.SaveResult{Committed: map[core.ChunkKey]uint64{
			saves[0].Key: saves[0].Revision + 1,
		}}, nil
	}
	running := newPersistenceServer(t, store)
	running.engine = dirtyPlayerEngine(t, key)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	call := receiveSaveCall(t, store)
	if len(call) != 1 || call[0].Revision != 1 {
		t.Fatalf("save call=%+v, want revision 1", call)
	}
	for sequence, wantRevision := range []uint64{2, 3} {
		running.engine.SetBlockForTest(core.BlockPos{}, core.GrassID)
		running.engine.Enqueue(sim.Command{
			Session: testSessionID, Sequence: uint64(sequence + 1),
			Kind: sim.CommandPlayerInput, Pitch: -1.5, Mining: true,
		})
		for range 4 {
			if primed := running.engine.Step(); len(primed.Changes) != 0 {
				t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
			}
		}
		changed := running.StepForTest()
		if len(changed.Changes) != 1 || changed.Changes[0].NewRevision != wantRevision {
			t.Fatalf("change %d=%+v, want revision %d", sequence, changed.Changes, wantRevision)
		}
	}
	info, ok := running.engine.ChunkInfo(key)
	if !ok || info.Revision != 3 {
		t.Fatalf("authority info=%+v,%v, want current revision 3", info, ok)
	}

	running.autosaveActive = false
	running.config.AutosaveTicks = running.engine.TickCount() + 100
	store.gate <- struct{}{}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()

	if got := persistenceRevisionsForTest(t, running.engine, key); got.current != 3 ||
		got.persisted != 2 || got.inFlight != 0 {
		t.Fatalf("persistence revisions=%+v, want current=3 persisted=2 inFlight=0", got)
	}
	retry := running.engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(retry) != 1 || retry[0].Key != key || retry[0].Revision != 3 {
		t.Fatalf("retry snapshots=%+v, want key at revision 3", retry)
	}
	running.engine.FailPersistence(retry)
}

func TestSaveCompletionEqualToNewerAuthorityDoesNotClaimForeignContent(t *testing.T) {
	key := chunkKey(0, 0)
	memory := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})
	foreign := world.NewChunk(key.Pos)
	foreign.SetBlock(7, 10, 7, core.DirtID)
	if _, err := memory.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 2, Chunk: foreign,
	}}); err != nil {
		t.Fatal(err)
	}

	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	store.respond = func(_ int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		return memory.SaveBatch(context.Background(), saves)
	}
	running := newPersistenceServer(t, store)
	running.engine = dirtyPlayerEngine(t, key)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	call := receiveSaveCall(t, store)
	if len(call) != 1 || call[0].Revision != 1 {
		t.Fatalf("save call=%+v, want revision 1", call)
	}
	running.engine.Enqueue(sim.Command{
		Session: testSessionID, Sequence: 1, Kind: sim.CommandPlayerInput,
		Pitch: -1.5, Mining: true,
	})
	for range 4 {
		if primed := running.engine.Step(); len(primed.Changes) != 0 {
			t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
		}
	}
	changed := running.StepForTest()
	if len(changed.Changes) != 1 || changed.Changes[0].NewRevision != 2 {
		t.Fatalf("local change=%+v, want revision 2", changed.Changes)
	}
	local, revision, ready := running.engine.CloneReadyChunk(key)
	stored, err := memory.LoadChunk(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !ready || revision != 2 || local.Hash() == stored.Chunk.Hash() {
		t.Fatalf(
			"fixture local=(revision=%d ready=%v hash=%x) stored=(revision=%d hash=%x)",
			revision, ready, local.Hash(), stored.Revision, stored.Chunk.Hash(),
		)
	}

	running.autosaveActive = false
	running.config.AutosaveTicks = running.engine.TickCount() + 100
	store.gate <- struct{}{}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()

	if got := persistenceRevisionsForTest(t, running.engine, key); got.current != 2 ||
		got.persisted != 0 || got.inFlight != 0 {
		t.Fatalf("persistence revisions=%+v, want current=2 persisted=0 inFlight=0", got)
	}
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 0 {
		t.Fatalf("foreign committed content stats=%+v, want dirty retryable authority", got)
	}
	retry := running.engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(retry) != 1 || retry[0].Key != key || retry[0].Revision != 2 {
		t.Fatalf("retry snapshots=%+v, want key at revision 2", retry)
	}
	running.engine.FailPersistence(retry)
}

func TestFullSaveQueueReleasesUndispatchedSnapshots(t *testing.T) {
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	running := newPersistenceServer(t, store)

	running.saveJobs <- saveJob{Region: storage.RegionKey{X: -3}}
	receiveSaveCall(t, store)
	running.saveJobs <- saveJob{Region: storage.RegionKey{X: -2}}
	running.saveJobs <- saveJob{Region: storage.RegionKey{X: -1}}

	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1
	running.StepForTest()
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 0 {
		t.Fatalf("queue-full snapshot remained in flight: %+v", got)
	}
}

func TestSaveSelectionHonorsBudgetsAndAllowsOversizedFirst(t *testing.T) {
	tests := []struct {
		name      string
		maxChunks int
		maxBytes  int
	}{
		{name: "chunk budget", maxChunks: 1, maxBytes: 1 << 20},
		{name: "oversized first", maxChunks: 8, maxBytes: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newPersistenceTestStore()
			store.gate = make(chan struct{})
			running := newPersistenceServer(t, store)
			running.config.SaveChunks = test.maxChunks
			running.config.SaveBytes = test.maxBytes
			running.engine = dirtyReadyEngine(t, []core.ChunkKey{
				chunkKey(0, 0), chunkKey(1, 0),
			})
			running.config.AutosaveTicks = running.engine.TickCount() + 1

			running.StepForTest()
			call := receiveSaveCall(t, store)
			if got := len(call); got != 1 {
				t.Fatalf("selected %d chunks, want exactly one", got)
			}
		})
	}
}

func TestPersistenceConfigDefaultsAndValidation(t *testing.T) {
	config := DefaultConfig(42)
	if config.SaveWorkers != 2 || config.SaveChunks != 8 ||
		config.SaveBytes != 4<<20 || config.AutosaveTicks != 6000 ||
		config.RetryBaseTicks != 20 || config.RetryMaxTicks != 1200 ||
		config.UnsavedBytes != 512<<20 || config.ShutdownTimeout != 30*time.Second ||
		config.SaveObserver != nil {
		t.Fatalf("persistence defaults=%+v", config)
	}

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "save workers", mutate: func(c *Config) { c.SaveWorkers = 0 }},
		{name: "save chunks", mutate: func(c *Config) { c.SaveChunks = 0 }},
		{name: "save bytes", mutate: func(c *Config) { c.SaveBytes = 0 }},
		{name: "autosave ticks", mutate: func(c *Config) { c.AutosaveTicks = 0 }},
		{name: "retry base ticks", mutate: func(c *Config) { c.RetryBaseTicks = 0 }},
		{name: "retry max ticks", mutate: func(c *Config) { c.RetryMaxTicks = 0 }},
		{name: "retry max below base", mutate: func(c *Config) { c.RetryMaxTicks = c.RetryBaseTicks - 1 }},
		{name: "unsaved bytes", mutate: func(c *Config) { c.UnsavedBytes = 0 }},
		{name: "shutdown timeout", mutate: func(c *Config) { c.ShutdownTimeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := DefaultConfig(42)
			test.mutate(&invalid)
			assertPanicsPersistence(t, invalid.validate)
		})
	}
}

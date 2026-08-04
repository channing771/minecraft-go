package server

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

// emptyChunkEstimateBytes 是全空区块的存档估算：512 信封 + 32 个固定掉落物槽。
const emptyChunkEstimateBytes = 512 + core.DropsPerChunk*world.DropSlotBytes +
	core.FurnacesPerChunk*world.FurnaceSlotBytes

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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
		running.incoming <- incomingCommand{
			Session: testSessionID, Generation: 1,
			Command: sim.Command{
				Session:  testSessionID,
				Sequence: uint64(sequence + 1),
				Kind:     sim.CommandBreakBlock,
				Pitch:    -1.5,
			}}
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
		FormatVersion: 1, Seed: 42, SpawnDimension: core.Overworld,
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
	running.incoming <- incomingCommand{
		Session: testSessionID, Generation: 1,
		Command: sim.Command{
			Session: testSessionID, Sequence: 1,
			Kind: sim.CommandBreakBlock, Pitch: -1.5,
		},
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

func TestSaveFailureRetriesWithBoundedBackoffAndKeepsDirty(t *testing.T) {
	wantErr := errors.New("transient write")
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call < 2 {
			return storage.SaveResult{}, wantErr
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	if len(first) != 1 || first[0].Revision != 1 {
		t.Fatalf("first save=%+v, want revision 1", first)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)

	running.StepForTest()
	second := receiveSaveCall(t, store)
	if len(second) != 1 || second[0].Revision != 1 {
		t.Fatalf("retry save=%+v, want retained revision 1", second)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("first failure released retained ownership: %+v", got)
	}

	running.StepForTest()
	assertNoSaveStarted(t, store)
	if got := persistenceTestStoreCalls(store); got != 2 {
		t.Fatalf("retry ran before two-tick delay: calls=%d", got)
	}
	running.StepForTest()
	third := receiveSaveCall(t, store)
	if len(third) != 1 || third[0].Revision != 1 {
		t.Fatalf("second retry=%+v, want retained revision 1", third)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()

	status := running.PersistenceStatus()
	if got := persistenceTestStoreCalls(store); got != 3 {
		t.Fatalf("save calls=%d, want 3", got)
	}
	if status.DirtyChunks != 0 || status.InFlightChunks != 0 ||
		status.LastSuccess.IsZero() || status.LastError == "" ||
		status.LastErrorAt.IsZero() || !status.AutosaveDrained {
		t.Fatalf("status after retry success=%+v", status)
	}
}

func TestSaveFailureIntegrationBackoffCapsAtFourTicks(t *testing.T) {
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call < 4 {
			return storage.SaveResult{}, errors.New("keep retrying")
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	initial := running.StepForTest()
	call := receiveSaveCall(t, store)
	if len(call) != 1 || call[0].Revision != 1 {
		t.Fatalf("initial save=%+v, want revision 1", call)
	}
	lastDispatchTick := initial.Tick
	for _, wantDelay := range []uint64{1, 2, 4, 4} {
		waitSaveReturned(t, store)
		waitCompletionQueued(t, running)
		var dispatched sim.TickResult
		for elapsed := uint64(1); elapsed <= wantDelay; elapsed++ {
			dispatched = running.StepForTest()
			if elapsed < wantDelay {
				assertNoSaveStarted(t, store)
			}
		}
		call = receiveSaveCall(t, store)
		if len(call) != 1 || call[0].Revision != 1 {
			t.Fatalf("retry after %d ticks=%+v, want revision 1", wantDelay, call)
		}
		if got := dispatched.Tick - lastDispatchTick; got != wantDelay {
			t.Fatalf("dispatch delay=%d ticks, want %d", got, wantDelay)
		}
		lastDispatchTick = dispatched.Tick
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	if got := persistenceTestStoreCalls(store); got != 5 {
		t.Fatalf("save calls=%d, want initial plus four retries", got)
	}
}

func TestSaveFailurePartialCommitRetriesOnlyUncommitted(t *testing.T) {
	keys := []core.ChunkKey{chunkKey(0, 0), chunkKey(1, 0)}
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{Committed: map[core.ChunkKey]uint64{
				saves[0].Key: saves[0].Revision,
			}}, errors.New("partial region write")
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, keys)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	if got := saveKeys(first); !reflect.DeepEqual(got, keys) {
		t.Fatalf("first save keys=%+v, want %+v", got, keys)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	retry := receiveSaveCall(t, store)
	if got, want := saveKeys(retry), keys[1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("partial retry keys=%+v, want %+v", got, want)
	}
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("partial retry ownership=%+v, want one retained in-flight chunk", got)
	}
}

func TestSaveNilErrorOmissionRetainsSubmittedSnapshot(t *testing.T) {
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{}, nil
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	retry := receiveSaveCall(t, store)
	if len(first) != 1 || len(retry) != 1 || retry[0].Key != first[0].Key ||
		retry[0].Revision != first[0].Revision {
		t.Fatalf("omitted snapshot first=%+v retry=%+v", first, retry)
	}
	if status := running.PersistenceStatus(); status.InFlightChunks != 1 ||
		!strings.Contains(status.LastError, "omitted submitted chunks") {
		t.Fatalf("omission status=%+v", status)
	}
}

func TestRetryDelayDoublesCapsAndCannotOverflow(t *testing.T) {
	tests := []struct {
		name     string
		base     uint64
		maximum  uint64
		attempts uint32
		want     uint64
	}{
		{name: "attempt one", base: 1, maximum: 4, attempts: 1, want: 1},
		{name: "attempt two", base: 1, maximum: 4, attempts: 2, want: 2},
		{name: "capped attempt three", base: 3, maximum: 4, attempts: 3, want: 4},
		{name: "overflow safe", base: ^uint64(0)/2 + 1, maximum: ^uint64(0), attempts: 2, want: ^uint64(0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := retryDelay(test.base, test.maximum, test.attempts); got != test.want {
				t.Fatalf("retryDelay(%d,%d,%d)=%d, want %d", test.base, test.maximum, test.attempts, got, test.want)
			}
		})
	}
}

func TestDueRetryQueueFullKeepsAttemptAndSnapshot(t *testing.T) {
	key := chunkKey(2, 3)
	region, _ := storage.RegionFor(key)
	retained := retrySave{
		Job: saveJob{Region: region, Snapshots: []sim.ChunkSaveSnapshot{{
			Key: key, Revision: 7, Chunk: world.NewChunk(key.Pos),
		}}, Retry: true, RetryID: 1},
		Attempts: 2,
		NextTick: 5,
	}
	running := &Server{
		saveJobs:      make(chan saveJob, 1),
		retry:         map[storage.RegionKey][]retrySave{region: []retrySave{retained}},
		retryInFlight: make(map[uint64]retrySave),
		nextRetryID:   1,
	}
	running.saveJobs <- saveJob{Region: storage.RegionKey{X: -99}}

	running.dispatchDueRetries(5)
	got := running.retry[region]
	if len(got) != 1 || got[0].Attempts != 2 || got[0].NextTick != 5 || len(got[0].Job.Snapshots) != 1 ||
		len(running.retryInFlight) != 0 || len(running.saveJobs) != 1 {
		t.Fatalf("queue-full retry changed or disappeared: retry=%+v inFlight=%+v queued=%d", got, running.retryInFlight, len(running.saveJobs))
	}
}

func TestDueRetryIsQueuedBeforeFreshAutosaveSnapshot(t *testing.T) {
	oldKey, freshKey := chunkKey(0, 0), chunkKey(64, 0)
	engine := dirtyReadyEngine(t, []core.ChunkKey{oldKey, freshKey})
	oldSnapshot := engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(oldSnapshot) != 1 || oldSnapshot[0].Key != oldKey {
		t.Fatalf("old snapshot=%+v, want first region key", oldSnapshot)
	}
	region, _ := storage.RegionFor(oldKey)
	config := DefaultConfig(42)
	config.AutosaveTicks = 1
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 1),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
		autosaveActive:  true,
		saveCompletions: make(chan saveCompletion, 1),
	}
	running.retry[region] = []retrySave{{
		Job:       saveJob{Region: region, Snapshots: oldSnapshot, Retry: true, RetryID: 1},
		Attempts:  1,
		NextTick:  engine.TickCount(),
		LastError: errors.New("old failed save"),
	}}
	running.nextRetryID = 1

	running.schedulePersistence(engine.TickCount())
	queued := <-running.saveJobs
	if !queued.Retry || queued.Attempt != 2 || len(queued.Snapshots) != 1 ||
		queued.Snapshots[0].Key != oldKey {
		t.Fatalf("first queued save=%+v, want due retry", queued)
	}
	if got := engine.PersistenceStats(); got.InFlightChunks != 1 || got.DirtyChunks != 2 {
		t.Fatalf("fresh queue-full selection was not released: %+v", got)
	}
}

func TestPersistenceBackpressureHysteresisBoundary(t *testing.T) {
	if !nextPersistenceBackpressure(false, 100, 100) {
		t.Fatal("estimated bytes at cap did not enter backpressure")
	}
	if !nextPersistenceBackpressure(true, 90, 100) {
		t.Fatal("estimated bytes at 90 percent cleared backpressure")
	}
	if nextPersistenceBackpressure(true, 89, 100) {
		t.Fatal("estimated bytes below 90 percent did not clear backpressure")
	}
	if nextPersistenceBackpressure(true, 90, 101) {
		t.Fatal("integer bytes below an exact 90 percent fraction did not clear backpressure")
	}
	if !nextPersistenceBackpressure(true, 91, 101) {
		t.Fatal("integer bytes above an exact 90 percent fraction cleared backpressure")
	}
}

func TestPersistenceStatusReturnsCopiedCurrentState(t *testing.T) {
	running := newPersistenceServer(t, newPersistenceTestStore())
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.lastSaveError = "original failure"
	running.lastSaveErrorAt = time.Unix(123, 0)
	running.backpressured = true

	status := running.PersistenceStatus()
	if status.DirtyChunks != 1 || status.EstimatedBytes != emptyChunkEstimateBytes ||
		status.InFlightChunks != 0 || !status.Backpressured ||
		status.LastError != "original failure" ||
		!status.LastErrorAt.Equal(time.Unix(123, 0)) || status.AutosaveDrained {
		t.Fatalf("persistence status=%+v", status)
	}
	status.LastError = "caller mutation"
	status.LastErrorAt = time.Time{}
	got := running.PersistenceStatus()
	if got.LastError != "original failure" || !got.LastErrorAt.Equal(time.Unix(123, 0)) {
		t.Fatalf("caller mutated server status: %+v", got)
	}
}

func TestMutationDuringRetrySelectsNewRevisionOnceAfterOldCommit(t *testing.T) {
	key := chunkKey(0, 0)
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{}, errors.New("retry old revision")
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 2
	running.config.RetryMaxTicks = 4
	running.engine = dirtyPlayerEngine(t, key)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	if len(first) != 1 || first[0].Revision != 1 {
		t.Fatalf("initial save=%+v, want revision 1", first)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.incoming <- incomingCommand{
		Session: testSessionID, Generation: 1,
		Command: sim.Command{
			Session: testSessionID, Sequence: 1,
			Kind: sim.CommandBreakBlock, Pitch: -1.5,
		},
	}
	changed := running.StepForTest()
	if len(changed.Changes) != 1 || changed.Changes[0].NewRevision != 2 {
		t.Fatalf("mutation=%+v, want revision 2", changed.Changes)
	}
	assertNoSaveStarted(t, store)
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("pending retry allowed duplicate selection: %+v", got)
	}

	running.StepForTest()
	retry := receiveSaveCall(t, store)
	if len(retry) != 1 || retry[0].Revision != 1 {
		t.Fatalf("retry=%+v, want old revision 1", retry)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	newer := receiveSaveCall(t, store)
	if len(newer) != 1 || newer[0].Revision != 2 {
		t.Fatalf("post-retry save=%+v, want new revision 2", newer)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	assertNoSaveStarted(t, store)
	if got := persistenceTestStoreCalls(store); got != 3 {
		t.Fatalf("new revision selected more than once: calls=%d", got)
	}
}

func TestSameRegionRetryCoalescingSortsAndKeepsOneClonePerKey(t *testing.T) {
	key0, key1, key2 := chunkKey(0, 0), chunkKey(1, 0), chunkKey(2, 0)
	firstKey2 := world.NewChunk(key2.Pos)
	replacementKey1 := world.NewChunk(key1.Pos)
	merged := mergeRetrySnapshots(
		[]sim.ChunkSaveSnapshot{
			{Key: key2, Revision: 7, Chunk: firstKey2},
			{Key: key1, Revision: 7, Chunk: world.NewChunk(key1.Pos)},
		},
		[]sim.ChunkSaveSnapshot{
			{Key: key0, Revision: 7, Chunk: world.NewChunk(key0.Pos)},
			{Key: key2, Revision: 7, Chunk: world.NewChunk(key2.Pos)},
			{Key: key1, Revision: 8, Chunk: replacementKey1},
		},
	)
	if got, want := snapshotKeys(merged), []core.ChunkKey{key0, key1, key2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("coalesced keys=%+v, want %+v", got, want)
	}
	if merged[1].Revision != 8 || merged[1].Chunk != replacementKey1 ||
		merged[2].Revision != 7 || merged[2].Chunk != firstKey2 {
		t.Fatalf("coalesced clones/revisions changed unexpectedly: %+v", merged)
	}
}

func TestSameRegionFreshFailureDoesNotInheritInflightRetryAttempts(t *testing.T) {
	keyA, keyB := chunkKey(0, 0), chunkKey(1, 0)
	engine := dirtyReadyEngine(t, []core.ChunkKey{keyA, keyB})
	snapshots := engine.PersistenceSnapshots(2, 1<<20, sim.SaveAll)
	if got := snapshotKeys(snapshots); !reflect.DeepEqual(got, []core.ChunkKey{keyA, keyB}) {
		t.Fatalf("snapshots=%+v, want A then B", got)
	}
	region, _ := storage.RegionFor(keyA)
	config := DefaultConfig(42)
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 256
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 4),
		saveCompletions: make(chan saveCompletion, 4),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
	}

	running.retainFailedSave(saveJob{
		Region: region, Snapshots: snapshots[:1], Attempt: 9, Retry: true,
	}, snapshots[:1], errors.New("A attempt 9 failed"))
	running.dispatchDueRetries(saturatingAddUint64(engine.TickCount(), 256))
	attemptA := <-running.saveJobs
	if attemptA.Attempt != 10 || len(attemptA.Snapshots) != 1 || attemptA.Snapshots[0].Key != keyA {
		t.Fatalf("A retry=%+v, want attempt 10", attemptA)
	}

	running.saveCompletions <- saveCompletion{
		Job: saveJob{Region: region, Snapshots: snapshots[1:], Attempt: 1},
		Err: errors.New("fresh B failed"),
	}
	running.saveCompletions <- saveCompletion{
		Job:    attemptA,
		Result: storage.SaveResult{Committed: map[core.ChunkKey]uint64{keyA: snapshots[0].Revision}},
	}
	running.drainSaveCompletions()
	running.dispatchDueRetries(engine.TickCount() + 1)

	retryB := <-running.saveJobs
	if retryB.Attempt != 2 || len(retryB.Snapshots) != 1 || retryB.Snapshots[0].Key != keyB {
		t.Fatalf("B retry=%+v, want independent attempt 2", retryB)
	}
}

func TestSameRegionFreshFailureDoesNotAdvanceOlderRetryDeadline(t *testing.T) {
	keyA, keyB := chunkKey(0, 0), chunkKey(1, 0)
	engine := dirtyReadyEngine(t, []core.ChunkKey{keyA, keyB})
	snapshots := engine.PersistenceSnapshots(2, 1<<20, sim.SaveAll)
	region, _ := storage.RegionFor(keyA)
	config := DefaultConfig(42)
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 256
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 4),
		saveCompletions: make(chan saveCompletion, 4),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
	}

	running.retainFailedSave(saveJob{
		Region: region, Snapshots: snapshots[:1], Attempt: 9, Retry: true,
	}, snapshots[:1], errors.New("A remains far in the future"))
	running.saveCompletions <- saveCompletion{
		Job: saveJob{Region: region, Snapshots: snapshots[1:], Attempt: 1},
		Err: errors.New("fresh B failed"),
	}
	running.drainSaveCompletions()
	running.dispatchDueRetries(engine.TickCount() + 1)

	retryB := <-running.saveJobs
	if retryB.Attempt != 2 || len(retryB.Snapshots) != 1 || retryB.Snapshots[0].Key != keyB {
		t.Fatalf("early retry=%+v, want only fresh B at attempt 2", retryB)
	}
	select {
	case extra := <-running.saveJobs:
		t.Fatalf("older A deadline was advanced: %+v", extra)
	default:
	}
	running.dispatchDueRetries(saturatingAddUint64(engine.TickCount(), 256))
	retryA := <-running.saveJobs
	if retryA.Attempt != 10 || len(retryA.Snapshots) != 1 || retryA.Snapshots[0].Key != keyA {
		t.Fatalf("late retry=%+v, want only A at attempt 10", retryA)
	}
}

func TestOldestDueRetryPreventsFixedRegionStarvation(t *testing.T) {
	config := DefaultConfig(42)
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 1
	engine := sim.NewEngine(0)
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 3),
		saveCompletions: make(chan saveCompletion, 3),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
	}
	keys := []core.ChunkKey{
		chunkKey(0, 0), chunkKey(32, 0), chunkKey(64, 0), chunkKey(96, 0),
	}
	for _, key := range keys {
		region, _ := storage.RegionFor(key)
		snapshot := sim.ChunkSaveSnapshot{Key: key, Revision: 1, Chunk: world.NewChunk(key.Pos)}
		running.retainFailedSave(
			saveJob{Region: region, Snapshots: []sim.ChunkSaveSnapshot{snapshot}, Attempt: 1},
			[]sim.ChunkSaveSnapshot{snapshot},
			errors.New("initial failure"),
		)
	}

	running.dispatchDueRetries(1)
	firstRound := make([]saveJob, 3)
	for index := range firstRound {
		firstRound[index] = <-running.saveJobs
	}
	if got, want := saveJobKeys(firstRound), keys[:3]; !reflect.DeepEqual(got, want) {
		t.Fatalf("first round=%+v, want %+v", got, want)
	}
	engine.Step()
	for _, job := range firstRound {
		running.saveCompletions <- saveCompletion{Job: job, Err: errors.New("retry failed")}
	}
	running.drainSaveCompletions()
	running.dispatchDueRetries(2)
	secondRound := make([]saveJob, 3)
	for index := range secondRound {
		secondRound[index] = <-running.saveJobs
	}
	if got := saveJobKeys(secondRound); !containsChunkKey(got, keys[3]) {
		t.Fatalf("oldest due region D starved in second round: %+v", got)
	}
}

func TestPersistenceBackpressureQueuesAcquireUntilMemoryRecovers(t *testing.T) {
	store := &blockingLoadStore{
		metadata: storage.Metadata{FormatVersion: 1, Seed: 42},
		started:  make(chan core.ChunkKey, 1),
	}
	_, endpoint := network.NewMemoryPair(64)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.TrustedObserver = true
	config.UnsavedBytes = 512
	running := newAttachedWorldForTest(config, endpoint, &countingGenerator{}, store)
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	heldKey := chunkKey(0, 0)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{heldKey})
	running.engine.RegisterObserverSession(trustedObserverSessionID)
	running.engine.Enqueue(sim.Command{
		Session: trustedObserverSessionID, Sequence: 1,
		Kind:      sim.CommandTrustedObserverCenter,
		Dimension: heldKey.Dimension, Center: heldKey.Pos,
	})
	running.engine.Step()
	running.config.AutosaveTicks = running.engine.TickCount() + 100
	running.trustedObserverSequence = 1
	target := core.ChunkPos{X: 20, Z: -20}
	if err := running.SetTrustedObserverCenter(core.Overworld, target); err != nil {
		t.Fatal(err)
	}

	result := running.StepForTest()
	wantAcquire := core.ChunkKey{Dimension: core.Overworld, Pos: target}
	if !reflect.DeepEqual(result.Acquire, []core.ChunkKey{wantAcquire}) {
		t.Fatalf("Acquire=%+v, want queued %+v", result.Acquire, wantAcquire)
	}
	select {
	case started := <-store.started:
		t.Fatalf("backpressured acquisition dispatched %+v", started)
	default:
	}
	info, exists := running.engine.ChunkInfo(wantAcquire)
	if !exists || info.State != sim.ChunkLoading || len(running.pending) != 1 {
		t.Fatalf("queued unknown chunk state=%+v exists=%v pending=%+v", info, exists, running.pending)
	}
	status := running.PersistenceStatus()
	if !status.Backpressured || status.DirtyChunks != 1 || status.EstimatedBytes != emptyChunkEstimateBytes ||
		status.InFlightChunks != 0 || status.AutosaveDrained {
		t.Fatalf("backpressure status=%+v", status)
	}

	selected := running.engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(selected) != 1 || selected[0].Key != heldKey {
		t.Fatalf("cleanup snapshot=%+v, want held key", selected)
	}
	running.engine.ApplyPersisted([]sim.PersistedChunk{{Key: heldKey, Revision: selected[0].Revision}})
	running.StepForTest()
	select {
	case started := <-store.started:
		if started != wantAcquire {
			t.Fatalf("resumed load=%+v, want %+v", started, wantAcquire)
		}
	case <-time.After(time.Second):
		t.Fatal("acquisition did not resume below hysteresis threshold")
	}
	if resumed := running.PersistenceStatus(); resumed.Backpressured || !resumed.AutosaveDrained {
		t.Fatalf("resumed status=%+v", resumed)
	}
}

type persistenceTestStore struct {
	metadata storage.Metadata
	started  chan []storage.ChunkSave
	returned chan struct{}
	canceled chan struct{}
	gate     chan struct{}
	respond  func(int, []storage.ChunkSave) (storage.SaveResult, error)

	mu         sync.Mutex
	calls      int
	syncCalls  int
	closeCalls int
}

func newPersistenceTestStore() *persistenceTestStore {
	return &persistenceTestStore{
		metadata: storage.Metadata{
			FormatVersion:  1,
			Seed:           42,
			SpawnDimension: core.Overworld,
		},
		started:  make(chan []storage.ChunkSave, 16),
		returned: make(chan struct{}, 16),
		canceled: make(chan struct{}, 16),
	}
}

func (store *persistenceTestStore) Metadata() storage.Metadata { return store.metadata }

func (*persistenceTestStore) LoadChunk(context.Context, core.ChunkKey) (storage.StoredChunk, error) {
	return storage.StoredChunk{}, storage.ErrChunkNotFound
}

func (store *persistenceTestStore) SaveBatch(
	ctx context.Context,
	saves []storage.ChunkSave,
) (storage.SaveResult, error) {
	copied := append([]storage.ChunkSave(nil), saves...)
	select {
	case store.started <- copied:
	case <-ctx.Done():
		return storage.SaveResult{}, ctx.Err()
	}
	store.mu.Lock()
	gate := store.gate
	store.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			select {
			case store.canceled <- struct{}{}:
			default:
			}
			return storage.SaveResult{}, ctx.Err()
		}
	}
	store.mu.Lock()
	call := store.calls
	store.calls++
	respond := store.respond
	store.mu.Unlock()
	var result storage.SaveResult
	var err error
	if respond != nil {
		result, err = respond(call, copied)
	} else {
		result = committedResult(copied)
	}
	select {
	case store.returned <- struct{}{}:
	default:
	}
	return result, err
}

func (store *persistenceTestStore) Sync(context.Context) error {
	store.mu.Lock()
	store.syncCalls++
	store.mu.Unlock()
	return nil
}

func (store *persistenceTestStore) Close() error {
	store.mu.Lock()
	store.closeCalls++
	store.mu.Unlock()
	return nil
}

func newPersistenceServer(t *testing.T, store storage.Store) *Server {
	t.Helper()
	running := newPersistenceServerWithoutCleanup(t, store)
	t.Cleanup(func() {
		if testStore, ok := store.(*persistenceTestStore); ok {
			testStore.recoverForShutdownCleanup()
		}
		shutdownServerForTest(t, running)
	})
	return running
}

func (store *persistenceTestStore) recoverForShutdownCleanup() {
	store.mu.Lock()
	gate := store.gate
	store.gate = nil
	store.respond = nil
	store.mu.Unlock()
	if gate != nil {
		close(gate)
	}
}

func newPersistenceServerWithoutCleanup(t *testing.T, store storage.Store) *Server {
	t.Helper()
	_, endpoint := network.NewMemoryPair(64)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.SaveChunks = 8
	config.SaveBytes = 1 << 20
	config.AutosaveTicks = 6000
	return newAttachedWorldForTest(config, endpoint, playerTestGenerator{}, store)
}

func dirtyReadyEngine(t *testing.T, keys []core.ChunkKey) *sim.Engine {
	t.Helper()
	engine := sim.NewEngine(0)
	for index, key := range keys {
		session := sim.SessionID(index + 1)
		engine.RegisterObserverSession(session)
		engine.Enqueue(sim.Command{
			Session: session, Sequence: 1,
			Kind: sim.CommandTrustedObserverCenter, Dimension: key.Dimension, Center: key.Pos,
		})
		requested := engine.Step()
		if !reflect.DeepEqual(requested.Acquire, []core.ChunkKey{key}) {
			t.Fatalf("Acquire=%+v, want %+v", requested.Acquire, []core.ChunkKey{key})
		}
		engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
		generated := engine.Step()
		if !reflect.DeepEqual(generated.Generate, []core.ChunkKey{key}) {
			t.Fatalf("Generate=%+v, want %+v", generated.Generate, []core.ChunkKey{key})
		}
		engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     world.NewChunk(key.Pos),
		})
		ready := engine.Step()
		if !reflect.DeepEqual(ready.Ready, []core.ChunkKey{key}) {
			t.Fatalf("Ready=%+v, want %+v", ready.Ready, []core.ChunkKey{key})
		}
	}
	return engine
}

func dirtyUnloadingEngine(t *testing.T, key core.ChunkKey) *sim.Engine {
	t.Helper()
	engine := dirtyReadyEngine(t, []core.ChunkKey{key})
	engine.Enqueue(sim.Command{
		Session: 1, Sequence: 2,
		Kind: sim.CommandTrustedObserverCenter, Dimension: key.Dimension,
		Center: core.ChunkPos{X: key.Pos.X + 100, Z: key.Pos.Z + 100},
	})
	engine.Step()
	info, ok := engine.ChunkInfo(key)
	if !ok || info.State != sim.ChunkUnloading {
		t.Fatalf("chunk state=%+v, want Unloading", info)
	}
	return engine
}

func dirtyPlayerEngine(t *testing.T, key core.ChunkKey) *sim.Engine {
	t.Helper()
	engine := sim.NewEngine(0)
	engine.RegisterSession(testSessionID, key.Dimension, key.Pos)
	requested := engine.Step()
	if !reflect.DeepEqual(requested.Acquire, []core.ChunkKey{key}) {
		t.Fatalf("Acquire=%+v, want %+v", requested.Acquire, []core.ChunkKey{key})
	}
	engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	generated := engine.Step()
	if !reflect.DeepEqual(generated.Generate, []core.ChunkKey{key}) {
		t.Fatalf("Generate=%+v, want %+v", generated.Generate, []core.ChunkKey{key})
	}
	engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: key.Dimension,
		Pos:       key.Pos,
		Chunk:     (&gatedGenerator{flat: true}).chunk(key.Pos),
	})
	ready := engine.Step()
	if len(ready.Ready) != 1 || len(ready.Players) != 1 || !ready.Players[0].Ready {
		t.Fatalf("ready tick=%+v, want one ready chunk and player", ready)
	}
	return engine
}

type persistenceRevisions struct {
	current, persisted, inFlight uint64
}

func persistenceRevisionsForTest(
	t *testing.T,
	engine *sim.Engine,
	key core.ChunkKey,
) persistenceRevisions {
	t.Helper()
	dimensions := reflect.ValueOf(engine).Elem().FieldByName("dimensions")
	dimension := dimensions.MapIndex(reflect.ValueOf(key.Dimension))
	if !dimension.IsValid() || dimension.IsNil() {
		t.Fatalf("dimension %d missing", key.Dimension)
	}
	records := dimension.Elem().FieldByName("records")
	record := records.MapIndex(reflect.ValueOf(key.Pos))
	if !record.IsValid() || record.IsNil() {
		t.Fatalf("chunk %+v missing", key)
	}
	value := record.Elem()
	return persistenceRevisions{
		current:   value.FieldByName("Revision").Uint(),
		persisted: value.FieldByName("PersistedRevision").Uint(),
		inFlight:  value.FieldByName("SaveInFlightRevision").Uint(),
	}
}

func receiveSaveCall(t *testing.T, store *persistenceTestStore) []storage.ChunkSave {
	t.Helper()
	select {
	case call := <-store.started:
		return call
	case <-time.After(time.Second):
		t.Fatal("Store.SaveBatch did not start")
		return nil
	}
}

func assertNoSaveStarted(t *testing.T, store *persistenceTestStore) {
	t.Helper()
	select {
	case call := <-store.started:
		t.Fatalf("unexpected save call: %+v", saveKeys(call))
	default:
	}
}

func waitSaveReturned(t *testing.T, store *persistenceTestStore) {
	t.Helper()
	select {
	case <-store.returned:
	case <-time.After(time.Second):
		t.Fatal("Store.SaveBatch did not return")
	}
}

func waitCompletionQueued(t *testing.T, running *Server) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for len(running.saveCompletions) == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(running.saveCompletions) == 0 {
		t.Fatal("save completion was not queued")
	}
}

func persistenceTestStoreCalls(store *persistenceTestStore) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.calls
}

func committedResult(saves []storage.ChunkSave) storage.SaveResult {
	committed := make(map[core.ChunkKey]uint64, len(saves))
	for _, save := range saves {
		committed[save.Key] = save.Revision
	}
	return storage.SaveResult{Committed: committed}
}

func chunkKey(x, z int32) core.ChunkKey {
	return core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: x, Z: z}}
}

func saveKeys(saves []storage.ChunkSave) []core.ChunkKey {
	keys := make([]core.ChunkKey, len(saves))
	for index, save := range saves {
		keys[index] = save.Key
	}
	return keys
}

func saveJobKeys(jobs []saveJob) []core.ChunkKey {
	keys := make([]core.ChunkKey, 0, len(jobs))
	for _, job := range jobs {
		for _, snapshot := range job.Snapshots {
			keys = append(keys, snapshot.Key)
		}
	}
	return keys
}

func containsChunkKey(keys []core.ChunkKey, want core.ChunkKey) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}

func snapshotKeys(snapshots []sim.ChunkSaveSnapshot) []core.ChunkKey {
	keys := make([]core.ChunkKey, len(snapshots))
	for index, snapshot := range snapshots {
		keys[index] = snapshot.Key
	}
	return keys
}

func assertPanicsPersistence(t *testing.T, action func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("operation did not panic")
		}
	}()
	action()
}

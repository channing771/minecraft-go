package server

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
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

func TestSaveErrorAcknowledgesOnlyCommittedAndReleasesUncommitted(t *testing.T) {
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

	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 0 {
		t.Fatalf("partial failure stats=%+v, want one dirty retryable chunk", got)
	}
	retry := running.engine.PersistenceSnapshots(2, 1<<20, sim.SaveAll)
	if got, want := snapshotKeys(retry), keys[1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry snapshots=%+v, want %+v", got, want)
	}
	running.engine.FailPersistence(retry)
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
		running.incoming <- sim.Command{
			Session:  localSessionID,
			Sequence: uint64(sequence + 1),
			Kind:     sim.CommandBreakBlock,
			Pitch:    -1.5,
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
	running.incoming <- sim.Command{
		Session: localSessionID, Sequence: 1,
		Kind: sim.CommandBreakBlock, Pitch: -1.5,
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

func TestCloseCancelsAndWaitsForSaveWorkersWithoutFinalStoreLifecycle(t *testing.T) {
	store := newPersistenceTestStore()
	store.gate = make(chan struct{})
	running := newPersistenceServerWithoutCleanup(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1
	running.StepForTest()
	receiveSaveCall(t, store)

	closed := make(chan struct{})
	go func() {
		running.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for canceled save worker to exit")
	}
	select {
	case <-store.canceled:
	default:
		t.Fatal("save worker did not cancel gated SaveBatch")
	}
	store.mu.Lock()
	syncCalls, closeCalls := store.syncCalls, store.closeCalls
	store.mu.Unlock()
	if syncCalls != 0 || closeCalls != 0 {
		t.Fatalf("Task 13 Close touched store lifecycle: Sync=%d Close=%d", syncCalls, closeCalls)
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
	if store.gate != nil {
		select {
		case <-store.gate:
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
	t.Cleanup(running.Close)
	return running
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
	config.TrustedObserver = true
	return New(config, endpoint, playerTestGenerator{}, store)
}

func dirtyReadyEngine(t *testing.T, keys []core.ChunkKey) *sim.Engine {
	t.Helper()
	engine := sim.NewEngine(0)
	for index, key := range keys {
		session := sim.SessionID(index + 1)
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
	engine.RegisterSession(localSessionID, key.Dimension, key.Pos)
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

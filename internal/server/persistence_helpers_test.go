package server

import (
	"context"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

// emptyChunkEstimateBytes 是全空区块的存档估算：512 信封 + 32 个固定掉落物槽 +
// 32 个固定熔炉槽 + 16 个固定箱子槽。
const emptyChunkEstimateBytes = 512 + core.DropsPerChunk*world.DropSlotBytes +
	core.FurnacesPerChunk*world.FurnaceSlotBytes + core.ChestsPerChunk*world.ChestSlotBytes

type persistenceTestStore struct {
	metadata storage.Metadata
	started  chan []storage.ChunkSave
	returned chan struct{}
	canceled chan struct{}
	gate     chan struct{}
	respond  func(int, []storage.ChunkSave) (storage.SaveResult, error)

	mu              sync.Mutex
	calls           int
	syncCalls       int
	closeCalls      int
	metadataSaves   []storage.Metadata
	metadataRespond func(int, storage.Metadata) error
}

func newPersistenceTestStore() *persistenceTestStore {
	return &persistenceTestStore{
		metadata: storage.Metadata{
			FormatVersion:  2,
			Seed:           42,
			SpawnDimension: core.Overworld,
		},
		started:  make(chan []storage.ChunkSave, 16),
		returned: make(chan struct{}, 16),
		canceled: make(chan struct{}, 16),
	}
}

func (store *persistenceTestStore) Metadata() storage.Metadata { return store.metadata }

// SaveMetadata 记录每次 metadata 提交，供世界时间保存测试断言。
func (store *persistenceTestStore) SaveMetadata(_ context.Context, metadata storage.Metadata) error {
	store.mu.Lock()
	call := len(store.metadataSaves)
	store.metadataSaves = append(store.metadataSaves, metadata)
	respond := store.metadataRespond
	store.mu.Unlock()
	if respond != nil {
		return respond(call, metadata)
	}
	return nil
}

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
	engine := sim.NewEngine(0, 0)
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
	engine := sim.NewEngine(0, 0)
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
	case <-time.After(waitDeadline):
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
	case <-time.After(waitDeadline):
		t.Fatal("Store.SaveBatch did not return")
	}
}

func waitCompletionQueued(t *testing.T, running *Server) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
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

package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

type playerPrepareResult struct {
	restore sim.PlayerRestore
	err     error
}

type controllablePlayerStore struct {
	mu                    sync.Mutex
	loaded                map[core.PlayerID]storage.StoredPlayer
	loadStarted           chan core.PlayerID
	loadBlocks            map[core.PlayerID]chan struct{}
	loadCalls             map[core.PlayerID]int
	saveStarted           chan storage.PlayerSave
	saveResults           chan error
	saveResultRevision    uint64
	hasSaveResultRevision bool
	mutateNextSaveInput   bool
	onSave                func()
}

func newControllablePlayerStore() *controllablePlayerStore {
	return &controllablePlayerStore{
		loaded:      make(map[core.PlayerID]storage.StoredPlayer),
		loadStarted: make(chan core.PlayerID, 16),
		loadBlocks:  make(map[core.PlayerID]chan struct{}),
		loadCalls:   make(map[core.PlayerID]int),
		saveStarted: make(chan storage.PlayerSave, 16),
		saveResults: make(chan error),
	}
}

func (store *controllablePlayerStore) LoadPlayer(
	ctx context.Context,
	id core.PlayerID,
) (storage.StoredPlayer, error) {
	if err := ctx.Err(); err != nil {
		return storage.StoredPlayer{}, err
	}
	store.mu.Lock()
	block := store.loadBlocks[id]
	store.loadCalls[id]++
	store.mu.Unlock()
	if block != nil {
		select {
		case store.loadStarted <- id:
		case <-ctx.Done():
			return storage.StoredPlayer{}, ctx.Err()
		}
		select {
		case <-block:
		case <-ctx.Done():
			return storage.StoredPlayer{}, ctx.Err()
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	stored, ok := store.loaded[id]
	if !ok {
		return storage.StoredPlayer{}, storage.ErrPlayerNotFound
	}
	return stored, nil
}

func (store *controllablePlayerStore) SavePlayer(
	ctx context.Context,
	save storage.PlayerSave,
) (uint64, error) {
	store.mu.Lock()
	mutate := store.mutateNextSaveInput
	store.mutateNextSaveInput = false
	store.mu.Unlock()
	if mutate && save.Safe != nil {
		save.Safe.Position[0] = 999
	}
	copy := clonePlayerSaveForTest(save)
	select {
	case store.saveStarted <- copy:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	store.mu.Lock()
	onSave := store.onSave
	store.mu.Unlock()
	if onSave != nil {
		onSave()
	}
	select {
	case err := <-store.saveResults:
		store.mu.Lock()
		revision := copy.Revision
		if store.hasSaveResultRevision {
			revision = store.saveResultRevision
			store.hasSaveResultRevision = false
		}
		store.mu.Unlock()
		if err != nil {
			return 0, err
		}
		return revision, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (store *controllablePlayerStore) complete(err error) {
	store.saveResults <- err
}

func (store *controllablePlayerStore) completeWithRevision(revision uint64, err error) {
	store.mu.Lock()
	store.saveResultRevision = revision
	store.hasSaveResultRevision = true
	store.mu.Unlock()
	store.complete(err)
}

func (store *controllablePlayerStore) mutateNextSave() {
	store.mu.Lock()
	store.mutateNextSaveInput = true
	store.mu.Unlock()
}

func (store *controllablePlayerStore) setOnSave(onSave func()) {
	store.mu.Lock()
	store.onSave = onSave
	store.mu.Unlock()
}

func (store *controllablePlayerStore) blockLoad(id core.PlayerID) chan struct{} {
	store.mu.Lock()
	defer store.mu.Unlock()
	release := make(chan struct{})
	store.loadBlocks[id] = release
	return release
}

func (store *controllablePlayerStore) loadCallCount(id core.PlayerID) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadCalls[id]
}

func playerPersistenceTestConfig() Config {
	config := DefaultConfig(42)
	config.AutosaveTicks = 6000
	config.RetryBaseTicks = 20
	config.RetryMaxTicks = 1200
	return config
}

func playerID(value byte) core.PlayerID {
	return core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, value}
}

func testMetadata() storage.Metadata {
	return storage.Metadata{
		FormatVersion:  2,
		Seed:           42,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{X: 2, Z: -3},
	}
}

func testPlayerSnapshot(position float32) sim.PlayerSnapshot {
	safe := sim.PlayerLocation{
		Dimension: core.Overworld,
		Position:  mgl32.Vec3{position - 1, 64, -position},
	}
	return sim.PlayerSnapshot{
		Current: sim.PlayerLocation{
			Dimension: core.Overworld,
			Position:  mgl32.Vec3{position, 70, -position},
		},
		Yaw:   position / 10,
		Pitch: -position / 20,
		Safe:  &safe,
	}
}

func assertNoPlayerSaveStarted(t *testing.T, store *controllablePlayerStore) {
	t.Helper()
	select {
	case save := <-store.saveStarted:
		t.Fatalf("unexpected SavePlayer(%+v)", save)
	case <-time.After(50 * time.Millisecond):
	}
}

func receivePlayerSave(t *testing.T, store *controllablePlayerStore) storage.PlayerSave {
	t.Helper()
	select {
	case save := <-store.saveStarted:
		return save
	case <-time.After(waitDeadline):
		t.Fatal("SavePlayer was not started")
		return storage.PlayerSave{}
	}
}

func assertPlayerSavesContainIDs(
	t *testing.T,
	saves []storage.PlayerSave,
	want ...core.PlayerID,
) {
	t.Helper()
	if len(saves) != len(want) {
		t.Fatalf("SavePlayer starts=%d, want %d", len(saves), len(want))
	}
	seen := make(map[core.PlayerID]bool, len(saves))
	for _, save := range saves {
		seen[save.PlayerID] = true
	}
	for _, id := range want {
		if !seen[id] {
			t.Fatalf("SavePlayer starts=%+v, missing PlayerID %s", saves, id)
		}
	}
}

func playerSaveForID(
	t *testing.T,
	saves []storage.PlayerSave,
	id core.PlayerID,
) storage.PlayerSave {
	t.Helper()
	for _, save := range saves {
		if save.PlayerID == id {
			return save
		}
	}
	t.Fatalf("SavePlayer starts=%+v, missing PlayerID %s", saves, id)
	return storage.PlayerSave{}
}

func blockPlayerSaveWorkers(
	t *testing.T,
	p *playerPersistence,
	store *concurrentPlayerSaveStore,
) {
	t.Helper()
	for _, value := range []byte{250, 251} {
		p.jobs <- schedulerTestSaveJob(playerID(value), 1, float32(value))
	}
	started := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	assertPlayerSavesContainIDs(t, started, playerID(250), playerID(251))
}

func newTwoInflightPlayerPersistence(
	t *testing.T,
) (*playerPersistence, *concurrentPlayerSaveStore) {
	t.Helper()
	store := newConcurrentPlayerSaveStore()
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	for _, value := range []byte{2, 1} {
		id := playerID(value)
		name := string(rune('A' + value))
		store.put(storedPlayerForTest(id, 7, name, testPlayerSnapshot(float32(value))))
		if _, err := p.Prepare(context.Background(), id, name, testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(id, name, testPlayerSnapshot(10), 0, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	started := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	assertPlayerSavesContainIDs(t, started, playerID(1), playerID(2))
	return p, store
}

func assertQueuedPlayerSaveJobIDs(
	t *testing.T,
	jobs <-chan playerSaveJob,
	want ...core.PlayerID,
) {
	t.Helper()
	for index, id := range want {
		select {
		case job := <-jobs:
			if job.Save.PlayerID != id {
				t.Fatalf("queued job %d PlayerID=%s, want %s", index, job.Save.PlayerID, id)
			}
		case <-time.After(waitDeadline):
			t.Fatalf("queued jobs ended at %d, want %d in PlayerID order", index, len(want))
		}
	}
}

func receivePlayerLoadStarted(t *testing.T, store *controllablePlayerStore) core.PlayerID {
	t.Helper()
	select {
	case id := <-store.loadStarted:
		return id
	case <-time.After(waitDeadline):
		t.Fatal("LoadPlayer was not started")
		return core.PlayerID{}
	}
}

func receivePlayerPrepareResult(
	t *testing.T,
	prepared <-chan playerPrepareResult,
) playerPrepareResult {
	t.Helper()
	select {
	case result := <-prepared:
		return result
	case <-time.After(waitDeadline):
		t.Fatal("Prepare did not return after LoadPlayer was released")
		return playerPrepareResult{}
	}
}

func pollPlayerPersistenceUntilIdle(t *testing.T, p *playerPersistence, tick uint64) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if err := p.Poll(tick); err != nil {
			t.Fatal(err)
		}
		p.mu.Lock()
		idle := true
		for _, player := range p.cache {
			if player.inFlight {
				idle = false
				break
			}
		}
		p.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("player persistence did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
}

func pollPlayerPersistenceUntilSaveStarts(
	t *testing.T,
	p *playerPersistence,
	store *controllablePlayerStore,
	tick uint64,
) storage.PlayerSave {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if err := p.Poll(tick); err != nil {
			t.Fatal(err)
		}
		select {
		case save := <-store.saveStarted:
			return save
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("SavePlayer was not started after Poll")
		}
		time.Sleep(time.Millisecond)
	}
}

func pollPlayerPersistenceUntilError(t *testing.T, p *playerPersistence, tick uint64) error {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if err := p.Poll(tick); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			t.Fatal("player persistence did not surface SavePlayer error")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForPlayerFlushToWait(t *testing.T, p *playerPersistence) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for {
		if p.completionMu.TryLock() {
			p.completionMu.Unlock()
		} else if p.mu.TryLock() {
			p.mu.Unlock()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Flush did not reach completion wait")
		}
		time.Sleep(time.Millisecond)
	}
}

func storedPlayerForTest(
	id core.PlayerID,
	revision uint64,
	name string,
	snapshot sim.PlayerSnapshot,
) storage.StoredPlayer {
	stored := storage.StoredPlayer{
		PlayerID:    id,
		Revision:    revision,
		DisplayName: name,
		Current: storage.PlayerLocation{
			Dimension: snapshot.Current.Dimension,
			Position:  [3]float32(snapshot.Current.Position),
		},
		Yaw: snapshot.Yaw, Pitch: snapshot.Pitch,
	}
	if snapshot.Safe != nil {
		stored.Safe = &storage.PlayerLocation{
			Dimension: snapshot.Safe.Dimension,
			Position:  [3]float32(snapshot.Safe.Position),
		}
	}
	return stored
}

func playerSavesEqual(left, right storage.PlayerSave) bool {
	if left.PlayerID != right.PlayerID || left.Revision != right.Revision ||
		left.DisplayName != right.DisplayName || left.Current != right.Current ||
		left.Yaw != right.Yaw || left.Pitch != right.Pitch {
		return false
	}
	if left.Safe == nil || right.Safe == nil {
		return left.Safe == nil && right.Safe == nil
	}
	return *left.Safe == *right.Safe
}

func clonePlayerSaveForTest(save storage.PlayerSave) storage.PlayerSave {
	copy := save
	if save.Safe != nil {
		safe := *save.Safe
		copy.Safe = &safe
	}
	return copy
}

var _ storage.PlayerStore = (*controllablePlayerStore)(nil)

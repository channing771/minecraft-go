package server

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

// 捕获：同一 PlayerID 的并发 Prepare 各自调用 LoadPlayer，而没有共享 loading placeholder。
func TestPlayerPersistenceCoalescesConcurrentLoad(t *testing.T) {
	store := newCachePlayerStore()
	id := playerID(41)
	release := store.blockLoad(id)
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	results := make(chan playerPrepareResult, 8)
	for index := 0; index < 8; index++ {
		go func() {
			restore, err := persistence.Prepare(
				context.Background(), id, "Player-41", testMetadata(),
			)
			results <- playerPrepareResult{restore: restore, err: err}
		}()
	}
	store.waitForLoadsStarted(t, 1)
	store.assertLoadCount(t, id, 1)
	close(release)
	for index := 0; index < 8; index++ {
		if result := receivePlayerPrepareResult(t, results); result.err != nil {
			t.Fatal(result.err)
		}
	}
	persistence.Abort(id)
	store.assertLoadCount(t, id, 1)
}

// 捕获：leader 在 shared load 后先 Abort 时，旧 waiter 按 PlayerID 重查并创建第二个 placeholder/load。
func TestPlayerPersistenceWaiterDoesNotReloadAfterLeaderAbort(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)

	for attempt := 0; attempt < 100; attempt++ {
		store := newCachePlayerStore()
		id := playerID(42)
		release := store.blockLoad(id)
		persistence := newPlayerPersistence(store, playerPersistenceTestConfig())

		leaderDone := make(chan error, 1)
		go func() {
			_, err := persistence.Prepare(context.Background(), id, "Shared", testMetadata())
			if err == nil {
				persistence.Abort(id)
			}
			leaderDone <- err
		}()
		store.waitForLoadsStarted(t, 1)
		waiterContext := newObservedDoneContext()
		waiterDone := make(chan error, 1)
		go func() {
			_, err := persistence.Prepare(waiterContext, id, "Shared", testMetadata())
			waiterDone <- err
		}()
		waiterContext.waitDoneObserved(t)
		close(release)

		if err := <-leaderDone; err != nil {
			persistence.CloseWorker()
			t.Fatal(err)
		}
		if err := <-waiterDone; err != nil && !errors.Is(err, ErrPlayerPersistenceBackpressure) {
			persistence.CloseWorker()
			t.Fatalf("waiter error=%v, want nil or generation backpressure", err)
		}
		loads := store.loadCount(id)
		persistence.Abort(id)
		persistence.CloseWorker()
		if loads != 1 {
			t.Fatalf("attempt %d LoadPlayer calls=%d, want one shared generation", attempt, loads)
		}
	}
}

// 捕获：同一 loading generation 的不同昵称 waiter 静默覆盖 leader 的 pendingName。
func TestPlayerPersistenceRejectsDifferentNameInLoadingGeneration(t *testing.T) {
	store := newCachePlayerStore()
	id := playerID(43)
	release := store.blockLoad(id)
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	leaderDone := make(chan error, 1)
	go func() {
		_, err := persistence.Prepare(context.Background(), id, "Leader", testMetadata())
		leaderDone <- err
	}()
	store.waitForLoadsStarted(t, 1)
	waiterDone := make(chan error, 1)
	go func() {
		_, err := persistence.Prepare(context.Background(), id, "Different", testMetadata())
		waiterDone <- err
	}()
	select {
	case err := <-waiterDone:
		if !errors.Is(err, ErrPlayerPersistenceBackpressure) {
			close(release)
			t.Fatalf("different-name waiter error=%v, want backpressure", err)
		}
	case <-time.After(waitDeadline):
		close(release)
		t.Fatal("different-name waiter waited on a generation owned by another nickname")
	}
	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatal(err)
	}
	if err := persistence.Activate(id, "Leader"); err != nil {
		t.Fatalf("leader lost pending ownership: %v", err)
	}
	persistence.Abort(id)
}

// 捕获：等待旧 load 的 Abort 在唤醒后按 PlayerID 循环，并清除已经替换的后继 generation。
func TestPlayerPersistenceOldAbortDoesNotTouchSuccessor(t *testing.T) {
	persistence := newPlayerPersistence(newCachePlayerStore(), playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)
	id := playerID(44)
	old := &cachedPlayer{
		id:          id,
		pendingName: "Old",
		loadDone:    make(chan struct{}),
		loading:     true,
	}
	persistence.mu.Lock()
	persistence.cache[id] = old
	persistence.mu.Unlock()

	aborted := make(chan struct{})
	go func() {
		persistence.Abort(id)
		close(aborted)
	}()
	waitForAbortLoadWait(t)

	successor := newMissingCachedPlayer(id, "Successor", testMetadata())
	successor.active = true
	persistence.mu.Lock()
	persistence.cache[id] = successor
	close(old.loadDone)
	persistence.mu.Unlock()
	select {
	case <-aborted:
	case <-time.After(waitDeadline):
		t.Fatal("old Abort waited on the successor generation")
	}

	persistence.mu.Lock()
	current := persistence.cache[id]
	persistence.mu.Unlock()
	if current != successor || !successor.active || successor.pendingName != "Successor" {
		t.Fatalf("successor changed by old Abort: current=%p successor=%+v", current, successor)
	}
	persistence.Abort(id)
}

// 捕获：不同 PlayerID 的 Prepare 被全局锁串行，或第 17 个 placeholder 越过容量继续访问 Store。
func TestPlayerPersistenceCapacityAllowsParallelLoadsAndBackpressures(t *testing.T) {
	store := newCachePlayerStore()
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	releases := make([]chan struct{}, playerCacheCapacity)
	results := make(chan playerPrepareResult, playerCacheCapacity)
	for index := 0; index < playerCacheCapacity; index++ {
		id := playerID(byte(50 + index))
		releases[index] = store.blockLoad(id)
		go func(id core.PlayerID) {
			restore, err := persistence.Prepare(
				context.Background(), id, "Parallel", testMetadata(),
			)
			results <- playerPrepareResult{restore: restore, err: err}
		}(id)
	}
	defer func() {
		for _, release := range releases {
			select {
			case <-release:
			default:
				close(release)
			}
		}
	}()
	store.waitForLoadsStarted(t, playerCacheCapacity)

	overflowID := playerID(90)
	overflow := make(chan error, 1)
	go func() {
		_, err := persistence.Prepare(
			context.Background(), overflowID, "Overflow", testMetadata(),
		)
		overflow <- err
	}()
	select {
	case err := <-overflow:
		if !errors.Is(err, ErrPlayerPersistenceBackpressure) {
			t.Fatalf("17th Prepare error=%v, want backpressure", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("17th Prepare blocked behind another identity's LoadPlayer")
	}
	store.assertLoadCount(t, overflowID, 0)

	for _, release := range releases {
		close(release)
	}
	for index := 0; index < playerCacheCapacity; index++ {
		if result := receivePlayerPrepareResult(t, results); result.err != nil {
			t.Fatal(result.err)
		}
		persistence.Abort(playerID(byte(50 + index)))
	}
}

// 捕获：clean 且非 active/pending 的缓存仍占满 16 项，导致新身份被错误反压。
func TestPlayerPersistenceEvictsCleanInactiveEntries(t *testing.T) {
	store := newCachePlayerStore()
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	for index := 0; index < playerCacheCapacity; index++ {
		id := playerID(byte(100 + index))
		store.put(storedPlayerForTest(id, 7, "Persisted", testPlayerSnapshot(float32(index+1))))
		if _, err := persistence.Prepare(context.Background(), id, "Persisted", testMetadata()); err != nil {
			t.Fatal(err)
		}
		persistence.Abort(id)
	}

	newID := playerID(120)
	if _, err := persistence.Prepare(context.Background(), newID, "New", testMetadata()); err != nil {
		t.Fatalf("Prepare after clean cache fill: %v", err)
	}
	store.assertLoadCount(t, newID, 1)
	persistence.mu.Lock()
	cacheEntries := len(persistence.cache)
	persistence.mu.Unlock()
	if cacheEntries != 1 {
		t.Fatalf("cache entries after clean eviction=%d, want 1", cacheEntries)
	}
	persistence.Abort(newID)
}

// 捕获：dirty 玩家断线后的重连重新读取旧 Store，而没有返回尚在保存的最新缓存快照。
func TestPlayerPersistenceReconnectsFromDirtyCacheBeforeSaveCompletes(t *testing.T) {
	store := newCachePlayerStore()
	id := playerID(121)
	store.put(storedPlayerForTest(id, 7, "Player", testPlayerSnapshot(3)))
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	if _, err := persistence.Prepare(context.Background(), id, "Player", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Activate(id, "Player"); err != nil {
		t.Fatal(err)
	}
	persistence.Confirm(id)
	if err := persistence.Observe(id, "Player", testPlayerSnapshot(19), 20, true); err != nil {
		t.Fatal(err)
	}
	started := store.receiveSave(t)
	if started.Current.Position != [3]float32{19, 70, -19} {
		t.Fatalf("SavePlayer snapshot=%+v, want newest position", started.Current)
	}
	persistence.Deactivate(id)

	restored, err := persistence.Prepare(context.Background(), id, "Player", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Current == nil || restored.Current.Position != testPlayerSnapshot(19).Current.Position {
		t.Fatalf("reconnect restore=%+v, want newest cached snapshot", restored)
	}
	store.assertLoadCount(t, id, 1)
	store.completeSave(nil)
	pollPlayerPersistenceUntilIdle(t, persistence, 21)
	persistence.Abort(id)
}

// 捕获：失败 Load 留下 placeholder、没有唤醒同 ID 等待者，或让等待者重复访问 Store。
func TestPlayerPersistenceFailedLoadCleansPlaceholderAndWakesWaiters(t *testing.T) {
	store := newCachePlayerStore()
	id := playerID(122)
	wantErr := errors.New("load failed")
	store.setLoadError(id, wantErr)
	release := store.blockLoad(id)
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	results := make(chan error, 2)
	go func() {
		_, err := persistence.Prepare(context.Background(), id, "Player", testMetadata())
		results <- err
	}()
	store.waitForLoadsStarted(t, 1)
	waiterContext := newObservedDoneContext()
	go func() {
		_, err := persistence.Prepare(waiterContext, id, "Player", testMetadata())
		results <- err
	}()
	waiterContext.waitDoneObserved(t)
	close(release)
	for index := 0; index < 2; index++ {
		select {
		case err := <-results:
			if !errors.Is(err, wantErr) {
				t.Fatalf("failed Prepare error=%v, want %v", err, wantErr)
			}
		case <-time.After(waitDeadline):
			t.Fatal("same-ID waiter was not woken after failed LoadPlayer")
		}
	}
	store.assertLoadCount(t, id, 1)

	store.setLoadError(id, nil)
	if _, err := persistence.Prepare(context.Background(), id, "Player", testMetadata()); err != nil {
		t.Fatalf("Prepare after failed placeholder cleanup: %v", err)
	}
	store.assertLoadCount(t, id, 2)
	persistence.Abort(id)
}

// 捕获：not-found 身份在 Confirm 前保存候选昵称，或 Abort 后仍残留 cache 项。
func TestPlayerPersistenceAbortsUnconfirmedMissingWithoutResidue(t *testing.T) {
	store := newCachePlayerStore()
	id := playerID(123)
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)

	if _, err := persistence.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Poll(6000); err != nil {
		t.Fatal(err)
	}
	store.assertNoSave(t)
	persistence.Abort(id)
	persistence.mu.Lock()
	_, remains := persistence.cache[id]
	persistence.mu.Unlock()
	if remains {
		t.Fatal("unconfirmed missing player remained cached after Abort")
	}
	if _, err := persistence.Prepare(context.Background(), id, "Second", testMetadata()); err != nil {
		t.Fatal(err)
	}
	store.assertLoadCount(t, id, 2)
	persistence.Abort(id)
}

type cachePlayerStore struct {
	mu          sync.Mutex
	loaded      map[core.PlayerID]storage.StoredPlayer
	loadErrors  map[core.PlayerID]error
	loadBlocks  map[core.PlayerID]chan struct{}
	loadCalls   map[core.PlayerID]int
	loadStarted chan core.PlayerID
	saveStarted chan storage.PlayerSave
	saveResults chan error
}

func newCachePlayerStore() *cachePlayerStore {
	return &cachePlayerStore{
		loaded:      make(map[core.PlayerID]storage.StoredPlayer),
		loadErrors:  make(map[core.PlayerID]error),
		loadBlocks:  make(map[core.PlayerID]chan struct{}),
		loadCalls:   make(map[core.PlayerID]int),
		loadStarted: make(chan core.PlayerID, 64),
		saveStarted: make(chan storage.PlayerSave, 16),
		saveResults: make(chan error),
	}
}

func (store *cachePlayerStore) LoadPlayer(
	ctx context.Context,
	id core.PlayerID,
) (storage.StoredPlayer, error) {
	store.mu.Lock()
	store.loadCalls[id]++
	block := store.loadBlocks[id]
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
	if err := store.loadErrors[id]; err != nil {
		return storage.StoredPlayer{}, err
	}
	stored, ok := store.loaded[id]
	if !ok {
		return storage.StoredPlayer{}, storage.ErrPlayerNotFound
	}
	return stored, nil
}

func (store *cachePlayerStore) SavePlayer(
	ctx context.Context,
	save storage.PlayerSave,
) (uint64, error) {
	select {
	case store.saveStarted <- clonePlayerSaveForTest(save):
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case err := <-store.saveResults:
		if err != nil {
			return 0, err
		}
		return save.Revision, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (store *cachePlayerStore) put(stored storage.StoredPlayer) {
	store.mu.Lock()
	store.loaded[stored.PlayerID] = stored
	store.mu.Unlock()
}

func (store *cachePlayerStore) blockLoad(id core.PlayerID) chan struct{} {
	store.mu.Lock()
	defer store.mu.Unlock()
	release := make(chan struct{})
	store.loadBlocks[id] = release
	return release
}

func (store *cachePlayerStore) setLoadError(id core.PlayerID, err error) {
	store.mu.Lock()
	store.loadErrors[id] = err
	store.mu.Unlock()
}

func (store *cachePlayerStore) waitForLoadsStarted(t *testing.T, want int) {
	t.Helper()
	for index := 0; index < want; index++ {
		select {
		case <-store.loadStarted:
		case <-time.After(waitDeadline):
			t.Fatalf("LoadPlayer starts=%d, want %d concurrent starts", index, want)
		}
	}
}

func (store *cachePlayerStore) assertLoadCount(t *testing.T, id core.PlayerID, want int) {
	t.Helper()
	store.mu.Lock()
	got := store.loadCalls[id]
	store.mu.Unlock()
	if got != want {
		t.Fatalf("LoadPlayer(%s) calls=%d, want %d", id, got, want)
	}
}

func (store *cachePlayerStore) loadCount(id core.PlayerID) int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.loadCalls[id]
}

func (store *cachePlayerStore) receiveSave(t *testing.T) storage.PlayerSave {
	t.Helper()
	select {
	case save := <-store.saveStarted:
		return save
	case <-time.After(waitDeadline):
		t.Fatal("SavePlayer did not start")
		return storage.PlayerSave{}
	}
}

func (store *cachePlayerStore) completeSave(err error) {
	store.saveResults <- err
}

func (store *cachePlayerStore) assertNoSave(t *testing.T) {
	t.Helper()
	select {
	case save := <-store.saveStarted:
		t.Fatalf("unexpected SavePlayer(%+v)", save)
	case <-time.After(50 * time.Millisecond):
	}
}

var _ storage.PlayerStore = (*cachePlayerStore)(nil)

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func newObservedDoneContext() *observedDoneContext {
	return &observedDoneContext{
		Context:  context.Background(),
		observed: make(chan struct{}),
	}
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return nil
}

func (ctx *observedDoneContext) waitDoneObserved(t *testing.T) {
	t.Helper()
	select {
	case <-ctx.observed:
	case <-time.After(waitDeadline):
		t.Fatal("same-ID Prepare did not begin waiting for the shared load")
	}
}

func waitForAbortLoadWait(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for time.Now().Before(deadline) {
		buffer := make([]byte, 1<<16)
		buffer = buffer[:runtime.Stack(buffer, true)]
		for _, stack := range bytes.Split(buffer, []byte("\n\n")) {
			if bytes.Contains(stack, []byte("[chan receive]")) &&
				bytes.Contains(stack, []byte("(*playerPersistence).Abort")) {
				return
			}
		}
		time.Sleep(integrationPollInterval)
	}
	t.Fatal("Abort did not begin waiting on the original loading placeholder")
}

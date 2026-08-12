package server

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// 捕获：CloseWorker 只发 cancel 而未等待 worker 退出，留下后台 goroutine 或关闭时序竞态。
func TestPlayerPersistenceCloseWorkerWaitsForWorkerExit(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	p := newPlayerPersistence(newControllablePlayerStore(), playerPersistenceTestConfig())
	p.CloseWorker()
	select {
	case <-p.done:
	default:
		t.Fatal("CloseWorker returned before worker exit")
	}
	p.CloseWorker()
}

// 捕获：worker 在调用 Store.SavePlayer 时持有 cache mutex，阻塞 Observe/Poll 并把慢 I/O 扩散到 authority。
func TestPlayerPersistenceWorkerDoesNotHoldCacheMutexDuringStoreSave(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(22)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	mutexFree := make(chan bool, 1)
	observeReturned := make(chan struct{})
	store.setOnSave(func() {
		<-observeReturned
		if p.mu.TryLock() {
			p.mu.Unlock()
			mutexFree <- true
			return
		}
		mutexFree <- false
	})
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	close(observeReturned)
	_ = receivePlayerSave(t, store)
	select {
	case free := <-mutexFree:
		if !free {
			t.Fatal("worker held cache mutex during Store.SavePlayer")
		}
	case <-time.After(waitDeadline):
		t.Fatal("Store callback did not run")
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 1)
}

// 捕获：Prepare 在慢 LoadPlayer 期间持有 cache mutex，使 authority 侧的 Observe/Poll 无法取得状态锁。
func TestPlayerPersistencePrepareDoesNotHoldCacheMutexDuringLoadPlayer(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(24)
	releaseLoad := store.blockLoad(id)
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	prepared := make(chan playerPrepareResult, 1)
	go func() {
		restore, err := p.Prepare(context.Background(), id, "A", testMetadata())
		prepared <- playerPrepareResult{restore: restore, err: err}
	}()
	if got := receivePlayerLoadStarted(t, store); got != id {
		close(releaseLoad)
		<-prepared
		t.Fatalf("blocked LoadPlayer id=%s, want %s", got, id)
	}

	cacheMutexFree := p.mu.TryLock()
	if cacheMutexFree {
		p.mu.Unlock()
	}
	close(releaseLoad)
	result := receivePlayerPrepareResult(t, prepared)
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !cacheMutexFree {
		t.Fatal("Prepare held cache mutex while LoadPlayer was blocked")
	}
}

// 捕获：不同身份的 Prepare 被串行，或并发 Load 完成时互相覆盖 cache。
func TestPlayerPersistencePrepareSerializesConcurrentLoadsAndKeepsLatestCache(t *testing.T) {
	store := newControllablePlayerStore()
	idA, idB := playerID(25), playerID(26)
	store.loaded[idA] = storedPlayerForTest(idA, 7, "A", testPlayerSnapshot(3))
	store.loaded[idB] = storedPlayerForTest(idB, 9, "B", testPlayerSnapshot(4))
	releaseA := store.blockLoad(idA)
	releaseB := store.blockLoad(idB)
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	preparedA := make(chan playerPrepareResult, 1)
	go func() {
		restore, err := p.Prepare(context.Background(), idA, "A", testMetadata())
		preparedA <- playerPrepareResult{restore: restore, err: err}
	}()
	if got := receivePlayerLoadStarted(t, store); got != idA {
		close(releaseA)
		t.Fatalf("first blocked LoadPlayer id=%s, want %s", got, idA)
	}
	preparedB := make(chan playerPrepareResult, 1)
	go func() {
		restore, err := p.Prepare(context.Background(), idB, "B", testMetadata())
		preparedB <- playerPrepareResult{restore: restore, err: err}
	}()
	if got := receivePlayerLoadStarted(t, store); got != idB {
		close(releaseA)
		close(releaseB)
		t.Fatalf("concurrent blocked LoadPlayer id=%s, want %s", got, idB)
	}
	close(releaseA)
	close(releaseB)
	if result := receivePlayerPrepareResult(t, preparedA); result.err != nil {
		t.Fatal(result.err)
	}
	if result := receivePlayerPrepareResult(t, preparedB); result.err != nil {
		t.Fatal(result.err)
	}

	p.mu.Lock()
	cacheA, cacheB := p.cache[idA], p.cache[idB]
	p.mu.Unlock()
	if cacheA == nil || cacheA.persisted != 7 || cacheB == nil || cacheB.persisted != 9 {
		t.Fatalf("concurrent caches: A=%+v B=%+v, want revisions 7/9", cacheA, cacheB)
	}
	if store.loadCallCount(idA) != 1 || store.loadCallCount(idB) != 1 {
		t.Fatalf("LoadPlayer calls: idA=%d idB=%d, want one each",
			store.loadCallCount(idA), store.loadCallCount(idB))
	}
	p.Abort(idA)
	p.Abort(idB)
}

// 捕获：Load 移出 cache mutex 后，Abort 在 load 期间过早返回并丢失取消 staged nickname 的原子性。
func TestPlayerPersistenceAbortWaitsForPrepareLoadAndClearsStage(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	store := newControllablePlayerStore()
	id := playerID(32)
	releaseLoad := store.blockLoad(id)
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	prepared := make(chan playerPrepareResult, 1)
	go func() {
		restore, err := p.Prepare(context.Background(), id, "Candidate", testMetadata())
		prepared <- playerPrepareResult{restore: restore, err: err}
	}()
	if got := receivePlayerLoadStarted(t, store); got != id {
		close(releaseLoad)
		<-prepared
		t.Fatalf("blocked LoadPlayer id=%s, want %s", got, id)
	}

	abortStarted := make(chan struct{})
	aborted := make(chan struct{})
	go func() {
		close(abortStarted)
		p.Abort(id)
		close(aborted)
	}()
	<-abortStarted
	runtime.Gosched()
	returnedBeforeLoadCompleted := false
	select {
	case <-aborted:
		returnedBeforeLoadCompleted = true
	default:
	}

	close(releaseLoad)
	if result := receivePlayerPrepareResult(t, prepared); result.err != nil {
		t.Fatal(result.err)
	}
	select {
	case <-aborted:
	case <-time.After(waitDeadline):
		t.Fatal("Abort did not finish after Prepare completed")
	}
	if returnedBeforeLoadCompleted {
		t.Fatal("Abort returned before blocked Prepare committed its staged cache")
	}
	if err := p.Activate(id, "Candidate"); !errors.Is(err, ErrPlayerPersistenceBackpressure) {
		t.Fatalf("Activate after concurrent Abort error=%v, want backpressure", err)
	}
}

// 捕获：CloseWorker 等待 worker 后未应用已经进入 completion 队列的最终结果。
func TestPlayerPersistenceCloseWorkerDrainsBufferedCompletions(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(5)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	if _, err := persistence.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	_ = receivePlayerSave(t, store)
	store.complete(nil)
	waitForPlayerSaveCompletionDepth(t, persistence.completions, 1)

	persistence.CloseWorker()
	persistence.mu.Lock()
	player := persistence.cache[id]
	persisted, inFlight := player.persisted, player.inFlight
	persistence.mu.Unlock()
	if persisted != 8 || inFlight {
		t.Fatalf("closed player state: persisted=%d inFlight=%t, want 8/false", persisted, inFlight)
	}
}

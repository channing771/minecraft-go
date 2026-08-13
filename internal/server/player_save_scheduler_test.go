package server

import (
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

// 捕获：player save scheduler 读取 world SaveWorkers、只启动一个 worker，或改变固定队列上限。
func TestPlayerSaveSchedulerUsesExactlyTwoWorkers(t *testing.T) {
	store := newConcurrentPlayerSaveStore()
	scheduler := newPlayerSaveScheduler(store)
	t.Cleanup(func() {
		scheduler.CloseJobs()
		scheduler.Wait()
	})

	if got := cap(scheduler.jobs); got != 16 {
		t.Fatalf("jobs capacity=%d, want 16", got)
	}
	if got := cap(scheduler.completions); got != 2 {
		t.Fatalf("completions capacity=%d, want 2", got)
	}
	for value := byte(1); value <= 3; value++ {
		scheduler.jobs <- schedulerTestSaveJob(playerID(value), 1, float32(value))
	}
	first := store.receiveStarted(t)
	second := store.receiveStarted(t)
	store.assertNoStart(t)

	store.complete(first.PlayerID, nil)
	third := store.receiveStarted(t)
	store.complete(second.PlayerID, nil)
	store.complete(third.PlayerID, nil)
	for index := 0; index < 3; index++ {
		receivePlayerSaveCompletion(t, scheduler.completions)
	}
}

// 捕获：completion backlog 通过额外 goroutine/无界 slice 绕过容量 2，而没有背压两个 worker。
func TestPlayerSaveSchedulerCompletionBackpressureIsBounded(t *testing.T) {
	store := newConcurrentPlayerSaveStore()
	scheduler := newPlayerSaveScheduler(store)
	t.Cleanup(func() {
		scheduler.CloseJobs()
		scheduler.Wait()
	})

	for value := byte(1); value <= 5; value++ {
		scheduler.jobs <- schedulerTestSaveJob(playerID(value), 1, float32(value))
	}
	first := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	for _, save := range first {
		store.complete(save.PlayerID, nil)
	}
	second := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	waitForPlayerSaveCompletionDepth(t, scheduler.completions, 2)
	for _, save := range second {
		store.complete(save.PlayerID, nil)
	}
	store.assertNoStart(t)

	receivePlayerSaveCompletion(t, scheduler.completions)
	fifth := store.receiveStarted(t)
	store.complete(fifth.PlayerID, nil)
	for index := 0; index < 4; index++ {
		receivePlayerSaveCompletion(t, scheduler.completions)
	}
}

// 捕获：CloseJobs 与 nonblocking producer 并发时关闭 channel，触发 send-on-closed panic；
// 同时捕获 completion 满载 worker 无法在 cancel 后退出，导致 Wait 永久阻塞。
func TestPlayerSaveSchedulerSubmitAndCloseAreRaceSafe(t *testing.T) {
	store := newConcurrentPlayerSaveStore()
	scheduler := newPlayerSaveScheduler(store)
	t.Cleanup(func() {
		scheduler.CloseJobs()
		scheduler.Wait()
	})

	for value := byte(1); value <= 4; value++ {
		if !scheduler.TrySubmit(schedulerTestSaveJob(playerID(value), 1, float32(value))) {
			t.Fatalf("initial submit %d was rejected", value)
		}
	}
	first := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	for _, save := range first {
		store.complete(save.PlayerID, nil)
	}
	second := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	waitForPlayerSaveCompletionDepth(t, scheduler.completions, playerSaveDoneCapacity)
	for _, save := range second {
		store.complete(save.PlayerID, nil)
	}

	stop := make(chan struct{})
	var producers sync.WaitGroup
	for producer := 0; producer < 4; producer++ {
		producers.Add(1)
		go func(offset byte) {
			defer producers.Done()
			for value := byte(10 + offset); ; value++ {
				select {
				case <-stop:
					return
				default:
					scheduler.TrySubmit(schedulerTestSaveJob(playerID(value), 1, float32(value)))
					runtime.Gosched()
				}
			}
		}(byte(producer * 32))
	}
	runtime.Gosched()
	scheduler.CloseJobs()
	close(stop)
	producers.Wait()
	if scheduler.TrySubmit(schedulerTestSaveJob(playerID(250), 1, 250)) {
		t.Fatal("submit succeeded after CloseJobs")
	}

	waited := make(chan struct{})
	go func() {
		scheduler.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(waitDeadline):
		t.Fatal("Wait blocked with workers backpressured by full completions")
	}
}

type concurrentPlayerSaveStore struct {
	mu      sync.Mutex
	loaded  map[core.PlayerID]storage.StoredPlayer
	results map[core.PlayerID]chan error
	started chan storage.PlayerSave
}

func newConcurrentPlayerSaveStore() *concurrentPlayerSaveStore {
	return &concurrentPlayerSaveStore{
		loaded:  make(map[core.PlayerID]storage.StoredPlayer),
		results: make(map[core.PlayerID]chan error),
		started: make(chan storage.PlayerSave, playerCacheCapacity),
	}
}

func (store *concurrentPlayerSaveStore) LoadPlayer(
	ctx context.Context,
	id core.PlayerID,
) (storage.StoredPlayer, error) {
	if err := ctx.Err(); err != nil {
		return storage.StoredPlayer{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	loaded, ok := store.loaded[id]
	if !ok {
		return storage.StoredPlayer{}, storage.ErrPlayerNotFound
	}
	return loaded, nil
}

func (store *concurrentPlayerSaveStore) SavePlayer(
	ctx context.Context,
	save storage.PlayerSave,
) (uint64, error) {
	store.mu.Lock()
	results := store.results[save.PlayerID]
	if results == nil {
		results = make(chan error, playerSaveWorkerCount+1)
		store.results[save.PlayerID] = results
	}
	store.mu.Unlock()
	select {
	case store.started <- clonePlayerSaveForTest(save):
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	select {
	case err := <-results:
		if err != nil {
			return 0, err
		}
		return save.Revision, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (store *concurrentPlayerSaveStore) put(stored storage.StoredPlayer) {
	store.mu.Lock()
	store.loaded[stored.PlayerID] = stored
	store.mu.Unlock()
}

func (store *concurrentPlayerSaveStore) complete(id core.PlayerID, err error) {
	store.mu.Lock()
	results := store.results[id]
	store.mu.Unlock()
	if results == nil {
		panic("complete called before SavePlayer started")
	}
	results <- err
}

func (store *concurrentPlayerSaveStore) receiveStarted(t *testing.T) storage.PlayerSave {
	t.Helper()
	select {
	case save := <-store.started:
		return save
	case <-time.After(waitDeadline):
		t.Fatal("SavePlayer did not start")
		return storage.PlayerSave{}
	}
}

func (store *concurrentPlayerSaveStore) assertNoStart(t *testing.T) {
	t.Helper()
	select {
	case save := <-store.started:
		t.Fatalf("unexpected SavePlayer start: %+v", save)
	case <-time.After(50 * time.Millisecond):
	}
}

func schedulerTestSaveJob(
	id core.PlayerID,
	revision uint64,
	position float32,
) playerSaveJob {
	return playerSaveJob{Save: storage.PlayerSave{
		PlayerID: id,
		Revision: revision,
		Current: storage.PlayerLocation{
			Dimension: core.Overworld,
			Position:  [3]float32{position, 70, -position},
		},
	}, Attempt: 1}
}

func receivePlayerSaveCompletion(
	t *testing.T,
	completions <-chan playerSaveCompletion,
) playerSaveCompletion {
	t.Helper()
	select {
	case completion := <-completions:
		return completion
	case <-time.After(waitDeadline):
		t.Fatal("player save completion was not emitted")
		return playerSaveCompletion{}
	}
}

func waitForPlayerSaveCompletionDepth(
	t *testing.T,
	completions <-chan playerSaveCompletion,
	want int,
) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	for len(completions) != want {
		if time.Now().After(deadline) {
			t.Fatalf("completion depth=%d, want %d", len(completions), want)
		}
		time.Sleep(time.Millisecond)
	}
}

var _ storage.PlayerStore = (*concurrentPlayerSaveStore)(nil)

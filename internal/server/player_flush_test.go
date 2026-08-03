package server

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
)

// 捕获：Flush 在首个失败后立即返回、同次重试失败 revision，或依赖 map 顺序拼接错误。
func TestPlayerFlushAttemptsEachRevisionOnceAndSortsErrors(t *testing.T) {
	persistence, store := newDirtyPersistence(t, 3)
	oneErr, threeErr := errors.New("one"), errors.New("three")
	store.fail(playerID(1), oneErr)
	store.fail(playerID(3), threeErr)

	err := persistence.Flush(context.Background())
	wantText := "save player 00000000-0000-4000-8000-000000000001 revision 1: one\n" +
		"save player 00000000-0000-4000-8000-000000000003 revision 1: three"
	if err == nil || err.Error() != wantText {
		t.Fatalf("Flush error=%q, want %q", err, wantText)
	}
	if !errors.Is(err, oneErr) || !errors.Is(err, threeErr) {
		t.Fatalf("Flush error=%v does not retain both roots", err)
	}
	store.assertAttempts(t, map[playerSaveKey]int{
		{playerID: playerID(1), revision: 1}: 1,
		{playerID: playerID(2), revision: 1}: 1,
		{playerID: playerID(3), revision: 1}: 1,
	})
}

// 捕获：Flush 继承旧 revision in-flight 后重试旧值，或漏掉其完成期间产生的最新 revision。
func TestPlayerFlushWaitsForInheritedRevisionAndAttemptsOnlyLatestFollowup(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(4)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)
	if _, err := persistence.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	inherited := receivePlayerSave(t, store)
	if inherited.Revision != 8 {
		t.Fatalf("inherited revision=%d, want 8", inherited.Revision)
	}
	if err := persistence.Observe(id, "A", testPlayerSnapshot(20), 0, false); err != nil {
		t.Fatal(err)
	}

	flushed := make(chan error, 1)
	go func() { flushed <- persistence.Flush(context.Background()) }()
	waitForPlayerFlushToWait(t, persistence)
	store.complete(nil)
	followup := receivePlayerSave(t, store)
	if followup.Revision != 9 || followup.Current.Position != [3]float32{20, 70, -20} {
		t.Fatalf("latest followup=%+v, want revision 9/latest snapshot", followup)
	}
	store.complete(nil)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatalf("Flush error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Flush did not return")
	}
	assertNoPlayerSaveStarted(t, store)
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

type deterministicFlushStore struct {
	mu       sync.Mutex
	loaded   map[core.PlayerID]storage.StoredPlayer
	failures map[core.PlayerID]error
	attempts map[playerSaveKey]int
}

func newDirtyPersistence(t *testing.T, count int) (*playerPersistence, *deterministicFlushStore) {
	t.Helper()
	store := &deterministicFlushStore{
		loaded:   make(map[core.PlayerID]storage.StoredPlayer),
		failures: make(map[core.PlayerID]error),
		attempts: make(map[playerSaveKey]int),
	}
	for number := 1; number <= count; number++ {
		id := playerID(byte(number))
		name := string(rune('A' + number - 1))
		store.loaded[id] = storedPlayerForTest(id, 0, name, testPlayerSnapshot(float32(number)))
	}
	persistence := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(persistence.CloseWorker)
	for number := 1; number <= count; number++ {
		id := playerID(byte(number))
		name := string(rune('A' + number - 1))
		if _, err := persistence.Prepare(context.Background(), id, name, testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := persistence.Observe(id, name, testPlayerSnapshot(float32(number+10)), 0, false); err != nil {
			t.Fatal(err)
		}
	}
	return persistence, store
}

func (store *deterministicFlushStore) LoadPlayer(
	_ context.Context,
	id core.PlayerID,
) (storage.StoredPlayer, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	loaded, ok := store.loaded[id]
	if !ok {
		return storage.StoredPlayer{}, storage.ErrPlayerNotFound
	}
	return loaded, nil
}

func (store *deterministicFlushStore) SavePlayer(
	_ context.Context,
	save storage.PlayerSave,
) (uint64, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.attempts[playerSaveKey{playerID: save.PlayerID, revision: save.Revision}]++
	if err := store.failures[save.PlayerID]; err != nil {
		return 0, err
	}
	return save.Revision, nil
}

func (store *deterministicFlushStore) fail(id core.PlayerID, err error) {
	store.mu.Lock()
	store.failures[id] = err
	store.mu.Unlock()
}

func (store *deterministicFlushStore) assertAttempts(
	t *testing.T,
	want map[playerSaveKey]int,
) {
	t.Helper()
	store.mu.Lock()
	got := make(map[playerSaveKey]int, len(store.attempts))
	for key, attempts := range store.attempts {
		got[key] = attempts
	}
	store.mu.Unlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SavePlayer attempts=%v, want %v", got, want)
	}
}

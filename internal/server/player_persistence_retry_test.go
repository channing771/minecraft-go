package server

import (
	"context"
	"errors"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

// 捕获：在途保存期间允许第二个 SavePlayer，或首个成功错误清除了较新的 coalesced 快照。
func TestPlayerPersistenceCoalescesLatestSnapshotBehindSingleInFlightSave(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(6)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 20, true); err != nil {
		t.Fatal(err)
	}
	first := receivePlayerSave(t, store)
	if first.Revision != 8 || first.Current.Position != [3]float32{10, 70, -10} {
		t.Fatalf("first SavePlayer=%+v", first)
	}
	for _, position := range []float32{11, 12} {
		if err := p.Observe(id, "A", testPlayerSnapshot(position), 20, true); err != nil {
			t.Fatal(err)
		}
	}
	assertNoPlayerSaveStarted(t, store)

	store.complete(nil)
	second := pollPlayerPersistenceUntilSaveStarts(t, p, store, 21)
	if second.PlayerID != id || second.Revision != 9 ||
		second.Current.Position != [3]float32{12, 70, -12} ||
		second.Safe == nil || second.Safe.Position != [3]float32{11, 64, -12} {
		t.Fatalf("coalesced SavePlayer=%+v", second)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 22)
	assertNoPlayerSaveStarted(t, store)
}

// 捕获：两个 worker 的 completion 以到达顺序直接应用，使同一 tick 的错误顺序不确定。
func TestPlayerSaveCompletionBatchAppliesByPlayerID(t *testing.T) {
	store := newConcurrentPlayerSaveStore()
	idOne, idTwo := playerID(1), playerID(2)
	store.put(storedPlayerForTest(idOne, 7, "One", testPlayerSnapshot(1)))
	store.put(storedPlayerForTest(idTwo, 7, "Two", testPlayerSnapshot(2)))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	for _, prepared := range []struct {
		id   core.PlayerID
		name string
	}{{idOne, "One"}, {idTwo, "Two"}} {
		if _, err := p.Prepare(
			context.Background(), prepared.id, prepared.name, testMetadata(),
		); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(
			prepared.id, prepared.name, testPlayerSnapshot(10), 0, true,
		); err != nil {
			t.Fatal(err)
		}
	}
	started := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	assertPlayerSavesContainIDs(t, started, idOne, idTwo)

	store.complete(idTwo, errors.New("two"))
	waitForPlayerSaveCompletionDepth(t, p.completions, 1)
	store.complete(idOne, errors.New("one"))
	waitForPlayerSaveCompletionDepth(t, p.completions, 2)
	if err := p.Poll(0); err == nil || err.Error() != "one\ntwo" {
		t.Fatalf("reverse completion error=%q, want PlayerID order %q", err, "one\ntwo")
	}
}

// 捕获：一个身份失败后全局阻塞另一个身份，或 retry 被较新的 Observe 改写 revision/value。
func TestPlayerSaveRetryIsPerPlayer(t *testing.T) {
	store := newConcurrentPlayerSaveStore()
	idOne, idTwo := playerID(1), playerID(2)
	store.put(storedPlayerForTest(idOne, 7, "One", testPlayerSnapshot(1)))
	store.put(storedPlayerForTest(idTwo, 7, "Two", testPlayerSnapshot(2)))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	for _, prepared := range []struct {
		id   core.PlayerID
		name string
	}{{idOne, "One"}, {idTwo, "Two"}} {
		if _, err := p.Prepare(
			context.Background(), prepared.id, prepared.name, testMetadata(),
		); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(
			prepared.id, prepared.name, testPlayerSnapshot(10), 0, true,
		); err != nil {
			t.Fatal(err)
		}
	}
	started := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
	assertPlayerSavesContainIDs(t, started, idOne, idTwo)
	firstOne := playerSaveForID(t, started, idOne)
	if err := p.Observe(idOne, "One", testPlayerSnapshot(20), 0, false); err != nil {
		t.Fatal(err)
	}

	store.complete(idOne, errors.New("disk full"))
	store.complete(idTwo, nil)
	waitForPlayerSaveCompletionDepth(t, p.completions, 2)
	if err := p.Poll(0); err == nil || err.Error() != "disk full" {
		t.Fatalf("Poll error=%v, want ID one failure only", err)
	}

	p.mu.Lock()
	one, two := p.cache[idOne], p.cache[idTwo]
	if one == nil || one.retry == nil {
		p.mu.Unlock()
		t.Fatal("failed ID did not retain retry")
	}
	retry := *one.retry
	twoPersisted, twoDirty, twoRetry := two.persisted, two.dirty, two.retry
	p.mu.Unlock()
	if retry.Attempt != 2 || retry.NextTick != 20 ||
		!playerSavesEqual(retry.Save, firstOne) {
		t.Fatalf("retry=%+v, want attempt 2 at tick 20 with frozen save %+v", retry, firstOne)
	}
	if twoPersisted != 8 || twoDirty || twoRetry != nil {
		t.Fatalf("successful ID state persisted=%d dirty=%v retry=%+v", twoPersisted, twoDirty, twoRetry)
	}

	if err := p.Poll(19); err != nil {
		t.Fatal(err)
	}
	store.assertNoStart(t)
	if err := p.Poll(20); err != nil {
		t.Fatal(err)
	}
	retried := store.receiveStarted(t)
	if !playerSavesEqual(retried, firstOne) {
		t.Fatalf("retry SavePlayer=%+v, want frozen=%+v", retried, firstOne)
	}
	store.assertNoStart(t)
	store.complete(idOne, nil)
	pollPlayerPersistenceUntilIdle(t, p, 20)

	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	fresh := store.receiveStarted(t)
	if fresh.PlayerID != idOne || fresh.Revision != 9 ||
		fresh.Current.Position != [3]float32{20, 70, -20} {
		t.Fatalf("post-retry latest SavePlayer=%+v", fresh)
	}
	store.assertNoStart(t)
	store.complete(idOne, nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
}

// 捕获：同一 tick 只调度一个 eligible identity，或 map 迭代顺序泄漏到 jobs 队列。
func TestPlayerSaveDispatchesEligiblePlayersInPlayerIDOrder(t *testing.T) {
	t.Run("autosave", func(t *testing.T) {
		store := newConcurrentPlayerSaveStore()
		p := newPlayerPersistence(store, playerPersistenceTestConfig())
		t.Cleanup(p.CloseWorker)
		for _, value := range []byte{3, 1, 2} {
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
		blockPlayerSaveWorkers(t, p, store)

		if err := p.Poll(6000); err != nil {
			t.Fatal(err)
		}
		assertQueuedPlayerSaveJobIDs(t, p.jobs, playerID(1), playerID(2), playerID(3))
	})

	t.Run("retry", func(t *testing.T) {
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
			if err := p.Observe(id, name, testPlayerSnapshot(10), 0, true); err != nil {
				t.Fatal(err)
			}
		}
		started := []storage.PlayerSave{store.receiveStarted(t), store.receiveStarted(t)}
		assertPlayerSavesContainIDs(t, started, playerID(1), playerID(2))
		store.complete(playerID(2), errors.New("two"))
		store.complete(playerID(1), errors.New("one"))
		waitForPlayerSaveCompletionDepth(t, p.completions, 2)
		if err := p.Poll(0); err == nil {
			t.Fatal("failed saves were not surfaced")
		}
		blockPlayerSaveWorkers(t, p, store)

		if err := p.Poll(20); err != nil {
			t.Fatal(err)
		}
		assertQueuedPlayerSaveJobIDs(t, p.jobs, playerID(1), playerID(2))
	})
}

// 捕获：失败保存丢弃最新离线快照，或一个 retry 项错误阻塞另一身份使用剩余 cache 容量。
func TestDirtyDisconnectedPlayerBlocksOnlyDifferentIdentity(t *testing.T) {
	store := newControllablePlayerStore()
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	idA, idB := playerID(7), playerID(8)
	if _, err := p.Prepare(context.Background(), idA, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(idA, "A"); err != nil {
		t.Fatal(err)
	}
	p.Confirm(idA)
	if err := p.Observe(idA, "A", testPlayerSnapshot(10), 20, true); err != nil {
		t.Fatal(err)
	}
	store.complete(errors.New("disk full"))
	if err := pollPlayerPersistenceUntilError(t, p, 21); err == nil {
		t.Fatal("save failure not surfaced")
	}
	restoredB, err := p.Prepare(context.Background(), idB, "B", testMetadata())
	if err != nil || restoredB.Current != nil || restoredB.Safe != nil {
		t.Fatalf("different ID restore=%+v err=%v, want independent cache slot", restoredB, err)
	}
	p.Abort(idB)
	restored, err := p.Prepare(context.Background(), idA, "A", testMetadata())
	if err != nil || restored.Current == nil || restored.Current.Position[0] != 10 {
		t.Fatalf("restore=%+v err=%v", restored, err)
	}
	p.Abort(idA)
}

// 捕获：retry 未按首个 20-tick backoff 调度，或复用了较新快照/新 revision 而破坏幂等保存。
func TestPlayerPersistenceRetryReusesFailedRevisionAndValueAtFirstBackoff(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(9)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	first := receivePlayerSave(t, store)
	if err := p.Observe(id, "A", testPlayerSnapshot(20), 0, false); err != nil {
		t.Fatal(err)
	}
	store.complete(errors.New("disk full"))
	if err := pollPlayerPersistenceUntilError(t, p, 0); err == nil {
		t.Fatal("first save failure not surfaced")
	}
	if err := p.Poll(19); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)
	if err := p.Poll(20); err != nil {
		t.Fatal(err)
	}
	retry := receivePlayerSave(t, store)
	if !playerSavesEqual(first, retry) || retry.Revision != 8 ||
		retry.Current.Position != [3]float32{10, 70, -10} {
		t.Fatalf("retry=%+v, want immutable first SavePlayer=%+v", retry, first)
	}
}

// 捕获：失败重试没有按 20/40/80… 指数退避并在 1200 tick 封顶，或任一次重试变更 immutable save。
func TestPlayerPersistenceRetryBackoffDoublesAndCaps(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(10)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	want := receivePlayerSave(t, store)
	tick := uint64(0)
	for _, delay := range []uint64{20, 40, 80, 160, 320, 640, 1200, 1200} {
		store.complete(errors.New("disk full"))
		if err := pollPlayerPersistenceUntilError(t, p, tick); err == nil {
			t.Fatalf("failure at tick %d was not surfaced", tick)
		}
		due := tick + delay
		if err := p.Poll(due - 1); err != nil {
			t.Fatal(err)
		}
		assertNoPlayerSaveStarted(t, store)
		got := pollPlayerPersistenceUntilSaveStarts(t, p, store, due)
		if !playerSavesEqual(got, want) {
			t.Fatalf("retry due at %d = %+v, want immutable %+v", due, got, want)
		}
		tick = due
	}
}

// 捕获：force=true 越过 pending retry 直接用较新值复用旧 revision，或 retry 成功后漏掉该强制的新快照。
func TestPlayerPersistenceForceObserveRetriesFrozenValueBeforeLatestSnapshot(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(17)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	first := receivePlayerSave(t, store)
	store.complete(errors.New("disk full"))
	if err := pollPlayerPersistenceUntilError(t, p, 0); err == nil {
		t.Fatal("failed save was not surfaced")
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(20), 1, true); err != nil {
		t.Fatal(err)
	}
	retry := receivePlayerSave(t, store)
	if !playerSavesEqual(retry, first) {
		t.Fatalf("forced retry=%+v, want frozen=%+v", retry, first)
	}
	store.complete(nil)
	fresh := pollPlayerPersistenceUntilSaveStarts(t, p, store, 2)
	if fresh.Revision != 9 || fresh.Current.Position != [3]float32{20, 70, -20} {
		t.Fatalf("forced latest save=%+v", fresh)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 3)
}

// 捕获：Store 的不匹配成功 revision 被接受，破坏 cache revision 的单调性并丢失原 job。
func TestPlayerPersistenceRejectsMismatchedStoreRevisionWithoutLosingRetry(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(18)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	first := receivePlayerSave(t, store)
	store.completeWithRevision(9, nil)
	if err := pollPlayerPersistenceUntilError(t, p, 0); err == nil {
		t.Fatal("mismatched store revision was accepted")
	}
	if err := p.Poll(20); err != nil {
		t.Fatal(err)
	}
	retry := receivePlayerSave(t, store)
	if !playerSavesEqual(retry, first) || retry.Revision != 8 {
		t.Fatalf("revision-mismatch retry=%+v, want frozen=%+v", retry, first)
	}
}

// 捕获：worker 把 job 的 Safe 指针直接交给 Store，使 Store 输入篡改污染后续 immutable retry。
func TestPlayerPersistenceRetryDoesNotAliasStoreSaveInput(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(19)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	store.mutateNextSave()
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 0, true); err != nil {
		t.Fatal(err)
	}
	first := receivePlayerSave(t, store)
	if first.Safe == nil || first.Safe.Position[0] != 999 {
		t.Fatalf("test Store did not mutate first save=%+v", first)
	}
	store.complete(errors.New("disk full"))
	if err := pollPlayerPersistenceUntilError(t, p, 0); err == nil {
		t.Fatal("failed save was not surfaced")
	}
	if err := p.Poll(20); err != nil {
		t.Fatal(err)
	}
	retry := receivePlayerSave(t, store)
	if retry.Safe == nil || retry.Safe.Position != [3]float32{9, 64, -10} || retry.Revision != 8 {
		t.Fatalf("retry polluted by Store mutation=%+v", retry)
	}
}

// 捕获：干净且无在途保存的离线 cache 仍无限期占用唯一身份槽。
func TestPlayerPersistenceAllowsAnotherIdentityAfterSuccessfulOfflineSave(t *testing.T) {
	store := newControllablePlayerStore()
	idA, idB := playerID(12), playerID(13)
	store.loaded[idA] = storedPlayerForTest(idA, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), idA, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(idA, "A", testPlayerSnapshot(10), 20, true); err != nil {
		t.Fatal(err)
	}
	_ = receivePlayerSave(t, store)
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 21)

	restored, err := p.Prepare(context.Background(), idB, "B", testMetadata())
	if err != nil || restored.Current != nil || restored.Safe != nil ||
		restored.SpawnAnchor != (core.ChunkPos{X: 2, Z: -3}) {
		t.Fatalf("clean identity switch restore=%+v err=%v", restored, err)
	}
}

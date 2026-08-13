package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

// 捕获：Flush 遵守 retry backoff 而没有立即重试，或重试时改变已冻结的 SavePlayer 值。
func TestPlayerFlushRetriesPendingJobWithoutWaitingForBackoff(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(14)
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

	flushed := make(chan error, 1)
	go func() { flushed <- p.Flush(context.Background()) }()
	retry := receivePlayerSave(t, store)
	if !playerSavesEqual(retry, first) {
		t.Fatalf("Flush retry=%+v, want original=%+v", retry, first)
	}
	store.complete(nil)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Flush did not return after retry success")
	}
}

// 捕获：Flush 将失败 job 丢弃，导致后续 Flush 用较新快照复用旧 revision 而破坏幂等重试。
func TestPlayerFlushFailureRetainsFrozenJobForLaterRetry(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(15)
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
	firstFlush := make(chan error, 1)
	go func() { firstFlush <- p.Flush(context.Background()) }()
	waitForPlayerFlushToWait(t, p)
	wantErr := errors.New("disk full")
	store.complete(wantErr)
	select {
	case err := <-firstFlush:
		if !errors.Is(err, wantErr) {
			t.Fatalf("first Flush error=%v, want disk full", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("failed Flush did not return")
	}

	secondFlush := make(chan error, 1)
	go func() { secondFlush <- p.Flush(context.Background()) }()
	retry := receivePlayerSave(t, store)
	if !playerSavesEqual(retry, first) {
		t.Fatalf("later Flush retry=%+v, want frozen=%+v", retry, first)
	}
	store.complete(nil)
	fresh := receivePlayerSave(t, store)
	if fresh.Revision != 9 || fresh.Current.Position != [3]float32{20, 70, -20} {
		t.Fatalf("Flush fresh save after frozen retry=%+v", fresh)
	}
	store.complete(nil)
	select {
	case err := <-secondFlush:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("retrying Flush did not return")
	}
}

// 捕获：已取消的 Flush 仍派发 retry，或者返回 context 错误时丢弃后续可重试的冻结 job。
func TestPlayerFlushCanceledContextLeavesRetryUndispatchedAndRetryable(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(16)
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
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Flush error=%v, want context.Canceled", err)
	}
	assertNoPlayerSaveStarted(t, store)

	flushed := make(chan error, 1)
	go func() { flushed <- p.Flush(context.Background()) }()
	retry := receivePlayerSave(t, store)
	if !playerSavesEqual(retry, first) {
		t.Fatalf("retry after canceled Flush=%+v, want frozen=%+v", retry, first)
	}
	store.complete(nil)
	select {
	case err := <-flushed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("retrying Flush did not return")
	}
}

// 捕获：Flush 在首错后没有继续处理其他身份，或把后续 completion 遗留给下一次 Flush。
func TestPlayerFlushDoesNotLeaveConcurrentFailureForNextFlush(t *testing.T) {
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

	firstFlush := make(chan error, 1)
	go func() { firstFlush <- p.Flush(context.Background()) }()
	first := store.receiveStarted(t)
	if first.PlayerID != playerID(1) {
		t.Fatalf("first Flush SavePlayer ID=%s, want sorted ID %s", first.PlayerID, playerID(1))
	}
	store.assertNoStart(t)
	wantErr := errors.New("disk unavailable")
	store.complete(first.PlayerID, wantErr)
	second := store.receiveStarted(t)
	if second.PlayerID != playerID(2) {
		t.Fatalf("first Flush second ID=%s, want %s", second.PlayerID, playerID(2))
	}
	store.complete(second.PlayerID, nil)
	select {
	case err := <-firstFlush:
		if !errors.Is(err, wantErr) || err.Error() !=
			"save player 00000000-0000-4000-8000-000000000001 revision 8: disk unavailable" {
			t.Fatalf("first Flush error=%v, want %v", err, wantErr)
		}
	case <-time.After(waitDeadline):
		t.Fatal("first failed Flush did not return")
	}

	secondFlush := make(chan error, 1)
	go func() { secondFlush <- p.Flush(context.Background()) }()
	retry := store.receiveStarted(t)
	if retry.PlayerID != playerID(1) || !playerSavesEqual(retry, first) {
		t.Fatalf("healed Flush retry=%+v, want frozen=%+v", retry, first)
	}
	store.complete(retry.PlayerID, nil)
	select {
	case err := <-secondFlush:
		if err != nil {
			t.Fatalf("healed second Flush error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("healed second Flush did not return")
	}
}

// 捕获：Flush 继承 Poll 已分派的多 ID in-flight 后在首错立即返回，遗留旧 completion 污染下次 Flush。
func TestPlayerFlushDrainsInheritedInflightBatchBeforeReturning(t *testing.T) {
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

	firstFlush := make(chan error, 1)
	go func() { firstFlush <- p.Flush(context.Background()) }()
	waitForPlayerFlushToWait(t, p)
	oneErr, twoErr := errors.New("one"), errors.New("two")
	store.complete(playerID(1), oneErr)
	select {
	case err := <-firstFlush:
		t.Fatalf("Flush returned before inherited ID two completion: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	store.complete(playerID(2), twoErr)
	select {
	case err := <-firstFlush:
		want := "save player 00000000-0000-4000-8000-000000000001 revision 8: one\n" +
			"save player 00000000-0000-4000-8000-000000000002 revision 8: two"
		if !errors.Is(err, oneErr) || !errors.Is(err, twoErr) || err.Error() != want {
			t.Fatalf("inherited batch error=%q, want deterministic %q", err, want)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Flush did not return after inherited in-flight barrier completed")
	}

	secondFlush := make(chan error, 1)
	go func() { secondFlush <- p.Flush(context.Background()) }()
	for _, id := range []core.PlayerID{playerID(1), playerID(2)} {
		retry := store.receiveStarted(t)
		if retry.PlayerID != id {
			t.Fatalf("healed retry PlayerID=%s, want %s", retry.PlayerID, id)
		}
		store.complete(id, nil)
	}
	select {
	case err := <-secondFlush:
		if err != nil {
			t.Fatalf("healed Flush observed stale completion: %v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("healed Flush did not finish")
	}
}

// 捕获：inherited batch 有失败时漏掉成功 ID 的 latest follow-up，或把其 completion 留给下次 Flush。
func TestPlayerFlushInheritedFailureDoesNotDispatchForcedFollowup(t *testing.T) {
	p, store := newTwoInflightPlayerPersistence(t)
	if err := p.Observe(playerID(1), "B", testPlayerSnapshot(20), 6000, true); err != nil {
		t.Fatal(err)
	}

	flushed := make(chan error, 1)
	go func() { flushed <- p.Flush(context.Background()) }()
	waitForPlayerFlushToWait(t, p)
	store.complete(playerID(1), nil)
	wantErr := errors.New("two failed")
	store.complete(playerID(2), wantErr)
	followup := store.receiveStarted(t)
	if followup.PlayerID != playerID(1) || followup.Revision != 9 ||
		followup.Current.Position != [3]float32{20, 70, -20} {
		t.Fatalf("same-Flush forced follow-up=%+v", followup)
	}
	store.complete(playerID(1), nil)
	select {
	case err := <-flushed:
		want := "save player 00000000-0000-4000-8000-000000000002 revision 8: two failed"
		if !errors.Is(err, wantErr) || err.Error() != want {
			t.Fatalf("inherited Flush error=%q, want %q", err, want)
		}
	case <-time.After(waitDeadline):
		t.Fatal("inherited Flush did not return after complete batch")
	}
	store.assertNoStart(t)

	healed := make(chan error, 1)
	go func() { healed <- p.Flush(context.Background()) }()
	retry := store.receiveStarted(t)
	if retry.PlayerID != playerID(2) || retry.Revision != 8 {
		t.Fatalf("healed retry=%+v, want ID2 revision 8", retry)
	}
	store.complete(playerID(2), nil)
	select {
	case err := <-healed:
		if err != nil {
			t.Fatalf("healed Flush error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("healed Flush did not finish")
	}
}

// 捕获：等待 inherited peer 时 ctx cancel，已收集成功 completion 仍自动分派 forced follow-up。
func TestPlayerFlushCanceledInheritedBatchDoesNotDispatchFollowup(t *testing.T) {
	p, store := newTwoInflightPlayerPersistence(t)
	if err := p.Observe(playerID(1), "B", testPlayerSnapshot(20), 6000, true); err != nil {
		t.Fatal(err)
	}
	store.complete(playerID(1), nil)
	waitForPlayerSaveCompletionDepth(t, p.completions, 1)
	ctx, cancel := context.WithCancel(context.Background())
	flushed := make(chan error, 1)
	go func() { flushed <- p.Flush(ctx) }()
	waitForPlayerFlushToWait(t, p)
	waitForPlayerSaveCompletionDepth(t, p.completions, 0)
	cancel()
	select {
	case err := <-flushed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled inherited Flush error=%v, want context.Canceled", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("canceled inherited Flush did not return")
	}
	store.assertNoStart(t)
	store.complete(playerID(2), nil)
}

// 捕获：相同 PlayerID 的旧 revision completion 被当作 inherited identity，提前释放 barrier
// 并篡改当前 in-flight generation。
func TestPlayerFlushInheritedBarrierRejectsForeignRevision(t *testing.T) {
	p, store := newTwoInflightPlayerPersistence(t)
	flushed := make(chan error, 1)
	go func() { flushed <- p.Flush(context.Background()) }()
	waitForPlayerFlushToWait(t, p)
	p.completions <- playerSaveCompletion{
		Job: playerSaveJob{Save: storage.PlayerSave{
			PlayerID: playerID(1),
			Revision: 7,
		}},
		Err: errors.New("foreign old revision"),
	}
	wantErr := errors.New("two failed")
	store.complete(playerID(2), wantErr)
	select {
	case err := <-flushed:
		t.Fatalf("Flush accepted foreign revision before exact ID1 completion: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	store.complete(playerID(1), nil)
	select {
	case err := <-flushed:
		want := "save player 00000000-0000-4000-8000-000000000002 revision 8: two failed"
		if !errors.Is(err, wantErr) || errors.Is(err, context.Canceled) || err.Error() != want {
			t.Fatalf("exact inherited batch error=%q, want %q", err, want)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Flush did not return after exact inherited completion")
	}
}

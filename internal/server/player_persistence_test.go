package server

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

// 捕获：Prepare 将缺失玩家提前标为 dirty，或把默认快照错误地暴露为恢复位置。
func TestPlayerPersistencePrepareMissingIsSpawnOnlyBeforeConfirm(t *testing.T) {
	store := newControllablePlayerStore()
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	restored, err := p.Prepare(
		context.Background(), playerID(1), "A", testMetadata(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Current != nil || restored.Safe != nil ||
		restored.SpawnDimension != core.Overworld ||
		restored.SpawnAnchor != (core.ChunkPos{X: 2, Z: -3}) {
		t.Fatalf("missing restore=%+v, want spawn-only configured restore", restored)
	}
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)
}

// 捕获：loaded 值的 storage→sim 转换遗漏字段，或 Safe/restore 与 cache 共享可变位置。
func TestPlayerPersistencePrepareLoadedConvertsAndCopies(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(2)
	safe := storage.PlayerLocation{
		Dimension: core.Overworld,
		Position:  [3]float32{4, 65, -6},
	}
	store.loaded[id] = storage.StoredPlayer{
		PlayerID:    id,
		Revision:    7,
		DisplayName: "Persisted",
		Current: storage.PlayerLocation{
			Dimension: core.Overworld,
			Position:  [3]float32{8, 70, -9},
		},
		Yaw: 0.75, Pitch: -0.25, Safe: &safe,
	}
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	restored, err := p.Prepare(context.Background(), id, "Candidate", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Current == nil || restored.Safe == nil ||
		restored.Current.Dimension != core.Overworld ||
		restored.Current.Position != (mgl32.Vec3{8, 70, -9}) ||
		restored.Safe.Position != (mgl32.Vec3{4, 65, -6}) ||
		restored.Yaw != 0.75 || restored.Pitch != -0.25 {
		t.Fatalf("loaded restore=%+v", restored)
	}

	safe.Position[0] = 999
	restored.Current.Position[0] = 998
	restored.Safe.Position[1] = 997
	again, err := p.Prepare(context.Background(), id, "Candidate", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if again.Current == nil || again.Safe == nil ||
		again.Current.Position != (mgl32.Vec3{8, 70, -9}) ||
		again.Safe.Position != (mgl32.Vec3{4, 65, -6}) {
		t.Fatalf("cached restore after caller/source mutation=%+v", again)
	}
}

// 捕获：迁移后的 StoredPlayer.NeedsRewrite 被当作 clean，导致没有昵称或快照变化时永不重写。
func TestPlayerPersistenceAutosavesLoadedRewriteWithoutNicknameChange(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(21)
	stored := storedPlayerForTest(id, 7, "Persisted", testPlayerSnapshot(3))
	stored.NeedsRewrite = true
	store.loaded[id] = stored
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "Persisted", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.PlayerID != id || save.Revision != 8 || save.DisplayName != "Persisted" ||
		save.Current.Position != [3]float32{3, 70, -3} ||
		save.Safe == nil || save.Safe.Position != [3]float32{2, 64, -3} {
		t.Fatalf("rewrite SavePlayer=%+v", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
}

// 捕获：Confirm 没有将缺失身份标为 dirty、提前于 autosave 调度，或以错误 revision/默认位置保存。
func TestPlayerPersistenceConfirmMakesMissingPlayerPersistableOnAutosave(t *testing.T) {
	store := newControllablePlayerStore()
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	id := playerID(3)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(id, "A"); err != nil {
		t.Fatal(err)
	}
	p.Confirm(id)

	if err := p.Poll(5999); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.PlayerID != id || save.Revision != 1 || save.DisplayName != "A" ||
		save.Current.Dimension != core.Overworld ||
		save.Current.Position != [3]float32{32.5, float32(core.MaxY + 1), -47.5} ||
		save.Safe != nil || save.Yaw != 0 || save.Pitch != 0 {
		t.Fatalf("confirmed missing SavePlayer=%+v", save)
	}
	store.complete(nil)
	if err := p.Poll(6001); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)
}

// 捕获：missing 身份在 Confirm 前把候选昵称或 transient snapshot 通过 force/autosave/Flush/Abort 写入 Store。
func TestPlayerPersistenceMissingPlayerDoesNotPersistBeforeConfirm(t *testing.T) {
	t.Run("force", func(t *testing.T) {
		store := newControllablePlayerStore()
		id := playerID(27)
		p := newPlayerPersistence(store, playerPersistenceTestConfig())
		t.Cleanup(p.CloseWorker)
		if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := p.Activate(id, "Candidate"); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(id, "Candidate", testPlayerSnapshot(10), 20, true); err != nil {
			t.Fatal(err)
		}
		assertNoPlayerSaveStarted(t, store)
	})

	t.Run("autosave", func(t *testing.T) {
		store := newControllablePlayerStore()
		id := playerID(28)
		p := newPlayerPersistence(store, playerPersistenceTestConfig())
		t.Cleanup(p.CloseWorker)
		if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := p.Activate(id, "Candidate"); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(id, "Candidate", testPlayerSnapshot(10), 20, false); err != nil {
			t.Fatal(err)
		}
		if err := p.Poll(6000); err != nil {
			t.Fatal(err)
		}
		assertNoPlayerSaveStarted(t, store)
	})

	t.Run("flush", func(t *testing.T) {
		store := newControllablePlayerStore()
		id := playerID(29)
		p := newPlayerPersistence(store, playerPersistenceTestConfig())
		t.Cleanup(p.CloseWorker)
		if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := p.Activate(id, "Candidate"); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(id, "Candidate", testPlayerSnapshot(10), 20, false); err != nil {
			t.Fatal(err)
		}
		flushed := make(chan error, 1)
		go func() { flushed <- p.Flush(context.Background()) }()
		select {
		case err := <-flushed:
			if err != nil {
				t.Fatal(err)
			}
		case save := <-store.saveStarted:
			t.Fatalf("Flush persisted unconfirmed missing player: %+v", save)
		case <-time.After(time.Second):
			t.Fatal("Flush did not return for clean unconfirmed missing player")
		}
	})

	t.Run("abort", func(t *testing.T) {
		store := newControllablePlayerStore()
		id := playerID(30)
		p := newPlayerPersistence(store, playerPersistenceTestConfig())
		t.Cleanup(p.CloseWorker)
		if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := p.Activate(id, "Candidate"); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(id, "Candidate", testPlayerSnapshot(10), 20, false); err != nil {
			t.Fatal(err)
		}
		p.Abort(id)
		if err := p.Poll(6000); err != nil {
			t.Fatal(err)
		}
		assertNoPlayerSaveStarted(t, store)
	})

	t.Run("clean cache can switch identity", func(t *testing.T) {
		store := newControllablePlayerStore()
		idA, idB := playerID(33), playerID(34)
		p := newPlayerPersistence(store, playerPersistenceTestConfig())
		t.Cleanup(p.CloseWorker)
		if _, err := p.Prepare(context.Background(), idA, "Candidate", testMetadata()); err != nil {
			t.Fatal(err)
		}
		if err := p.Observe(idA, "Candidate", testPlayerSnapshot(10), 20, true); err != nil {
			t.Fatal(err)
		}
		restored, err := p.Prepare(context.Background(), idB, "B", testMetadata())
		if err != nil || restored.Current != nil || restored.Safe != nil {
			t.Fatalf("clean missing identity switch restore=%+v err=%v", restored, err)
		}
		assertNoPlayerSaveStarted(t, store)
	})
}

// 捕获：Confirm 遗漏 Confirm 前已缓存的最新 transient snapshot，或未用确认后的昵称/revision=1 保存它。
func TestPlayerPersistenceConfirmPersistsLatestMissingSnapshot(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(31)
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(id, "Candidate"); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "Candidate", testPlayerSnapshot(10), 20, false); err != nil {
		t.Fatal(err)
	}
	p.Confirm(id)
	p.Confirm(id)
	p.mu.Lock()
	activeAfterConfirm := p.cache[id] != nil && p.cache[id].active
	p.mu.Unlock()
	if !activeAfterConfirm {
		t.Fatal("Confirm cleared active before the session exit lifecycle")
	}
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.PlayerID != id || save.Revision != 1 || save.DisplayName != "Candidate" ||
		save.Current.Position != [3]float32{10, 70, -10} ||
		save.Safe == nil || save.Safe.Position != [3]float32{9, 64, -10} {
		t.Fatalf("confirmed missing SavePlayer=%+v, want latest snapshot and candidate name", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
	p.Deactivate(id)
}

// 捕获：Abort 保留了 staged nickname，或 Observe 在 Confirm 前错误地提交传入 nickname。
func TestPlayerPersistenceAbortDoesNotPersistStagedNickname(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(4)
	store.loaded[id] = storedPlayerForTest(id, 7, "Persisted", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(id, "Candidate"); err != nil {
		t.Fatal(err)
	}
	p.Abort(id)

	wantSnapshot := testPlayerSnapshot(11)
	if err := p.Observe(id, "Candidate", wantSnapshot, 20, true); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.PlayerID != id || save.Revision != 8 || save.DisplayName != "Persisted" ||
		save.Current.Dimension != core.Overworld ||
		save.Current.Position != [3]float32{11, 70, -11} ||
		save.Safe == nil || save.Safe.Dimension != core.Overworld ||
		save.Safe.Position != [3]float32{10, 64, -11} ||
		save.Yaw != 1.1 || save.Pitch != -0.55 {
		t.Fatalf("aborted nickname SavePlayer=%+v", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 21)
}

// 捕获：Confirm 忽略已加载玩家的 nickname 变化，或错误地更换玩家 ID/revision 基线。
func TestPlayerPersistenceConfirmPersistsStagedNicknameWithoutChangingIdentity(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(5)
	store.loaded[id] = storedPlayerForTest(id, 7, "Persisted", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(id, "Candidate"); err != nil {
		t.Fatal(err)
	}
	p.Confirm(id)
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.PlayerID != id || save.Revision != 8 || save.DisplayName != "Candidate" ||
		save.Current.Position != [3]float32{3, 70, -3} ||
		save.Safe == nil || save.Safe.Position != [3]float32{2, 64, -3} ||
		save.Yaw != 0.3 || save.Pitch != -0.15 {
		t.Fatalf("confirmed nickname SavePlayer=%+v", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
}

// 捕获：Confirm 被重复调用时再次消费已清空的 pendingName，把已确认的昵称写成空字符串。
func TestPlayerPersistenceConfirmConsumesActivationOnlyOnce(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(23)
	store.loaded[id] = storedPlayerForTest(id, 7, "Persisted", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "Candidate", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(id, "Candidate"); err != nil {
		t.Fatal(err)
	}
	p.Confirm(id)
	p.Confirm(id)
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.DisplayName != "Candidate" || save.Revision != 8 {
		t.Fatalf("repeated Confirm SavePlayer=%+v, want confirmed nickname", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
}

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

// 捕获：force=false 的 Observe 提前触发 I/O，或 Poll 未在 AutosaveTicks 边界分派最新快照。
func TestPlayerPersistenceObserveWithoutForceWaitsForAutosave(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(11)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Observe(id, "A", testPlayerSnapshot(10), 19, false); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)
	if err := p.Poll(5999); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.Revision != 8 || save.Current.Position != [3]float32{10, 70, -10} {
		t.Fatalf("autosave SavePlayer=%+v", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
}

// 捕获：Observe 保留调用者的 PlayerSnapshot.Safe 指针，导致调用者后续突变污染落盘值。
func TestPlayerPersistenceObserveCopiesCallerSnapshot(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(20)
	store.loaded[id] = storedPlayerForTest(id, 7, "A", testPlayerSnapshot(3))
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "A", testMetadata()); err != nil {
		t.Fatal(err)
	}
	snapshot := testPlayerSnapshot(10)
	if err := p.Observe(id, "A", snapshot, 19, false); err != nil {
		t.Fatal(err)
	}
	snapshot.Current.Position[0] = 999
	snapshot.Safe.Position[1] = 998
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.Current.Position != [3]float32{10, 70, -10} ||
		save.Safe == nil || save.Safe.Position != [3]float32{9, 64, -10} {
		t.Fatalf("caller-mutated snapshot leaked into SavePlayer=%+v", save)
	}
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
		t.Fatal("retrying Flush did not return")
	}
}

// 捕获：Flush 同时分派多个身份但在首错返回，给下次已修复的 Flush 留下旧失败 completion。
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
	select {
	case err := <-firstFlush:
		if !errors.Is(err, wantErr) {
			t.Fatalf("first Flush error=%v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("first failed Flush did not return")
	}

	secondFlush := make(chan error, 1)
	go func() { secondFlush <- p.Flush(context.Background()) }()
	retry := store.receiveStarted(t)
	if retry.PlayerID != playerID(1) || !playerSavesEqual(retry, first) {
		t.Fatalf("healed Flush retry=%+v, want frozen=%+v", retry, first)
	}
	store.complete(retry.PlayerID, nil)
	second := store.receiveStarted(t)
	if second.PlayerID != playerID(2) {
		t.Fatalf("healed Flush second ID=%s, want %s", second.PlayerID, playerID(2))
	}
	store.complete(second.PlayerID, nil)
	select {
	case err := <-secondFlush:
		if err != nil {
			t.Fatalf("healed second Flush error=%v", err)
		}
	case <-time.After(time.Second):
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
		if !errors.Is(err, oneErr) || !errors.Is(err, twoErr) || err.Error() != "one\ntwo" {
			t.Fatalf("inherited batch error=%q, want deterministic %q", err, "one\ntwo")
		}
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
		t.Fatal("healed Flush did not finish")
	}
}

// 捕获：inherited batch 中较小 ID 成功时立刻分派 forced follow-up，随后较大 ID 失败返回，
// 使 follow-up completion 跨越到下一次已修复的 Flush。
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
	select {
	case err := <-flushed:
		if !errors.Is(err, wantErr) || err.Error() != "two failed" {
			t.Fatalf("inherited Flush error=%q, want %q", err, "two failed")
		}
	case <-time.After(time.Second):
		t.Fatal("inherited Flush did not return after complete batch")
	}
	store.assertNoStart(t)

	healed := make(chan error, 1)
	go func() { healed <- p.Flush(context.Background()) }()
	followup := store.receiveStarted(t)
	if followup.PlayerID != playerID(1) || followup.Revision != 9 ||
		followup.Current.Position != [3]float32{20, 70, -20} {
		t.Fatalf("healed forced follow-up=%+v", followup)
	}
	store.complete(playerID(1), nil)
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
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
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
		if !errors.Is(err, wantErr) || errors.Is(err, context.Canceled) || err.Error() != "two failed" {
			t.Fatalf("exact inherited batch error=%q, want %q", err, "two failed")
		}
	case <-time.After(time.Second):
		t.Fatal("Flush did not return after exact inherited completion")
	}
}

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
	store.setOnSave(func() {
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
	_ = receivePlayerSave(t, store)
	select {
	case free := <-mutexFree:
		if !free {
			t.Fatal("worker held cache mutex during Store.SavePlayer")
		}
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
		t.Fatal("Abort did not finish after Prepare completed")
	}
	if returnedBeforeLoadCompleted {
		t.Fatal("Abort returned before blocked Prepare committed its staged cache")
	}
	if err := p.Activate(id, "Candidate"); !errors.Is(err, ErrPlayerPersistenceBackpressure) {
		t.Fatalf("Activate after concurrent Abort error=%v, want backpressure", err)
	}
}

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
		FormatVersion:  1,
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
	case <-time.After(time.Second):
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
		case <-time.After(time.Second):
			t.Fatalf("queued jobs ended at %d, want %d in PlayerID order", index, len(want))
		}
	}
}

func receivePlayerLoadStarted(t *testing.T, store *controllablePlayerStore) core.PlayerID {
	t.Helper()
	select {
	case id := <-store.loadStarted:
		return id
	case <-time.After(time.Second):
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
	case <-time.After(time.Second):
		t.Fatal("Prepare did not return after LoadPlayer was released")
		return playerPrepareResult{}
	}
}

func pollPlayerPersistenceUntilIdle(t *testing.T, p *playerPersistence, tick uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
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
	deadline := time.Now().Add(time.Second)
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
	deadline := time.Now().Add(time.Second)
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
	deadline := time.Now().Add(time.Second)
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

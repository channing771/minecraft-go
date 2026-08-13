package server

import (
	"context"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
)

func wantStarterMaterialInventory() core.Inventory {
	items := [...]core.ItemID{
		core.ItemCobblestone, core.ItemSmoothStone, core.ItemSand, core.ItemGravel,
		core.ItemOakLog, core.ItemOakPlanks, core.ItemLeaves, core.ItemGlass,
		core.ItemBrick, core.ItemWhiteWool, core.ItemRoofTile, core.ItemClay,
		core.ItemSnowBlock, core.ItemMossyCobblestone,
	}
	var inventory core.Inventory
	for slot, item := range items {
		inventory.Backpack[slot] = core.ItemStack{Item: item, Count: core.MaxStackCount}
	}
	return inventory
}

// 捕获：缺失玩家的初始材料包没有通过 Prepare 交给模拟注册流程。
func TestPlayerPersistencePrepareMissingProvidesStarterMaterialInventory(t *testing.T) {
	store := newControllablePlayerStore()
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	restored, err := p.Prepare(context.Background(), playerID(35), "Starter", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Current != nil || restored.Safe != nil {
		t.Fatalf("missing restore exposed position: %+v", restored)
	}
	if restored.Inventory != wantStarterMaterialInventory() || restored.Inventory.Hotbar != (core.Hotbar{}) {
		t.Fatalf("missing restore inventory=%+v, want fixed starter materials and empty hotbar", restored.Inventory)
	}
}

// 捕获：已有玩家在加载时被错误地替换为初始材料包。
func TestPlayerPersistencePrepareExistingKeepsCustomInventory(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(36)
	want := core.Inventory{}
	want.Hotbar.Slots[2] = core.ItemStack{Item: core.ItemStone, Count: 7}
	want.Backpack[5] = core.ItemStack{Item: core.ItemGlass, Count: 3}
	stored := storedPlayerForTest(id, 7, "Existing", testPlayerSnapshot(3))
	stored.Inventory = want
	store.loaded[id] = stored
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	restored, err := p.Prepare(context.Background(), id, "Existing", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inventory != want {
		t.Fatalf("existing restore inventory=%+v, want=%+v", restored.Inventory, want)
	}
}

// 捕获：未确认的缺失玩家在断开后保存材料包，或再次 Prepare 时重复累加材料。
func TestPlayerPersistenceMissingStarterDoesNotPersistBeforeConfirm(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(37)
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)

	restored, err := p.Prepare(context.Background(), id, "Candidate", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inventory != wantStarterMaterialInventory() {
		t.Fatalf("first missing inventory=%+v", restored.Inventory)
	}
	if err := p.Activate(id, "Candidate"); err != nil {
		t.Fatal(err)
	}
	p.Abort(id)
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	assertNoPlayerSaveStarted(t, store)

	again, err := p.Prepare(context.Background(), id, "Candidate", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if again.Inventory != wantStarterMaterialInventory() {
		t.Fatalf("reprepared missing inventory=%+v, want one starter material set", again.Inventory)
	}
}

// 捕获：确认后的初始材料包未保存，或重载时又被重新补发。
func TestPlayerPersistenceConfirmPersistsStarterMaterialInventoryOnce(t *testing.T) {
	store := newControllablePlayerStore()
	id := playerID(38)
	p := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(p.CloseWorker)
	if _, err := p.Prepare(context.Background(), id, "Starter", testMetadata()); err != nil {
		t.Fatal(err)
	}
	if err := p.Activate(id, "Starter"); err != nil {
		t.Fatal(err)
	}
	p.Confirm(id)
	if err := p.Poll(6000); err != nil {
		t.Fatal(err)
	}
	save := receivePlayerSave(t, store)
	if save.Inventory != wantStarterMaterialInventory() {
		t.Fatalf("confirmed save inventory=%+v", save.Inventory)
	}
	store.mu.Lock()
	store.loaded[id] = storage.StoredPlayer{
		PlayerID:    save.PlayerID,
		Revision:    save.Revision,
		DisplayName: save.DisplayName,
		Current:     save.Current,
		Yaw:         save.Yaw,
		Pitch:       save.Pitch,
		Safe:        save.Safe,
		Inventory:   save.Inventory,
		Health:      save.Health,
	}
	store.mu.Unlock()
	store.complete(nil)
	pollPlayerPersistenceUntilIdle(t, p, 6001)

	reloaded := newPlayerPersistence(store, playerPersistenceTestConfig())
	t.Cleanup(reloaded.CloseWorker)
	restored, err := reloaded.Prepare(context.Background(), id, "Starter", testMetadata())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Inventory != wantStarterMaterialInventory() {
		t.Fatalf("reloaded inventory=%+v, want unchanged starter material set", restored.Inventory)
	}
}

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
		case <-time.After(waitDeadline):
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

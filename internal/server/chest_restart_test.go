package server_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

// TestChestRestartRestoresItemsCountsAndDurability 覆盖 schema v5 箱子的重启闭环：
// 27 格物品、数量与其中一件磨损工具的耐久必须原值恢复，不因正常关服重开而漂移。
func TestChestRestartRestoresItemsCountsAndDurability(t *testing.T) {
	root := t.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld}
	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("箱子位置没有区块索引")
	}
	full, ok := core.ItemMaxDurability(core.ItemStonePickaxe)
	if !ok {
		t.Fatal("石镐没有最大耐久")
	}
	want := world.ChestSlot{
		Generation: 5, Active: true, BlockIndex: index,
	}
	want.Items[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full - 37}
	want.Items[1] = core.ItemStack{Item: core.ItemStone, Count: 42}
	want.Items[26] = core.ItemStack{Item: core.ItemCoal, Count: 3}

	first, firstStore, firstClient := newDropDiskWorld(t, root)
	step := stepUntilDropReady(t, first, firstClient)
	first.SetBlockForTest(core.BlockPos{}, core.ChestID)
	first.SetChunkChestForTest(key, 0, want)
	step()
	flushDropWorld(t, first, firstStore)

	second, secondStore, secondClient := newDropDiskWorld(t, root)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		if err := second.Shutdown(ctx); err != nil {
			t.Errorf("second Shutdown: %v", err)
		}
		if err := secondStore.Close(); err != nil {
			t.Errorf("second store Close: %v", err)
		}
	}()
	restart := stepUntilDropReady(t, second, secondClient)

	deadline := time.Now().Add(waitDeadline)
	var restored world.ChestSlot
	for !restored.Active {
		if time.Now().After(deadline) {
			t.Fatal("等待重启后加载箱子区块超时")
		}
		restart()
		chunk, _, ok := second.CloneReadyChunkForTest(key)
		if !ok {
			continue
		}
		restored = chunk.Chest(0)
	}
	if restored != want {
		t.Fatalf("重启后箱子 = %+v，想要 %+v", restored, want)
	}
}

// TestChestSaveFailureRetainsOldRecordUntilRetrySucceeds 注入一次箱子所在区块的
// 保存失败：失败期间磁盘上的旧完整记录必须仍然可以被原样读回（不残留半份写入），
// 只有故障恢复并重试成功后，磁盘内容才会整体更新为新值。
func TestChestSaveFailureRetainsOldRecordUntilRetrySucceeds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "world")
	key := persistenceChunkKey(core.ChunkPos{})
	index, ok := world.ChunkBlockIndex(core.BlockPos{})
	if !ok {
		t.Fatal("箱子位置没有区块索引")
	}
	full, ok := core.ItemMaxDurability(core.ItemStonePickaxe)
	if !ok {
		t.Fatal("石镐没有最大耐久")
	}
	oldSlot := world.ChestSlot{Generation: 1, Active: true, BlockIndex: index}
	oldSlot.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	newSlot := oldSlot
	newSlot.Items[0] = core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: full - 7}

	generator := newCountingPersistenceGenerator(core.StoneID)

	// 阶段一：正常落盘，建立磁盘上的旧完整记录。
	first := newPersistentHarness(t, root, generator, false, nil)
	first.waitReady()
	first.running.SetBlockForTest(core.BlockPos{}, core.ChestID)
	first.running.SetChunkChestForTest(key, 0, oldSlot)
	first.stepUntil(func() bool {
		chunk, _, ok := first.running.CloneReadyChunkForTest(key)
		return ok && chunk.Chest(0).Active
	})
	if err := first.shutdown(); err != nil {
		t.Fatalf("首次落盘: %v", err)
	}

	// 阶段二：重开时包一层可注入失败的 store，写入新值后触发一次保存失败。
	diskFull := errors.New("injected chest save failure")
	var failing *recoverablePersistenceStore
	second := newPersistentHarness(t, root, generator, false, func(store storage.Store) storage.Store {
		failing = &recoverablePersistenceStore{Store: store, err: diskFull, failing: true}
		return failing
	})
	t.Cleanup(failing.recover)
	second.waitReady()

	restoredOld, _, ok := second.running.CloneReadyChunkForTest(key)
	if !ok || restoredOld.Chest(0) != oldSlot {
		t.Fatalf("重开后旧内容 = %+v，想要 %+v", restoredOld.Chest(0), oldSlot)
	}
	second.running.SetChunkChestForTest(key, 0, newSlot)
	// SetChunkChestForTest 只是原始状态覆写，不会像真实容器命令那样触碰区块 revision；
	// 显式标脏后 Shutdown 才会真正尝试保存新值。
	second.running.TouchChunkForTest(key)

	shutdownErr := second.shutdown()
	if !errors.Is(shutdownErr, diskFull) {
		t.Fatalf("注入失败后 Shutdown error=%v，想要 %v", shutdownErr, diskFull)
	}

	// 失败期间磁盘上的记录必须仍是旧值、完整可读。
	stillOld, err := failing.LoadChunk(context.Background(), key)
	if err != nil {
		t.Fatalf("失败期间读取磁盘记录: %v", err)
	}
	if got := stillOld.Chunk.Chest(0); got != oldSlot {
		t.Fatalf("失败期间磁盘记录 = %+v，想要保持旧值 %+v", got, oldSlot)
	}

	// 恢复并重试：这一次必须成功整体更新为新值。
	failing.recover()
	if err := second.shutdown(); err != nil {
		t.Fatalf("重试 Shutdown: %v", err)
	}

	readBack := openPersistentDiskStore(t, root)
	defer func() {
		if err := readBack.Close(); err != nil {
			t.Errorf("readBack Close: %v", err)
		}
	}()
	stored, err := readBack.LoadChunk(context.Background(), key)
	if err != nil {
		t.Fatalf("重试后读取磁盘记录: %v", err)
	}
	if got := stored.Chunk.Chest(0); got != newSlot {
		t.Fatalf("重试后磁盘记录 = %+v，想要 %+v", got, newSlot)
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

func TestMigrateNaturalMaterialsPreservesOtherBlocksAndPayloads(t *testing.T) {
	seed := int64(42)
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -2, Z: 3}}
	chunk := world.NewChunk(key.Pos)
	natural := []core.BlockID{
		core.StoneID,
		core.DirtID,
		core.GrassID,
		core.SandID,
		core.GravelID,
		core.ClayID,
		core.SnowBlockID,
	}
	for index, block := range natural {
		chunk.SetBlock(index, core.MinY, 0, block)
	}

	other := []core.BlockID{
		core.AirID,
		core.CoalOreID,
		core.IronOreID,
		core.ChestID,
		core.FurnaceID,
		core.BrickID,
	}
	for index, block := range other {
		chunk.SetBlock(index, core.MinY+1, 1, block)
	}

	furnacePosition := blockPosition(key.Pos, 4, core.MinY+1, 1)
	furnaceIndex, ok := world.ChunkBlockIndex(furnacePosition)
	if !ok {
		t.Fatal("熔炉位置无法转换为区块索引")
	}
	chunk.SetFurnace(0, world.FurnaceSlot{
		Generation:    4,
		Active:        true,
		BlockIndex:    furnaceIndex,
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 3},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 1},
		ProgressTicks: 17,
		BurnTicks:     83,
	})
	chestPosition := blockPosition(key.Pos, 3, core.MinY+1, 1)
	chestIndex, ok := world.ChunkBlockIndex(chestPosition)
	if !ok {
		t.Fatal("箱子位置无法转换为区块索引")
	}
	var chestItems [core.ChestSlots]core.ItemStack
	chestItems[0] = core.ItemStack{Item: core.ItemBrick, Count: 9}
	chunk.SetChest(0, world.ChestSlot{
		Generation: 7,
		Active:     true,
		BlockIndex: chestIndex,
		Items:      chestItems,
	})
	chunk.SetDrop(0, world.DropSlot{
		Generation:       5,
		Active:           true,
		Stack:            core.ItemStack{Item: core.ItemStoneBrick, Count: 6},
		BlockIndex:       chestIndex,
		AgeTicks:         41,
		PickupDelayTicks: 3,
	})

	inputBefore := chunk.Clone()
	want := chunk.Clone()
	generator := worldgen.New(seed)
	for index := range natural {
		position := blockPosition(key.Pos, index, core.MinY, 0)
		want.SetBlock(index, core.MinY, 0, generator.BaseBlockAt(position))
	}

	candidate, changed, err := migrateNaturalMaterials(generator, storage.StoredChunk{
		Key: key, Revision: 8, PersistedRevision: 8, Chunk: chunk,
	})
	if err != nil {
		t.Fatalf("迁移自然材料失败: %v", err)
	}
	if !changed {
		t.Fatal("混合旧区块未报告变化")
	}
	if candidate.Key != key || candidate.Revision != 9 {
		t.Fatalf("迁移候选 key/revision = %+v/%d，期望 %+v/9", candidate.Key, candidate.Revision, key)
	}
	if !reflect.DeepEqual(candidate.Chunk, want) {
		t.Fatal("迁移结果未限定为七种自然值，或改变了非方块负载")
	}
	if !reflect.DeepEqual(chunk, inputBefore) {
		t.Fatal("迁移函数原地修改了输入区块")
	}
}

func TestMigrateNaturalMaterialsKeepsRevisionWhenUnchanged(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: -1}}
	generator := worldgen.New(42)
	chunk := generator.GenerateChunk(key.Pos)

	candidate, changed, err := migrateNaturalMaterials(generator, storage.StoredChunk{
		Key: key, Revision: 13, PersistedRevision: 13, Chunk: chunk,
	})
	if err != nil {
		t.Fatalf("迁移已是新规则的区块失败: %v", err)
	}
	if changed {
		t.Fatal("已是新规则的区块被报告为变化")
	}
	if candidate.Revision != 13 {
		t.Fatalf("无变化候选 revision = %d，期望 13", candidate.Revision)
	}
	if !reflect.DeepEqual(candidate.Chunk, chunk) {
		t.Fatal("无变化迁移改变了区块")
	}
}

func TestMaterialMigrationRealDiskRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	backupPath := filepath.Join(root, "world-before-materials")
	generator := worldgen.New(42)
	changedKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: 3}}
	unchangedKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: -2}}
	otherDimensionKey := core.ChunkKey{Dimension: 1, Pos: core.ChunkPos{X: -4, Z: 5}}
	changedBefore, changedAfter := materialMigrationDiskFixture(t, generator, changedKey)
	unchanged := generator.GenerateChunk(unchangedKey.Pos)
	otherDimension := world.NewChunk(otherDimensionKey.Pos)
	otherDimension.SetBlock(0, core.MinY, 0, core.StoneID)

	store, err := storage.OpenDisk(ctx, worldPath, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2,
		Seed:          42,
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.SaveBatch(ctx, []storage.ChunkSave{
		{Key: changedKey, Revision: 7, Chunk: changedBefore},
		{Key: unchangedKey, Revision: 11, Chunk: unchanged},
		{Key: otherDimensionKey, Revision: 13, Chunk: otherDimension},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(store.Sync(ctx), store.Close()); err != nil {
		t.Fatal(err)
	}

	if err := migrateMaterials(ctx, worldPath, backupPath); err != nil {
		t.Fatalf("运行真实磁盘材料迁移失败: %v", err)
	}
	state := readMaterialMigrationStateForTest(t, worldPath)
	if !state.Complete {
		t.Fatal("真实磁盘迁移未写入完成状态")
	}

	assertMaterialMigrationDiskChunk(t, backupPath, changedKey, 7, changedBefore)
	assertMaterialMigrationDiskChunk(t, backupPath, unchangedKey, 11, unchanged)
	assertMaterialMigrationDiskChunk(t, backupPath, otherDimensionKey, 13, otherDimension)
	assertMaterialMigrationDiskChunk(t, worldPath, changedKey, 8, changedAfter)
	assertMaterialMigrationDiskChunk(t, worldPath, unchangedKey, 11, unchanged)
	assertMaterialMigrationDiskChunk(t, worldPath, otherDimensionKey, 13, otherDimension)

	if err := migrateMaterials(ctx, worldPath, backupPath); err != nil {
		t.Fatalf("重复运行真实磁盘材料迁移失败: %v", err)
	}
	assertMaterialMigrationDiskChunk(t, worldPath, changedKey, 8, changedAfter)
}

func TestMaterialMigrationRealDiskRetriesProgressFailureWithoutSecondRevision(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	worldPath := filepath.Join(root, "world")
	backupPath := filepath.Join(root, "world-before-materials")
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: -2}}
	chunk := world.NewChunk(key.Pos)
	chunk.SetBlock(0, core.MinY, 0, core.StoneID)

	store, err := storage.OpenDisk(ctx, worldPath, storage.OpenOptions{Create: storage.Metadata{
		FormatVersion: 2,
		Seed:          42,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveBatch(ctx, []storage.ChunkSave{{
		Key: key, Revision: 5, Chunk: chunk,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(store.Sync(ctx), store.Close()); err != nil {
		t.Fatal(err)
	}

	store, err = storage.OpenDisk(ctx, worldPath, storage.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected real disk progress rename failure")
	err = runMaterialMigration(
		ctx, store, worldPath, backupPath, store.Backup,
		materialMigrationFS{rename: func(string, string) error { return injected }},
	)
	if !errors.Is(err, injected) {
		t.Fatalf("真实磁盘首次迁移错误 = %v，期望 %v", err, injected)
	}
	if _, err := os.Stat(filepath.Join(worldPath, materialMigrationStateName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("进度失败后不应有正式状态，Stat 错误: %v", err)
	}
	want := chunk.Clone()
	want.SetBlock(0, core.MinY, 0, worldgen.New(42).BaseBlockAt(blockPosition(key.Pos, 0, core.MinY, 0)))
	assertMaterialMigrationDiskChunk(t, worldPath, key, 6, want)

	if err := migrateMaterials(ctx, worldPath, backupPath); err != nil {
		t.Fatalf("真实磁盘进度失败后重试迁移失败: %v", err)
	}
	assertMaterialMigrationDiskChunk(t, worldPath, key, 6, want)
	if !readMaterialMigrationStateForTest(t, worldPath).Complete {
		t.Fatal("真实磁盘进度失败后重试未完成")
	}
}

func TestMaterialMigrationUsesStableOverworldBatches(t *testing.T) {
	root := t.TempDir()
	backupPath := root + string(filepath.Separator) + "." + string(filepath.Separator) + "backup"
	store := newMaterialMigrationTestStore(42)
	for x := int32(33); x >= 0; x-- {
		key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: x}}
		chunk := world.NewChunk(key.Pos)
		if x == 0 || x == 33 {
			chunk.SetBlock(0, core.MinY, 0, core.StoneID)
		}
		store.addChunk(key, uint64(100+x), chunk)
		store.keys = append(store.keys, key)
	}
	otherDimension := core.ChunkKey{Dimension: 1, Pos: core.ChunkPos{X: -5}}
	otherChunk := world.NewChunk(otherDimension.Pos)
	otherChunk.SetBlock(0, core.MinY, 0, core.StoneID)
	store.addChunk(otherDimension, 9, otherChunk)
	store.keys = append([]core.ChunkKey{otherDimension}, store.keys...)

	var writtenStates []materialMigrationState
	fs := materialMigrationFS{rename: func(oldPath, newPath string) error {
		data, err := os.ReadFile(oldPath)
		if err != nil {
			return err
		}
		var state materialMigrationState
		if err := json.Unmarshal(data, &state); err != nil {
			return err
		}
		writtenStates = append(writtenStates, state)
		return os.Rename(oldPath, newPath)
	}}
	backup := func(_ context.Context, got string) error {
		store.events = append(store.events, "backup")
		absolute, err := filepath.Abs(backupPath)
		if err != nil {
			return err
		}
		if got != absolute {
			return fmt.Errorf("备份路径 = %q，期望 %q", got, absolute)
		}
		return nil
	}

	if err := runMaterialMigration(context.Background(), store, root, backupPath, backup, fs); err != nil {
		t.Fatalf("运行材料迁移失败: %v", err)
	}
	backupIndex := slices.Index(store.events, "backup")
	keysIndex := slices.Index(store.events, "keys")
	if backupIndex < 0 || keysIndex < 0 || backupIndex >= keysIndex {
		t.Fatalf("迁移事件 = %v，备份必须先于区块枚举", store.events)
	}
	wantLoadKeys := make([]core.ChunkKey, 34)
	for x := range int32(34) {
		wantLoadKeys[x] = core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: x}}
	}
	if !slices.Equal(store.loadKeys, wantLoadKeys) {
		t.Fatalf("加载顺序 = %v，期望 %v", store.loadKeys, wantLoadKeys)
	}
	if len(store.saveCalls) != 2 || len(store.saveCalls[0]) != 1 || len(store.saveCalls[1]) != 1 {
		t.Fatalf("SaveBatch 调用 = %v，期望两个批次各保存一个变化区块", store.saveCalls)
	}
	if store.saveCalls[0][0].Key.Pos.X != 0 || store.saveCalls[0][0].Revision != 101 ||
		store.saveCalls[1][0].Key.Pos.X != 33 || store.saveCalls[1][0].Revision != 134 {
		t.Fatalf("保存候选 = %v，期望 X=0/rev101 与 X=33/rev134", store.saveCalls)
	}
	if store.syncCalls != 3 {
		t.Fatalf("Sync 调用 = %d，期望两个批次加完成前共 3 次", store.syncCalls)
	}
	if len(writtenStates) != 3 {
		t.Fatalf("状态写入次数 = %d，期望两个批次进度加完成状态", len(writtenStates))
	}
	assertMaterialMigrationState(t, writtenStates[0], 42, backupPath, 31, false)
	assertMaterialMigrationState(t, writtenStates[1], 42, backupPath, 33, false)
	assertMaterialMigrationState(t, writtenStates[2], 42, backupPath, 33, true)
	if store.closed != 1 {
		t.Fatalf("store.Close 调用 = %d，期望 1", store.closed)
	}
}

func TestMaterialMigrationRejectsPartialStateWhenBackupIsMissing(t *testing.T) {
	assertMaterialMigrationRejectsMissingExistingBackup(t, false)
}

func TestMaterialMigrationRejectsCompletedStateWhenBackupIsMissing(t *testing.T) {
	assertMaterialMigrationRejectsMissingExistingBackup(t, true)
}

func assertMaterialMigrationRejectsMissingExistingBackup(t *testing.T, complete bool) {
	t.Helper()
	root := t.TempDir()
	backupPath := filepath.Join(root, "backup")
	store, key := changedMaterialMigrationStore()
	wantHash := store.chunks[key].Chunk.Hash()
	lastKey := key
	if !complete {
		lastKey.Pos.X--
	}
	state := materialMigrationState{
		Version:    materialMigrationVersion,
		Seed:       42,
		BackupPath: backupPath,
		LastKey:    &lastKey,
		Complete:   complete,
	}
	stateData, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	stateData = append(stateData, '\n')
	statePath := filepath.Join(root, materialMigrationStateName)
	if err := os.WriteFile(statePath, stateData, 0o600); err != nil {
		t.Fatal(err)
	}

	err = runMaterialMigration(
		context.Background(), store, root, backupPath,
		func(_ context.Context, path string) error {
			return os.Mkdir(path, 0o755)
		}, defaultMaterialMigrationFS(),
	)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("已有状态缺失备份的迁移错误 = %v，期望 os.ErrNotExist", err)
	}
	if _, err := os.Lstat(backupPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("已有状态缺失备份后创建了新备份，Lstat 错误: %v", err)
	}
	gotState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotState, stateData) {
		t.Fatal("拒绝缺失备份后迁移状态被改变")
	}
	stored := store.chunks[key]
	if stored.Revision != 5 || stored.Chunk.Hash() != wantHash || len(store.saveCalls) != 0 {
		t.Fatalf("拒绝缺失备份后区块改变: revision=%d saves=%d",
			stored.Revision, len(store.saveCalls))
	}
}

func TestMaterialMigrationStopsAtBackupSaveAndSyncFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*materialMigrationTestStore, error) func(context.Context, string) error
		wantSaves int
		wantSyncs int
	}{
		{
			name: "Backup",
			configure: func(_ *materialMigrationTestStore, injected error) func(context.Context, string) error {
				return func(context.Context, string) error { return injected }
			},
		},
		{
			name: "SaveBatch",
			configure: func(store *materialMigrationTestStore, injected error) func(context.Context, string) error {
				store.saveFailureEnabled = true
				store.saveFailAfter = 0
				store.saveErr = injected
				return successfulMaterialMigrationBackup
			},
			wantSaves: 1,
		},
		{
			name: "Sync",
			configure: func(store *materialMigrationTestStore, injected error) func(context.Context, string) error {
				store.syncErr = injected
				return successfulMaterialMigrationBackup
			},
			wantSaves: 1,
			wantSyncs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := newMaterialMigrationTestStore(42)
			key := core.ChunkKey{Dimension: core.Overworld}
			chunk := world.NewChunk(key.Pos)
			chunk.SetBlock(0, core.MinY, 0, core.StoneID)
			store.addChunk(key, 5, chunk)
			store.keys = []core.ChunkKey{key}
			injected := errors.New("injected " + tc.name + " failure")
			backup := tc.configure(store, injected)

			err := runMaterialMigration(
				context.Background(), store, root, filepath.Join(root, "backup"), backup,
				defaultMaterialMigrationFS(),
			)
			if !errors.Is(err, injected) {
				t.Fatalf("迁移错误 = %v，期望 %v", err, injected)
			}
			if len(store.saveCalls) != tc.wantSaves || store.syncCalls != tc.wantSyncs {
				t.Fatalf("失败后 saves/syncs = %d/%d，期望 %d/%d",
					len(store.saveCalls), store.syncCalls, tc.wantSaves, tc.wantSyncs)
			}
			if _, err := os.Stat(filepath.Join(root, "material-migration-v1.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("失败后不应有正式进度状态，Stat 错误: %v", err)
			}
		})
	}
}

func TestMaterialMigrationRetryAfterProgressRenameFailureDoesNotResave(t *testing.T) {
	root := t.TempDir()
	store, key := changedMaterialMigrationStore()
	injected := errors.New("injected progress rename failure")
	failedFS := materialMigrationFS{rename: func(string, string) error { return injected }}

	err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, failedFS,
	)
	if !errors.Is(err, injected) {
		t.Fatalf("首次迁移错误 = %v，期望 %v", err, injected)
	}
	if got := store.chunks[key].Revision; got != 6 {
		t.Fatalf("进度失败后已提交 revision = %d，期望 6", got)
	}
	if len(store.saveCalls) != 1 {
		t.Fatalf("首次保存次数 = %d，期望 1", len(store.saveCalls))
	}

	if err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
	); err != nil {
		t.Fatalf("重试迁移失败: %v", err)
	}
	if got := store.chunks[key].Revision; got != 6 {
		t.Fatalf("重试后 revision = %d，期望仍为 6", got)
	}
	if len(store.saveCalls) != 1 {
		t.Fatalf("重试重复保存了无变化区块，SaveBatch 次数 = %d", len(store.saveCalls))
	}
	if !readMaterialMigrationStateForTest(t, root).Complete {
		t.Fatal("重试后迁移未标记完成")
	}
}

func TestMaterialMigrationRetryAfterProgressDirectorySyncFailureRepairsState(t *testing.T) {
	root := t.TempDir()
	store, key := changedMaterialMigrationStore()
	injected := errors.New("injected progress directory sync failure")
	failedFS := materialMigrationFS{
		rename: os.Rename,
		syncDirectory: func(string) error {
			return injected
		},
	}

	err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, failedFS,
	)
	if !errors.Is(err, injected) {
		t.Fatalf("首次迁移错误 = %v，期望 %v", err, injected)
	}
	statePath := filepath.Join(root, materialMigrationStateName)
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("目录同步失败后正式状态仍可复用，Stat 错误: %v", err)
	}

	if err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
	); err != nil {
		t.Fatalf("目录同步失败后重试迁移: %v", err)
	}
	if store.chunks[key].Revision != 6 || len(store.saveCalls) != 1 {
		t.Fatalf("重试重复提交区块: revision=%d SaveBatch=%d",
			store.chunks[key].Revision, len(store.saveCalls))
	}
	if !readMaterialMigrationStateForTest(t, root).Complete {
		t.Fatal("目录同步失败后重试未完成迁移")
	}
}

func TestMaterialMigrationRetryAfterCompletionRenameFailureDoesNotResave(t *testing.T) {
	root := t.TempDir()
	store, key := changedMaterialMigrationStore()
	injected := errors.New("injected completion rename failure")
	renames := 0
	failedFS := materialMigrationFS{rename: func(oldPath, newPath string) error {
		renames++
		if renames == 2 {
			return injected
		}
		return os.Rename(oldPath, newPath)
	}}

	err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, failedFS,
	)
	if !errors.Is(err, injected) {
		t.Fatalf("首次迁移错误 = %v，期望 %v", err, injected)
	}
	if state := readMaterialMigrationStateForTest(t, root); state.Complete || state.LastKey == nil {
		t.Fatalf("完成标记失败后状态 = %+v，期望保留未完成进度", state)
	}

	loadsBefore := len(store.loadKeys)
	savesBefore := len(store.saveCalls)
	if err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
	); err != nil {
		t.Fatalf("重试完成标记失败: %v", err)
	}
	if len(store.loadKeys) != loadsBefore || len(store.saveCalls) != savesBefore || store.chunks[key].Revision != 6 {
		t.Fatalf("完成标记重试重新处理了区块: loads=%d saves=%d revision=%d",
			len(store.loadKeys)-loadsBefore, len(store.saveCalls)-savesBefore, store.chunks[key].Revision)
	}
	if !readMaterialMigrationStateForTest(t, root).Complete {
		t.Fatal("重试后完成状态仍为 false")
	}

	savesBefore = len(store.saveCalls)
	if err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
	); err != nil {
		t.Fatalf("完成后再次运行失败: %v", err)
	}
	if len(store.saveCalls) != savesBefore {
		t.Fatalf("完成后再次运行产生 %d 次保存", len(store.saveCalls)-savesBefore)
	}
}

func TestMaterialMigrationRetryAfterPartialSaveFailureDoesNotIncrementCommittedRevision(t *testing.T) {
	root := t.TempDir()
	store := newMaterialMigrationTestStore(42)
	keys := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 0}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1}},
	}
	for _, key := range keys {
		chunk := world.NewChunk(key.Pos)
		chunk.SetBlock(0, core.MinY, 0, core.StoneID)
		store.addChunk(key, 5, chunk)
	}
	store.keys = slices.Clone(keys)
	store.saveFailureEnabled = true
	store.saveFailAfter = 1
	store.saveErr = errors.New("injected partial region failure")

	if err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
	); !errors.Is(err, store.lastSaveErr) {
		t.Fatalf("部分保存错误 = %v，期望 %v", err, store.lastSaveErr)
	}
	if store.chunks[keys[0]].Revision != 6 || store.chunks[keys[1]].Revision != 5 {
		t.Fatalf("部分保存后 revisions = %d/%d，期望 6/5",
			store.chunks[keys[0]].Revision, store.chunks[keys[1]].Revision)
	}

	if err := runMaterialMigration(
		context.Background(), store, root, filepath.Join(root, "backup"),
		successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
	); err != nil {
		t.Fatalf("部分保存后重试失败: %v", err)
	}
	if store.chunks[keys[0]].Revision != 6 || store.chunks[keys[1]].Revision != 6 {
		t.Fatalf("重试后 revisions = %d/%d，期望 6/6",
			store.chunks[keys[0]].Revision, store.chunks[keys[1]].Revision)
	}
	if len(store.saveCalls) != 2 || len(store.saveCalls[1]) != 1 || store.saveCalls[1][0].Key != keys[1] {
		t.Fatalf("重试保存 = %v，期望只保存第二个区块", store.saveCalls)
	}
}

func TestMaterialMigrationRejectsMismatchedStateWithoutChangingIt(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*materialMigrationState)
	}{
		{name: "Version", mutate: func(state *materialMigrationState) { state.Version++ }},
		{name: "Seed", mutate: func(state *materialMigrationState) { state.Seed++ }},
		{name: "BackupPath", mutate: func(state *materialMigrationState) { state.BackupPath += "-other" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			backupPath, err := filepath.Abs(filepath.Join(root, "backup"))
			if err != nil {
				t.Fatal(err)
			}
			state := materialMigrationState{Version: 1, Seed: 42, BackupPath: backupPath}
			tc.mutate(&state)
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, '\n')
			statePath := filepath.Join(root, "material-migration-v1.json")
			if err := os.WriteFile(statePath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			store := newMaterialMigrationTestStore(42)

			if err := runMaterialMigration(
				context.Background(), store, root, backupPath,
				successfulMaterialMigrationBackup, defaultMaterialMigrationFS(),
			); err == nil {
				t.Fatalf("%s 不匹配的状态未被拒绝", tc.name)
			}
			got, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, data) {
				t.Fatalf("拒绝 %s 不匹配后状态被改变", tc.name)
			}
			if store.chunkKeysCalls != 0 || len(store.saveCalls) != 0 {
				t.Fatalf("拒绝状态后仍扫描或保存: keys=%d saves=%d", store.chunkKeysCalls, len(store.saveCalls))
			}
		})
	}
}

type materialMigrationTestStore struct {
	metadata           storage.Metadata
	keys               []core.ChunkKey
	chunks             map[core.ChunkKey]storage.StoredChunk
	events             []string
	loadKeys           []core.ChunkKey
	saveCalls          [][]storage.ChunkSave
	syncCalls          int
	chunkKeysCalls     int
	closed             int
	saveFailureEnabled bool
	saveFailAfter      int
	saveErr            error
	lastSaveErr        error
	syncErr            error
}

func newMaterialMigrationTestStore(seed int64) *materialMigrationTestStore {
	return &materialMigrationTestStore{
		metadata: storage.Metadata{FormatVersion: 2, Seed: seed},
		chunks:   make(map[core.ChunkKey]storage.StoredChunk),
	}
}

func changedMaterialMigrationStore() (*materialMigrationTestStore, core.ChunkKey) {
	store := newMaterialMigrationTestStore(42)
	key := core.ChunkKey{Dimension: core.Overworld}
	chunk := world.NewChunk(key.Pos)
	chunk.SetBlock(0, core.MinY, 0, core.StoneID)
	store.addChunk(key, 5, chunk)
	store.keys = []core.ChunkKey{key}
	return store, key
}

func (store *materialMigrationTestStore) addChunk(key core.ChunkKey, revision uint64, chunk *world.Chunk) {
	store.chunks[key] = storage.StoredChunk{
		Key: key, Revision: revision, PersistedRevision: revision, Chunk: chunk,
	}
}

func (store *materialMigrationTestStore) Metadata() storage.Metadata {
	store.events = append(store.events, "metadata")
	return store.metadata
}

func (store *materialMigrationTestStore) ChunkKeys(context.Context) ([]core.ChunkKey, error) {
	store.events = append(store.events, "keys")
	store.chunkKeysCalls++
	return slices.Clone(store.keys), nil
}

func (store *materialMigrationTestStore) LoadChunk(_ context.Context, key core.ChunkKey) (storage.StoredChunk, error) {
	store.events = append(store.events, fmt.Sprintf("load:%d:%d:%d", key.Dimension, key.Pos.X, key.Pos.Z))
	store.loadKeys = append(store.loadKeys, key)
	stored, ok := store.chunks[key]
	if !ok {
		return storage.StoredChunk{}, storage.ErrChunkNotFound
	}
	stored.Chunk = stored.Chunk.Clone()
	return stored, nil
}

func (store *materialMigrationTestStore) SaveBatch(_ context.Context, saves []storage.ChunkSave) (storage.SaveResult, error) {
	call := make([]storage.ChunkSave, len(saves))
	for index, save := range saves {
		call[index] = save
		call[index].Chunk = save.Chunk.Clone()
	}
	store.saveCalls = append(store.saveCalls, call)
	result := storage.SaveResult{Committed: make(map[core.ChunkKey]uint64)}
	limit := len(saves)
	var injected error
	if store.saveFailureEnabled {
		limit = min(limit, store.saveFailAfter)
		injected = store.saveErr
		store.lastSaveErr = injected
		store.saveFailureEnabled = false
	}
	for _, save := range saves[:limit] {
		stored := store.chunks[save.Key]
		if save.Revision == stored.Revision && save.Chunk.Hash() == stored.Chunk.Hash() {
			result.Committed[save.Key] = stored.Revision
			continue
		}
		if save.Revision != stored.Revision+1 {
			return result, fmt.Errorf("unexpected revision %d after %d", save.Revision, stored.Revision)
		}
		stored.Revision = save.Revision
		stored.PersistedRevision = save.Revision
		stored.Chunk = save.Chunk.Clone()
		store.chunks[save.Key] = stored
		result.Committed[save.Key] = save.Revision
	}
	return result, injected
}

func (store *materialMigrationTestStore) Sync(context.Context) error {
	store.syncCalls++
	if store.syncErr != nil {
		err := store.syncErr
		store.syncErr = nil
		return err
	}
	return nil
}

func (store *materialMigrationTestStore) Close() error {
	store.closed++
	return nil
}

func successfulMaterialMigrationBackup(_ context.Context, path string) error {
	return os.MkdirAll(path, 0o755)
}

func assertMaterialMigrationState(
	t *testing.T,
	state materialMigrationState,
	seed int64,
	backupPath string,
	lastX int32,
	complete bool,
) {
	t.Helper()
	absoluteBackup, err := filepath.Abs(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Version != 1 || state.Seed != seed || state.BackupPath != absoluteBackup ||
		state.LastKey == nil || state.LastKey.Dimension != core.Overworld ||
		state.LastKey.Pos != (core.ChunkPos{X: lastX}) || state.Complete != complete {
		t.Fatalf("迁移状态 = %+v，期望 version=1 seed=%d backup=%q lastX=%d complete=%v",
			state, seed, absoluteBackup, lastX, complete)
	}
}

func readMaterialMigrationStateForTest(t *testing.T, root string) materialMigrationState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "material-migration-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state materialMigrationState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func materialMigrationDiskFixture(
	t *testing.T,
	generator *worldgen.Generator,
	key core.ChunkKey,
) (*world.Chunk, *world.Chunk) {
	t.Helper()
	chunk := world.NewChunk(key.Pos)
	for index, block := range []core.BlockID{
		core.StoneID,
		core.DirtID,
		core.GrassID,
		core.SandID,
		core.GravelID,
		core.ClayID,
		core.SnowBlockID,
	} {
		chunk.SetBlock(index, core.MinY, 0, block)
	}
	payloadY := int32(1)
	chunk.SetBlock(0, payloadY, 1, core.ChestID)
	chunk.SetBlock(1, payloadY, 1, core.FurnaceID)
	chunk.SetBlock(2, payloadY, 1, core.IronOreID)
	chestIndex, ok := world.ChunkBlockIndex(blockPosition(key.Pos, 0, payloadY, 1))
	if !ok {
		t.Fatal("箱子位置无法转换为区块索引")
	}
	furnaceIndex, ok := world.ChunkBlockIndex(blockPosition(key.Pos, 1, payloadY, 1))
	if !ok {
		t.Fatal("熔炉位置无法转换为区块索引")
	}
	chunk.SetDrop(0, world.DropSlot{
		Generation: 3, Active: true,
		Stack:      core.ItemStack{Item: core.ItemStoneBrick, Count: 4},
		BlockIndex: chestIndex, AgeTicks: 37, PickupDelayTicks: 2,
	})
	chunk.SetFurnace(0, world.FurnaceSlot{
		Generation: 5, Active: true, BlockIndex: furnaceIndex,
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 3},
		Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 1},
		ProgressTicks: 17, BurnTicks: 83,
	})
	var chestItems [core.ChestSlots]core.ItemStack
	chestItems[0] = core.ItemStack{Item: core.ItemBrick, Count: 9}
	chunk.SetChest(0, world.ChestSlot{
		Generation: 7, Active: true, BlockIndex: chestIndex, Items: chestItems,
	})

	want := chunk.Clone()
	for x := range 7 {
		want.SetBlock(x, core.MinY, 0, generator.BaseBlockAt(blockPosition(key.Pos, x, core.MinY, 0)))
	}
	return chunk, want
}

func assertMaterialMigrationDiskChunk(
	t *testing.T,
	root string,
	key core.ChunkKey,
	revision uint64,
	want *world.Chunk,
) {
	t.Helper()
	store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{})
	if err != nil {
		t.Fatalf("打开真实磁盘世界 %q 失败: %v", root, err)
	}
	stored, loadErr := store.LoadChunk(context.Background(), key)
	closeErr := store.Close()
	if err := errors.Join(loadErr, closeErr); err != nil {
		t.Fatalf("读取真实磁盘区块 %v 失败: %v", key, err)
	}
	if stored.Revision != revision || stored.PersistedRevision != revision {
		t.Fatalf("真实磁盘区块 %v revision=%d/%d，期望 %d",
			key, stored.Revision, stored.PersistedRevision, revision)
	}
	if stored.NeedsRewrite || stored.Recovered || !reflect.DeepEqual(stored.Chunk, want) {
		t.Fatalf("真实磁盘区块 %v 内容或 schema v8 往返状态不符", key)
	}
}

func blockPosition(chunk core.ChunkPos, localX int, y int32, localZ int) core.BlockPos {
	return core.BlockPos{
		X: chunk.X<<core.SectionShift + int32(localX),
		Y: y,
		Z: chunk.Z<<core.SectionShift + int32(localZ),
	}
}

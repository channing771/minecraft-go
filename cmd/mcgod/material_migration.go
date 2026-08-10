package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

const (
	materialMigrationVersion       = 1
	materialMigrationBatchSize     = 32
	materialMigrationStateName     = "material-migration-v1.json"
	maxMaterialMigrationStateBytes = int64(4 << 10)
)

type materialMigrationStore interface {
	Metadata() storage.Metadata
	ChunkKeys(context.Context) ([]core.ChunkKey, error)
	LoadChunk(context.Context, core.ChunkKey) (storage.StoredChunk, error)
	SaveBatch(context.Context, []storage.ChunkSave) (storage.SaveResult, error)
	Sync(context.Context) error
	Close() error
}

type materialMigrationState struct {
	Version    int            `json:"version"`
	Seed       int64          `json:"seed"`
	BackupPath string         `json:"backup_path"`
	LastKey    *core.ChunkKey `json:"last_key"`
	Complete   bool           `json:"complete"`
}

type materialMigrationFS struct {
	rename        func(string, string) error
	syncDirectory func(string) error
}

func defaultMaterialMigrationFS() materialMigrationFS {
	return materialMigrationFS{
		rename:        os.Rename,
		syncDirectory: syncMaterialMigrationDirectory,
	}
}

func migrateMaterials(ctx context.Context, worldPath, backupPath string) error {
	if ctx == nil {
		return errors.New("材料迁移 context 不能为空")
	}
	if strings.TrimSpace(worldPath) == "" || strings.TrimSpace(backupPath) == "" {
		return errors.New("材料迁移世界和备份路径不能为空")
	}
	absoluteWorld, err := filepath.Abs(worldPath)
	if err != nil {
		return fmt.Errorf("规范世界路径 %q: %w", worldPath, err)
	}
	absoluteBackup, err := filepath.Abs(backupPath)
	if err != nil {
		return fmt.Errorf("规范备份路径 %q: %w", backupPath, err)
	}
	metadataPath := filepath.Join(absoluteWorld, "world.meta")
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil {
		return fmt.Errorf("检查既有世界 metadata %q: %w", metadataPath, err)
	}
	if !metadataInfo.Mode().IsRegular() {
		return fmt.Errorf("既有世界 metadata %q 不是普通文件", metadataPath)
	}

	store, err := storage.OpenDisk(ctx, absoluteWorld, storage.OpenOptions{})
	if err != nil {
		return fmt.Errorf("打开迁移世界 %q: %w", absoluteWorld, err)
	}
	return runMaterialMigration(
		ctx,
		store,
		absoluteWorld,
		absoluteBackup,
		store.Backup,
		defaultMaterialMigrationFS(),
	)
}

func runMaterialMigration(
	ctx context.Context,
	store materialMigrationStore,
	worldPath string,
	backupPath string,
	backup func(context.Context, string) error,
	fs materialMigrationFS,
) (returnErr error) {
	defer func() {
		returnErr = errors.Join(returnErr, store.Close())
	}()
	if ctx == nil {
		return errors.New("材料迁移 context 不能为空")
	}
	absoluteWorld, err := filepath.Abs(worldPath)
	if err != nil {
		return fmt.Errorf("规范世界路径 %q: %w", worldPath, err)
	}
	absoluteBackup, err := filepath.Abs(backupPath)
	if err != nil {
		return fmt.Errorf("规范备份路径 %q: %w", backupPath, err)
	}
	if fs.rename == nil {
		fs.rename = os.Rename
	}
	if fs.syncDirectory == nil {
		fs.syncDirectory = syncMaterialMigrationDirectory
	}
	metadata := store.Metadata()
	statePath := filepath.Join(absoluteWorld, materialMigrationStateName)
	state, exists, err := readMaterialMigrationState(statePath)
	if err != nil {
		return err
	}
	if exists {
		if err := validateMaterialMigrationState(state, metadata.Seed, absoluteBackup); err != nil {
			return err
		}
		if _, err := os.Lstat(absoluteBackup); err != nil {
			return fmt.Errorf("检查已有迁移备份 %q: %w", absoluteBackup, err)
		}
	} else {
		state = materialMigrationState{
			Version:    materialMigrationVersion,
			Seed:       metadata.Seed,
			BackupPath: absoluteBackup,
		}
	}
	if err := backup(ctx, absoluteBackup); err != nil {
		return fmt.Errorf("创建或验证迁移备份: %w", err)
	}
	if state.Complete {
		return nil
	}

	keys, err := store.ChunkKeys(ctx)
	if err != nil {
		return fmt.Errorf("枚举迁移区块: %w", err)
	}
	slices.SortFunc(keys, compareMaterialMigrationKeys)
	pending := keys[:0]
	for _, key := range keys {
		if key.Dimension != core.Overworld {
			continue
		}
		if state.LastKey != nil && compareMaterialMigrationKeys(key, *state.LastKey) <= 0 {
			continue
		}
		pending = append(pending, key)
	}

	generator := worldgen.New(metadata.Seed)
	for start := 0; start < len(pending); start += materialMigrationBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+materialMigrationBatchSize, len(pending))
		batch := pending[start:end]
		saves := make([]storage.ChunkSave, 0, len(batch))
		for _, key := range batch {
			stored, err := store.LoadChunk(ctx, key)
			if err != nil {
				return fmt.Errorf("加载迁移区块 %v: %w", key, err)
			}
			candidate, changed, err := migrateNaturalMaterials(generator, stored)
			if err != nil {
				return err
			}
			if changed {
				saves = append(saves, candidate)
			}
		}
		if len(saves) != 0 {
			result, err := store.SaveBatch(ctx, saves)
			if err != nil {
				return fmt.Errorf("保存迁移批次: %w", err)
			}
			for _, save := range saves {
				if result.Committed[save.Key] != save.Revision {
					return fmt.Errorf(
						"保存迁移批次未完整提交 %v revision %d", save.Key, save.Revision,
					)
				}
			}
		}
		if err := store.Sync(ctx); err != nil {
			return fmt.Errorf("同步迁移批次: %w", err)
		}
		lastKey := batch[len(batch)-1]
		state.LastKey = &lastKey
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := writeMaterialMigrationState(statePath, state, fs); err != nil {
			return fmt.Errorf("写入迁移进度: %w", err)
		}
	}

	if err := store.Sync(ctx); err != nil {
		return fmt.Errorf("同步迁移完成状态前的世界: %w", err)
	}
	state.Complete = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeMaterialMigrationState(statePath, state, fs); err != nil {
		return fmt.Errorf("写入迁移完成状态: %w", err)
	}
	return nil
}

func compareMaterialMigrationKeys(left, right core.ChunkKey) int {
	if order := cmp.Compare(left.Dimension, right.Dimension); order != 0 {
		return order
	}
	if order := cmp.Compare(left.Pos.X, right.Pos.X); order != 0 {
		return order
	}
	return cmp.Compare(left.Pos.Z, right.Pos.Z)
}

func validateMaterialMigrationState(state materialMigrationState, seed int64, backupPath string) error {
	if state.Version != materialMigrationVersion || state.Seed != seed || state.BackupPath != backupPath {
		return fmt.Errorf(
			"迁移状态身份不匹配: version=%d seed=%d backup=%q",
			state.Version, state.Seed, state.BackupPath,
		)
	}
	if state.LastKey != nil && state.LastKey.Dimension != core.Overworld {
		return fmt.Errorf("迁移状态 LastKey 维度无效: %d", state.LastKey.Dimension)
	}
	return nil
}

func readMaterialMigrationState(path string) (materialMigrationState, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return materialMigrationState{}, false, nil
	}
	if err != nil {
		return materialMigrationState{}, false, fmt.Errorf("检查迁移状态 %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return materialMigrationState{}, false, fmt.Errorf("迁移状态 %q 不是普通文件", path)
	}
	if info.Size() > maxMaterialMigrationStateBytes {
		return materialMigrationState{}, false, fmt.Errorf(
			"迁移状态 %q 超过 %d 字节", path, maxMaterialMigrationStateBytes,
		)
	}
	file, err := os.Open(path)
	if err != nil {
		return materialMigrationState{}, false, fmt.Errorf("打开迁移状态 %q: %w", path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxMaterialMigrationStateBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return materialMigrationState{}, false, fmt.Errorf("读取迁移状态 %q: %w", path, err)
	}
	if int64(len(data)) > maxMaterialMigrationStateBytes {
		return materialMigrationState{}, false, fmt.Errorf(
			"迁移状态 %q 超过 %d 字节", path, maxMaterialMigrationStateBytes,
		)
	}
	var state materialMigrationState
	if err := json.Unmarshal(data, &state); err != nil {
		return materialMigrationState{}, false, fmt.Errorf("解析迁移状态 %q: %w", path, err)
	}
	return state, true, nil
}

func writeMaterialMigrationState(
	path string,
	state materialMigrationState,
	fs materialMigrationFS,
) (returnErr error) {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("编码迁移状态: %w", err)
	}
	data = append(data, '\n')
	parent := filepath.Dir(path)
	temporary, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("创建迁移状态临时文件: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	promoted := false
	defer func() {
		if !closed {
			returnErr = errors.Join(returnErr, temporary.Close())
		}
		if !promoted {
			if err := os.Remove(temporaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				returnErr = errors.Join(returnErr, err)
			}
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("设置迁移状态临时文件权限: %w", err)
	}
	for remaining := data; len(remaining) != 0; {
		written, err := temporary.Write(remaining)
		if err != nil {
			return fmt.Errorf("写入迁移状态临时文件: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("写入迁移状态临时文件: %w", io.ErrShortWrite)
		}
		remaining = remaining[written:]
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步迁移状态临时文件: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("关闭迁移状态临时文件: %w", err)
	}
	closed = true
	if err := fs.rename(temporaryPath, path); err != nil {
		return fmt.Errorf("替换迁移状态: %w", err)
	}
	promoted = true
	if err := fs.syncDirectory(parent); err != nil {
		cleanupErrors := []error{fmt.Errorf("同步迁移状态目录: %w", err)}
		removeErr := os.Remove(path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("移除未持久化迁移状态: %w", removeErr))
		}
		if cleanupSyncErr := fs.syncDirectory(parent); cleanupSyncErr != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("同步迁移状态清理: %w", cleanupSyncErr))
		}
		return errors.Join(cleanupErrors...)
	}
	return nil
}

func syncMaterialMigrationDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func migrateNaturalMaterials(
	generator *worldgen.Generator,
	stored storage.StoredChunk,
) (storage.ChunkSave, bool, error) {
	if stored.Chunk == nil {
		return storage.ChunkSave{}, false, fmt.Errorf("迁移区块 %v: chunk 为空", stored.Key)
	}
	if stored.Chunk.Pos != stored.Key.Pos {
		return storage.ChunkSave{}, false, fmt.Errorf(
			"迁移区块 %v: chunk 位置为 %v", stored.Key, stored.Chunk.Pos,
		)
	}

	next := stored.Chunk.Clone()
	changed := false
	baseX := stored.Key.Pos.X << core.SectionShift
	baseZ := stored.Key.Pos.Z << core.SectionShift
	for y := int32(core.MinY); y < core.MaxY; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				current := next.BlockAt(x, y, z)
				if !naturalMaterialValue(current) {
					continue
				}
				generated := generator.TerrainBlockAt(core.BlockPos{
					X: baseX + int32(x),
					Y: y,
					Z: baseZ + int32(z),
				})
				if generated == current {
					continue
				}
				next.SetBlock(x, y, z, generated)
				changed = true
			}
		}
	}

	revision := stored.Revision
	if changed {
		if revision == math.MaxUint64 {
			return storage.ChunkSave{}, false, fmt.Errorf("迁移区块 %v: revision 已耗尽", stored.Key)
		}
		revision++
	}
	return storage.ChunkSave{Key: stored.Key, Revision: revision, Chunk: next}, changed, nil
}

func naturalMaterialValue(block core.BlockID) bool {
	switch block {
	case core.StoneID, core.DirtID, core.GrassID, core.SandID,
		core.GravelID, core.ClayID, core.SnowBlockID:
		return true
	default:
		return false
	}
}

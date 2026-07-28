package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"minecraft-go/internal/core"
)

// DiskStore persists chunks in lazily opened region files under one locked world.
type DiskStore struct {
	mu      sync.Mutex
	files   *worldFiles
	regions map[RegionKey]*region
	closing atomic.Bool
	closed  bool
}

func OpenDisk(ctx context.Context, root string, options OpenOptions) (*DiskStore, error) {
	files, err := openWorldFiles(ctx, root, options)
	if err != nil {
		return nil, err
	}
	return &DiskStore{
		files:   files,
		regions: make(map[RegionKey]*region),
	}, nil
}

func (store *DiskStore) Metadata() Metadata {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.files.metadata
}

func (store *DiskStore) LoadChunk(
	ctx context.Context,
	key core.ChunkKey,
) (StoredChunk, error) {
	if store.closing.Load() {
		return StoredChunk{}, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closing.Load() || store.closed {
		return StoredChunk{}, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}

	regionKey, _ := RegionFor(key)
	opened, ok := store.regions[regionKey]
	if !ok {
		var err error
		opened, err = openRegion(ctx, store.regionPath(regionKey), regionKey)
		if errors.Is(err, os.ErrNotExist) {
			return StoredChunk{}, fmt.Errorf("%w: %v", ErrChunkNotFound, key)
		}
		if err != nil {
			return StoredChunk{}, err
		}
		store.regions[regionKey] = opened
	}
	return opened.load(ctx, key)
}

func (store *DiskStore) SaveBatch(
	ctx context.Context,
	saves []ChunkSave,
) (SaveResult, error) {
	result := SaveResult{Committed: make(map[core.ChunkKey]uint64, len(saves))}
	if store.closing.Load() {
		return result, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	saves, err := validateAndNormalizeSaves(saves)
	if err != nil {
		return result, err
	}

	grouped := make(map[RegionKey][]ChunkSave)
	for _, save := range saves {
		key, _ := RegionFor(save.Key)
		grouped[key] = append(grouped[key], save)
	}
	keys := make([]RegionKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sortRegionKeys(keys)
	if store.closing.Load() {
		return result, os.ErrClosed
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closing.Load() || store.closed {
		return result, os.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		opened, err := store.regionForSave(ctx, key)
		if err != nil {
			return result, err
		}
		regionResult, err := opened.save(ctx, grouped[key])
		for chunkKey, revision := range regionResult.Committed {
			result.Committed[chunkKey] = revision
		}
		if err != nil {
			return result, fmt.Errorf("save region %+v: %w", key, err)
		}
		if opened.shouldCompact(productionRegionSpacePolicy) {
			if err := opened.compact(ctx); err != nil {
				return result, fmt.Errorf("compact region %+v: %w", key, err)
			}
		}
	}
	return result, nil
}

func validateAndNormalizeSaves(saves []ChunkSave) ([]ChunkSave, error) {
	maxRevisions := make(map[core.ChunkKey]uint64, len(saves))
	for _, save := range saves {
		if err := validateChunkSave(save); err != nil {
			return nil, err
		}
		if save.Revision > maxRevisions[save.Key] {
			maxRevisions[save.Key] = save.Revision
		}
	}

	candidates := make(map[core.ChunkKey][]ChunkSave, len(maxRevisions))
	for _, save := range saves {
		if save.Revision == maxRevisions[save.Key] {
			candidates[save.Key] = append(candidates[save.Key], save)
		}
	}
	keys := make([]core.ChunkKey, 0, len(maxRevisions))
	for key := range maxRevisions {
		keys = append(keys, key)
	}
	sortChunkKeys(keys)

	normalized := make([]ChunkSave, 0, len(keys))
	for _, key := range keys {
		selected := candidates[key][0]
		selectedHash := selected.Chunk.Hash()
		for _, candidate := range candidates[key][1:] {
			if candidate.Chunk.Hash() != selectedHash {
				return nil, fmt.Errorf(
					"%w: %v revision %d", ErrRevisionConflict, key, selected.Revision,
				)
			}
		}
		normalized = append(normalized, selected)
	}
	return normalized, nil
}

func sortChunkKeys(keys []core.ChunkKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dimension != keys[j].Dimension {
			return keys[i].Dimension < keys[j].Dimension
		}
		if keys[i].Pos.X != keys[j].Pos.X {
			return keys[i].Pos.X < keys[j].Pos.X
		}
		return keys[i].Pos.Z < keys[j].Pos.Z
	})
}

func (store *DiskStore) Sync(ctx context.Context) error {
	if store.closing.Load() {
		return os.ErrClosed
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closing.Load() || store.closed {
		return os.ErrClosed
	}

	keys := store.regionKeys()
	errs := make([]error, 0, len(keys))
	for _, key := range keys {
		if err := store.regions[key].sync(ctx); err != nil {
			errs = append(errs, fmt.Errorf("sync region %+v: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

func (store *DiskStore) Close() error {
	if store == nil {
		return nil
	}
	store.closing.Store(true)
	store.mu.Lock()
	defer store.mu.Unlock()
	store.closed = true

	keys := store.regionKeys()
	errs := make([]error, 0, len(keys))
	for _, key := range keys {
		if err := store.regions[key].close(); err != nil {
			errs = append(errs, fmt.Errorf("close region %+v: %w", key, err))
			continue
		}
		delete(store.regions, key)
	}
	if len(errs) != 0 {
		return errors.Join(errs...)
	}
	return store.files.close()
}

func (store *DiskStore) regionPath(key RegionKey) string {
	return filepath.Join(
		store.files.root,
		"dimensions", strconv.FormatInt(int64(key.Dimension), 10),
		"regions", fmt.Sprintf("r.%d.%d.region", key.X, key.Z),
	)
}

func (store *DiskStore) regionForSave(ctx context.Context, key RegionKey) (*region, error) {
	if opened, ok := store.regions[key]; ok {
		return opened, nil
	}
	path := store.regionPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create region directory %q: %w", filepath.Dir(path), err)
	}
	opened, err := openRegion(ctx, path, key)
	if errors.Is(err, os.ErrNotExist) {
		opened, err = createRegion(ctx, path, key)
	}
	if err != nil {
		return nil, err
	}
	store.regions[key] = opened
	return opened, nil
}

func (store *DiskStore) regionKeys() []RegionKey {
	keys := make([]RegionKey, 0, len(store.regions))
	for key := range store.regions {
		keys = append(keys, key)
	}
	sortRegionKeys(keys)
	return keys
}

func sortRegionKeys(keys []RegionKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dimension != keys[j].Dimension {
			return keys[i].Dimension < keys[j].Dimension
		}
		if keys[i].X != keys[j].X {
			return keys[i].X < keys[j].X
		}
		return keys[i].Z < keys[j].Z
	})
}

var _ Store = (*DiskStore)(nil)

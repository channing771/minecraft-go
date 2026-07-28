package storage

import (
	"context"
	"fmt"
	"sync"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// MemoryStore 是保留存储所有权与 revision 语义的内存 Store。
type MemoryStore struct {
	mu       sync.Mutex
	metadata Metadata
	chunks   map[core.ChunkKey]memoryChunk
}

type memoryChunk struct {
	revision uint64
	hash     [32]byte
	chunk    *world.Chunk
}

type pendingChunk struct {
	revision uint64
	hash     [32]byte
	chunk    *world.Chunk
}

// NewMemory 创建不执行磁盘 I/O 的内存 Store。
func NewMemory(metadata Metadata) *MemoryStore {
	return &MemoryStore{
		metadata: metadata,
		chunks:   make(map[core.ChunkKey]memoryChunk),
	}
}

func (store *MemoryStore) Metadata() Metadata {
	return store.metadata
}

func (store *MemoryStore) LoadChunk(
	ctx context.Context,
	key core.ChunkKey,
) (StoredChunk, error) {
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StoredChunk{}, err
	}

	stored, ok := store.chunks[key]
	if !ok {
		return StoredChunk{}, fmt.Errorf("%w: %v", ErrChunkNotFound, key)
	}
	return StoredChunk{
		Key:               key,
		Revision:          stored.revision,
		PersistedRevision: stored.revision,
		Chunk:             stored.chunk.Clone(),
	}, nil
}

func (store *MemoryStore) SaveBatch(
	ctx context.Context,
	saves []ChunkSave,
) (SaveResult, error) {
	if err := ctx.Err(); err != nil {
		return SaveResult{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return SaveResult{}, err
	}

	committed := make(map[core.ChunkKey]uint64, len(saves))
	pending := make(map[core.ChunkKey]pendingChunk, len(saves))
	for _, save := range saves {
		if err := validateChunkSave(save); err != nil {
			return SaveResult{}, err
		}

		hash := save.Chunk.Hash()
		if candidate, ok := pending[save.Key]; ok {
			if err := compareSave(
				save.Key, save.Revision, hash, candidate.revision, candidate.hash,
				committed,
			); err != nil {
				return SaveResult{}, err
			}
			if save.Revision > candidate.revision {
				pending[save.Key] = pendingChunk{
					revision: save.Revision,
					hash:     hash,
					chunk:    save.Chunk,
				}
			}
			continue
		}

		if stored, ok := store.chunks[save.Key]; ok {
			if err := compareSave(
				save.Key, save.Revision, hash, stored.revision, stored.hash, committed,
			); err != nil {
				return SaveResult{}, err
			}
			if save.Revision <= stored.revision {
				continue
			}
		}
		pending[save.Key] = pendingChunk{
			revision: save.Revision,
			hash:     hash,
			chunk:    save.Chunk,
		}
	}

	for key, candidate := range pending {
		store.chunks[key] = memoryChunk{
			revision: candidate.revision,
			hash:     candidate.hash,
			chunk:    candidate.chunk.Clone(),
		}
		committed[key] = candidate.revision
	}
	return SaveResult{Committed: committed}, nil
}

func (store *MemoryStore) Sync(ctx context.Context) error {
	return ctx.Err()
}

func (store *MemoryStore) Close() error {
	return nil
}

func validateChunkSave(save ChunkSave) error {
	if save.Chunk == nil {
		return fmt.Errorf("storage: chunk save for %v has nil chunk", save.Key)
	}
	if save.Chunk.Pos != save.Key.Pos {
		return fmt.Errorf("storage: chunk save key %v does not match chunk position %v", save.Key, save.Chunk.Pos)
	}
	if save.Revision == 0 {
		return fmt.Errorf("storage: chunk save for %v has zero revision", save.Key)
	}
	return nil
}

func compareSave(
	key core.ChunkKey,
	revision uint64,
	hash [32]byte,
	storedRevision uint64,
	storedHash [32]byte,
	committed map[core.ChunkKey]uint64,
) error {
	if revision < storedRevision {
		committed[key] = storedRevision
		return nil
	}
	if revision == storedRevision {
		if hash != storedHash {
			return fmt.Errorf("%w: %v revision %d", ErrRevisionConflict, key, revision)
		}
		committed[key] = storedRevision
	}
	return nil
}

var _ Store = (*MemoryStore)(nil)

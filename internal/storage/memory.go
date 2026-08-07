package storage

import (
	"bytes"
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
	players  map[core.PlayerID]memoryPlayer
}

type memoryChunk struct {
	revision uint64
	hash     [32]byte
	encoded  []byte
}

type pendingChunk struct {
	revision uint64
	hash     [32]byte
	chunk    *world.Chunk
}

type memoryPlayer struct {
	revision uint64
	encoded  []byte
}

// NewMemory 创建不执行磁盘 I/O 的内存 Store。
func NewMemory(metadata Metadata) *MemoryStore {
	return &MemoryStore{
		metadata: metadata,
		chunks:   make(map[core.ChunkKey]memoryChunk),
		players:  make(map[core.PlayerID]memoryPlayer),
	}
}

func (store *MemoryStore) Metadata() Metadata {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.metadata
}

// SaveMetadata 用值语义替换内存中的 metadata，与 DiskStore 行为一致。
func (store *MemoryStore) SaveMetadata(ctx context.Context, metadata Metadata) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := encodeMetadata(metadata); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.metadata = metadata
	return nil
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
	decoded, err := decodeChunkPayload(key, stored.revision, stored.encoded)
	if err != nil {
		return StoredChunk{}, fmt.Errorf("decode memory chunk %v: %w", key, err)
	}
	return StoredChunk{
		Key:               key,
		Revision:          stored.revision,
		PersistedRevision: stored.revision,
		Chunk:             decoded.Chunk,
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

	encoded := make(map[core.ChunkKey][]byte, len(pending))
	for key, candidate := range pending {
		if err := ctx.Err(); err != nil {
			return SaveResult{}, err
		}
		payload, err := encodeChunkPayload(ChunkSave{
			Key: key, Revision: candidate.revision, Chunk: candidate.chunk,
		})
		if err != nil {
			return SaveResult{}, fmt.Errorf("encode memory chunk %v: %w", key, err)
		}
		encoded[key] = payload
	}
	if err := ctx.Err(); err != nil {
		return SaveResult{}, err
	}
	for key, candidate := range pending {
		store.chunks[key] = memoryChunk{
			revision: candidate.revision,
			hash:     candidate.hash,
			encoded:  encoded[key],
		}
		committed[key] = candidate.revision
	}
	return SaveResult{Committed: committed}, nil
}

func (store *MemoryStore) LoadPlayer(
	ctx context.Context,
	id core.PlayerID,
) (StoredPlayer, error) {
	if err := ctx.Err(); err != nil {
		return StoredPlayer{}, err
	}
	if !id.Valid() {
		return StoredPlayer{}, fmt.Errorf("%w: invalid requested player ID", ErrCorrupt)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StoredPlayer{}, err
	}
	stored, ok := store.players[id]
	if !ok {
		return StoredPlayer{}, fmt.Errorf("%w: %s", ErrPlayerNotFound, id)
	}
	return decodePlayer(id, bytes.Clone(stored.encoded))
}

func (store *MemoryStore) SavePlayer(
	ctx context.Context,
	save PlayerSave,
) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	encoded, err := encodePlayer(save)
	if err != nil {
		return 0, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if stored, ok := store.players[save.PlayerID]; ok {
		switch {
		case save.Revision < stored.revision:
			return stored.revision, fmt.Errorf(
				"%w: player %s revision %d is below %d",
				ErrRevisionConflict, save.PlayerID, save.Revision, stored.revision,
			)
		case save.Revision == stored.revision:
			if !bytes.Equal(encoded, stored.encoded) {
				return stored.revision, fmt.Errorf(
					"%w: player %s revision %d",
					ErrRevisionConflict, save.PlayerID, save.Revision,
				)
			}
			return stored.revision, nil
		}
	}
	store.players[save.PlayerID] = memoryPlayer{
		revision: save.Revision,
		encoded:  bytes.Clone(encoded),
	}
	return save.Revision, nil
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

var _ WorldStore = (*MemoryStore)(nil)

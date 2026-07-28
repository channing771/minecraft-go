package storage_test

import (
	"context"
	"errors"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

func TestMemoryStoreOwnsSavedAndLoadedChunks(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: 42})
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 32}}
	chunk := world.NewChunk(key.Pos)
	chunk.SetBlock(1, 0, 2, core.StoneID)

	result, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 1, Chunk: chunk,
	}})
	if err != nil || result.Committed[key] != 1 {
		t.Fatalf("SaveBatch = %+v, %v", result, err)
	}
	chunk.SetBlock(1, 0, 2, core.DirtID)

	loaded, err := store.LoadChunk(context.Background(), key)
	if err != nil || loaded.Chunk.BlockAt(1, 0, 2) != core.StoneID {
		t.Fatalf("LoadChunk = %+v, %v", loaded, err)
	}
	loaded.Chunk.SetBlock(1, 0, 2, core.GrassID)
	again, _ := store.LoadChunk(context.Background(), key)
	if again.Chunk.BlockAt(1, 0, 2) != core.StoneID {
		t.Fatal("loaded chunk aliases store state")
	}
}

func TestMemoryStoreMissingChunkWrapsNotFound(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	_, err := store.LoadChunk(context.Background(), core.ChunkKey{})
	if !errors.Is(err, storage.ErrChunkNotFound) {
		t.Fatalf("LoadChunk missing error = %v, want errors.Is(ErrChunkNotFound)", err)
	}
}

func TestMemoryStoreSkipsLowerRevision(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	key := testChunkKey()
	higher := chunkWithBlock(key.Pos, core.StoneID)
	lower := chunkWithBlock(key.Pos, core.DirtID)
	saveChunk(t, store, key, 2, higher)

	result, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 1, Chunk: lower,
	}})
	if err != nil || result.Committed[key] != 2 {
		t.Fatalf("lower-revision SaveBatch = %+v, %v", result, err)
	}
	loaded, err := store.LoadChunk(context.Background(), key)
	if err != nil || loaded.Revision != 2 || loaded.Chunk.BlockAt(0, 0, 0) != core.StoneID {
		t.Fatalf("LoadChunk after lower revision = %+v, %v", loaded, err)
	}
}

func TestMemoryStoreSameRevisionSameHashIsIdempotent(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	key := testChunkKey()
	chunk := chunkWithBlock(key.Pos, core.StoneID)
	saveChunk(t, store, key, 3, chunk)

	result, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 3, Chunk: chunk.Clone(),
	}})
	if err != nil || result.Committed[key] != 3 {
		t.Fatalf("idempotent SaveBatch = %+v, %v", result, err)
	}
}

func TestMemoryStoreRejectsSameRevisionDifferentHash(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	key := testChunkKey()
	saveChunk(t, store, key, 3, chunkWithBlock(key.Pos, core.StoneID))

	_, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 3, Chunk: chunkWithBlock(key.Pos, core.DirtID),
	}})
	if !errors.Is(err, storage.ErrRevisionConflict) {
		t.Fatalf("same-revision different-content error = %v, want errors.Is(ErrRevisionConflict)", err)
	}
}

func TestMemoryStoreCanceledSaveDoesNotMutate(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	key := testChunkKey()
	saveChunk(t, store, key, 1, chunkWithBlock(key.Pos, core.StoneID))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := store.SaveBatch(ctx, []storage.ChunkSave{{
		Key: key, Revision: 2, Chunk: chunkWithBlock(key.Pos, core.DirtID),
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SaveBatch error = %v, want context.Canceled", err)
	}
	loaded, err := store.LoadChunk(context.Background(), key)
	if err != nil || loaded.Revision != 1 || loaded.Chunk.BlockAt(0, 0, 0) != core.StoneID {
		t.Fatalf("LoadChunk after canceled save = %+v, %v", loaded, err)
	}
}

func TestMemoryStoreValidatesEntireBatchBeforeApplying(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	validKey := testChunkKey()
	invalidKey := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 9, Z: 9}}

	_, err := store.SaveBatch(context.Background(), []storage.ChunkSave{
		{Key: validKey, Revision: 1, Chunk: chunkWithBlock(validKey.Pos, core.StoneID)},
		{Key: invalidKey, Revision: 1, Chunk: world.NewChunk(core.ChunkPos{})},
	})
	if err == nil {
		t.Fatal("SaveBatch accepted a chunk whose position does not match its key")
	}
	_, err = store.LoadChunk(context.Background(), validKey)
	if !errors.Is(err, storage.ErrChunkNotFound) {
		t.Fatalf("valid save applied despite invalid batch member: %v", err)
	}
}

func TestMemoryStoreCloseIsIdempotent(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	if err := store.Close(); err != nil {
		t.Fatalf("first Close = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

func testChunkKey() core.ChunkKey {
	return core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 32}}
}

func chunkWithBlock(pos core.ChunkPos, id core.BlockID) *world.Chunk {
	chunk := world.NewChunk(pos)
	chunk.SetBlock(0, 0, 0, id)
	return chunk
}

func saveChunk(
	t *testing.T,
	store *storage.MemoryStore,
	key core.ChunkKey,
	revision uint64,
	chunk *world.Chunk,
) {
	t.Helper()
	if _, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: revision, Chunk: chunk,
	}}); err != nil {
		t.Fatalf("SaveBatch = %v", err)
	}
}

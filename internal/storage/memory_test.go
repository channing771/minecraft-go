package storage_test

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

func TestMemoryStoreOwnsSavedAndLoadedChunks(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: 42})
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

func TestMemoryStoreRejectsUnencodableBatchAtomically(t *testing.T) {
	store := storage.NewMemory(storage.Metadata{})
	validKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 1, Z: 2},
	}
	existingKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 5, Z: 6},
	}
	invalidKey := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: 3, Z: 4},
	}
	saveChunk(t, store, existingKey, 1, chunkWithBlock(existingKey.Pos, core.StoneID))
	invalid := world.NewChunk(invalidKey.Pos)
	invalid.SetFurnace(0, world.FurnaceSlot{
		Generation: 1,
		Active:     true,
		BlockIndex: 0,
	})

	_, err := store.SaveBatch(context.Background(), []storage.ChunkSave{
		{
			Key: validKey, Revision: 1,
			Chunk: chunkWithBlock(validKey.Pos, core.StoneID),
		},
		{
			Key: existingKey, Revision: 2,
			Chunk: chunkWithBlock(existingKey.Pos, core.DirtID),
		},
		{Key: invalidKey, Revision: 1, Chunk: invalid},
	})
	if !errors.Is(err, storage.ErrCorrupt) {
		t.Fatalf("SaveBatch error = %v，想要 ErrCorrupt", err)
	}
	if _, err := store.LoadChunk(context.Background(), validKey); !errors.Is(err, storage.ErrChunkNotFound) {
		t.Fatalf("编码失败后合法 chunk 可见: %v", err)
	}
	loaded, err := store.LoadChunk(context.Background(), existingKey)
	if err != nil || loaded.Revision != 1 || loaded.Chunk.BlockAt(0, 0, 0) != core.StoneID {
		t.Fatalf("编码失败后已有 chunk = %+v, %v", loaded, err)
	}
}

func TestMemoryStoreRetainedHeapIsBounded(t *testing.T) {
	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	store := storage.NewMemory(storage.Metadata{})
	for index := 0; index < 192; index++ {
		key := core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       core.ChunkPos{X: int32(index)},
		}
		chunk := world.NewChunk(key.Pos)
		for section := 0; section < core.SectionsPerChunk; section++ {
			y := int32(core.MinY + section*core.SectionSize)
			chunk.SetBlock(section%core.SectionSize, y, section/core.SectionSize, core.StoneID)
		}
		if _, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
			Key: key, Revision: 1, Chunk: chunk,
		}}); err != nil {
			t.Fatal(err)
		}
	}

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(store)
	if after.HeapAlloc < before.HeapAlloc {
		t.Fatalf("HeapAlloc before=%d after=%d", before.HeapAlloc, after.HeapAlloc)
	}
	const limit = 8 << 20
	if retained := after.HeapAlloc - before.HeapAlloc; retained >= limit {
		t.Fatalf("retained HeapAlloc = %d，必须小于 %d", retained, limit)
	}

	for index := 0; index < 192; index++ {
		key := core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       core.ChunkPos{X: int32(index)},
		}
		loaded, err := store.LoadChunk(context.Background(), key)
		if err != nil || loaded.Revision != 1 {
			t.Fatalf("LoadChunk(%+v) = %+v, %v", key, loaded, err)
		}
		for section := 0; section < core.SectionsPerChunk; section++ {
			y := int32(core.MinY + section*core.SectionSize)
			if got := loaded.Chunk.BlockAt(section%core.SectionSize, y, section/core.SectionSize); got != core.StoneID {
				t.Fatalf("LoadChunk(%+v) section %d block = %v，想要 Stone", key, section, got)
			}
		}
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

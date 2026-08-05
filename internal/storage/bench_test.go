package storage

import (
	"context"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/worldgen"
)

var (
	benchmarkEncodedPayload []byte
	benchmarkDecodedChunk   StoredChunk
)

func BenchmarkChunkEncode(b *testing.B) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -2, Z: 3}}
	save := ChunkSave{
		Key: key, Revision: 1,
		Chunk: worldgen.New(42).GenerateChunk(key.Pos),
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		payload, err := encodeChunkPayload(save)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkEncodedPayload = payload
	}
}

func BenchmarkChunkDecode(b *testing.B) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -2, Z: 3}}
	save := ChunkSave{
		Key: key, Revision: 1,
		Chunk: worldgen.New(42).GenerateChunk(key.Pos),
	}
	payload, err := encodeChunkPayload(save)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		decoded, err := decodeChunkPayload(key, save.Revision, payload)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkDecodedChunk = StoredChunk{
			Key: key, Revision: decoded.Revision, Chunk: decoded.Chunk,
		}
	}
}

func BenchmarkDiskStoreSave32(b *testing.B) {
	ctx := context.Background()
	store, err := OpenDisk(ctx, b.TempDir(), OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 42},
	})
	if err != nil {
		b.Fatal(err)
	}
	generator := worldgen.New(42)
	saves := make([]ChunkSave, 32)
	for index := range saves {
		key := core.ChunkKey{
			Dimension: core.Overworld,
			Pos:       core.ChunkPos{X: int32(index), Z: 0},
		}
		saves[index] = ChunkSave{Key: key, Chunk: generator.GenerateChunk(key.Pos)}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for iteration := range b.N {
		for index := range saves {
			saves[index].Revision = uint64(iteration + 1)
		}
		if _, err := store.SaveBatch(ctx, saves); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}
}

func BenchmarkDiskStoreColdLoad(b *testing.B) {
	ctx := context.Background()
	root := b.TempDir()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -2, Z: 3}}
	store, err := OpenDisk(ctx, root, OpenOptions{
		Create: Metadata{FormatVersion: 2, Seed: 42},
	})
	if err != nil {
		b.Fatal(err)
	}
	if _, err := store.SaveBatch(ctx, []ChunkSave{{
		Key: key, Revision: 1,
		Chunk: worldgen.New(42).GenerateChunk(key.Pos),
	}}); err != nil {
		b.Fatal(err)
	}
	if err := store.Close(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		store, err := OpenDisk(ctx, root, OpenOptions{})
		if err != nil {
			b.Fatal(err)
		}
		loaded, loadErr := store.LoadChunk(ctx, key)
		closeErr := store.Close()
		if loadErr != nil {
			b.Fatal(loadErr)
		}
		if closeErr != nil {
			b.Fatal(closeErr)
		}
		benchmarkDecodedChunk = loaded
	}
}

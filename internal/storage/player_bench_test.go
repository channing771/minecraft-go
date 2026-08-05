package storage

import (
	"context"
	"testing"

	"minecraft-go/internal/core"
)

var (
	benchmarkEncodedPlayer  []byte
	benchmarkStoredPlayer   StoredPlayer
	benchmarkPlayerRevision uint64
)

func BenchmarkPlayerCodec(b *testing.B) {
	save := benchmarkPlayerSave(1)
	encoded, err := encodePlayer(save)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Encode", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			payload, err := encodePlayer(save)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkEncodedPlayer = payload
		}
	})
	b.Run("Decode", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			player, err := decodePlayer(save.PlayerID, encoded)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkStoredPlayer = player
		}
	})
}

func BenchmarkMemoryPlayerStore(b *testing.B) {
	store := NewMemory(benchmarkPlayerMetadata())
	benchmarkPlayerStore(b, store)
}

func BenchmarkDiskPlayerStore(b *testing.B) {
	store, err := OpenDisk(context.Background(), b.TempDir(), OpenOptions{Create: benchmarkPlayerMetadata()})
	if err != nil {
		b.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			b.Fatal(err)
		}
	}()
	benchmarkPlayerStore(b, store)
}

func benchmarkPlayerStore(b *testing.B, store PlayerStore) {
	b.Helper()
	ctx := context.Background()
	var latestRevision uint64
	b.Run("Save", func(b *testing.B) {
		b.ReportAllocs()
		for iteration := range b.N {
			save := benchmarkPlayerSave(uint64(iteration + 1))
			revision, err := store.SavePlayer(ctx, save)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkPlayerRevision = revision
			latestRevision = revision
		}
	})
	if latestRevision == 0 {
		if _, err := store.SavePlayer(ctx, benchmarkPlayerSave(1)); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("Load", func(b *testing.B) {
		id := benchmarkPlayerSave(1).PlayerID
		b.ReportAllocs()
		for range b.N {
			player, err := store.LoadPlayer(ctx, id)
			if err != nil {
				b.Fatal(err)
			}
			benchmarkStoredPlayer = player
		}
	})
}

func benchmarkPlayerSave(revision uint64) PlayerSave {
	safe := PlayerLocation{Dimension: core.Overworld, Position: [3]float32{4.5, 65, -6.5}}
	return PlayerSave{
		PlayerID: core.PlayerID{0x6f, 0xce, 0x82, 0x77, 0xa9, 0x33, 0x46, 0xcb, 0x9a, 0x1f, 0xda, 0x13, 0xb7, 0xee, 0x56, 0x44},
		Revision: revision, DisplayName: "Benchmark",
		Current: PlayerLocation{Dimension: core.Overworld, Position: [3]float32{8.5, 70, -9.5}},
		Yaw:     0.75, Pitch: -0.25, Safe: &safe,
	}
}

func benchmarkPlayerMetadata() Metadata {
	return Metadata{FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld}
}

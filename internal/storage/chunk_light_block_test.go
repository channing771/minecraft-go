package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestChunkV6MigratesToV7WithoutChangingPayloadSemantics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "chunk-v6.bin"))
	if err != nil {
		t.Fatal(err)
	}
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: -3, Z: 7},
	}
	got, err := decodeChunkPayload(key, 19, data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Migrated || got.Schema != 7 {
		t.Fatalf("v6 迁移结果 schema=%d migrated=%v", got.Schema, got.Migrated)
	}
}

func TestChunkV7RoundTripsLightBlockAndDrop(t *testing.T) {
	if currentChunkSchema != 7 {
		t.Fatalf("区块 schema=%d，想要 7", currentChunkSchema)
	}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	want := world.NewChunk(key.Pos)
	want.SetBlock(1, 2, 3, core.LightBlockID)
	want.SetDrop(0, world.DropSlot{
		Generation:       3,
		Active:           true,
		Stack:            core.ItemStack{Item: core.ItemLightBlock, Count: 7},
		BlockIndex:       furnaceBlockIndex(t, key.Pos, 1, 2, 3),
		AgeTicks:         11,
		PickupDelayTicks: 5,
	})

	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 19, Chunk: want})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "chunk-v7.bin")
	if *updateStorageFixtures {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(golden, encoded) {
		t.Fatal("v7 fixture drift; change schema version")
	}

	got, err := decodeChunkPayload(key, 19, golden)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != 7 || got.Migrated {
		t.Fatalf("v7 往返结果 schema=%d migrated=%v", got.Schema, got.Migrated)
	}
	if got.Key != key || got.Revision != 19 {
		t.Fatalf("v7 往返 identity key=%+v revision=%d", got.Key, got.Revision)
	}
	if got.Chunk.Hash() != want.Hash() || got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v7 往返改变了方块或掉落物状态")
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("v7 往返改变熔炉槽 %d", slot)
		}
	}
	for slot := range core.ChestsPerChunk {
		if got.Chunk.Chest(slot) != want.Chest(slot) {
			t.Fatalf("v7 往返改变箱子槽 %d", slot)
		}
	}
}

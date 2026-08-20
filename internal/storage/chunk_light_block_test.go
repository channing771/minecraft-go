package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

func TestChunkV6MigratesToV9WithoutChangingPayloadSemantics(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "chunk-v6.bin"))
	if err != nil {
		t.Fatal(err)
	}
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	got, err := decodeChunkPayload(key, 19, data)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Migrated || got.Schema != currentChunkSchema {
		t.Fatalf("v6 迁移结果 schema=%d migrated=%v", got.Schema, got.Migrated)
	}
}

func TestChunkV7FixtureMigratesToV9WithLightBlockAndDrop(t *testing.T) {
	if currentChunkSchema != 9 {
		t.Fatalf("区块 schema=%d，想要 9", currentChunkSchema)
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

	data, err := os.ReadFile(filepath.Join("testdata", "chunk-v7.bin"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 19, data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != currentChunkSchema || !got.Migrated {
		t.Fatalf("v7 迁移结果 schema=%d migrated=%v", got.Schema, got.Migrated)
	}
	if got.Key != key || got.Revision != 19 {
		t.Fatalf("v7 迁移 identity key=%+v revision=%d", got.Key, got.Revision)
	}
	if got.Chunk.Hash() != want.Hash() || got.Chunk.DropsHash() != want.DropsHash() {
		t.Fatal("v7 迁移改变了方块或掉落物状态")
	}
	for slot := range core.FurnacesPerChunk {
		if got.Chunk.Furnace(slot) != want.Furnace(slot) {
			t.Fatalf("v7 迁移改变熔炉槽 %d", slot)
		}
	}
	for slot := range core.ChestsPerChunk {
		if got.Chunk.Chest(slot) != want.Chest(slot) {
			t.Fatalf("v7 迁移改变箱子槽 %d", slot)
		}
	}
}

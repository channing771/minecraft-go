package storage

import (
	"bytes"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

func TestChunkPayloadRoundTripsDeterministically(t *testing.T) {
	key := core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{X: -3, Z: 7},
	}
	chunk := codecFixtureChunk(key.Pos)
	save := ChunkSave{Key: key, Revision: 19, Chunk: chunk}

	one, err := encodeChunkPayload(save)
	if err != nil {
		t.Fatal(err)
	}
	two, err := encodeChunkPayload(save)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(one, two) {
		t.Fatal("same chunk encoded differently")
	}

	got, err := decodeChunkPayload(key, 19, one)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != key || got.Revision != 19 || got.Chunk.Hash() != chunk.Hash() {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestChunkSchemaV8RoundTripsCommonBlockMaterialPalette(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	chunk := world.NewChunk(key.Pos)
	want := []core.BlockID{
		core.CobblestoneID, core.SmoothStoneID, core.SandID, core.GravelID,
		core.OakLogID, core.OakPlanksID, core.LeavesID, core.GlassID,
		core.BrickID, core.WhiteWoolID, core.RoofTileID, core.ClayID,
		core.SnowBlockID, core.MossyCobblestoneID,
	}
	for index, id := range want {
		setFixtureBlock(chunk, 0, index, id)
	}
	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 23, Chunk: chunk})
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeChunkPayload(key, 23, encoded)
	if err != nil {
		t.Fatal(err)
	}
	if currentChunkSchema != 8 || got.Schema != 8 || got.Migrated {
		t.Fatalf("区块 schema=%d decoded=%d migrated=%v，想要 8/8/false", currentChunkSchema, got.Schema, got.Migrated)
	}
	for index, id := range want {
		x := index & core.SectionMask
		z := index >> core.SectionShift & core.SectionMask
		if block := got.Chunk.BlockAt(x, core.MinY, z); block != id {
			t.Fatalf("新材料 %d 往返 = %d，想要 %d", index, block, id)
		}
	}
}

func TestChunkPayloadRejectsInvalidSave(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	valid := ChunkSave{Key: key, Revision: 19, Chunk: codecFixtureChunk(key.Pos)}

	tests := []struct {
		name   string
		mutate func(*ChunkSave)
	}{
		{
			name:   "nil chunk",
			mutate: func(save *ChunkSave) { save.Chunk = nil },
		},
		{
			name:   "zero revision",
			mutate: func(save *ChunkSave) { save.Revision = 0 },
		},
		{
			name:   "key chunk position mismatch",
			mutate: func(save *ChunkSave) { save.Chunk.Pos.X++ },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			save := valid
			tc.mutate(&save)
			if _, err := encodeChunkPayload(save); err == nil {
				t.Fatal("invalid save was encoded")
			}
		})
	}
}

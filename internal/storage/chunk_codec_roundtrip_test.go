package storage

import (
	"bytes"
	"testing"

	"minecraft-go/internal/core"
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

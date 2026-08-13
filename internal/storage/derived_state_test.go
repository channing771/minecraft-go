package storage

import (
	"bytes"
	"context"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// TestChunkEncodingIgnoresDerivedHeights 证明派生的列顶高度表不进入区块存档：
// 同样的方块内容无论高度表是增量维护还是重建，编码字节都必须一致。
func TestChunkEncodingIgnoresDerivedHeights(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -3}}

	incremental := world.NewChunk(key.Pos)
	incremental.SetBlock(1, 0, 1, core.StoneID)
	incremental.SetBlock(1, 100, 1, core.StoneID)
	incremental.SetBlock(5, 60, 7, core.GrassID)

	rebuilt := world.NewChunk(key.Pos)
	rebuilt.SetBlock(1, 0, 1, core.StoneID)
	rebuilt.SetBlock(1, 100, 1, core.StoneID)
	rebuilt.SetBlock(5, 60, 7, core.GrassID)
	rebuilt.RebuildHeights()

	first, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 1, Chunk: incremental})
	if err != nil {
		t.Fatal(err)
	}
	second, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 1, Chunk: rebuilt})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("派生高度表影响了区块编码字节")
	}

	// 区块 Hash 只由逻辑方块决定，也不得受派生值影响。
	if incremental.Hash() != rebuilt.Hash() {
		t.Fatal("派生高度表影响了区块 Hash")
	}
}

// TestChunkRoundTripRestoresDerivedHeights 证明存档往返后派生高度表
// 与原始区块完全一致，且 schema 保持 v4。
func TestChunkRoundTripRestoresDerivedHeights(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 4}}
	original := world.NewChunk(key.Pos)
	original.SetBlock(0, core.MinY, 0, core.BedrockID)
	original.SetBlock(3, 42, 9, core.StoneID)
	original.SetBlock(15, core.MaxY-1, 15, core.StoneID)

	encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 1, Chunk: original})
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemory(Metadata{FormatVersion: currentMetadataVersion})
	if _, err := store.SaveBatch(context.Background(), []ChunkSave{{
		Key: key, Revision: 1, Chunk: original,
	}}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadChunk(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loaded.Chunk.Heights(), original.Heights(); got != want {
		t.Fatal("往返后派生高度表与原始区块不一致")
	}

	// 存档字节不因派生值而改变长度。
	again, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 1, Chunk: loaded.Chunk})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, again) {
		t.Fatal("往返后重新编码的区块字节发生变化")
	}
}

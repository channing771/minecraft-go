package storage

import (
	"fmt"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// TestChunkCodecRoundTripsContainersAcrossWorldSections 防止把容器索引还原出的世界 Y
// 误当作 section-local Y 传给 Chunk.BlockAt，导致合法容器被拒绝。
func TestChunkCodecRoundTripsContainersAcrossWorldSections(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -3, Z: 7}}
	for _, y := range []int32{core.MinY + 1, 18, core.MaxY - 2} {
		t.Run(fmt.Sprintf("y_%d", y), func(t *testing.T) {
			chunk := world.NewChunk(key.Pos)
			furnaceIndex := furnaceBlockIndex(t, key.Pos, 1, y, 2)
			chestIndex := furnaceBlockIndex(t, key.Pos, 3, y, 4)
			chunk.SetBlock(1, y, 2, core.FurnaceID)
			chunk.SetBlock(3, y, 4, core.ChestID)
			chunk.SetFurnace(0, world.FurnaceSlot{
				Generation: 1, Active: true, BlockIndex: furnaceIndex,
			})
			chunk.SetChest(0, world.ChestSlot{
				Generation: 1, Active: true, BlockIndex: chestIndex,
			})

			encoded, err := encodeChunkPayload(ChunkSave{Key: key, Revision: 7, Chunk: chunk})
			if err != nil {
				t.Fatalf("编码 Y=%d 的合法容器: %v", y, err)
			}
			decoded, err := decodeChunkPayload(key, 7, encoded)
			if err != nil {
				t.Fatalf("解码 Y=%d 的合法容器: %v", y, err)
			}
			if decoded.Chunk.Furnace(0) != chunk.Furnace(0) ||
				decoded.Chunk.Chest(0) != chunk.Chest(0) {
				t.Fatalf("Y=%d 的容器槽往返改变", y)
			}
		})
	}
}

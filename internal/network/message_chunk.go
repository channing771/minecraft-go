package network

import (
	"errors"

	"minecraft-go/internal/core"
)

type ForgetChunks struct {
	Dimension core.DimensionID
	Chunks    []core.ChunkPos
}

func (ForgetChunks) serverMessage() {}
func (ForgetChunks) serverPacket()  {}

func (forget ForgetChunks) Validate() error {
	if len(forget.Chunks) < 1 || len(forget.Chunks) > 4096 {
		return errors.New("network: forget chunks count is outside 1..4096")
	}
	seen := make(map[core.ChunkPos]struct{}, len(forget.Chunks))
	for _, chunk := range forget.Chunks {
		if _, duplicate := seen[chunk]; duplicate {
			return errors.New("network: forget chunks contains a duplicate chunk")
		}
		seen[chunk] = struct{}{}
	}
	return nil
}

// maxChunkBlockIndex 是区块内方块索引的排他上限。
const maxChunkBlockIndex = core.SectionsPerChunk * core.BlocksPerSection

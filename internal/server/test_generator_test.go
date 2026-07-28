package server

import (
	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

type flatTestGenerator struct{}

// FlatTestGenerator exposes the shared flat integration fixture to the
// external-package server tests.
type FlatTestGenerator = flatTestGenerator

var playerIntegrationObstacle = core.BlockPos{X: 0, Y: 1, Z: -6}

func (flatTestGenerator) GenerateChunk(position core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(position)
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			for y := int32(core.MinY); y <= 0; y++ {
				worldPosition := core.BlockPos{
					X: position.X<<core.SectionShift + int32(x),
					Y: y,
					Z: position.Z<<core.SectionShift + int32(z),
				}
				chunk.SetBlock(x, y, z, flatTestGenerator{}.BaseBlockAt(worldPosition))
			}
		}
	}
	if playerIntegrationObstacle.Chunk() == position {
		x, _, z := playerIntegrationObstacle.Local()
		chunk.SetBlock(x, playerIntegrationObstacle.Y, z, core.StoneID)
	}
	chunk.Compact()
	return chunk
}

func (flatTestGenerator) BaseBlockAt(position core.BlockPos) core.BlockID {
	switch {
	case position == playerIntegrationObstacle:
		return core.StoneID
	case position.Y == core.MinY:
		return core.BedrockID
	case position.Y < -1:
		return core.StoneID
	case position.Y == -1:
		return core.DirtID
	case position.Y == 0:
		return core.GrassID
	default:
		return core.AirID
	}
}

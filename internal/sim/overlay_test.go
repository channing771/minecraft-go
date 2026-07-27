package sim_test

import (
	"errors"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func TestDimensionOverlaySurvivesUnloadAndRegeneration(t *testing.T) {
	base := flatBaseBlock
	dimension := sim.NewDimension(core.Overworld, base)
	chunkPos := core.ChunkPos{X: -1, Z: 2}
	breakPos := core.BlockPos{X: -15, Y: 0, Z: 34}
	placePos := core.BlockPos{X: -13, Y: 1, Z: 36}

	loadFlatChunk(t, dimension, chunkPos)
	old, changed, err := dimension.SetBlock(breakPos, core.AirID)
	if err != nil || !changed || old != core.GrassID {
		t.Fatalf("break = (%d,%v,%v)", old, changed, err)
	}
	old, changed, err = dimension.SetBlock(placePos, core.StoneID)
	if err != nil || !changed || old != core.AirID {
		t.Fatalf("place = (%d,%v,%v)", old, changed, err)
	}
	if got := dimension.OverlayEntries(chunkPos); got != 2 {
		t.Fatalf("overlay entries = %d，想要 2", got)
	}

	if err := dimension.Unload(chunkPos); err != nil {
		t.Fatal(err)
	}
	loadFlatChunk(t, dimension, chunkPos)
	if got, ready := dimension.BlockAt(breakPos); !ready || got != core.AirID {
		t.Fatalf("重载后的 break block = (%d,%v)", got, ready)
	}
	if got, ready := dimension.BlockAt(placePos); !ready || got != core.StoneID {
		t.Fatalf("重载后的 place block = (%d,%v)", got, ready)
	}

	if _, changed, err := dimension.SetBlock(breakPos, base(breakPos)); err != nil || !changed {
		t.Fatalf("恢复 break block = (%v,%v)", changed, err)
	}
	if _, changed, err := dimension.SetBlock(placePos, base(placePos)); err != nil || !changed {
		t.Fatalf("恢复 place block = (%v,%v)", changed, err)
	}
	if got := dimension.OverlayEntries(chunkPos); got != 0 {
		t.Fatalf("恢复基础地形后 overlay entries = %d", got)
	}
}

func TestDimensionSetBlockRequiresReadyAndWorldHeight(t *testing.T) {
	dimension := sim.NewDimension(core.Overworld, flatBaseBlock)
	if _, _, err := dimension.SetBlock(
		core.BlockPos{Y: 0},
		core.StoneID,
	); !errors.Is(err, sim.ErrChunkNotReady) {
		t.Fatalf("未加载 SetBlock = %v", err)
	}
	if _, _, err := dimension.SetBlock(
		core.BlockPos{Y: core.MaxY},
		core.StoneID,
	); !errors.Is(err, sim.ErrBlockOutOfWorld) {
		t.Fatalf("越界 SetBlock = %v", err)
	}
}

func loadFlatChunk(
	t *testing.T,
	dimension *sim.Dimension,
	pos core.ChunkPos,
) {
	t.Helper()
	if !dimension.BeginGeneration(pos) {
		t.Fatalf("区块 %+v 没有进入 Generating", pos)
	}
	if err := dimension.ApplyGenerated(pos, generateFlatChunk(pos)); err != nil {
		t.Fatal(err)
	}
}

func generateFlatChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			for y := int32(core.MinY); y <= 0; y++ {
				worldPos := core.BlockPos{
					X: pos.X<<core.SectionShift + int32(x),
					Y: y,
					Z: pos.Z<<core.SectionShift + int32(z),
				}
				chunk.SetBlock(x, y, z, flatBaseBlock(worldPos))
			}
		}
	}
	chunk.Compact()
	return chunk
}

func flatBaseBlock(position core.BlockPos) core.BlockID {
	switch {
	case position.Y < core.MinY || position.Y >= core.MaxY:
		return core.AirID
	case position.Y == core.MinY:
		return core.BedrockID
	case position.Y < 0:
		return core.StoneID
	case position.Y == 0:
		return core.GrassID
	default:
		return core.AirID
	}
}

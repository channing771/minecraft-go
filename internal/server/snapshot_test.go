package server

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/world"
	"github.com/channing771/mornlea/internal/worldgen"
)

func TestBuildChunkSnapshotOwnsDataAndValidates(t *testing.T) {
	source := worldgen.New(42).GenerateChunk(core.ChunkPos{X: -2, Z: 3})
	wantHash := source.Hash()
	message, err := buildChunkSnapshot(core.Overworld, source, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := message.Validate(); err != nil {
		t.Fatalf("构造的快照不合法: %v", err)
	}
	if len(message.Sections) != core.SectionsPerChunk {
		t.Fatalf("sections = %d", len(message.Sections))
	}
	for index, section := range message.Sections {
		if section.Y != int32(index) {
			t.Fatalf("section[%d].Y = %d", index, section.Y)
		}
	}

	source.SetBlock(0, 0, 0, core.BarrierID)
	restored := importChunkSnapshot(t, message)
	if got := restored.Hash(); got != wantHash {
		t.Fatalf("源区块修改影响了已构造快照: %x != %x", got, wantHash)
	}
}

func importChunkSnapshot(
	t *testing.T,
	message network.ChunkSnapshot,
) *world.Chunk {
	t.Helper()
	chunk := world.NewChunk(message.Chunk)
	for index, section := range message.Sections {
		snapshot := world.ContainerSnapshot{
			Kind:    world.StorageKind(section.Storage),
			Single:  section.Single,
			Bits:    section.Bits,
			Palette: append([]core.BlockID(nil), section.Palette...),
			Packed:  append([]uint64(nil), section.Packed...),
		}
		container, err := world.NewPalettedContainerFromSnapshot(snapshot)
		if err != nil {
			t.Fatalf("导入 section %d: %v", index, err)
		}
		chunk.Section(index).Blocks = container
	}
	return chunk
}

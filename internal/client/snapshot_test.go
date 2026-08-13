package client_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/world"
)

func TestMirrorSnapshotImportIsAtomic(t *testing.T) {
	mirror := client.NewMirror()
	position := core.BlockPos{X: 1, Y: core.MinY, Z: 2}
	chunk := world.NewChunk(position.Chunk())
	chunk.SetBlock(1, position.Y, 2, core.StoneID)
	snapshot := snapshotFromChunk(t, core.Overworld, chunk, 3)

	if _, err := mirror.Apply(snapshot); err != nil {
		t.Fatalf("导入合法快照: %v", err)
	}
	wantHash, wantRevision, ok := mirror.Hash(core.Overworld, chunk.Pos)
	if !ok || wantRevision != 3 {
		t.Fatalf("导入后 Hash ok=%v revision=%d", ok, wantRevision)
	}

	malformed := cloneSnapshot(snapshot)
	section := position.SectionIndex()
	malformed.Sections[section].Packed = malformed.Sections[section].Packed[:1]
	if _, err := mirror.Apply(malformed); err == nil {
		t.Fatal("畸形快照未被拒绝")
	}
	gotHash, gotRevision, ok := mirror.Hash(core.Overworld, chunk.Pos)
	if !ok || gotRevision != wantRevision || gotHash != wantHash {
		t.Fatalf("畸形导入改变镜像: ok=%v revision=%d hash=%x", ok, gotRevision, gotHash)
	}
	if block, loaded := mirror.BlockAt(core.Overworld, position); !loaded || block != core.StoneID {
		t.Fatalf("畸形导入后 BlockAt = %d, %v", block, loaded)
	}
}

func TestMirrorSnapshotDirtiesOwnAndLoadedNeighborSections(t *testing.T) {
	mirror := client.NewMirror()
	center := core.ChunkPos{}
	east := core.ChunkPos{X: 1}

	first, err := mirror.Apply(snapshotFromChunk(t, core.Overworld, world.NewChunk(center), 1))
	if err != nil {
		t.Fatalf("导入中心快照: %v", err)
	}
	assertSectionKeys(t, first.Dirty, chunkSectionKeys(core.Overworld, center))

	second, err := mirror.Apply(snapshotFromChunk(t, core.Overworld, world.NewChunk(east), 1))
	if err != nil {
		t.Fatalf("导入东侧快照: %v", err)
	}
	want := append(chunkSectionKeys(core.Overworld, center), chunkSectionKeys(core.Overworld, east)...)
	assertSectionKeys(t, second.Dirty, want)
}

func snapshotFromChunk(
	t testing.TB,
	dimension core.DimensionID,
	chunk *world.Chunk,
	revision uint64,
) network.ChunkSnapshot {
	t.Helper()
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		snapshot := chunk.Section(index).Blocks.Snapshot()
		sections[index] = network.SectionData{
			Y:       int32(index),
			Storage: network.SectionStorage(snapshot.Kind),
			Single:  snapshot.Single,
			Bits:    snapshot.Bits,
			Palette: append([]core.BlockID(nil), snapshot.Palette...),
			Packed:  append([]uint64(nil), snapshot.Packed...),
		}
	}
	result := network.ChunkSnapshot{
		Dimension: dimension,
		Chunk:     chunk.Pos,
		Revision:  revision,
		Sections:  sections,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("测试快照非法: %v", err)
	}
	return result
}

func cloneSnapshot(source network.ChunkSnapshot) network.ChunkSnapshot {
	clone := source
	clone.Sections = append([]network.SectionData(nil), source.Sections...)
	for index := range clone.Sections {
		clone.Sections[index].Palette = append(
			[]core.BlockID(nil), source.Sections[index].Palette...,
		)
		clone.Sections[index].Packed = append(
			[]uint64(nil), source.Sections[index].Packed...,
		)
	}
	return clone
}

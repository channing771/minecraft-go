package network_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func TestChunkSnapshotValidatesCanonicalSections(t *testing.T) {
	snapshot := validChunkSnapshot()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("合法快照被拒绝: %v", err)
	}
	if got, want := snapshot.PayloadBytes(), core.SectionsPerChunk*2; got != want {
		t.Fatalf("PayloadBytes = %d，想要 %d", got, want)
	}

	indexed := network.SectionData{
		Y:       0,
		Storage: network.SectionIndexed,
		Bits:    4,
		Palette: []core.BlockID{core.AirID, core.StoneID},
		Packed:  make([]uint64, 256),
	}
	if err := indexed.Validate(); err != nil {
		t.Fatalf("合法 indexed section 被拒绝: %v", err)
	}
	if got, want := indexed.PayloadBytes(), 2*2+256*8; got != want {
		t.Fatalf("indexed PayloadBytes = %d，想要 %d", got, want)
	}
}

func TestChunkSnapshotRejectsMalformedSections(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*network.ChunkSnapshot)
	}{
		{
			name: "section count",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections = snapshot.Sections[:23]
			},
		},
		{
			name: "duplicate and missing Y",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[23].Y = 22
			},
		},
		{
			name: "out of order Y",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0], snapshot.Sections[1] =
					snapshot.Sections[1], snapshot.Sections[0]
			},
		},
		{
			name: "unknown storage",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0].Storage = network.SectionStorage(99)
			},
		},
		{
			name: "illegal bits",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0] = validIndexedSection(0)
				snapshot.Sections[0].Bits = 5
			},
		},
		{
			name: "wrong packed words",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0] = validIndexedSection(0)
				snapshot.Sections[0].Packed = snapshot.Sections[0].Packed[:255]
			},
		},
		{
			name: "indexed slot outside palette",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0] = validIndexedSection(0)
				snapshot.Sections[0].Palette = snapshot.Sections[0].Palette[:1]
				snapshot.Sections[0].Packed[0] = 1
			},
		},
		{
			name: "block ID exceeds 15 bits",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0].Single = core.BlockID(1 << 15)
			},
		},
		{
			name: "direct unused high bits",
			mutate: func(snapshot *network.ChunkSnapshot) {
				snapshot.Sections[0] = network.SectionData{
					Y:       0,
					Storage: network.SectionDirect,
					Bits:    15,
					Packed:  make([]uint64, 1024),
				}
				snapshot.Sections[0].Packed[0] = uint64(1) << 63
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := validChunkSnapshot()
			tc.mutate(&snapshot)
			if err := snapshot.Validate(); err == nil {
				t.Fatal("想要快照验证错误")
			}
		})
	}
}

func TestSectionDataRejectsUnknownBlockEveryStorage(t *testing.T) {
	unknown := core.MossyCobblestoneID + 1
	tests := []struct {
		name    string
		section network.SectionData
	}{
		{
			name: "single",
			section: network.SectionData{
				Storage: network.SectionSingle,
				Single:  unknown,
			},
		},
		{
			name: "indexed",
			section: network.SectionData{
				Storage: network.SectionIndexed,
				Bits:    4,
				Palette: []core.BlockID{core.AirID, unknown},
				Packed:  make([]uint64, 256),
			},
		},
		{
			name: "direct",
			section: network.SectionData{
				Storage: network.SectionDirect,
				Bits:    15,
				Packed:  append([]uint64{uint64(unknown)}, make([]uint64, 1023)...),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.section.Validate(); err == nil {
				t.Fatalf("未注册方块 %d 被接受", unknown)
			}
		})
	}
}

func TestBlockChangesValidateRevisionPositionAndOrder(t *testing.T) {
	valid := network.BlockChanges{
		Dimension:    core.Overworld,
		Chunk:        core.ChunkPos{X: -2, Z: 1},
		BaseRevision: 3,
		NewRevision:  4,
		Changes: []network.BlockChange{
			{
				Position: core.BlockPos{X: -32, Y: core.MinY, Z: 16},
				Block:    core.StoneID,
			},
			{
				Position: core.BlockPos{X: -31, Y: core.MinY, Z: 16},
				Block:    core.DirtID,
			},
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法增量被拒绝: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*network.BlockChanges)
	}{
		{
			name: "revision gap",
			mutate: func(changes *network.BlockChanges) {
				changes.NewRevision = 5
			},
		},
		{
			name: "wrong chunk",
			mutate: func(changes *network.BlockChanges) {
				changes.Changes[0].Position.X = -33
			},
		},
		{
			name: "outside world height",
			mutate: func(changes *network.BlockChanges) {
				changes.Changes[0].Position.Y = core.MaxY
			},
		},
		{
			name: "not strictly index sorted",
			mutate: func(changes *network.BlockChanges) {
				changes.Changes[0], changes.Changes[1] =
					changes.Changes[1], changes.Changes[0]
			},
		},
		{
			name: "unregistered block ID",
			mutate: func(changes *network.BlockChanges) {
				changes.Changes[0].Block = core.MossyCobblestoneID + 1
			},
		},
		{
			name: "too many changes",
			mutate: func(changes *network.BlockChanges) {
				changes.Changes = make([]network.BlockChange, 4097)
				for index := range changes.Changes {
					changes.Changes[index] = network.BlockChange{
						Position: core.BlockPos{
							X: int32(index % core.SectionSize),
							Y: core.MinY + int32(index/(core.SectionSize*core.SectionSize)),
							Z: int32((index / core.SectionSize) % core.SectionSize),
						},
						Block: core.StoneID,
					}
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			changes := valid
			changes.Changes = append([]network.BlockChange(nil), valid.Changes...)
			tc.mutate(&changes)
			if err := changes.Validate(); err == nil {
				t.Fatal("想要增量验证错误")
			}
		})
	}
}

func validChunkSnapshot() network.ChunkSnapshot {
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for y := range sections {
		sections[y] = network.SectionData{
			Y:       int32(y),
			Storage: network.SectionSingle,
			Single:  core.AirID,
		}
	}
	return network.ChunkSnapshot{
		Dimension: core.Overworld,
		Chunk:     core.ChunkPos{X: -2, Z: 1},
		Revision:  1,
		Sections:  sections,
	}
}

func validIndexedSection(y int32) network.SectionData {
	return network.SectionData{
		Y:       y,
		Storage: network.SectionIndexed,
		Bits:    4,
		Palette: []core.BlockID{core.AirID, core.StoneID},
		Packed:  make([]uint64, 256),
	}
}

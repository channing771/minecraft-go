package world_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
	"github.com/channing771/mornlea/internal/worldgen"
)

func TestContainerSnapshotRoundTripsEveryStorageKind(t *testing.T) {
	tests := []struct {
		name string
		kind world.StorageKind
		make func() *world.PalettedContainer
	}{
		{
			name: "single",
			kind: world.StorageSingle,
			make: func() *world.PalettedContainer {
				return world.NewPalettedContainer(core.AirID)
			},
		},
		{
			name: "indexed4",
			kind: world.StorageIndexed,
			make: func() *world.PalettedContainer {
				c := world.NewPalettedContainer(core.AirID)
				fillDistinct(c, 1, 8)
				return c
			},
		},
		{
			name: "indexed8",
			kind: world.StorageIndexed,
			make: func() *world.PalettedContainer {
				c := world.NewPalettedContainer(core.AirID)
				fillDistinct(c, 1, 16)
				return c
			},
		},
		{
			name: "direct",
			kind: world.StorageDirect,
			make: func() *world.PalettedContainer {
				c := world.NewPalettedContainer(core.AirID)
				fillDistinct(c, 1, 256)
				for i := 0; i < 256; i++ {
					x := i & core.SectionMask
					z := (i >> core.SectionShift) & core.SectionMask
					y := (i >> (2 * core.SectionShift)) & core.SectionMask
					c.Set(x, y, z, core.BlockID(i%int(core.MossyCobblestoneID+1)))
				}
				return c
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.make()
			snapshot := source.Snapshot()
			if snapshot.Kind != tc.kind {
				t.Fatalf("Kind = %v，想要 %v", snapshot.Kind, tc.kind)
			}
			switch tc.name {
			case "indexed4":
				if snapshot.Bits != 4 {
					t.Fatalf("Bits = %d，想要 4", snapshot.Bits)
				}
			case "indexed8":
				if snapshot.Bits != 8 {
					t.Fatalf("Bits = %d，想要 8", snapshot.Bits)
				}
			case "direct":
				if snapshot.Bits != 15 {
					t.Fatalf("Bits = %d，想要 15", snapshot.Bits)
				}
			}

			restored, err := world.NewPalettedContainerFromSnapshot(snapshot)
			if err != nil {
				t.Fatalf("导入快照: %v", err)
			}
			assertContainersEqual(t, source, restored)
		})
	}
}

func TestContainerSnapshotDoesNotAliasSourceOrInput(t *testing.T) {
	source := world.NewPalettedContainer(core.AirID)
	source.Set(1, 2, 3, core.StoneID)
	snapshot := source.Snapshot()

	source.Set(1, 2, 3, core.DirtID)
	restored, err := world.NewPalettedContainerFromSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.Get(1, 2, 3); got != core.StoneID {
		t.Fatalf("restored = %d，想要 stone", got)
	}

	snapshot.Palette[1] = core.GrassID
	for i := range snapshot.Packed {
		snapshot.Packed[i] = 0
	}
	if got := restored.Get(1, 2, 3); got != core.StoneID {
		t.Fatalf("修改输入快照影响导入结果: got %d，想要 stone", got)
	}
}

func TestContainerSnapshotRejectsMalformedData(t *testing.T) {
	indexed := func() world.ContainerSnapshot {
		return world.ContainerSnapshot{
			Kind:    world.StorageIndexed,
			Bits:    4,
			Palette: []core.BlockID{core.AirID, core.StoneID},
			Packed:  make([]uint64, 256),
		}
	}
	direct := func() world.ContainerSnapshot {
		return world.ContainerSnapshot{
			Kind:   world.StorageDirect,
			Bits:   15,
			Packed: make([]uint64, 1024),
		}
	}

	tests := []struct {
		name     string
		snapshot func() world.ContainerSnapshot
	}{
		{
			name: "unknown storage",
			snapshot: func() world.ContainerSnapshot {
				return world.ContainerSnapshot{Kind: world.StorageKind(99)}
			},
		},
		{
			name: "illegal indexed bits",
			snapshot: func() world.ContainerSnapshot {
				s := indexed()
				s.Bits = 5
				return s
			},
		},
		{
			name: "short packed data",
			snapshot: func() world.ContainerSnapshot {
				s := indexed()
				s.Packed = s.Packed[:len(s.Packed)-1]
				return s
			},
		},
		{
			name: "palette index out of range",
			snapshot: func() world.ContainerSnapshot {
				s := indexed()
				s.Palette = s.Palette[:1]
				s.Packed[0] = 1
				return s
			},
		},
		{
			name: "empty indexed palette",
			snapshot: func() world.ContainerSnapshot {
				s := indexed()
				s.Palette = nil
				return s
			},
		},
		{
			name: "duplicate indexed palette",
			snapshot: func() world.ContainerSnapshot {
				s := indexed()
				s.Palette[1] = core.AirID
				return s
			},
		},
		{
			name: "single ID exceeds 15 bits",
			snapshot: func() world.ContainerSnapshot {
				return world.ContainerSnapshot{
					Kind:   world.StorageSingle,
					Single: core.BlockID(1 << 15),
				}
			},
		},
		{
			name: "indexed ID exceeds 15 bits",
			snapshot: func() world.ContainerSnapshot {
				s := indexed()
				s.Palette[1] = core.BlockID(1 << 15)
				return s
			},
		},
		{
			name: "direct unused high bits",
			snapshot: func() world.ContainerSnapshot {
				s := direct()
				s.Packed[0] = uint64(1) << 63
				return s
			},
		},
		{
			name: "single has packed payload",
			snapshot: func() world.ContainerSnapshot {
				return world.ContainerSnapshot{
					Kind:   world.StorageSingle,
					Single: core.AirID,
					Packed: []uint64{0},
				}
			},
		},
		{
			name: "direct has palette",
			snapshot: func() world.ContainerSnapshot {
				s := direct()
				s.Palette = []core.BlockID{core.AirID}
				return s
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := world.NewPalettedContainerFromSnapshot(tc.snapshot()); err == nil {
				t.Fatal("想要快照验证错误")
			}
		})
	}
}

func TestContainerSnapshotRejectsUnknownBlockEveryStorage(t *testing.T) {
	// 未注册编号一律用独占哨兵 core.BlockIDMax 表达：写死具体编号（历史上写过
	// MossyCobblestoneID+1、WaterLevel7ID+1）会在追加新方块时静默变成已注册。
	unknown := core.BlockIDMax
	tests := []struct {
		name     string
		snapshot world.ContainerSnapshot
	}{
		{
			name: "single",
			snapshot: world.ContainerSnapshot{
				Kind:   world.StorageSingle,
				Single: unknown,
			},
		},
		{
			name: "indexed",
			snapshot: world.ContainerSnapshot{
				Kind:    world.StorageIndexed,
				Bits:    4,
				Palette: []core.BlockID{core.AirID, unknown},
				Packed:  make([]uint64, 256),
			},
		},
		{
			name: "direct",
			snapshot: world.ContainerSnapshot{
				Kind:   world.StorageDirect,
				Bits:   15,
				Packed: append([]uint64{uint64(unknown)}, make([]uint64, 1023)...),
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := world.NewPalettedContainerFromSnapshot(tc.snapshot); err == nil {
				t.Fatalf("未注册方块 %d 被接受", unknown)
			}
		})
	}
}

func BenchmarkExportChunkSnapshot(b *testing.B) {
	chunk := worldgen.New(42, false).GenerateChunk(core.ChunkPos{X: 3, Z: -7})
	b.ReportAllocs()
	for b.Loop() {
		for i := 0; i < core.SectionsPerChunk; i++ {
			_ = chunk.Section(i).Blocks.Snapshot()
		}
	}
}

func BenchmarkImportChunkSnapshot(b *testing.B) {
	chunk := worldgen.New(42, false).GenerateChunk(core.ChunkPos{X: 3, Z: -7})
	snapshots := make([]world.ContainerSnapshot, core.SectionsPerChunk)
	for i := range snapshots {
		snapshots[i] = chunk.Section(i).Blocks.Snapshot()
	}
	b.ReportAllocs()
	for b.Loop() {
		for _, snapshot := range snapshots {
			if _, err := world.NewPalettedContainerFromSnapshot(snapshot); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func fillDistinct(c *world.PalettedContainer, first, count int) {
	for i := 0; i < count; i++ {
		x := i & core.SectionMask
		z := (i >> core.SectionShift) & core.SectionMask
		y := (i >> (2 * core.SectionShift)) & core.SectionMask
		c.Set(x, y, z, core.BlockID(first+i))
	}
}

func assertContainersEqual(
	t *testing.T,
	a, b *world.PalettedContainer,
) {
	t.Helper()
	for y := 0; y < core.SectionSize; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				if got, want := b.Get(x, y, z), a.Get(x, y, z); got != want {
					t.Fatalf("(%d,%d,%d): restored = %d，想要 %d", x, y, z, got, want)
				}
			}
		}
	}
}

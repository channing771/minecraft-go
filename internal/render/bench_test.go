package render_test

import (
	"fmt"
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

const benchSeed = 20260726

func BenchmarkGenerateChunk(b *testing.B) {
	g := worldgen.New(benchSeed)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.GenerateChunk(core.ChunkPos{X: int32(i & 63), Z: int32((i >> 6) & 63)})
	}
}

func BenchmarkMeshChunk(b *testing.B) {
	g := worldgen.New(benchSeed)
	reg := assets.NewRegistry()
	chunks := make(map[core.ChunkPos]*world.Chunk, 9)
	for cx := int32(-1); cx <= 1; cx++ {
		for cz := int32(-1); cz <= 1; cz++ {
			pos := core.ChunkPos{X: cx, Z: cz}
			chunks[pos] = g.GenerateChunk(pos)
		}
	}
	get := func(p core.ChunkPos) *world.Chunk { return chunks[p] }
	light := mesh.NewLightScratch()
	b.ResetTimer()
	for range b.N {
		for si := 0; si < core.SectionsPerChunk; si++ {
			_ = mesh.MeshSection(world.NeighborhoodAt(get, core.ChunkPos{}, si), reg, light)
		}
	}
}

func BenchmarkChunkSnapshotExport(b *testing.B) {
	chunk := worldgen.New(benchSeed).GenerateChunk(core.ChunkPos{})
	b.ReportAllocs()
	for iteration := 0; iteration < b.N; iteration++ {
		for section := 0; section < core.SectionsPerChunk; section++ {
			_ = chunk.Section(section).Blocks.Snapshot()
		}
	}
}

func BenchmarkChunkSnapshotImport(b *testing.B) {
	chunk := worldgen.New(benchSeed).GenerateChunk(core.ChunkPos{})
	snapshots := make([]world.ContainerSnapshot, core.SectionsPerChunk)
	for section := range snapshots {
		snapshots[section] = chunk.Section(section).Blocks.Snapshot()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		imported := world.NewChunk(core.ChunkPos{})
		for section := 0; section < len(snapshots); section++ {
			container, err := world.NewPalettedContainerFromSnapshot(snapshots[section])
			if err != nil {
				b.Fatal(err)
			}
			sectionData := imported.Section(section)
			sectionData.Blocks = container
		}
	}
}

func BenchmarkVisibleSections(b *testing.B) {
	g := worldgen.New(benchSeed)
	reg := assets.NewRegistry()
	conns := make(map[core.SectionPos]mesh.Connectivity, 65*65*core.SectionsPerChunk)
	for cx := int32(-32); cx <= 32; cx++ {
		for cz := int32(-32); cz <= 32; cz++ {
			ch := g.GenerateChunk(core.ChunkPos{X: cx, Z: cz})
			for si := 0; si < core.SectionsPerChunk; si++ {
				conns[core.SectionPos{X: cx, Y: int32(si), Z: cz}] =
					mesh.ComputeConnectivity(ch.Section(si), reg)
			}
		}
	}
	lookup := func(p core.SectionPos) (mesh.Connectivity, bool) {
		c, ok := conns[p]
		return c, ok
	}
	const sectionRecordBytes = 32
	for _, radius := range []int{16, 24, 32} {
		b.Run(fmt.Sprintf("r%d", radius), func(b *testing.B) {
			var scratch mesh.VisibilityScratch
			var visible []core.SectionPos
			b.ResetTimer()
			for range b.N {
				visible = mesh.VisibleSectionsInto(visible, &scratch,
					core.SectionPos{Y: 8}, radius, mesh.EverythingVisible(), lookup)
				b.ReportMetric(float64(len(visible)), "candidate_sections")
				b.ReportMetric(float64(len(visible)*sectionRecordBytes), "candidate_bytes/frame")
			}
		})
	}
}

func BenchmarkPalettePayloadEstimate(b *testing.B) {
	g := worldgen.New(benchSeed)
	var totalBytes int
	const radius = 32
	for cx := int32(-radius); cx <= radius; cx++ {
		for cz := int32(-radius); cz <= radius; cz++ {
			ch := g.GenerateChunk(core.ChunkPos{X: cx, Z: cz})
			for si := 0; si < core.SectionsPerChunk; si++ {
				totalBytes += ch.Section(si).Blocks.PayloadBytes()
			}
		}
	}
	b.ReportMetric(float64(totalBytes)/(1<<20), "MB")
	if mb := totalBytes / (1 << 20); mb > 300 {
		b.Fatalf("32 视距调色板 payload 估算 %d MB，超出预算", mb)
	}
}

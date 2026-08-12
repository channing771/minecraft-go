package worldgen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/worldgen"
)

var update = flag.Bool("update", false, "重写黄金文件")

func TestGenerateChunkGolden(t *testing.T) {
	g := worldgen.New(42)

	var b strings.Builder
	for _, pos := range []core.ChunkPos{
		{X: 0, Z: 0}, {X: 1, Z: 0}, {X: -1, Z: -1}, {X: 37, Z: -104},
	} {
		c := g.GenerateChunk(pos)
		h := sha256.New()
		for y := int32(core.MinY); y < core.MaxY; y++ {
			for z := 0; z < core.SectionSize; z++ {
				for x := 0; x < core.SectionSize; x++ {
					id := c.BlockAt(x, y, z)
					_, _ = h.Write([]byte{byte(id), byte(id >> 8)})
				}
			}
		}
		fmt.Fprintf(&b, "chunk(%d,%d) %s\n", pos.X, pos.Z, hex.EncodeToString(h.Sum(nil)))
	}
	got := b.String()

	golden := filepath.Join("testdata", "golden_seed42.txt")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("黄金文件已重写")
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("读黄金文件失败（首次运行请加 -update）: %v", err)
	}
	if got != string(want) {
		t.Fatalf("地形输出已改变\n实际:\n%s\n期望:\n%s", got, want)
	}
}

func TestGenerateChunkIsDeterministic(t *testing.T) {
	pos := core.ChunkPos{X: 5, Z: -3}
	a := worldgen.New(1234).GenerateChunk(pos)
	b := worldgen.New(1234).GenerateChunk(pos)
	for y := int32(core.MinY); y < core.MaxY; y++ {
		for z := 0; z < core.SectionSize; z++ {
			for x := 0; x < core.SectionSize; x++ {
				if a.BlockAt(x, y, z) != b.BlockAt(x, y, z) {
					t.Fatalf("(%d,%d,%d) 处两次生成结果不同", x, y, z)
				}
			}
		}
	}
}

func TestBaseBlockAtMatchesGeneratedChunk(t *testing.T) {
	generator := worldgen.New(42)
	for _, horizontal := range []core.BlockPos{
		{X: 15, Z: -17},
		{X: 16, Z: -16},
		{X: -19, Z: -33},
	} {
		chunk := generator.GenerateChunk(horizontal.Chunk())
		x, _, z := horizontal.Local()
		for y := int32(core.MinY); y < core.MaxY; y++ {
			position := core.BlockPos{X: horizontal.X, Y: y, Z: horizontal.Z}
			got := generator.BaseBlockAt(position)
			want := chunk.BlockAt(x, position.Y, z)
			if got != want {
				t.Fatalf(
					"BaseBlockAt(%+v) = %d，GenerateChunk = %d",
					position,
					got,
					want,
				)
			}
		}
	}
	for _, position := range []core.BlockPos{
		{Y: core.MinY - 1},
		{Y: core.MaxY},
	} {
		if got := generator.BaseBlockAt(position); got != core.AirID {
			t.Fatalf("世界高度外 BaseBlockAt(%+v) = %d", position, got)
		}
	}
}

func TestGenerateChunkIsSeamlessAcrossBorders(t *testing.T) {
	g := worldgen.New(99)
	for wz := int32(-40); wz < 40; wz++ {
		h0 := g.HeightAt(15, wz)
		h1 := g.HeightAt(16, wz)
		if d := h0 - h1; d > 4 || d < -4 {
			t.Fatalf("区块边界 x=15/16, z=%d 处高度突变 %d", wz, d)
		}
	}
}

func TestGeneratedChunkCompresses(t *testing.T) {
	c := worldgen.New(7).GenerateChunk(core.ChunkPos{X: 0, Z: 0})
	c.Compact()

	total := 0
	uniform := 0
	for i := 0; i < core.SectionsPerChunk; i++ {
		s := c.Section(i)
		total += s.Blocks.PayloadBytes()
		if _, ok := s.Blocks.IsUniform(); ok {
			uniform++
		}
	}
	if uniform < 15 {
		t.Fatalf("只有 %d/24 个区段是单值态，压缩效果不及预期", uniform)
	}
	if total > 40000 {
		t.Fatalf("单区块 payload 估算 %d 字节，朴素 payload 为 196608，压缩比不达标", total)
	}
}

func BenchmarkGenerateChunkWithOakTrees(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		worldgen.New(42).GenerateChunk(core.ChunkPos{X: -1, Z: -1})
	}
}

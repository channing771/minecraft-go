package client_test

import (
	"testing"
	"time"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/mesh"
	"minecraft-go/internal/world"
)

// skyMirror 载入一个 3×3 区块镜像，中心列在 baseY 有一层地面。
func skyMirror(t testing.TB, baseY int32) *client.Mirror {
	t.Helper()
	mirror := client.NewMirror()
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			pos := core.ChunkPos{X: x, Z: z}
			chunk := world.NewChunk(pos)
			for lz := 0; lz < core.SectionSize; lz++ {
				for lx := 0; lx < core.SectionSize; lx++ {
					chunk.SetBlock(lx, baseY, lz, core.StoneID)
				}
			}
			if _, err := mirror.Apply(snapshotFromChunk(t, core.Overworld, chunk, 1)); err != nil {
				t.Fatalf("导入区块 %+v: %v", pos, err)
			}
		}
	}
	return mirror
}

func sectionIndexOfY(y int32) int32 { return (y - core.MinY) >> core.SectionShift }

func TestMirrorSnapshotRebuildsColumnHeights(t *testing.T) {
	mirror := skyMirror(t, 64)
	chunk, loaded := mirror.Chunk(core.Overworld, core.ChunkPos{})
	if !loaded {
		t.Fatal("中心区块未加载")
	}
	// snapshot 直接装入 section，派生高度必须已经重建。
	if got := chunk.Chunk.HighestOpaque(3, 5); got != 64 {
		t.Fatalf("快照重建后列顶 = %d，想要 64", got)
	}
}

func TestMirrorNonTopChangeKeepsExistingDirtyRange(t *testing.T) {
	mirror := skyMirror(t, 64)
	// 在列顶之下放置方块不改变遮挡高度，只走既有 ±1 dirty。
	position := core.BlockPos{X: 3, Y: 20, Z: 5}
	update, err := mirror.Apply(blockChanges(core.Overworld, core.ChunkPos{}, 1, position, core.StoneID))
	if err != nil {
		t.Fatal(err)
	}
	if len(update.Dirty) > 9 {
		t.Fatalf("非列顶变化 dirty 数量 = %d，想要不超过既有 ±1 邻域的 9 个", len(update.Dirty))
	}
	for _, key := range update.Dirty {
		if key.Pos.Y != sectionIndexOfY(position.Y) {
			t.Fatalf("非列顶变化标脏了其他高度的区段：%+v", key)
		}
	}
}

func TestMirrorRoofPlacementDirtiesExactVerticalSpan(t *testing.T) {
	mirror := skyMirror(t, 64)
	position := core.BlockPos{X: 3, Y: 200, Z: 5}
	update, err := mirror.Apply(blockChanges(core.Overworld, core.ChunkPos{}, 1, position, core.StoneID))
	if err != nil {
		t.Fatal(err)
	}

	lowSection, highSection := sectionIndexOfY(64), sectionIndexOfY(200)
	seen := make(map[int32]bool)
	for _, key := range update.Dirty {
		if key.Pos.Y < lowSection || key.Pos.Y > highSection {
			t.Fatalf("dirty 超出新旧列顶跨度：%+v", key)
		}
		seen[key.Pos.Y] = true
	}
	for section := lowSection; section <= highSection; section++ {
		if !seen[section] {
			t.Fatalf("列顶跨度内的区段 Y=%d 没有被标脏", section)
		}
	}
	if got := mirrorHeight(t, mirror, core.ChunkPos{}, 3, 5); got != 200 {
		t.Fatalf("放置屋顶后列顶 = %d，想要 200", got)
	}
}

func TestMirrorRoofRemovalDirtiesExactVerticalSpan(t *testing.T) {
	mirror := skyMirror(t, 64)
	position := core.BlockPos{X: 3, Y: 200, Z: 5}
	if _, err := mirror.Apply(blockChanges(
		core.Overworld, core.ChunkPos{}, 1, position, core.StoneID,
	)); err != nil {
		t.Fatal(err)
	}

	update, err := mirror.Apply(blockChanges(core.Overworld, core.ChunkPos{}, 2, position, core.AirID))
	if err != nil {
		t.Fatal(err)
	}
	lowSection, highSection := sectionIndexOfY(64), sectionIndexOfY(200)
	seen := make(map[int32]bool)
	for _, key := range update.Dirty {
		if key.Pos.Y < lowSection || key.Pos.Y > highSection {
			t.Fatalf("移除屋顶 dirty 超出跨度：%+v", key)
		}
		seen[key.Pos.Y] = true
	}
	for section := lowSection; section <= highSection; section++ {
		if !seen[section] {
			t.Fatalf("移除屋顶后区段 Y=%d 没有被标脏", section)
		}
	}
	if got := mirrorHeight(t, mirror, core.ChunkPos{}, 3, 5); got != 64 {
		t.Fatalf("移除屋顶后列顶 = %d，想要 64", got)
	}
}

func TestMirrorSkyDirtyStaysWithinFourChunksAndNinetySixSections(t *testing.T) {
	mirror := skyMirror(t, core.MinY)
	// 区块角上的列顶从世界底部升到世界顶部：最坏跨度加最坏水平邻域。
	position := core.BlockPos{X: 0, Y: core.MaxY - 1, Z: 0}
	update, err := mirror.Apply(blockChanges(core.Overworld, core.ChunkPos{}, 1, position, core.StoneID))
	if err != nil {
		t.Fatal(err)
	}
	if len(update.Dirty) > 96 {
		t.Fatalf("单次变化 dirty 区段数 = %d，想要不超过 96", len(update.Dirty))
	}
	chunks := make(map[core.ChunkPos]bool)
	unique := make(map[core.SectionKey]bool)
	for _, key := range update.Dirty {
		if unique[key] {
			t.Fatalf("dirty 集合含重复项：%+v", key)
		}
		unique[key] = true
		chunks[core.ChunkPos{X: key.Pos.X, Z: key.Pos.Z}] = true
	}
	if len(chunks) > 4 {
		t.Fatalf("dirty 跨越 %d 个区块，想要不超过 4", len(chunks))
	}
}

func mirrorHeight(
	t *testing.T,
	mirror *client.Mirror,
	pos core.ChunkPos,
	lx, lz int,
) int32 {
	t.Helper()
	chunk, loaded := mirror.Chunk(core.Overworld, pos)
	if !loaded {
		t.Fatalf("区块 %+v 未加载", pos)
	}
	return chunk.Chunk.HighestOpaque(lx, lz)
}

// meshedSkyLight 返回中心区段中指定面朝向的 quad 天空光集合。
func meshedSkyLight(results []client.MeshedSection) map[uint8]bool {
	lights := make(map[uint8]bool)
	for _, result := range results {
		for _, quad := range result.Quads {
			if quad.Face == mesh.FacePosY {
				lights[quad.Light] = true
			}
		}
	}
	return lights
}

func TestMesherSkySnapshotSharesChunkStampGeneration(t *testing.T) {
	mirror := skyMirror(t, core.MinY+8)
	mesher := client.NewMesher(assets.NewRegistry(), 2)
	defer mesher.Close()

	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	mesher.MarkDirty(key)
	mesher.Schedule(mirror, 1)
	results := waitForMesherResults(t, mesher, mirror, 1, 5*time.Second)
	if len(results) != 1 {
		t.Fatalf("网格结果数量 = %d，想要 1", len(results))
	}
	if len(results[0].Stamps) != 9 {
		t.Fatalf("stamps = %d，想要 9", len(results[0].Stamps))
	}
	// 九个邻区都已加载且露天，顶面必须取得满天空光。
	if lights := meshedSkyLight(results); !lights[0xF0] {
		t.Fatalf("露天顶面天空光集合 = %v，想要含 0xF0", lights)
	}
}

func TestMesherDiscardsStaleSkyLightAfterRoofChange(t *testing.T) {
	mirror := skyMirror(t, core.MinY+8)
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	defer mesher.Close()

	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	release := mesher.BlockForTest(key)
	mesher.MarkDirty(key)
	mesher.Schedule(mirror, 1)
	waitForMesherStats(t, mesher, 5*time.Second, func(stats client.MesherStats) bool {
		return stats.InFlightJobs == 1
	})

	// job 在飞行中时加盖屋顶：旧的满亮结果必须因 revision 印章失效被丢弃。
	roof := core.BlockPos{X: 3, Y: core.MinY + 40, Z: 5}
	if _, err := mirror.Apply(blockChanges(
		core.Overworld, core.ChunkPos{}, 1, roof, core.StoneID,
	)); err != nil {
		t.Fatal(err)
	}
	release()
	waitForMesherStats(t, mesher, 5*time.Second, func(stats client.MesherStats) bool {
		return stats.ReadyResults == 1
	})
	if got := mesher.Drain(mirror, 1); len(got) != 0 {
		t.Fatalf("接受了屋顶变化前的过期光照结果：%+v", got)
	}

	mesher.Schedule(mirror, 1)
	fresh := waitForMesherResults(t, mesher, mirror, 1, 5*time.Second)
	if lights := meshedSkyLight(fresh); !lights[0xE0] {
		t.Fatalf("屋顶下顶面天空光集合 = %v，想要含相邻露天传播的 0xE0", lights)
	}
}

func BenchmarkSkyDirtyRange(b *testing.B) {
	mirror := client.NewMirror()
	for x := int32(-1); x <= 1; x++ {
		for z := int32(-1); z <= 1; z++ {
			chunk := world.NewChunk(core.ChunkPos{X: x, Z: z})
			for lz := 0; lz < core.SectionSize; lz++ {
				for lx := 0; lx < core.SectionSize; lx++ {
					chunk.SetBlock(lx, core.MinY, lz, core.StoneID)
				}
			}
			if _, err := mirror.Apply(snapshotFromChunk(b, core.Overworld, chunk, 1)); err != nil {
				b.Fatal(err)
			}
		}
	}

	// 最坏跨度：区块角上的列顶在世界底部与顶部之间反复切换。
	position := core.BlockPos{X: 0, Y: core.MaxY - 1, Z: 0}
	revision := uint64(1)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		block := core.StoneID
		if i%2 == 1 {
			block = core.AirID
		}
		if _, err := mirror.Apply(blockChanges(
			core.Overworld, core.ChunkPos{}, revision, position, block,
		)); err != nil {
			b.Fatal(err)
		}
		revision++
	}
}

func BenchmarkMesherSkySnapshot(b *testing.B) {
	mirror := skyMirror(b, core.MinY+8)
	mesher := client.NewMesher(assets.NewRegistry(), 2)
	defer mesher.Close()

	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// 重复标脏同一区段：dirty map 合并后每轮只产生一份九区高度快照。
		mesher.MarkDirty(key, key, key)
		mesher.Schedule(mirror, 1)
		for len(mesher.Drain(mirror, 8)) == 0 {
		}
	}
}

package client_test

import (
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestMesherBuildsInitialChunkAndBoundaryRemeshes(t *testing.T) {
	mirror := client.NewMirror()
	loadMirrorSquare(t, mirror, core.Overworld, core.ChunkPos{}, 1, 1)
	mesher := client.NewMesher(assets.NewRegistry(), 2)
	defer mesher.Close()

	centerSections := chunkSectionKeys(core.Overworld, core.ChunkPos{})
	mesher.MarkDirty(centerSections...)
	mesher.Schedule(mirror, len(centerSections))
	initial := waitForMesherResults(t, mesher, mirror, len(centerSections), 5*time.Second)
	if got := resultKeys(initial); !reflect.DeepEqual(got, sortedKeys(centerSections)) {
		t.Fatalf("初始网格 section = %+v，想要 %+v", got, sortedKeys(centerSections))
	}
	for _, result := range initial {
		if len(result.Stamps) != 9 {
			t.Fatalf("%+v stamps = %d，想要 9", result.Pos, len(result.Stamps))
		}
	}

	corner := core.BlockPos{X: 15, Y: core.MinY + 15, Z: 15}
	update, err := mirror.Apply(blockChanges(
		core.Overworld, core.ChunkPos{}, 1, corner, core.StoneID,
	))
	if err != nil {
		t.Fatalf("应用角落方块增量: %v", err)
	}
	// 区块角的传播半径相交 3×3 个区块和 2 个高度区段。
	if len(update.Dirty) != 18 {
		t.Fatalf("角落 dirty = %+v，想要 18 个", update.Dirty)
	}
	mesher.MarkDirty(update.Dirty...)
	mesher.Schedule(mirror, len(update.Dirty))
	remeshed := waitForMesherResults(t, mesher, mirror, len(update.Dirty), 5*time.Second)
	if got := resultKeys(remeshed); !reflect.DeepEqual(got, sortedKeys(update.Dirty)) {
		t.Fatalf("边界重网格 = %+v，想要 %+v", got, sortedKeys(update.Dirty))
	}
}

func TestMesherDiscardsStaleNeighborRevisionAndRedirties(t *testing.T) {
	mirror := client.NewMirror()
	loadMirrorSquare(t, mirror, core.Overworld, core.ChunkPos{}, 1, 1)
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	defer mesher.Close()

	key := core.SectionKey{
		Dimension: core.Overworld,
		Pos:       core.SectionPos{X: 0, Y: 0, Z: 0},
	}
	release := mesher.BlockForTest(key)
	mesher.MarkDirty(key)
	mesher.Schedule(mirror, 1)
	waitForMesherStats(t, mesher, 5*time.Second, func(stats client.MesherStats) bool {
		return stats.InFlightJobs == 1
	})

	east := core.ChunkPos{X: 1}
	if _, err := mirror.Apply(airSnapshot(core.Overworld, east, 2)); err != nil {
		t.Fatalf("更新输入邻居 revision: %v", err)
	}
	release()
	waitForMesherStats(t, mesher, 5*time.Second, func(stats client.MesherStats) bool {
		return stats.ReadyResults == 1
	})
	if got := mesher.Drain(mirror, 1); len(got) != 0 {
		t.Fatalf("Drain 接受了过期结果: %+v", got)
	}
	if stats := mesher.Stats(); stats.DirtySections != 1 {
		t.Fatalf("丢弃过期结果后 dirty = %d，想要 1", stats.DirtySections)
	}

	mesher.Schedule(mirror, 1)
	valid := waitForMesherResults(t, mesher, mirror, 1, 5*time.Second)
	if len(valid) != 1 || valid[0].Pos != key.Pos {
		t.Fatalf("重调度结果 = %+v", valid)
	}
}

func TestMesherSurvivesPanickingJob(t *testing.T) {
	mirror := client.NewMirror()
	loadMirrorSquare(t, mirror, core.Overworld, core.ChunkPos{}, 1, 1)
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	defer mesher.Close()

	panics := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	later := core.SectionKey{
		Dimension: core.Overworld,
		Pos:       core.SectionPos{X: 0, Y: 1, Z: 0},
	}
	mesher.InjectPanicForTest(panics)
	mesher.MarkDirty(panics, later)
	mesher.Schedule(mirror, 2)
	results := waitForMesherResults(t, mesher, mirror, 1, 5*time.Second)
	if results[0].Pos != later.Pos {
		t.Fatalf("panic 后首个结果 = %+v，想要 %+v", results[0].Pos, later.Pos)
	}

	waitForMesherStats(t, mesher, 5*time.Second, func(stats client.MesherStats) bool {
		return stats.InFlightJobs == 0 && stats.QueuedJobs == 0
	})
	mesher.Schedule(mirror, 1)
	results = waitForMesherResults(t, mesher, mirror, 1, 5*time.Second)
	if results[0].Pos != panics.Pos {
		t.Fatalf("panic 任务未能重试: %+v", results[0].Pos)
	}
}

func TestMesherCloseReturnsWithFullResultQueue(t *testing.T) {
	mirror := client.NewMirror()
	loadMirrorSquare(t, mirror, core.Overworld, core.ChunkPos{}, 1, 1)
	workers := 2
	mesher := client.NewMesher(assets.NewRegistry(), workers)

	var keys []core.SectionKey
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			keys = append(keys, chunkSectionKeys(
				core.Overworld, core.ChunkPos{X: x, Z: z},
			)...)
		}
	}
	mesher.MarkDirty(keys...)
	mesher.Schedule(mirror, len(keys))
	waitForMesherStats(t, mesher, 10*time.Second, func(stats client.MesherStats) bool {
		blockedPublishers := len(keys) - stats.ResultCapacity - workers
		return stats.ReadyResults == stats.ResultCapacity &&
			stats.QueuedJobs == blockedPublishers && stats.InFlightJobs == 0
	})

	done := make(chan struct{})
	go func() {
		mesher.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("满结果队列时 Close 超过 1 秒")
	}
}

func BenchmarkRemeshBoundaryEdit(b *testing.B) {
	mirror := client.NewMirror()
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			if _, err := mirror.Apply(airSnapshot(
				core.Overworld, core.ChunkPos{X: x, Z: z}, 1,
			)); err != nil {
				b.Fatal(err)
			}
		}
	}
	mesher := client.NewMesher(assets.NewRegistry(), 1)
	defer mesher.Close()
	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}

	b.ResetTimer()
	for range b.N {
		mesher.MarkDirty(key)
		mesher.Schedule(mirror, 1)
		for len(mesher.Drain(mirror, 1)) == 0 {
		}
	}
}

func loadMirrorSquare(
	t *testing.T,
	mirror *client.Mirror,
	dimension core.DimensionID,
	center core.ChunkPos,
	radius int32,
	revision uint64,
) {
	t.Helper()
	for z := center.Z - radius; z <= center.Z+radius; z++ {
		for x := center.X - radius; x <= center.X+radius; x++ {
			if _, err := mirror.Apply(airSnapshot(
				dimension, core.ChunkPos{X: x, Z: z}, revision,
			)); err != nil {
				t.Fatalf("导入镜像区块 {%d,%d}: %v", x, z, err)
			}
		}
	}
}

func airSnapshot(
	dimension core.DimensionID,
	position core.ChunkPos,
	revision uint64,
) network.ChunkSnapshot {
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		sections[index] = network.SectionData{
			Y:       int32(index),
			Storage: network.SectionSingle,
			Single:  core.AirID,
		}
	}
	return network.ChunkSnapshot{
		Dimension: dimension,
		Chunk:     position,
		Revision:  revision,
		Sections:  sections,
	}
}

func waitForMesherResults(
	t *testing.T,
	mesher *client.Mesher,
	mirror *client.Mirror,
	want int,
	timeout time.Duration,
) []client.MeshedSection {
	t.Helper()
	deadline := time.Now().Add(timeout)
	results := make([]client.MeshedSection, 0, want)
	for len(results) < want {
		results = append(results, mesher.Drain(mirror, want-len(results))...)
		if time.Now().After(deadline) {
			t.Fatalf("%s 内得到 %d/%d 个结果；stats=%+v", timeout, len(results), want, mesher.Stats())
		}
		time.Sleep(time.Millisecond)
	}
	return results
}

func waitForMesherStats(
	t *testing.T,
	mesher *client.Mesher,
	timeout time.Duration,
	ready func(client.MesherStats) bool,
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		stats := mesher.Stats()
		if ready(stats) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待 Mesher 状态超时: %+v", stats)
		}
		time.Sleep(time.Millisecond)
	}
}

func resultKeys(results []client.MeshedSection) []core.SectionKey {
	keys := make([]core.SectionKey, len(results))
	for index, result := range results {
		keys[index] = core.SectionKey{Dimension: result.Dimension, Pos: result.Pos}
	}
	return sortedKeys(keys)
}

func sortedKeys(keys []core.SectionKey) []core.SectionKey {
	keys = append([]core.SectionKey(nil), keys...)
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dimension != keys[j].Dimension {
			return keys[i].Dimension < keys[j].Dimension
		}
		if keys[i].Pos.X != keys[j].Pos.X {
			return keys[i].Pos.X < keys[j].Pos.X
		}
		if keys[i].Pos.Y != keys[j].Pos.Y {
			return keys[i].Pos.Y < keys[j].Pos.Y
		}
		return keys[i].Pos.Z < keys[j].Pos.Z
	})
	return keys
}

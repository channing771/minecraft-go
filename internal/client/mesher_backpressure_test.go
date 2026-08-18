package client

import (
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestMesherScheduleFullJobQueueDoesNotScanDirtyBacklog(t *testing.T) {
	mesher := newUnstartedMesherForBackpressureTest(1)
	mesher.jobs <- mesherJob{}
	keys := mesherBacklogKeys(90_000)
	mesher.MarkDirty(keys...)
	before := mesher.Stats()
	mirror := NewMirror()
	allocs := testing.AllocsPerRun(5, func() {
		mesher.Schedule(mirror, len(keys))
	})
	if allocs != 0 {
		t.Fatalf("满 job 队列 Schedule allocations = %.1f，想要 0", allocs)
	}
	if after := mesher.Stats(); after != before {
		t.Fatalf("满 job 队列改变状态: before=%+v after=%+v", before, after)
	}
}

func BenchmarkMesherScheduleFullJobQueue90K(b *testing.B) {
	mesher := newUnstartedMesherForBackpressureTest(1)
	mesher.jobs <- mesherJob{}
	mesher.MarkDirty(mesherBacklogKeys(90_000)...)
	mirror := NewMirror()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		mesher.Schedule(mirror, 90_000)
	}
}

func BenchmarkMesherScheduleOneFreeSlot90K(b *testing.B) {
	mesher := newUnstartedMesherForBackpressureTest(2)
	mesher.MarkDirty(mesherBacklogKeys(90_000)...)
	mirror := NewMirror()
	applyAirChunkForBackpressureTest(b, mirror, core.ChunkPos{})
	sentinel := mesherJob{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		mesher.jobs <- sentinel
		mesher.Schedule(mirror, 90_000)
		<-mesher.jobs
		job := <-mesher.jobs
		mesher.mu.Lock()
		delete(mesher.queued, job.key)
		mesher.enqueueReadyLocked(job.key)
		mesher.mu.Unlock()
	}
}

func TestMesherScheduleUsesOnlyAvailableReadyKeys(t *testing.T) {
	mesher := newUnstartedMesherForBackpressureTest(2)
	mesher.jobs <- mesherJob{}
	mirror := NewMirror()
	applyAirChunkForBackpressureTest(t, mirror, core.ChunkPos{})
	smallest := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	keys := []core.SectionKey{
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 2}},
		smallest,
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 1}},
	}
	mesher.MarkDirty(keys...)
	mesher.MarkDirty(smallest)
	mesher.mu.Lock()
	latest := mesher.dirty[smallest]
	readyBefore := mesher.ready.Len()
	mesher.mu.Unlock()
	if readyBefore != 3 {
		t.Fatalf("ready = %d，想要 3 个唯一键", readyBefore)
	}

	mesher.Schedule(mirror, 4096)
	mesher.mu.Lock()
	readyAfter := mesher.ready.Len()
	mesher.mu.Unlock()
	if readyAfter != 2 {
		t.Fatalf("一个空位后 ready = %d，想要 2", readyAfter)
	}
	<-mesher.jobs
	job := <-mesher.jobs
	if job.key != smallest || job.generation != latest {
		t.Fatalf("job=(%+v,%d)，想要 (%+v,%d)", job.key, job.generation, smallest, latest)
	}
}

func TestMesherForgetChunkRemovesReadySections(t *testing.T) {
	mesher := newUnstartedMesherForBackpressureTest(1)
	keys := make([]core.SectionKey, 0, core.SectionsPerChunk+1)
	for y := int32(0); y < core.SectionsPerChunk; y++ {
		keys = append(keys, core.SectionKey{
			Dimension: core.Overworld,
			Pos:       core.SectionPos{Y: y},
		})
	}
	survivor := core.SectionKey{
		Dimension: core.Overworld,
		Pos:       core.SectionPos{X: 1},
	}
	keys = append(keys, survivor)
	mesher.MarkDirty(keys...)
	mesher.ForgetChunk(core.Overworld, core.ChunkPos{})
	if stats := mesher.Stats(); stats.DirtySections != 1 {
		t.Fatalf("dirty = %d，想要 1", stats.DirtySections)
	}
	mesher.mu.Lock()
	readyCount := mesher.ready.Len()
	got, ok := mesher.ready.Take()
	mesher.mu.Unlock()
	if readyCount != 1 || !ok || got != survivor {
		t.Fatalf("ready=(%d,%+v,%v)，想要唯一 survivor", readyCount, got, ok)
	}
}

func TestMesherRedirtyInFlightQueuesLatestGeneration(t *testing.T) {
	mesher := NewMesher(assets.NewRegistry(), 1)
	mirror := NewMirror()
	applyAirChunkForBackpressureTest(t, mirror, core.ChunkPos{})
	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	release := mesher.BlockForTest(key)
	defer func() {
		release()
		mesher.Close()
	}()
	mesher.MarkDirty(key)
	mesher.Schedule(mirror, 1)
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().InFlightJobs == 1
	})
	mesher.MarkDirty(key)
	mesher.mu.Lock()
	latest := mesher.dirty[key]
	mesher.mu.Unlock()
	release()
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().ReadyResults == 1
	})
	if got := mesher.Drain(mirror, 1); len(got) != 0 {
		t.Fatalf("接受了旧 generation: %+v", got)
	}
	waitForMesherBackpressureTest(t, func() bool {
		mesher.mu.Lock()
		defer mesher.mu.Unlock()
		return mesher.ready.Len() == 1 && len(mesher.inFlight) == 0
	})
	mesher.Schedule(mirror, 1)
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().ReadyResults == 1
	})
	got := mesher.Drain(mirror, 1)
	if len(got) != 1 || got[0].Generation != latest {
		t.Fatalf("最新结果 = %+v，想要 generation %d", got, latest)
	}
}

func TestMesherClearsInFlightBeforeBlockedResultPublication(t *testing.T) {
	mesher := NewMesher(assets.NewRegistry(), 1)
	mirror := NewMirror()
	applyAirChunkForBackpressureTest(t, mirror, core.ChunkPos{})
	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	release := mesher.BlockForTest(key)
	for range cap(mesher.results) {
		mesher.results <- mesherResult{}
	}
	defer func() {
		release()
		select {
		case <-mesher.results:
		default:
		}
		mesher.Close()
	}()

	mesher.MarkDirty(key)
	mesher.Schedule(mirror, 1)
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().InFlightJobs == 1
	})
	release()
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().InFlightJobs == 0
	})

	mesher.mu.Lock()
	_, dirty := mesher.dirty[key]
	ready := mesher.ready.Len()
	mesher.mu.Unlock()
	if !dirty || ready != 0 {
		t.Fatalf("阻塞发布前 dirty=%v ready=%d，想要 unchanged generation 保持 dirty 且不 requeue", dirty, ready)
	}
}

func TestMesherRedirtyQueuesLatestBeforeBlockedResultPublication(t *testing.T) {
	mesher := NewMesher(assets.NewRegistry(), 1)
	mirror := NewMirror()
	applyAirChunkForBackpressureTest(t, mirror, core.ChunkPos{})
	key := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	release := mesher.BlockForTest(key)
	for range cap(mesher.results) {
		mesher.results <- mesherResult{}
	}
	defer func() {
		release()
		select {
		case <-mesher.results:
		default:
		}
		mesher.Close()
	}()

	mesher.MarkDirty(key)
	mesher.Schedule(mirror, 1)
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().InFlightJobs == 1
	})
	mesher.MarkDirty(key)
	mesher.mu.Lock()
	latest := mesher.dirty[key]
	mesher.mu.Unlock()
	release()
	waitForMesherBackpressureTest(t, func() bool {
		return mesher.Stats().InFlightJobs == 0
	})

	mesher.mu.Lock()
	current, dirty := mesher.dirty[key]
	readyCount := mesher.ready.Len()
	readyKey, ready := mesher.ready.Take()
	mesher.mu.Unlock()
	resultsFull := len(mesher.results) == cap(mesher.results)
	if !dirty || current != latest || readyCount != 1 || !ready || readyKey != key || !resultsFull {
		t.Fatalf(
			"阻塞发布时 dirty=(%v,%d/%d) ready=(%d,%+v,%v) resultsFull=%v",
			dirty, current, latest, readyCount, readyKey, ready, resultsFull,
		)
	}
}

func applyAirChunkForBackpressureTest(t testing.TB, mirror *Mirror, pos core.ChunkPos) {
	t.Helper()
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		sections[index] = network.SectionData{
			Y: int32(index), Storage: network.SectionSingle, Single: core.AirID,
		}
	}
	if _, err := mirror.Apply(network.ChunkSnapshot{
		Dimension: core.Overworld, Chunk: pos, Revision: 1, Sections: sections,
	}); err != nil {
		t.Fatalf("应用 air snapshot: %v", err)
	}
}

func waitForMesherBackpressureTest(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("5 秒内条件未满足")
		}
		// 热轮询（runtime.Gosched）改为固定 sleep 退避：饱和并行 race 测试中
		// 空转等待抢核拖慢条件生产者并施压邻居测试（与 internal/server 测试
		// 同型治理保持一致）。
		time.Sleep(500 * time.Microsecond)
	}
}

func newUnstartedMesherForBackpressureTest(jobCapacity int) *Mesher {
	return &Mesher{
		registry: assets.NewRegistry(),
		jobs:     make(chan mesherJob, jobCapacity),
		results:  make(chan mesherResult, 1),
		closed:   make(chan struct{}),
		dirty:    make(map[core.SectionKey]uint64),
		ready:    newReadySectionHeap(),
		queued:   make(map[core.SectionKey]uint64),
		inFlight: make(map[core.SectionKey]uint64),
		panicAt:  make(map[core.SectionKey]bool),
		blockAt:  make(map[core.SectionKey]chan struct{}),
	}
}

func mesherBacklogKeys(count int) []core.SectionKey {
	keys := make([]core.SectionKey, count)
	for index := range keys {
		keys[index] = core.SectionKey{
			Dimension: core.Overworld,
			Pos:       core.SectionPos{X: int32(index)},
		}
	}
	return keys
}

# Mesher Bounded Deterministic Scheduling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `Mesher.Schedule`'s full dirty-map scan/sort with an indexed deterministic ready heap whose per-frame work is bounded by actual job-channel capacity.

**Architecture:** `dirty` remains the authoritative generation map. A mutex-protected indexed min-heap holds only dirty keys that are neither queued nor in flight, ordered exactly by `Dimension/X/Z/Y`; `Schedule` pops at most `min(maxJobs, free job slots)` keys, while worker cleanup requeues only panic or newer-generation work.

**Tech Stack:** Go 1.26, standard-library `container/heap`, existing `internal/client` Mesher/Mirror tests, race detector, macOS Metal benchmark, GitNexus.

## Global Constraints

- Work only in `/Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync` on `codex/m3c-multiplayer-sync`.
- Preserve ordering exactly: `Dimension`, `Pos.X`, `Pos.Z`, `Pos.Y`.
- Do not change benchmark scene, camera speed, phase durations, 2560×1440 resolution, 12 ms gate, worker count, channel capacities, upload budget, or v5 baseline.
- Do not change any exported `Mesher` method signature.
- `dirty` remains the only authoritative generation state; ready membership is derived scheduling state.
- No production edit may precede its focused RED.
- Before editing each existing symbol, run worktree-qualified GitNexus upstream impact and report the blast radius.
- `Mesher.handle` is already known HIGH and requires explicit user approval before editing.
- Preserve unrelated dirty-worktree changes; stage only exact scheduler files.
- Task 2's diagnostic artifact is not a formal report and must never become the baseline.

---

### Task 1: Implement the bounded deterministic scheduler with TDD

**Files:**
- Create: `internal/client/mesher_ready_queue.go`
- Create: `internal/client/mesher_ready_queue_test.go`
- Create: `internal/client/mesher_backpressure_test.go`
- Modify: `internal/client/mesher.go:3-6,59-76,79-101,119-219,317-388,450-463`
- Verify without rewriting: `internal/client/mesher_test.go`

**Interfaces:**
- Consumes: `core.SectionKey`, existing `Mesher.dirty/queued/inFlight`, `cloneNeighborhood`, `mesherJob`, and `Schedule(*Mirror, int)`.
- Produces: `readySectionHeap`, `newReadySectionHeap()`, `Add(core.SectionKey) bool`, `Remove(core.SectionKey) bool`, `Take() (core.SectionKey, bool)`, and `Mesher.enqueueReadyLocked(core.SectionKey)`.
- Preserves: existing exported Mesher API, generation semantics, result validation, panic retry, ForgetChunk, and Close behavior.

- [ ] **Step 1: Reconfirm workspace and existing-symbol impact gates**

Run:

```bash
pwd
git branch --show-current
git status --short
```

Expected workspace/branch:

```text
/Users/sheepzhao/WorkSpace/minecraft-go/.worktrees/m3c-multiplayer-sync
codex/m3c-multiplayer-sync
```

Run GitNexus upstream impact for `Mesher`, `NewMesher`, `MarkDirty`, `ForgetChunk`, `Schedule`, `handle`, `markDirtyLocked`, `removeQueued`, and `sortSectionKeySlice` before editing. Known maximum risk is `handle` HIGH: 1 direct caller, 2 affected application-construction processes, 3 modules. `NewMesher`, `MarkDirty`, and `Schedule` are MEDIUM; the rest are LOW. Stop if another HIGH/CRITICAL symbol appears. Do not edit `handle` until the user explicitly approves this warning.

- [ ] **Step 2: Write the ready-heap tests first**

Create `internal/client/mesher_ready_queue_test.go` in package `client`:

```go
package client

import (
	"reflect"
	"testing"

	"minecraft-go/internal/core"
)

func TestReadySectionHeapOrdersAndDeduplicates(t *testing.T) {
	ready := newReadySectionHeap()
	keys := []core.SectionKey{
		{Dimension: 1, Pos: core.SectionPos{X: 0, Y: 0, Z: 0}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 1, Y: 0, Z: 0}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 0, Y: 1, Z: 0}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 0, Y: 0, Z: 1}},
		{Dimension: core.Overworld, Pos: core.SectionPos{X: 0, Y: 0, Z: 0}},
	}
	for _, key := range keys {
		if !ready.Add(key) {
			t.Fatalf("首次 Add(%+v) = false", key)
		}
	}
	if ready.Add(keys[2]) {
		t.Fatal("重复 Add 返回 true")
	}
	want := []core.SectionKey{keys[4], keys[2], keys[3], keys[1], keys[0]}
	got := make([]core.SectionKey, 0, len(want))
	for {
		key, ok := ready.Take()
		if !ok {
			break
		}
		got = append(got, key)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Take 顺序 = %+v，想要 %+v", got, want)
	}
}

func TestReadySectionHeapRemoveMaintainsIndexes(t *testing.T) {
	ready := newReadySectionHeap()
	left := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{X: -1}}
	middle := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{}}
	right := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{X: 1}}
	for _, key := range []core.SectionKey{middle, right, left} {
		ready.Add(key)
	}
	if !ready.Remove(middle) || ready.Remove(middle) {
		t.Fatal("Remove 未精确报告存在性")
	}
	if !ready.Add(middle) || !ready.Remove(left) {
		t.Fatal("删除后重新添加或交换索引失败")
	}
	first, ok := ready.Take()
	if !ok || first != middle {
		t.Fatalf("首项 = %+v,%v，想要 middle", first, ok)
	}
	second, ok := ready.Take()
	if !ok || second != right {
		t.Fatalf("次项 = %+v,%v，想要 right", second, ok)
	}
}
```

These tests must kill a comparator mutation that swaps `Pos.Z` and `Pos.Y`, and a removal mutation that omits index updates in `Swap`.

- [ ] **Step 3: Run the ready-heap RED**

```bash
go test ./internal/client -run '^TestReadySectionHeap' -count=1
```

Expected RED: build failure naming `undefined: newReadySectionHeap`. Reject unrelated import, syntax, or package failures.

- [ ] **Step 4: Add the minimal indexed heap**

Create `internal/client/mesher_ready_queue.go`:

```go
package client

import (
	"container/heap"

	"minecraft-go/internal/core"
)

type readySectionHeap struct {
	keys    []core.SectionKey
	indexes map[core.SectionKey]int
}

func newReadySectionHeap() readySectionHeap {
	return readySectionHeap{indexes: make(map[core.SectionKey]int)}
}

func (ready *readySectionHeap) Len() int { return len(ready.keys) }
func (ready *readySectionHeap) Less(i, j int) bool {
	return sectionKeyLess(ready.keys[i], ready.keys[j])
}
func (ready *readySectionHeap) Swap(i, j int) {
	ready.keys[i], ready.keys[j] = ready.keys[j], ready.keys[i]
	ready.indexes[ready.keys[i]] = i
	ready.indexes[ready.keys[j]] = j
}
func (ready *readySectionHeap) Push(value any) {
	key := value.(core.SectionKey)
	ready.indexes[key] = len(ready.keys)
	ready.keys = append(ready.keys, key)
}
func (ready *readySectionHeap) Pop() any {
	last := len(ready.keys) - 1
	key := ready.keys[last]
	ready.keys[last] = core.SectionKey{}
	ready.keys = ready.keys[:last]
	delete(ready.indexes, key)
	return key
}
func (ready *readySectionHeap) Add(key core.SectionKey) bool {
	if ready.indexes == nil {
		ready.indexes = make(map[core.SectionKey]int)
	}
	if _, exists := ready.indexes[key]; exists {
		return false
	}
	heap.Push(ready, key)
	return true
}
func (ready *readySectionHeap) Remove(key core.SectionKey) bool {
	index, exists := ready.indexes[key]
	if !exists {
		return false
	}
	heap.Remove(ready, index)
	return true
}
func (ready *readySectionHeap) Take() (core.SectionKey, bool) {
	if ready.Len() == 0 {
		return core.SectionKey{}, false
	}
	return heap.Pop(ready).(core.SectionKey), true
}
func sectionKeyLess(left, right core.SectionKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.Pos.X != right.Pos.X {
		return left.Pos.X < right.Pos.X
	}
	if left.Pos.Z != right.Pos.Z {
		return left.Pos.Z < right.Pos.Z
	}
	return left.Pos.Y < right.Pos.Y
}
```

- [ ] **Step 5: Run the ready-heap GREEN**

```bash
gofmt -w internal/client/mesher_ready_queue.go internal/client/mesher_ready_queue_test.go
go test ./internal/client -run '^TestReadySectionHeap' -count=1
```

Expected: PASS with pristine output.

- [ ] **Step 6: Add the full-channel regression and pre-fix benchmark**

Create `internal/client/mesher_backpressure_test.go` in package `client`:

```go
package client

import (
	"testing"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/core"
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

func newUnstartedMesherForBackpressureTest(jobCapacity int) *Mesher {
	return &Mesher{
		registry: assets.NewRegistry(),
		jobs: make(chan mesherJob, jobCapacity),
		results: make(chan mesherResult, 1),
		closed: make(chan struct{}),
		dirty: make(map[core.SectionKey]uint64),
		queued: make(map[core.SectionKey]uint64),
		inFlight: make(map[core.SectionKey]uint64),
		panicAt: make(map[core.SectionKey]bool),
		blockAt: make(map[core.SectionKey]chan struct{}),
	}
}

func mesherBacklogKeys(count int) []core.SectionKey {
	keys := make([]core.SectionKey, count)
	for index := range keys {
		keys[index] = core.SectionKey{
			Dimension: core.Overworld,
			Pos: core.SectionPos{X: int32(index)},
		}
	}
	return keys
}
```

- [ ] **Step 7: Capture the full-channel RED and old benchmark**

```bash
go test ./internal/client -run '^TestMesherScheduleFullJobQueueDoesNotScanDirtyBacklog$' -count=1
go test ./internal/client -run '^$' -bench '^BenchmarkMesherScheduleFullJobQueue90K$' -benchmem -count=3
```

Expected RED: nonzero allocations from the 90k candidate slice. Record all old `ns/op`, `B/op`, and `allocs/op` values in the SDD report.

- [ ] **Step 8: Add only the job-capacity clamp and make the first regression GREEN**

Inside the existing locked preamble of `Mesher.Schedule`, after the closed check:

```go
	freeSlots := cap(mesher.jobs) - len(mesher.jobs)
	if freeSlots <= 0 {
		mesher.mu.Unlock()
		return
	}
	maxJobs = min(maxJobs, freeSlots)
```

Leave the dirty scan/sort in place for this cycle. Run:

```bash
gofmt -w internal/client/mesher.go internal/client/mesher_backpressure_test.go
go test ./internal/client -run '^TestMesherScheduleFullJobQueueDoesNotScanDirtyBacklog$' -count=1
```

Expected GREEN: PASS. Partial-capacity frames remain unfixed, so continue.

- [ ] **Step 9: Add bounded-ready and lifecycle RED tests**

Extend `mesher_backpressure_test.go` with imports `runtime`, `time`, and `minecraft-go/internal/network`.

`TestMesherScheduleUsesOnlyAvailableReadyKeys` must:

1. Build an unstarted Mesher with job capacity 2 and prefill one sentinel job.
2. Load one real air chunk at `{0,0}` into a real Mirror.
3. Mark keys in literal order `{X:2}`, `{X:0}`, `{X:1}`, then mark `{X:0}` again.
4. Assert `mesher.ready.Len()==3` and capture the latest `dirty[{X:0}]` generation.
5. Call `Schedule(mirror, 4096)`.
6. Assert `ready.Len()==2`; pop the sentinel and scheduled job; assert the scheduled key is `{X:0}` and its generation is the captured latest generation.

Use this exact real snapshot helper:

```go
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
```

`TestMesherForgetChunkRemovesReadySections` must mark every section in chunk `{0,0}` plus one survivor in `{1,0}`, call `ForgetChunk({0,0})`, then assert `ready.Len()==1`, `DirtySections==1`, and `ready.Take()` yields only the survivor.

`TestMesherRedirtyInFlightQueuesLatestGeneration` must use `NewMesher(...,1)`, `BlockForTest`, and a real Mirror. After the first job reaches `InFlightJobs==1`, mark the same key again, capture the new generation, release the worker, drain/reject the old result, wait until `ready.Len()==1`, reschedule, and assert the accepted result has the new generation. Poll with a 5-second deadline plus `runtime.Gosched()`; never use an unbounded wait.

Use these concrete test bodies; `waitForMesherBackpressureTest` is the only polling helper:

```go
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
			Pos: core.SectionPos{Y: y},
		})
	}
	survivor := core.SectionKey{
		Dimension: core.Overworld,
		Pos: core.SectionPos{X: 1},
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

func waitForMesherBackpressureTest(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("5 秒内条件未满足")
		}
		runtime.Gosched()
	}
}
```

These tests catch duplicate-ready insertion, ignoring free slots, missing worker requeue, and missing ForgetChunk removal.

- [ ] **Step 10: Run the integration RED**

```bash
go test ./internal/client -run '^(TestMesherScheduleUsesOnlyAvailableReadyKeys|TestMesherForgetChunkRemovesReadySections|TestMesherRedirtyInFlightQueuesLatestGeneration)$' -count=1
```

Expected RED: build failure because `Mesher` lacks `ready`. Fix only test syntax/import issues until that is the sole failure.

- [ ] **Step 11: Integrate ready state and bounded scheduling**

Add `ready readySectionHeap` beside `dirty` in `Mesher`, initialize it with `newReadySectionHeap()` in `NewMesher`, and call `mesher.ready.Remove(key)` from `ForgetChunk`.

Add:

```go
func (mesher *Mesher) enqueueReadyLocked(key core.SectionKey) {
	if mesher.isClosed {
		return
	}
	if _, dirty := mesher.dirty[key]; !dirty {
		return
	}
	if _, queued := mesher.queued[key]; queued {
		return
	}
	if _, inFlight := mesher.inFlight[key]; inFlight {
		return
	}
	mesher.ready.Add(key)
}
```

Call it after `dirty[key]=nextGeneration` in `markDirtyLocked`, and after deleting a matching entry in `removeQueued`.

Replace candidate allocation/sort with a `for range maxJobs` loop that:

```go
mesher.mu.Lock()
key, ok := mesher.ready.Take()
if !ok {
	mesher.mu.Unlock()
	return
}
generation, dirty := mesher.dirty[key]
_, queued := mesher.queued[key]
_, inFlight := mesher.inFlight[key]
mesher.mu.Unlock()
```

Return if no ready key. Skip invalid state. Clone outside the lock. If `cloneNeighborhood` returns false, delete `dirty[key]` only when its generation still matches; otherwise call `enqueueReadyLocked`. Recheck generation/state under lock before assigning `queued[key]=generation`. On any recheck failure call `enqueueReadyLocked` before unlocking. Keep the existing nonblocking send; `removeQueued` now restores ready membership on a failed send.

The HIGH-risk `handle` defer must become:

```go
	defer func() {
		recovered := recover()
		if recovered != nil {
			slog.Error("区段网格化失败", "section", job.key, "panic", recovered)
		}
		if !claimed {
			return
		}
		mesher.mu.Lock()
		if mesher.inFlight[job.key] == job.generation {
			delete(mesher.inFlight, job.key)
		}
		current, dirty := mesher.dirty[job.key]
		if recovered != nil || (dirty && current != job.generation) {
			mesher.enqueueReadyLocked(job.key)
		}
		mesher.mu.Unlock()
	}()
```

Do not requeue a successful unchanged generation; `Drain` owns accepting it and deleting dirty.

Add this benchmark after the integration is GREEN. It keeps 90k dirty/ready keys and exactly one free slot on every measured iteration:

```go
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
```

- [ ] **Step 12: Run focused GREEN including inherited lifecycle tests**

```bash
gofmt -w internal/client/mesher.go internal/client/mesher_ready_queue.go internal/client/mesher_ready_queue_test.go internal/client/mesher_backpressure_test.go
go test ./internal/client -run '^(TestReadySectionHeap|TestMesherScheduleFullJobQueueDoesNotScanDirtyBacklog|TestMesherScheduleUsesOnlyAvailableReadyKeys|TestMesherForgetChunkRemovesReadySections|TestMesherRedirtyInFlightQueuesLatestGeneration|TestMesherBuildsInitialChunkAndBoundaryRemeshes|TestMesherDiscardsStaleNeighborRevisionAndRedirties|TestMesherSurvivesPanickingJob|TestMesherCloseReturnsWithFullResultQueue)$' -count=1
```

Expected: all selected tests PASS.

- [ ] **Step 13: Remove the obsolete full-sort path while staying GREEN**

Remove the `sort` import and delete `sortSectionKeySlice`; do not refactor unrelated code. Run:

```bash
go test ./internal/client -run '^(TestReadySectionHeap|TestMesher)' -count=1
git diff --check -- internal/client/mesher.go internal/client/mesher_ready_queue.go internal/client/mesher_ready_queue_test.go internal/client/mesher_backpressure_test.go
```

Expected: PASS and no whitespace errors.

- [ ] **Step 14: Mutation check A — bypass capacity enforcement**

Temporarily make `workLimit` ignore `freeSlots`, including the full-channel case. Run:

```bash
go test ./internal/client -run '^TestMesherScheduleFullJobQueueDoesNotScanDirtyBacklog$' -count=1
```

Expected RED: dirty/ready state changes or allocations become nonzero. Restore the inverse patch and rerun to GREEN.

- [ ] **Step 15: Mutation check B — bypass the ready heap**

Temporarily choose candidates from a newly allocated/sorted copy of `dirty` without popping `mesher.ready`. Run:

```bash
go test ./internal/client -run '^TestMesherScheduleUsesOnlyAvailableReadyKeys$' -count=1
go test ./internal/client -run '^$' -bench '^BenchmarkMesherScheduleOneFreeSlot90K$' -benchmem -count=1
```

Expected RED: ready length remains 3 instead of falling to 2. Restore the inverse patch, verify the pre-mutation SHA-256 of `mesher.go`, and rerun to GREEN.

- [ ] **Step 16: Run complete verification and benchmarks**

```bash
go test ./internal/client -count=1
go test -race ./internal/client -count=1
go test ./... -count=1
gofmt -d internal/client/mesher.go internal/client/mesher_ready_queue.go internal/client/mesher_ready_queue_test.go internal/client/mesher_backpressure_test.go
git diff --check
go test ./internal/client -run '^$' -bench '^(BenchmarkMesherScheduleFullJobQueue90K|BenchmarkMesherScheduleOneFreeSlot90K|BenchmarkRemeshBoundaryEdit)$' -benchmem -count=3
```

Expected: all commands exit 0; `gofmt -d` and diff-check print nothing; the 90k/full-channel benchmark has no all-dirty allocation. Record exact before/after values in the SDD report and one concise checkpoint line in `progress.md`.

- [ ] **Step 17: Obtain a fresh two-stage scheduler review**

Give a fresh reviewer the approved spec, this plan, scoped diff, RED/GREEN evidence, mutations, HIGH `handle` warning, and benchmarks. Require:

```text
Spec compliance: Approved / Changes Requested
Code quality: Approved / Changes Requested
Critical / Important / Minor findings
Ready for diagnostic Memory: Yes / No
```

Use `superpowers:receiving-code-review` for findings. Every production fix begins with another focused RED. Re-review until no Critical or Important finding remains.

- [ ] **Step 18: Detect staged scope and commit only scheduler files**

```bash
git add internal/client/mesher.go \
  internal/client/mesher_ready_queue.go \
  internal/client/mesher_ready_queue_test.go \
  internal/client/mesher_backpressure_test.go
git diff --cached --check
git diff --cached --stat
```

Run `gitnexus_detect_changes(scope="staged")` and inspect all symbols/processes. Unstage any path outside these four without discarding work. Commit:

```bash
git commit -m "perf: 限制 mesher 背压调度开销"
```

Expected: one scheduler-only commit; all other Task 17 changes remain unstaged.

---

### Task 2: Run one non-authoritative diagnostic Memory checkpoint

**Files:**
- Read only: `docs/notes/perf-baseline.json`
- Read only: `docs/notes/perf-baseline.md`
- Create outside repository: `/tmp/mcgo-m3c-mesher-bounded-diagnostic.json`
- Update evidence: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/task-17-report.md`
- Update evidence: `.superpowers/sdd/2026-08-01-m3c-multiplayer-sync/progress.md`

**Interfaces:**
- Consumes: reviewed scheduler commit and existing scenario-v6 benchmark.
- Produces: one diagnostic-only Memory artifact if all absolute gates pass; never produces an accepted baseline/current/TCP report.

- [ ] **Step 1: Perform no-retry preflight**

```bash
test ! -e /tmp/mcgo-m3c-mesher-bounded-diagnostic.json
shasum -a 256 docs/notes/perf-baseline.json docs/notes/perf-baseline.md
pgrep -x mcgo || true
```

Expected hashes:

```text
428e9b61bd8a8bf782fdc4e8d54f488d544bee4b7da948873638fe40ba60a191  docs/notes/perf-baseline.json
ac4dfffd78fc3a31d56b3cf728651e805535b60d302d2c2f55273d23f79ecfdb  docs/notes/perf-baseline.md
```

Expected: path absent and no `mcgo` PID. Stop instead of overwriting an existing path.

- [ ] **Step 2: Run exactly one diagnostic Memory benchmark**

```bash
go run ./cmd/mcgo --benchmark --perf-output /tmp/mcgo-m3c-mesher-bounded-diagnostic.json
```

Do not retry. If any absolute gate fails, preserve exact output, check whether the report path is absent, and stop for a new root-cause decision.

- [ ] **Step 3: Validate but do not accept a passing artifact**

Only after exit 0:

```bash
jq -e '
  .scenario_version == 6 and
  .phases.flying.p99_ms < 12 and
  .phases.still.p99_ms < 12 and
  .multiplayer.remote_state_encode.samples > 0 and
  .multiplayer.remote_state_decode.samples > 0 and
  .multiplayer.interest_diff.samples > 0 and
  .multiplayer.roster_apply.samples > 0 and
  .multiplayer.interpolation.samples > 0 and
  .multiplayer.avatar_submit.samples > 0 and
  .multiplayer.name_tag_submit.samples > 0
' /tmp/mcgo-m3c-mesher-bounded-diagnostic.json
shasum -a 256 /tmp/mcgo-m3c-mesher-bounded-diagnostic.json
```

Expected: `true`, exit 0, and a recorded diagnostic SHA. Do not copy it to `docs/notes`, policy Memory, TCP/current, or baseline paths. Do not run perfcheck or TCP.

- [ ] **Step 4: Record checkpoint and stop before formal Task 2**

Append exact still/flying FPS, p50/p95/p99/max, RSS, diagnostic SHA, scheduler benchmark comparison, unchanged baseline hashes, and no-process check to the SDD report/progress. Run:

```bash
git diff --check
pgrep -x mcgo || true
```

Expected: clean diff check and no process. Stop for controller review; formal Memory→migration→TCP→same-scenario acceptance remains separately authorized.

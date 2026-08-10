package server

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

func TestSaveFailureRetriesWithBoundedBackoffAndKeepsDirty(t *testing.T) {
	wantErr := errors.New("transient write")
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call < 2 {
			return storage.SaveResult{}, wantErr
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	if len(first) != 1 || first[0].Revision != 1 {
		t.Fatalf("first save=%+v, want revision 1", first)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)

	running.StepForTest()
	second := receiveSaveCall(t, store)
	if len(second) != 1 || second[0].Revision != 1 {
		t.Fatalf("retry save=%+v, want retained revision 1", second)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("first failure released retained ownership: %+v", got)
	}

	running.StepForTest()
	assertNoSaveStarted(t, store)
	if got := persistenceTestStoreCalls(store); got != 2 {
		t.Fatalf("retry ran before two-tick delay: calls=%d", got)
	}
	running.StepForTest()
	third := receiveSaveCall(t, store)
	if len(third) != 1 || third[0].Revision != 1 {
		t.Fatalf("second retry=%+v, want retained revision 1", third)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()

	status := running.PersistenceStatus()
	if got := persistenceTestStoreCalls(store); got != 3 {
		t.Fatalf("save calls=%d, want 3", got)
	}
	if status.DirtyChunks != 0 || status.InFlightChunks != 0 ||
		status.LastSuccess.IsZero() || status.LastError == "" ||
		status.LastErrorAt.IsZero() || !status.AutosaveDrained {
		t.Fatalf("status after retry success=%+v", status)
	}
}

func TestSaveFailureIntegrationBackoffCapsAtFourTicks(t *testing.T) {
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call < 4 {
			return storage.SaveResult{}, errors.New("keep retrying")
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	initial := running.StepForTest()
	call := receiveSaveCall(t, store)
	if len(call) != 1 || call[0].Revision != 1 {
		t.Fatalf("initial save=%+v, want revision 1", call)
	}
	lastDispatchTick := initial.Tick
	for _, wantDelay := range []uint64{1, 2, 4, 4} {
		waitSaveReturned(t, store)
		waitCompletionQueued(t, running)
		var dispatched sim.TickResult
		for elapsed := uint64(1); elapsed <= wantDelay; elapsed++ {
			dispatched = running.StepForTest()
			if elapsed < wantDelay {
				assertNoSaveStarted(t, store)
			}
		}
		call = receiveSaveCall(t, store)
		if len(call) != 1 || call[0].Revision != 1 {
			t.Fatalf("retry after %d ticks=%+v, want revision 1", wantDelay, call)
		}
		if got := dispatched.Tick - lastDispatchTick; got != wantDelay {
			t.Fatalf("dispatch delay=%d ticks, want %d", got, wantDelay)
		}
		lastDispatchTick = dispatched.Tick
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	if got := persistenceTestStoreCalls(store); got != 5 {
		t.Fatalf("save calls=%d, want initial plus four retries", got)
	}
}

func TestSaveFailurePartialCommitRetriesOnlyUncommitted(t *testing.T) {
	keys := []core.ChunkKey{chunkKey(0, 0), chunkKey(1, 0)}
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{Committed: map[core.ChunkKey]uint64{
				saves[0].Key: saves[0].Revision,
			}}, errors.New("partial region write")
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, keys)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	if got := saveKeys(first); !reflect.DeepEqual(got, keys) {
		t.Fatalf("first save keys=%+v, want %+v", got, keys)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	retry := receiveSaveCall(t, store)
	if got, want := saveKeys(retry), keys[1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("partial retry keys=%+v, want %+v", got, want)
	}
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("partial retry ownership=%+v, want one retained in-flight chunk", got)
	}
}

func TestSaveNilErrorOmissionRetainsSubmittedSnapshot(t *testing.T) {
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{}, nil
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 1
	running.config.RetryMaxTicks = 4
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	retry := receiveSaveCall(t, store)
	if len(first) != 1 || len(retry) != 1 || retry[0].Key != first[0].Key ||
		retry[0].Revision != first[0].Revision {
		t.Fatalf("omitted snapshot first=%+v retry=%+v", first, retry)
	}
	if status := running.PersistenceStatus(); status.InFlightChunks != 1 ||
		!strings.Contains(status.LastError, "omitted submitted chunks") {
		t.Fatalf("omission status=%+v", status)
	}
}

func TestRetryDelayDoublesCapsAndCannotOverflow(t *testing.T) {
	tests := []struct {
		name     string
		base     uint64
		maximum  uint64
		attempts uint32
		want     uint64
	}{
		{name: "attempt one", base: 1, maximum: 4, attempts: 1, want: 1},
		{name: "attempt two", base: 1, maximum: 4, attempts: 2, want: 2},
		{name: "capped attempt three", base: 3, maximum: 4, attempts: 3, want: 4},
		{name: "overflow safe", base: ^uint64(0)/2 + 1, maximum: ^uint64(0), attempts: 2, want: ^uint64(0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := retryDelay(test.base, test.maximum, test.attempts); got != test.want {
				t.Fatalf("retryDelay(%d,%d,%d)=%d, want %d", test.base, test.maximum, test.attempts, got, test.want)
			}
		})
	}
}

func TestDueRetryQueueFullKeepsAttemptAndSnapshot(t *testing.T) {
	key := chunkKey(2, 3)
	region, _ := storage.RegionFor(key)
	retained := retrySave{
		Job: saveJob{Region: region, Snapshots: []sim.ChunkSaveSnapshot{{
			Key: key, Revision: 7, Chunk: world.NewChunk(key.Pos),
		}}, Retry: true, RetryID: 1},
		Attempts: 2,
		NextTick: 5,
	}
	running := &Server{
		saveJobs:      make(chan saveJob, 1),
		retry:         map[storage.RegionKey][]retrySave{region: []retrySave{retained}},
		retryInFlight: make(map[uint64]retrySave),
		nextRetryID:   1,
	}
	running.saveJobs <- saveJob{Region: storage.RegionKey{X: -99}}

	running.dispatchDueRetries(5)
	got := running.retry[region]
	if len(got) != 1 || got[0].Attempts != 2 || got[0].NextTick != 5 || len(got[0].Job.Snapshots) != 1 ||
		len(running.retryInFlight) != 0 || len(running.saveJobs) != 1 {
		t.Fatalf("queue-full retry changed or disappeared: retry=%+v inFlight=%+v queued=%d", got, running.retryInFlight, len(running.saveJobs))
	}
}

func TestDueRetryIsQueuedBeforeFreshAutosaveSnapshot(t *testing.T) {
	oldKey, freshKey := chunkKey(0, 0), chunkKey(64, 0)
	engine := dirtyReadyEngine(t, []core.ChunkKey{oldKey, freshKey})
	oldSnapshot := engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(oldSnapshot) != 1 || oldSnapshot[0].Key != oldKey {
		t.Fatalf("old snapshot=%+v, want first region key", oldSnapshot)
	}
	region, _ := storage.RegionFor(oldKey)
	config := DefaultConfig(42)
	config.AutosaveTicks = 1
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 1),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
		autosaveActive:  true,
		saveCompletions: make(chan saveCompletion, 1),
	}
	running.retry[region] = []retrySave{{
		Job:       saveJob{Region: region, Snapshots: oldSnapshot, Retry: true, RetryID: 1},
		Attempts:  1,
		NextTick:  engine.TickCount(),
		LastError: errors.New("old failed save"),
	}}
	running.nextRetryID = 1

	running.schedulePersistence(engine.TickCount())
	queued := <-running.saveJobs
	if !queued.Retry || queued.Attempt != 2 || len(queued.Snapshots) != 1 ||
		queued.Snapshots[0].Key != oldKey {
		t.Fatalf("first queued save=%+v, want due retry", queued)
	}
	if got := engine.PersistenceStats(); got.InFlightChunks != 1 || got.DirtyChunks != 2 {
		t.Fatalf("fresh queue-full selection was not released: %+v", got)
	}
}

func TestMutationDuringRetrySelectsNewRevisionOnceAfterOldCommit(t *testing.T) {
	key := chunkKey(0, 0)
	store := newPersistenceTestStore()
	store.respond = func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{}, errors.New("retry old revision")
		}
		return committedResult(saves), nil
	}
	running := newPersistenceServer(t, store)
	running.config.RetryBaseTicks = 2
	running.config.RetryMaxTicks = 4
	running.engine = dirtyPlayerEngine(t, key)
	running.config.AutosaveTicks = running.engine.TickCount() + 1

	running.StepForTest()
	first := receiveSaveCall(t, store)
	if len(first) != 1 || first[0].Revision != 1 {
		t.Fatalf("initial save=%+v, want revision 1", first)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.engine.Enqueue(sim.Command{
		Session: testSessionID, Sequence: 1, Kind: sim.CommandPlayerInput,
		Pitch: -1.5, Mining: true,
	})
	for range 4 {
		if primed := running.engine.Step(); len(primed.Changes) != 0 {
			t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
		}
	}
	changed := running.StepForTest()
	if len(changed.Changes) != 1 || changed.Changes[0].NewRevision != 2 {
		t.Fatalf("mutation=%+v, want revision 2", changed.Changes)
	}
	assertNoSaveStarted(t, store)
	if got := running.engine.PersistenceStats(); got.DirtyChunks != 1 || got.InFlightChunks != 1 {
		t.Fatalf("pending retry allowed duplicate selection: %+v", got)
	}

	running.StepForTest()
	retry := receiveSaveCall(t, store)
	if len(retry) != 1 || retry[0].Revision != 1 {
		t.Fatalf("retry=%+v, want old revision 1", retry)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	newer := receiveSaveCall(t, store)
	if len(newer) != 1 || newer[0].Revision != 2 {
		t.Fatalf("post-retry save=%+v, want new revision 2", newer)
	}
	waitSaveReturned(t, store)
	waitCompletionQueued(t, running)
	running.StepForTest()
	assertNoSaveStarted(t, store)
	if got := persistenceTestStoreCalls(store); got != 3 {
		t.Fatalf("new revision selected more than once: calls=%d", got)
	}
}

func TestSameRegionRetryCoalescingSortsAndKeepsOneClonePerKey(t *testing.T) {
	key0, key1, key2 := chunkKey(0, 0), chunkKey(1, 0), chunkKey(2, 0)
	firstKey2 := world.NewChunk(key2.Pos)
	replacementKey1 := world.NewChunk(key1.Pos)
	merged := mergeRetrySnapshots(
		[]sim.ChunkSaveSnapshot{
			{Key: key2, Revision: 7, Chunk: firstKey2},
			{Key: key1, Revision: 7, Chunk: world.NewChunk(key1.Pos)},
		},
		[]sim.ChunkSaveSnapshot{
			{Key: key0, Revision: 7, Chunk: world.NewChunk(key0.Pos)},
			{Key: key2, Revision: 7, Chunk: world.NewChunk(key2.Pos)},
			{Key: key1, Revision: 8, Chunk: replacementKey1},
		},
	)
	if got, want := snapshotKeys(merged), []core.ChunkKey{key0, key1, key2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("coalesced keys=%+v, want %+v", got, want)
	}
	if merged[1].Revision != 8 || merged[1].Chunk != replacementKey1 ||
		merged[2].Revision != 7 || merged[2].Chunk != firstKey2 {
		t.Fatalf("coalesced clones/revisions changed unexpectedly: %+v", merged)
	}
}

func TestSameRegionFreshFailureDoesNotInheritInflightRetryAttempts(t *testing.T) {
	keyA, keyB := chunkKey(0, 0), chunkKey(1, 0)
	engine := dirtyReadyEngine(t, []core.ChunkKey{keyA, keyB})
	snapshots := engine.PersistenceSnapshots(2, 1<<20, sim.SaveAll)
	if got := snapshotKeys(snapshots); !reflect.DeepEqual(got, []core.ChunkKey{keyA, keyB}) {
		t.Fatalf("snapshots=%+v, want A then B", got)
	}
	region, _ := storage.RegionFor(keyA)
	config := DefaultConfig(42)
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 256
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 4),
		saveCompletions: make(chan saveCompletion, 4),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
	}

	running.retainFailedSave(saveJob{
		Region: region, Snapshots: snapshots[:1], Attempt: 9, Retry: true,
	}, snapshots[:1], errors.New("A attempt 9 failed"))
	running.dispatchDueRetries(saturatingAddUint64(engine.TickCount(), 256))
	attemptA := <-running.saveJobs
	if attemptA.Attempt != 10 || len(attemptA.Snapshots) != 1 || attemptA.Snapshots[0].Key != keyA {
		t.Fatalf("A retry=%+v, want attempt 10", attemptA)
	}

	running.saveCompletions <- saveCompletion{
		Job: saveJob{Region: region, Snapshots: snapshots[1:], Attempt: 1},
		Err: errors.New("fresh B failed"),
	}
	running.saveCompletions <- saveCompletion{
		Job:    attemptA,
		Result: storage.SaveResult{Committed: map[core.ChunkKey]uint64{keyA: snapshots[0].Revision}},
	}
	running.drainSaveCompletions()
	running.dispatchDueRetries(engine.TickCount() + 1)

	retryB := <-running.saveJobs
	if retryB.Attempt != 2 || len(retryB.Snapshots) != 1 || retryB.Snapshots[0].Key != keyB {
		t.Fatalf("B retry=%+v, want independent attempt 2", retryB)
	}
}

func TestSameRegionFreshFailureDoesNotAdvanceOlderRetryDeadline(t *testing.T) {
	keyA, keyB := chunkKey(0, 0), chunkKey(1, 0)
	engine := dirtyReadyEngine(t, []core.ChunkKey{keyA, keyB})
	snapshots := engine.PersistenceSnapshots(2, 1<<20, sim.SaveAll)
	region, _ := storage.RegionFor(keyA)
	config := DefaultConfig(42)
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 256
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 4),
		saveCompletions: make(chan saveCompletion, 4),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
	}

	running.retainFailedSave(saveJob{
		Region: region, Snapshots: snapshots[:1], Attempt: 9, Retry: true,
	}, snapshots[:1], errors.New("A remains far in the future"))
	running.saveCompletions <- saveCompletion{
		Job: saveJob{Region: region, Snapshots: snapshots[1:], Attempt: 1},
		Err: errors.New("fresh B failed"),
	}
	running.drainSaveCompletions()
	running.dispatchDueRetries(engine.TickCount() + 1)

	retryB := <-running.saveJobs
	if retryB.Attempt != 2 || len(retryB.Snapshots) != 1 || retryB.Snapshots[0].Key != keyB {
		t.Fatalf("early retry=%+v, want only fresh B at attempt 2", retryB)
	}
	select {
	case extra := <-running.saveJobs:
		t.Fatalf("older A deadline was advanced: %+v", extra)
	default:
	}
	running.dispatchDueRetries(saturatingAddUint64(engine.TickCount(), 256))
	retryA := <-running.saveJobs
	if retryA.Attempt != 10 || len(retryA.Snapshots) != 1 || retryA.Snapshots[0].Key != keyA {
		t.Fatalf("late retry=%+v, want only A at attempt 10", retryA)
	}
}

func TestOldestDueRetryPreventsFixedRegionStarvation(t *testing.T) {
	config := DefaultConfig(42)
	config.RetryBaseTicks = 1
	config.RetryMaxTicks = 1
	engine := sim.NewEngine(0, 0)
	running := &Server{
		config:          config,
		engine:          engine,
		saveJobs:        make(chan saveJob, 3),
		saveCompletions: make(chan saveCompletion, 3),
		retry:           make(map[storage.RegionKey][]retrySave),
		retryInFlight:   make(map[uint64]retrySave),
	}
	keys := []core.ChunkKey{
		chunkKey(0, 0), chunkKey(32, 0), chunkKey(64, 0), chunkKey(96, 0),
	}
	for _, key := range keys {
		region, _ := storage.RegionFor(key)
		snapshot := sim.ChunkSaveSnapshot{Key: key, Revision: 1, Chunk: world.NewChunk(key.Pos)}
		running.retainFailedSave(
			saveJob{Region: region, Snapshots: []sim.ChunkSaveSnapshot{snapshot}, Attempt: 1},
			[]sim.ChunkSaveSnapshot{snapshot},
			errors.New("initial failure"),
		)
	}

	running.dispatchDueRetries(1)
	firstRound := make([]saveJob, 3)
	for index := range firstRound {
		firstRound[index] = <-running.saveJobs
	}
	if got, want := saveJobKeys(firstRound), keys[:3]; !reflect.DeepEqual(got, want) {
		t.Fatalf("first round=%+v, want %+v", got, want)
	}
	engine.Step()
	for _, job := range firstRound {
		running.saveCompletions <- saveCompletion{Job: job, Err: errors.New("retry failed")}
	}
	running.drainSaveCompletions()
	running.dispatchDueRetries(2)
	secondRound := make([]saveJob, 3)
	for index := range secondRound {
		secondRound[index] = <-running.saveJobs
	}
	if got := saveJobKeys(secondRound); !containsChunkKey(got, keys[3]) {
		t.Fatalf("oldest due region D starved in second round: %+v", got)
	}
}

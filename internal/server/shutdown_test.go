package server

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

func TestShutdownFailureFreezesAndCanRetry(t *testing.T) {
	wantErr := errors.New("disk full")
	store := newShutdownTestStore()
	store.setSaveError(wantErr)
	running, _ := newShutdownTestServer(t, store)
	key := chunkKey(0, 0)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{key})
	tickBefore := running.engine.TickCount()

	err := shutdownWithDeadline(running, waitDeadline)
	if !errors.Is(err, wantErr) {
		t.Fatalf("first Shutdown error=%v, want disk full", err)
	}
	if got := running.engine.TickCount(); got != tickBefore+1 {
		t.Fatalf("freeze ticks=%d, want exactly one final tick after %d", got, tickBefore)
	}
	if result := running.StepForTest(); !reflect.DeepEqual(result, sim.TickResult{}) {
		t.Fatalf("frozen StepForTest=%+v, want zero TickResult", result)
	}
	status := running.PersistenceStatus()
	if status.DirtyChunks != 1 || status.InFlightChunks != 1 || status.AutosaveDrained {
		t.Fatalf("failed shutdown persistence status=%+v", status)
	}
	if !store.worldOwned() {
		t.Fatal("failed shutdown released Store ownership")
	}

	store.setSaveError(nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("retry Shutdown error=%v", err)
	}
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("idempotent Shutdown error=%v", err)
	}
	if got := running.engine.TickCount(); got != tickBefore+1 {
		t.Fatalf("retry advanced authority to tick %d, want %d", got, tickBefore+1)
	}
	if got := store.savedRevisions(); !reflect.DeepEqual(got, [][]uint64{{1}, {1}}) {
		t.Fatalf("saved revisions=%v, want failed and retried revision 1", got)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 1 || closeCalls != 1 {
		t.Fatalf("Store lifecycle Sync=%d Close=%d, want 1,1", syncCalls, closeCalls)
	}
	if store.worldOwned() {
		t.Fatal("successful shutdown retained Store ownership")
	}
}

func TestShutdownAppliesFinalBufferedCommandExactlyOnceWithoutPublishing(t *testing.T) {
	store := newShutdownTestStore()
	running, client := newShutdownTestServer(t, store)
	key := chunkKey(0, 0)
	running.engine = dirtyPlayerEngine(t, key)
	running.engine.Enqueue(sim.Command{
		Session: testSessionID, Sequence: 1, Kind: sim.CommandPlayerInput,
		Pitch: -1.5, Mining: true,
	})
	for range 4 {
		if primed := running.engine.Step(); len(primed.Changes) != 0 {
			t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
		}
	}
	tickBefore := running.engine.TickCount()
	running.incoming <- incomingCommand{
		Session: testSessionID, Generation: 1,
		Command: sim.Command{
			Session: testSessionID, Sequence: 2,
			Kind: sim.CommandPlayerInput, Pitch: -1.5, Mining: true,
		},
	}

	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("Shutdown error=%v", err)
	}
	if got := running.engine.TickCount(); got != tickBefore+1 {
		t.Fatalf("Shutdown tick=%d, want one final tick after %d", got, tickBefore)
	}
	if got := store.savedRevisions(); !reflect.DeepEqual(got, [][]uint64{{2}}) {
		t.Fatalf("saved revisions=%v, want final command revision 2", got)
	}
	if len(running.pending) != 0 || len(running.jobs) != 0 {
		t.Fatalf("final tick scheduled chunk work: pending=%+v jobs=%d", running.pending, len(running.jobs))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if message, err := client.Recv(ctx); err == nil {
		t.Fatalf("final tick published %#v", message)
	}
	if result := running.StepForTest(); !reflect.DeepEqual(result, sim.TickResult{}) {
		t.Fatalf("post-close StepForTest=%+v, want zero TickResult", result)
	}
}

func TestShutdownCallerTimeoutPreservesFrozenAuthorityForRetry(t *testing.T) {
	store := newShutdownTestStore()
	gate := make(chan struct{})
	store.setSaveGate(gate)
	running, _ := newShutdownTestServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := running.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error=%v, want deadline exceeded", err)
	}
	if result := running.StepForTest(); !reflect.DeepEqual(result, sim.TickResult{}) {
		t.Fatalf("timed-out shutdown did not freeze Step: %+v", result)
	}
	if status := running.PersistenceStatus(); status.DirtyChunks != 1 ||
		status.InFlightChunks != 1 || status.AutosaveDrained {
		t.Fatalf("timed-out shutdown lost persistence state: %+v", status)
	}
	if !store.worldOwned() {
		t.Fatal("timed-out shutdown released Store ownership")
	}

	gate <- struct{}{}
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("retry after timeout error=%v", err)
	}
	if got := store.savedRevisions(); !reflect.DeepEqual(got, [][]uint64{{1}}) {
		t.Fatalf("timeout retry duplicated or lost snapshot: %v", got)
	}
}

func TestShutdownContextAppliesReadySaveFailureBeforeReturning(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previousProcs)
	for iteration := range 32 {
		wantErr := errors.New("ready save failure")
		store := newShutdownTestStore()
		store.setSaveError(wantErr)
		running, _ := newShutdownTestServer(t, store)
		running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
		ctx, cancel := context.WithCancel(context.Background())
		var cancelOnce sync.Once
		running.config.SaveObserver = func(time.Duration) {
			cancelOnce.Do(cancel)
		}

		err := running.Shutdown(ctx)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, wantErr) {
			store.setSaveError(nil)
			recoverShutdownAfterExpectedFailure(t, running, wantErr)
			t.Fatalf(
				"iteration %d Shutdown error=%v, want ready save failure joined with canceled caller",
				iteration,
				err,
			)
		}
		store.setSaveError(nil)
		running.config.SaveObserver = nil
		recoverShutdownAfterExpectedFailure(t, running, wantErr)
	}
}

func TestShutdownTimeoutIncludesUnresolvedRetryFailure(t *testing.T) {
	wantErr := errors.New("retryable disk error")
	store := newShutdownTestStore()
	store.setSaveError(wantErr)
	running, _ := newShutdownTestServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, wantErr) {
		t.Fatalf("first Shutdown error=%v, want retryable disk error", err)
	}

	gate := make(chan struct{})
	store.setSaveError(nil)
	store.setSaveGate(gate)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := running.Shutdown(ctx)
	if !errors.Is(err, wantErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed retry error=%v, want unresolved retry joined with deadline", err)
	}

	gate <- struct{}{}
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("recovered retry Shutdown error=%v", err)
	}
}

func TestShutdownTimeoutDoesNotIncludeResolvedHistoricalSaveFailure(t *testing.T) {
	historicalErr := errors.New("historical A failure")
	store := newShutdownTestStore()
	store.setResponder(func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call == 0 {
			return storage.SaveResult{}, historicalErr
		}
		return committedResult(saves), nil
	})
	running, _ := newShutdownTestServer(t, store)
	running.config.SaveChunks = 1
	keys := []core.ChunkKey{chunkKey(0, 0), chunkKey(32, 0)}
	running.engine = dirtyReadyEngine(t, keys)
	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, historicalErr) {
		t.Fatalf("first Shutdown error=%v, want historical A failure", err)
	}

	gateB := make(chan struct{})
	store.setSaveGateAtCall(gateB, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := running.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("B Shutdown error=%v, want deadline exceeded", err)
	}
	if errors.Is(err, historicalErr) {
		t.Fatalf("B Shutdown error=%v retained resolved A failure", err)
	}
	running.stepMu.Lock()
	pendingRetries, retryInFlight := len(running.retry), len(running.retryInFlight)
	running.stepMu.Unlock()
	if pendingRetries != 0 || retryInFlight != 0 {
		t.Fatalf("A retry still unresolved: pending=%d inFlight=%d", pendingRetries, retryInFlight)
	}

	gateB <- struct{}{}
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("B recovery Shutdown error=%v", err)
	}
	if got, want := store.savedKeys(), [][]core.ChunkKey{{keys[0]}, {keys[0]}, {keys[1]}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("save calls=%+v, want %+v", got, want)
	}
}

func TestShutdownJoinsBufferedPersistenceFailureWithCanceledCaller(t *testing.T) {
	wantErr := errors.New("buffered write failure")
	store := newShutdownTestStore()
	running, _ := newShutdownTestServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	snapshots := running.engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	jobs := groupSaveJobs(snapshots)
	jobs[0].Attempt = 1
	running.saveCompletions <- saveCompletion{Job: jobs[0], Err: wantErr}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := running.Shutdown(ctx)
	if !errors.Is(err, wantErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error=%v, want buffered persistence root joined with canceled caller", err)
	}
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("buffered failure retry Shutdown error=%v", err)
	}
}

func TestShutdownPartialSaveAcknowledgesCommittedAndRetriesOnlyRemainder(t *testing.T) {
	wantErr := errors.New("partial write")
	store := newShutdownTestStore()
	store.setResponder(func(call int, saves []storage.ChunkSave) (storage.SaveResult, error) {
		if call != 0 {
			return committedResult(saves), nil
		}
		return storage.SaveResult{Committed: map[core.ChunkKey]uint64{
			saves[0].Key: saves[0].Revision,
		}}, wantErr
	})
	running, _ := newShutdownTestServer(t, store)
	keys := []core.ChunkKey{chunkKey(0, 0), chunkKey(1, 0)}
	running.engine = dirtyReadyEngine(t, keys)

	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, wantErr) {
		t.Fatalf("first Shutdown error=%v, want partial write", err)
	}
	if status := running.PersistenceStatus(); status.DirtyChunks != 1 ||
		status.InFlightChunks != 1 || status.AutosaveDrained {
		t.Fatalf("partial completion status=%+v, want one retained remainder", status)
	}
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("partial retry Shutdown error=%v", err)
	}
	if got, want := store.savedKeys(), [][]core.ChunkKey{keys, keys[1:]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("save calls=%+v, want %+v", got, want)
	}
}

func TestShutdownSavesThenSyncsThenClosesAndRetainsLockOnSyncFailure(t *testing.T) {
	wantErr := errors.New("sync failed")
	store := newShutdownTestStore()
	store.setSyncError(wantErr)
	running, _ := newShutdownTestServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})

	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, wantErr) {
		t.Fatalf("first Shutdown error=%v, want sync failed", err)
	}
	if got := store.eventLog(); !reflect.DeepEqual(got, []string{"save", "sync"}) {
		t.Fatalf("failed Sync event order=%v", got)
	}
	if !store.worldOwned() {
		t.Fatal("Sync failure released Store ownership")
	}

	store.setSyncError(nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("Sync retry Shutdown error=%v", err)
	}
	if got := store.eventLog(); !reflect.DeepEqual(got, []string{"save", "sync", "sync", "close"}) {
		t.Fatalf("successful retry event order=%v", got)
	}
}

func TestShutdownCancellationAfterSyncDefersCloseToRetry(t *testing.T) {
	store := newShutdownTestStore()
	running, _ := newShutdownTestServer(t, store)
	ctx, cancel := context.WithCancel(context.Background())
	store.setSyncHook(cancel)

	err := running.Shutdown(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown error=%v, want canceled after Sync", err)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 1 || closeCalls != 0 {
		t.Fatalf("canceled lifecycle Sync=%d Close=%d, want 1,0", syncCalls, closeCalls)
	}
	if !store.worldOwned() {
		t.Fatal("cancellation after Sync released Store ownership")
	}

	store.setSyncHook(nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("Close-phase retry Shutdown error=%v", err)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 1 || closeCalls != 1 {
		t.Fatalf("retry lifecycle Sync=%d Close=%d, want 1,1", syncCalls, closeCalls)
	}
}

func TestShutdownCloseFailureIsSerializedAndRetryable(t *testing.T) {
	wantErr := errors.New("close failed")
	store := newShutdownTestStore()
	store.setClosePoisonsSync(true)
	store.setCloseError(wantErr)
	running, _ := newShutdownTestServer(t, store)

	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, wantErr) {
		t.Fatalf("first Shutdown error=%v, want close failed", err)
	}
	if !store.worldOwned() {
		t.Fatal("failed Close released fake world ownership")
	}
	store.setCloseError(nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("Close retry Shutdown error=%v", err)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 1 || closeCalls != 2 {
		t.Fatalf("retry lifecycle Sync=%d Close=%d, want 1,2", syncCalls, closeCalls)
	}
	select {
	case <-running.saveDone:
	default:
		t.Fatal("successful Close retry left save workers running")
	}
	if store.worldOwned() {
		t.Fatal("successful Close retry retained Store ownership")
	}
}

func TestServerRunUsesFreshShutdownTimeoutAndReturnsPersistenceError(t *testing.T) {
	wantErr := errors.New("sync disk full")
	store := newShutdownTestStore()
	store.setSyncError(wantErr)
	client, endpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = client.Close() })
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.TrustedObserver = true
	config.ShutdownTimeout = 200 * time.Millisecond
	running := newAttachedWorldForTest(config, endpoint, playerTestGenerator{}, store)
	runCtx, cancelRun := context.WithCancel(context.Background())
	cancelRun()

	err := running.Run(runCtx)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error=%v, want persistence error", err)
	}
	deadline, ok := store.lastSyncDeadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > config.ShutdownTimeout {
		t.Fatalf("Sync deadline=%v,%v, want fresh timeout within %s", deadline, ok, config.ShutdownTimeout)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned canceled context alongside precedence error: %v", err)
	}

	store.setSyncError(nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("cleanup Shutdown error=%v", err)
	}
}

func TestServerRunReturnsNilOnlyAfterExternalShutdownSucceeds(t *testing.T) {
	wantErr := errors.New("external shutdown save failed")
	store := newShutdownTestStore()
	store.setSaveError(wantErr)
	running, _ := newShutdownTestServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	runDone := make(chan error, 1)
	go func() { runDone <- running.Run(context.Background()) }()

	if err := shutdownWithDeadline(running, waitDeadline); !errors.Is(err, wantErr) {
		t.Fatalf("external Shutdown error=%v, want save failure", err)
	}
	select {
	case err := <-runDone:
		t.Fatalf("Run returned %v before external Shutdown succeeded", err)
	case <-time.After(20 * time.Millisecond):
	}

	store.setSaveError(nil)
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("external retry Shutdown error=%v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run after successful external Shutdown error=%v", err)
		}
	case <-time.After(waitDeadline):
		t.Fatal("Run did not return after successful external Shutdown")
	}
}

func TestConcurrentShutdownRunsFinalTickAndStoreLifecycleOnce(t *testing.T) {
	store := newShutdownTestStore()
	running, _ := newShutdownTestServer(t, store)
	current := running.sessions[testSessionID]
	tickBefore := running.engine.TickCount()
	const callers = 8
	results := make(chan error, callers)
	for range callers {
		go func() { results <- shutdownWithDeadline(running, waitDeadline) }()
	}
	deadline := time.After(waitDeadline)
	for range callers {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("concurrent Shutdown error=%v", err)
			}
		case <-deadline:
			t.Fatal("concurrent Shutdown calls did not finish within shared deadline")
		}
	}
	if got := running.engine.TickCount(); got != tickBefore+1 {
		t.Fatalf("concurrent Shutdown tick=%d, want %d", got, tickBefore+1)
	}
	if syncCalls, closeCalls := store.lifecycleCalls(); syncCalls != 1 || closeCalls != 1 {
		t.Fatalf("concurrent lifecycle Sync=%d Close=%d, want 1,1", syncCalls, closeCalls)
	}

	select {
	case <-running.runtimeDone:
	case <-deadline:
		t.Fatal("runtime/session goroutines did not exit within shared deadline")
	}
	select {
	case <-running.saveDone:
	case <-deadline:
		t.Fatal("save goroutines did not exit within shared deadline")
	}
	if !current.closed() || running.sessions[testSessionID] != nil {
		t.Fatal("successful Shutdown left session open")
	}
}

func TestConcurrentShutdownCallerCanTimeoutWhileWaitingForSerializer(t *testing.T) {
	store := newShutdownTestStore()
	gate := make(chan struct{})
	store.setSaveGate(gate)
	running, _ := newShutdownTestServer(t, store)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	firstDone := make(chan error, 1)
	go func() { firstDone <- running.Shutdown(context.Background()) }()
	select {
	case <-store.saveStarted:
	case <-time.After(waitDeadline):
		t.Fatal("first Shutdown did not reach gated save")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	secondDone := make(chan error, 1)
	go func() { secondDone <- running.Shutdown(ctx) }()
	timedOutWhileWaiting := false
	select {
	case err := <-secondDone:
		timedOutWhileWaiting = errors.Is(err, context.DeadlineExceeded)
	case <-time.After(shortWaitDeadline):
	}

	gate <- struct{}{}
	if err := <-firstDone; err != nil {
		t.Fatalf("first Shutdown error=%v", err)
	}
	if !timedOutWhileWaiting {
		select {
		case <-secondDone:
		default:
		}
		t.Fatal("second Shutdown did not honor its context while waiting for serialization")
	}
}

func TestShutdownWaitsForQueueCapacityWithoutReleasingSnapshots(t *testing.T) {
	store := newShutdownTestStore()
	gate := make(chan struct{})
	store.setSaveGate(gate)
	running, _ := newShutdownTestServer(t, store)
	keys := []core.ChunkKey{
		chunkKey(0, 0),
		chunkKey(32, 0),
		chunkKey(64, 0),
		chunkKey(96, 0),
	}
	running.engine = dirtyReadyEngine(t, keys)
	done := make(chan error, 1)
	go func() { done <- shutdownWithDeadline(running, waitDeadline) }()

	deadline := time.After(waitDeadline)
	for range keys {
		select {
		case <-store.saveStarted:
		case <-deadline:
			t.Fatal("queue-saturated shutdown did not start every save within shared deadline")
		}
		gate <- struct{}{}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("queue-saturated Shutdown error=%v", err)
		}
	case <-deadline:
		t.Fatal("queue-saturated Shutdown did not finish within shared deadline")
	}
	got := store.savedKeys()
	if len(got) != len(keys) {
		t.Fatalf("save calls=%+v, want one per region", got)
	}
	flattened := make([]core.ChunkKey, 0, len(keys))
	for _, call := range got {
		flattened = append(flattened, call...)
	}
	if !reflect.DeepEqual(flattened, keys) {
		t.Fatalf("queue-saturated saves=%+v, want deterministic %+v", flattened, keys)
	}
}

func shutdownWithDeadline(running *Server, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return running.Shutdown(ctx)
}

func recoverShutdownAfterExpectedFailure(t *testing.T, running *Server, wantErr error) {
	t.Helper()
	for range 3 {
		err := shutdownWithDeadline(running, waitDeadline)
		if err == nil {
			return
		}
		if !errors.Is(err, wantErr) {
			t.Fatalf("recovery Shutdown error=%v", err)
		}
	}
	t.Fatal("recovery Shutdown did not succeed")
}

func shutdownServerForTest(t *testing.T, running *Server) {
	t.Helper()
	if err := shutdownWithDeadline(running, waitDeadline); err != nil {
		t.Fatalf("Shutdown cleanup error=%v", err)
	}
}

func newShutdownTestServer(
	t *testing.T,
	store storage.Store,
) (*Server, network.ClientEndpoint) {
	t.Helper()
	client, endpoint := network.NewMemoryPair(64)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.SaveChunks = 8
	config.SaveBytes = 1 << 20
	return newAttachedWorldForTest(config, endpoint, playerTestGenerator{}, store), client
}

type shutdownTestStore struct {
	metadata storage.Metadata

	mu               sync.Mutex
	saveErr          error
	syncErr          error
	closeErr         error
	closePoisonsSync bool
	closing          bool
	saveGate         <-chan struct{}
	saveGateCall     int
	respond          func(int, []storage.ChunkSave) (storage.SaveResult, error)
	syncHook         func()
	saveCalls        [][]storage.ChunkSave
	syncCalls        int
	closeCalls       int
	owned            bool
	events           []string
	syncBy           time.Time
	hasSyncBy        bool
	saveStarted      chan int
	metadataSaves    []storage.Metadata
	metadataRespond  func(int, storage.Metadata) error
}

func newShutdownTestStore() *shutdownTestStore {
	return &shutdownTestStore{
		metadata: storage.Metadata{
			FormatVersion:  2,
			Seed:           42,
			SpawnDimension: core.Overworld,
		},
		owned:        true,
		saveStarted:  make(chan int, 32),
		saveGateCall: -1,
	}
}

func (store *shutdownTestStore) Metadata() storage.Metadata { return store.metadata }

// SaveMetadata 记录每次 metadata 提交，供世界时间保存测试断言。
func (store *shutdownTestStore) SaveMetadata(_ context.Context, metadata storage.Metadata) error {
	store.mu.Lock()
	call := len(store.metadataSaves)
	store.metadataSaves = append(store.metadataSaves, metadata)
	respond := store.metadataRespond
	store.mu.Unlock()
	if respond != nil {
		return respond(call, metadata)
	}
	return nil
}

func (*shutdownTestStore) LoadChunk(
	context.Context,
	core.ChunkKey,
) (storage.StoredChunk, error) {
	return storage.StoredChunk{}, storage.ErrChunkNotFound
}

func (store *shutdownTestStore) SaveBatch(
	ctx context.Context,
	saves []storage.ChunkSave,
) (storage.SaveResult, error) {
	store.mu.Lock()
	call := len(store.saveCalls)
	store.saveCalls = append(store.saveCalls, append([]storage.ChunkSave(nil), saves...))
	store.events = append(store.events, "save")
	gate, gateCall := store.saveGate, store.saveGateCall
	respond, saveErr := store.respond, store.saveErr
	store.mu.Unlock()
	store.saveStarted <- call
	if gate != nil && (gateCall < 0 || gateCall == call) {
		select {
		case <-gate:
		case <-ctx.Done():
			return storage.SaveResult{}, ctx.Err()
		}
	}
	if respond != nil {
		return respond(call, saves)
	}
	if saveErr != nil {
		return storage.SaveResult{}, saveErr
	}
	return committedResult(saves), nil
}

func (store *shutdownTestStore) Sync(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncCalls++
	store.events = append(store.events, "sync")
	store.syncBy, store.hasSyncBy = ctx.Deadline()
	if store.syncHook != nil {
		store.syncHook()
	}
	if store.closePoisonsSync && store.closing {
		return os.ErrClosed
	}
	return store.syncErr
}

func (store *shutdownTestStore) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.closeCalls++
	store.events = append(store.events, "close")
	if store.closePoisonsSync {
		store.closing = true
	}
	if store.closeErr != nil {
		return store.closeErr
	}
	store.owned = false
	return nil
}

func (store *shutdownTestStore) setSaveError(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saveErr = err
}

func (store *shutdownTestStore) setSyncError(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncErr = err
}

func (store *shutdownTestStore) setSyncHook(hook func()) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.syncHook = hook
}

func (store *shutdownTestStore) setCloseError(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.closeErr = err
}

func (store *shutdownTestStore) setClosePoisonsSync(enabled bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.closePoisonsSync = enabled
}

func (store *shutdownTestStore) setSaveGate(gate <-chan struct{}) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saveGate = gate
	store.saveGateCall = -1
}

func (store *shutdownTestStore) setSaveGateAtCall(gate <-chan struct{}, call int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.saveGate = gate
	store.saveGateCall = call
}

func (store *shutdownTestStore) setResponder(
	respond func(int, []storage.ChunkSave) (storage.SaveResult, error),
) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.respond = respond
}

func (store *shutdownTestStore) savedRevisions() [][]uint64 {
	store.mu.Lock()
	defer store.mu.Unlock()
	revisions := make([][]uint64, len(store.saveCalls))
	for call, saves := range store.saveCalls {
		for _, save := range saves {
			revisions[call] = append(revisions[call], save.Revision)
		}
	}
	return revisions
}

func (store *shutdownTestStore) savedKeys() [][]core.ChunkKey {
	store.mu.Lock()
	defer store.mu.Unlock()
	keys := make([][]core.ChunkKey, len(store.saveCalls))
	for call, saves := range store.saveCalls {
		for _, save := range saves {
			keys[call] = append(keys[call], save.Key)
		}
	}
	return keys
}

func (store *shutdownTestStore) lifecycleCalls() (int, int) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.syncCalls, store.closeCalls
}

func (store *shutdownTestStore) worldOwned() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.owned
}

func (store *shutdownTestStore) eventLog() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]string(nil), store.events...)
}

func (store *shutdownTestStore) lastSyncDeadline() (time.Time, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.syncBy, store.hasSyncBy
}

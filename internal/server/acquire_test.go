package server

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/world"
)

func TestAcquireLoadsBeforeGenerating(t *testing.T) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	store := storage.NewMemory(storage.Metadata{FormatVersion: 2, Seed: 42})
	want := world.NewChunk(key.Pos)
	want.SetBlock(0, 0, 0, core.DirtID)
	if _, err := store.SaveBatch(context.Background(), []storage.ChunkSave{{
		Key: key, Revision: 7, Chunk: want,
	}}); err != nil {
		t.Fatal(err)
	}
	generator := &countingGenerator{}
	running := newAcquireServer(t, store, generator)

	stepUntilServer(t, running, func(result sim.TickResult) bool {
		_, revision, ready := running.ChunkHash(core.Overworld, key.Pos)
		return ready && revision == 7
	})
	if calls := generator.Calls(); calls != 0 {
		t.Fatalf("storage hit generated %d times", calls)
	}
}

func TestAcquireOnlyTypedNotFoundFallsBackToGeneration(t *testing.T) {
	tests := []struct {
		name      string
		loadErr   error
		wantState sim.ChunkState
		wantCalls int
	}{
		{name: "typed miss", loadErr: storage.ErrChunkNotFound, wantState: sim.ChunkReady, wantCalls: 1},
		{name: "permission", loadErr: fs.ErrPermission, wantState: sim.ChunkFailed},
		{name: "corrupt", loadErr: storage.ErrCorrupt, wantState: sim.ChunkFailed},
		{name: "future", loadErr: storage.ErrFutureVersion, wantState: sim.ChunkFailed},
		{name: "canceled", loadErr: context.Canceled, wantState: sim.ChunkFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &loadResultStore{
				metadata: storage.Metadata{FormatVersion: 2, Seed: 42},
				err:      test.loadErr,
			}
			generator := &countingGenerator{}
			running := newAcquireServer(t, store, generator)
			key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}

			stepUntilServer(t, running, func(sim.TickResult) bool {
				info, ok := running.ChunkInfo(key.Dimension, key.Pos)
				return ok && info.State == test.wantState
			})
			if calls := generator.Calls(); calls != test.wantCalls {
				t.Fatalf("generator calls=%d, want %d", calls, test.wantCalls)
			}
			if test.wantState == sim.ChunkFailed {
				info, _ := running.ChunkInfo(key.Dimension, key.Pos)
				if !errors.Is(info.Err, test.loadErr) {
					t.Fatalf("failure=%v, want wrapping %v", info.Err, test.loadErr)
				}
			}
		})
	}
}

func TestEmbeddedConstructorUsesStoreMetadata(t *testing.T) {
	metadata := storage.Metadata{
		FormatVersion:  2,
		Seed:           918273,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{X: 4, Z: -7},
	}
	store := storage.NewMemory(metadata)
	config := DefaultConfig(11)
	config.ViewRadius = 0
	config.Workers = 1
	config.SpawnAnchor = core.ChunkPos{X: 99, Z: 99}
	_, endpoint := network.NewMemoryPair(32)
	running := newEmbeddedAttachedWorldForTest(config, endpoint, store)
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	result := running.StepForTest()
	want := core.ChunkKey{Dimension: core.Overworld, Pos: metadata.SpawnAnchor}
	if len(result.Acquire) != 1 || result.Acquire[0] != want ||
		running.config.Seed != metadata.Seed ||
		running.config.SpawnDimension != metadata.SpawnDimension ||
		running.config.SpawnAnchor != metadata.SpawnAnchor {
		t.Fatalf("config=%+v Acquire=%+v, want metadata=%+v", running.config, result.Acquire, metadata)
	}
}

func TestAcquireCancelsForgottenPendingLoads(t *testing.T) {
	store := &blockingLoadStore{
		metadata: storage.Metadata{FormatVersion: 2, Seed: 42},
		started:  make(chan core.ChunkKey, 1),
	}
	_, endpoint := network.NewMemoryPair(64)
	config := DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.TrustedObserver = true
	running := newAttachedWorldForTest(config, endpoint, &countingGenerator{}, store)
	t.Cleanup(func() { shutdownServerForTest(t, running) })

	if err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{}); err != nil {
		t.Fatal(err)
	}
	initial := running.StepForTest()
	if len(initial.Acquire) != 9 || len(running.pending) == 0 {
		t.Fatalf("initial Acquire=%d pending=%d", len(initial.Acquire), len(running.pending))
	}
	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("load worker did not start")
	}

	center := core.ChunkPos{X: 50, Z: -50}
	if err := running.SetTrustedObserverCenter(core.Overworld, center); err != nil {
		t.Fatal(err)
	}
	moved := running.StepForTest()
	if len(moved.Acquire) != 9 {
		t.Fatalf("moved Acquire=%+v", moved.Acquire)
	}
	for _, job := range running.pending {
		if job.Kind != chunkJobLoad || job.Key.Pos.X < center.X-1 ||
			job.Key.Pos.X > center.X+1 || job.Key.Pos.Z < center.Z-1 ||
			job.Key.Pos.Z > center.Z+1 {
			t.Fatalf("forgotten queued load retained: %+v", job)
		}
	}
}

func newAcquireServer(t *testing.T, store storage.Store, generator Generator) *Server {
	t.Helper()
	_, endpoint := network.NewMemoryPair(64)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.TrustedObserver = true
	running := newAttachedWorldForTest(config, endpoint, generator, store)
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	if err := running.SetTrustedObserverCenter(core.Overworld, core.ChunkPos{}); err != nil {
		t.Fatal(err)
	}
	return running
}

func stepUntilServer(t *testing.T, running *Server, condition func(sim.TickResult) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition(running.StepForTest()) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("server condition timed out")
}

type countingGenerator struct {
	mu    sync.Mutex
	calls int
}

func (generator *countingGenerator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	generator.mu.Lock()
	generator.calls++
	generator.mu.Unlock()
	return world.NewChunk(pos)
}

func (generator *countingGenerator) Calls() int {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	return generator.calls
}

type loadResultStore struct {
	metadata storage.Metadata
	stored   storage.StoredChunk
	err      error
}

func (store *loadResultStore) Metadata() storage.Metadata { return store.metadata }

func (*loadResultStore) SaveMetadata(context.Context, storage.Metadata) error { return nil }

func (store *loadResultStore) LoadChunk(context.Context, core.ChunkKey) (storage.StoredChunk, error) {
	return store.stored, store.err
}

func (*loadResultStore) SaveBatch(
	_ context.Context,
	saves []storage.ChunkSave,
) (storage.SaveResult, error) {
	return committedResult(saves), nil
}

func (*loadResultStore) Sync(context.Context) error { return nil }

func (*loadResultStore) Close() error { return nil }

type blockingLoadStore struct {
	metadata storage.Metadata
	started  chan core.ChunkKey
}

func (store *blockingLoadStore) Metadata() storage.Metadata { return store.metadata }

func (*blockingLoadStore) SaveMetadata(context.Context, storage.Metadata) error { return nil }

func (store *blockingLoadStore) LoadChunk(
	ctx context.Context,
	key core.ChunkKey,
) (storage.StoredChunk, error) {
	select {
	case store.started <- key:
	default:
	}
	<-ctx.Done()
	return storage.StoredChunk{}, ctx.Err()
}

func (*blockingLoadStore) SaveBatch(context.Context, []storage.ChunkSave) (storage.SaveResult, error) {
	return storage.SaveResult{}, errors.New("unexpected save")
}

func (*blockingLoadStore) Sync(context.Context) error { return nil }

func (*blockingLoadStore) Close() error { return nil }

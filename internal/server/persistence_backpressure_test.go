package server

import (
	"reflect"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

func TestPersistenceBackpressureHysteresisBoundary(t *testing.T) {
	if !nextPersistenceBackpressure(false, 100, 100) {
		t.Fatal("estimated bytes at cap did not enter backpressure")
	}
	if !nextPersistenceBackpressure(true, 90, 100) {
		t.Fatal("estimated bytes at 90 percent cleared backpressure")
	}
	if nextPersistenceBackpressure(true, 89, 100) {
		t.Fatal("estimated bytes below 90 percent did not clear backpressure")
	}
	if nextPersistenceBackpressure(true, 90, 101) {
		t.Fatal("integer bytes below an exact 90 percent fraction did not clear backpressure")
	}
	if !nextPersistenceBackpressure(true, 91, 101) {
		t.Fatal("integer bytes above an exact 90 percent fraction cleared backpressure")
	}
}

func TestPersistenceStatusReturnsCopiedCurrentState(t *testing.T) {
	running := newPersistenceServer(t, newPersistenceTestStore())
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{chunkKey(0, 0)})
	running.lastSaveError = "original failure"
	running.lastSaveErrorAt = time.Unix(123, 0)
	running.backpressured = true

	status := running.PersistenceStatus()
	if status.DirtyChunks != 1 || status.EstimatedBytes != emptyChunkEstimateBytes ||
		status.InFlightChunks != 0 || !status.Backpressured ||
		status.LastError != "original failure" ||
		!status.LastErrorAt.Equal(time.Unix(123, 0)) || status.AutosaveDrained {
		t.Fatalf("persistence status=%+v", status)
	}
	status.LastError = "caller mutation"
	status.LastErrorAt = time.Time{}
	got := running.PersistenceStatus()
	if got.LastError != "original failure" || !got.LastErrorAt.Equal(time.Unix(123, 0)) {
		t.Fatalf("caller mutated server status: %+v", got)
	}
}

func TestPersistenceBackpressureQueuesAcquireUntilMemoryRecovers(t *testing.T) {
	store := &blockingLoadStore{
		metadata: storage.Metadata{FormatVersion: 2, Seed: 42},
		started:  make(chan core.ChunkKey, 1),
	}
	_, endpoint := network.NewMemoryPair(64)
	config := DefaultConfig(42)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.TrustedObserver = true
	config.UnsavedBytes = 512
	running := newAttachedWorldForTest(config, endpoint, &countingGenerator{}, store)
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	heldKey := chunkKey(0, 0)
	running.engine = dirtyReadyEngine(t, []core.ChunkKey{heldKey})
	running.engine.RegisterObserverSession(trustedObserverSessionID)
	running.engine.Enqueue(sim.Command{
		Session: trustedObserverSessionID, Sequence: 1,
		Kind:      sim.CommandTrustedObserverCenter,
		Dimension: heldKey.Dimension, Center: heldKey.Pos,
	})
	running.engine.Step()
	running.config.AutosaveTicks = running.engine.TickCount() + 100
	running.trustedObserverSequence = 1
	target := core.ChunkPos{X: 20, Z: -20}
	if err := running.SetTrustedObserverCenter(core.Overworld, target); err != nil {
		t.Fatal(err)
	}

	result := running.StepForTest()
	wantAcquire := core.ChunkKey{Dimension: core.Overworld, Pos: target}
	if !reflect.DeepEqual(result.Acquire, []core.ChunkKey{wantAcquire}) {
		t.Fatalf("Acquire=%+v, want queued %+v", result.Acquire, wantAcquire)
	}
	select {
	case started := <-store.started:
		t.Fatalf("backpressured acquisition dispatched %+v", started)
	default:
	}
	info, exists := running.engine.ChunkInfo(wantAcquire)
	if !exists || info.State != sim.ChunkLoading || len(running.pending) != 1 {
		t.Fatalf("queued unknown chunk state=%+v exists=%v pending=%+v", info, exists, running.pending)
	}
	status := running.PersistenceStatus()
	if !status.Backpressured || status.DirtyChunks != 1 || status.EstimatedBytes != emptyChunkEstimateBytes ||
		status.InFlightChunks != 0 || status.AutosaveDrained {
		t.Fatalf("backpressure status=%+v", status)
	}

	selected := running.engine.PersistenceSnapshots(1, 1<<20, sim.SaveAll)
	if len(selected) != 1 || selected[0].Key != heldKey {
		t.Fatalf("cleanup snapshot=%+v, want held key", selected)
	}
	running.engine.ApplyPersisted([]sim.PersistedChunk{{Key: heldKey, Revision: selected[0].Revision}})
	running.StepForTest()
	select {
	case started := <-store.started:
		if started != wantAcquire {
			t.Fatalf("resumed load=%+v, want %+v", started, wantAcquire)
		}
	case <-time.After(waitDeadline):
		t.Fatal("acquisition did not resume below hysteresis threshold")
	}
	if resumed := running.PersistenceStatus(); resumed.Backpressured || !resumed.AutosaveDrained {
		t.Fatalf("resumed status=%+v", resumed)
	}
}

package server

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/world"
)

func TestServerRejectsStaleSessionGeneration(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client1, server1 := network.NewMemoryPair(32)
	exit1, err := running.AttachSession(registrySessionSpec(7, 1, server1))
	if err != nil {
		t.Fatal(err)
	}
	if !running.DetachSession(7, 1, network.ErrClosed) {
		t.Fatal("detach failed")
	}
	<-exit1
	client2, server2 := network.NewMemoryPair(32)
	defer client1.Close()
	defer client2.Close()
	if _, err := running.AttachSession(registrySessionSpec(8, 2, server2)); err != nil {
		t.Fatal(err)
	}
	running.enqueueIncoming(context.Background(), incomingCommand{
		Session: 7, Generation: 1,
		Command: sim.Command{
			Session: 7, Sequence: 99, Kind: sim.CommandPlayerInput,
		},
	})
	running.StepForTest()
	if _, ok := running.PlayerStateFor(7); ok {
		t.Fatal("stale session recreated")
	}
}

func TestSessionRegistryRejectsInvalidAndDuplicateSpecs(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	_, endpoint := network.NewMemoryPair(8)

	valid := registrySessionSpec(7, 1, endpoint)
	missingID := valid
	missingID.ID = 0
	missingGeneration := valid
	missingGeneration.Generation = 0
	missingEndpoint := valid
	missingEndpoint.Endpoint = nil
	invalid := []SessionSpec{missingID, missingGeneration, missingEndpoint}
	for _, spec := range invalid {
		if _, err := running.AttachSession(spec); !errors.Is(err, ErrInvalidSession) {
			t.Fatalf("AttachSession(%+v) error = %v，想要 %v", spec, err, ErrInvalidSession)
		}
	}

	exit, err := running.AttachSession(valid)
	if err != nil {
		t.Fatal(err)
	}
	duplicateID := registrySessionSpec(8, 2, endpoint)
	duplicateID.ID = 7
	if duplicateID.PlayerID == valid.PlayerID {
		t.Fatal("duplicate SessionID fixture reused PlayerID")
	}
	if _, err := running.AttachSession(duplicateID); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("重复 ID error = %v，想要 %v", err, ErrSessionExists)
	}
	if !running.DetachSession(7, 1, nil) {
		t.Fatal("detach failed")
	}
	<-exit
}

func TestSessionRegistryAcceptsArbitraryPlayerID(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client, endpoint := network.NewMemoryPair(32)
	defer client.Close()

	exit, err := running.AttachSession(registrySessionSpec(91, 4, endpoint))
	if err != nil {
		t.Fatal(err)
	}
	state, ok := running.PlayerStateFor(91)
	if !ok || state.Session != 91 {
		t.Fatalf("PlayerStateFor(91) = (%+v, %v)", state, ok)
	}
	requested := running.engine.Step()
	for _, key := range requested.Acquire {
		running.engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	}
	generated := running.engine.Step()
	for _, key := range generated.Generate {
		running.engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     playerTestGenerator{}.GenerateChunk(key.Pos),
		})
	}
	ready := running.engine.Step()
	running.stepMu.Lock()
	running.publish(ready)
	running.stepMu.Unlock()
	snapshotMessage := recvWorldServerMessage(t, client)
	if _, ok := snapshotMessage.(network.ChunkSnapshot); !ok {
		t.Fatalf("arbitrary ID world message = %#v", snapshotMessage)
	}
	var playerMessage network.PlayerState
	for {
		message := recvServerMessage(t, client)
		var ok bool
		playerMessage, ok = message.(network.PlayerState)
		if ok {
			break
		}
	}
	if !playerMessage.Ready {
		t.Fatalf("arbitrary ID player state = %+v", playerMessage)
	}
	snapshot, ok := running.PlayerSnapshotFor(91)
	if !ok {
		t.Fatal("active arbitrary ID 没有 snapshot")
	}
	if !running.DetachSession(91, 4, nil) {
		t.Fatal("detach failed")
	}
	if got := <-exit; got.ID != 91 || got.Generation != 4 || got.Err != nil ||
		!got.HasSnapshot || !reflect.DeepEqual(got.Snapshot, snapshot) {
		t.Fatalf("SessionExit = %+v", got)
	}
	if _, open := <-exit; open {
		t.Fatal("exit channel 没有关闭")
	}
}

func TestSessionRegistryPublishesOnlyToTargetSession(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client7, endpoint7 := network.NewMemoryPair(32)
	client8, endpoint8 := network.NewMemoryPair(32)
	defer client7.Close()
	defer client8.Close()
	for _, spec := range []SessionSpec{
		registrySessionSpec(7, 1, endpoint7),
		registrySessionSpec(8, 1, endpoint8),
	} {
		if _, err := running.AttachSession(spec); err != nil {
			t.Fatal(err)
		}
	}

	running.publish(sim.TickResult{
		Tick: 42,
		Players: []sim.PlayerUpdate{
			{Session: 8, Dimension: core.Overworld, ViewCenter: core.ChunkPos{X: 8}},
			{Session: 7, Dimension: core.Overworld, ViewCenter: core.ChunkPos{X: 7}},
		},
		Rejected: []sim.Rejection{
			{Session: 8, Sequence: 18, Reason: sim.RejectInvalidInput},
			{Session: 7, Sequence: 17, Reason: sim.RejectNoTarget},
		},
	})

	assertSessionMessages := func(
		client network.ClientEndpoint,
		wantSequence uint64,
		wantX int32,
	) {
		t.Helper()
		first := recvServerMessage(t, client)
		rejected, ok := first.(network.CommandRejected)
		if !ok || rejected.Sequence != wantSequence {
			t.Fatalf("首条消息 = %#v，想要 rejection %d", first, wantSequence)
		}
		second := recvServerMessage(t, client)
		state, ok := second.(network.PlayerState)
		if !ok || state.ServerTick != 42 || state.Position[0] != 0 {
			t.Fatalf("第二条消息 = %#v", second)
		}
		_ = wantX
		assertNoServerMessage(t, client)
	}
	assertSessionMessages(client7, 17, 7)
	assertSessionMessages(client8, 18, 8)
}

func TestSessionRegistryShutdownDetachesInIDOrderAndExitsOnce(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	var closeMu sync.Mutex
	var closeOrder []sim.SessionID
	exits := make(map[sim.SessionID]<-chan SessionExit)
	for _, id := range []sim.SessionID{9, 3, 7} {
		endpoint := &orderedCloseEndpoint{
			id: id,
			record: func(id sim.SessionID) {
				closeMu.Lock()
				closeOrder = append(closeOrder, id)
				closeMu.Unlock()
			},
		}
		exit, err := running.AttachSession(registrySessionSpec(id, 1, endpoint))
		if err != nil {
			t.Fatal(err)
		}
		exits[id] = exit
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	if err := running.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	closeMu.Lock()
	gotOrder := append([]sim.SessionID(nil), closeOrder...)
	closeMu.Unlock()
	if want := []sim.SessionID{3, 7, 9}; !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("endpoint close order = %v，想要 %v", gotOrder, want)
	}
	for id, exit := range exits {
		got, open := <-exit
		if !open || got.ID != id || got.Generation != 1 {
			t.Fatalf("session %d exit = (%+v, %v)", id, got, open)
		}
		if _, open := <-exit; open {
			t.Fatalf("session %d 收到重复 exit", id)
		}
	}
}

func TestSessionRegistryLateFailureCannotDetachNewGeneration(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	_, endpoint1 := network.NewMemoryPair(8)
	exit1, err := running.AttachSession(registrySessionSpec(7, 1, endpoint1))
	if err != nil {
		t.Fatal(err)
	}
	old := running.sessions[7]
	called := make(chan bool, 1)
	old.detach = func(id sim.SessionID, generation uint64, cause error) bool {
		detached := running.DetachSession(id, generation, cause)
		called <- detached
		return detached
	}
	if !running.DetachSession(7, 1, nil) {
		t.Fatal("old detach failed")
	}
	<-exit1

	_, endpoint2 := network.NewMemoryPair(8)
	if _, err := running.AttachSession(registrySessionSpec(7, 2, endpoint2)); err != nil {
		t.Fatal(err)
	}
	old.fail(errors.New("late reader failure"))
	select {
	case detached := <-called:
		if detached {
			t.Fatal("旧 generation 的迟到失败摘除了新 session")
		}
	case <-time.After(waitDeadline):
		t.Fatal("迟到失败路径没有调用 DetachSession")
	}
	if state, ok := running.PlayerStateFor(7); !ok || state.Session != 7 {
		t.Fatalf("new generation state = (%+v, %v)", state, ok)
	}
}

func TestSessionRegistryDetachReleasesReaderBlockedOnFullIncoming(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	running.incoming = make(chan incomingCommand, 1)
	running.incoming <- incomingCommand{
		Session: 99, Generation: 1,
		Command: sim.Command{Session: 99, Sequence: 1, Kind: sim.CommandPlayerInput},
	}
	endpoint := &countingRecvEndpoint{
		calls: make(chan int, 2),
	}
	exit, err := running.AttachSession(registrySessionSpec(7, 1, endpoint))
	if err != nil {
		t.Fatal(err)
	}
	if call := <-endpoint.calls; call != 1 {
		t.Fatalf("first Recv call = %d", call)
	}
	if !running.DetachSession(7, 1, nil) {
		t.Fatal("DetachSession = false")
	}
	<-exit
	select {
	case call := <-endpoint.calls:
		if call != 2 {
			t.Fatalf("second Recv call = %d", call)
		}
	case <-time.After(shortWaitDeadline):
		t.Fatal("detach 后 reader 仍阻塞在满 incoming")
	}
}

func TestSessionRegistrySlowSessionDoesNotCloseHealthySession(t *testing.T) {
	config := registryTestConfig()
	config.OutboxCapacity = 1
	running := NewWorld(config, playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	slowEndpoint := newBlockingServerEndpoint()
	healthyEndpoint := newHeartbeatEndpoint()
	slowExit, err := running.AttachSession(registrySessionSpec(7, 1, slowEndpoint))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := running.AttachSession(registrySessionSpec(8, 1, healthyEndpoint)); err != nil {
		t.Fatal(err)
	}

	publishRejection := func(sequence uint64) {
		running.stepMu.Lock()
		defer running.stepMu.Unlock()
		running.publish(sim.TickResult{Rejected: []sim.Rejection{
			{Session: 7, Sequence: sequence, Reason: sim.RejectNoTarget},
			{Session: 8, Sequence: sequence, Reason: sim.RejectNoTarget},
		}})
	}
	publishRejection(1)
	select {
	case <-slowEndpoint.sendStarted:
	case <-time.After(waitDeadline):
		t.Fatal("slow writer 没有阻塞")
	}
	if message := healthyEndpoint.nextSent(t); message.(network.CommandRejected).Sequence != 1 {
		t.Fatalf("healthy first message = %#v", message)
	}
	publishRejection(2)
	if message := healthyEndpoint.nextSent(t); message.(network.CommandRejected).Sequence != 2 {
		t.Fatalf("healthy second message = %#v", message)
	}
	publishRejection(3)
	if message := healthyEndpoint.nextSent(t); message.(network.CommandRejected).Sequence != 3 {
		t.Fatalf("healthy third message = %#v", message)
	}
	if got := waitSessionExit(t, slowExit); got.ID != 7 || got.Err == nil {
		t.Fatalf("slow exit = %+v", got)
	}
	if _, ok := running.PlayerStateFor(8); !ok {
		t.Fatal("slow session 关闭了健康 session")
	}
}

func TestSessionRegistryUnknownMessageClosesOnlyTarget(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	failingEndpoint := newHeartbeatEndpoint()
	healthyEndpoint := newHeartbeatEndpoint()
	exit, err := running.AttachSession(registrySessionSpec(7, 1, failingEndpoint))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := running.AttachSession(registrySessionSpec(8, 1, healthyEndpoint)); err != nil {
		t.Fatal(err)
	}
	failingEndpoint.recv <- nil
	if got := waitSessionExit(t, exit); !errors.Is(got.Err, errUnknownClientMessage) {
		t.Fatalf("unknown message exit = %+v", got)
	}
	if _, ok := running.PlayerStateFor(8); !ok {
		t.Fatal("unknown message 关闭了健康 session")
	}
}

func TestSessionRegistryInvalidRejectionClosesOnlyTarget(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	_, endpoint7 := network.NewMemoryPair(8)
	_, endpoint8 := network.NewMemoryPair(8)
	exit, err := running.AttachSession(registrySessionSpec(7, 1, endpoint7))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := running.AttachSession(registrySessionSpec(8, 1, endpoint8)); err != nil {
		t.Fatal(err)
	}
	running.stepMu.Lock()
	running.publish(sim.TickResult{Rejected: []sim.Rejection{{
		Session: 7, Sequence: 1, Reason: sim.RejectReason(255),
	}}})
	running.stepMu.Unlock()
	if got := waitSessionExit(t, exit); got.ID != 7 || got.Err == nil {
		t.Fatalf("invalid rejection exit = %+v", got)
	}
	if _, ok := running.PlayerStateFor(8); !ok {
		t.Fatal("invalid rejection 关闭了健康 session")
	}
}

func TestSessionRegistryPublicationOrderIsStableByID(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	var closeMu sync.Mutex
	var closeOrder []sim.SessionID
	for _, id := range []sim.SessionID{9, 3, 7} {
		endpoint := &orderedCloseEndpoint{
			id: id,
			record: func(id sim.SessionID) {
				closeMu.Lock()
				closeOrder = append(closeOrder, id)
				closeMu.Unlock()
			},
		}
		if _, err := running.AttachSession(registrySessionSpec(id, 1, endpoint)); err != nil {
			t.Fatal(err)
		}
	}
	running.stepMu.Lock()
	running.publish(sim.TickResult{Rejected: []sim.Rejection{
		{Session: 9, Reason: sim.RejectReason(255)},
		{Session: 3, Reason: sim.RejectReason(255)},
		{Session: 7, Reason: sim.RejectReason(255)},
	}})
	running.stepMu.Unlock()
	closeMu.Lock()
	got := append([]sim.SessionID(nil), closeOrder...)
	closeMu.Unlock()
	if want := []sim.SessionID{3, 7, 9}; !reflect.DeepEqual(got, want) {
		t.Fatalf("publication close order = %v，想要 %v", got, want)
	}
}

func TestSessionRegistrySnapshotsDeltasAndForgetStayWithTarget(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client7, endpoint7 := network.NewMemoryPair(64)
	client8, endpoint8 := network.NewMemoryPair(64)
	defer client7.Close()
	defer client8.Close()
	restores := map[sim.SessionID]sim.PlayerRestore{
		7: {
			SpawnDimension: core.Overworld,
			SpawnAnchor:    core.ChunkPos{},
		},
		8: {
			SpawnDimension: core.Overworld,
			SpawnAnchor:    core.ChunkPos{X: 10},
		},
	}
	for _, spec := range []SessionSpec{
		registrySessionSpecWithRestore(7, 1, endpoint7, restores[7]),
		registrySessionSpecWithRestore(8, 1, endpoint8, restores[8]),
	} {
		if _, err := running.AttachSession(spec); err != nil {
			t.Fatal(err)
		}
	}

	requested := running.engine.Step()
	if len(requested.Acquire) < 2 {
		t.Fatalf("initial Acquire = %+v", requested.Acquire)
	}
	for _, key := range requested.Acquire {
		running.engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	}
	generated := running.engine.Step()
	for _, key := range generated.Generate {
		running.engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     world.NewChunk(key.Pos),
		})
	}
	ready := running.engine.Step()
	running.publish(ready)

	key7 := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	key8 := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 10}}
	snapshot7 := recvWorldServerMessage(t, client7).(network.ChunkSnapshot)
	snapshot8 := recvWorldServerMessage(t, client8).(network.ChunkSnapshot)
	if snapshot7.Chunk != key7.Pos || snapshot8.Chunk != key8.Pos {
		t.Fatalf("snapshots = (%+v, %+v)", snapshot7.Chunk, snapshot8.Chunk)
	}
	assertNoWorldServerMessage(t, client7)
	assertNoWorldServerMessage(t, client8)

	running.publish(sim.TickResult{Changes: []sim.ChunkChangeBatch{{
		Dimension:    key7.Dimension,
		Chunk:        key7.Pos,
		BaseRevision: snapshot7.Revision,
		NewRevision:  snapshot7.Revision + 1,
		Changes: []sim.BlockChange{{
			Position: core.BlockPos{},
			Block:    core.StoneID,
		}},
	}}})
	if delta := recvWorldServerMessage(t, client7).(network.BlockChanges); delta.Chunk != key7.Pos {
		t.Fatalf("delta = %+v", delta)
	}
	assertNoWorldServerMessage(t, client8)

	running.publish(sim.TickResult{
		Forget: map[sim.SessionID][]core.ChunkKey{7: {key7}},
	})
	if forgotten := recvWorldServerMessage(t, client7).(network.ForgetChunks); !reflect.DeepEqual(
		forgotten.Chunks,
		[]core.ChunkPos{key7.Pos},
	) {
		t.Fatalf("forget = %+v", forgotten)
	}
	assertNoWorldServerMessage(t, client8)
}

func TestSessionRegistryResyncCannotReadOtherSessionReadyChunk(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	client7, endpoint7 := network.NewMemoryPair(32)
	client8, endpoint8 := network.NewMemoryPair(32)
	defer client7.Close()
	defer client8.Close()
	for _, spec := range []SessionSpec{
		registrySessionSpecWithRestore(
			7, 1, endpoint7,
			sim.PlayerRestore{
				SpawnDimension: core.Overworld,
				SpawnAnchor:    core.ChunkPos{},
			},
		),
		registrySessionSpecWithRestore(
			8, 1, endpoint8,
			sim.PlayerRestore{
				SpawnDimension: core.Overworld,
				SpawnAnchor:    core.ChunkPos{X: 10},
			},
		),
	} {
		if _, err := running.AttachSession(spec); err != nil {
			t.Fatal(err)
		}
	}

	requested := running.engine.Step()
	for _, key := range requested.Acquire {
		running.engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	}
	generated := running.engine.Step()
	for _, key := range generated.Generate {
		running.engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     world.NewChunk(key.Pos),
		})
	}
	ready := running.engine.Step()
	key8 := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: 10}}
	if !containsChunkKey(ready.Ready, key8) ||
		running.engine.SessionWantsChunk(7, key8) ||
		!running.engine.SessionWantsChunk(8, key8) {
		t.Fatalf("隔离 fixture 无效: Ready=%+v", ready.Ready)
	}

	running.stepMu.Lock()
	running.publish(sim.TickResult{Resync: []sim.ResyncRequest{{
		Session: 7, Sequence: 1,
		Dimension: key8.Dimension, Chunk: key8.Pos,
	}}})
	running.stepMu.Unlock()
	assertNoWorldServerMessage(t, client7)
	assertNoWorldServerMessage(t, client8)
}

func TestSessionRegistryForgetDoesNotCancelUnionPendingWork(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	for _, id := range []sim.SessionID{7, 8} {
		_, endpoint := network.NewMemoryPair(8)
		if _, err := running.AttachSession(registrySessionSpec(id, 1, endpoint)); err != nil {
			t.Fatal(err)
		}
	}
	result := running.engine.Step()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	if !containsChunkKey(result.Acquire, key) {
		t.Fatalf("Acquire = %+v", result.Acquire)
	}
	running.pending = []chunkJob{{Kind: chunkJobLoad, Key: key}}
	running.queued[key] = struct{}{}

	running.cancelUnwantedPending()
	if len(running.pending) != 1 || running.pending[0].Key != key {
		t.Fatalf("单 session forget 取消了 union pending: %+v", running.pending)
	}
	if _, queued := running.queued[key]; !queued {
		t.Fatal("单 session forget 删除了 union queued 标记")
	}
}

func TestSessionRegistryDetachCancelsPendingWorkWithNoSubscribers(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	_, endpoint := network.NewMemoryPair(8)
	if _, err := running.AttachSession(registrySessionSpec(7, 1, endpoint)); err != nil {
		t.Fatal(err)
	}
	result := running.engine.Step()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	if !containsChunkKey(result.Acquire, key) {
		t.Fatalf("Acquire = %+v", result.Acquire)
	}
	running.pending = []chunkJob{{Kind: chunkJobLoad, Key: key}}
	running.queued[key] = struct{}{}

	if !running.DetachSession(7, 1, nil) {
		t.Fatal("detach failed")
	}
	running.engine.Step()
	running.cancelUnwantedPending()
	if len(running.pending) != 0 {
		t.Fatalf("最后订阅者 detach 后 pending = %+v", running.pending)
	}
	if _, queued := running.queued[key]; queued {
		t.Fatal("最后订阅者 detach 后 queued 标记仍存在")
	}
}

func TestSessionRegistryDetachKeepsPendingWorkForRemainingSubscriber(t *testing.T) {
	running := NewWorld(registryTestConfig(), playerTestGenerator{}, testStore())
	t.Cleanup(func() { shutdownServerForTest(t, running) })
	for _, id := range []sim.SessionID{7, 8} {
		_, endpoint := network.NewMemoryPair(8)
		if _, err := running.AttachSession(registrySessionSpec(id, 1, endpoint)); err != nil {
			t.Fatal(err)
		}
	}
	result := running.engine.Step()
	key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{}}
	if !containsChunkKey(result.Acquire, key) {
		t.Fatalf("Acquire = %+v", result.Acquire)
	}
	running.pending = []chunkJob{{Kind: chunkJobLoad, Key: key}}
	running.queued[key] = struct{}{}

	if !running.DetachSession(7, 1, nil) {
		t.Fatal("detach failed")
	}
	running.engine.Step()
	running.cancelUnwantedPending()
	if len(running.pending) != 1 || running.pending[0].Key != key {
		t.Fatalf("仍有订阅者时 pending = %+v", running.pending)
	}
	if _, queued := running.queued[key]; !queued {
		t.Fatal("仍有订阅者时 queued 标记被删除")
	}
}

func registryTestConfig() Config {
	config := DefaultConfig(1)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	config.AutosaveTicks = 1000
	config.OutboxCapacity = 32
	return config
}

func testRestore() sim.PlayerRestore {
	return sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	}
}

func testStore() storage.Store {
	return storage.NewMemory(storage.Metadata{
		FormatVersion:  2,
		Seed:           1,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
}

func detachAndWait(
	t *testing.T,
	running *Server,
	id sim.SessionID,
	generation uint64,
	exit <-chan SessionExit,
) SessionExit {
	t.Helper()
	if !running.DetachSession(id, generation, context.Canceled) {
		t.Fatalf("DetachSession(%d, %d) = false", id, generation)
	}
	select {
	case got := <-exit:
		return got
	case <-time.After(waitDeadline):
		t.Fatal("等待 session exit 超时")
		return SessionExit{}
	}
}

type orderedCloseEndpoint struct {
	id        sim.SessionID
	record    func(sim.SessionID)
	closeOnce sync.Once
}

type countingRecvEndpoint struct {
	calls chan int

	mu    sync.Mutex
	count int
}

func (endpoint *countingRecvEndpoint) Send(
	context.Context,
	network.ServerMessage,
) error {
	return nil
}

func (endpoint *countingRecvEndpoint) Recv(
	ctx context.Context,
) (network.ClientMessage, error) {
	endpoint.mu.Lock()
	endpoint.count++
	call := endpoint.count
	endpoint.mu.Unlock()
	endpoint.calls <- call
	if call == 1 {
		return network.PlayerInput{Sequence: 1}, nil
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (endpoint *countingRecvEndpoint) Close() error {
	return nil
}

func (endpoint *orderedCloseEndpoint) Send(
	context.Context,
	network.ServerMessage,
) error {
	return nil
}

func (endpoint *orderedCloseEndpoint) Recv(
	ctx context.Context,
) (network.ClientMessage, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (endpoint *orderedCloseEndpoint) Close() error {
	endpoint.closeOnce.Do(func() { endpoint.record(endpoint.id) })
	return nil
}

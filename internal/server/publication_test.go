package server

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func TestSnapshotPublicationHonorsChunkBudgetAndDistanceOrder(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 2, 1<<20, false)
	prepareReadySquare(t, running, generator)

	first := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	second := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	if first.Chunk != (core.ChunkPos{}) ||
		second.Chunk != (core.ChunkPos{X: -1, Z: 0}) {
		t.Fatalf("前两个快照 = %+v, %+v", first.Chunk, second.Chunk)
	}
	assertNoWorldServerMessage(t, client)
}

func TestSnapshotPublicationAllowsOneOversizedFirstChunk(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 9, 1, false)
	prepareReadySquare(t, running, generator)

	message := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	if message.Chunk != (core.ChunkPos{}) {
		t.Fatalf("首个 oversized 快照 = %+v", message.Chunk)
	}
	if message.PayloadBytes() <= 1 {
		t.Fatalf("测试快照没有超过 byte budget: %d", message.PayloadBytes())
	}
	assertNoWorldServerMessage(t, client)
}

func TestInitialSnapshotCapturesSameTickChangesBeforeDelta(t *testing.T) {
	running, client, generator := newPublicationServer(t, 0, 4, 1<<20, true)
	requested := running.engine.Step()
	if len(requested.Acquire) != 1 {
		t.Fatalf("Acquire = %+v", requested.Acquire)
	}
	submitServerAcquiredMisses(running, requested.Acquire)
	generated := running.engine.Step()
	if len(generated.Generate) != 1 {
		t.Fatalf("Generate = %+v", generated.Generate)
	}
	running.engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
		Chunk:     generator.chunk(core.ChunkPos{}),
	})
	ready := running.engine.Step()
	if len(ready.Ready) != 1 || !ready.Players[0].Ready {
		t.Fatalf("spawn ready = %+v", ready)
	}
	running.engine.Enqueue(sim.Command{
		Session: testSessionID, Sequence: 2, Kind: sim.CommandPlayerInput,
		Yaw: 0, Pitch: -1.5, Mining: true,
	})
	for range 4 {
		if primed := running.engine.Step(); len(primed.Changes) != 0 {
			t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
		}
	}
	running.sessions[testSessionID].queueSnapshot(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       core.ChunkPos{},
	}, false)
	result := running.Step()
	if len(result.Changes) != 1 {
		t.Fatalf("同 tick Changes = %+v", result.Changes)
	}

	snapshot := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	if snapshot.Revision != 2 {
		t.Fatalf("初始 snapshot revision = %d，想要 2", snapshot.Revision)
	}
	assertNoWorldServerMessage(t, client)
}

func TestPublishedDeltaIsContiguousAfterSnapshot(t *testing.T) {
	running, client, generator := newPublicationServer(t, 0, 4, 1<<20, true)
	prepareReadySquare(t, running, generator)
	snapshot := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	if snapshot.Revision != 1 {
		t.Fatalf("初始 revision = %d", snapshot.Revision)
	}

	running.engine.Enqueue(sim.Command{
		Session: testSessionID, Sequence: 2, Kind: sim.CommandPlayerInput,
		Pitch: -1.5, Mining: true,
	})
	for range 4 {
		if primed := running.engine.Step(); len(primed.Changes) != 0 {
			t.Fatalf("采掘完成前出现变更: %+v", primed.Changes)
		}
	}
	running.Step()
	delta := recvWorldServerMessage(t, client).(network.BlockChanges)
	if delta.BaseRevision != 1 || delta.NewRevision != 2 {
		t.Fatalf("delta revision = %d→%d", delta.BaseRevision, delta.NewRevision)
	}
	if err := delta.Validate(); err != nil {
		t.Fatalf("发布了非法 delta: %v", err)
	}
}

func TestResyncSnapshotPrecedesOrdinaryPendingSnapshots(t *testing.T) {
	running, client, generator := newPublicationServer(t, 1, 1, 1<<20, false)
	prepareReadySquare(t, running, generator)
	first := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	if first.Chunk != (core.ChunkPos{}) {
		t.Fatalf("首个快照 = %+v", first.Chunk)
	}

	running.incoming <- incomingCommand{
		Session: testSessionID, Generation: 1,
		Command: sim.Command{
			Session:      testSessionID,
			Sequence:     2,
			Kind:         sim.CommandResync,
			Dimension:    core.Overworld,
			Chunk:        core.ChunkPos{},
			HaveRevision: 0,
		},
	}
	running.Step()
	resync := recvWorldServerMessage(t, client).(network.ChunkSnapshot)
	if resync.Chunk != (core.ChunkPos{}) {
		t.Fatalf("resync 前发送了普通快照 %+v", resync.Chunk)
	}
}

func TestForgetRemovesPendingSnapshotsAndSortsChunks(t *testing.T) {
	running, client, _ := newPublicationServer(t, 1, 1, 1<<20, false)
	want := []core.ChunkPos{
		{X: -1, Z: -1}, {X: -1, Z: 0}, {X: -1, Z: 1},
		{X: 0, Z: -1}, {X: 0, Z: 0}, {X: 0, Z: 1},
		{X: 1, Z: -1}, {X: 1, Z: 0}, {X: 1, Z: 1},
	}
	keys := make([]core.ChunkKey, len(want))
	for index, chunk := range want {
		key := core.ChunkKey{Dimension: core.Overworld, Pos: chunk}
		keys[index] = key
		running.sessions[testSessionID].pendingSnapshots[key] = snapshotRequest{}
		running.sessions[testSessionID].publications[key] = &publication{}
	}
	running.publish(sim.TickResult{
		Tick:   1,
		Forget: map[sim.SessionID][]core.ChunkKey{testSessionID: keys},
		Players: []sim.PlayerUpdate{{
			Session:    testSessionID,
			Dimension:  core.Overworld,
			ViewCenter: core.ChunkPos{},
		}},
	})
	for _, key := range keys {
		if _, pending := running.sessions[testSessionID].pendingSnapshots[key]; pending {
			t.Fatalf("Forget 后 pendingSnapshots 仍包含 %+v", key)
		}
		if _, published := running.sessions[testSessionID].publications[key]; published {
			t.Fatalf("Forget 后 publications 仍包含 %+v", key)
		}
	}
	forgotten := recvWorldServerMessage(t, client).(network.ForgetChunks)
	if !reflect.DeepEqual(forgotten.Chunks, want) {
		t.Fatalf("ForgetChunks = %+v，想要 %+v", forgotten.Chunks, want)
	}
	assertNoWorldServerMessage(t, client)
}

func TestPublishLocalResultMapsCanonicalMiningStateIntoSinglePlayerState(t *testing.T) {
	current := &session{id: testSessionID, outbox: make(chan network.ServerMessage, 2)}
	player := sim.PlayerUpdate{
		Session: testSessionID,
		Mining: sim.MiningUpdate{
			Active:        true,
			Target:        core.BlockPos{X: 1, Y: 2, Z: 3},
			ProgressTicks: 6,
			RequiredTicks: 15,
			Harvestable:   true,
		},
	}
	(&Server{}).publishLocalResult(current, sim.TickResult{Tick: 9}, player)

	if len(current.outbox) != 1 {
		t.Fatalf("本地发布消息数 = %d，想要唯一 PlayerState", len(current.outbox))
	}
	state, ok := (<-current.outbox).(network.PlayerState)
	if !ok {
		t.Fatalf("本地发布消息 = %T，想要 PlayerState", state)
	}
	if !state.MiningActive || state.MiningTarget != player.Mining.Target ||
		state.MiningProgressTicks != player.Mining.ProgressTicks ||
		state.MiningRequiredTicks != player.Mining.RequiredTicks ||
		state.MiningHarvestable != player.Mining.Harvestable {
		t.Fatalf("采掘映射 = %+v，想要 %+v", state, player.Mining)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("映射后的 PlayerState 非法: %v", err)
	}
}

func TestRemotePlayerStatesDoNotPublishMiningState(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	h.markSnapshotSent(1, core.ChunkPos{})
	observer := h.playerUpdate(1, true, core.Overworld, [3]float32{0.5, 2, 0.5})
	target := h.playerUpdate(2, true, core.Overworld, [3]float32{0.5, 2, 0.5})
	target.Mining = sim.MiningUpdate{
		Active: true, Target: core.BlockPos{X: 1, Y: 2, Z: 3},
		ProgressTicks: 6, RequiredTicks: 15, Harvestable: true,
	}
	h.publish(sim.TickResult{Tick: 1, Players: []sim.PlayerUpdate{observer, target}})
	h.drain(1)
	h.publish(sim.TickResult{Tick: 2, Players: []sim.PlayerUpdate{observer, target}})
	active := onlyRemotePlayerMessages(h.drain(1))
	target.Mining = sim.MiningUpdate{}
	h.publish(sim.TickResult{Tick: 3, Players: []sim.PlayerUpdate{observer, target}})
	inactive := onlyRemotePlayerMessages(h.drain(1))

	if len(active) != 1 || len(inactive) != 1 {
		t.Fatalf("远端状态消息数 active=%d inactive=%d", len(active), len(inactive))
	}
	activeStates, activeOK := active[0].(network.RemotePlayerStates)
	inactiveStates, inactiveOK := inactive[0].(network.RemotePlayerStates)
	activeStates.ServerTick = 0
	inactiveStates.ServerTick = 0
	if !activeOK || !inactiveOK || !reflect.DeepEqual(activeStates, inactiveStates) {
		t.Fatalf("远端状态受采掘影响: active=%#v inactive=%#v", active[0], inactive[0])
	}
}

func TestForgetSplits4097ChunksIntoValidDeterministicPackets(t *testing.T) {
	current := &session{
		pendingSnapshots: make(map[core.ChunkKey]snapshotRequest),
		publications:     make(map[core.ChunkKey]*publication),
	}
	keys := make([]core.ChunkKey, 4097)
	for index := range keys {
		chunk := core.ChunkPos{X: int32(4096 - index), Z: int32(index % 3)}
		keys[index] = core.ChunkKey{Dimension: core.Overworld, Pos: chunk}
		current.pendingSnapshots[keys[index]] = snapshotRequest{}
		current.publications[keys[index]] = &publication{}
	}

	messages := current.applyForget(keys)
	if len(messages) != 2 {
		t.Fatalf("forget packet count = %d, want 2", len(messages))
	}
	first := messages[0].(network.ForgetChunks)
	second := messages[1].(network.ForgetChunks)
	if len(first.Chunks) != 4096 || len(second.Chunks) != 1 {
		t.Fatalf("forget packet sizes = %d/%d, want 4096/1", len(first.Chunks), len(second.Chunks))
	}
	for index, message := range []network.ForgetChunks{first, second} {
		if err := message.Validate(); err != nil {
			t.Fatalf("forget packet %d invalid: %v", index, err)
		}
	}
	if first.Chunks[0] != (core.ChunkPos{X: 0, Z: 1}) ||
		second.Chunks[0] != (core.ChunkPos{X: 4096}) {
		t.Fatalf("deterministic forget boundaries = first %+v last %+v", first.Chunks[0], second.Chunks[0])
	}
}

func TestDefaultRadiusLargeCenterMovePublishesBoundedForgetPackets(t *testing.T) {
	config := DefaultConfig(1)
	engine := sim.NewEngine(config.ViewRadius, 0, 0)
	const observer = sim.SessionID(77)
	engine.RegisterObserverSession(observer)
	engine.Enqueue(sim.Command{
		Session: observer, Sequence: 1, Kind: sim.CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{},
	})
	engine.Step()
	engine.Enqueue(sim.Command{
		Session: observer, Sequence: 2, Kind: sim.CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: core.ChunkPos{X: 1000},
	})
	moved := engine.Step()
	keys := moved.Forget[observer]
	if len(keys) != 4489 {
		t.Fatalf("default-radius center move forget count = %d, want 4489", len(keys))
	}

	current := &session{
		pendingSnapshots: make(map[core.ChunkKey]snapshotRequest),
		publications:     make(map[core.ChunkKey]*publication),
	}
	messages := current.applyForget(keys)
	if len(messages) != 2 {
		t.Fatalf("default-radius forget packet count = %d, want 2", len(messages))
	}
	total := 0
	for index, raw := range messages {
		message := raw.(network.ForgetChunks)
		if err := message.Validate(); err != nil {
			t.Fatalf("default-radius forget packet %d invalid: %v", index, err)
		}
		total += len(message.Chunks)
	}
	if total != 4489 {
		t.Fatalf("default-radius packet total = %d, want 4489", total)
	}
}

func newPublicationServer(
	t *testing.T,
	radius, snapshotChunks, snapshotBytes int,
	flat bool,
) (*Server, network.ClientEndpoint, *gatedGenerator) {
	t.Helper()
	client, endpoint := network.NewMemoryPair(64)
	generator := &gatedGenerator{
		release: make(chan struct{}),
		flat:    flat,
	}
	config := DefaultConfig(1)
	config.ViewRadius = radius
	config.Workers = 1
	config.SnapshotChunks = snapshotChunks
	config.SnapshotBytes = snapshotBytes
	config.OutboxCapacity = 64
	running := newMemoryAttachedWorldForTest(config, endpoint, generator)
	t.Cleanup(func() {
		close(generator.release)
		shutdownServerForTest(t, running)
	})
	return running, client, generator
}

func prepareReadySquare(
	t *testing.T,
	running *Server,
	generator *gatedGenerator,
) {
	t.Helper()
	requested := running.Step()
	if len(requested.Acquire) == 0 {
		t.Fatal("没有 acquisition requests")
	}
	submitServerAcquiredMisses(running, requested.Acquire)
	generated := running.engine.Step()
	if len(generated.Generate) != len(requested.Acquire) {
		t.Fatalf("Generate = %d，想要 %d", len(generated.Generate), len(requested.Acquire))
	}
	for _, key := range generated.Generate {
		running.engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     generator.chunk(key.Pos),
		})
	}
	ready := running.Step()
	if len(ready.Ready) != len(generated.Generate) {
		t.Fatalf("Ready = %d，想要 %d", len(ready.Ready), len(generated.Generate))
	}
}

func submitServerAcquiredMisses(running *Server, keys []core.ChunkKey) {
	for _, key := range keys {
		running.engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	}
}

func recvServerMessage(
	t *testing.T,
	client network.ClientEndpoint,
) network.ServerMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
	defer cancel()
	message, err := client.Recv(ctx)
	if err != nil {
		t.Fatalf("接收 server message: %v", err)
	}
	return message
}

func recvWorldServerMessage(
	t *testing.T,
	client network.ClientEndpoint,
) network.ServerMessage {
	t.Helper()
	for {
		message := recvServerMessage(t, client)
		switch message.(type) {
		case network.PlayerState, network.InventoryState,
			network.ItemDropUpserts, network.ItemDropRemoves:
			// 玩家状态、快捷栏与掉落物差分是按会话定向的控制消息，不属于世界发布。
			continue
		}
		return message
	}
}

func assertNoServerMessage(
	t *testing.T,
	client network.ClientEndpoint,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if message, err := client.Recv(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("意外 server message = %#v, err=%v", message, err)
	}
}

func assertNoWorldServerMessage(
	t *testing.T,
	client network.ClientEndpoint,
) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Millisecond)
	for {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		message, err := client.Recv(ctx)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			t.Fatalf("接收 server message: %v", err)
		}
		switch message.(type) {
		case network.PlayerState, network.InventoryState,
			network.ItemDropUpserts, network.ItemDropRemoves:
		default:
			t.Fatalf("意外 world server message = %#v", message)
		}
	}
}

type gatedGenerator struct {
	release chan struct{}
	flat    bool
}

func (generator *gatedGenerator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	<-generator.release
	return generator.chunk(pos)
}

func (generator *gatedGenerator) BaseBlockAt(pos core.BlockPos) core.BlockID {
	if !generator.flat {
		return core.AirID
	}
	return publicationBaseBlock(pos)
}

func (generator *gatedGenerator) chunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	if !generator.flat {
		return chunk
	}
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			for y := int32(core.MinY); y <= 0; y++ {
				worldPos := core.BlockPos{
					X: pos.X<<core.SectionShift + int32(x),
					Y: y,
					Z: pos.Z<<core.SectionShift + int32(z),
				}
				chunk.SetBlock(x, y, z, publicationBaseBlock(worldPos))
			}
		}
	}
	chunk.Compact()
	return chunk
}

func publicationBaseBlock(pos core.BlockPos) core.BlockID {
	switch {
	case pos.Y < core.MinY || pos.Y >= core.MaxY:
		return core.AirID
	case pos.Y == core.MinY:
		return core.BedrockID
	case pos.Y < 0:
		return core.StoneID
	case pos.Y == 0:
		return core.GrassID
	default:
		return core.AirID
	}
}

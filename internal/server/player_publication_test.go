package server

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func TestRemotePlayerInterestMatrix(t *testing.T) {
	tests := []struct {
		name             string
		observerReady    bool
		targetReady      bool
		targetDimension  core.DimensionID
		targetPosition   mgl32.Vec3
		snapshotSent     bool
		removeTargetMeta bool
		wantSpawn        bool
	}{
		{
			name:          "双方 ready 且 snapshot 已发送",
			observerReady: true, targetReady: true,
			targetDimension: core.Overworld, snapshotSent: true,
			wantSpawn: true,
		},
		{
			name:          "观察者未 ready",
			observerReady: false, targetReady: true,
			targetDimension: core.Overworld, snapshotSent: true,
		},
		{
			name:          "目标未 ready",
			observerReady: true, targetReady: false,
			targetDimension: core.Overworld, snapshotSent: true,
		},
		{
			name:          "异维度",
			observerReady: true, targetReady: true,
			targetDimension: core.DimensionID(1), snapshotSent: true,
		},
		{
			name:          "目标脚底 chunk 不 wanted",
			observerReady: true, targetReady: true,
			targetDimension: core.Overworld,
			targetPosition:  mgl32.Vec3{16.1, 2, 0}, snapshotSent: true,
		},
		{
			name:          "wanted chunk 尚未发送 snapshot",
			observerReady: true, targetReady: true,
			targetDimension: core.Overworld, snapshotSent: false,
		},
		{
			name:          "目标 session 元数据已移除",
			observerReady: true, targetReady: true,
			targetDimension: core.Overworld, snapshotSent: true,
			removeTargetMeta: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			h := newRemotePublicationHarness(t, 1, 2)
			h.markSnapshotSent(1, core.ChunkPos{})
			if !test.snapshotSent {
				h.running.sessions[1].publications[overworldChunk(core.ChunkPos{})].snapshotSent = false
			}
			if test.removeTargetMeta {
				delete(h.running.sessions, 2)
			}
			observer := h.playerUpdate(1, test.observerReady, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
			target := h.playerUpdate(2, test.targetReady, test.targetDimension, test.targetPosition)
			h.publish(sim.TickResult{Tick: 10, Players: []sim.PlayerUpdate{observer, target}})

			messages := onlyRemotePlayerMessages(h.drain(1))
			if test.wantSpawn {
				if len(messages) != 1 {
					t.Fatalf("remote messages = %#v，想要一个 Spawn", messages)
				}
				spawn, ok := messages[0].(network.RemotePlayerSpawn)
				if !ok || spawn.PlayerID != h.playerID(2) || spawn.ServerTick != 10 {
					t.Fatalf("remote message = %#v", messages[0])
				}
				return
			}
			if len(messages) != 0 {
				t.Fatalf("不可见目标产生 remote messages: %#v", messages)
			}
		})
	}

	t.Run("自己永不进入远端 roster", func(t *testing.T) {
		h := newRemotePublicationHarness(t, 1)
		h.markSnapshotSent(1, core.ChunkPos{})
		h.publish(sim.TickResult{Tick: 1, Players: []sim.PlayerUpdate{
			h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5}),
		}})
		if messages := onlyRemotePlayerMessages(h.drain(1)); len(messages) != 0 {
			t.Fatalf("自己的 remote messages = %#v", messages)
		}
	})
}

func TestRemotePlayerOutsideInterestJoinAndLeaveAreSilent(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	h.markSnapshotSent(1, core.ChunkPos{})
	observer := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	target := h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{16.1, 2, 0.5})
	h.publish(sim.TickResult{Tick: 1, Players: []sim.PlayerUpdate{observer, target}})
	delete(h.running.sessions, 2)
	h.publish(sim.TickResult{Tick: 2, Players: []sim.PlayerUpdate{observer}})
	if messages := onlyRemotePlayerMessages(h.drain(1)); len(messages) != 0 {
		t.Fatalf("兴趣外 join/leave 产生 remote messages: %#v", messages)
	}
}

func TestRemotePlayerNegativeFootChunkUsesFloor(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	h.moveInterest(1, core.ChunkPos{X: -1})
	h.markSnapshotSent(1, core.ChunkPos{X: -1})
	h.publish(sim.TickResult{Tick: 3, Players: []sim.PlayerUpdate{
		h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{-0.1, 2, 0.5}),
		h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{-0.1, 2, 0.5}),
	}})
	messages := onlyRemotePlayerMessages(h.drain(1))
	if len(messages) != 1 {
		t.Fatalf("x=-0.1 remote messages = %#v，想要 Spawn", messages)
	}
	if _, ok := messages[0].(network.RemotePlayerSpawn); !ok {
		t.Fatalf("x=-0.1 message = %T，想要 RemotePlayerSpawn", messages[0])
	}
}

func TestRemotePlayerPublicationOrder(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	h.markSnapshotSent(1, core.ChunkPos{})
	oldObserver := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	oldTarget := h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	h.publish(sim.TickResult{Tick: 1, Players: []sim.PlayerUpdate{oldObserver, oldTarget}})
	h.drain(1)

	moved := h.moveInterest(1, core.ChunkPos{X: 1})
	for _, key := range moved.Acquire {
		h.running.engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	}
	generated := h.running.engine.Step()
	for _, key := range generated.Generate {
		h.running.engine.SubmitGenerated(sim.GeneratedChunk{
			Dimension: key.Dimension,
			Pos:       key.Pos,
			Chunk:     world.NewChunk(key.Pos),
		})
	}
	ready := h.running.engine.Step()
	newObserver := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{16.5, 2, 0.5})
	newTarget := h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{16.5, 2, 0.5})
	h.publish(sim.TickResult{
		Tick:    ready.Tick,
		Forget:  moved.Forget,
		Ready:   ready.Ready,
		Players: []sim.PlayerUpdate{newObserver, newTarget},
	})
	assertRemoteOrder(t, h.drain(1), []reflect.Type{
		reflect.TypeOf(network.RemotePlayerDespawn{}),
		reflect.TypeOf(network.ForgetChunks{}),
		reflect.TypeOf(network.ChunkSnapshot{}),
		reflect.TypeOf(network.RemotePlayerSpawn{}),
		reflect.TypeOf(network.PlayerState{}),
	})
}

func TestRemotePlayerGenerationReplacementDespawnsBeforeSpawn(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	h.markSnapshotSent(1, core.ChunkPos{})
	players := []sim.PlayerUpdate{
		h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5}),
		h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5}),
	}
	h.publish(sim.TickResult{Tick: 1, Players: players})
	h.drain(1)

	old := h.running.sessions[2]
	h.running.sessions[2] = h.newSession(2, 2, old.playerID, old.displayName, 32)
	h.publish(sim.TickResult{Tick: 2, Players: players})
	assertRemoteOrder(t, h.drain(1), []reflect.Type{
		reflect.TypeOf(network.RemotePlayerDespawn{}),
		reflect.TypeOf(network.RemotePlayerSpawn{}),
		reflect.TypeOf(network.PlayerState{}),
	})
}

func TestRemotePlayerStatesAreSortedAndNewSpawnsSkipCurrentTick(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2, 3, 4)
	h.running.sessions[2].playerID = publicationPlayerID(30)
	h.running.sessions[3].playerID = publicationPlayerID(10)
	h.running.sessions[4].playerID = publicationPlayerID(20)
	h.markSnapshotSent(1, core.ChunkPos{})
	players := h.readyPlayers(1, 2, 3, 4)

	h.publish(sim.TickResult{Tick: 7, Players: players})
	first := onlyRemotePlayerMessages(h.drain(1))
	if len(first) != 3 {
		t.Fatalf("spawn tick remote messages = %#v", first)
	}
	wantIDs := []core.PlayerID{publicationPlayerID(10), publicationPlayerID(20), publicationPlayerID(30)}
	for index, message := range first {
		spawn, ok := message.(network.RemotePlayerSpawn)
		if !ok || spawn.PlayerID != wantIDs[index] {
			t.Fatalf("spawn[%d] = %#v，想要 PlayerID %x", index, message, wantIDs[index])
		}
	}

	h.publish(sim.TickResult{Tick: 8, Players: players})
	second := onlyRemotePlayerMessages(h.drain(1))
	if len(second) != 1 {
		t.Fatalf("stable tick remote messages = %#v", second)
	}
	states, ok := second[0].(network.RemotePlayerStates)
	if !ok || states.ServerTick != 8 || len(states.Players) != 3 {
		t.Fatalf("stable states = %#v", second[0])
	}
	for index, state := range states.Players {
		if state.PlayerID != wantIDs[index] {
			t.Fatalf("states[%d].PlayerID = %x，想要 %x", index, state.PlayerID, wantIDs[index])
		}
	}
}

func TestRemotePlayerEightSessionsPublishSevenStatesWith296BytePayload(t *testing.T) {
	ids := []sim.SessionID{1, 2, 3, 4, 5, 6, 7, 8}
	h := newRemotePublicationHarness(t, ids...)
	for _, observer := range ids {
		h.markSnapshotSent(observer, core.ChunkPos{})
	}
	players := h.readyPlayers(ids...)
	h.publish(sim.TickResult{Tick: 20, Players: players})
	for _, observer := range ids {
		for _, message := range onlyRemotePlayerMessages(h.drain(observer)) {
			if _, states := message.(network.RemotePlayerStates); states {
				t.Fatalf("observer %d spawn tick 收到 States", observer)
			}
		}
	}

	h.publish(sim.TickResult{Tick: 21, Players: players})
	codec, err := network.NewCodec()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = codec.Close() })
	for _, observer := range ids {
		messages := onlyRemotePlayerMessages(h.drain(observer))
		if len(messages) != 1 {
			t.Fatalf("observer %d stable remote messages = %#v", observer, messages)
		}
		states, ok := messages[0].(network.RemotePlayerStates)
		if !ok || len(states.Players) != 7 {
			t.Fatalf("observer %d states = %#v", observer, messages[0])
		}
		if err := states.Validate(); err != nil {
			t.Fatalf("observer %d states invalid: %v", observer, err)
		}
		_, payload, err := codec.EncodeServer(network.StatePlay, states)
		if err != nil {
			t.Fatalf("observer %d encode states: %v", observer, err)
		}
		if len(payload) != 296 {
			t.Fatalf("observer %d states payload = %d bytes，想要 296", observer, len(payload))
		}
	}
}

func TestSessionRegistrySlowSessionRemotePublicationDoesNotCloseHealthySessions(t *testing.T) {
	ids := []sim.SessionID{1, 2, 3, 4, 5, 6, 7, 8}
	h := newRemotePublicationHarness(t, ids...)
	for _, observer := range ids {
		h.markSnapshotSent(observer, core.ChunkPos{})
	}
	slow := h.running.sessions[1]
	slow.outbox = make(chan network.ServerMessage, 1)

	h.publish(sim.TickResult{Tick: 30, Players: h.readyPlayers(ids...)})
	if h.running.sessions[1] != nil {
		t.Fatal("满 outbox 的 slow observer 未 detach")
	}
	for _, observer := range ids[1:] {
		messages := h.drain(observer)
		foundLocal := false
		for _, message := range messages {
			state, ok := message.(network.PlayerState)
			if ok && state.ServerTick == 30 {
				foundLocal = true
				break
			}
		}
		if !foundLocal {
			t.Fatalf("healthy observer %d 未收到 tick 30 local PlayerState: %#v", observer, messages)
		}
		if h.running.sessions[observer] == nil {
			t.Fatalf("slow observer 关闭了 healthy observer %d", observer)
		}
	}
}

func assertRemoteOrder(t *testing.T, messages []network.ServerMessage, want []reflect.Type) {
	t.Helper()
	got := make([]reflect.Type, len(messages))
	for index, message := range messages {
		got[index] = reflect.TypeOf(message)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order=%v want=%v messages=%#v", got, want, messages)
	}
}

type remotePublicationHarness struct {
	t       *testing.T
	running *Server
	seq     map[sim.SessionID]uint64
}

func newRemotePublicationHarness(t *testing.T, ids ...sim.SessionID) *remotePublicationHarness {
	t.Helper()
	config := DefaultConfig(1)
	config.ViewRadius = 0
	config.SnapshotChunks = 64
	config.SnapshotBytes = 1 << 20
	config.OutboxCapacity = 32
	running := &Server{
		config:         config,
		engine:         sim.NewEngine(0, 0, 0),
		sessions:       make(map[sim.SessionID]*session),
		playerSessions: make(map[core.PlayerID]sim.SessionID),
		lifecycle:      serverRunning,
	}
	h := &remotePublicationHarness{t: t, running: running, seq: make(map[sim.SessionID]uint64)}
	for _, id := range ids {
		playerID := publicationPlayerID(byte(id))
		running.engine.RegisterObserverSession(id)
		running.sessions[id] = h.newSession(id, 1, playerID, fmt.Sprintf("Player-%d", id), 32)
		running.playerSessions[playerID] = id
		h.enqueueCenter(id, core.ChunkPos{})
	}
	running.engine.Step()
	t.Cleanup(func() {
		for _, current := range running.sessions {
			current.shutdown()
		}
	})
	return h
}

func (h *remotePublicationHarness) newSession(
	id sim.SessionID,
	generation uint64,
	playerID core.PlayerID,
	displayName string,
	capacity int,
) *session {
	ctx, cancel := context.WithCancel(context.Background())
	return &session{
		id:               id,
		generation:       generation,
		playerID:         playerID,
		displayName:      displayName,
		endpoint:         newBlockingServerEndpoint(),
		ctx:              ctx,
		cancel:           cancel,
		outbox:           make(chan network.ServerMessage, capacity),
		exit:             make(chan SessionExit, 1),
		publications:     make(map[core.ChunkKey]*publication),
		pendingSnapshots: make(map[core.ChunkKey]snapshotRequest),
	}
}

func (h *remotePublicationHarness) enqueueCenter(id sim.SessionID, center core.ChunkPos) {
	h.seq[id]++
	h.running.engine.Enqueue(sim.Command{
		Session: id, Sequence: h.seq[id], Kind: sim.CommandTrustedObserverCenter,
		Dimension: core.Overworld, Center: center,
	})
}

func (h *remotePublicationHarness) moveInterest(id sim.SessionID, center core.ChunkPos) sim.TickResult {
	h.t.Helper()
	h.enqueueCenter(id, center)
	return h.running.engine.Step()
}

func (h *remotePublicationHarness) markSnapshotSent(id sim.SessionID, pos core.ChunkPos) {
	h.t.Helper()
	h.running.sessions[id].publications[overworldChunk(pos)] = &publication{snapshotSent: true}
}

func (h *remotePublicationHarness) playerUpdate(
	id sim.SessionID,
	ready bool,
	dimension core.DimensionID,
	position mgl32.Vec3,
) sim.PlayerUpdate {
	update := sim.PlayerUpdate{
		Session:    id,
		Dimension:  dimension,
		ViewCenter: core.ChunkPos{},
		Ready:      ready,
	}
	update.State.Position = position
	update.Yaw = float32(id)
	update.Pitch = -float32(id)
	return update
}

func (h *remotePublicationHarness) readyPlayers(ids ...sim.SessionID) []sim.PlayerUpdate {
	players := make([]sim.PlayerUpdate, 0, len(ids))
	for _, id := range ids {
		players = append(players, h.playerUpdate(
			id, true, core.Overworld,
			mgl32.Vec3{float32(id) * 0.25, 2, 0.5},
		))
	}
	return players
}

func (h *remotePublicationHarness) publish(result sim.TickResult) {
	h.running.publish(result)
}

func (h *remotePublicationHarness) drain(id sim.SessionID) []network.ServerMessage {
	h.t.Helper()
	current := h.running.sessions[id]
	if current == nil {
		return nil
	}
	messages := make([]network.ServerMessage, 0, len(current.outbox))
	for len(current.outbox) > 0 {
		messages = append(messages, <-current.outbox)
	}
	return messages
}

func (h *remotePublicationHarness) playerID(id sim.SessionID) core.PlayerID {
	current := h.running.sessions[id]
	if current == nil {
		return core.PlayerID{}
	}
	return current.playerID
}

func onlyRemotePlayerMessages(messages []network.ServerMessage) []network.ServerMessage {
	result := make([]network.ServerMessage, 0, len(messages))
	for _, message := range messages {
		switch message.(type) {
		case network.RemotePlayerSpawn,
			network.RemotePlayerDespawn,
			network.RemotePlayerStates:
			result = append(result, message)
		}
	}
	return result
}

func publicationPlayerID(suffix byte) core.PlayerID {
	return core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, suffix}
}

func overworldChunk(pos core.ChunkPos) core.ChunkKey {
	return core.ChunkKey{Dimension: core.Overworld, Pos: pos}
}

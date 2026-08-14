package server

import (
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

func TestCompanionPublicationWaitsForFootChunkSnapshot(t *testing.T) {
	h := newRemotePublicationHarness(t, 1)
	id := publicationCompanionID(1)
	h.running.config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	observer := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	update := publicationCompanionUpdate(id, 0.5)

	h.publish(sim.TickResult{Tick: 10, Players: []sim.PlayerUpdate{observer}, Companions: []sim.CompanionUpdate{update}})
	if messages := onlyCompanionMessages(h.drain(1)); len(messages) != 0 {
		t.Fatalf("snapshot 前收到伙伴消息：%#v", messages)
	}
	if got := len(h.running.sessions[1].visibleCompanions); got != 0 {
		t.Fatalf("snapshot 前 visibleCompanions=%d，想要 0", got)
	}

	key := overworldChunk(core.ChunkPos{})
	h.running.engine.SubmitAcquired(sim.AcquiredChunk{Key: key, Missing: true})
	generate := h.running.engine.Step()
	if len(generate.Generate) != 1 || generate.Generate[0] != key {
		t.Fatalf("Generate=%+v，想要 [%+v]", generate.Generate, key)
	}
	h.running.engine.SubmitGenerated(sim.GeneratedChunk{
		Dimension: key.Dimension,
		Pos:       key.Pos,
		Chunk:     world.NewChunk(key.Pos),
	})
	ready := h.running.engine.Step()
	if len(ready.Ready) != 1 || ready.Ready[0] != key {
		t.Fatalf("Ready=%+v，想要 [%+v]", ready.Ready, key)
	}
	h.publish(sim.TickResult{
		Tick:       ready.Tick,
		Ready:      ready.Ready,
		Players:    []sim.PlayerUpdate{observer},
		Companions: []sim.CompanionUpdate{update},
	})

	messages := onlyChunkAndCompanionMessages(h.drain(1))
	assertPublicationTypes(t, messages, []reflect.Type{
		reflect.TypeOf(network.ChunkSnapshot{}),
		reflect.TypeOf(network.CompanionSpawn{}),
	})
	spawn := messages[1].(network.CompanionSpawn)
	if err := spawn.Validate(); err != nil {
		t.Fatalf("CompanionSpawn.Validate: %v", err)
	}
	if spawn.ID != id || spawn.Name != "阿木" || spawn.Tick != ready.Tick ||
		spawn.Dimension != update.Dimension || spawn.Position != update.State.Position ||
		spawn.Yaw != update.Yaw || spawn.Pitch != update.Pitch {
		t.Fatalf("CompanionSpawn=%+v，想要 definition/update 的完整状态", spawn)
	}
}

func TestCompanionPublicationStatesAreSortedAndNewSpawnsSkipCurrentTick(t *testing.T) {
	h := newRemotePublicationHarness(t, 1)
	ids := []companion.ID{
		publicationCompanionID(30),
		publicationCompanionID(10),
		publicationCompanionID(20),
		publicationCompanionID(15),
	}
	h.running.config.Companions = []companion.Definition{
		{ID: ids[0], Name: "甲"},
		{ID: ids[1], Name: "乙"},
		{ID: ids[2], Name: "丙"},
		{ID: ids[3], Name: "丁"},
	}
	h.markSnapshotSent(1, core.ChunkPos{})
	observer := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	firstUpdates := []sim.CompanionUpdate{
		publicationCompanionUpdate(ids[0], 3.5),
		publicationCompanionUpdate(ids[1], 1.5),
		publicationCompanionUpdate(ids[2], 2.5),
	}

	h.publish(sim.TickResult{Tick: 7, Players: []sim.PlayerUpdate{observer}, Companions: firstUpdates})
	first := onlyCompanionMessages(h.drain(1))
	assertCompanionSpawns(t, first, []companion.ID{ids[1], ids[2], ids[0]})

	secondUpdates := append([]sim.CompanionUpdate{}, firstUpdates...)
	secondUpdates = append(secondUpdates, publicationCompanionUpdate(ids[3], 1.75))
	h.publish(sim.TickResult{Tick: 8, Players: []sim.PlayerUpdate{observer}, Companions: secondUpdates})
	second := onlyCompanionMessages(h.drain(1))
	assertPublicationTypes(t, second, []reflect.Type{
		reflect.TypeOf(network.CompanionSpawn{}),
		reflect.TypeOf(network.CompanionStates{}),
	})
	spawn := second[0].(network.CompanionSpawn)
	if err := spawn.Validate(); err != nil {
		t.Fatalf("second tick CompanionSpawn.Validate: %v", err)
	}
	if spawn.ID != ids[3] {
		t.Fatalf("second tick Spawn.ID=%s，想要 %s", spawn.ID, ids[3])
	}
	states := second[1].(network.CompanionStates)
	assertCompanionStates(t, states, 8, []companion.ID{ids[1], ids[2], ids[0]})

	h.publish(sim.TickResult{Tick: 9, Players: []sim.PlayerUpdate{observer}, Companions: secondUpdates})
	third := onlyCompanionMessages(h.drain(1))
	assertPublicationTypes(t, third, []reflect.Type{reflect.TypeOf(network.CompanionStates{})})
	assertCompanionStates(t, third[0].(network.CompanionStates), 9, []companion.ID{ids[1], ids[3], ids[2], ids[0]})
}

func TestCompanionPublicationDespawnsOnInterestExit(t *testing.T) {
	h := newRemotePublicationHarness(t, 1, 2)
	id := publicationCompanionID(1)
	h.running.config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	h.markSnapshotSent(1, core.ChunkPos{})
	oldObserver := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	oldTarget := h.playerUpdate(2, true, core.Overworld, mgl32.Vec3{0.75, 2, 0.5})
	update := publicationCompanionUpdate(id, 0.5)
	h.publish(sim.TickResult{
		Tick:       1,
		Players:    []sim.PlayerUpdate{oldObserver, oldTarget},
		Companions: []sim.CompanionUpdate{update},
	})
	h.drain(1)

	moved := h.moveInterest(1, core.ChunkPos{X: 1})
	newObserver := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{16.5, 2, 0.5})
	newObserver.ViewCenter = core.ChunkPos{X: 1}
	h.publish(sim.TickResult{
		Tick:       2,
		Forget:     moved.Forget,
		Players:    []sim.PlayerUpdate{newObserver, oldTarget},
		Companions: []sim.CompanionUpdate{update},
	})

	published := h.drain(1)
	messages := onlyEntityDespawnAndForgetMessages(published)
	assertPublicationTypes(t, messages, []reflect.Type{
		reflect.TypeOf(network.RemotePlayerDespawn{}),
		reflect.TypeOf(network.CompanionDespawn{}),
		reflect.TypeOf(network.ForgetChunks{}),
	})
	if err := messages[1].(network.CompanionDespawn).Validate(); err != nil {
		t.Fatalf("CompanionDespawn.Validate: %v", err)
	}
	current := h.running.sessions[1]
	if _, ok := current.visiblePlayers[h.playerID(2)]; ok {
		t.Fatal("兴趣退出后远端玩家仍可见")
	}
	if _, ok := current.visibleCompanions[id]; ok {
		t.Fatal("兴趣退出后伙伴仍可见")
	}
	for _, message := range onlyCompanionMessages(published) {
		if _, ok := message.(network.CompanionStates); ok {
			t.Fatal("兴趣退出 tick 仍发布旧伙伴 States")
		}
	}
}

func TestEightPlayersAndFourCompanionsUseIndependentServerCapacity(t *testing.T) {
	firstPlayerID := playerIdentity(1).PlayerID
	sameBytes := companion.ID(firstPlayerID)
	definitions := []companion.Definition{
		{ID: sameBytes, Name: "阿木"},
		{ID: publicationCompanionID(42), Name: "阿石"},
		{ID: publicationCompanionID(43), Name: "阿铁"},
		{ID: publicationCompanionID(44), Name: "阿光"},
	}
	config := hostTestConfig()
	config.MaxPlayers = 8
	config.Companions = definitions
	config.OutboxCapacity = 4096
	config.HeartbeatInterval = time.Hour
	config.HeartbeatTimeout = time.Hour
	host, stop := startHostWithConfig(t, config, newHostTestStore())
	defer stop()
	logins := loginEightMemoryPlayers(t, host)
	t.Cleanup(func() {
		for _, login := range logins {
			_ = login.Client.Close()
		}
	})

	first := activeLoginForPlayer(t, host, logins[0].Identity.PlayerID)
	second := activeLoginForPlayer(t, host, logins[1].Identity.PlayerID)
	waitForIndependentCompanionCapacity(t, host, second.Session, first.Session, first.PlayerID, sameBytes)
	_, err := attemptMemoryLogin(host, playerIdentity(9))
	assertLoginRejectCode(t, err, network.LoginServerFull)

	host.mu.Lock()
	players, sessions := len(host.activeByPlayer), len(host.activeBySession)
	host.mu.Unlock()
	host.world.stepMu.Lock()
	bodies := host.world.engine.CompanionBodies()
	observer := host.world.sessions[second.Session]
	_, playerVisible := observer.visiblePlayers[first.PlayerID]
	_, companionVisible := observer.visibleCompanions[sameBytes]
	host.world.stepMu.Unlock()
	if players != 8 || sessions != 8 || len(bodies) != companion.MaxActive {
		t.Fatalf("第九名拒绝后 players/sessions/companions=%d/%d/%d，想要 8/8/4", players, sessions, len(bodies))
	}
	if !playerVisible || !companionVisible {
		t.Fatalf("同字节玩家/伙伴可见域丢失：player=%t companion=%t", playerVisible, companionVisible)
	}
}

func TestCompanionPublicationRejectsUnknownDefinitionWithoutPartialVisibility(t *testing.T) {
	h := newRemotePublicationHarness(t, 1)
	known := publicationCompanionID(1)
	unknown := publicationCompanionID(2)
	h.running.config.Companions = []companion.Definition{{ID: known, Name: "阿木"}}
	h.markSnapshotSent(1, core.ChunkPos{})
	observer := h.playerUpdate(1, true, core.Overworld, mgl32.Vec3{0.5, 2, 0.5})
	h.publish(sim.TickResult{
		Tick:       1,
		Players:    []sim.PlayerUpdate{observer},
		Companions: []sim.CompanionUpdate{publicationCompanionUpdate(known, 0.5)},
	})
	current := h.running.sessions[1]
	h.drain(1)
	wantVisible := maps.Clone(current.visibleCompanions)

	h.publish(sim.TickResult{
		Tick:    2,
		Players: []sim.PlayerUpdate{observer},
		Companions: []sim.CompanionUpdate{
			publicationCompanionUpdate(known, 0.75),
			publicationCompanionUpdate(unknown, 32.5),
		},
	})
	if got := len(current.outbox); got != 0 {
		t.Fatalf("未知 definition 前产生 %d 条 partial enqueue", got)
	}
	if !maps.Equal(current.visibleCompanions, wantVisible) {
		t.Fatalf("未知 definition 改写可见集：got=%v want=%v", current.visibleCompanions, wantVisible)
	}
	if h.running.sessions[1] != nil {
		t.Fatal("未知 definition 未关闭当前 session")
	}
}

func publicationCompanionID(suffix byte) companion.ID {
	return companion.ID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, suffix}
}

func publicationCompanionUpdate(id companion.ID, x float32) sim.CompanionUpdate {
	update := sim.CompanionUpdate{
		ID:        id,
		Dimension: core.Overworld,
		Yaw:       x,
		Pitch:     -0.25,
	}
	update.State.Position = mgl32.Vec3{x, 2, 0.5}
	return update
}

func onlyCompanionMessages(messages []network.ServerMessage) []network.ServerMessage {
	result := make([]network.ServerMessage, 0, len(messages))
	for _, message := range messages {
		switch message.(type) {
		case network.CompanionSpawn, network.CompanionStates, network.CompanionDespawn:
			result = append(result, message)
		}
	}
	return result
}

func onlyChunkAndCompanionMessages(messages []network.ServerMessage) []network.ServerMessage {
	result := make([]network.ServerMessage, 0, len(messages))
	for _, message := range messages {
		switch message.(type) {
		case network.ChunkSnapshot, network.CompanionSpawn, network.CompanionStates, network.CompanionDespawn:
			result = append(result, message)
		}
	}
	return result
}

func onlyEntityDespawnAndForgetMessages(messages []network.ServerMessage) []network.ServerMessage {
	result := make([]network.ServerMessage, 0, len(messages))
	for _, message := range messages {
		switch message.(type) {
		case network.RemotePlayerDespawn, network.CompanionDespawn, network.ForgetChunks:
			result = append(result, message)
		}
	}
	return result
}

func assertPublicationTypes(t *testing.T, messages []network.ServerMessage, want []reflect.Type) {
	t.Helper()
	got := make([]reflect.Type, len(messages))
	for index, message := range messages {
		got[index] = reflect.TypeOf(message)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publication types=%v，想要 %v；messages=%#v", got, want, messages)
	}
}

func assertCompanionSpawns(t *testing.T, messages []network.ServerMessage, want []companion.ID) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("CompanionSpawn 数=%d，想要 %d；messages=%#v", len(messages), len(want), messages)
	}
	for index, message := range messages {
		spawn, ok := message.(network.CompanionSpawn)
		if !ok || spawn.ID != want[index] {
			t.Fatalf("Spawn[%d]=%#v，想要 ID %s", index, message, want[index])
		}
		if err := spawn.Validate(); err != nil {
			t.Fatalf("Spawn[%d].Validate: %v", index, err)
		}
	}
}

func assertCompanionStates(t *testing.T, states network.CompanionStates, tick uint64, want []companion.ID) {
	t.Helper()
	if err := states.Validate(); err != nil {
		t.Fatalf("CompanionStates.Validate: %v", err)
	}
	if states.Tick != tick || len(states.States) != len(want) {
		t.Fatalf("CompanionStates tick/count=%d/%d，想要 %d/%d", states.Tick, len(states.States), tick, len(want))
	}
	for index, state := range states.States {
		if state.ID != want[index] {
			t.Fatalf("States[%d].ID=%s，想要 %s", index, state.ID, want[index])
		}
	}
}

func waitForIndependentCompanionCapacity(
	t *testing.T,
	host *Host,
	observerSession sim.SessionID,
	playerSession sim.SessionID,
	playerID core.PlayerID,
	companionID companion.ID,
) {
	t.Helper()
	deadline := time.Now().Add(waitDeadline)
	lastBodies, lastSessions, lastPlayers, lastCompanions := 0, 0, 0, 0
	lastObserver := false
	lastSnapshots := 0
	var lastObserverUpdate, lastPlayerUpdate sim.PlayerUpdate
	var lastCompanionBody companion.Body
	lastPlayerWanted, lastCompanionWanted := false, false
	for time.Now().Before(deadline) {
		host.world.stepMu.Lock()
		bodies := host.world.engine.CompanionBodies()
		observer := host.world.sessions[observerSession]
		lastBodies = len(bodies)
		lastSessions = len(host.world.sessions)
		lastObserver = observer != nil
		lastObserverUpdate, _ = host.world.engine.Player(observerSession)
		lastPlayerUpdate, _ = host.world.engine.Player(playerSession)
		for _, body := range bodies {
			if body.ID == companionID {
				lastCompanionBody = body
				break
			}
		}
		lastPlayerWanted = host.world.engine.SessionWantsChunk(
			observerSession,
			publicationFootChunk(lastPlayerUpdate.Dimension, lastPlayerUpdate.State.Position),
		)
		lastCompanionWanted = host.world.engine.SessionWantsChunk(
			observerSession,
			publicationFootChunk(lastCompanionBody.Dimension, lastCompanionBody.Position),
		)
		playerVisible, companionVisible := false, false
		if observer != nil {
			lastPlayers = len(observer.visiblePlayers)
			lastCompanions = len(observer.visibleCompanions)
			lastSnapshots = 0
			for _, publication := range observer.publications {
				if publication.snapshotSent {
					lastSnapshots++
				}
			}
			_, playerVisible = observer.visiblePlayers[playerID]
			_, companionVisible = observer.visibleCompanions[companionID]
		}
		host.world.stepMu.Unlock()
		if len(bodies) == companion.MaxActive && playerVisible && companionVisible {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf(
		"未观察到独立可见容量：bodies=%d worldSessions=%d observer=%t visiblePlayers=%d visibleCompanions=%d snapshots=%d playerWanted=%t companionWanted=%t observerUpdate=%+v playerUpdate=%+v companionBody=%+v",
		lastBodies, lastSessions, lastObserver, lastPlayers, lastCompanions, lastSnapshots,
		lastPlayerWanted, lastCompanionWanted, lastObserverUpdate, lastPlayerUpdate, lastCompanionBody,
	)
}

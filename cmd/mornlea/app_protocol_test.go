//go:build darwin

package main

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
)

func TestPerformanceRecordersOnlyEnableSaveSamplingForBenchmark(t *testing.T) {
	interactiveTicks, interactiveSaves := newPerformanceRecorders(false)
	if interactiveTicks == nil || interactiveSaves != nil {
		t.Fatalf("交互模式 recorders ticks=%v saves=%v，想要 tick recorder 且无 save recorder",
			interactiveTicks, interactiveSaves)
	}

	benchmarkTicks, benchmarkSaves := newPerformanceRecorders(true)
	if benchmarkTicks == nil || benchmarkSaves == nil {
		t.Fatalf("benchmark recorders ticks=%v saves=%v，想要两者都有",
			benchmarkTicks, benchmarkSaves)
	}
}

func TestProtocolV19ClientIsCurrent(t *testing.T) {
	if network.ProtocolVersion != 19 {
		t.Fatalf("客户端协议版本 = %d，想要 19", network.ProtocolVersion)
	}
}

// Mutation killed: routing any remote-player message through Mirror closes the
// endpoint instead of completing the spawn/state/despawn roster lifecycle.
func TestRemoteMessagesRouteOnlyToRoster(t *testing.T) {
	app, serverEndpoint, endpoint, _ := newRemoteProtocolApplication(t)
	spawn := remoteSpawn(2, "Remote-2", 1, mgl32.Vec3{1, 64, 3})
	sendInteractiveServerMessage(t, serverEndpoint, spawn)
	app.drainServerMessages(1)
	got := app.remotePlayers.Presentations()
	if len(got) != 1 || got[0].PlayerID != spawn.PlayerID || got[0].DisplayName != spawn.DisplayName {
		t.Fatalf("spawn presentations=%+v", got)
	}
	states := network.RemotePlayerStates{ServerTick: 2, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld,
		Position: mgl32.Vec3{9, 65, -4}, Yaw: 0.7, Pitch: -0.2,
	}}}
	sendInteractiveServerMessage(t, serverEndpoint, states)
	app.drainServerMessages(1)
	got = app.remotePlayers.Presentations()
	if len(got) != 1 || got[0].Position != states.Players[0].Position || got[0].Yaw != 0.7 || got[0].Pitch != -0.2 {
		t.Fatalf("state presentations=%+v", got)
	}
	sendInteractiveServerMessage(t, serverEndpoint, network.RemotePlayerDespawn{PlayerID: spawn.PlayerID})
	app.drainServerMessages(1)
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("despawn roster=%d", got)
	}
	if got := endpoint.closeCalls.Load(); got != 0 {
		t.Fatalf("valid remote lifecycle closed endpoint %d times", got)
	}
}

// Mutation killed: calling serverCancel, leaving the endpoint open, or failing
// to reset the roster on a duplicate Spawn violates client-session isolation.
func TestRemoteProtocolErrorClosesOnlyClientEndpoint(t *testing.T) {
	app, serverEndpoint, endpoint, cancelCount := newRemoteProtocolApplication(t)
	spawn := remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{0, 64, 0})
	sendInteractiveServerMessage(t, serverEndpoint, spawn)
	sendInteractiveServerMessage(t, serverEndpoint, spawn)
	app.drainServerMessages(1)
	if got := len(app.remotePlayers.Presentations()); got != 1 {
		t.Fatalf("roster after valid spawn=%d", got)
	}
	if got := endpoint.closeCalls.Load(); got != 0 {
		t.Fatalf("first valid spawn closed endpoint %d times", got)
	}
	app.drainServerMessages(1)
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("protocol close count=%d", got)
	}
	if got := cancelCount(); got != 0 {
		t.Fatalf("server cancel count=%d", got)
	}
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("roster after protocol close=%d", got)
	}
}

// Mutation killed: observing a transport close without invoking the session
// cleanup leaves stale remote players visible in the disconnected world.
func TestRemoteConnectionCloseResetsRoster(t *testing.T) {
	app, serverEndpoint, endpoint, cancelCount := newRemoteProtocolApplication(t)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	if err := serverEndpoint.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for app.receiver.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := app.frame(0, 0, 0); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("frame after disconnect error=%v want network.ErrClosed", err)
	}
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("roster after disconnect=%d", got)
	}
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("disconnect endpoint Close calls=%d", got)
	}
	if got := cancelCount(); got != 0 {
		t.Fatalf("disconnect server cancel calls=%d", got)
	}
}

// Mutation killed: Advance before drain, a missing Advance, or two Advances
// produces position 8, 8, or 4 instead of the hand-derived midpoint 2.
func TestFrameAdvancesRemotePlayersOnceAfterDrain(t *testing.T) {
	app, serverEndpoint, _, _ := newRemoteProtocolApplication(t)
	spawn := remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{0, 64, 0})
	if err := app.remotePlayers.Apply(spawn); err != nil {
		t.Fatal(err)
	}
	if err := app.remotePlayers.Apply(network.RemotePlayerStates{ServerTick: 2, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{4, 64, 0},
	}}}); err != nil {
		t.Fatal(err)
	}
	sendInteractiveServerMessage(t, serverEndpoint, network.RemotePlayerStates{ServerTick: 3, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{8, 64, 0},
	}}})
	rendered, err := app.frame(1, 1, 25*time.Millisecond)
	if err != nil || rendered {
		t.Fatalf("frame=(%v,%v), want (false,nil) for zero framebuffer", rendered, err)
	}
	if got := app.remotePlayers.Presentations()[0].Position; got != (mgl32.Vec3{2, 64, 0}) {
		t.Fatalf("advanced position=%v want [2 64 0]", got)
	}
}

func TestFrameKeepsMesherWorkBoundIndependentFromMessageDrain(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		sections[index] = network.SectionData{
			Y: int32(index), Storage: network.SectionSingle, Single: core.AirID,
		}
	}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			if _, err := app.mirror.Apply(network.ChunkSnapshot{
				Dimension: core.Overworld,
				Chunk:     core.ChunkPos{X: x, Z: z},
				Revision:  1,
				Sections:  sections,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{Y: 0}}
	second := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{Y: 1}}
	release := app.mesher.BlockForTest(first)
	t.Cleanup(release)
	app.mesher.MarkDirty(first, second)

	rendered, err := app.frame(4096, 1, 0)
	if err != nil || !rendered {
		t.Fatalf("frame=(%v,%v)", rendered, err)
	}
	stats := app.mesher.Stats()
	if scheduled := stats.QueuedJobs + stats.InFlightJobs; scheduled != 1 {
		t.Fatalf("drain=4096 mesh=1 scheduled=%d stats=%+v", scheduled, stats)
	}
}

// Mutation killed: dropping identity/motion/name fields, sorting by input
// order, or anchoring at the feet changes these literal render values.
func TestRemotePresentationConversionPreservesSortedRenderData(t *testing.T) {
	presentations := []client.RemotePresentation{
		{PlayerID: integrationPlayerID(2), DisplayName: "乙", Position: mgl32.Vec3{8, 9, 10}, Yaw: 0.8, Pitch: -0.3},
		{PlayerID: integrationPlayerID(1), DisplayName: "甲", Position: mgl32.Vec3{1, 2, 3}, Yaw: -0.4, Pitch: 0.2},
	}
	avatars, tags := remoteRenderPresentations(presentations)
	wantAvatars := []render.Avatar{
		{Key: render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(integrationPlayerID(1))}, Position: mgl32.Vec3{1, 2, 3}, Yaw: -0.4, Pitch: 0.2},
		{Key: render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(integrationPlayerID(2))}, Position: mgl32.Vec3{8, 9, 10}, Yaw: 0.8, Pitch: -0.3},
	}
	wantTags := []render.NameTag{
		{Key: render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(integrationPlayerID(1))}, Text: "甲", Anchor: mgl32.Vec3{1, 4.05, 3}},
		{Key: render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(integrationPlayerID(2))}, Text: "乙", Anchor: mgl32.Vec3{8, 11.05, 10}},
	}
	if !reflect.DeepEqual(avatars, wantAvatars) || !reflect.DeepEqual(tags, wantTags) {
		t.Fatalf("converted avatars/tags=%+v/%+v want=%+v/%+v", avatars, tags, wantAvatars, wantTags)
	}
}
func newRemoteProtocolApplication(t *testing.T) (*application, network.ServerEndpoint, *connectionTestEndpoint, func() int) {
	t.Helper()
	rawClient, serverEndpoint := network.NewMemoryPair(16)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawClient}
	cancelCalls := 0
	app := &application{
		clientEndpoint: endpoint, receiver: client.NewReceiver(endpoint, 16),
		mirror: client.NewMirror(), predictor: client.NewPredictor(),
		remotePlayers: client.NewRemotePlayers(), serverCancel: func() { cancelCalls++ },
	}
	t.Cleanup(func() { app.closeClientSession(nil); _ = serverEndpoint.Close() })
	return app, serverEndpoint, endpoint, func() int { return cancelCalls }
}

func remoteSpawn(id byte, name string, tick uint64, position mgl32.Vec3) network.RemotePlayerSpawn {
	return network.RemotePlayerSpawn{PlayerID: integrationPlayerID(id), DisplayName: name, ServerTick: tick, Dimension: core.Overworld, Position: position}
}

func integrationPlayerID(last byte) core.PlayerID {
	return core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: last}
}

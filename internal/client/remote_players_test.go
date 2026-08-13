package client_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestRemotePlayersSpawnCreatesPresentation(t *testing.T) {
	players := client.NewRemotePlayers()
	spawn := remotePlayerSpawn(1, 7, "Chen", mgl32.Vec3{1, 2, 3})
	spawn.Yaw = 0.5
	spawn.Pitch = -0.25

	if err := players.Apply(spawn); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}
	want := []client.RemotePresentation{{
		PlayerID:    spawn.PlayerID,
		DisplayName: "Chen",
		Dimension:   core.Overworld,
		Position:    mgl32.Vec3{1, 2, 3},
		Yaw:         0.5,
		Pitch:       -0.25,
	}}
	if got := players.Presentations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Presentations() = %+v, want %+v", got, want)
	}
}

func TestRemotePlayersRejectsDuplicateSpawnWithoutOverwrite(t *testing.T) {
	players := client.NewRemotePlayers()
	first := remotePlayerSpawn(1, 7, "First", mgl32.Vec3{1, 2, 3})
	if err := players.Apply(first); err != nil {
		t.Fatalf("Apply first spawn: %v", err)
	}
	duplicate := remotePlayerSpawn(1, 8, "Replacement", mgl32.Vec3{9, 9, 9})
	err := players.Apply(duplicate)
	assertRemotePlayerProtocolError(t, err, "RemotePlayerSpawn", first.PlayerID.String(), "8")

	want := []client.RemotePresentation{{
		PlayerID: first.PlayerID, DisplayName: "First", Dimension: core.Overworld,
		Position: mgl32.Vec3{1, 2, 3},
	}}
	if got := players.Presentations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicate spawn changed roster: got %+v, want %+v", got, want)
	}
}

func TestRemotePlayersRejectsInvalidSpawnWithoutMutation(t *testing.T) {
	valid := remotePlayerSpawn(1, 7, "Chen", mgl32.Vec3{1, 2, 3})
	tests := []struct {
		name  string
		spawn network.RemotePlayerSpawn
	}{
		{name: "non-v4 player ID", spawn: func() network.RemotePlayerSpawn {
			message := valid
			message.PlayerID = core.PlayerID{1}
			return message
		}()},
		{name: "noncanonical display name", spawn: func() network.RemotePlayerSpawn {
			message := valid
			message.DisplayName = " Chen "
			return message
		}()},
		{name: "invalid dimension", spawn: func() network.RemotePlayerSpawn {
			message := valid
			message.Dimension = core.DimensionID(1)
			return message
		}()},
		{name: "nonfinite position", spawn: func() network.RemotePlayerSpawn {
			message := valid
			message.Position[0] = float32(math.NaN())
			return message
		}()},
		{name: "nonfinite yaw", spawn: func() network.RemotePlayerSpawn {
			message := valid
			message.Yaw = float32(math.Inf(1))
			return message
		}()},
		{name: "nonfinite pitch", spawn: func() network.RemotePlayerSpawn {
			message := valid
			message.Pitch = float32(math.Inf(-1))
			return message
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			players := client.NewRemotePlayers()
			err := players.Apply(test.spawn)
			assertRemotePlayerProtocolError(t, err, "RemotePlayerSpawn", test.spawn.PlayerID.String(), "7")
			if got := players.Presentations(); len(got) != 0 {
				t.Fatalf("invalid spawn changed roster: %+v", got)
			}
		})
	}
}

func TestRemotePlayersDespawnRemovesKnownPlayerAndRejectsUnknown(t *testing.T) {
	players := client.NewRemotePlayers()
	spawn := remotePlayerSpawn(1, 7, "Chen", mgl32.Vec3{1, 2, 3})
	if err := players.Apply(spawn); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}

	unknownID := remotePlayerTestID(2)
	err := players.Apply(network.RemotePlayerDespawn{PlayerID: unknownID})
	assertRemotePlayerProtocolError(t, err, "RemotePlayerDespawn", unknownID.String())
	if got := players.Presentations(); len(got) != 1 || got[0].PlayerID != spawn.PlayerID {
		t.Fatalf("unknown despawn changed roster: %+v", got)
	}

	if err := players.Apply(network.RemotePlayerDespawn{PlayerID: spawn.PlayerID}); err != nil {
		t.Fatalf("Apply known despawn: %v", err)
	}
	if got := players.Presentations(); len(got) != 0 {
		t.Fatalf("known despawn left roster entries: %+v", got)
	}
}

func TestRemotePlayersRejectsInvalidDespawnWithoutMutation(t *testing.T) {
	players := client.NewRemotePlayers()
	spawn := remotePlayerSpawn(1, 7, "Chen", mgl32.Vec3{1, 2, 3})
	if err := players.Apply(spawn); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}

	err := players.Apply(network.RemotePlayerDespawn{PlayerID: core.PlayerID{1}})
	assertRemotePlayerProtocolError(t, err, "RemotePlayerDespawn")
	if got := players.Presentations(); len(got) != 1 || got[0].PlayerID != spawn.PlayerID {
		t.Fatalf("invalid despawn changed roster: %+v", got)
	}
}

func TestRemotePlayersRejectsUnknownStateAtomically(t *testing.T) {
	players := client.NewRemotePlayers()
	first := remotePlayerSpawn(1, 1, "First", mgl32.Vec3{1, 0, 0})
	if err := players.Apply(first); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}
	unknownID := remotePlayerTestID(2)
	err := players.Apply(network.RemotePlayerStates{
		ServerTick: 2,
		Players: []network.RemotePlayerState{
			{PlayerID: first.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{10, 0, 0}},
			{PlayerID: unknownID, Dimension: core.Overworld, Position: mgl32.Vec3{20, 0, 0}},
		},
	})
	assertRemotePlayerProtocolError(t, err, "RemotePlayerStates", unknownID.String(), "2")
	if got := players.Presentations(); len(got) != 1 || got[0].Position != (mgl32.Vec3{1, 0, 0}) {
		t.Fatalf("unknown state partially changed roster: %+v", got)
	}
}

func TestRemotePlayersRejectsNonIncreasingStateTickAtomically(t *testing.T) {
	for _, serverTick := range []uint64{7, 6} {
		t.Run(string(rune('0'+serverTick)), func(t *testing.T) {
			players := client.NewRemotePlayers()
			first := remotePlayerSpawn(1, 1, "First", mgl32.Vec3{1, 0, 0})
			second := remotePlayerSpawn(2, 7, "Second", mgl32.Vec3{2, 0, 0})
			for _, spawn := range []network.RemotePlayerSpawn{first, second} {
				if err := players.Apply(spawn); err != nil {
					t.Fatalf("Apply spawn: %v", err)
				}
			}

			err := players.Apply(network.RemotePlayerStates{
				ServerTick: serverTick,
				Players: []network.RemotePlayerState{
					{PlayerID: first.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{10, 0, 0}},
					{PlayerID: second.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{20, 0, 0}},
				},
			})
			assertRemotePlayerProtocolError(t, err, "RemotePlayerStates", second.PlayerID.String())
			got := players.Presentations()
			if len(got) != 2 || got[0].Position != first.Position || got[1].Position != second.Position {
				t.Fatalf("stale batch partially changed roster: %+v", got)
			}
		})
	}
}

func TestRemotePlayersRejectsInvalidStatesWithoutMutation(t *testing.T) {
	validID := remotePlayerTestID(1)
	tests := []struct {
		name  string
		state network.RemotePlayerState
	}{
		{name: "non-v4 player ID", state: network.RemotePlayerState{PlayerID: core.PlayerID{1}, Dimension: core.Overworld}},
		{name: "invalid dimension", state: network.RemotePlayerState{PlayerID: validID, Dimension: core.DimensionID(1)}},
		{name: "nonfinite position", state: network.RemotePlayerState{PlayerID: validID, Dimension: core.Overworld, Position: mgl32.Vec3{0, float32(math.NaN()), 0}}},
		{name: "nonfinite yaw", state: network.RemotePlayerState{PlayerID: validID, Dimension: core.Overworld, Yaw: float32(math.Inf(1))}},
		{name: "nonfinite pitch", state: network.RemotePlayerState{PlayerID: validID, Dimension: core.Overworld, Pitch: float32(math.NaN())}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			players := client.NewRemotePlayers()
			spawn := remotePlayerSpawn(1, 1, "Chen", mgl32.Vec3{1, 2, 3})
			if err := players.Apply(spawn); err != nil {
				t.Fatalf("Apply spawn: %v", err)
			}
			err := players.Apply(network.RemotePlayerStates{ServerTick: 2, Players: []network.RemotePlayerState{test.state}})
			assertRemotePlayerProtocolError(t, err, "RemotePlayerStates", test.state.PlayerID.String(), "2")
			got := players.Presentations()
			if len(got) != 1 || got[0].Position != spawn.Position {
				t.Fatalf("invalid state changed roster: %+v", got)
			}
		})
	}
}

func TestRemotePlayersRejectsMalformedStateBatchWithoutMutation(t *testing.T) {
	players := client.NewRemotePlayers()
	spawn := remotePlayerSpawn(1, 1, "Chen", mgl32.Vec3{1, 2, 3})
	if err := players.Apply(spawn); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}
	tests := []network.RemotePlayerStates{
		{ServerTick: 2},
		{ServerTick: 2, Players: []network.RemotePlayerState{
			{PlayerID: spawn.PlayerID, Dimension: core.Overworld},
			{PlayerID: spawn.PlayerID, Dimension: core.Overworld},
		}},
	}
	for _, message := range tests {
		err := players.Apply(message)
		assertRemotePlayerProtocolError(t, err, "RemotePlayerStates", "2")
		got := players.Presentations()
		if len(got) != 1 || got[0].Position != spawn.Position {
			t.Fatalf("malformed batch changed roster: %+v", got)
		}
	}
}

func TestRemotePlayersStatesUpdatesMultipleKnownPlayers(t *testing.T) {
	players := client.NewRemotePlayers()
	first := remotePlayerSpawn(1, 1, "First", mgl32.Vec3{1, 0, 0})
	second := remotePlayerSpawn(2, 2, "Second", mgl32.Vec3{2, 0, 0})
	for _, spawn := range []network.RemotePlayerSpawn{first, second} {
		if err := players.Apply(spawn); err != nil {
			t.Fatalf("Apply spawn: %v", err)
		}
	}
	if err := players.Apply(network.RemotePlayerStates{
		ServerTick: 3,
		Players: []network.RemotePlayerState{
			{PlayerID: first.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{10, 11, 12}, Yaw: 0.1, Pitch: 0.2},
			{PlayerID: second.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{20, 21, 22}, Yaw: 0.3, Pitch: 0.4},
		},
	}); err != nil {
		t.Fatalf("Apply states: %v", err)
	}
	want := []client.RemotePresentation{
		{PlayerID: first.PlayerID, DisplayName: "First", Dimension: core.Overworld, Position: mgl32.Vec3{10, 11, 12}, Yaw: 0.1, Pitch: 0.2},
		{PlayerID: second.PlayerID, DisplayName: "Second", Dimension: core.Overworld, Position: mgl32.Vec3{20, 21, 22}, Yaw: 0.3, Pitch: 0.4},
	}
	if got := players.Presentations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Presentations() = %+v, want %+v", got, want)
	}
}

func TestRemotePlayersAcceptsRemoteMessagePointersAndRejectsNilPointers(t *testing.T) {
	players := client.NewRemotePlayers()
	spawn := remotePlayerSpawn(1, 1, "Chen", mgl32.Vec3{1, 2, 3})
	if err := players.Apply(&spawn); err != nil {
		t.Fatalf("Apply spawn pointer: %v", err)
	}
	states := network.RemotePlayerStates{ServerTick: 2, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{4, 5, 6},
	}}}
	if err := players.Apply(&states); err != nil {
		t.Fatalf("Apply states pointer: %v", err)
	}
	despawn := network.RemotePlayerDespawn{PlayerID: spawn.PlayerID}
	if err := players.Apply(&despawn); err != nil {
		t.Fatalf("Apply despawn pointer: %v", err)
	}

	var nilSpawn *network.RemotePlayerSpawn
	assertRemotePlayerProtocolError(t, players.Apply(nilSpawn), "RemotePlayerSpawn")
}

func TestRemotePlayersPresentationsAreSortedCopies(t *testing.T) {
	players := client.NewRemotePlayers()
	for _, last := range []byte{3, 1, 2} {
		if err := players.Apply(remotePlayerSpawn(last, 1, string(rune('A'+last)), mgl32.Vec3{float32(last), 0, 0})); err != nil {
			t.Fatalf("Apply spawn %d: %v", last, err)
		}
	}
	got := players.Presentations()
	if len(got) != 3 || got[0].PlayerID != remotePlayerTestID(1) || got[1].PlayerID != remotePlayerTestID(2) || got[2].PlayerID != remotePlayerTestID(3) {
		t.Fatalf("Presentations() order = %+v", got)
	}
	got[0].DisplayName = "mutated"
	got[0].Position[0] = 99
	again := players.Presentations()
	if again[0].DisplayName == "mutated" || again[0].Position[0] == 99 {
		t.Fatalf("Presentations returned aliased state: %+v", again)
	}
}

func TestRemotePlayersResetClearsRoster(t *testing.T) {
	players := client.NewRemotePlayers()
	if err := players.Apply(remotePlayerSpawn(1, 1, "Chen", mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}
	players.Reset()
	if got := players.Presentations(); len(got) != 0 {
		t.Fatalf("Presentations after Reset = %+v", got)
	}
}

func TestRemotePlayersRejectsNonRemoteMessages(t *testing.T) {
	players := client.NewRemotePlayers()
	for _, message := range []network.ServerMessage{network.PlayerState{}, nil} {
		err := players.Apply(message)
		assertRemotePlayerProtocolError(t, err)
	}
}

func remotePlayerSpawn(last byte, tick uint64, name string, position mgl32.Vec3) network.RemotePlayerSpawn {
	return network.RemotePlayerSpawn{
		PlayerID:    remotePlayerTestID(last),
		DisplayName: name,
		ServerTick:  tick,
		Dimension:   core.Overworld,
		Position:    position,
	}
}

func remotePlayerTestID(last byte) core.PlayerID {
	return core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, last}
}

func assertRemotePlayerProtocolError(t *testing.T, err error, parts ...string) {
	t.Helper()
	if !errors.Is(err, client.ErrRemotePlayerProtocol) {
		t.Fatalf("error = %v, want ErrRemotePlayerProtocol", err)
	}
	for _, part := range parts {
		if !strings.Contains(err.Error(), part) {
			t.Fatalf("error = %q, want it to contain %q", err, part)
		}
	}
}

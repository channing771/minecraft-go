package client

import (
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func TestRemotePlayersInterpolatesTwoTicksBehind(t *testing.T) {
	players, _ := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}, yaw: -0.2, pitch: -0.3},
		{tick: 11, position: mgl32.Vec3{1, 2, 3}, yaw: -0.1, pitch: -0.1},
		{tick: 12, position: mgl32.Vec3{2, 4, 6}, yaw: 0, pitch: 0.1},
		{tick: 13, position: mgl32.Vec3{3, 6, 9}, yaw: 0.1, pitch: 0.3},
	})

	players.Advance(25 * time.Millisecond)
	got := onlyRemotePresentation(t, players)
	assertRemoteVec3Close(t, got.Position, mgl32.Vec3{1.5, 3, 4.5})
	assertRemoteFloatClose(t, got.Yaw, -0.05)
	assertRemoteFloatClose(t, got.Pitch, 0)
}

func TestRemotePlayersInterpolatesYawAcrossPiByShortestArc(t *testing.T) {
	players, _ := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, yaw: 0},
		{tick: 11, yaw: math.Pi - 0.1},
		{tick: 12, yaw: -math.Pi + 0.1},
		{tick: 13, yaw: -math.Pi + 0.2},
	})

	players.Advance(25 * time.Millisecond)
	got := onlyRemotePresentation(t, players)
	if distance := math.Abs(float64(normalizeRemoteAngle(got.Yaw - math.Pi))); distance > 1e-5 {
		t.Fatalf("Yaw = %v, want the shortest arc midpoint at pi (distance %v)", got.Yaw, distance)
	}
}

func TestRemotePlayersHoldsLatestUntilThreeSnapshotsThenWarmsUp(t *testing.T) {
	players, playerID := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}},
		{tick: 11, position: mgl32.Vec3{1, 0, 0}},
	})
	players.Advance(25 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{1, 0, 0})

	applyInterpolationState(t, players, playerID, remoteSnapshot{
		tick: 12, position: mgl32.Vec3{2, 0, 0},
	}, false)
	players.Advance(25 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{0.5, 0, 0})
}

func TestRemotePlayersBoundsSnapshotsWithoutExtrapolating(t *testing.T) {
	var ring remoteSnapshotRing
	for _, snapshot := range []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{1, 0, 0}},
		{tick: 20, position: mgl32.Vec3{2, 0, 0}},
		{tick: 30, position: mgl32.Vec3{3, 0, 0}},
	} {
		ring.append(snapshot)
	}

	early := ring.sample(5)
	late := ring.sample(35)
	if early.tick != 10 || early.position != (mgl32.Vec3{1, 0, 0}) {
		t.Fatalf("sample before oldest = %+v, want tick 10 endpoint", early)
	}
	if late.tick != 30 || late.position != (mgl32.Vec3{3, 0, 0}) {
		t.Fatalf("sample after latest = %+v, want tick 30 endpoint", late)
	}
}

func TestRemotePlayersBoundsSnapshotsToLatestFour(t *testing.T) {
	var ring remoteSnapshotRing
	for tick := uint64(10); tick <= 14; tick++ {
		ring.append(remoteSnapshot{tick: tick, position: mgl32.Vec3{float32(tick), 0, 0}})
	}

	if ring.count != remoteSnapshotCapacity {
		t.Fatalf("snapshot count = %d, want %d", ring.count, remoteSnapshotCapacity)
	}
	oldest := ring.sample(0)
	if oldest.tick != 11 || oldest.position[0] != 11 {
		t.Fatalf("oldest retained snapshot = %+v, want tick 11", oldest)
	}
}

func TestRemotePlayersResetsInterpolationOnAuthoritativeDiscontinuity(t *testing.T) {
	tests := []struct {
		name     string
		snapshot remoteSnapshot
		reset    bool
	}{
		{
			name:     "reset flag",
			snapshot: remoteSnapshot{tick: 13, position: mgl32.Vec3{3, 0, 0}},
			reset:    true,
		},
		{
			name:     "distance strictly over eight",
			snapshot: remoteSnapshot{tick: 13, position: mgl32.Vec3{10.01, 0, 0}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			players, playerID := seededInterpolationPlayers(t, []remoteSnapshot{
				{tick: 10, position: mgl32.Vec3{0, 0, 0}},
				{tick: 11, position: mgl32.Vec3{1, 0, 0}},
				{tick: 12, position: mgl32.Vec3{2, 0, 0}},
			})
			applyInterpolationState(t, players, playerID, test.snapshot, test.reset)
			players.Advance(25 * time.Millisecond)
			assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, test.snapshot.position)
		})
	}
}

func TestRemotePlayersResetsInterpolationOnDimensionChange(t *testing.T) {
	player := &remotePlayer{}
	player.pushSnapshot(remoteSnapshot{tick: 10, dimension: core.Overworld}, false)
	player.pushSnapshot(remoteSnapshot{tick: 11, dimension: core.Overworld}, false)
	player.pushSnapshot(remoteSnapshot{tick: 12, dimension: core.DimensionID(1)}, false)

	if player.snapshots.count != 1 {
		t.Fatalf("snapshot count after dimension change = %d, want 1", player.snapshots.count)
	}
	if got := player.snapshots.latest(); got.tick != 12 || got.dimension != core.DimensionID(1) {
		t.Fatalf("latest after dimension change = %+v", got)
	}
}

func TestRemotePlayersHoldsHistoryAtExactlyEightBlocks(t *testing.T) {
	players, playerID := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}},
		{tick: 11, position: mgl32.Vec3{1, 0, 0}},
		{tick: 12, position: mgl32.Vec3{2, 0, 0}},
	})
	applyInterpolationState(t, players, playerID, remoteSnapshot{
		tick: 13, position: mgl32.Vec3{10, 0, 0},
	}, false)

	players.Advance(25 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{1.5, 0, 0})
}

func TestRemotePlayersHoldsTickGapsWithoutResettingHistory(t *testing.T) {
	players, _ := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}},
		{tick: 20, position: mgl32.Vec3{1, 0, 0}},
		{tick: 30, position: mgl32.Vec3{2, 0, 0}},
	})

	players.Advance(0)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{1.8, 0, 0})
}

func TestRemotePlayersAdvanceAccumulatesAndClampsElapsed(t *testing.T) {
	players, playerID := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}},
		{tick: 11, position: mgl32.Vec3{1, 0, 0}},
		{tick: 12, position: mgl32.Vec3{2, 0, 0}},
		{tick: 13, position: mgl32.Vec3{3, 0, 0}},
	})

	players.Advance(25 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{1.5, 0, 0})
	players.Advance(25 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{2, 0, 0})
	players.Advance(time.Hour)
	players.Advance(-time.Hour)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{2, 0, 0})

	applyInterpolationState(t, players, playerID, remoteSnapshot{
		tick: 14, position: mgl32.Vec3{4, 0, 0},
	}, false)
	players.Advance(25 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{2.5, 0, 0})
}

func TestRemotePlayersWarmsUpAgainAfterDespawnAndSpawn(t *testing.T) {
	players, playerID := seededInterpolationPlayers(t, []remoteSnapshot{
		{tick: 10, position: mgl32.Vec3{0, 0, 0}},
		{tick: 11, position: mgl32.Vec3{1, 0, 0}},
		{tick: 12, position: mgl32.Vec3{2, 0, 0}},
		{tick: 13, position: mgl32.Vec3{3, 0, 0}},
	})
	if err := players.Apply(network.RemotePlayerDespawn{PlayerID: playerID}); err != nil {
		t.Fatalf("Apply despawn: %v", err)
	}
	if err := players.Apply(network.RemotePlayerSpawn{
		PlayerID: playerID, DisplayName: "Remote", ServerTick: 20,
		Dimension: core.Overworld, Position: mgl32.Vec3{20, 0, 0},
	}); err != nil {
		t.Fatalf("Apply respawn: %v", err)
	}
	applyInterpolationState(t, players, playerID, remoteSnapshot{
		tick: 21, position: mgl32.Vec3{21, 0, 0},
	}, false)

	players.Advance(50 * time.Millisecond)
	assertRemoteVec3Close(t, onlyRemotePresentation(t, players).Position, mgl32.Vec3{21, 0, 0})
}

func seededInterpolationPlayers(t *testing.T, snapshots []remoteSnapshot) (*RemotePlayers, core.PlayerID) {
	t.Helper()
	if len(snapshots) == 0 {
		t.Fatal("seededInterpolationPlayers requires at least one snapshot")
	}
	playerID := interpolationPlayerID(1)
	players := NewRemotePlayers()
	first := snapshots[0]
	if err := players.Apply(network.RemotePlayerSpawn{
		PlayerID: playerID, DisplayName: "Remote", ServerTick: first.tick,
		Dimension: first.dimension, Position: first.position, Yaw: first.yaw, Pitch: first.pitch,
	}); err != nil {
		t.Fatalf("Apply spawn: %v", err)
	}
	for _, snapshot := range snapshots[1:] {
		applyInterpolationState(t, players, playerID, snapshot, false)
	}
	return players, playerID
}

func applyInterpolationState(
	t *testing.T,
	players *RemotePlayers,
	playerID core.PlayerID,
	snapshot remoteSnapshot,
	reset bool,
) {
	t.Helper()
	if err := players.Apply(network.RemotePlayerStates{
		ServerTick: snapshot.tick,
		Players: []network.RemotePlayerState{{
			PlayerID: playerID, Dimension: snapshot.dimension, Position: snapshot.position,
			Yaw: snapshot.yaw, Pitch: snapshot.pitch, Reset: reset,
		}},
	}); err != nil {
		t.Fatalf("Apply state tick %d: %v", snapshot.tick, err)
	}
}

func onlyRemotePresentation(t *testing.T, players *RemotePlayers) RemotePresentation {
	t.Helper()
	presentations := players.Presentations()
	if len(presentations) != 1 {
		t.Fatalf("presentation count = %d, want 1", len(presentations))
	}
	return presentations[0]
}

func interpolationPlayerID(last byte) core.PlayerID {
	return core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, last}
}

func assertRemoteVec3Close(t *testing.T, got, want mgl32.Vec3) {
	t.Helper()
	for index := range got {
		assertRemoteFloatClose(t, got[index], want[index])
	}
}

func assertRemoteFloatClose(t *testing.T, got, want float32) {
	t.Helper()
	if difference := math.Abs(float64(got - want)); difference > 1e-5 {
		t.Fatalf("value = %v, want %v (difference %v)", got, want, difference)
	}
}

package network_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func TestProtocolMessageShapesImplementSealedInterfaces(t *testing.T) {
	clientMessages := []network.ClientMessage{
		network.PlayerInput{
			Sequence: 1,
			MoveX:    -1,
			MoveZ:    1,
			Jump:     true,
			Yaw:      90,
			Pitch:    -15,
		},
		network.BreakBlock{Sequence: 2, Yaw: 90, Pitch: -15},
		network.PlaceBlock{
			Sequence: 3,
			Yaw:      90,
			Pitch:    -15,
			Block:    core.StoneID,
		},
		network.RequestChunkResync{
			Sequence:     4,
			Dimension:    core.Overworld,
			Chunk:        core.ChunkPos{X: 2, Z: -3},
			HaveRevision: 7,
		},
		network.KeepAliveReply{Token: 1},
	}
	serverMessages := []network.ServerMessage{
		network.ChunkSnapshot{},
		network.BlockChanges{},
		network.ForgetChunks{},
		network.CommandRejected{
			Sequence: 4,
			Reason:   network.RejectInvalidRay,
		},
		network.PlayerState{
			ServerTick:        8,
			LastInputSequence: 7,
			Dimension:         core.Overworld,
			Position:          mgl32.Vec3{1, 2, 3},
			Velocity:          mgl32.Vec3{4, 5, 6},
			Yaw:               90,
			Pitch:             -15,
			OnGround:          true,
			Ready:             true,
			Reset:             true,
		},
		network.RemotePlayerSpawn{PlayerID: core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}, DisplayName: "Chen"},
		network.RemotePlayerDespawn{PlayerID: core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}},
		network.RemotePlayerStates{Players: []network.RemotePlayerState{{PlayerID: core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}}}},
		network.KeepAlive{Token: 1},
		network.Disconnect{Code: network.DisconnectTimeout},
	}
	if len(clientMessages) != 5 || len(serverMessages) != 10 {
		t.Fatal("消息集合不完整")
	}
}

func TestRejectReasonsAreStableProtocolValues(t *testing.T) {
	tests := []struct {
		got  network.RejectReason
		want string
	}{
		{network.RejectInvalidRay, "invalid_ray"},
		{network.RejectNoTarget, "no_target"},
		{network.RejectChunkNotReady, "chunk_not_ready"},
		{network.RejectProtectedBlock, "protected_block"},
		{network.RejectInvalidBlock, "invalid_block"},
		{network.RejectOccupied, "occupied"},
		{network.RejectInvalidInput, "invalid_input"},
		{network.RejectPlayerNotReady, "player_not_ready"},
	}
	for _, tc := range tests {
		if string(tc.got) != tc.want {
			t.Fatalf("reject reason = %q，想要 %q", tc.got, tc.want)
		}
	}
}

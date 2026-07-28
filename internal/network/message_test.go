package network_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

func TestProtocolMessageShapesImplementSealedInterfaces(t *testing.T) {
	clientMessages := []network.ClientMessage{
		network.SetViewCenter{
			Sequence:  1,
			Dimension: core.Overworld,
			Center:    core.ChunkPos{X: 2, Z: -3},
		},
		network.BreakRay{
			Sequence:  2,
			Dimension: core.Overworld,
			Origin:    mgl32.Vec3{1, 2, 3},
			Direction: mgl32.Vec3{0, -1, 0},
		},
		network.PlaceRay{
			Sequence:  3,
			Dimension: core.Overworld,
			Origin:    mgl32.Vec3{1, 2, 3},
			Direction: mgl32.Vec3{0, -1, 0},
			Block:     core.StoneID,
		},
		network.PlayerInput{
			Sequence: 4,
			MoveX:    -1,
			MoveZ:    1,
			Jump:     true,
			Yaw:      90,
			Pitch:    -15,
		},
		network.BreakBlock{Sequence: 5, Yaw: 90, Pitch: -15},
		network.PlaceBlock{
			Sequence: 6,
			Yaw:      90,
			Pitch:    -15,
			Block:    core.StoneID,
		},
		network.RequestChunkResync{
			Sequence:     7,
			Dimension:    core.Overworld,
			Chunk:        core.ChunkPos{X: 2, Z: -3},
			HaveRevision: 7,
		},
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
	}
	if len(clientMessages) != 7 || len(serverMessages) != 5 {
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

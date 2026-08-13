package network_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
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
			Mining:   true,
		},
		network.PlaceBlock{
			Sequence: 3,
			Yaw:      90,
			Pitch:    -15,
			Slot:     4,
		},
		network.SelectHotbar{Sequence: 9, Slot: 8},
		network.MoveInventoryStack{Sequence: 10, From: 0, To: 35},
		network.CraftRecipe{Sequence: 11, Recipe: core.RecipeStoneBricks},
		network.RequestChunkResync{
			Sequence:     4,
			Dimension:    core.Overworld,
			Chunk:        core.ChunkPos{X: 2, Z: -3},
			HaveRevision: 7,
		},
		network.KeepAliveReply{Token: 1},
		network.ChatCommand{Text: "@A x"},
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
			ServerTick:          8,
			LastInputSequence:   7,
			Dimension:           core.Overworld,
			Position:            mgl32.Vec3{1, 2, 3},
			Velocity:            mgl32.Vec3{4, 5, 6},
			Yaw:                 90,
			Pitch:               -15,
			OnGround:            true,
			Ready:               true,
			Reset:               true,
			MiningActive:        true,
			MiningTarget:        core.BlockPos{X: 1, Y: 2, Z: 3},
			MiningProgressTicks: 6,
			MiningRequiredTicks: 15,
			MiningHarvestable:   true,
		},
		network.RemotePlayerSpawn{PlayerID: core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}, DisplayName: "Chen"},
		network.RemotePlayerDespawn{PlayerID: core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}},
		network.RemotePlayerStates{Players: []network.RemotePlayerState{{PlayerID: core.PlayerID{0, 1, 2, 3, 4, 5, 0x46, 7, 0x88, 9, 10, 11, 12, 13, 14, 15}}}},
		network.KeepAlive{Token: 1},
		network.Disconnect{Code: network.DisconnectTimeout},
		network.InventoryState{},
		network.ItemDropUpserts{},
		network.ItemDropRemoves{},
		network.ChatEvent{},
		network.CompanionSpawn{},
		network.CompanionStates{},
		network.CompanionDespawn{ID: companion.ID{}},
	}
	if len(clientMessages) != 8 || len(serverMessages) != 17 {
		t.Fatal("消息集合不完整")
	}
}

func TestHotbarMessagesValidateFixedBounds(t *testing.T) {
	var hotbar core.Hotbar
	hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	valid := []interface{ Validate() error }{
		network.PlaceBlock{Slot: core.HotbarSlots - 1},
		network.SelectHotbar{Slot: 0},
		network.InventoryState{Inventory: core.Inventory{Hotbar: hotbar}},
	}
	for _, message := range valid {
		if err := message.Validate(); err != nil {
			t.Fatalf("%T 合法值被拒绝: %v", message, err)
		}
	}

	overflow := hotbar
	overflow.Slots[1] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount + 1}
	unknown := hotbar
	unknown.Slots[2] = core.ItemStack{Item: core.ItemID(4242), Count: 1}
	ghost := hotbar
	ghost.Slots[3] = core.ItemStack{Item: core.ItemNone, Count: 1}
	selected := hotbar
	selected.Selected = core.HotbarSlots
	invalid := []interface{ Validate() error }{
		network.PlaceBlock{Slot: core.HotbarSlots},
		network.SelectHotbar{Slot: 255},
		network.InventoryState{Inventory: core.Inventory{Hotbar: overflow}},
		network.InventoryState{Inventory: core.Inventory{Hotbar: unknown}},
		network.InventoryState{Inventory: core.Inventory{Hotbar: ghost}},
		network.InventoryState{Inventory: core.Inventory{Hotbar: selected}},
	}
	for _, message := range invalid {
		if err := message.Validate(); err == nil {
			t.Fatalf("%T 非法值被接受: %+v", message, message)
		}
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
		{network.RejectInvalidSlot, "invalid_slot"},
		{network.RejectHotbarFull, "hotbar_full"},
		{network.RejectDropCapacity, "drop_capacity"},
	}
	for _, tc := range tests {
		if string(tc.got) != tc.want {
			t.Fatalf("reject reason = %q，想要 %q", tc.got, tc.want)
		}
	}
}

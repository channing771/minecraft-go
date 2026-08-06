package network

import (
	"fmt"
	"testing"
)

func TestProtocolV1PacketIDsAreFrozen(t *testing.T) {
	client := []struct {
		state  State
		packet ClientPacket
		id     uint32
	}{
		{StateHandshake, ClientHello{}, 0}, {StateLogin, LoginStart{}, 0},
		{StatePlay, PlayerInput{}, 0},
		{StatePlay, PlaceBlock{}, 2}, {StatePlay, RequestChunkResync{}, 3},
		{StatePlay, KeepAliveReply{}, 4},
	}
	server := []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{
		{StateHandshake, ServerHello{}, 0}, {StateHandshake, HandshakeReject{}, 1},
		{StateLogin, LoginSuccess{}, 0}, {StateLogin, LoginReject{}, 1},
		{StatePlay, ChunkSnapshot{}, 0}, {StatePlay, BlockChanges{}, 1},
		{StatePlay, ForgetChunks{}, 2}, {StatePlay, PlayerState{}, 3},
		{StatePlay, CommandRejected{}, 4}, {StatePlay, KeepAlive{}, 5},
		{StatePlay, Disconnect{}, 6},
	}
	assertClientRegistry(t, client)
	assertServerRegistry(t, server)
	if _, ok := clientPacketForID(StatePlay, 1); ok {
		t.Fatal("Play client packet ID 1 必须保持未分配")
	}
	if id, ok := clientPacketID(StatePlay, PlaceBlock{}); !ok || id != 2 {
		t.Fatalf("PlaceBlock ID = %d, ok=%v，想要 2, true", id, ok)
	}
	if id, ok := clientPacketID(StatePlay, CloseContainer{}); !ok || id != 10 {
		t.Fatalf("CloseContainer ID = %d, ok=%v，想要 10, true", id, ok)
	}
}

func TestProtocolV2RemotePlayerPacketIDsAreFrozen(t *testing.T) {
	for _, test := range []struct {
		packet ServerPacket
		id     uint32
	}{
		{RemotePlayerSpawn{}, 7},
		{RemotePlayerDespawn{}, 8},
		{RemotePlayerStates{}, 9},
	} {
		gotID, ok := serverPacketID(StatePlay, test.packet)
		if !ok || gotID != test.id {
			t.Fatalf("server packet %T id=%d ok=%v, want %d true", test.packet, gotID, ok, test.id)
		}
		decoded, ok := serverPacketForID(StatePlay, test.id)
		if !ok || fmt.Sprintf("%T", decoded) != fmt.Sprintf("%T", test.packet) {
			t.Fatalf("server packet ID %d decoded=%T ok=%v, want %T true", test.id, decoded, ok, test.packet)
		}
	}
	if _, ok := serverPacketForID(StatePlay, 16); ok {
		t.Fatal("unknown play server packet ID accepted")
	}
}

func TestProtocolV3HotbarPacketIDsAreFrozen(t *testing.T) {
	assertClientRegistry(t, []struct {
		state  State
		packet ClientPacket
		id     uint32
	}{{StatePlay, SelectHotbar{}, 5}})
	assertServerRegistry(t, []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{{StatePlay, InventoryState{}, 10}})
	if _, ok := clientPacketForID(StatePlay, 12); ok {
		t.Fatal("unknown play client packet ID accepted")
	}
	if _, ok := clientPacketForID(StatePlay, 1); ok {
		t.Fatal("Play client packet ID 1 必须保持未分配")
	}
}

func TestProtocolV1RegistryRejectsUnknownIDsAndStates(t *testing.T) {
	if _, ok := clientPacketForID(StateHandshake, 1); ok {
		t.Fatal("unknown handshake client packet ID accepted")
	}
	if _, ok := serverPacketForID(StatePlay, 16); ok {
		t.Fatal("unknown play server packet ID accepted")
	}
	if _, ok := clientPacketID(StateLogin, ClientHello{}); ok {
		t.Fatal("wrong-state client packet accepted")
	}
	if _, ok := serverPacketID(StateLogin, KeepAlive{}); ok {
		t.Fatal("wrong-state server packet accepted")
	}
}

func TestCommandRejectReasonIDsAreFrozen(t *testing.T) {
	reasons := []struct {
		reason RejectReason
		id     uint8
	}{
		{RejectInvalidRay, 1}, {RejectNoTarget, 2}, {RejectChunkNotReady, 3},
		{RejectProtectedBlock, 4}, {RejectInvalidBlock, 5}, {RejectOccupied, 6},
		{RejectInvalidInput, 7}, {RejectPlayerNotReady, 8},
		{RejectInvalidSlot, 9}, {RejectHotbarFull, 10}, {RejectDropCapacity, 11},
		{RejectContainerCapacity, 12},
	}
	for _, tc := range reasons {
		got, ok := commandRejectReasonID(tc.reason)
		if !ok || got != tc.id {
			t.Fatalf("reason %q ID = %d, ok=%v; want %d, true", tc.reason, got, ok, tc.id)
		}
		decoded, ok := commandRejectReasonForID(tc.id)
		if !ok || decoded != tc.reason {
			t.Fatalf("ID %d decoded to %q, ok=%v; want %q, true", tc.id, decoded, ok, tc.reason)
		}
	}
	if _, ok := commandRejectReasonID(RejectReason("unknown")); ok {
		t.Fatal("unknown rejection reason encoded")
	}
	if _, ok := commandRejectReasonForID(0); ok {
		t.Fatal("zero rejection reason ID decoded")
	}
	if _, ok := commandRejectReasonForID(13); ok {
		t.Fatal("unknown rejection reason ID decoded")
	}
}

func assertClientRegistry(t *testing.T, packets []struct {
	state  State
	packet ClientPacket
	id     uint32
}) {
	t.Helper()
	for _, tc := range packets {
		got, ok := clientPacketID(tc.state, tc.packet)
		if !ok || got != tc.id {
			t.Fatalf("client packet %T in state %d ID = %d, ok=%v; want %d, true", tc.packet, tc.state, got, ok, tc.id)
		}
		decoded, ok := clientPacketForID(tc.state, tc.id)
		if !ok || !sameClientPacketType(decoded, tc.packet) {
			t.Fatalf("client packet ID %d in state %d decodes to %T, ok=%v; want %T, true", tc.id, tc.state, decoded, ok, tc.packet)
		}
	}
}

func assertServerRegistry(t *testing.T, packets []struct {
	state  State
	packet ServerPacket
	id     uint32
}) {
	t.Helper()
	for _, tc := range packets {
		got, ok := serverPacketID(tc.state, tc.packet)
		if !ok || got != tc.id {
			t.Fatalf("server packet %T in state %d ID = %d, ok=%v; want %d, true", tc.packet, tc.state, got, ok, tc.id)
		}
		decoded, ok := serverPacketForID(tc.state, tc.id)
		if !ok || !sameServerPacketType(decoded, tc.packet) {
			t.Fatalf("server packet ID %d in state %d decodes to %T, ok=%v; want %T, true", tc.id, tc.state, decoded, ok, tc.packet)
		}
	}
}

func sameClientPacketType(left, right ClientPacket) bool {
	switch left.(type) {
	case ClientHello:
		_, ok := right.(ClientHello)
		return ok
	case LoginStart:
		_, ok := right.(LoginStart)
		return ok
	case PlayerInput:
		_, ok := right.(PlayerInput)
		return ok
	case PlaceBlock:
		_, ok := right.(PlaceBlock)
		return ok
	case RequestChunkResync:
		_, ok := right.(RequestChunkResync)
		return ok
	case KeepAliveReply:
		_, ok := right.(KeepAliveReply)
		return ok
	case SelectHotbar:
		_, ok := right.(SelectHotbar)
		return ok
	case MoveInventoryStack:
		_, ok := right.(MoveInventoryStack)
		return ok
	case CraftRecipe:
		_, ok := right.(CraftRecipe)
		return ok
	case OpenContainer:
		_, ok := right.(OpenContainer)
		return ok
	case MoveContainerStack:
		_, ok := right.(MoveContainerStack)
		return ok
	case CloseContainer:
		_, ok := right.(CloseContainer)
		return ok
	case DropSelectedItem:
		_, ok := right.(DropSelectedItem)
		return ok
	}
	return false
}

func sameServerPacketType(left, right ServerPacket) bool {
	switch left.(type) {
	case ServerHello:
		_, ok := right.(ServerHello)
		return ok
	case HandshakeReject:
		_, ok := right.(HandshakeReject)
		return ok
	case LoginSuccess:
		_, ok := right.(LoginSuccess)
		return ok
	case LoginReject:
		_, ok := right.(LoginReject)
		return ok
	case ChunkSnapshot:
		_, ok := right.(ChunkSnapshot)
		return ok
	case BlockChanges:
		_, ok := right.(BlockChanges)
		return ok
	case ForgetChunks:
		_, ok := right.(ForgetChunks)
		return ok
	case PlayerState:
		_, ok := right.(PlayerState)
		return ok
	case CommandRejected:
		_, ok := right.(CommandRejected)
		return ok
	case KeepAlive:
		_, ok := right.(KeepAlive)
		return ok
	case Disconnect:
		_, ok := right.(Disconnect)
		return ok
	case InventoryState:
		_, ok := right.(InventoryState)
		return ok
	case ItemDropUpserts:
		_, ok := right.(ItemDropUpserts)
		return ok
	case ItemDropRemoves:
		_, ok := right.(ItemDropRemoves)
		return ok
	case FurnaceState:
		_, ok := right.(FurnaceState)
		return ok
	case ChestState:
		_, ok := right.(ChestState)
		return ok
	case ContainerClosed:
		_, ok := right.(ContainerClosed)
		return ok
	}
	return false
}

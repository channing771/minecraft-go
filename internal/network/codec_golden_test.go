package network

import (
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
)

func TestProtocolV1SmallPacketGolden(t *testing.T) {
	id := mustCodecPlayerID(t)
	clients := []struct {
		name    string
		state   State
		packet  ClientPacket
		wantID  uint32
		wantHex string
	}{
		{"hello", StateHandshake, ClientHello{ProtocolVersion: 15}, 0, "0f"},
		{"login start", StateLogin, LoginStart{PlayerID: id, DisplayName: "Chen"}, 0, "00112233445546778899aabbccddeeff044368656e"},
		{"input", StatePlay, PlayerInput{Sequence: 1, MoveX: -1, MoveZ: 1, Jump: true, Yaw: 1.5, Pitch: -0.5, Mining: true}, 0, "0100000000000000ff01010000c03f000000bf01"},
		{"place", StatePlay, PlaceBlock{Sequence: 3, Yaw: 2, Pitch: -1, Slot: 4}, 2, "030000000000000000000040000080bf04"},
		{"resync", StatePlay, RequestChunkResync{Sequence: 4, Dimension: core.Overworld, Chunk: core.ChunkPos{X: -2, Z: 3}, HaveRevision: 5}, 3, "040000000000000000000000feffffff030000000500000000000000"},
		{"keep alive reply", StatePlay, KeepAliveReply{Token: 6}, 4, "0600000000000000"},
		{"select hotbar", StatePlay, SelectHotbar{Sequence: 9, Slot: 8}, 5, "090000000000000008"},
		{"move inventory stack", StatePlay, MoveInventoryStack{Sequence: 10, From: 3, To: 35}, 6, "0a00000000000000" + "03" + "23"},
		{"craft recipe", StatePlay, CraftRecipe{Sequence: 11, Recipe: core.RecipeStoneBricks}, 7, "0b00000000000000" + "01"},
		{"drop selected item", StatePlay, DropSelectedItem{
			Sequence: 0x1122334455667788,
		}, 11, "8877665544332211"},
	}
	for _, tc := range clients {
		t.Run(tc.name, func(t *testing.T) {
			gotID, got, err := encodeClientPacketPayload(tc.state, tc.packet)
			if err != nil || gotID != tc.wantID || hex.EncodeToString(got) != tc.wantHex {
				t.Fatalf("%T id=%d payload=%x err=%v", tc.packet, gotID, got, err)
			}
			round, err := decodeClientPacketPayload(tc.state, gotID, got)
			if err != nil || !sameClientPacket(round, tc.packet) {
				t.Fatalf("round=%#v err=%v", round, err)
			}
			for length := 0; length < len(got); length++ {
				if _, err := decodeClientPacketPayload(tc.state, gotID, got[:length]); err == nil {
					t.Fatalf("truncated %T at %d accepted", tc.packet, length)
				}
			}
		})
	}

	servers := []struct {
		name    string
		state   State
		packet  ServerPacket
		wantID  uint32
		wantHex string
	}{
		{"server hello", StateHandshake, ServerHello{ProtocolVersion: 15}, 0, "0f"},
		{"handshake reject", StateHandshake, HandshakeReject{ServerProtocolVersion: 15, Code: HandshakeVersionMismatch, Message: "no"}, 1, "0f01026e6f"},
		{"login success", StateLogin, LoginSuccess{PlayerID: id}, 0, "00112233445546778899aabbccddeeff"},
		{"login reject", StateLogin, LoginReject{Code: LoginInvalidIdentity, Message: "no"}, 1, "02026e6f"},
		{"block changes", StatePlay, BlockChanges{Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: -1}, BaseRevision: 1, NewRevision: 2, Changes: []BlockChange{{Position: core.BlockPos{X: 16, Y: -64, Z: -1}, Block: core.StoneID}}}, 1, "0000000001000000ffffffff010000000000000002000000000000000110000000c0ffffffffffffff0200"},
		{"forget chunks", StatePlay, ForgetChunks{Dimension: core.Overworld, Chunks: []core.ChunkPos{{X: 1, Z: -1}, {X: 2, Z: 3}}}, 2, "000000000201000000ffffffff0200000003000000"},
		{"inactive player state", StatePlay, PlayerState{}, 3, "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000" + "00" + "0000000000000000"},
		{"active player state", StatePlay, PlayerState{Dimension: core.Overworld, MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 2, Z: 3}, MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true, Health: 15, WorldTimeTicks: 24000}, 3, "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000101000000020000000300000006000f0001" + "0f" + "c05d000000000000"},
		{"command rejected", StatePlay, CommandRejected{Sequence: 7, Reason: RejectOccupied}, 4, "070000000000000006"},
		{"keep alive", StatePlay, KeepAlive{Token: 8}, 5, "0800000000000000"},
		{"disconnect", StatePlay, Disconnect{Code: DisconnectTimeout, Message: "bye"}, 6, "0203627965"},
		{"inventory state", StatePlay, goldenInventoryState(), 10, goldenInventoryStateHex()},
	}
	for _, tc := range servers {
		t.Run(tc.name, func(t *testing.T) {
			gotID, got, err := encodeServerControlPayload(tc.state, tc.packet)
			if err != nil || gotID != tc.wantID || hex.EncodeToString(got) != tc.wantHex {
				t.Fatalf("%T id=%d payload=%x err=%v", tc.packet, gotID, got, err)
			}
			round, err := decodeServerControlPayload(tc.state, gotID, got)
			if err != nil || !sameServerPacket(round, tc.packet) {
				t.Fatalf("round=%#v err=%v", round, err)
			}
			for length := 0; length < len(got); length++ {
				if _, err := decodeServerControlPayload(tc.state, gotID, got[:length]); err == nil {
					t.Fatalf("truncated %T at %d accepted", tc.packet, length)
				}
			}
		})
	}
}

func TestProtocolV2RemotePlayerGolden(t *testing.T) {
	id := mustCodecPlayerID(t)
	tests := []struct {
		packet  ServerPacket
		wantID  uint32
		wantHex string
	}{
		{RemotePlayerSpawn{PlayerID: id, DisplayName: "陈", ServerTick: 1, Dimension: core.Overworld, Position: mgl32.Vec3{1, 2, 3}, Yaw: 4, Pitch: -5}, 7, "00112233445546778899aabbccddeeff03e999880100000000000000000000000000803f0000004000004040000080400000a0c0"},
		{RemotePlayerDespawn{PlayerID: id}, 8, "00112233445546778899aabbccddeeff"},
		{RemotePlayerStates{ServerTick: 2, Players: []RemotePlayerState{{PlayerID: id, Dimension: core.Overworld, Position: mgl32.Vec3{1, 2, 3}, Yaw: 4, Pitch: -5, Reset: true}}}, 9, "02000000000000000100112233445546778899aabbccddeeff000000000000803f0000004000004040000080400000a0c001"},
	}
	for _, test := range tests {
		packetID, payload, err := encodeServerControlPayload(StatePlay, test.packet)
		if err != nil || packetID != test.wantID || hex.EncodeToString(payload) != test.wantHex {
			t.Fatalf("%T id=%d payload=%x err=%v", test.packet, packetID, payload, err)
		}
		decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
		if err != nil || !reflect.DeepEqual(decoded, test.packet) {
			t.Fatalf("round=%#v err=%v", decoded, err)
		}
	}
}

func TestSmallPacketErrorCodeWireValues(t *testing.T) {
	for _, tc := range []struct {
		packet ServerPacket
		want   string
	}{
		{HandshakeReject{ServerProtocolVersion: 8, Code: HandshakeVersionMismatch}, "080100"},
		{LoginReject{Code: LoginServerFull}, "0100"},
		{LoginReject{Code: LoginInvalidIdentity}, "0200"},
		{LoginReject{Code: LoginPlayerDataCorrupt}, "0300"},
		{LoginReject{Code: LoginStoreUnavailable}, "0400"},
		{LoginReject{Code: LoginProtocolViolation}, "0500"},
		{LoginReject{Code: LoginInternalError}, "0600"},
		{LoginReject{Code: LoginAlreadyOnline}, "0700"},
		{Disconnect{Code: DisconnectProtocolViolation}, "0100"},
		{Disconnect{Code: DisconnectTimeout}, "0200"},
		{Disconnect{Code: DisconnectServerShutdown}, "0300"},
		{Disconnect{Code: DisconnectSlowClient}, "0400"},
		{Disconnect{Code: DisconnectInternalError}, "0500"},
	} {
		state := StateLogin
		switch tc.packet.(type) {
		case HandshakeReject:
			state = StateHandshake
		case Disconnect:
			state = StatePlay
		}
		_, got, err := encodeServerControlPayload(state, tc.packet)
		if err != nil || hex.EncodeToString(got) != tc.want {
			t.Fatalf("%T payload=%x err=%v; want %s", tc.packet, got, err, tc.want)
		}
	}
}

func TestSmallPacketCommandRejectedReasonWireValues(t *testing.T) {
	tests := []struct {
		name    string
		reason  RejectReason
		wantHex string
	}{
		{"invalid ray", RejectInvalidRay, "010000000000000001"},
		{"no target", RejectNoTarget, "010000000000000002"},
		{"chunk not ready", RejectChunkNotReady, "010000000000000003"},
		{"protected block", RejectProtectedBlock, "010000000000000004"},
		{"invalid block", RejectInvalidBlock, "010000000000000005"},
		{"occupied", RejectOccupied, "010000000000000006"},
		{"invalid input", RejectInvalidInput, "010000000000000007"},
		{"player not ready", RejectPlayerNotReady, "010000000000000008"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packet := CommandRejected{Sequence: 1, Reason: tc.reason}
			packetID, payload, err := encodeServerControlPayload(StatePlay, packet)
			if err != nil || packetID != 4 || hex.EncodeToString(payload) != tc.wantHex {
				t.Fatalf("encode payload=%x id=%d err=%v; want id=4 payload=%s", payload, packetID, err, tc.wantHex)
			}

			fixture, err := hex.DecodeString(tc.wantHex)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeServerControlPayload(StatePlay, 4, fixture)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := decoded.(CommandRejected)
			if !ok || got.Sequence != 1 || got.Reason != tc.reason {
				t.Fatalf("decode=%#v; want %#v", decoded, packet)
			}
		})
	}
}

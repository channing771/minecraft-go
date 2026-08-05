package network

import (
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
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
		{"hello", StateHandshake, ClientHello{ProtocolVersion: 9}, 0, "09"},
		{"login start", StateLogin, LoginStart{PlayerID: id, DisplayName: "Chen"}, 0, "00112233445546778899aabbccddeeff044368656e"},
		{"input", StatePlay, PlayerInput{Sequence: 1, MoveX: -1, MoveZ: 1, Jump: true, Yaw: 1.5, Pitch: -0.5, Mining: true}, 0, "0100000000000000ff01010000c03f000000bf01"},
		{"place", StatePlay, PlaceBlock{Sequence: 3, Yaw: 2, Pitch: -1, Slot: 4}, 2, "030000000000000000000040000080bf04"},
		{"resync", StatePlay, RequestChunkResync{Sequence: 4, Dimension: core.Overworld, Chunk: core.ChunkPos{X: -2, Z: 3}, HaveRevision: 5}, 3, "040000000000000000000000feffffff030000000500000000000000"},
		{"keep alive reply", StatePlay, KeepAliveReply{Token: 6}, 4, "0600000000000000"},
		{"select hotbar", StatePlay, SelectHotbar{Sequence: 9, Slot: 8}, 5, "090000000000000008"},
		{"move inventory stack", StatePlay, MoveInventoryStack{Sequence: 10, From: 3, To: 35}, 6, "0a00000000000000" + "03" + "23"},
		{"craft recipe", StatePlay, CraftRecipe{Sequence: 11, Recipe: core.RecipeStoneBricks}, 7, "0b00000000000000" + "01"},
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
		{"server hello", StateHandshake, ServerHello{ProtocolVersion: 9}, 0, "09"},
		{"handshake reject", StateHandshake, HandshakeReject{ServerProtocolVersion: 9, Code: HandshakeVersionMismatch, Message: "no"}, 1, "0901026e6f"},
		{"login success", StateLogin, LoginSuccess{PlayerID: id}, 0, "00112233445546778899aabbccddeeff"},
		{"login reject", StateLogin, LoginReject{Code: LoginInvalidIdentity, Message: "no"}, 1, "02026e6f"},
		{"block changes", StatePlay, BlockChanges{Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: -1}, BaseRevision: 1, NewRevision: 2, Changes: []BlockChange{{Position: core.BlockPos{X: 16, Y: -64, Z: -1}, Block: core.StoneID}}}, 1, "0000000001000000ffffffff010000000000000002000000000000000110000000c0ffffffffffffff0200"},
		{"forget chunks", StatePlay, ForgetChunks{Dimension: core.Overworld, Chunks: []core.ChunkPos{{X: 1, Z: -1}, {X: 2, Z: 3}}}, 2, "000000000201000000ffffffff0200000003000000"},
		{"inactive player state", StatePlay, PlayerState{}, 3, "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000" + "0000000000000000"},
		{"active player state", StatePlay, PlayerState{Dimension: core.Overworld, MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 2, Z: 3}, MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true, WorldTimeTicks: 24000}, 3, "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000101000000020000000300000006000f0001" + "c05d000000000000"},
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

func TestPlayClientPacketIDOneIsUnknown(t *testing.T) {
	if _, err := decodeClientPacketPayload(StatePlay, 1, nil); !errors.Is(err, errUnknownPacketID) {
		t.Fatalf("Play client packet ID 1 解码错误 = %v，想要 %v", err, errUnknownPacketID)
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

func TestRemotePlayerWireRejectsInvalidValues(t *testing.T) {
	id := mustCodecPlayerID(t)
	invalidID := id
	invalidID[6] = 0
	states := make([]RemotePlayerState, 7)
	for index := range states {
		states[index] = RemotePlayerState{PlayerID: id, Dimension: core.Overworld}
		states[index].PlayerID[15] = byte(index + 1)
	}
	_, maxPayload, err := encodeServerControlPayload(StatePlay, RemotePlayerStates{Players: states})
	if err != nil || len(maxPayload) != 296 || len(maxPayload) >= 512 {
		t.Fatalf("seven remote states payload=%d err=%v, want 296 and <512", len(maxPayload), err)
	}

	for _, packet := range []ServerPacket{
		RemotePlayerSpawn{PlayerID: invalidID, DisplayName: "Chen"},
		RemotePlayerSpawn{PlayerID: id, DisplayName: " Chen "},
		RemotePlayerSpawn{PlayerID: id, DisplayName: "Chen", Dimension: core.DimensionID(1)},
		RemotePlayerSpawn{PlayerID: id, DisplayName: "Chen", Position: mgl32.Vec3{float32(math.NaN()), 0, 0}},
		RemotePlayerDespawn{PlayerID: invalidID},
		RemotePlayerStates{},
		RemotePlayerStates{Players: append(states, states[0])},
		RemotePlayerStates{Players: []RemotePlayerState{{PlayerID: id, Dimension: core.Overworld}, {PlayerID: id, Dimension: core.Overworld}}},
		RemotePlayerStates{Players: []RemotePlayerState{{PlayerID: id, Dimension: core.DimensionID(1)}}},
		RemotePlayerStates{Players: []RemotePlayerState{{PlayerID: id, Position: mgl32.Vec3{float32(math.Inf(1)), 0, 0}}}},
	} {
		if _, _, err := encodeServerControlPayload(StatePlay, packet); err == nil {
			t.Fatalf("invalid remote packet encoded: %#v", packet)
		}
	}

	for _, test := range []struct {
		name    string
		payload []byte
	}{
		{"zero count", append(make([]byte, 8), 0)},
		{"eight count", append(make([]byte, 8), 8)},
		{"invalid UUIDv4", append(append(make([]byte, 8), 1), append(append([]byte{}, invalidID[:]...), make([]byte, 25)...)...)},
		{"duplicate UUID", remotePlayerStatesWireFixture(id, id)},
		{"out of order UUID", remotePlayerStatesWireFixture(states[1].PlayerID, states[0].PlayerID)},
		{"noncanonical reset bool", mustDecodeHex(t, "02000000000000000100112233445546778899aabbccddeeff000000000000803f0000004000004040000080400000a0c002")},
	} {
		t.Run(test.name, func(t *testing.T) {
			if packet, err := decodeServerControlPayload(StatePlay, 9, test.payload); err == nil {
				t.Fatalf("invalid remote wire decoded as %#v", packet)
			}
		})
	}
}

func TestRemotePlayerStatesRejectsNonCanonicalCountVarint(t *testing.T) {
	payload := mustDecodeHex(t, "0200000000000000810000112233445546778899aabbccddeeff000000000000803f0000004000004040000080400000a0c001")
	if packet, err := decodeServerControlPayload(StatePlay, 9, payload); !errors.Is(err, errInvalidUvarint) {
		t.Fatalf("noncanonical state count decoded as %#v with %v, want errInvalidUvarint", packet, err)
	}
}

func remotePlayerStatesWireFixture(ids ...core.PlayerID) []byte {
	payload := make([]byte, 9)
	payload[8] = byte(len(ids))
	for _, id := range ids {
		payload = append(payload, id[:]...)
		payload = append(payload, make([]byte, 25)...)
	}
	return payload
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

func TestSmallPacketRejectsMalformedPayloads(t *testing.T) {
	validID := mustCodecPlayerID(t)
	validClient := LoginStart{PlayerID: validID, DisplayName: "Chen"}
	_, validClientPayload, err := encodeClientPacketPayload(StateLogin, validClient)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(validClientPayload); n++ {
		if _, err := decodeClientPacketPayload(StateLogin, 0, validClientPayload[:n]); err == nil {
			t.Fatalf("truncated LoginStart at %d accepted", n)
		}
	}

	validServer := PlayerState{Position: mgl32.Vec3{1, 2, 3}, Velocity: mgl32.Vec3{4, 5, 6}}
	_, validServerPayload, err := encodeServerControlPayload(StatePlay, validServer)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(validServerPayload); n++ {
		if _, err := decodeServerControlPayload(StatePlay, 3, validServerPayload[:n]); err == nil {
			t.Fatalf("truncated PlayerState at %d accepted", n)
		}
	}

	tests := []struct {
		name string
		call func() error
	}{
		{"unknown client ID", func() error { _, err := decodeClientPacketPayload(StatePlay, 9, nil); return err }},
		{"wrong client state", func() error { _, err := decodeClientPacketPayload(StateLogin, 4, nil); return err }},
		{"unknown server ID", func() error { _, err := decodeServerControlPayload(StatePlay, 9, nil); return err }},
		{"snapshot delegated to task 5", func() error { _, err := decodeServerControlPayload(StatePlay, 0, nil); return err }},
		{"trailing client bytes", func() error { _, err := decodeClientPacketPayload(StateHandshake, 0, []byte{1, 0}); return err }},
		{"trailing server bytes", func() error {
			_, err := decodeServerControlPayload(StatePlay, 5, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0})
			return err
		}},
		{"invalid bool", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 0, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0})
			return err
		}},
		{"invalid mining bool", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 0, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2})
			return err
		}},
		{"invalid float", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 1, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xc0, 0x7f, 0, 0, 0, 0})
			return err
		}},
		{"invalid place slot", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 2, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, core.HotbarSlots})
			return err
		}},
		{"invalid select slot", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 5, []byte{0, 0, 0, 0, 0, 0, 0, 0, core.HotbarSlots})
			return err
		}},
		{"inventory state selected out of range", func() error {
			_, err := decodeServerControlPayload(StatePlay, 10, inventoryStateWire(core.Inventory{Hotbar: core.Hotbar{Selected: core.HotbarSlots}}))
			return err
		}},
		{"inventory state unknown item", func() error {
			inventory := core.Inventory{}
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemID(4242), Count: 1}
			_, err := decodeServerControlPayload(StatePlay, 10, inventoryStateWire(inventory))
			return err
		}},
		{"inventory state backpack count overflow", func() error {
			inventory := core.Inventory{}
			inventory.Backpack[0] = core.ItemStack{
				Item: core.ItemStone, Count: core.MaxStackCount + 1,
			}
			_, err := decodeServerControlPayload(StatePlay, 10, inventoryStateWire(inventory))
			return err
		}},
		{"inventory state empty item with count", func() error {
			inventory := core.Inventory{}
			inventory.Backpack[core.BackpackSlots-1] = core.ItemStack{
				Item: core.ItemNone, Count: 3,
			}
			_, err := decodeServerControlPayload(StatePlay, 10, inventoryStateWire(inventory))
			return err
		}},
		{"inventory move same slot", func() error {
			_, err := decodeClientPacketPayload(
				StatePlay, 6, []byte{0, 0, 0, 0, 0, 0, 0, 0, 4, 4},
			)
			return err
		}},
		{"unknown craft recipe", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 7, []byte{0, 0, 0, 0, 0, 0, 0, 0, 200})
			return err
		}},
		{"craft recipe trailing byte", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 7, []byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 0})
			return err
		}},
		{"inventory move slot out of range", func() error {
			_, err := decodeClientPacketPayload(
				StatePlay, 6, []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, core.InventorySlots},
			)
			return err
		}},
		{"inventory state trailing bytes", func() error {
			_, err := decodeServerControlPayload(StatePlay, 10, append(inventoryStateWire(core.Inventory{}), 0))
			return err
		}},
		{"inventory state truncated", func() error {
			wire := inventoryStateWire(core.Inventory{})
			_, err := decodeServerControlPayload(StatePlay, 10, wire[:len(wire)-1])
			return err
		}},
		{"invalid dimension", func() error {
			_, err := decodeClientPacketPayload(StatePlay, 3, []byte{0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			return err
		}},
		{"oversized block changes", func() error {
			_, err := decodeServerControlPayload(StatePlay, 1, append(make([]byte, 28), 0x81, 0x20))
			return err
		}},
		{"oversized forget chunks", func() error {
			_, err := decodeServerControlPayload(StatePlay, 2, []byte{0, 0, 0, 0, 0x81, 0x20})
			return err
		}},
		{"unknown rejection reason", func() error {
			_, err := decodeServerControlPayload(StatePlay, 4, []byte{0, 0, 0, 0, 0, 0, 0, 0, 13})
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("malformed payload accepted")
			} else if tc.name == "snapshot delegated to task 5" && !strings.Contains(err.Error(), "Task 5") {
				t.Fatalf("snapshot error %q does not name Task 5", err)
			}
		})
	}
}

func TestSmallPacketRejectsInvalidSemanticPackets(t *testing.T) {
	badChanges := BlockChanges{Dimension: core.Overworld, Chunk: core.ChunkPos{}, BaseRevision: 1, NewRevision: 3, Changes: []BlockChange{{Position: core.BlockPos{Y: core.MinY}, Block: core.StoneID}}}
	unsortedChanges := BlockChanges{Dimension: core.Overworld, Chunk: core.ChunkPos{}, BaseRevision: 1, NewRevision: 2, Changes: []BlockChange{{Position: core.BlockPos{X: 1, Y: core.MinY}, Block: core.StoneID}, {Position: core.BlockPos{Y: core.MinY}, Block: core.StoneID}}}
	crossChunkChanges := BlockChanges{Dimension: core.Overworld, Chunk: core.ChunkPos{}, BaseRevision: 1, NewRevision: 2, Changes: []BlockChange{{Position: core.BlockPos{X: 16, Y: core.MinY}, Block: core.StoneID}}}
	tooMany := make([]core.ChunkPos, 4097)
	for index := range tooMany {
		tooMany[index] = core.ChunkPos{X: int32(index)}
	}
	tests := []struct {
		name   string
		state  State
		packet ServerPacket
	}{
		{"non-continuous revision", StatePlay, badChanges},
		{"unsorted changes", StatePlay, unsortedChanges},
		{"cross chunk changes", StatePlay, crossChunkChanges},
		{"4097 changes", StatePlay, tooManyValidBlockChanges()},
		{"4097 forget chunks", StatePlay, ForgetChunks{Dimension: core.Overworld, Chunks: tooMany}},
		{"invalid server dimension", StatePlay, PlayerState{Dimension: core.DimensionID(1)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := encodeServerControlPayload(tc.state, tc.packet); err == nil {
				t.Fatal("invalid packet encoded")
			}
		})
	}
	if _, _, err := encodeClientPacketPayload(StatePlay, PlaceBlock{Slot: core.HotbarSlots}); err == nil {
		t.Fatal("invalid client slot encoded")
	}
	if _, _, err := encodeClientPacketPayload(StatePlay, SelectHotbar{Slot: core.HotbarSlots}); err == nil {
		t.Fatal("invalid client hotbar selection encoded")
	}
	if _, _, err := encodeServerControlPayload(
		StatePlay,
		InventoryState{Inventory: core.Inventory{Hotbar: core.Hotbar{Selected: core.HotbarSlots}}},
	); err == nil {
		t.Fatal("invalid server inventory state encoded")
	}
	if _, _, err := encodeClientPacketPayload(
		StatePlay, MoveInventoryStack{From: core.InventorySlots},
	); err == nil {
		t.Fatal("invalid inventory move encoded")
	}
	if _, _, err := encodeClientPacketPayload(StatePlay, CraftRecipe{}); err == nil {
		t.Fatal("unknown recipe encoded")
	}
	if _, _, err := encodeClientPacketPayload(StatePlay, PlayerInput{Yaw: float32(math.NaN())}); err == nil {
		t.Fatal("non-finite client float encoded")
	}
}

func TestSmallPacketRejectsInvalidBlockChangesWire(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		wantErr error
	}{
		{
			name:    "non-continuous revision",
			payload: blockChangesWireFixture(1, 3, []BlockChange{{Position: core.BlockPos{Y: core.MinY}, Block: core.StoneID}}),
		},
		{
			name: "unsorted changes",
			payload: blockChangesWireFixture(1, 2, []BlockChange{
				{Position: core.BlockPos{X: 1, Y: core.MinY}, Block: core.StoneID},
				{Position: core.BlockPos{Y: core.MinY}, Block: core.StoneID},
			}),
		},
		{
			name:    "cross chunk changes",
			payload: blockChangesWireFixture(1, 2, []BlockChange{{Position: core.BlockPos{X: 16, Y: core.MinY}, Block: core.StoneID}}),
		},
		{name: "4097 changes", payload: blockChangesCountFixture(4097), wantErr: errInvalidCount},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if packet, err := decodeServerControlPayload(StatePlay, 1, tc.payload); err == nil {
				t.Fatalf("invalid wire payload decoded as %#v", packet)
			} else if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("decode error=%v; want %v", err, tc.wantErr)
			}
		})
	}
}

func blockChangesWireFixture(baseRevision, newRevision uint64, changes []BlockChange) []byte {
	var encoder byteEncoder
	encoder.i32(int32(core.Overworld))
	encoder.i32(0)
	encoder.i32(0)
	encoder.u64(baseRevision)
	encoder.u64(newRevision)
	encoder.uvarint(uint32(len(changes)))
	for _, change := range changes {
		encoder.i32(change.Position.X)
		encoder.i32(change.Position.Y)
		encoder.i32(change.Position.Z)
		encoder.u16(uint16(change.Block))
	}
	return encoder.data
}

func blockChangesCountFixture(count uint32) []byte {
	var encoder byteEncoder
	encoder.i32(int32(core.Overworld))
	encoder.i32(0)
	encoder.i32(0)
	encoder.u64(1)
	encoder.u64(2)
	encoder.uvarint(count)
	return encoder.data
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func mustCodecPlayerID(t *testing.T) core.PlayerID {
	t.Helper()
	id, err := core.ParsePlayerID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func goldenInventoryState() InventoryState {
	var inventory core.Inventory
	inventory.Hotbar.Selected = 2
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemGrass, Count: core.MaxStackCount}
	inventory.Backpack[0] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	inventory.Backpack[core.BackpackSlots-1] = core.ItemStack{Item: core.ItemStone, Count: 9}
	return InventoryState{Inventory: inventory}
}

// goldenInventoryStateHex 是 1 字节选中栏位加 36 格固定编码，共 109 字节。
func goldenInventoryStateHex() string {
	empty := "000000"
	hex := "02" + "010005" + empty + empty + empty + "030040"
	for range 4 {
		hex += empty
	}
	hex += "020001"
	for range core.BackpackSlots - 2 {
		hex += empty
	}
	return hex + "010009"
}

// inventoryStateWire 手工构造固定负载，用于绕过编码器校验注入非法状态。
func inventoryStateWire(inventory core.Inventory) []byte {
	wire := make([]byte, 0, 1+core.InventorySlots*3)
	wire = append(wire, inventory.Hotbar.Selected)
	appendStack := func(stack core.ItemStack) {
		wire = append(wire, byte(stack.Item), byte(stack.Item>>8), stack.Count)
	}
	for _, stack := range inventory.Hotbar.Slots {
		appendStack(stack)
	}
	for _, stack := range inventory.Backpack {
		appendStack(stack)
	}
	return wire
}

func sameClientPacket(got, want ClientPacket) bool {
	switch got := got.(type) {
	case ClientHello:
		other, ok := want.(ClientHello)
		return ok && got == other
	case LoginStart:
		other, ok := want.(LoginStart)
		return ok && got == other
	case PlayerInput:
		other, ok := want.(PlayerInput)
		return ok && got == other
	case PlaceBlock:
		other, ok := want.(PlaceBlock)
		return ok && got == other
	case RequestChunkResync:
		other, ok := want.(RequestChunkResync)
		return ok && got == other
	case KeepAliveReply:
		other, ok := want.(KeepAliveReply)
		return ok && got == other
	case SelectHotbar:
		other, ok := want.(SelectHotbar)
		return ok && got == other
	case MoveInventoryStack:
		other, ok := want.(MoveInventoryStack)
		return ok && got == other
	case CraftRecipe:
		other, ok := want.(CraftRecipe)
		return ok && got == other
	case OpenFurnace:
		other, ok := want.(OpenFurnace)
		return ok && got == other
	case MoveFurnaceStack:
		other, ok := want.(MoveFurnaceStack)
		return ok && got == other
	case CloseFurnace:
		other, ok := want.(CloseFurnace)
		return ok && got == other
	default:
		return false
	}
}

func sameServerPacket(got, want ServerPacket) bool {
	switch got := got.(type) {
	case ServerHello:
		other, ok := want.(ServerHello)
		return ok && got == other
	case HandshakeReject:
		other, ok := want.(HandshakeReject)
		return ok && got == other
	case LoginSuccess:
		other, ok := want.(LoginSuccess)
		return ok && got == other
	case LoginReject:
		other, ok := want.(LoginReject)
		return ok && got == other
	case BlockChanges:
		other, ok := want.(BlockChanges)
		if !ok || got.Dimension != other.Dimension || got.Chunk != other.Chunk || got.BaseRevision != other.BaseRevision || got.NewRevision != other.NewRevision || len(got.Changes) != len(other.Changes) {
			return false
		}
		for index := range got.Changes {
			if got.Changes[index] != other.Changes[index] {
				return false
			}
		}
		return true
	case ForgetChunks:
		other, ok := want.(ForgetChunks)
		if !ok || got.Dimension != other.Dimension || len(got.Chunks) != len(other.Chunks) {
			return false
		}
		for index := range got.Chunks {
			if got.Chunks[index] != other.Chunks[index] {
				return false
			}
		}
		return true
	case PlayerState:
		other, ok := want.(PlayerState)
		return ok && got == other
	case CommandRejected:
		other, ok := want.(CommandRejected)
		return ok && got == other
	case KeepAlive:
		other, ok := want.(KeepAlive)
		return ok && got == other
	case Disconnect:
		other, ok := want.(Disconnect)
		return ok && got == other
	case InventoryState:
		other, ok := want.(InventoryState)
		return ok && got == other
	default:
		return false
	}
}

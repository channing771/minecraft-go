package network

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

func TestPlayClientPacketIDOneIsUnknown(t *testing.T) {
	if _, err := decodeClientPacketPayload(StatePlay, 1, nil); !errors.Is(err, errUnknownPacketID) {
		t.Fatalf("Play client packet ID 1 解码错误 = %v，想要 %v", err, errUnknownPacketID)
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

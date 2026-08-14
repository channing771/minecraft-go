package network

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"math"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

func TestCompanionMessageIDsAreAppendOnly(t *testing.T) {
	assertClientRegistry(t, []struct {
		state  State
		packet ClientPacket
		id     uint32
	}{
		{StatePlay, PlayerInput{}, 0},
		{StatePlay, PlaceBlock{}, 2},
		{StatePlay, RequestChunkResync{}, 3},
		{StatePlay, KeepAliveReply{}, 4},
		{StatePlay, SelectHotbar{}, 5},
		{StatePlay, MoveInventoryStack{}, 6},
		{StatePlay, CraftRecipe{}, 7},
		{StatePlay, OpenContainer{}, 8},
		{StatePlay, MoveContainerStack{}, 9},
		{StatePlay, CloseContainer{}, 10},
		{StatePlay, DropSelectedItem{}, 11},
		{StatePlay, ChatCommand{}, 12},
	})
	assertServerRegistry(t, []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{
		{StatePlay, ChunkSnapshot{}, 0},
		{StatePlay, BlockChanges{}, 1},
		{StatePlay, ForgetChunks{}, 2},
		{StatePlay, PlayerState{}, 3},
		{StatePlay, CommandRejected{}, 4},
		{StatePlay, KeepAlive{}, 5},
		{StatePlay, Disconnect{}, 6},
		{StatePlay, RemotePlayerSpawn{}, 7},
		{StatePlay, RemotePlayerDespawn{}, 8},
		{StatePlay, RemotePlayerStates{}, 9},
		{StatePlay, InventoryState{}, 10},
		{StatePlay, ItemDropUpserts{}, 11},
		{StatePlay, ItemDropRemoves{}, 12},
		{StatePlay, FurnaceState{}, 13},
		{StatePlay, ContainerClosed{}, 14},
		{StatePlay, ChestState{}, 15},
		{StatePlay, ChatEvent{}, 16},
		{StatePlay, CompanionSpawn{}, 17},
		{StatePlay, CompanionStates{}, 18},
		{StatePlay, CompanionDespawn{}, 19},
	})
	if _, ok := clientPacketForID(StatePlay, 1); ok {
		t.Fatal("Play client packet ID 1 必须保持未分配")
	}
	if _, ok := clientPacketForID(StatePlay, 13); ok {
		t.Fatal("未知 client packet ID 13 被接受")
	}
	if _, ok := serverPacketForID(StatePlay, 20); ok {
		t.Fatal("未知 server packet ID 20 被接受")
	}
}

func TestCompanionMessageGolden(t *testing.T) {
	tests := []struct {
		name    string
		client  ClientPacket
		server  ServerPacket
		wantID  uint32
		wantHex string
	}{
		{"ChatCommand", ChatCommand{Text: "@A x"}, nil, 12, "0440412078"},
		{"ChatEvent", nil, validAcceptedChatEvent(), 16,
			"0100000000000000" + "10000000000040008000000000000009" + "044368656e" +
				"10000000000040008000000000000001" + "0141" + "0100" + "0178"},
		{"CompanionSpawn", nil, CompanionSpawn{ID: testCompanionID(1), Name: "A", Tick: 1}, 17,
			"10000000000040008000000000000001" + "0141" + "0100000000000000" +
				"00000000" + "000000000000000000000000" + "0000000000000000"},
		{"CompanionStates", nil, CompanionStates{Tick: 2, States: []CompanionState{{ID: testCompanionID(1)}}}, 18,
			"0200000000000000" + "01" + "10000000000040008000000000000001" +
				"00000000" + "000000000000000000000000" + "0000000000000000" + "00"},
		{"CompanionDespawn", nil, CompanionDespawn{ID: testCompanionID(1)}, 19,
			"10000000000040008000000000000001"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var packetID uint32
			var payload []byte
			var err error
			if test.client != nil {
				packetID, payload, err = encodeClientPacketPayload(StatePlay, test.client)
			} else {
				packetID, payload, err = encodeServerControlPayload(StatePlay, test.server)
			}
			if err != nil || packetID != test.wantID || hex.EncodeToString(payload) != test.wantHex {
				t.Fatalf("id=%d payload=%x error=%v，想要 id=%d payload=%s",
					packetID, payload, err, test.wantID, test.wantHex)
			}
		})
	}
}

func TestChatCommandAccepts1024BytesAndRejects1025(t *testing.T) {
	for _, text := range []string{"@A x", strings.Repeat("x", 1024)} {
		packet := ChatCommand{Text: text}
		if err := packet.Validate(); err != nil {
			t.Fatalf("%d-byte ChatCommand 被拒绝: %v", len(text), err)
		}
		packetID, payload, err := encodeClientPacketPayload(StatePlay, packet)
		if err != nil || packetID != 12 {
			t.Fatalf("ChatCommand 编码 = (%d,%d,%v)", packetID, len(payload), err)
		}
	}
	for _, text := range []string{
		"", strings.Repeat("x", 1025), " x", "x ", "\u3000x", "x\u00a0",
		"x\x00y", "x\ny", string([]byte{0xff}),
	} {
		packet := ChatCommand{Text: text}
		if err := packet.Validate(); err == nil {
			t.Fatalf("非法 ChatCommand %q 通过 Validate", text)
		}
		if _, _, err := encodeClientPacketPayload(StatePlay, packet); err == nil {
			t.Fatalf("非法 ChatCommand %q 被编码", text)
		}
	}
	for _, payload := range [][]byte{
		{0},
		append([]byte{0x81, 0x08}, make([]byte, 1025)...),
		{1, 0xff},
		{3, 'x', 0, 'y'},
		{3, 'x', '\n', 'y'},
		{2, ' ', 'x'},
		{2, 'x', ' '},
		{4, 0xe3, 0x80, 0x80, 'x'},
		{3, 'x', 0xc2, 0xa0},
	} {
		if packet, err := decodeClientPacketPayload(StatePlay, 12, payload); err == nil || packet != nil {
			t.Fatalf("非法 ChatCommand wire 解码为 %#v, %v", packet, err)
		}
	}
}

func TestCompanionSpawnAndChatEventStringBoundaries(t *testing.T) {
	id := testCompanionID(1)
	playerID := core.PlayerID(testCompanionID(2))
	maxName := strings.Repeat("𐐀", 32)
	maxCommand := strings.Repeat("x", 1024)
	valid := []interface{ Validate() error }{
		CompanionSpawn{ID: id, Name: maxName, Dimension: core.Overworld, Pitch: float32(math.Pi / 2)},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: maxName, CompanionID: id,
			CompanionName: maxName, Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: maxCommand},
		ChatEvent{EventID: 2, PlayerID: playerID, PlayerName: "Chen", Kind: ChatEventRejected,
			RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 3, PlayerID: playerID, PlayerName: "Chen", CompanionName: maxName,
			Kind: ChatEventRejected, RejectReason: ChatRejectUnknownCompanion},
	}
	for _, message := range valid {
		if err := message.Validate(); err != nil {
			t.Fatalf("合法 %T 被拒绝: %v", message, err)
		}
	}

	tooManyRunes := strings.Repeat("a", 33)
	// 单个合法 UTF-8 rune 最多四字节，因此超过 128 bytes 的合法名称必然也超过 32 rune。
	exact129Bytes := strings.Repeat("𐐀", 32) + "a"
	tooManyBytes := strings.Repeat("𐐀", 33)
	if len(exact129Bytes) != 129 || utf8.RuneCountInString(exact129Bytes) != 33 {
		t.Fatalf("精确边界夹具 = %d bytes/%d runes，想要 129/33",
			len(exact129Bytes), utf8.RuneCountInString(exact129Bytes))
	}
	invalid := []interface{ Validate() error }{
		CompanionSpawn{ID: id, Name: tooManyRunes, Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: exact129Bytes, Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: tooManyBytes, Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: " A", Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: "A\n", Dimension: core.Overworld},
		CompanionSpawn{ID: id, Name: "A", Dimension: 1},
		CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: float32(math.Pi/2) + 0.01},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: tooManyRunes, Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: exact129Bytes, Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: tooManyBytes, Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventAccepted, RejectReason: ChatRejectNone},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionName: "A",
			Kind: ChatEventRejected, RejectReason: ChatRejectInvalidFormat},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionName: "A", Command: "x",
			Kind: ChatEventRejected, RejectReason: ChatRejectUnknownCompanion},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionName: "A",
			Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: "x"},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventAccepted, RejectReason: ChatRejectInvalidFormat, Command: "x"},
		ChatEvent{EventID: 1, PlayerID: playerID, PlayerName: "Chen", CompanionID: id,
			CompanionName: "A", Kind: ChatEventKind(3), RejectReason: ChatRejectNone, Command: "x"},
	}
	for _, message := range invalid {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法 %T 被接受: %+v", message, message)
		}
	}

	var oversizedNameEvent byteEncoder
	oversizedNameEvent.u64(1)
	oversizedNameEvent.data = append(oversizedNameEvent.data, playerID[:]...)
	oversizedNameEvent.string(exact129Bytes, 129)
	oversizedNameEvent.data = append(oversizedNameEvent.data, make([]byte, len(id))...)
	oversizedNameEvent.string("", 128)
	oversizedNameEvent.u8(uint8(ChatEventRejected))
	oversizedNameEvent.u8(uint8(ChatRejectInvalidFormat))
	oversizedNameEvent.string("", 1024)
	if packet, err := decodeServerControlPayload(StatePlay, 16, oversizedNameEvent.data); err == nil || packet != nil {
		t.Fatalf("129-byte player name wire 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionPitchUsesRadiansInValidateAndDecode(t *testing.T) {
	id := testCompanionID(1)
	halfPi := float32(math.Pi / 2)
	aboveHalfPi := math.Nextafter32(halfPi, float32(math.Inf(1)))
	belowNegativeHalfPi := math.Nextafter32(-halfPi, float32(math.Inf(-1)))

	for _, pitch := range []float32{-halfPi, halfPi} {
		if err := (CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: pitch}).Validate(); err != nil {
			t.Fatalf("合法 Spawn pitch %v 被拒绝: %v", pitch, err)
		}
		if err := (CompanionStates{States: []CompanionState{{
			ID: id, Dimension: core.Overworld, Pitch: pitch,
		}}}).Validate(); err != nil {
			t.Fatalf("合法 States pitch %v 被拒绝: %v", pitch, err)
		}
	}

	for _, message := range []interface{ Validate() error }{
		CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: aboveHalfPi},
		CompanionSpawn{ID: id, Name: "A", Dimension: core.Overworld, Pitch: belowNegativeHalfPi},
		CompanionStates{States: []CompanionState{{ID: id, Dimension: core.Overworld, Pitch: aboveHalfPi}}},
		CompanionStates{States: []CompanionState{{ID: id, Dimension: core.Overworld, Pitch: belowNegativeHalfPi}}},
	} {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法 %T pitch 通过 Validate: %+v", message, message)
		}
	}

	_, spawnWire, err := encodeServerControlPayload(StatePlay, CompanionSpawn{
		ID: id, Name: "A", Dimension: core.Overworld, Pitch: halfPi,
	})
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(spawnWire[len(spawnWire)-4:], math.Float32bits(aboveHalfPi))
	if packet, err := decodeServerControlPayload(StatePlay, 17, spawnWire); err == nil || packet != nil {
		t.Fatalf("非法 Spawn pitch raw wire 解码为 %#v, %v", packet, err)
	}

	statesWire := companionStatesWireFixture(id)
	binary.LittleEndian.PutUint32(statesWire[len(statesWire)-5:len(statesWire)-1], math.Float32bits(belowNegativeHalfPi))
	if packet, err := decodeServerControlPayload(StatePlay, 18, statesWire); err == nil || packet != nil {
		t.Fatalf("非法 States pitch raw wire 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionMessagesHaveFixedMaximumWireLengths(t *testing.T) {
	id := testCompanionID(1)
	maxName := strings.Repeat("𐐀", 32)
	maxCommand := strings.Repeat("x", 1024)
	states := make([]CompanionState, 4)
	for index := range states {
		states[index] = CompanionState{ID: testCompanionID(byte(index + 1)), Dimension: core.Overworld}
	}
	tests := []struct {
		name string
		want int
		call func() ([]byte, error)
	}{
		{"ChatCommand", 1026, func() ([]byte, error) {
			_, payload, err := encodeClientPacketPayload(StatePlay, ChatCommand{Text: maxCommand})
			return payload, err
		}},
		{"CompanionSpawn", 178, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, CompanionSpawn{ID: id, Name: maxName, Dimension: core.Overworld})
			return payload, err
		}},
		{"CompanionStates", 173, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, CompanionStates{States: states})
			return payload, err
		}},
		{"ChatEvent", 1328, func() ([]byte, error) {
			_, payload, err := encodeServerControlPayload(StatePlay, ChatEvent{EventID: 1,
				PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: maxName, CompanionID: id,
				CompanionName: maxName, Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: maxCommand})
			return payload, err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, err := test.call()
			if err != nil || len(payload) != test.want {
				t.Fatalf("wire length=%d error=%v, 想要 %d", len(payload), err, test.want)
			}
		})
	}
	if packet, err := decodeClientPacketPayload(StatePlay, 12, make([]byte, 1027)); err == nil || packet != nil {
		t.Fatalf("超长 ChatCommand 解码为 %#v, %v", packet, err)
	}
	for id, length := range map[uint32]int{16: 1329, 17: 179, 18: 174, 19: 17} {
		if packet, err := decodeServerControlPayload(StatePlay, id, make([]byte, length)); err == nil || packet != nil {
			t.Fatalf("超长 server ID %d 解码为 %#v, %v", id, packet, err)
		}
	}
}

func TestCompanionStatesRejectsFiveDuplicateOrUnsortedAtomically(t *testing.T) {
	five := make([]CompanionState, 5)
	for index := range five {
		five[index] = CompanionState{ID: testCompanionID(byte(index + 1)), Dimension: core.Overworld}
	}
	if err := (CompanionStates{States: five}).Validate(); err == nil {
		t.Fatal("五项 states 通过 Validate")
	}
	if _, _, err := encodeServerControlPayload(StatePlay, CompanionStates{States: five}); err == nil {
		t.Fatal("五项 states 被编码")
	}
	for _, test := range []struct {
		name string
		ids  []companion.ID
	}{
		{"empty", nil},
		{"five", []companion.ID{testCompanionID(1), testCompanionID(2), testCompanionID(3), testCompanionID(4), testCompanionID(5)}},
		{"duplicate", []companion.ID{testCompanionID(1), testCompanionID(1)}},
		{"unsorted", []companion.ID{testCompanionID(2), testCompanionID(1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			packet, err := decodeServerControlPayload(StatePlay, 18, companionStatesWireFixture(test.ids...))
			if err == nil || packet != nil {
				t.Fatalf("非法 states 解码为 %#v, %v", packet, err)
			}
		})
	}
	declaredHuge := append(make([]byte, 8), 0xff, 0xff, 0xff, 0xff, 0x0f)
	if packet, err := decodeServerControlPayload(StatePlay, 18, declaredHuge); err == nil || packet != nil {
		t.Fatalf("巨大 count 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionDecoderRejectsInvalidIDsEnumsNumbersAndDimensions(t *testing.T) {
	spawn := CompanionSpawn{ID: testCompanionID(1), Name: "A", Tick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{1, 2, 3}, Yaw: 4, Pitch: 0.5}
	_, spawnWire, err := encodeServerControlPayload(StatePlay, spawn)
	if err != nil {
		t.Fatal(err)
	}
	mutations := [][]byte{
		append([]byte(nil), spawnWire...),
		append([]byte(nil), spawnWire...),
		append([]byte(nil), spawnWire...),
		append([]byte(nil), spawnWire...),
	}
	clear(mutations[0][:16])
	binary.LittleEndian.PutUint32(mutations[1][26:30], 1)
	binary.LittleEndian.PutUint32(mutations[2][len(spawnWire)-4:], math.Float32bits(91))
	binary.LittleEndian.PutUint32(mutations[3][len(spawnWire)-8:len(spawnWire)-4], 0x7fc00000)
	for index, payload := range mutations {
		if packet, err := decodeServerControlPayload(StatePlay, 17, payload); err == nil || packet != nil {
			t.Fatalf("spawn mutation %d 解码为 %#v, %v", index, packet, err)
		}
	}

	accepted := validAcceptedChatEvent()
	_, eventWire, err := encodeServerControlPayload(StatePlay, accepted)
	if err != nil {
		t.Fatal(err)
	}
	kindOffset := 8 + 16 + 1 + len(accepted.PlayerName) + 16 + 1 + len(accepted.CompanionName)
	for _, mutation := range []func([]byte){
		func(payload []byte) { payload[kindOffset] = 3 },
		func(payload []byte) { payload[kindOffset+1] = byte(ChatRejectInvalidFormat) },
		func(payload []byte) { clear(payload[8 : 8+16]) },
	} {
		payload := append([]byte(nil), eventWire...)
		mutation(payload)
		if packet, err := decodeServerControlPayload(StatePlay, 16, payload); err == nil || packet != nil {
			t.Fatalf("ChatEvent mutation 解码为 %#v, %v", packet, err)
		}
	}

	stateWire := companionStatesWireFixture(testCompanionID(1))
	stateOffset := 9
	for _, mutation := range []func([]byte){
		func(payload []byte) { clear(payload[stateOffset : stateOffset+16]) },
		func(payload []byte) { binary.LittleEndian.PutUint32(payload[stateOffset+16:stateOffset+20], 1) },
		func(payload []byte) {
			binary.LittleEndian.PutUint32(payload[len(payload)-5:len(payload)-1], math.Float32bits(-91))
		},
		func(payload []byte) { payload[len(payload)-1] = 2 },
	} {
		payload := append([]byte(nil), stateWire...)
		mutation(payload)
		if packet, err := decodeServerControlPayload(StatePlay, 18, payload); err == nil || packet != nil {
			t.Fatalf("CompanionStates mutation 解码为 %#v, %v", packet, err)
		}
	}
	if packet, err := decodeServerControlPayload(StatePlay, 19, make([]byte, 16)); err == nil || packet != nil {
		t.Fatalf("零 CompanionDespawn ID 解码为 %#v, %v", packet, err)
	}
}

func TestCompanionMessagesMatchMemoryAndTCP(t *testing.T) {
	clientMessage := ChatCommand{Text: "@A x"}
	serverMessages := []ServerPacket{
		validAcceptedChatEvent(),
		CompanionSpawn{ID: testCompanionID(1), Name: "A", Tick: 1, Dimension: core.Overworld},
		CompanionStates{Tick: 2, States: []CompanionState{{ID: testCompanionID(1), Dimension: core.Overworld}}},
		CompanionDespawn{ID: testCompanionID(1)},
	}
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			client, server := open.open(t)
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			if err := client.Send(context.Background(), StatePlay, clientMessage); err != nil {
				t.Fatal(err)
			}
			gotClient, err := server.Recv(context.Background(), StatePlay)
			if err != nil || !reflect.DeepEqual(gotClient, clientMessage) {
				t.Fatalf("client message = (%#v,%v)", gotClient, err)
			}
			for _, message := range serverMessages {
				if err := server.Send(context.Background(), StatePlay, message); err != nil {
					t.Fatal(err)
				}
				got, err := client.Recv(context.Background(), StatePlay)
				if err != nil || !reflect.DeepEqual(got, message) {
					t.Fatalf("server message = (%#v,%v), 想要 %#v", got, err, message)
				}
			}
		})
	}
}

func TestCompanionStatesFiveRejectedByMemoryAndTCP(t *testing.T) {
	five := make([]CompanionState, 5)
	for index := range five {
		five[index] = CompanionState{ID: testCompanionID(byte(index + 1)), Dimension: core.Overworld}
	}
	for _, open := range transportOpeners {
		t.Run(open.name, func(t *testing.T) {
			client, server := open.open(t)
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			if err := server.Send(context.Background(), StatePlay, CompanionStates{States: five}); err == nil {
				t.Fatal("五项 CompanionStates 被 transport 接受")
			}
		})
	}
}

func validAcceptedChatEvent() ChatEvent {
	return ChatEvent{
		EventID: 1, PlayerID: core.PlayerID(testCompanionID(9)), PlayerName: "Chen",
		CompanionID: testCompanionID(1), CompanionName: "A",
		Kind: ChatEventAccepted, RejectReason: ChatRejectNone, Command: "x",
	}
}

func testCompanionID(last byte) companion.ID {
	return companion.ID{0: 0x10, 6: 0x40, 8: 0x80, 15: last}
}

func companionStatesWireFixture(ids ...companion.ID) []byte {
	var encoder byteEncoder
	encoder.u64(1)
	encoder.uvarint(uint32(len(ids)))
	for _, id := range ids {
		encoder.data = append(encoder.data, id[:]...)
		encoder.i32(int32(core.Overworld))
		for range 3 {
			encoder.f32(0)
		}
		encoder.f32(0)
		encoder.f32(0)
		encoder.bool(false)
	}
	return encoder.data
}

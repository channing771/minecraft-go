package network

import (
	"math"
	"testing"

	"minecraft-go/internal/core"
)

func testFurnaceRef() core.FurnaceRef {
	return core.FurnaceRef{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: -3, Z: 7},
		Slot:       5,
		Generation: 9,
	}
}

func TestProtocolV7FurnacePacketIDsAreFrozen(t *testing.T) {
	assertClientRegistry(t, []struct {
		state  State
		packet ClientPacket
		id     uint32
	}{
		{StatePlay, OpenFurnace{}, 8},
		{StatePlay, MoveFurnaceStack{}, 9},
		{StatePlay, CloseFurnace{}, 10},
	})
	assertServerRegistry(t, []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{
		{StatePlay, FurnaceState{}, 13},
		{StatePlay, FurnaceClosed{}, 14},
	})
	if _, ok := clientPacketForID(StatePlay, 12); ok {
		t.Fatal("unknown play client packet ID accepted")
	}
	if _, ok := clientPacketForID(StatePlay, 1); ok {
		t.Fatal("Play client packet ID 1 必须保持未分配")
	}
	if _, ok := serverPacketForID(StatePlay, 15); ok {
		t.Fatal("unknown play server packet ID accepted")
	}
}

func TestProtocolV7FurnacePayloadsAreFixedLength(t *testing.T) {
	ref := testFurnaceRef()
	clients := []struct {
		name   string
		packet ClientPacket
		bytes  int
	}{
		{"open", OpenFurnace{Sequence: 3, Yaw: 1.5, Pitch: -0.5}, 16},
		{"move", MoveFurnaceStack{
			Sequence: 4, Furnace: ref, From: 0, To: core.FurnaceInputSlot,
		}, 27},
		{"close", CloseFurnace{Sequence: 5}, 8},
	}
	for _, tc := range clients {
		t.Run(tc.name, func(t *testing.T) {
			_, payload, err := encodeClientPacketPayload(StatePlay, tc.packet)
			if err != nil || len(payload) != tc.bytes {
				t.Fatalf("%T payload=%d err=%v，想要 %d 字节", tc.packet, len(payload), err, tc.bytes)
			}
			for length := range len(payload) {
				id, _ := clientPacketID(StatePlay, tc.packet)
				if _, err := decodeClientPacketPayload(StatePlay, id, payload[:length]); err == nil {
					t.Fatalf("截断到 %d 字节仍被接受", length)
				}
			}
			id, _ := clientPacketID(StatePlay, tc.packet)
			trailing := append(append([]byte(nil), payload...), 0)
			if _, err := decodeClientPacketPayload(StatePlay, id, trailing); err == nil {
				t.Fatal("尾随字节被接受")
			}
		})
	}

	servers := []struct {
		name   string
		packet ServerPacket
		bytes  int
	}{
		{"state", FurnaceState{
			Furnace:       ref,
			Input:         core.ItemStack{Item: core.ItemRawIron, Count: 7},
			Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 2},
			Output:        core.ItemStack{Item: core.ItemIronIngot, Count: 5},
			ProgressTicks: 137, BurnTicks: 1463,
		}, 29},
		{"closed", FurnaceClosed{Furnace: ref}, 17},
	}
	for _, tc := range servers {
		t.Run(tc.name, func(t *testing.T) {
			id, payload, err := encodeServerControlPayload(StatePlay, tc.packet)
			if err != nil || len(payload) != tc.bytes {
				t.Fatalf("%T payload=%d err=%v，想要 %d 字节", tc.packet, len(payload), err, tc.bytes)
			}
			round, err := decodeServerControlPayload(StatePlay, id, payload)
			if err != nil || round != tc.packet {
				t.Fatalf("round=%#v err=%v", round, err)
			}
			for length := range len(payload) {
				if _, err := decodeServerControlPayload(StatePlay, id, payload[:length]); err == nil {
					t.Fatalf("截断到 %d 字节仍被接受", length)
				}
			}
		})
	}
}

func TestFurnaceMessagesRejectInvalidValues(t *testing.T) {
	ref := testFurnaceRef()
	clients := []ClientPacket{
		OpenFurnace{Yaw: float32(math.NaN())},
		OpenFurnace{Pitch: float32(math.Inf(1))},
		MoveFurnaceStack{Furnace: ref, From: core.FurnaceViewSlots, To: 0},
		MoveFurnaceStack{Furnace: ref, From: 0, To: core.FurnaceViewSlots},
		MoveFurnaceStack{Furnace: ref, From: 3, To: 3},
		// 输出格只能作为来源。
		MoveFurnaceStack{Furnace: ref, From: 0, To: core.FurnaceOutputSlot},
		// generation 为 0 的引用永远不可能有效。
		MoveFurnaceStack{Furnace: core.FurnaceRef{Dimension: core.Overworld}, From: 0, To: 36},
		// 非 Overworld 维度。
		MoveFurnaceStack{
			Furnace: core.FurnaceRef{Dimension: core.DimensionID(1), Generation: 1},
			From:    0, To: 36,
		},
		// 槽位越界。
		MoveFurnaceStack{
			Furnace: core.FurnaceRef{
				Dimension: core.Overworld, Slot: core.FurnacesPerChunk, Generation: 1,
			},
			From: 0, To: 36,
		},
	}
	for _, packet := range clients {
		if _, _, err := encodeClientPacketPayload(StatePlay, packet); err == nil {
			t.Fatalf("非法客户端熔炉消息被编码: %#v", packet)
		}
	}

	servers := []ServerPacket{
		FurnaceState{Furnace: core.FurnaceRef{Dimension: core.Overworld}},
		FurnaceState{Furnace: ref, Input: core.ItemStack{Item: core.ItemCoal, Count: 1}},
		FurnaceState{Furnace: ref, Fuel: core.ItemStack{Item: core.ItemRawIron, Count: 1}},
		FurnaceState{Furnace: ref, Output: core.ItemStack{Item: core.ItemStone, Count: 1}},
		FurnaceState{Furnace: ref, ProgressTicks: core.FurnaceSmeltTicks},
		FurnaceState{Furnace: ref, BurnTicks: core.FurnaceBurnTicks + 1},
		FurnaceClosed{Furnace: core.FurnaceRef{Dimension: core.Overworld}},
	}
	for _, packet := range servers {
		if _, _, err := encodeServerControlPayload(StatePlay, packet); err == nil {
			t.Fatalf("非法服务端熔炉消息被编码: %#v", packet)
		}
	}
}

func TestFurnaceDecodeRejectsUnknownWireValues(t *testing.T) {
	ref := testFurnaceRef()
	moveID, _ := clientPacketID(StatePlay, MoveFurnaceStack{})
	_, payload, err := encodeClientPacketPayload(StatePlay, MoveFurnaceStack{
		Sequence: 1, Furnace: ref, From: 0, To: core.FurnaceInputSlot,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 最后两个字节是 from/to；改成越界值必须被拒绝。
	corrupted := append([]byte(nil), payload...)
	corrupted[len(corrupted)-1] = core.FurnaceViewSlots
	if _, err := decodeClientPacketPayload(StatePlay, moveID, corrupted); err == nil {
		t.Fatal("越界目标索引被接受")
	}

	stateID, _ := serverPacketID(StatePlay, FurnaceState{})
	_, statePayload, err := encodeServerControlPayload(StatePlay, FurnaceState{
		Furnace: ref, ProgressTicks: 10, BurnTicks: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	badProgress := append([]byte(nil), statePayload...)
	badProgress[len(badProgress)-3] = core.FurnaceSmeltTicks
	if _, err := decodeServerControlPayload(StatePlay, stateID, badProgress); err == nil {
		t.Fatal("越界进度被接受")
	}
}

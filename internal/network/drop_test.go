package network

import (
	"context"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func dropTestID(slot uint8, generation uint32) core.DropID {
	return core.DropID{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: 1, Z: -2},
		Slot:       slot,
		Generation: generation,
	}
}

func dropTestUpsert(slot uint8, generation uint32, count uint8) ItemDrop {
	return ItemDrop{
		ID: dropTestID(slot, generation), BlockIndex: 9,
		Item: core.ItemStone, Count: count,
	}
}

func TestProtocolV4DropPacketIDsAreFrozen(t *testing.T) {
	assertServerRegistry(t, []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{
		{StatePlay, ItemDropUpserts{}, 11},
		{StatePlay, ItemDropRemoves{}, 12},
	})
}

func TestProtocolV4DropGolden(t *testing.T) {
	tests := []struct {
		packet  ServerPacket
		wantID  uint32
		wantHex string
	}{
		{
			ItemDropUpserts{ServerTick: 5, Drops: []ItemDrop{dropTestUpsert(3, 7, 4)}},
			11,
			"05000000000000000100000000010000" + "00feffffff0307000000090000000100" + "040000",
		},
		{
			ItemDropRemoves{ServerTick: 6, IDs: []core.DropID{dropTestID(3, 7)}},
			12,
			"06000000000000000100000000010000" + "00feffffff0307000000",
		},
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
		for length := 0; length < len(payload); length++ {
			if _, err := decodeServerControlPayload(StatePlay, packetID, payload[:length]); err == nil {
				t.Fatalf("truncated %T at %d accepted", test.packet, length)
			}
		}
		if _, err := decodeServerControlPayload(
			StatePlay, packetID, append(append([]byte(nil), payload...), 0),
		); err == nil {
			t.Fatalf("%T trailing byte accepted", test.packet)
		}
	}
}

func TestItemDropMessagesValidateBoundedBatches(t *testing.T) {
	full := make([]ItemDrop, 32)
	fullIDs := make([]core.DropID, 32)
	for index := range full {
		full[index] = dropTestUpsert(uint8(index), 1, 1)
		fullIDs[index] = dropTestID(uint8(index), 1)
	}
	valid := []interface{ Validate() error }{
		ItemDropUpserts{Drops: full},
		ItemDropRemoves{IDs: fullIDs},
	}
	for _, message := range valid {
		if err := message.Validate(); err != nil {
			t.Fatalf("%T 合法批次被拒绝: %v", message, err)
		}
	}

	unsorted := []ItemDrop{dropTestUpsert(2, 1, 1), dropTestUpsert(1, 1, 1)}
	duplicate := []ItemDrop{dropTestUpsert(1, 1, 1), dropTestUpsert(1, 1, 2)}
	stonePickaxeDurability, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	invalid := []interface{ Validate() error }{
		ItemDropUpserts{},
		ItemDropUpserts{Drops: append(append([]ItemDrop(nil), full...), dropTestUpsert(31, 2, 1))},
		ItemDropUpserts{Drops: unsorted},
		ItemDropUpserts{Drops: duplicate},
		ItemDropUpserts{Drops: []ItemDrop{{ID: dropTestID(0, 0), Item: core.ItemStone, Count: 1}}},
		ItemDropUpserts{Drops: []ItemDrop{{ID: dropTestID(0, 1), Item: core.ItemID(4242), Count: 1}}},
		ItemDropUpserts{Drops: []ItemDrop{{ID: dropTestID(0, 1), Item: core.ItemStone, Count: 0}}},
		ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), Item: core.ItemStone, Count: core.MaxStackCount + 1,
		}}},
		ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), Item: core.ItemStone, Count: 1, BlockIndex: maxChunkBlockIndex,
		}}},
		ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), Item: core.ItemStonePickaxe, Count: 2,
			Durability: stonePickaxeDurability,
		}}},
		ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), Item: core.ItemStonePickaxe, Count: 1,
		}}},
		ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), Item: core.ItemStonePickaxe, Count: 1,
			Durability: stonePickaxeDurability + 1,
		}}},
		ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), Item: core.ItemStone, Count: 1, Durability: 1,
		}}},
		ItemDropRemoves{},
		ItemDropRemoves{IDs: append(append([]core.DropID(nil), fullIDs...), dropTestID(31, 2))},
		ItemDropRemoves{IDs: []core.DropID{dropTestID(2, 1), dropTestID(1, 1)}},
		ItemDropRemoves{IDs: []core.DropID{dropTestID(1, 1), dropTestID(1, 1)}},
		ItemDropRemoves{IDs: []core.DropID{dropTestID(0, 0)}},
	}
	for index, message := range invalid {
		if err := message.Validate(); err == nil {
			t.Fatalf("非法批次 %d 被接受: %T %+v", index, message, message)
		}
	}
}

func TestProtocolV16ToolDropUsesFiveByteStackWire(t *testing.T) {
	if ProtocolVersion != 20 {
		t.Fatalf("协议版本 = %d，想要 20", ProtocolVersion)
	}
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	packet := ItemDropUpserts{ServerTick: 5, Drops: []ItemDrop{{
		ID: dropTestID(3, 7), BlockIndex: 9,
		Item: core.ItemStonePickaxe, Count: 1, Durability: full - 7,
	}}}
	packetID, payload, err := encodeServerControlPayload(StatePlay, packet)
	if err != nil {
		t.Fatalf("编码磨损工具掉落物: %v", err)
	}
	if packetID != 11 || len(payload) != 8+1+itemDropWireBytes {
		t.Fatalf("v11 工具掉落物 id=%d wire=%d，想要 id=11 wire=%d", packetID, len(payload), 8+1+itemDropWireBytes)
	}
	decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil || !reflect.DeepEqual(decoded, packet) {
		t.Fatalf("v11 工具掉落物往返 = %#v，error=%v，想要 %#v", decoded, err, packet)
	}
}

func TestProtocolV11CarriesWornToolDropOnCodecAndMemory(t *testing.T) {
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	worn := ItemDropUpserts{Drops: []ItemDrop{{
		ID: dropTestID(0, 1), BlockIndex: 9,
		Item: core.ItemStonePickaxe, Count: 1, Durability: full - 1,
	}}}
	if err := worn.Validate(); err != nil {
		t.Fatalf("v11 拒绝磨损工具掉落物: %v", err)
	}
	packetID, payload, err := encodeServerControlPayload(StatePlay, worn)
	if err != nil {
		t.Fatalf("v11 codec 拒绝磨损工具掉落物: %v", err)
	}
	round, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil || !reflect.DeepEqual(round, worn) {
		t.Fatalf("磨损工具掉落物往返 = %#v, %v，想要 %#v", round, err, worn)
	}
	client, server := NewMemoryStreamPair(1)
	t.Cleanup(func() { _ = client.Close() })
	if err := server.Send(context.Background(), StatePlay, worn); err != nil {
		t.Fatalf("Memory transport 拒绝磨损工具掉落物: %v", err)
	}
}

func TestItemDropDecodeRejectsOversizedCountBeforeAllocation(t *testing.T) {
	// 计数 33 超过固定上限，且剩余字节不足以支撑该计数。
	oversized := append([]byte{6, 0, 0, 0, 0, 0, 0, 0}, 33)
	for _, packetID := range []uint32{11, 12} {
		if _, err := decodeServerControlPayload(StatePlay, packetID, oversized); err == nil {
			t.Fatalf("packet %d 接受了超限计数", packetID)
		}
	}
}

func TestBlockChangesAllowZeroChangeRevisionBarrier(t *testing.T) {
	barrier := BlockChanges{
		Dimension: core.Overworld, Chunk: core.ChunkPos{X: 1, Z: -2},
		BaseRevision: 4, NewRevision: 5,
	}
	if err := barrier.Validate(); err != nil {
		t.Fatalf("零方块 revision barrier 被拒绝: %v", err)
	}
	packetID, payload, err := encodeServerControlPayload(StatePlay, barrier)
	if err != nil {
		t.Fatalf("编码 barrier: %v", err)
	}
	decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil {
		t.Fatalf("解码 barrier: %v", err)
	}
	got, ok := decoded.(BlockChanges)
	if !ok || got.BaseRevision != 4 || got.NewRevision != 5 || len(got.Changes) != 0 {
		t.Fatalf("barrier 往返 = %#v", decoded)
	}
}

func TestItemDropUpsertsAcceptEveryRegisteredItem(t *testing.T) {
	for _, item := range []core.ItemID{
		core.ItemCoal,
		core.ItemRawIron,
		core.ItemIronIngot,
		core.ItemStonePickaxe,
		core.ItemIronPickaxe,
	} {
		durability, _ := core.ItemMaxDurability(item)
		message := ItemDropUpserts{Drops: []ItemDrop{{
			ID: dropTestID(0, 1), BlockIndex: 9, Item: item, Count: 1, Durability: durability,
		}}}
		if err := message.Validate(); err != nil {
			t.Fatalf("已注册物品 %d 被拒绝: %v", item, err)
		}
		packetID, payload, err := encodeServerControlPayload(StatePlay, message)
		if err != nil {
			t.Fatalf("编码已注册物品 %d: %v", item, err)
		}
		if _, err := decodeServerControlPayload(StatePlay, packetID, payload); err != nil {
			t.Fatalf("解码已注册物品 %d: %v", item, err)
		}
	}
}

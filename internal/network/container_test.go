package network

import (
	"encoding/hex"
	"reflect"
	"testing"

	"minecraft-go/internal/core"
)

func testChestRef() core.ContainerRef {
	return core.ContainerRef{
		Dimension:  core.Overworld,
		Chunk:      core.ChunkPos{X: -3, Z: 7},
		Kind:       core.ContainerKindChest,
		Slot:       5,
		Generation: 9,
	}
}

func testChestItems() [core.ChestSlots]core.ItemStack {
	var items [core.ChestSlots]core.ItemStack
	items[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
	return items
}

// chestStateGoldenHex 手工拼出与 encodeContainerRef/encodeItemStack 完全对应的固定字节，
// 用于捕获字段顺序、宽度或字节序上的静默漂移。
func chestStateGoldenHex() string {
	ref := "00000000" + "fdffffff" + "07000000" + "01" + "05" + "09000000"
	hex := ref + "0100050000"
	for range core.ChestSlots - 1 {
		hex += "0000000000"
	}
	return hex
}

// TestProtocolV12ChestStatePacketIDIsFrozen 锁定 ChestState 使用新分配的 packet ID 15，
// 且它之外的 ID 仍然保持未分配。
func TestProtocolV12ChestStatePacketIDIsFrozen(t *testing.T) {
	assertServerRegistry(t, []struct {
		state  State
		packet ServerPacket
		id     uint32
	}{
		{StatePlay, ChestState{}, 15},
	})
	if _, ok := serverPacketForID(StatePlay, 16); ok {
		t.Fatal("unknown play server packet ID accepted")
	}
}

// TestProtocolV12ChestStateGolden 覆盖 ChestState 的固定 153 字节布局：
// 18 字节容器引用加 27 × 5 字节格子，并验证截断与尾随字节都被拒绝。
func TestProtocolV12ChestStateGolden(t *testing.T) {
	packet := ChestState{Chest: testChestRef(), Items: testChestItems()}
	wantHex := chestStateGoldenHex()

	packetID, payload, err := encodeServerControlPayload(StatePlay, packet)
	if err != nil || packetID != 15 || len(payload) != 153 || hex.EncodeToString(payload) != wantHex {
		t.Fatalf("ChestState id=%d len=%d payload=%x err=%v，想要 id=15 len=153 hex=%s",
			packetID, len(payload), payload, err, wantHex)
	}
	decoded, err := decodeServerControlPayload(StatePlay, packetID, payload)
	if err != nil || !reflect.DeepEqual(decoded, packet) {
		t.Fatalf("round=%#v err=%v", decoded, err)
	}
	for length := 0; length < len(payload); length++ {
		if _, err := decodeServerControlPayload(StatePlay, packetID, payload[:length]); err == nil {
			t.Fatalf("截断到 %d 字节仍被接受", length)
		}
	}
	if _, err := decodeServerControlPayload(
		StatePlay, packetID, append(append([]byte(nil), payload...), 0),
	); err == nil {
		t.Fatal("尾随字节被接受")
	}
}

// TestChestStateRejectsInvalidValues 覆盖箱子专属拒绝路径：非箱子种类的引用、
// 越界统一索引之外的非法格与非箱子种类的整堆移动统一索引上限。
func TestChestStateRejectsInvalidValues(t *testing.T) {
	ref := testChestRef()
	invalidStack := core.ItemStack{Item: core.ItemID(0xffff), Count: 1}

	states := []ChestState{
		// Kind 是熔炉（零值）而不是箱子。
		{Chest: core.ContainerRef{Dimension: core.Overworld, Generation: 1}},
		// Kind 是既不是熔炉也不是箱子的未知值。
		{Chest: core.ContainerRef{Dimension: core.Overworld, Kind: core.ContainerKind(7), Generation: 1}},
		// 槽位越界（ChestsPerChunk = 16）。
		{Chest: core.ContainerRef{
			Dimension: core.Overworld, Kind: core.ContainerKindChest,
			Slot: core.ChestsPerChunk, Generation: 1,
		}},
		// generation 为 0 的引用永远不可能有效。
		{Chest: core.ContainerRef{Dimension: core.Overworld, Kind: core.ContainerKindChest}},
		// 非 Overworld 维度。
		{Chest: core.ContainerRef{Dimension: core.DimensionID(1), Kind: core.ContainerKindChest, Generation: 1}},
		// 非法格子内容。
		{Chest: ref, Items: func() [core.ChestSlots]core.ItemStack {
			var items [core.ChestSlots]core.ItemStack
			items[3] = invalidStack
			return items
		}()},
	}
	for _, state := range states {
		if _, _, err := encodeServerControlPayload(StatePlay, state); err == nil {
			t.Fatalf("非法箱子状态被编码: %#v", state)
		}
	}
}

// TestMoveContainerStackChestUnifiedSlotRange 覆盖箱子统一栏位 0..62：
// 63 及以上必须被拒绝，62 是最后一个合法索引。
func TestMoveContainerStackChestUnifiedSlotRange(t *testing.T) {
	ref := testChestRef()
	if err := (MoveContainerStack{Container: ref, From: 0, To: core.ChestViewSlots - 1}).Validate(); err != nil {
		t.Fatalf("箱子统一栏位上限 %d 被拒绝: %v", core.ChestViewSlots-1, err)
	}
	if err := (MoveContainerStack{Container: ref, From: 0, To: core.ChestViewSlots}).Validate(); err == nil {
		t.Fatalf("箱子越界统一栏位 %d 被接受", core.ChestViewSlots)
	}
	// 箱子没有输出格限制：熔炉的输出格索引在箱子这里是普通格。
	if err := (MoveContainerStack{
		Container: ref, From: 0, To: core.FurnaceOutputSlot,
	}).Validate(); err != nil {
		t.Fatalf("箱子格 %d 被错误地当成熔炉输出格拒绝: %v", core.FurnaceOutputSlot, err)
	}
}

// TestContainerNeutralMessagesRejectUnknownKind 覆盖容器中性消息（打开命令除外，
// 它本身不携带引用）对未知种类枚举值的一致拒绝。
func TestContainerNeutralMessagesRejectUnknownKind(t *testing.T) {
	unknown := core.ContainerRef{Dimension: core.Overworld, Kind: core.ContainerKind(9), Generation: 1}
	if err := (MoveContainerStack{Container: unknown, From: 0, To: 1}).Validate(); err == nil {
		t.Fatal("MoveContainerStack 接受了未知容器种类")
	}
	if err := (ContainerClosed{Container: unknown}).Validate(); err == nil {
		t.Fatal("ContainerClosed 接受了未知容器种类")
	}
}

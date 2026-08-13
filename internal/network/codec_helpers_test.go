package network

import (
	"encoding/hex"
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

func remotePlayerStatesWireFixture(ids ...core.PlayerID) []byte {
	payload := make([]byte, 9)
	payload[8] = byte(len(ids))
	for _, id := range ids {
		payload = append(payload, id[:]...)
		payload = append(payload, make([]byte, 25)...)
	}
	return payload
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

// goldenInventoryStateHex 是 1 字节选中栏位加 36 格固定编码，共 181 字节。
func goldenInventoryStateHex() string {
	empty := "0000000000"
	hex := "02" + "0100050000" + empty + empty + empty + "0300400000"
	for range 4 {
		hex += empty
	}
	hex += "0200010000"
	for range core.BackpackSlots - 2 {
		hex += empty
	}
	return hex + "0100090000"
}

// inventoryStateWire 手工构造固定负载，用于绕过编码器校验注入非法状态。
func inventoryStateWire(inventory core.Inventory) []byte {
	wire := make([]byte, 0, 1+core.InventorySlots*5)
	wire = append(wire, inventory.Hotbar.Selected)
	appendStack := func(stack core.ItemStack) {
		wire = append(wire,
			byte(stack.Item), byte(stack.Item>>8), stack.Count,
			byte(stack.Durability), byte(stack.Durability>>8),
		)
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
	case DropSelectedItem:
		other, ok := want.(DropSelectedItem)
		return ok && got == other
	case CraftRecipe:
		other, ok := want.(CraftRecipe)
		return ok && got == other
	case OpenContainer:
		other, ok := want.(OpenContainer)
		return ok && got == other
	case MoveContainerStack:
		other, ok := want.(MoveContainerStack)
		return ok && got == other
	case CloseContainer:
		other, ok := want.(CloseContainer)
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

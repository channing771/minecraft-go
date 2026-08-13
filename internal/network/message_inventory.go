package network

import (
	"errors"

	"github.com/channing771/mornlea/internal/core"
)

// InventoryState 是服务端发给所属玩家的完整权威物品状态。
type InventoryState struct {
	Inventory core.Inventory
}

func (InventoryState) serverMessage() {}
func (InventoryState) serverPacket()  {}

func (state InventoryState) Validate() error {
	if !state.Inventory.Valid() {
		return errors.New("network: inventory state is not a valid fixed inventory")
	}
	return nil
}

// MoveInventoryStack 请求在 36 格之间整堆移动。
type MoveInventoryStack struct {
	Sequence uint64
	From, To uint8
}

func (MoveInventoryStack) clientMessage() {}
func (MoveInventoryStack) clientPacket()  {}

func (command MoveInventoryStack) Validate() error {
	if command.From >= core.InventorySlots || command.To >= core.InventorySlots {
		return errors.New("network: inventory move slot is outside 0..35")
	}
	if command.From == command.To {
		return errors.New("network: inventory move source equals target")
	}
	return nil
}

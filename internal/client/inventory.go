package client

import (
	"errors"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

// InventoryMirror 是权威物品状态的固定只读镜像。
// 它由客户端主线程独占，只接受完整且有效的服务端状态，不做本地预测。
type InventoryMirror struct {
	inventory core.Inventory
	confirmed bool
}

// Apply 用一份完整权威状态替换镜像；非法状态被整包拒绝且不部分应用。
func (mirror *InventoryMirror) Apply(state network.InventoryState) error {
	if mirror == nil {
		return errors.New("client: nil inventory mirror")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	mirror.inventory = state.Inventory
	mirror.confirmed = true
	return nil
}

// State 返回最后一个已确认的完整权威物品状态。
func (mirror *InventoryMirror) State() (core.Inventory, bool) {
	if mirror == nil {
		return core.Inventory{}, false
	}
	return mirror.inventory, mirror.confirmed
}

// Hotbar 返回已确认物品状态中的快捷栏，供 HUD 使用。
func (mirror *InventoryMirror) Hotbar() (core.Hotbar, bool) {
	inventory, ok := mirror.State()
	return inventory.Hotbar, ok
}

// Reset 丢弃上一个会话的状态；重连前不得继承任何已确认值。
func (mirror *InventoryMirror) Reset() {
	if mirror == nil {
		return
	}
	*mirror = InventoryMirror{}
}

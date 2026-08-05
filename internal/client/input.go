package client

import "minecraft-go/internal/core"

type Movement struct {
	MoveX int8
	MoveZ int8
	Jump  bool
}

func MovementFromKeys(w, a, s, d, jump bool) Movement {
	var movement Movement
	if d {
		movement.MoveX++
	}
	if a {
		movement.MoveX--
	}
	if w {
		movement.MoveZ++
	}
	if s {
		movement.MoveZ--
	}
	movement.Jump = jump
	return movement
}

// Actions 是一帧内需要上行的意图。选择只发送请求，
// 客户端不据此改写任何已确认的权威快捷栏状态。
type Actions struct {
	Mining     bool
	Place      bool
	Select     bool
	SelectSlot uint8
	// ToggleInventory 是 E 键的上升沿；界面开关只影响本地输入路由。
	ToggleInventory bool
	// Click 是背包界面打开时的左键上升沿。
	Click bool
	// Drop 是 Q 的有效上升沿：请求丢弃权威选中栏位中的一个物品。
	Drop bool
}

type InputState struct {
	primaryDown   bool
	secondaryDown bool
	numberDown    int
	inventoryDown bool
	dropDown      bool
}

// Update 把数字键 1..9 转换为一次快捷栏选择请求，把 E 与 Q 的上升沿分别转换为
// 界面开关和丢弃请求。inventoryOpen 为 true 时抑制挖掘、放置、快捷栏选择和丢弃，
// 只保留界面点击。
func (state *InputState) Update(
	primary, secondary bool,
	number int,
	inventoryKey, dropKey, inventoryOpen bool,
) Actions {
	rising := primary && !state.primaryDown
	actions := Actions{
		ToggleInventory: inventoryKey && !state.inventoryDown,
		// 界面打开时抑制丢弃，但下方仍记录 Q 的物理状态，
		// 使抑制期间按住的 Q 在恢复后不会被当成新的上升沿。
		Drop: dropKey && !state.dropDown && !inventoryOpen,
	}
	if inventoryOpen {
		actions.Click = rising
	} else {
		actions.Mining = primary
		actions.Place = secondary && !state.secondaryDown
		if number >= 1 && number <= core.HotbarSlots && number != state.numberDown {
			actions.Select = true
			actions.SelectSlot = uint8(number - 1)
		}
	}
	state.primaryDown = primary
	state.secondaryDown = secondary
	state.numberDown = number
	state.inventoryDown = inventoryKey
	state.dropDown = dropKey
	return actions
}

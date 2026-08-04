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
	Break      bool
	Place      bool
	Select     bool
	SelectSlot uint8
	// ToggleInventory 是 E 键的上升沿；界面开关只影响本地输入路由。
	ToggleInventory bool
	// Click 是背包界面打开时的左键上升沿。
	Click bool
}

type InputState struct {
	primaryDown   bool
	secondaryDown bool
	numberDown    int
	inventoryDown bool
}

// Update 把数字键 1..9 转换为一次快捷栏选择请求，把 E 的上升沿转换为界面开关。
// inventoryOpen 为 true 时抑制挖掘、放置和快捷栏选择，只保留界面点击。
func (state *InputState) Update(
	primary, secondary bool,
	number int,
	inventoryKey, inventoryOpen bool,
) Actions {
	rising := primary && !state.primaryDown
	actions := Actions{
		ToggleInventory: inventoryKey && !state.inventoryDown,
	}
	if inventoryOpen {
		actions.Click = rising
	} else {
		actions.Break = rising
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
	return actions
}

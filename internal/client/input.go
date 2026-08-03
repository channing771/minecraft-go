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
}

type InputState struct {
	primaryDown   bool
	secondaryDown bool
	numberDown    int
}

// Update 把数字键 1..9 转换为一次快捷栏选择请求；
// number 为 0 或超出 1..9 时不产生选择。
func (state *InputState) Update(primary, secondary bool, number int) Actions {
	actions := Actions{
		Break: primary && !state.primaryDown,
		Place: secondary && !state.secondaryDown,
	}
	if number >= 1 && number <= core.HotbarSlots && number != state.numberDown {
		actions.Select = true
		actions.SelectSlot = uint8(number - 1)
	}
	state.primaryDown = primary
	state.secondaryDown = secondary
	state.numberDown = number
	return actions
}

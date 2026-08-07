package client

import (
	"errors"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

// FurnaceMirror 是当前查看熔炉的固定只读镜像。
// 它由客户端主线程独占，只接受服务端确认的完整状态，不做本地预测。
type FurnaceMirror struct {
	state  network.FurnaceState
	opened bool
}

// Apply 用一份完整权威熔炉状态替换镜像；非法状态被整包拒绝且不部分应用。
// 引用不同的新状态直接替换旧界面，因此重新放置的熔炉不会沿用旧值。
func (mirror *FurnaceMirror) Apply(state network.FurnaceState) error {
	if mirror == nil {
		return errors.New("client: nil furnace mirror")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	mirror.state = state
	mirror.opened = true
	return nil
}

// Close 处理服务端关闭通知；引用与当前界面不一致的通知被忽略。
func (mirror *FurnaceMirror) Close(closed network.ContainerClosed) error {
	if mirror == nil {
		return errors.New("client: nil furnace mirror")
	}
	if err := closed.Validate(); err != nil {
		return err
	}
	if !mirror.opened || mirror.state.Furnace != closed.Container {
		return nil
	}
	*mirror = FurnaceMirror{}
	return nil
}

// State 返回最后一个已确认的完整熔炉状态副本。
func (mirror *FurnaceMirror) State() (network.FurnaceState, bool) {
	if mirror == nil {
		return network.FurnaceState{}, false
	}
	return mirror.state, mirror.opened
}

// Ref 返回当前查看的熔炉引用，供后续命令使用。
func (mirror *FurnaceMirror) Ref() (core.FurnaceRef, bool) {
	state, ok := mirror.State()
	return state.Furnace, ok
}

// Reset 丢弃上一个会话或上一个界面的状态。
func (mirror *FurnaceMirror) Reset() {
	if mirror == nil {
		return
	}
	*mirror = FurnaceMirror{}
}

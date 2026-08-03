package client

import (
	"errors"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

// HotbarMirror 是权威快捷栏的固定只读镜像。
// 它由客户端主线程独占，只接受完整且有效的服务端状态，不做本地预测。
type HotbarMirror struct {
	hotbar    core.Hotbar
	confirmed bool
}

// Apply 用一份完整权威状态替换镜像；非法状态被整包拒绝且不部分应用。
func (mirror *HotbarMirror) Apply(state network.HotbarState) error {
	if mirror == nil {
		return errors.New("client: nil hotbar mirror")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	mirror.hotbar = state.Hotbar
	mirror.confirmed = true
	return nil
}

// State 返回最后一个已确认的权威快捷栏。
func (mirror *HotbarMirror) State() (core.Hotbar, bool) {
	if mirror == nil {
		return core.Hotbar{}, false
	}
	return mirror.hotbar, mirror.confirmed
}

// Reset 丢弃上一个会话的状态；重连前不得继承任何已确认值。
func (mirror *HotbarMirror) Reset() {
	if mirror == nil {
		return
	}
	*mirror = HotbarMirror{}
}

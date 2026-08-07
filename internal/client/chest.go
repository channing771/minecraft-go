package client

import (
	"errors"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
)

// ChestMirror 是当前查看箱子的固定只读镜像，与 FurnaceMirror 语义完全对称：
// 它由客户端主线程独占，只接受服务端确认的完整状态，不做本地预测。
// 熔炉与箱子各自持有一份镜像是有意的：项目决定不引入通用 Container 接口，
// 两个具体容器的字段与校验规则差异明确，接口只会把可以直接内联的分支变成间接层。
type ChestMirror struct {
	state  network.ChestState
	opened bool
}

// Apply 用一份完整权威箱子状态替换镜像；非法状态被整包拒绝且不部分应用。
// 新引用直接替换旧界面，因此重新放置的箱子不会沿用旧值。
func (mirror *ChestMirror) Apply(state network.ChestState) error {
	if mirror == nil {
		return errors.New("client: nil chest mirror")
	}
	if err := state.Validate(); err != nil {
		return err
	}
	mirror.state = state
	mirror.opened = true
	return nil
}

// Close 处理服务端关闭通知；引用与当前界面不一致的通知被忽略，
// 因此熔炉的关闭通知不会影响正在查看的箱子界面，反之亦然。
func (mirror *ChestMirror) Close(closed network.ContainerClosed) error {
	if mirror == nil {
		return errors.New("client: nil chest mirror")
	}
	if err := closed.Validate(); err != nil {
		return err
	}
	if !mirror.opened || mirror.state.Chest != closed.Container {
		return nil
	}
	*mirror = ChestMirror{}
	return nil
}

// State 返回最后一个已确认的完整箱子状态副本。
func (mirror *ChestMirror) State() (network.ChestState, bool) {
	if mirror == nil {
		return network.ChestState{}, false
	}
	return mirror.state, mirror.opened
}

// Ref 返回当前查看的箱子引用，供后续命令使用。
func (mirror *ChestMirror) Ref() (core.ContainerRef, bool) {
	state, ok := mirror.State()
	return state.Chest, ok
}

// Reset 丢弃上一个会话或上一个界面的状态。
func (mirror *ChestMirror) Reset() {
	if mirror == nil {
		return
	}
	*mirror = ChestMirror{}
}

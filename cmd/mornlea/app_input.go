//go:build darwin

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render/hud"
)

// dropSelectedItem 请求把权威选中栏位中的一个物品丢到脚下。
// 客户端不预测：不读也不改本地背包镜像，不创建本地掉落物。
func (a *application) dropSelectedItem() {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	if err := a.send(network.DropSelectedItem{Sequence: a.nextSequence()}); err != nil {
		slog.Warn("发送主动丢弃请求失败", "error", err)
	}
}

func (a *application) placeBlock() {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	hit, found, err := core.RaycastBlocks(
		a.camera.Pos,
		a.camera.Forward(),
		6,
		func(position core.BlockPos) (bool, error) {
			block, loaded := a.mirror.BlockAt(core.Overworld, position)
			return loaded && core.InteractionTarget(block), nil
		},
	)
	if err != nil {
		slog.Warn("本地容器射线失败", "error", err)
	} else if found {
		block, loaded := a.mirror.BlockAt(core.Overworld, hit.Block)
		if loaded && (block == core.FurnaceID || block == core.ChestID) {
			// 服务端才是权威射线：这里只按本地镜像的方块类型决定发哪种请求，
			// 具体命中的是熔炉还是箱子由服务端重新判定。
			if err := a.send(network.OpenContainer{
				Sequence: a.nextSequence(), Yaw: a.camera.Yaw, Pitch: a.camera.Pitch,
			}); err != nil {
				slog.Warn("发送打开容器请求失败", "error", err)
			}
			return
		}
	}
	// 放置引用最后一个已确认的选中栏位；尚未确认时不发送。
	hotbar, confirmed := a.inventory.Hotbar()
	if !confirmed {
		return
	}
	if err := a.send(network.PlaceBlock{
		Sequence: a.nextSequence(),
		Yaw:      a.camera.Yaw,
		Pitch:    a.camera.Pitch,
		Slot:     hotbar.Selected,
	}); err != nil {
		slog.Warn("发送放置命令失败", "error", err)
	}
}

// containerOpen 报告是否有已确认的容器镜像（熔炉或箱子）正在驱动当前界面。
// 两个镜像互斥：Apply 一个的同时会 Reset 另一个，因此至多一个返回 true。
func (a *application) containerOpen() bool {
	if _, opened := a.furnace.State(); opened {
		return true
	}
	_, opened := a.chest.State()
	return opened
}

// setInventoryOpen 切换容器界面：显式关闭时立即清理并通知服务端。
func (a *application) setInventoryOpen(open bool) {
	if !open && a.containerOpen() {
		a.clearContainerUI()
		if err := a.send(network.CloseContainer{Sequence: a.nextSequence()}); err != nil {
			slog.Warn("发送关闭容器请求失败", "error", err)
		}
		return
	}
	a.inventoryOpen = open
	a.inventorySource = -1
	if a.window != nil {
		a.window.SetCursorCaptured(!open)
	}
	if open {
		// 立即发送一次中性输入，清除服务端保留的上一帧移动。
		a.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)
	}
}

// clearContainerUI 丢弃当前熔炉与箱子镜像并关闭容器界面，不发送协议消息。
func (a *application) clearContainerUI() {
	a.furnace.Reset()
	a.chest.Reset()
	a.inventoryOpen = false
	a.inventorySource = -1
	if a.window != nil {
		a.window.SetCursorCaptured(true)
	}
}

// clickInventorySlot 处理固定配方按钮，或用两次有效栏位点击组成一次整堆移动请求。
func (a *application) clickInventorySlot(cursorX, cursorY float64, width, height uint32) {
	furnace, furnaceOpen := a.furnace.State()
	chest, chestOpen := a.chest.State()
	if !furnaceOpen && !chestOpen {
		if recipe, ok := hud.RecipeButtonAt(cursorX, cursorY, width, height); ok {
			inventory, confirmed := a.inventory.State()
			if !confirmed {
				return
			}
			if _, craftable := inventory.Craft(recipe); !craftable {
				return
			}
			a.inventorySource = -1
			if err := a.send(network.CraftRecipe{
				Sequence: a.nextSequence(), Recipe: recipe,
			}); err != nil {
				slog.Warn("发送合成请求失败", "error", err)
			}
			return
		}
	}

	var slot uint8
	var ok bool
	switch {
	case chestOpen:
		slot, ok = hud.ChestSlotAt(cursorX, cursorY, width, height)
	case furnaceOpen:
		slot, ok = hud.FurnaceSlotAt(cursorX, cursorY, width, height)
	default:
		slot, ok = hud.InventorySlotAt(cursorX, cursorY, width, height)
	}
	if !ok {
		return
	}
	if a.inventorySource < 0 {
		a.inventorySource = int(slot)
		return
	}
	from := uint8(a.inventorySource)
	a.inventorySource = -1
	if from == slot {
		return
	}
	if chestOpen {
		if err := a.send(network.MoveContainerStack{
			Sequence: a.nextSequence(), Container: chest.Chest, From: from, To: slot,
		}); err != nil {
			slog.Warn("发送箱子移动失败", "error", err)
		}
		return
	}
	if furnaceOpen {
		if slot == core.FurnaceOutputSlot {
			return
		}
		if err := a.send(network.MoveContainerStack{
			Sequence: a.nextSequence(), Container: furnace.Furnace, From: from, To: slot,
		}); err != nil {
			slog.Warn("发送熔炉移动失败", "error", err)
		}
		return
	}
	if err := a.send(network.MoveInventoryStack{
		Sequence: a.nextSequence(), From: from, To: slot,
	}); err != nil {
		slog.Warn("发送背包移动失败", "error", err)
	}
}

// selectHotbarSlot 只发送选择请求，不本地改写快捷栏镜像。
func (a *application) selectHotbarSlot(slot uint8) {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	if err := a.send(network.SelectHotbar{
		Sequence: a.nextSequence(),
		Slot:     slot,
	}); err != nil {
		slog.Warn("发送快捷栏选择失败", "error", err)
	}
}

func (a *application) send(message network.ClientMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return a.clientEndpoint.Send(ctx, message)
}

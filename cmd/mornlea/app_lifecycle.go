//go:build darwin

package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render/hud"
)

func (a *application) Close() error {
	a.closeOnce.Do(func() {
		a.closeClientSession(nil)
		a.closeErr = errors.Join(a.closeErr, a.clientCloseErr)
		if a.serverCancel != nil {
			a.serverCancel()
		}
		if a.serverDone != nil {
			if err := <-a.serverDone; err != nil && err != context.Canceled {
				a.closeErr = errors.Join(a.closeErr, err)
			}
		}
		if a.releaseResources != nil {
			a.releaseResources()
		}
	})
	return a.closeErr
}

func (a *application) releaseOwnedResources() {
	// layouter(名牌/HUD/面板)无 GPU 资源,无需释放;字形图集停 worker,
	// 渲染器句柄经 Close 归还 Rust。
	if a.glyphAtlas != nil {
		a.glyphAtlas.Release()
	}
	if a.mesher != nil {
		a.mesher.Close()
	}
	// 远环调度器先于渲染器关闭:Close 停 worker 并等待在途生成返回,之后
	// 不再有任何 sink 调用落到已释放的渲染器句柄上。幂等且 nil 安全。
	if a.lodScheduler != nil {
		a.lodScheduler.Close()
	}
	if a.renderer != nil {
		a.renderer.Close()
	}
	if a.window != nil {
		a.window.Close()
	}
}

// closeClientSession closes only the current client endpoint. The embedded
// server belongs to the whole application and is stopped exclusively by Close.
func (a *application) closeClientSession(cause error) {
	a.clientCloseOnce.Do(func() {
		if cause != nil {
			slog.Info("关闭客户端会话", "cause", cause)
		}
		if a.receiver != nil {
			a.clientCloseErr = a.receiver.Close()
		} else if a.clientEndpoint != nil {
			a.clientCloseErr = a.clientEndpoint.Close()
		}
		if a.remotePlayers != nil {
			a.remotePlayers.Reset()
		}
		if a.companions != nil {
			a.companions.Reset()
		}
		if a.chatEvents != nil {
			a.chatEvents.Reset()
		}
		chatWasOpen := a.chatInput.open
		a.chatInput.Cancel()
		a.clearFormattedChatLines()
		// 容量与 client.ChatEventCapacity 同源（E9/C9）：与 application 字段声明
		// 共用同一常量，断线重置不会因字面量漂移而改变缓冲长度。
		a.chatEventBuffer = [client.ChatEventCapacity]network.ChatEvent{}
		if chatWasOpen && a.window != nil {
			a.window.SetCursorCaptured(true)
		}
		a.inventory.Reset()
		a.furnace.Reset()
		a.chest.Reset()
		a.miningOverlay = hud.MiningOverlay{}
		a.damageFeedback.Reset()
		a.damageStrength = 0
		a.inventoryOpen = false
		a.inventorySource = -1
		a.itemDrops.Reset()
		a.clientSessionClosed = true
	})
}

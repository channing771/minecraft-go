//go:build darwin

package main

import (
	"context"
	"errors"
	"log/slog"

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
	if a.debugPanelRenderer != nil {
		a.debugPanelRenderer.Release()
	}
	if a.damageOverlayRenderer != nil {
		a.damageOverlayRenderer.Release()
	}
	if a.blockOutlineRenderer != nil {
		a.blockOutlineRenderer.Release()
	}
	if a.itemDropRenderer != nil {
		a.itemDropRenderer.Release()
	}
	if a.hotbarRenderer != nil {
		a.hotbarRenderer.Release()
	}
	if a.nameTagRenderer != nil {
		a.nameTagRenderer.Release()
	}
	if a.glyphAtlas != nil {
		a.glyphAtlas.Release()
	}
	if a.avatarRenderer != nil {
		a.avatarRenderer.Release()
	}
	if a.renderer != nil {
		a.renderer.Release()
	}
	if a.mesher != nil {
		a.mesher.Close()
	}
	if a.depth != nil {
		a.depth.Release()
	}
	if a.colorView != nil {
		a.colorView.Release()
	}
	if a.color != nil {
		a.color.Release()
	}
	if a.surface != nil {
		a.surface.Release()
	}
	if a.dev != nil {
		a.dev.Release()
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
		a.chatEventBuffer = [32]network.ChatEvent{}
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

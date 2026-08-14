//go:build darwin

package main

import (
	"context"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

type applicationDependencies struct {
	openStore                func(context.Context, applicationOptions) (storage.WorldStore, error)
	dialTCP                  func(context.Context, string) (network.ClientPacketStream, error)
	loginClient              func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, error)
	newHost                  func(context.Context, server.Config, server.Generator, storage.WorldStore) (applicationHost, error)
	newMemoryStreamPair      func(int) (network.ClientPacketStream, network.ServerPacketStream, error)
	newWindow                func(int, int, string) (applicationWindow, error)
	newDevice                func(gfx.NativeWindowHandle, uint32, uint32) (gfx.Device, gfx.Surface, error)
	newHeadlessDevice        func() (gfx.Device, error)
	newGlyphAtlas            func(gfx.Device) (*render.GlyphAtlas, error)
	newAvatarRenderer        func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat) (*render.AvatarRenderer, error)
	newNameTagRenderer       func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat, render.GlyphSource) (*render.NameTagRenderer, error)
	newHotbarRenderer        func(gfx.Device, gfx.TextureFormat, render.GlyphSource, *assets.Registry) (*hud.HotbarRenderer, error)
	newItemDropRenderer      func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat) (*render.ItemDropRenderer, error)
	newBlockOutlineRenderer  func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat) (*render.BlockOutlineRenderer, error)
	newDamageOverlayRenderer func(gfx.Device, gfx.TextureFormat) (*render.DamageOverlayRenderer, error)
	newDebugPanelRenderer    func(gfx.Device, gfx.TextureFormat, render.GlyphSource) (*render.DebugPanelRenderer, error)
}

func defaultApplicationDependencies() applicationDependencies {
	return applicationDependencies{
		openStore:   openApplicationStore,
		dialTCP:     network.DialTCP,
		loginClient: network.LoginClient,
		newHost: func(
			ctx context.Context,
			config server.Config,
			generator server.Generator,
			store storage.WorldStore,
		) (applicationHost, error) {
			return server.NewHost(ctx, config, generator, store)
		},
		newMemoryStreamPair: func(capacity int) (
			network.ClientPacketStream,
			network.ServerPacketStream,
			error,
		) {
			clientStream, serverStream := network.NewMemoryStreamPair(capacity)
			return clientStream, serverStream, nil
		},
		newWindow: func(width, height int, title string) (applicationWindow, error) {
			return client.NewWindow(width, height, title)
		},
		newDevice:         gfx.NewDevice,
		newHeadlessDevice: gfx.NewHeadlessDevice,
	}
}

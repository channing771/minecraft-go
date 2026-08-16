//go:build darwin

package main

import (
	"context"
	"errors"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
)

type applicationDependencies struct {
	openStore            func(context.Context, applicationOptions) (storage.WorldStore, error)
	dialTCP              func(context.Context, string) (network.ClientPacketStream, error)
	loginClient          func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, error)
	newHost              func(context.Context, server.Config, server.Generator, storage.WorldStore) (applicationHost, error)
	newMemoryStreamPair  func(int) (network.ClientPacketStream, network.ServerPacketStream, error)
	newWindow            func(int, int, string) (applicationWindow, error)
	newWindowedRenderer  func(applicationWindow) (*client.Renderer, error)
	newOffscreenRenderer func(int, int) (*client.Renderer, error)
	newGlyphAtlas        func(render.GlyphSink) (*render.GlyphAtlas, error)
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
		newWindowedRenderer: func(window applicationWindow) (*client.Renderer, error) {
			concrete, ok := window.(*client.Window)
			if !ok {
				return nil, errors.New("windowed 渲染器需要真实 client.Window")
			}
			return client.NewWindowedRenderer(concrete)
		},
		newOffscreenRenderer: client.NewRenderer,
		newGlyphAtlas:        render.NewGlyphAtlasWithSink,
	}
}

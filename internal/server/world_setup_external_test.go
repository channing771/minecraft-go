package server_test

import (
	"fmt"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

func newMemoryAttachedWorldForExternalTest(config server.Config, endpoint network.ServerEndpoint, generator server.Generator) *server.Server {
	running := server.NewWorld(config, generator, storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: config.Seed, SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}))
	if config.TrustedObserver {
		if err := running.AttachTrustedObserver(endpoint); err != nil {
			panic(err)
		}
		return running
	}
	restore := sim.PlayerRestore{
		SpawnDimension: config.SpawnDimension,
		SpawnAnchor:    config.SpawnAnchor,
	}
	if _, err := running.AttachSession(externalSessionSpec(1, 1, endpoint, restore)); err != nil {
		panic(err)
	}
	return running
}

func playerStateForExternalTest(running *server.Server) (sim.PlayerUpdate, bool) {
	return running.PlayerStateFor(1)
}

func newAttachedPersistentWorldForExternalTest(config server.Config, endpoint network.ServerEndpoint, generator server.Generator, store storage.Store) *server.Server {
	running := server.NewWorld(config, generator, store)
	if config.TrustedObserver {
		if err := running.AttachTrustedObserver(endpoint); err != nil {
			panic(err)
		}
		return running
	}
	restore := sim.PlayerRestore{
		SpawnDimension: config.SpawnDimension,
		SpawnAnchor:    config.SpawnAnchor,
	}
	if _, err := running.AttachSession(externalSessionSpec(1, 1, endpoint, restore)); err != nil {
		panic(err)
	}
	return running
}

func externalSessionSpec(
	id sim.SessionID,
	generation uint64,
	endpoint network.ServerEndpoint,
	restore sim.PlayerRestore,
) server.SessionSpec {
	return server.SessionSpec{
		ID:          id,
		Generation:  generation,
		PlayerID:    core.PlayerID{0, 0, 0, 0, 0, 0, 0x40, 0, 0x80, 0, 0, 0, 0, 0, 0, byte(id)},
		DisplayName: fmt.Sprintf("Player-%d", id),
		Endpoint:    endpoint,
		Restore:     restore,
	}
}

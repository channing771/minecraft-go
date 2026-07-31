package server_test

import (
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
	if _, err := running.AttachSession(server.SessionSpec{ID: 1, Generation: 1, Endpoint: endpoint, Restore: sim.PlayerRestore{SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}}); err != nil {
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
	if _, err := running.AttachSession(server.SessionSpec{ID: 1, Generation: 1, Endpoint: endpoint, Restore: sim.PlayerRestore{SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}}); err != nil {
		panic(err)
	}
	return running
}

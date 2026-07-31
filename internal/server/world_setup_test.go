package server

import (
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
	"minecraft-go/internal/worldgen"
)

func newAttachedWorldForTest(config Config, endpoint network.ServerEndpoint, generator Generator, store storage.Store) *Server {
	running := NewWorld(config, generator, store)
	if config.TrustedObserver {
		if err := running.AttachTrustedObserver(endpoint); err != nil {
			panic(err)
		}
		return running
	}
	if _, err := running.AttachSession(SessionSpec{ID: testSessionID, Generation: 1, Endpoint: endpoint, Restore: sim.PlayerRestore{SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}}); err != nil {
		panic(err)
	}
	return running
}

func newMemoryAttachedWorldForTest(config Config, endpoint network.ServerEndpoint, generator Generator) *Server {
	return newAttachedWorldForTest(config, endpoint, generator, storage.NewMemory(storage.Metadata{FormatVersion: 1, Seed: config.Seed, SpawnDimension: config.SpawnDimension, SpawnAnchor: config.SpawnAnchor}))
}

func newEmbeddedAttachedWorldForTest(config Config, endpoint network.ServerEndpoint, store storage.Store) *Server {
	metadata := store.Metadata()
	config.Seed, config.SpawnDimension, config.SpawnAnchor = metadata.Seed, metadata.SpawnDimension, metadata.SpawnAnchor
	return newAttachedWorldForTest(config, endpoint, worldgen.New(metadata.Seed), store)
}

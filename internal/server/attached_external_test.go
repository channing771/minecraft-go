package server_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

func TestExternalCallerAttachesDynamicSession(t *testing.T) {
	config := server.DefaultConfig(7)
	config.ViewRadius = 0
	config.Workers = 1
	config.SaveWorkers = 1
	running := server.NewWorld(
		config,
		server.FlatTestGenerator{},
		storage.NewMemory(storage.Metadata{
			FormatVersion:  2,
			Seed:           7,
			SpawnDimension: core.Overworld,
			SpawnAnchor:    core.ChunkPos{},
		}),
	)
	t.Cleanup(func() { shutdownExternalServerForTest(t, running) })
	client, endpoint := network.NewMemoryPair(8)
	defer client.Close()
	exit, err := running.AttachSession(externalSessionSpec(
		41,
		3,
		endpoint,
		sim.PlayerRestore{
			SpawnDimension: core.Overworld,
			SpawnAnchor:    core.ChunkPos{},
		},
	))
	if err != nil {
		t.Fatal(err)
	}
	if state, ok := running.PlayerStateFor(41); !ok || state.Session != 41 {
		t.Fatalf("dynamic state = (%+v, %v)", state, ok)
	}
	if !running.DetachSession(41, 3, nil) {
		t.Fatal("DetachSession = false")
	}
	if got := <-exit; got.ID != 41 || got.Generation != 3 {
		t.Fatalf("exit = %+v", got)
	}
}

package server_test

import (
	"reflect"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

func TestDefaultConfigUsesServerOwnedSubscriptionRadius(t *testing.T) {
	config := server.DefaultConfig(42)
	if config.ViewRadius != 33 {
		t.Fatalf("DefaultConfig ViewRadius = %d，想要 33", config.ViewRadius)
	}
}

func TestServerSubscriptionGenerationOrderAndBounds(t *testing.T) {
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SpawnAnchor = core.ChunkPos{X: 5, Z: -4}
	_, endpoint := network.NewMemoryPair(64)
	running := server.New(config, endpoint, emptyGenerator{})
	t.Cleanup(running.Close)

	first := waitForGenerate(t, running)
	wantFirst := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 5, Z: -4}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4, Z: -4}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 5, Z: -5}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 5, Z: -3}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: -4}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4, Z: -5}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 4, Z: -3}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: -5}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 6, Z: -3}},
	}
	if !reflect.DeepEqual(first.Generate, wantFirst) {
		t.Fatalf("初始 Generate = %+v，想要 %+v", first.Generate, wantFirst)
	}
}

func waitForGenerate(t *testing.T, running *server.Server) sim.TickResult {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result := running.Step()
		if len(result.Generate) != 0 {
			return result
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待 generation requests 超时")
	return sim.TickResult{}
}

type emptyGenerator struct{}

func (emptyGenerator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	return world.NewChunk(pos)
}

func (emptyGenerator) BaseBlockAt(core.BlockPos) core.BlockID {
	return core.AirID
}

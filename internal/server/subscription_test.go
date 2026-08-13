package server_test

import (
	"reflect"
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/world"
)

func TestDefaultConfigUsesServerOwnedSubscriptionRadius(t *testing.T) {
	config := server.DefaultConfig(42)
	if config.ViewRadius != 33 {
		t.Fatalf("DefaultConfig ViewRadius = %d，想要 33", config.ViewRadius)
	}
}

func TestServerSubscriptionAcquisitionOrderAndBounds(t *testing.T) {
	config := server.DefaultConfig(42)
	config.ViewRadius = 1
	config.Workers = 1
	config.SpawnAnchor = core.ChunkPos{X: 5, Z: -4}
	_, endpoint := network.NewMemoryPair(64)
	running := newMemoryAttachedWorldForExternalTest(config, endpoint, emptyGenerator{})
	t.Cleanup(func() { shutdownExternalServerForTest(t, running) })

	first := running.Step()
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
	if !reflect.DeepEqual(first.Acquire, wantFirst) || len(first.Generate) != 0 {
		t.Fatalf("初始 Acquire = %+v Generate=%+v，想要 %+v", first.Acquire, first.Generate, wantFirst)
	}
}

type emptyGenerator struct{}

func (emptyGenerator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	return world.NewChunk(pos)
}

func (emptyGenerator) BaseBlockAt(core.BlockPos) core.BlockID {
	return core.AirID
}

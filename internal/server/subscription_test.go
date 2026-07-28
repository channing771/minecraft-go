package server_test

import (
	"context"
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
	client, endpoint := network.NewMemoryPair(64)
	running := server.New(config, endpoint, emptyGenerator{})
	t.Cleanup(running.Close)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Send(ctx, network.SetViewCenter{
		Sequence:  1,
		Dimension: core.Overworld,
		Center:    core.ChunkPos{},
	}); err != nil {
		t.Fatal(err)
	}
	first := waitForGenerate(t, running)
	wantFirst := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 0, Z: 0}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 0}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 0, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 0, Z: 1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 0}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1, Z: 1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 1, Z: 1}},
	}
	if !reflect.DeepEqual(first.Generate, wantFirst) {
		t.Fatalf("初始 Generate = %+v，想要 %+v", first.Generate, wantFirst)
	}

	if err := client.Send(ctx, network.SetViewCenter{
		Sequence:  2,
		Dimension: core.Overworld,
		Center:    core.ChunkPos{X: 1, Z: 0},
	}); err != nil {
		t.Fatal(err)
	}
	moved := waitForGenerate(t, running)
	wantMoved := []core.ChunkKey{
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: 0}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: -1}},
		{Dimension: core.Overworld, Pos: core.ChunkPos{X: 2, Z: 1}},
	}
	if !reflect.DeepEqual(moved.Generate, wantMoved) {
		t.Fatalf("移动后 Generate = %+v，想要 %+v", moved.Generate, wantMoved)
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

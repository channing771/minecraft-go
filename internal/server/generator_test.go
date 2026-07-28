package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/world"
)

func TestGeneratorWorkerPanicIsolated(t *testing.T) {
	panicAt := core.ChunkPos{X: 1, Z: 1}
	generator := &panicGenerator{panicAt: panicAt}
	config := server.DefaultConfig(7)
	config.ViewRadius = 1
	config.Workers = 2
	client, endpoint := network.NewMemoryPair(64)
	running := server.New(config, endpoint, generator)
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

	ready := make(map[core.ChunkPos]struct{})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result := running.Step()
		for _, key := range result.Ready {
			ready[key.Pos] = struct{}{}
		}
		if generator.callsFor(panicAt) >= 2 &&
			generator.callsFor(core.ChunkPos{X: -1, Z: -1}) > 0 &&
			len(ready) >= 5 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf(
		"panic 未隔离: panicCalls=%d otherCalls=%d ready=%d",
		generator.callsFor(panicAt),
		generator.callsFor(core.ChunkPos{X: -1, Z: -1}),
		len(ready),
	)
}

type panicGenerator struct {
	mu      sync.Mutex
	panicAt core.ChunkPos
	calls   map[core.ChunkPos]int
}

func (generator *panicGenerator) GenerateChunk(pos core.ChunkPos) *world.Chunk {
	generator.mu.Lock()
	if generator.calls == nil {
		generator.calls = make(map[core.ChunkPos]int)
	}
	generator.calls[pos]++
	generator.mu.Unlock()
	if pos == generator.panicAt {
		panic("injected generator panic")
	}
	return world.NewChunk(pos)
}

func (generator *panicGenerator) BaseBlockAt(core.BlockPos) core.BlockID {
	return core.AirID
}

func (generator *panicGenerator) callsFor(pos core.ChunkPos) int {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	return generator.calls[pos]
}

package server

import (
	"fmt"
	"log/slog"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
	"minecraft-go/internal/worldgen"
)

type Generator interface {
	GenerateChunk(core.ChunkPos) *world.Chunk
}

type TerrainProbe struct {
	generator *worldgen.Generator
}

func NewTerrainProbe(seed int64) *TerrainProbe {
	return &TerrainProbe{generator: worldgen.New(seed)}
}

func (probe *TerrainProbe) HeightAt(x, z int32) int32 {
	return probe.generator.HeightAt(x, z)
}

// NewEmbedded 创建使用内置确定性地形生成器的服务端。
func NewEmbedded(config Config, endpoint network.ServerEndpoint) *Server {
	return New(config, endpoint, worldgen.New(config.Seed))
}

func (server *Server) generationWorker() {
	defer server.workers.Done()
	for {
		select {
		case <-server.ctx.Done():
			return
		case key := <-server.jobs:
			result := runGeneration(server.generator, key)
			select {
			case server.generated <- result:
			case <-server.ctx.Done():
				return
			}
		}
	}
}

func runGeneration(
	generator Generator,
	key core.ChunkKey,
) (result sim.GeneratedChunk) {
	result.Dimension = key.Dimension
	result.Pos = key.Pos
	defer func() {
		if recovered := recover(); recovered != nil {
			result.Chunk = nil
			result.Err = fmt.Errorf(
				"generator panic at dimension=%d chunk=(%d,%d): %v",
				key.Dimension,
				key.Pos.X,
				key.Pos.Z,
				recovered,
			)
			slog.Error(
				"区块生成 worker panic 已隔离",
				"dimension",
				key.Dimension,
				"chunk_x",
				key.Pos.X,
				"chunk_z",
				key.Pos.Z,
				"panic",
				recovered,
			)
		}
	}()
	result.Chunk = generator.GenerateChunk(key.Pos)
	if result.Chunk == nil {
		result.Err = fmt.Errorf(
			"generator returned nil at dimension=%d chunk=(%d,%d)",
			key.Dimension,
			key.Pos.X,
			key.Pos.Z,
		)
	}
	return result
}

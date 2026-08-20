package server

import (
	"fmt"
	"log/slog"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
	"github.com/channing771/mornlea/internal/worldgen"
)

type Generator interface {
	GenerateChunk(core.ChunkPos) *world.Chunk
}

type TerrainProbe struct {
	generator *worldgen.Generator
}

// NewTerrainProbe 创建只查询地表高度的探针。
//
// 固定传 fluidEnabled=false:探针只用 HeightAt,而高度图由地形噪声决定,
// 与海平面注水无关,传 false 可避免它依赖调用方的配置。
func NewTerrainProbe(seed int64) *TerrainProbe {
	return &TerrainProbe{generator: worldgen.New(seed, false)}
}

func (probe *TerrainProbe) HeightAt(x, z int32) int32 {
	return probe.generator.HeightAt(x, z)
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

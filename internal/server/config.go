// Package server 编排权威 Engine、生成 worker 和传输端点。
package server

import (
	"runtime"
	"time"

	"minecraft-go/internal/core"
)

type Config struct {
	Seed            int64
	ViewRadius      int
	Workers         int
	SnapshotChunks  int
	SnapshotBytes   int
	OutboxCapacity  int
	TickObserver    func(time.Duration)
	SpawnDimension  core.DimensionID
	SpawnAnchor     core.ChunkPos
	TrustedObserver bool
}

func DefaultConfig(seed int64) Config {
	return Config{
		Seed:           seed,
		ViewRadius:     33,
		Workers:        max(1, runtime.GOMAXPROCS(0)-1),
		SnapshotChunks: 64,
		SnapshotBytes:  1 << 20,
		OutboxCapacity: 512,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	}
}

func (config Config) validate() {
	if config.ViewRadius < 0 {
		panic("server: negative view radius")
	}
	if config.Workers < 1 {
		panic("server: worker count must be positive")
	}
	if config.SnapshotChunks < 1 || config.SnapshotBytes < 1 {
		panic("server: snapshot budgets must be positive")
	}
	if config.OutboxCapacity < 1 {
		panic("server: outbox capacity must be positive")
	}
	if config.SpawnDimension != core.Overworld {
		panic("server: spawn dimension must be overworld")
	}
}

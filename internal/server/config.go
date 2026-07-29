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
	SaveWorkers     int
	SaveChunks      int
	SaveBytes       int
	AutosaveTicks   uint64
	RetryBaseTicks  uint64
	RetryMaxTicks   uint64
	UnsavedBytes    int64
	ShutdownTimeout time.Duration
	SaveObserver    func(time.Duration)
}

func DefaultConfig(seed int64) Config {
	return Config{
		Seed:            seed,
		ViewRadius:      33,
		Workers:         max(1, runtime.GOMAXPROCS(0)-1),
		SnapshotChunks:  64,
		SnapshotBytes:   1 << 20,
		OutboxCapacity:  512,
		SpawnDimension:  core.Overworld,
		SpawnAnchor:     core.ChunkPos{},
		SaveWorkers:     2,
		SaveChunks:      8,
		SaveBytes:       4 << 20,
		AutosaveTicks:   6000,
		RetryBaseTicks:  20,
		RetryMaxTicks:   1200,
		UnsavedBytes:    512 << 20,
		ShutdownTimeout: 30 * time.Second,
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
	if config.SaveWorkers < 1 {
		panic("server: save worker count must be positive")
	}
	if config.SaveChunks < 1 || config.SaveBytes < 1 {
		panic("server: save budgets must be positive")
	}
	if config.AutosaveTicks == 0 {
		panic("server: autosave ticks must be positive")
	}
	if config.RetryBaseTicks == 0 || config.RetryMaxTicks == 0 {
		panic("server: retry ticks must be positive")
	}
	if config.RetryMaxTicks < config.RetryBaseTicks {
		panic("server: retry maximum must not be below base")
	}
	if config.UnsavedBytes < 1 {
		panic("server: unsaved byte limit must be positive")
	}
	if config.ShutdownTimeout <= 0 {
		panic("server: shutdown timeout must be positive")
	}
}

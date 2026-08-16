// Package server 编排权威 Engine、生成 worker 和传输端点。
package server

import (
	"runtime"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
)

type Config struct {
	Companions []companion.Definition
	// AIModel 是伙伴 AI 的模型运行时设置。伙伴列表非空时 NewHost 会校验它的
	// 静态完整性（AIModel.Validate），并要求 https endpoint 携带非空 AIAPIKey，
	// 否则拒绝启动；伙伴列表为空时不做任何要求（AI 关闭）。
	AIModel companion.ModelSettings
	// AIAPIKey 是按 AIModel.APIKeyEnv 指向的环境变量解析出的密钥值。
	// 它只存在于进程内存：不写入配置文件与任何存档，也不进入日志或错误
	// 文本；需要打印配置的地方必须跳过该字段。
	AIAPIKey              string
	Seed                  int64
	MaxPlayers            int
	ViewRadius            int
	Workers               int
	SnapshotChunks        int
	SnapshotBytes         int
	OutboxCapacity        int
	TickObserver          func(time.Duration)
	ScheduledTickObserver func(time.Time, time.Duration)
	InterestObserver      func(time.Duration)
	SpawnDimension        core.DimensionID
	SpawnAnchor           core.ChunkPos
	TrustedObserver       bool
	SaveWorkers           int
	SaveChunks            int
	SaveBytes             int
	AutosaveTicks         uint64
	RetryBaseTicks        uint64
	RetryMaxTicks         uint64
	UnsavedBytes          int64
	ShutdownTimeout       time.Duration
	SaveObserver          func(time.Duration)
	HeartbeatInterval     time.Duration
	HeartbeatTimeout      time.Duration

	heartbeatClock heartbeatClock
}

func DefaultConfig(seed int64) Config {
	return Config{
		Seed:              seed,
		MaxPlayers:        8,
		ViewRadius:        33,
		Workers:           max(1, runtime.GOMAXPROCS(0)-1),
		SnapshotChunks:    64,
		SnapshotBytes:     1 << 20,
		OutboxCapacity:    512,
		SpawnDimension:    core.Overworld,
		SpawnAnchor:       core.ChunkPos{},
		SaveWorkers:       2,
		SaveChunks:        8,
		SaveBytes:         4 << 20,
		AutosaveTicks:     6000,
		RetryBaseTicks:    20,
		RetryMaxTicks:     1200,
		UnsavedBytes:      512 << 20,
		ShutdownTimeout:   30 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  15 * time.Second,
		heartbeatClock:    realHeartbeatClock{},
	}
}

func (config *Config) validate() {
	if err := companion.ValidateDefinitions(config.Companions); err != nil {
		panic("server: invalid companion definitions: " + err.Error())
	}
	if config.MaxPlayers == 0 {
		config.MaxPlayers = 8
	}
	if config.MaxPlayers < 1 || config.MaxPlayers > 8 {
		panic("server: max players must be between 1 and 8")
	}
	if config.heartbeatClock == nil {
		config.heartbeatClock = realHeartbeatClock{}
	}
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
	if config.HeartbeatInterval <= 0 || config.HeartbeatTimeout <= 0 {
		panic("server: heartbeat durations must be positive")
	}
}

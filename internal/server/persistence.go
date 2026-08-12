package server

import (
	"time"

	"minecraft-go/internal/sim"
	"minecraft-go/internal/storage"
)

// saveKind 区分同一批 worker 处理的两类固定保存工作。
type saveKind uint8

const (
	saveKindChunks saveKind = iota
	saveKindMetadata
)

type saveJob struct {
	Kind      saveKind
	Region    storage.RegionKey
	Snapshots []sim.ChunkSaveSnapshot
	Attempt   uint32
	Retry     bool
	RetryID   uint64
	// Metadata 只在 Kind 为 saveKindMetadata 时有效，是一份不可变的世界快照。
	Metadata storage.Metadata
}

// metadataSaveState 是世界时间保存的固定调度状态：
// 最新权威时间、待提交边界、最多一个 in-flight、失败次数与下一重试 tick。
type metadataSaveState struct {
	latest        uint64
	committed     uint64
	pending       bool
	inFlight      bool
	attempts      uint32
	nextRetryTick uint64
	lastError     string
	lastErrorAt   time.Time
}

type saveCompletion struct {
	Job    saveJob
	Result storage.SaveResult
	Err    error
}

type retrySave struct {
	Job       saveJob
	Attempts  uint32
	NextTick  uint64
	LastError error
}

// PersistenceStatus 汇总权威区块的存档积压与最近一次存档结果。
type PersistenceStatus struct {
	DirtyChunks     int
	EstimatedBytes  int64
	InFlightChunks  int
	Backpressured   bool
	LastSuccess     time.Time
	LastError       string
	LastErrorAt     time.Time
	AutosaveDrained bool
	// 世界 metadata 保存有独立的固定调度状态，不进入按 region 分组的区块重试。
	MetadataPending   bool
	MetadataInFlight  bool
	MetadataLastError string
}

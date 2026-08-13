// Package storage 定义世界存储的稳定值与接口。
package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

type regionFile interface {
	ReadAt([]byte, int64) (int, error)
	WriteAt([]byte, int64) (int, error)
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(int64) error
	Close() error
}

type regionFileHooks struct {
	Open func(string, int, fs.FileMode) (regionFile, error)
}

var (
	ErrChunkNotFound    = errors.New("storage: chunk not found")
	ErrPlayerNotFound   = errors.New("storage: player not found")
	ErrWorldLocked      = errors.New("storage: world locked")
	ErrCorrupt          = errors.New("storage: corrupt data")
	ErrFutureVersion    = errors.New("storage: future version")
	ErrRevisionConflict = errors.New("storage: revision conflict")
)

type Metadata struct {
	FormatVersion  uint32
	Seed           int64
	SpawnDimension core.DimensionID
	SpawnAnchor    core.ChunkPos
	// WorldTimeTicks 是权威绝对世界时间，metadata v2 起持久化；v1 世界迁移后为零。
	WorldTimeTicks uint64
}

type StoredChunk struct {
	Key               core.ChunkKey
	Revision          uint64
	PersistedRevision uint64
	NeedsRewrite      bool
	Recovered         bool
	Chunk             *world.Chunk
}

type ChunkSave struct {
	Key      core.ChunkKey
	Revision uint64
	Chunk    *world.Chunk
}

type SaveResult struct {
	Committed map[core.ChunkKey]uint64
}

type Store interface {
	Metadata() Metadata
	// SaveMetadata 原子提交一份世界 metadata 快照，失败时磁盘上必须保留完整旧版。
	SaveMetadata(context.Context, Metadata) error
	LoadChunk(context.Context, core.ChunkKey) (StoredChunk, error)
	SaveBatch(context.Context, []ChunkSave) (SaveResult, error)
	Sync(context.Context) error
	Close() error
}

// WorldStore combines world chunk storage with persisted player state.
type WorldStore interface {
	Store
	PlayerStore
}

type RegionKey struct {
	Dimension core.DimensionID
	X, Z      int32
}

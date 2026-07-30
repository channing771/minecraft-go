// Package storage 定义世界存储的稳定值与接口。
package storage

import (
	"context"
	"errors"
	"io/fs"
	"os"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
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

// Package storage 定义世界存储的稳定值与接口。
package storage

import (
	"context"
	"errors"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

var (
	ErrChunkNotFound    = errors.New("storage: chunk not found")
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

type RegionKey struct {
	Dimension core.DimensionID
	X, Z      int32
}

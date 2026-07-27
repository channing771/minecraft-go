// Package sim 实现与协议和渲染无关的权威世界状态。
package sim

import (
	"errors"
	"fmt"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

var (
	ErrChunkNotReady   = errors.New("sim: chunk not ready")
	ErrBlockOutOfWorld = errors.New("sim: block outside world height")
)

type ChunkState uint8

const (
	ChunkAbsent ChunkState = iota
	ChunkGenerating
	ChunkReady
	ChunkFailed
	ChunkUnloading
)

type ChunkRecord struct {
	State    ChunkState
	Chunk    *world.Chunk
	Revision uint64
	Err      error
}

type ChunkInfo struct {
	State    ChunkState
	Revision uint64
	Err      error
}

type BaseBlockLookup func(core.BlockPos) core.BlockID

// Dimension 由 Engine 的单写者 tick 独占，不提供内部锁。
type Dimension struct {
	id       core.DimensionID
	base     BaseBlockLookup
	records  map[core.ChunkPos]*ChunkRecord
	overlays map[core.ChunkPos]map[uint32]core.BlockID
}

func NewDimension(id core.DimensionID, base BaseBlockLookup) *Dimension {
	if base == nil {
		panic("sim: nil base block lookup")
	}
	return &Dimension{
		id:       id,
		base:     base,
		records:  make(map[core.ChunkPos]*ChunkRecord),
		overlays: make(map[core.ChunkPos]map[uint32]core.BlockID),
	}
}

// BeginGeneration 把 Absent 或 Failed 区块转为 Generating。
func (dimension *Dimension) BeginGeneration(pos core.ChunkPos) bool {
	record, exists := dimension.records[pos]
	if !exists {
		dimension.records[pos] = &ChunkRecord{State: ChunkGenerating}
		return true
	}
	switch record.State {
	case ChunkGenerating, ChunkReady:
		return false
	case ChunkFailed:
		*record = ChunkRecord{State: ChunkGenerating}
		return true
	case ChunkAbsent, ChunkUnloading:
		panic(fmt.Sprintf(
			"sim: illegal chunk transition %d -> Generating at %+v",
			record.State,
			pos,
		))
	default:
		panic(fmt.Sprintf("sim: unknown chunk state %d at %+v", record.State, pos))
	}
}

// ApplyGenerated 接管生成结果，应用持久 overlay 后进入 Ready。
func (dimension *Dimension) ApplyGenerated(
	pos core.ChunkPos,
	chunk *world.Chunk,
) error {
	record := dimension.recordForTransition(pos, ChunkGenerating, "Ready")
	if chunk == nil {
		return errors.New("sim: generated chunk is nil")
	}
	if chunk.Pos != pos {
		return fmt.Errorf(
			"sim: generated chunk position %+v, want %+v",
			chunk.Pos,
			pos,
		)
	}
	if err := dimension.applyOverlay(pos, chunk); err != nil {
		return err
	}
	record.State = ChunkReady
	record.Chunk = chunk
	record.Revision = 1
	record.Err = nil
	return nil
}

// MarkFailed 把生成任务的失败结果记录在区块状态中。
func (dimension *Dimension) MarkFailed(pos core.ChunkPos, err error) {
	if err == nil {
		panic("sim: nil generation failure")
	}
	record := dimension.recordForTransition(pos, ChunkGenerating, "Failed")
	*record = ChunkRecord{
		State: ChunkFailed,
		Err:   err,
	}
}

// Unload 完成 Ready → Unloading → Absent，并保留 overlay。
func (dimension *Dimension) Unload(pos core.ChunkPos) error {
	record := dimension.recordForTransition(pos, ChunkReady, "Unloading")
	record.State = ChunkUnloading
	record.Chunk = nil
	delete(dimension.records, pos)
	return nil
}

func (dimension *Dimension) Info(pos core.ChunkPos) (ChunkInfo, bool) {
	record, ok := dimension.records[pos]
	if !ok {
		return ChunkInfo{}, false
	}
	return ChunkInfo{
		State:    record.State,
		Revision: record.Revision,
		Err:      record.Err,
	}, true
}

func (dimension *Dimension) CloneReadyChunk(
	pos core.ChunkPos,
) (*world.Chunk, uint64, bool) {
	record, ok := dimension.records[pos]
	if !ok || record.State != ChunkReady {
		return nil, 0, false
	}
	return record.Chunk.Clone(), record.Revision, true
}

// BlockAt 返回方块与其所属区块是否 Ready。世界高度外恒为空气。
func (dimension *Dimension) BlockAt(
	position core.BlockPos,
) (core.BlockID, bool) {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return core.AirID, true
	}
	record, ok := dimension.records[position.Chunk()]
	if !ok || record.State != ChunkReady {
		return core.AirID, false
	}
	x, _, z := position.Local()
	return record.Chunk.BlockAt(x, position.Y, z), true
}

func (dimension *Dimension) recordForTransition(
	pos core.ChunkPos,
	want ChunkState,
	next string,
) *ChunkRecord {
	record, ok := dimension.records[pos]
	if !ok || record.State != want {
		state := ChunkAbsent
		if ok {
			state = record.State
		}
		panic(fmt.Sprintf(
			"sim: illegal chunk transition %d -> %s at %+v",
			state,
			next,
			pos,
		))
	}
	return record
}

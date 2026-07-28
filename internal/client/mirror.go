package client

import (
	"errors"
	"fmt"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/world"
)

// MirrorChunk 是客户端主线程持有的只读权威区块副本。
type MirrorChunk struct {
	Revision uint64
	Chunk    *world.Chunk
	Desynced bool
}

// MirrorUpdate 描述一次服务端消息对渲染和上行协议的影响。
type MirrorUpdate struct {
	Dirty     []core.SectionKey
	Forgotten []core.SectionKey
	Resync    *network.RequestChunkResync
	Rejected  *network.CommandRejected
}

// Mirror 保存按维度隔离、按 revision 前进的客户端世界镜像。
// 它由客户端主线程独占，不提供并发访问保证。
type Mirror struct {
	dimensions     map[core.DimensionID]map[core.ChunkPos]*MirrorChunk
	resyncInFlight map[core.ChunkKey]bool
}

func NewMirror() *Mirror {
	return &Mirror{
		dimensions:     make(map[core.DimensionID]map[core.ChunkPos]*MirrorChunk),
		resyncInFlight: make(map[core.ChunkKey]bool),
	}
}

// Apply 验证并应用一条服务端消息。
func (mirror *Mirror) Apply(message network.ServerMessage) (MirrorUpdate, error) {
	if mirror == nil {
		return MirrorUpdate{}, errors.New("client: nil mirror")
	}
	switch message := message.(type) {
	case network.ChunkSnapshot:
		return mirror.applySnapshot(message)
	case *network.ChunkSnapshot:
		if message == nil {
			return MirrorUpdate{}, errors.New("client: nil chunk snapshot")
		}
		return mirror.applySnapshot(*message)
	case network.BlockChanges:
		return mirror.applyBlockChanges(message)
	case *network.BlockChanges:
		if message == nil {
			return MirrorUpdate{}, errors.New("client: nil block changes")
		}
		return mirror.applyBlockChanges(*message)
	case network.ForgetChunks:
		return mirror.applyForget(message)
	case *network.ForgetChunks:
		if message == nil {
			return MirrorUpdate{}, errors.New("client: nil forget chunks")
		}
		return mirror.applyForget(*message)
	case network.CommandRejected:
		return rejectionUpdate(message)
	case *network.CommandRejected:
		if message == nil {
			return MirrorUpdate{}, errors.New("client: nil command rejection")
		}
		return rejectionUpdate(*message)
	default:
		return MirrorUpdate{}, fmt.Errorf("client: unsupported server message %T", message)
	}
}

// Chunk 返回指定维度与位置的镜像区块。
func (mirror *Mirror) Chunk(
	dimension core.DimensionID,
	position core.ChunkPos,
) (*MirrorChunk, bool) {
	if mirror == nil {
		return nil, false
	}
	chunks := mirror.dimensions[dimension]
	chunk, ok := chunks[position]
	return chunk, ok
}

// BlockAt 返回方块值以及所属区块是否已加载。
func (mirror *Mirror) BlockAt(
	dimension core.DimensionID,
	position core.BlockPos,
) (core.BlockID, bool) {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return core.AirID, true
	}
	chunk, ok := mirror.Chunk(dimension, position.Chunk())
	if !ok {
		return core.AirID, false
	}
	x, _, z := position.Local()
	return chunk.Chunk.BlockAt(x, position.Y, z), true
}

// Hash 返回指定镜像区块的逻辑哈希与 revision。
func (mirror *Mirror) Hash(
	dimension core.DimensionID,
	position core.ChunkPos,
) ([32]byte, uint64, bool) {
	chunk, ok := mirror.Chunk(dimension, position)
	if !ok {
		return [32]byte{}, 0, false
	}
	return chunk.Chunk.Hash(), chunk.Revision, true
}

func (mirror *Mirror) applySnapshot(
	snapshot network.ChunkSnapshot,
) (MirrorUpdate, error) {
	chunk, err := chunkFromSnapshot(snapshot)
	if err != nil {
		return MirrorUpdate{}, err
	}

	chunks := mirror.dimension(snapshot.Dimension)
	chunks[snapshot.Chunk] = &MirrorChunk{
		Revision: snapshot.Revision,
		Chunk:    chunk,
	}
	delete(mirror.resyncInFlight, core.ChunkKey{
		Dimension: snapshot.Dimension,
		Pos:       snapshot.Chunk,
	})

	dirty := make(map[core.SectionKey]struct{})
	addChunkSections(dirty, snapshot.Dimension, snapshot.Chunk)
	addLoadedHorizontalNeighbors(dirty, chunks, snapshot.Dimension, snapshot.Chunk)
	return MirrorUpdate{Dirty: sortedSectionKeys(dirty)}, nil
}

func (mirror *Mirror) applyBlockChanges(
	changes network.BlockChanges,
) (MirrorUpdate, error) {
	if err := changes.Validate(); err != nil {
		return MirrorUpdate{}, fmt.Errorf("client: invalid block changes: %w", err)
	}
	key := core.ChunkKey{Dimension: changes.Dimension, Pos: changes.Chunk}
	chunk, loaded := mirror.Chunk(changes.Dimension, changes.Chunk)
	if !loaded {
		return mirror.requestResync(key, 0), nil
	}
	if changes.NewRevision <= chunk.Revision {
		return MirrorUpdate{}, nil
	}
	if chunk.Desynced {
		return MirrorUpdate{}, nil
	}
	if changes.BaseRevision != chunk.Revision {
		chunk.Desynced = true
		return mirror.requestResync(key, chunk.Revision), nil
	}

	dirty := make(map[core.SectionKey]struct{})
	for _, change := range changes.Changes {
		x, _, z := change.Position.Local()
		chunk.Chunk.SetBlock(x, change.Position.Y, z, change.Block)
		mirror.addDirtyAround(dirty, changes.Dimension, change.Position)
	}
	chunk.Revision = changes.NewRevision
	return MirrorUpdate{Dirty: sortedSectionKeys(dirty)}, nil
}

func (mirror *Mirror) applyForget(
	forget network.ForgetChunks,
) (MirrorUpdate, error) {
	seen := make(map[core.ChunkPos]struct{}, len(forget.Chunks))
	for _, position := range forget.Chunks {
		if _, duplicate := seen[position]; duplicate {
			return MirrorUpdate{}, fmt.Errorf("client: duplicate forgotten chunk %+v", position)
		}
		seen[position] = struct{}{}
	}

	chunks := mirror.dimensions[forget.Dimension]
	forgotten := make(map[core.SectionKey]struct{})
	removed := make([]core.ChunkPos, 0, len(forget.Chunks))
	for _, position := range forget.Chunks {
		if _, loaded := chunks[position]; loaded {
			addChunkSections(forgotten, forget.Dimension, position)
			removed = append(removed, position)
			delete(chunks, position)
		}
		delete(mirror.resyncInFlight, core.ChunkKey{
			Dimension: forget.Dimension,
			Pos:       position,
		})
	}
	if len(chunks) == 0 {
		delete(mirror.dimensions, forget.Dimension)
	}

	dirty := make(map[core.SectionKey]struct{})
	for _, position := range removed {
		addLoadedHorizontalNeighbors(dirty, chunks, forget.Dimension, position)
	}
	return MirrorUpdate{
		Dirty:     sortedSectionKeys(dirty),
		Forgotten: sortedSectionKeys(forgotten),
	}, nil
}

func (mirror *Mirror) dimension(
	dimension core.DimensionID,
) map[core.ChunkPos]*MirrorChunk {
	chunks := mirror.dimensions[dimension]
	if chunks == nil {
		chunks = make(map[core.ChunkPos]*MirrorChunk)
		mirror.dimensions[dimension] = chunks
	}
	return chunks
}

func (mirror *Mirror) requestResync(
	key core.ChunkKey,
	haveRevision uint64,
) MirrorUpdate {
	if mirror.resyncInFlight[key] {
		return MirrorUpdate{}
	}
	mirror.resyncInFlight[key] = true
	return MirrorUpdate{Resync: &network.RequestChunkResync{
		Dimension:    key.Dimension,
		Chunk:        key.Pos,
		HaveRevision: haveRevision,
	}}
}

func (mirror *Mirror) addDirtyAround(
	dirty map[core.SectionKey]struct{},
	dimension core.DimensionID,
	position core.BlockPos,
) {
	for dy := int32(-1); dy <= 1; dy++ {
		y := position.Y + dy
		if y < core.MinY || y >= core.MaxY {
			continue
		}
		for dz := int32(-1); dz <= 1; dz++ {
			for dx := int32(-1); dx <= 1; dx++ {
				neighbor := core.BlockPos{X: position.X + dx, Y: y, Z: position.Z + dz}
				if _, loaded := mirror.Chunk(dimension, neighbor.Chunk()); loaded {
					dirty[core.SectionKey{Dimension: dimension, Pos: neighbor.Section()}] = struct{}{}
				}
			}
		}
	}
}

func addChunkSections(
	keys map[core.SectionKey]struct{},
	dimension core.DimensionID,
	position core.ChunkPos,
) {
	for y := int32(0); y < core.SectionsPerChunk; y++ {
		keys[core.SectionKey{
			Dimension: dimension,
			Pos:       core.SectionPos{X: position.X, Y: y, Z: position.Z},
		}] = struct{}{}
	}
}

func addLoadedHorizontalNeighbors(
	keys map[core.SectionKey]struct{},
	chunks map[core.ChunkPos]*MirrorChunk,
	dimension core.DimensionID,
	position core.ChunkPos,
) {
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			neighbor := core.ChunkPos{X: position.X + dx, Z: position.Z + dz}
			if _, loaded := chunks[neighbor]; loaded {
				addChunkSections(keys, dimension, neighbor)
			}
		}
	}
}

func sortedSectionKeys(set map[core.SectionKey]struct{}) []core.SectionKey {
	if len(set) == 0 {
		return nil
	}
	keys := make([]core.SectionKey, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dimension != keys[j].Dimension {
			return keys[i].Dimension < keys[j].Dimension
		}
		if keys[i].Pos.X != keys[j].Pos.X {
			return keys[i].Pos.X < keys[j].Pos.X
		}
		if keys[i].Pos.Y != keys[j].Pos.Y {
			return keys[i].Pos.Y < keys[j].Pos.Y
		}
		return keys[i].Pos.Z < keys[j].Pos.Z
	})
	return keys
}

func rejectionUpdate(rejected network.CommandRejected) (MirrorUpdate, error) {
	switch rejected.Reason {
	case network.RejectInvalidRay,
		network.RejectNoTarget,
		network.RejectChunkNotReady,
		network.RejectProtectedBlock,
		network.RejectInvalidBlock,
		network.RejectOccupied:
		return MirrorUpdate{Rejected: &rejected}, nil
	default:
		return MirrorUpdate{}, fmt.Errorf(
			"client: unknown command rejection reason %q",
			rejected.Reason,
		)
	}
}

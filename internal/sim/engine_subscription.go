package sim

import (
	"math"
	"sort"

	"minecraft-go/internal/core"
)

func (engine *Engine) RegisterObserverSession(id SessionID) {
	if engine.sessions[id] != nil {
		panic("sim: duplicate registered session")
	}
	engine.sessions[id] = &sessionState{
		trustedObserver: true,
		wanted:          make(map[core.ChunkKey]struct{}),
	}
}

// WantsChunk reports whether the current union subscription still needs key.
// Callers serialize this query with Step and session lifecycle operations.
func (engine *Engine) WantsChunk(key core.ChunkKey) bool {
	_, wanted := engine.wanted[key]
	return wanted
}

// SessionWantsChunk reports whether id currently subscribes to key.
// Callers serialize this query with Step and session lifecycle operations.
func (engine *Engine) SessionWantsChunk(id SessionID, key core.ChunkKey) bool {
	session := engine.sessions[id]
	if session == nil {
		return false
	}
	_, wanted := session.wanted[key]
	return wanted
}

func (engine *Engine) reconcileSubscriptions(result *TickResult) {
	union := make(map[core.ChunkKey]struct{})
	for sessionID, session := range engine.sessions {
		next := engine.sessionWantedSnapshot(session)
		for key := range next {
			union[key] = struct{}{}
		}
		for key := range session.wanted {
			if _, retained := next[key]; !retained {
				result.Forget[sessionID] = append(result.Forget[sessionID], key)
			}
		}
		sortChunkKeys(result.Forget[sessionID])
		session.wanted = next
	}

	candidates := make([]core.ChunkKey, 0)
	for key := range union {
		_, wasWanted := engine.wanted[key]
		dimension := engine.dimensions[key.Dimension]
		info, exists := dimension.Info(key.Pos)
		if !wasWanted || exists && info.State == ChunkFailed {
			candidates = append(candidates, key)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftDistance := engine.subscriptionDistanceSquared(candidates[i])
		rightDistance := engine.subscriptionDistanceSquared(candidates[j])
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		return chunkKeyLess(candidates[i], candidates[j])
	})
	for _, key := range candidates {
		dimension := engine.dimensions[key.Dimension]
		if dimension.CancelUnload(key.Pos) {
			result.Ready = append(result.Ready, key)
			continue
		}
		if dimension.BeginLoading(key.Pos) {
			result.Acquire = append(result.Acquire, key)
		}
	}

	for key := range engine.wanted {
		if _, retained := union[key]; retained {
			continue
		}
		dimension := engine.dimensions[key.Dimension]
		if info, ok := dimension.Info(key.Pos); ok && info.State == ChunkReady {
			dimension.RequestUnload(key.Pos)
		}
	}
	engine.wanted = union
}

func (engine *Engine) wantedSnapshot() map[core.ChunkKey]struct{} {
	wanted := make(map[core.ChunkKey]struct{})
	for _, session := range engine.sessions {
		for key := range engine.sessionWantedSnapshot(session) {
			wanted[key] = struct{}{}
		}
	}
	return wanted
}

func (engine *Engine) sessionWantedSnapshot(
	session *sessionState,
) map[core.ChunkKey]struct{} {
	wanted := make(map[core.ChunkKey]struct{})
	if session.hasView && engine.dimensions[session.dimension] != nil {
		for dz := -engine.viewRadius; dz <= engine.viewRadius; dz++ {
			for dx := -engine.viewRadius; dx <= engine.viewRadius; dx++ {
				key := core.ChunkKey{
					Dimension: session.dimension,
					Pos: core.ChunkPos{
						X: session.center.X + int32(dx),
						Z: session.center.Z + int32(dz),
					},
				}
				wanted[key] = struct{}{}
			}
		}
	}
	if session.player != nil && session.player.lifecycle == PlayerPendingSpawn {
		for key := range session.player.restoreWanted {
			wanted[key] = struct{}{}
		}
		for chunk := range session.player.spawnWanted {
			wanted[core.ChunkKey{Dimension: session.dimension, Pos: chunk}] = struct{}{}
		}
	}
	return wanted
}

func (engine *Engine) applyAcquired(
	acquired []AcquiredChunk,
	wanted map[core.ChunkKey]struct{},
	result *TickResult,
) {
	sort.SliceStable(acquired, func(i, j int) bool {
		return chunkKeyLess(acquired[i].Key, acquired[j].Key)
	})
	for _, acquiredChunk := range acquired {
		key := acquiredChunk.Key
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		info, ok := dimension.Info(key.Pos)
		if !ok || info.State != ChunkLoading {
			continue
		}
		switch {
		case acquiredChunk.Err != nil:
			dimension.MarkLoadFailed(key.Pos, acquiredChunk.Err)
		case acquiredChunk.Missing:
			if _, retained := wanted[key]; !retained {
				dimension.DropLoading(key.Pos)
			} else if dimension.MarkGenerating(key.Pos) {
				result.Generate = append(result.Generate, key)
			}
		default:
			err := dimension.ApplyLoaded(
				key.Pos,
				acquiredChunk.Chunk,
				acquiredChunk.Revision,
				acquiredChunk.PersistedRevision,
				acquiredChunk.NeedsRewrite,
				acquiredChunk.Recovered,
			)
			if err != nil {
				dimension.MarkLoadFailed(key.Pos, err)
				continue
			}
			if _, retained := wanted[key]; !retained {
				dimension.RequestUnload(key.Pos)
				continue
			}
			result.Ready = append(result.Ready, key)
		}
	}
}

func (engine *Engine) subscriptionDistanceSquared(key core.ChunkKey) int64 {
	distance := int64(math.MaxInt64)
	for _, session := range engine.sessions {
		if _, wanted := session.wanted[key]; !wanted {
			continue
		}
		dx := int64(key.Pos.X - session.center.X)
		dz := int64(key.Pos.Z - session.center.Z)
		candidate := dx*dx + dz*dz
		if candidate < distance {
			distance = candidate
		}
	}
	return distance
}

func (engine *Engine) applyGenerated(
	generated []GeneratedChunk,
	wanted map[core.ChunkKey]struct{},
	result *TickResult,
) {
	sort.SliceStable(generated, func(i, j int) bool {
		left := core.ChunkKey{
			Dimension: generated[i].Dimension,
			Pos:       generated[i].Pos,
		}
		right := core.ChunkKey{
			Dimension: generated[j].Dimension,
			Pos:       generated[j].Pos,
		}
		return chunkKeyLess(left, right)
	})
	for _, generatedChunk := range generated {
		key := core.ChunkKey{
			Dimension: generatedChunk.Dimension,
			Pos:       generatedChunk.Pos,
		}
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		info, ok := dimension.Info(key.Pos)
		if !ok || info.State != ChunkGenerating {
			continue
		}
		if generatedChunk.Err != nil {
			dimension.MarkFailed(key.Pos, generatedChunk.Err)
			continue
		}
		if err := dimension.ApplyGenerated(key.Pos, generatedChunk.Chunk); err != nil {
			dimension.MarkFailed(key.Pos, err)
			continue
		}
		if _, retained := wanted[key]; !retained {
			dimension.RequestUnload(key.Pos)
			continue
		}
		result.Ready = append(result.Ready, key)
	}
}

package sim

import (
	"math"
	"sort"

	"github.com/channing771/mornlea/internal/core"
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
	engine.addCompanionWanted(union)

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
	engine.addCompanionWanted(wanted)
	return wanted
}

func (engine *Engine) addCompanionWanted(wanted map[core.ChunkKey]struct{}) {
	for _, state := range engine.companions {
		if state.active {
			center := companionChunk(state.body.Position)
			for dz := -companionInterestRadius; dz <= companionInterestRadius; dz++ {
				for dx := -companionInterestRadius; dx <= companionInterestRadius; dx++ {
					wanted[core.ChunkKey{
						Dimension: state.dimension,
						Pos: core.ChunkPos{
							X: center.X + int32(dx),
							Z: center.Z + int32(dz),
						},
					}] = struct{}{}
				}
			}
			continue
		}
		for key := range state.restoreWanted {
			wanted[key] = struct{}{}
		}
		for chunk := range state.spawnWanted {
			wanted[core.ChunkKey{Dimension: state.dimension, Pos: chunk}] = struct{}{}
		}
	}
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
	for _, state := range engine.companions {
		candidate, relevant := companionSubscriptionDistanceSquared(state, key)
		if relevant && candidate < distance {
			distance = candidate
		}
	}
	return distance
}

func companionSubscriptionDistanceSquared(
	state *companionState,
	key core.ChunkKey,
) (int64, bool) {
	if state.active {
		if key.Dimension != state.dimension {
			return 0, false
		}
		center := companionChunk(state.body.Position)
		if absChunkDelta(key.Pos.X, center.X) > companionInterestRadius ||
			absChunkDelta(key.Pos.Z, center.Z) > companionInterestRadius {
			return 0, false
		}
		return chunkDistanceSquared(key.Pos, center), true
	}
	distance := int64(math.MaxInt64)
	relevant := false
	if _, wanted := state.restoreWanted[key]; wanted {
		for _, candidate := range state.restoreCandidates {
			if candidate.location.Dimension != key.Dimension {
				continue
			}
			distance = min(distance, chunkDistanceSquared(
				key.Pos,
				companionChunk(candidate.location.Position),
			))
			relevant = true
		}
	}
	if key.Dimension == state.dimension {
		if _, wanted := state.spawnWanted[key.Pos]; wanted && len(state.spawnCandidates) != 0 {
			anchor := (core.BlockPos{
				X: state.spawnCandidates[0].X,
				Z: state.spawnCandidates[0].Z,
			}).Chunk()
			distance = min(distance, chunkDistanceSquared(key.Pos, anchor))
			relevant = true
		}
	}
	return distance, relevant
}

func chunkDistanceSquared(left, right core.ChunkPos) int64 {
	dx := int64(left.X - right.X)
	dz := int64(left.Z - right.Z)
	return dx*dx + dz*dz
}

func absChunkDelta(left, right int32) int {
	delta := left - right
	if delta < 0 {
		delta = -delta
	}
	return int(delta)
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

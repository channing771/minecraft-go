package server

import (
	"log/slog"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

func (server *Server) queueReadyAndResync(current *session, result sim.TickResult) {
	for _, key := range result.Ready {
		if server.engine.SessionWantsChunk(current.id, key) {
			current.queueSnapshot(key, false)
		}
	}
	for _, request := range result.Resync {
		if request.Session != current.id {
			continue
		}
		key := core.ChunkKey{
			Dimension: request.Dimension,
			Pos:       request.Chunk,
		}
		if !server.engine.SessionWantsChunk(current.id, key) {
			continue
		}
		current.queueSnapshot(key, true)
	}
}

func (current *session) queueSnapshot(key core.ChunkKey, resync bool) {
	state := current.publications[key]
	if state == nil {
		state = &publication{}
		current.publications[key] = state
	}
	if state.snapshotSent && !resync {
		return
	}
	request := current.pendingSnapshots[key]
	request.resync = request.resync || resync
	current.pendingSnapshots[key] = request
	state.resyncQueued = state.resyncQueued || resync
}

func (current *session) applyForget(
	keys []core.ChunkKey,
) []network.ServerMessage {
	if len(keys) == 0 {
		return nil
	}
	byDimension := make(map[core.DimensionID][]core.ChunkPos)
	for _, key := range keys {
		delete(current.pendingSnapshots, key)
		delete(current.publications, key)
		byDimension[key.Dimension] = append(byDimension[key.Dimension], key.Pos)
	}
	dimensions := make([]core.DimensionID, 0, len(byDimension))
	for dimension := range byDimension {
		dimensions = append(dimensions, dimension)
	}
	sort.Slice(dimensions, func(i, j int) bool {
		return dimensions[i] < dimensions[j]
	})
	messages := make([]network.ServerMessage, 0, len(dimensions))
	for _, dimension := range dimensions {
		chunks := byDimension[dimension]
		sort.Slice(chunks, func(i, j int) bool {
			if chunks[i].X != chunks[j].X {
				return chunks[i].X < chunks[j].X
			}
			return chunks[i].Z < chunks[j].Z
		})
		for start := 0; start < len(chunks); start += maxForgetChunksPerPacket {
			end := min(start+maxForgetChunksPerPacket, len(chunks))
			messages = append(messages, network.ForgetChunks{
				Dimension: dimension,
				Chunks:    append([]core.ChunkPos(nil), chunks[start:end]...),
			})
		}
	}
	return messages
}

func (server *Server) publishSnapshots(current *session) bool {
	keys := make([]core.ChunkKey, 0, len(current.pendingSnapshots))
	for key := range current.pendingSnapshots {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		leftRequest := current.pendingSnapshots[keys[i]]
		rightRequest := current.pendingSnapshots[keys[j]]
		if leftRequest.resync != rightRequest.resync {
			return leftRequest.resync
		}
		leftDistance := current.snapshotDistanceSquared(keys[i])
		rightDistance := current.snapshotDistanceSquared(keys[j])
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		return chunkKeyLessForPublication(keys[i], keys[j])
	})

	sentChunks := 0
	sentBytes := 0
	for _, key := range keys {
		if sentChunks >= server.config.SnapshotChunks {
			break
		}
		chunk, revision, ready := server.engine.CloneReadyChunk(key)
		if !ready {
			delete(current.pendingSnapshots, key)
			if publication := current.publications[key]; publication != nil {
				publication.resyncQueued = false
			}
			continue
		}
		message, err := buildChunkSnapshot(key.Dimension, chunk, revision)
		if err != nil {
			slog.Error(
				"构造区块快照失败",
				"session", current.id,
				"key", key,
				"error", err,
			)
			server.closePublicationSessionLocked(current, err)
			return false
		}
		payload := message.PayloadBytes()
		if sentChunks > 0 && sentBytes+payload > server.config.SnapshotBytes {
			break
		}
		if !current.enqueue(message) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		delete(current.pendingSnapshots, key)
		publication := current.publications[key]
		publication.snapshotSent = true
		publication.lastRevision = revision
		publication.resyncQueued = false
		sentChunks++
		sentBytes += payload
	}
	return true
}

func (current *session) snapshotDistanceSquared(key core.ChunkKey) int64 {
	if !current.hasView || key.Dimension != current.viewDimension {
		return int64(^uint64(0) >> 1)
	}
	dx := int64(key.Pos.X) - int64(current.viewCenter.X)
	dz := int64(key.Pos.Z) - int64(current.viewCenter.Z)
	return dx*dx + dz*dz
}

package server

import (
	"log/slog"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

type queuedDelta struct {
	key     core.ChunkKey
	message network.BlockChanges
}

func (server *Server) publish(result sim.TickResult) {
	session := server.session
	if session.closed() {
		return
	}

	for _, key := range result.Ready {
		session.queueSnapshot(key, false)
	}
	for _, request := range result.Resync {
		if request.Session != localSessionID {
			continue
		}
		session.queueSnapshot(core.ChunkKey{
			Dimension: request.Dimension,
			Pos:       request.Chunk,
		}, true)
	}

	forgetMessages := session.applyForget(result.Forget[localSessionID])
	deltas := server.classifyDeltas(result.Changes)

	if !server.publishSnapshots() {
		return
	}
	for _, delta := range deltas {
		if !session.enqueue(delta.message) {
			return
		}
		publication := session.publications[delta.key]
		publication.lastRevision = delta.message.NewRevision
	}
	for _, message := range forgetMessages {
		if !session.enqueue(message) {
			return
		}
	}
	for _, rejection := range result.Rejected {
		if rejection.Session != localSessionID {
			continue
		}
		reason, ok := networkRejectReason(rejection.Reason)
		if !ok {
			slog.Error("未知 sim rejection", "reason", rejection.Reason)
			session.close()
			return
		}
		if !session.enqueue(network.CommandRejected{
			Sequence: rejection.Sequence,
			Reason:   reason,
		}) {
			return
		}
	}
}

func (session *session) queueSnapshot(key core.ChunkKey, resync bool) {
	state := session.publications[key]
	if state == nil {
		state = &publication{}
		session.publications[key] = state
	}
	if state.snapshotSent && !resync {
		return
	}
	request := session.pendingSnapshots[key]
	request.resync = request.resync || resync
	session.pendingSnapshots[key] = request
	state.resyncQueued = state.resyncQueued || resync
}

func (session *session) applyForget(
	keys []core.ChunkKey,
) []network.ServerMessage {
	if len(keys) == 0 {
		return nil
	}
	byDimension := make(map[core.DimensionID][]core.ChunkPos)
	for _, key := range keys {
		delete(session.pendingSnapshots, key)
		delete(session.publications, key)
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
		messages = append(messages, network.ForgetChunks{
			Dimension: dimension,
			Chunks:    append([]core.ChunkPos(nil), chunks...),
		})
	}
	return messages
}

func (server *Server) classifyDeltas(
	batches []sim.ChunkChangeBatch,
) []queuedDelta {
	deltas := make([]queuedDelta, 0, len(batches))
	for _, batch := range batches {
		key := core.ChunkKey{
			Dimension: batch.Dimension,
			Pos:       batch.Chunk,
		}
		publication := server.session.publications[key]
		if publication == nil || !publication.snapshotSent {
			server.session.queueSnapshot(key, false)
			continue
		}
		if publication.resyncQueued ||
			publication.lastRevision != batch.BaseRevision {
			server.session.queueSnapshot(key, true)
			continue
		}
		changes := make([]network.BlockChange, len(batch.Changes))
		for index, change := range batch.Changes {
			changes[index] = network.BlockChange{
				Position: change.Position,
				Block:    change.Block,
			}
		}
		message := network.BlockChanges{
			Dimension:    batch.Dimension,
			Chunk:        batch.Chunk,
			BaseRevision: batch.BaseRevision,
			NewRevision:  batch.NewRevision,
			Changes:      changes,
		}
		if err := message.Validate(); err != nil {
			slog.Error("sim 产生非法 block delta", "error", err)
			server.session.close()
			return nil
		}
		deltas = append(deltas, queuedDelta{
			key:     key,
			message: message,
		})
	}
	return deltas
}

func (server *Server) publishSnapshots() bool {
	session := server.session
	keys := make([]core.ChunkKey, 0, len(session.pendingSnapshots))
	for key := range session.pendingSnapshots {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		leftRequest := session.pendingSnapshots[keys[i]]
		rightRequest := session.pendingSnapshots[keys[j]]
		if leftRequest.resync != rightRequest.resync {
			return leftRequest.resync
		}
		leftDistance := session.snapshotDistanceSquared(keys[i])
		rightDistance := session.snapshotDistanceSquared(keys[j])
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
			delete(session.pendingSnapshots, key)
			if publication := session.publications[key]; publication != nil {
				publication.resyncQueued = false
			}
			continue
		}
		message, err := buildChunkSnapshot(key.Dimension, chunk, revision)
		if err != nil {
			slog.Error("构造区块快照失败", "key", key, "error", err)
			session.close()
			return false
		}
		payload := message.PayloadBytes()
		if sentChunks > 0 && sentBytes+payload > server.config.SnapshotBytes {
			break
		}
		if !session.enqueue(message) {
			return false
		}
		delete(session.pendingSnapshots, key)
		publication := session.publications[key]
		publication.snapshotSent = true
		publication.lastRevision = revision
		publication.resyncQueued = false
		sentChunks++
		sentBytes += payload
	}
	return true
}

func (session *session) snapshotDistanceSquared(key core.ChunkKey) int64 {
	if !session.hasView || key.Dimension != session.viewDimension {
		return int64(^uint64(0) >> 1)
	}
	dx := int64(key.Pos.X) - int64(session.viewCenter.X)
	dz := int64(key.Pos.Z) - int64(session.viewCenter.Z)
	return dx*dx + dz*dz
}

func chunkKeyLessForPublication(left, right core.ChunkKey) bool {
	if left.Dimension != right.Dimension {
		return left.Dimension < right.Dimension
	}
	if left.Pos.X != right.Pos.X {
		return left.Pos.X < right.Pos.X
	}
	return left.Pos.Z < right.Pos.Z
}

func networkRejectReason(reason sim.RejectReason) (network.RejectReason, bool) {
	switch reason {
	case sim.RejectInvalidRay:
		return network.RejectInvalidRay, true
	case sim.RejectNoTarget:
		return network.RejectNoTarget, true
	case sim.RejectChunkNotReady:
		return network.RejectChunkNotReady, true
	case sim.RejectProtectedBlock:
		return network.RejectProtectedBlock, true
	case sim.RejectInvalidBlock:
		return network.RejectInvalidBlock, true
	case sim.RejectOccupied:
		return network.RejectOccupied, true
	default:
		return "", false
	}
}

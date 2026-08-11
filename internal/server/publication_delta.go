package server

import (
	"log/slog"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

type queuedDelta struct {
	key     core.ChunkKey
	message network.BlockChanges
}

const maxForgetChunksPerPacket = 4096

func (server *Server) publishForget(current *session, keys []core.ChunkKey) bool {
	for _, message := range current.applyForget(keys) {
		if !current.enqueue(message) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
	}
	return true
}

func (server *Server) publishDeltas(current *session, deltas []queuedDelta) bool {
	for _, delta := range deltas {
		if !current.enqueue(delta.message) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		publication := current.publications[delta.key]
		publication.lastRevision = delta.message.NewRevision
	}
	return true
}

func (server *Server) classifyDeltas(
	current *session,
	batches []sim.ChunkChangeBatch,
) []queuedDelta {
	deltas := make([]queuedDelta, 0, len(batches))
	for _, batch := range batches {
		key := core.ChunkKey{
			Dimension: batch.Dimension,
			Pos:       batch.Chunk,
		}
		if !server.engine.SessionWantsChunk(current.id, key) {
			continue
		}
		publication := current.publications[key]
		if publication == nil || !publication.snapshotSent {
			current.queueSnapshot(key, false)
			continue
		}
		if publication.resyncQueued ||
			publication.lastRevision != batch.BaseRevision {
			current.queueSnapshot(key, true)
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
			slog.Error(
				"sim 产生非法 block delta",
				"session", current.id,
				"error", err,
			)
			server.closePublicationSessionLocked(current, err)
			return nil
		}
		deltas = append(deltas, queuedDelta{
			key:     key,
			message: message,
		})
	}
	return deltas
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
	case sim.RejectInvalidInput:
		return network.RejectInvalidInput, true
	case sim.RejectPlayerNotReady:
		return network.RejectPlayerNotReady, true
	case sim.RejectInvalidSlot:
		return network.RejectInvalidSlot, true
	case sim.RejectHotbarFull:
		return network.RejectHotbarFull, true
	case sim.RejectDropCapacity:
		return network.RejectDropCapacity, true
	case sim.RejectContainerCapacity:
		return network.RejectContainerCapacity, true
	default:
		return "", false
	}
}

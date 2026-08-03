package server

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

type queuedDelta struct {
	key     core.ChunkKey
	message network.BlockChanges
}

const maxForgetChunksPerPacket = 4096

func (server *Server) publish(result sim.TickResult) {
	players := make(map[sim.SessionID]sim.PlayerUpdate, len(result.Players))
	for _, player := range result.Players {
		players[player.Session] = player
	}
	for _, id := range server.sortedPublicationIDsLocked() {
		current := server.publicationSessionLocked(id)
		if current == nil || current.closed() {
			continue
		}
		if observer := server.config.InterestObserver; observer != nil {
			started := time.Now()
			server.publishSession(current, result, players)
			observer(time.Since(started))
		} else {
			server.publishSession(current, result, players)
		}
	}
}

func (server *Server) publishSession(
	current *session,
	result sim.TickResult,
	players map[sim.SessionID]sim.PlayerUpdate,
) {
	server.updateSessionView(current, players[current.id])
	server.queueReadyAndResync(current, result)
	if !server.publishRemoteDespawns(current, players) {
		return
	}
	if !server.publishForget(current, result.Forget[current.id]) {
		return
	}
	deltas := server.classifyDeltas(current, result.Changes)
	if !server.publishSnapshots(current) || !server.publishDeltas(current, deltas) {
		return
	}
	if !server.publishRemoteSpawnsAndStates(current, result.Tick, players) {
		return
	}
	server.publishLocalResult(current, result, players[current.id])
}

func (server *Server) updateSessionView(current *session, player sim.PlayerUpdate) {
	if player.Session != current.id {
		return
	}
	current.hasView = true
	current.viewDimension = player.Dimension
	current.viewCenter = player.ViewCenter
}

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

func (server *Server) publishLocalResult(
	current *session,
	result sim.TickResult,
	playerUpdate sim.PlayerUpdate,
) {
	for _, rejection := range result.Rejected {
		if rejection.Session != current.id {
			continue
		}
		reason, ok := networkRejectReason(rejection.Reason)
		if !ok {
			slog.Error(
				"未知 sim rejection",
				"session", current.id,
				"reason", rejection.Reason,
			)
			server.closePublicationSessionLocked(
				current,
				fmt.Errorf(
					"server: unknown sim rejection: %d",
					rejection.Reason,
				),
			)
			return
		}
		if !current.enqueue(network.CommandRejected{
			Sequence: rejection.Sequence,
			Reason:   reason,
		}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	// 快捷栏只发给所属会话，并排在使客户端开始交互的 Ready 状态之前。
	for _, update := range result.Hotbars {
		if update.Session != current.id {
			continue
		}
		if !current.enqueue(network.HotbarState{Hotbar: update.Hotbar}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return
		}
	}
	if playerUpdate.Session == current.id {
		if !current.enqueue(network.PlayerState{
			ServerTick:        result.Tick,
			LastInputSequence: playerUpdate.LastInputSequence,
			Dimension:         playerUpdate.Dimension,
			Position:          playerUpdate.State.Position,
			Velocity:          playerUpdate.State.Velocity,
			Yaw:               playerUpdate.Yaw,
			Pitch:             playerUpdate.Pitch,
			OnGround:          playerUpdate.State.OnGround,
			Ready:             playerUpdate.Ready,
			Reset:             playerUpdate.Reset,
		}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
		}
	}
}

func (server *Server) closePublicationSessionLocked(current *session, cause error) {
	if current == server.trustedObserver {
		server.detachTrustedObserverLocked(
			current.id,
			current.generation,
			cause,
		)
		return
	}
	server.detachSessionLocked(current.id, current.generation, cause)
}

func (server *Server) sortedPublicationIDsLocked() []sim.SessionID {
	ids := server.sortedSessionIDsLocked()
	if server.trustedObserver != nil {
		ids = append(ids, server.trustedObserver.id)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	return ids
}

func (server *Server) publicationSessionLocked(id sim.SessionID) *session {
	if server.trustedObserver != nil && server.trustedObserver.id == id {
		return server.trustedObserver
	}
	return server.sessions[id]
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
	default:
		return "", false
	}
}

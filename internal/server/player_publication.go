package server

import (
	"bytes"
	"math"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

type visiblePlayer struct {
	Session    sim.SessionID
	Generation uint64
}

type visibleCandidate struct {
	PlayerID    core.PlayerID
	DisplayName string
	Session     sim.SessionID
	Generation  uint64
	Update      sim.PlayerUpdate
}

func (server *Server) publishRemoteDespawns(
	current *session,
	players map[sim.SessionID]sim.PlayerUpdate,
) bool {
	candidates := server.visibleCandidates(current, players)
	ids := make([]core.PlayerID, 0, len(current.visiblePlayers))
	for id := range current.visiblePlayers {
		ids = append(ids, id)
	}
	sortPlayerIDs(ids)
	for _, id := range ids {
		visible := current.visiblePlayers[id]
		candidate, retained := candidates[id]
		if retained && candidate.Session == visible.Session &&
			candidate.Generation == visible.Generation {
			continue
		}
		if !current.enqueue(network.RemotePlayerDespawn{PlayerID: id}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		delete(current.visiblePlayers, id)
	}
	return true
}

func (server *Server) publishRemoteSpawnsAndStates(
	current *session,
	tick uint64,
	players map[sim.SessionID]sim.PlayerUpdate,
) bool {
	candidates := server.visibleCandidates(current, players)
	ids := make([]core.PlayerID, 0, len(candidates))
	for id := range candidates {
		ids = append(ids, id)
	}
	sortPlayerIDs(ids)
	states := make([]network.RemotePlayerState, 0, len(ids))
	if current.visiblePlayers == nil {
		current.visiblePlayers = make(map[core.PlayerID]visiblePlayer)
	}
	for _, id := range ids {
		candidate := candidates[id]
		visible, alreadyVisible := current.visiblePlayers[id]
		if alreadyVisible && visible.Session == candidate.Session &&
			visible.Generation == candidate.Generation {
			states = append(states, network.RemotePlayerState{
				PlayerID:  candidate.PlayerID,
				Dimension: candidate.Update.Dimension,
				Position:  candidate.Update.State.Position,
				Yaw:       candidate.Update.Yaw,
				Pitch:     candidate.Update.Pitch,
				Reset:     candidate.Update.Reset,
			})
			continue
		}
		spawn := network.RemotePlayerSpawn{
			PlayerID:    candidate.PlayerID,
			DisplayName: candidate.DisplayName,
			ServerTick:  tick,
			Dimension:   candidate.Update.Dimension,
			Position:    candidate.Update.State.Position,
			Yaw:         candidate.Update.Yaw,
			Pitch:       candidate.Update.Pitch,
		}
		if err := spawn.Validate(); err != nil {
			server.closePublicationSessionLocked(current, err)
			return false
		}
		if !current.enqueue(spawn) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		current.visiblePlayers[id] = visiblePlayer{
			Session:    candidate.Session,
			Generation: candidate.Generation,
		}
	}
	if len(states) == 0 {
		return true
	}
	message := network.RemotePlayerStates{ServerTick: tick, Players: states}
	if err := message.Validate(); err != nil {
		server.closePublicationSessionLocked(current, err)
		return false
	}
	if !current.enqueue(message) {
		server.closePublicationSessionLocked(current, errSessionOutboxFull)
		return false
	}
	return true
}

func (server *Server) visibleCandidates(
	current *session,
	players map[sim.SessionID]sim.PlayerUpdate,
) map[core.PlayerID]visibleCandidate {
	observer, ok := players[current.id]
	if !ok || !observer.Ready {
		return nil
	}
	candidates := make(map[core.PlayerID]visibleCandidate)
	for id, update := range players {
		if id == current.id || !update.Ready || update.Dimension != observer.Dimension {
			continue
		}
		target := server.sessions[id]
		if target == nil || target.closed() || !target.playerID.Valid() {
			continue
		}
		canonicalName, err := core.NormalizeDisplayName(target.displayName)
		if err != nil || canonicalName != target.displayName {
			continue
		}
		key := playerFootChunk(update)
		if !server.engine.SessionWantsChunk(current.id, key) {
			continue
		}
		publication := current.publications[key]
		if publication == nil || !publication.snapshotSent {
			continue
		}
		candidates[target.playerID] = visibleCandidate{
			PlayerID:    target.playerID,
			DisplayName: target.displayName,
			Session:     target.id,
			Generation:  target.generation,
			Update:      update,
		}
	}
	return candidates
}

func playerFootChunk(update sim.PlayerUpdate) core.ChunkKey {
	return core.ChunkKey{
		Dimension: update.Dimension,
		Pos: core.ChunkPos{
			X: int32(math.Floor(float64(update.State.Position.X()) / core.SectionSize)),
			Z: int32(math.Floor(float64(update.State.Position.Z()) / core.SectionSize)),
		},
	}
}

func sortPlayerIDs(ids []core.PlayerID) {
	sort.Slice(ids, func(i, j int) bool {
		return bytes.Compare(ids[i][:], ids[j][:]) < 0
	})
}

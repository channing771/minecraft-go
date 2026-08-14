package server

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
)

type companionVisibleCandidate struct {
	Definition companion.Definition
	Update     sim.CompanionUpdate
	Foot       core.ChunkKey
}

func (server *Server) companionPublicationCandidates(
	current *session,
	updates []sim.CompanionUpdate,
	definitions map[companion.ID]companion.Definition,
) (map[companion.ID]companionVisibleCandidate, bool) {
	if len(updates) == 0 {
		return nil, true
	}
	candidates := make(map[companion.ID]companionVisibleCandidate, len(updates))
	for _, update := range updates {
		definition, ok := definitions[update.ID]
		if !ok {
			server.closePublicationSessionLocked(current, fmt.Errorf(
				"server: unknown companion definition: %s",
				update.ID,
			))
			return nil, false
		}
		candidates[update.ID] = companionVisibleCandidate{
			Definition: definition,
			Update:     update,
			Foot:       publicationFootChunk(update.Dimension, update.State.Position),
		}
	}
	return candidates, true
}

func (server *Server) publishCompanionDespawns(
	current *session,
	candidates map[companion.ID]companionVisibleCandidate,
) bool {
	ids := make([]companion.ID, 0, len(current.visibleCompanions))
	for id := range current.visibleCompanions {
		ids = append(ids, id)
	}
	sortCompanionPublicationIDs(ids)
	for _, id := range ids {
		candidate, retained := candidates[id]
		if retained && server.companionCandidateVisible(current, candidate) {
			continue
		}
		if !current.enqueue(network.CompanionDespawn{ID: id}) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		delete(current.visibleCompanions, id)
	}
	return true
}

func (server *Server) queueCompanionSnapshots(
	current *session,
	candidates map[companion.ID]companionVisibleCandidate,
) {
	for _, candidate := range candidates {
		if !server.engine.SessionWantsChunk(current.id, candidate.Foot) {
			continue
		}
		publication := current.publications[candidate.Foot]
		if publication != nil && publication.snapshotSent {
			continue
		}
		info, ready := server.engine.ChunkInfo(candidate.Foot)
		if !ready || info.State != sim.ChunkReady {
			continue
		}
		// 伙伴兴趣可能早于当前 session 把区块保持为 Ready，此时不会再有全局
		// Ready 事件；仍须给新观察者补排自己的首次 snapshot。
		current.queueSnapshot(candidate.Foot, false)
	}
}

func (server *Server) publishCompanionSpawnsAndStates(
	current *session,
	tick uint64,
	candidates map[companion.ID]companionVisibleCandidate,
) bool {
	spawnIDs := make([]companion.ID, 0, len(candidates))
	stateIDs := make([]companion.ID, 0, len(current.visibleCompanions))
	for id, candidate := range candidates {
		if !server.companionCandidateVisible(current, candidate) {
			continue
		}
		if _, visible := current.visibleCompanions[id]; visible {
			stateIDs = append(stateIDs, id)
		} else {
			spawnIDs = append(spawnIDs, id)
		}
	}
	sortCompanionPublicationIDs(spawnIDs)
	sortCompanionPublicationIDs(stateIDs)

	spawns := make([]network.CompanionSpawn, 0, len(spawnIDs))
	for _, id := range spawnIDs {
		candidate := candidates[id]
		spawn := network.CompanionSpawn{
			ID:        id,
			Name:      candidate.Definition.Name,
			Tick:      tick,
			Dimension: candidate.Update.Dimension,
			Position:  candidate.Update.State.Position,
			Yaw:       candidate.Update.Yaw,
			Pitch:     candidate.Update.Pitch,
		}
		if err := spawn.Validate(); err != nil {
			server.closePublicationSessionLocked(current, err)
			return false
		}
		spawns = append(spawns, spawn)
	}

	states := make([]network.CompanionState, 0, len(stateIDs))
	for _, id := range stateIDs {
		update := candidates[id].Update
		states = append(states, network.CompanionState{
			ID:        id,
			Dimension: update.Dimension,
			Position:  update.State.Position,
			Yaw:       update.Yaw,
			Pitch:     update.Pitch,
			Reset:     update.Reset,
		})
	}
	var stateMessage network.CompanionStates
	if len(states) != 0 {
		stateMessage = network.CompanionStates{Tick: tick, States: states}
		if err := stateMessage.Validate(); err != nil {
			server.closePublicationSessionLocked(current, err)
			return false
		}
	}

	if current.visibleCompanions == nil {
		current.visibleCompanions = make(map[companion.ID]struct{})
	}
	for _, spawn := range spawns {
		if !current.enqueue(spawn) {
			server.closePublicationSessionLocked(current, errSessionOutboxFull)
			return false
		}
		current.visibleCompanions[spawn.ID] = struct{}{}
	}
	if len(states) != 0 && !current.enqueue(stateMessage) {
		server.closePublicationSessionLocked(current, errSessionOutboxFull)
		return false
	}
	return true
}

func (server *Server) companionCandidateVisible(
	current *session,
	candidate companionVisibleCandidate,
) bool {
	if !server.engine.SessionWantsChunk(current.id, candidate.Foot) {
		return false
	}
	publication := current.publications[candidate.Foot]
	return publication != nil && publication.snapshotSent
}

func sortCompanionPublicationIDs(ids []companion.ID) {
	sort.Slice(ids, func(i, j int) bool {
		return bytes.Compare(ids[i][:], ids[j][:]) < 0
	})
}

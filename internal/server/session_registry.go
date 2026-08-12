package server

import (
	"errors"
	"sort"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/sim"
)

const trustedObserverSessionID = sim.SessionID(^uint64(0))

// attachSessionLocked owns player session construction and registry mutation.
// The public AttachSession entry establishes server.stepMu before calling it.
func (server *Server) attachSessionLocked(
	spec SessionSpec,
) (<-chan SessionExit, error) {
	canonical, err := core.NormalizeDisplayName(spec.DisplayName)
	if server.lifecycle != serverRunning ||
		spec.ID == 0 ||
		spec.Generation == 0 ||
		spec.Endpoint == nil ||
		spec.ID == trustedObserverSessionID ||
		!spec.PlayerID.Valid() ||
		err != nil ||
		canonical != spec.DisplayName {
		return nil, ErrInvalidSession
	}
	if server.sessions[spec.ID] != nil || server.playerSessions[spec.PlayerID] != 0 {
		return nil, ErrSessionExists
	}
	server.engine.RegisterPlayer(spec.ID, spec.Restore)
	current := newSession(
		server.ctx,
		spec,
		server.config,
		&server.workers,
		server.config.heartbeatClock,
		server.DetachSession,
	)
	server.sessions[spec.ID] = current
	server.playerSessions[spec.PlayerID] = spec.ID
	server.workers.Add(1)
	go server.endpointReader(current)
	return current.exit, nil
}

// detachSessionLocked owns player session teardown and registry mutation.
// The public DetachSession entry and shutdown caller establish server.stepMu.
func (server *Server) detachSessionLocked(
	id sim.SessionID,
	generation uint64,
	cause error,
) bool {
	current := server.sessions[id]
	if current == nil || current.generation != generation {
		return false
	}
	delete(server.sessions, id)
	if server.playerSessions[current.playerID] == id {
		delete(server.playerSessions, current.playerID)
	}
	snapshot, hasSnapshot := server.engine.UnregisterSession(id)
	current.shutdown()
	current.exit <- SessionExit{
		ID:          id,
		Generation:  generation,
		Snapshot:    snapshot,
		HasSnapshot: hasSnapshot,
		Err:         cause,
	}
	close(current.exit)
	return true
}

// attachTrustedObserverLocked owns observer construction and registry mutation.
// The public AttachTrustedObserver entry establishes server.stepMu before calling it.
func (server *Server) attachTrustedObserverLocked(
	endpoint network.ServerEndpoint,
) error {
	if server.lifecycle != serverRunning ||
		!server.config.TrustedObserver ||
		endpoint == nil {
		return ErrInvalidSession
	}
	if server.trustedObserver != nil {
		return ErrSessionExists
	}
	server.trustedObserverGeneration++
	server.engine.RegisterObserverSession(trustedObserverSessionID)
	server.trustedObserver = newObserverSession(
		server.ctx,
		trustedObserverSessionID,
		server.trustedObserverGeneration,
		endpoint,
		server.config.OutboxCapacity,
		&server.workers,
		server.detachTrustedObserver,
	)
	return nil
}

// detachTrustedObserverLocked owns observer teardown and registry mutation.
// Its callers establish server.stepMu before calling it.
func (server *Server) detachTrustedObserverLocked(
	id sim.SessionID,
	generation uint64,
	_ error,
) bool {
	current := server.trustedObserver
	if current == nil ||
		current.id != id ||
		current.generation != generation {
		return false
	}
	server.trustedObserver = nil
	server.engine.UnregisterSession(id)
	current.shutdown()
	return true
}

// setTrustedObserverCenterLocked serializes the observer's input with Step.
// The public SetTrustedObserverCenter entry establishes server.stepMu.
func (server *Server) setTrustedObserverCenterLocked(
	dimension core.DimensionID,
	center core.ChunkPos,
) error {
	if server.lifecycle != serverRunning ||
		!server.config.TrustedObserver ||
		server.trustedObserver == nil {
		return ErrTrustedObserverDisabled
	}
	if dimension != core.Overworld {
		return errors.New("server: trusted observer center must be overworld")
	}
	request := trustedObserverCenter{dimension: dimension, center: center}
	server.trustedObserverMu.Lock()
	defer server.trustedObserverMu.Unlock()
	select {
	case <-server.trustedObserverCenters:
	default:
	}
	select {
	case server.trustedObserverCenters <- request:
	default:
		panic("server: trusted observer queue invariant violated")
	}
	return nil
}

// appliedTrustedObserverCenterLocked reads the observer state while stepMu is held.
func (server *Server) appliedTrustedObserverCenterLocked() (
	core.DimensionID,
	core.ChunkPos,
	uint64,
	bool,
) {
	applied := server.appliedTrustedObserver
	if server.trustedObserver == nil || applied.sequence == 0 {
		return 0, core.ChunkPos{}, 0, false
	}
	return applied.dimension, applied.center, applied.sequence, true
}

// sortedSessionIDsLocked is used by registry callers while server.stepMu is held.
func (server *Server) sortedSessionIDsLocked() []sim.SessionID {
	ids := make([]sim.SessionID, 0, len(server.sessions))
	for id := range server.sessions {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

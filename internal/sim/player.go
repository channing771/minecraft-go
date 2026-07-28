package sim

import (
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
)

type PlayerLifecycle uint8

const (
	PlayerPendingSpawn PlayerLifecycle = iota
	PlayerActive
)

type PlayerUpdate struct {
	Session           SessionID
	Dimension         core.DimensionID
	ViewCenter        core.ChunkPos
	State             physics.State
	Yaw, Pitch        float32
	LastInputSequence uint64
	Ready             bool
	Reset             bool
}

type playerState struct {
	lifecycle         PlayerLifecycle
	state             physics.State
	input             physics.Input
	yaw, pitch        float32
	lastInputSequence uint64
	reset             bool

	candidates         []spawnColumn
	candidateChunks    []core.ChunkPos
	nextCandidate      int
	spawnWanted        map[core.ChunkPos]struct{}
	exhausted          bool
	exhaustedRevisions []uint64
}

func (engine *Engine) RegisterSession(
	id SessionID,
	dimensionID core.DimensionID,
	anchor core.ChunkPos,
) {
	if engine.dimensions[dimensionID] == nil {
		panic("sim: register session in unknown dimension")
	}
	if engine.sessions[id] != nil {
		panic("sim: duplicate registered session")
	}
	candidates := spawnCandidates(anchor)
	player := &playerState{
		lifecycle: PlayerPendingSpawn,
		state: physics.State{Position: mgl32.Vec3{
			float32(anchor.X)*core.SectionSize + 0.5,
			core.MaxY + 1,
			float32(anchor.Z)*core.SectionSize + 0.5,
		}},
		candidates:      candidates,
		candidateChunks: spawnCandidateChunks(candidates),
		spawnWanted:     make(map[core.ChunkPos]struct{}),
	}
	player.spawnWanted[anchor] = struct{}{}
	engine.sessions[id] = &sessionState{
		hasView:   true,
		dimension: dimensionID,
		center:    anchor,
		wanted:    make(map[core.ChunkKey]struct{}),
		player:    player,
	}
	engine.subscriptionsDirty = true
}

func (engine *Engine) Player(id SessionID) (PlayerUpdate, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil {
		return PlayerUpdate{}, false
	}
	return session.player.update(id, session), true
}

func (player *playerState) update(
	id SessionID,
	session *sessionState,
) PlayerUpdate {
	return PlayerUpdate{
		Session:           id,
		Dimension:         session.dimension,
		ViewCenter:        session.center,
		State:             player.state,
		Yaw:               player.yaw,
		Pitch:             player.pitch,
		LastInputSequence: player.lastInputSequence,
		Ready:             player.lifecycle == PlayerActive,
		Reset:             player.reset,
	}
}

func (engine *Engine) publishPlayers(result *TickResult) {
	sessions := make([]SessionID, 0, len(engine.sessions))
	for id, session := range engine.sessions {
		if session.player != nil {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	for _, id := range sessions {
		session := engine.sessions[id]
		result.Players = append(result.Players, session.player.update(id, session))
		session.player.reset = false
	}
}

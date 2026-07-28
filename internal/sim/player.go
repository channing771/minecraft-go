package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
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
	anchor            core.ChunkPos
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
		anchor:    anchor,
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

func (engine *Engine) PlayerHash(id SessionID) ([32]byte, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil {
		return [32]byte{}, false
	}
	player := session.player
	var encoded [53]byte
	offset := 0
	putUint32 := func(value uint32) {
		binary.LittleEndian.PutUint32(encoded[offset:], value)
		offset += 4
	}
	putFloat32 := func(value float32) {
		putUint32(math.Float32bits(value))
	}
	putBool := func(value bool) {
		if value {
			encoded[offset] = 1
		}
		offset++
	}

	putUint32(uint32(session.dimension))
	encoded[offset] = byte(player.lifecycle)
	offset++
	for _, value := range player.state.Position {
		putFloat32(value)
	}
	for _, value := range player.state.Velocity {
		putFloat32(value)
	}
	putFloat32(player.yaw)
	putFloat32(player.pitch)
	putBool(player.state.OnGround)
	encoded[offset] = byte(player.input.MoveX)
	offset++
	encoded[offset] = byte(player.input.MoveZ)
	offset++
	putBool(player.input.Jump)
	putFloat32(player.input.Yaw)
	binary.LittleEndian.PutUint64(encoded[offset:], player.lastInputSequence)
	return sha256.Sum256(encoded[:]), true
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

func (engine *Engine) advanceActivePlayers() {
	sessions := make([]SessionID, 0, len(engine.sessions))
	for id, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerActive {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	for _, id := range sessions {
		session := engine.sessions[id]
		player := session.player
		if !physics.ValidState(player.state) || player.state.Position.Y() < core.MinY-16 {
			player.beginReset()
			engine.subscriptionsDirty = true
			continue
		}
		if !engine.tryUnstick(player, engine.dimensions[session.dimension]) {
			player.beginReset()
			engine.subscriptionsDirty = true
			continue
		}
		step := physics.Step(
			player.state,
			player.input,
			dimensionCollisionSource{dimension: engine.dimensions[session.dimension]},
		)
		player.state = step.State
	}
}

func (engine *Engine) advancePendingPlayersPreservingInputSequence() {
	type pendingAck struct {
		player   *playerState
		sequence uint64
	}
	var acknowledgements []pendingAck
	for _, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerPendingSpawn &&
			session.player.lastInputSequence != 0 {
			acknowledgements = append(acknowledgements, pendingAck{
				player: session.player, sequence: session.player.lastInputSequence,
			})
		}
	}
	engine.advancePendingPlayers()
	for _, acknowledgement := range acknowledgements {
		if acknowledgement.player.lifecycle == PlayerActive {
			acknowledgement.player.lastInputSequence = acknowledgement.sequence
		}
	}
}

func (engine *Engine) tryUnstick(player *playerState, dimension *Dimension) bool {
	source := dimensionCollisionSource{dimension: dimension}
	if free, ready := playerBoundsAreFree(player.state.Position, source); !ready {
		return false
	} else if free {
		return true
	}
	for step := 1; step <= 16; step++ {
		candidate := player.state.Position
		candidate[1] += float32(step) / 16
		free, ready := playerBoundsAreFree(candidate, source)
		if !ready {
			return false
		}
		if free {
			player.state.Position = candidate
			return true
		}
	}
	return false
}

func (player *playerState) beginReset() {
	player.lifecycle = PlayerPendingSpawn
	player.state = physics.State{Position: mgl32.Vec3{
		float32(player.anchor.X)*core.SectionSize + 0.5,
		core.MaxY + 1,
		float32(player.anchor.Z)*core.SectionSize + 0.5,
	}}
	player.input = physics.Input{}
	player.reset = false
	player.nextCandidate = 0
	player.exhausted = false
	player.exhaustedRevisions = nil
	player.spawnWanted[player.anchor] = struct{}{}
}

func (engine *Engine) derivePlayerCenters() bool {
	changed := false
	for _, session := range engine.sessions {
		if session.player == nil {
			continue
		}
		center := session.player.anchor
		if session.player.lifecycle == PlayerActive {
			center = (core.BlockPos{
				X: int32(math.Floor(float64(session.player.state.Position.X()))),
				Z: int32(math.Floor(float64(session.player.state.Position.Z()))),
			}).Chunk()
		}
		if center != session.center {
			session.center = center
			changed = true
		}
	}
	return changed
}

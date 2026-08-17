package sim

import (
	"bytes"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

const (
	companionInterestRadius = 1
	companionSpawnRadius    = int32(16)
)

// CompanionRestore 描述伙伴的可选持久化身体和出生回退锚点。
type CompanionRestore struct {
	ID             companion.ID
	Body           *companion.Body
	SpawnDimension core.DimensionID
	SpawnAnchor    core.ChunkPos
}

// CompanionUpdate 是一个已激活伙伴在当前权威 tick 的静态身体状态。
// Mining 对齐玩家 MiningUpdate 语义承载本 tick 的采掘进度；这是 sim→server
// 的内部结构，wire 上的 CompanionStates 形状不变。
type CompanionUpdate struct {
	ID         companion.ID
	Dimension  core.DimensionID
	State      physics.State
	Yaw, Pitch float32
	Reset      bool
	Mining     MiningUpdate
}

type companionState struct {
	// actorState 内嵌玩家与伙伴共有的运动/朝向/背包/采掘状态（物理体由提升的
	// state 字段承载，等价于旧 body 字段）；稳定 CompanionID 与激活状态等
	// 伙伴专属语义留在本结构体。提取范围与动机见 actor.go。
	actorState
	id            companion.ID
	dimension     core.DimensionID
	active, reset bool
	// miningTarget 是 MineHold action 携带、伙伴专属的采掘意图目标：玩家的目标
	// 由视线 raycast 逐 tick 派生，不需要持久化；伙伴的目标由 Task Runner 显式
	// 指定，跨 tick 保持（Manager 每个采掘 tick 重新提交同一目标）。
	miningTarget       core.BlockPos
	restoreCandidates  []restoreCandidate
	nextRestore        int
	restoreWanted      map[core.ChunkKey]struct{}
	spawnCandidates    []spawnColumn
	spawnChunks        []core.ChunkPos
	spawnWanted        map[core.ChunkPos]struct{}
	spawnIndex         int
	exhausted          bool
	exhaustedRevisions []uint64
}

// RegisterCompanion 注册一个独立于玩家会话的待恢复伙伴。
func (engine *Engine) RegisterCompanion(restore CompanionRestore) {
	if !restore.ID.Valid() {
		panic("sim: register companion with invalid ID")
	}
	if engine.dimensions[restore.SpawnDimension] == nil {
		panic("sim: register companion in unknown spawn dimension")
	}
	if engine.companions[restore.ID] != nil {
		panic("sim: duplicate registered companion")
	}
	if len(engine.companions) >= companion.MaxActive {
		panic("sim: too many registered companions")
	}
	candidates := spawnCandidates(restore.SpawnAnchor, companionSpawnRadius)
	entry := &companionState{
		id:              restore.ID,
		dimension:       restore.SpawnDimension,
		actorState:      actorState{state: physics.State{Position: mgl32.Vec3{float32(restore.SpawnAnchor.X)*core.SectionSize + 0.5, core.MaxY + 1, float32(restore.SpawnAnchor.Z)*core.SectionSize + 0.5}}},
		restoreWanted:   make(map[core.ChunkKey]struct{}),
		spawnCandidates: candidates,
		spawnChunks:     spawnCandidateChunks(candidates),
		spawnWanted:     map[core.ChunkPos]struct{}{restore.SpawnAnchor: {}},
	}
	if restore.Body != nil {
		if restore.Body.ID != restore.ID {
			panic("sim: companion restore ID mismatch")
		}
		if !restore.Body.Inventory.Valid() {
			panic("sim: register companion with invalid inventory")
		}
		entry.state.Position = mgl32.Vec3(restore.Body.Position)
		entry.yaw = restore.Body.Yaw
		entry.pitch = restore.Body.Pitch
		entry.inventory = restore.Body.Inventory
		entry.restoreCandidates = []restoreCandidate{{location: PlayerLocation{
			Dimension: restore.Body.Dimension,
			Position:  entry.state.Position,
		}}}
	}
	engine.companions[restore.ID] = entry
	engine.subscriptionsDirty = true
}

// CompanionBodies 返回按伙伴 ID 排序的已激活身体快照。
func (engine *Engine) CompanionBodies() []companion.Body {
	ids := engine.activeCompanionIDs()
	bodies := make([]companion.Body, 0, len(ids))
	for _, id := range ids {
		state := engine.companions[id]
		bodies = append(bodies, companion.Body{
			ID:        id,
			Dimension: state.dimension,
			Position:  [3]float32(state.state.Position),
			Yaw:       state.yaw,
			Pitch:     state.pitch,
			Inventory: state.inventory,
		})
	}
	return bodies
}

func (engine *Engine) advancePendingCompanions() {
	ids := make([]companion.ID, 0, len(engine.companions))
	for id, state := range engine.companions {
		if !state.active {
			ids = append(ids, id)
		}
	}
	sortCompanionIDs(ids)
	for _, id := range ids {
		engine.advancePendingCompanion(engine.companions[id])
	}
}

func (engine *Engine) advancePendingCompanion(state *companionState) {
	for state.nextRestore < len(state.restoreCandidates) {
		candidate := state.restoreCandidates[state.nextRestore]
		for _, key := range restoreCandidateChunks(candidate.location) {
			if _, retained := state.restoreWanted[key]; !retained {
				state.restoreWanted[key] = struct{}{}
				engine.subscriptionsDirty = true
			}
		}
		valid, ready, onGround := engine.validateRestoreCandidate(candidate)
		if !ready {
			return
		}
		if valid {
			state.activate(candidate.location, onGround)
			engine.subscriptionsDirty = true
			return
		}
		state.nextRestore++
	}
	if len(state.restoreWanted) != 0 {
		state.restoreWanted = nil
		engine.subscriptionsDirty = true
	}

	dimension := engine.dimensions[state.dimension]
	if state.exhausted {
		if !spawnChunkRevisionsChanged(dimension, state.spawnChunks, state.exhaustedRevisions) {
			return
		}
		state.exhausted = false
		state.exhaustedRevisions = nil
		state.spawnIndex = 0
	}

	source := dimensionCollisionSource{dimension: dimension}
	for state.spawnIndex < len(state.spawnCandidates) {
		candidate := state.spawnCandidates[state.spawnIndex]
		chunk := (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk()
		if _, retained := state.spawnWanted[chunk]; !retained {
			state.spawnWanted[chunk] = struct{}{}
			engine.subscriptionsDirty = true
		}
		position, valid, ready := findSpawnInColumn(candidate, dimension, source)
		if !ready {
			if info, ok := dimension.Info(chunk); !ok || info.State == ChunkFailed {
				engine.subscriptionsDirty = true
			}
			return
		}
		if valid {
			state.activate(PlayerLocation{Dimension: state.dimension, Position: position}, true)
			engine.subscriptionsDirty = true
			return
		}
		state.spawnIndex++
	}

	state.exhausted = true
	state.exhaustedRevisions = make([]uint64, len(state.spawnChunks))
	for index, chunk := range state.spawnChunks {
		info, ok := dimension.Info(chunk)
		if !ok || info.State != ChunkReady {
			panic("sim: exhausted companion spawn chunk is not ready")
		}
		state.exhaustedRevisions[index] = info.Revision
	}
}

func (state *companionState) activate(location PlayerLocation, onGround bool) {
	state.dimension = location.Dimension
	state.state = physics.State{Position: location.Position, OnGround: onGround}
	state.active = true
	state.reset = true
	state.restoreCandidates = nil
	state.restoreWanted = nil
}

func (engine *Engine) publishCompanions(result *TickResult) {
	for _, id := range engine.activeCompanionIDs() {
		state := engine.companions[id]
		result.Companions = append(result.Companions, CompanionUpdate{
			ID: id, Dimension: state.dimension, State: state.state,
			Yaw: state.yaw, Pitch: state.pitch, Reset: state.reset,
			Mining: state.mining.update(),
		})
		state.reset = false
	}
}

func (engine *Engine) activeCompanionIDs() []companion.ID {
	ids := make([]companion.ID, 0, len(engine.companions))
	for id, state := range engine.companions {
		if state.active {
			ids = append(ids, id)
		}
	}
	sortCompanionIDs(ids)
	return ids
}

func sortCompanionIDs(ids []companion.ID) {
	sort.Slice(ids, func(i, j int) bool {
		return bytes.Compare(ids[i][:], ids[j][:]) < 0
	})
}

func companionChunk(position mgl32.Vec3) core.ChunkPos {
	return (core.BlockPos{
		X: int32(math.Floor(float64(position.X()))),
		Z: int32(math.Floor(float64(position.Z()))),
	}).Chunk()
}

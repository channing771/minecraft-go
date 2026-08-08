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
	Mining            MiningUpdate
	// Health 是本 tick 结束时的权威生命值，0..core.MaxHealth；只发布给玩家本人。
	Health uint8
	// WorldTimeTicks 是本 tick 结束时的权威绝对世界时间。
	WorldTimeTicks uint64
}

type PlayerLocation struct {
	Dimension core.DimensionID
	Position  mgl32.Vec3
}

type PlayerRestore struct {
	Current        *PlayerLocation
	Safe           *PlayerLocation
	Yaw, Pitch     float32
	SpawnDimension core.DimensionID
	SpawnAnchor    core.ChunkPos
	Inventory      core.Inventory
	// Health 是存档中的权威生命值；零值代表"缺失"，注册时会回落到 core.MaxHealth。
	Health uint8
}

type PlayerSnapshot struct {
	Current    PlayerLocation
	Yaw, Pitch float32
	Safe       *PlayerLocation
	Inventory  core.Inventory
	Health     uint8
}

// InventoryUpdate 是一名玩家在本 tick 的最终权威物品状态，只发送给所属会话。
type InventoryUpdate struct {
	Session   SessionID
	Inventory core.Inventory
}

type restoreCandidate struct {
	location       PlayerLocation
	requireSupport bool
}

type playerState struct {
	lifecycle         PlayerLifecycle
	anchor            core.ChunkPos
	state             physics.State
	input             physics.Input
	yaw, pitch        float32
	lastInputSequence uint64
	miningHeld        bool
	mining            playerMiningState
	reset             bool
	// spawned 记录这名玩家是否至少出生过一次。出生之前权威状态与登录时恢复的
	// 状态完全一致；出生之后两者就可能分岔，快照因而必须可被观察。见 persistable。
	spawned        bool
	inventory      core.Inventory
	inventoryDirty bool
	// health 是服务端单写者拥有的权威生命值，0..core.MaxHealth。
	health uint8
	// peakY 是离地后到达过的最高高度，瞬态字段，不持久化、不进入快照/哈希。
	// 落地、传送、重生、维度 reset 都会把它重置为当前高度。
	peakY float32
	// ticksSinceDamage 是自最后一次受伤以来连续未受伤的 tick 数，瞬态字段，
	// 不持久化、不进入快照/哈希；满血时不推进。见 health_regen.go。
	ticksSinceDamage uint32

	restoreCandidates  []restoreCandidate
	nextRestore        int
	restoreWanted      map[core.ChunkKey]struct{}
	safe               *PlayerLocation
	candidates         []spawnColumn
	candidateChunks    []core.ChunkPos
	nextCandidate      int
	spawnWanted        map[core.ChunkPos]struct{}
	exhausted          bool
	exhaustedRevisions []uint64
}

func (engine *Engine) RegisterPlayer(id SessionID, restore PlayerRestore) {
	if engine.dimensions[restore.SpawnDimension] == nil {
		panic("sim: register session in unknown dimension")
	}
	if engine.sessions[id] != nil {
		panic("sim: duplicate registered session")
	}
	if !restore.Inventory.Valid() {
		panic("sim: register session with invalid inventory")
	}
	candidates := spawnCandidates(restore.SpawnAnchor, engine.tunables.SpawnRadius)
	health := restore.Health
	if health == 0 {
		health = core.MaxHealth
	}
	player := &playerState{
		lifecycle: PlayerPendingSpawn,
		anchor:    restore.SpawnAnchor,
		state: physics.State{Position: mgl32.Vec3{
			float32(restore.SpawnAnchor.X)*core.SectionSize + 0.5,
			core.MaxY + 1,
			float32(restore.SpawnAnchor.Z)*core.SectionSize + 0.5,
		}},
		yaw:             restore.Yaw,
		pitch:           restore.Pitch,
		inventory:       restore.Inventory,
		health:          health,
		inventoryDirty:  true,
		restoreWanted:   make(map[core.ChunkKey]struct{}),
		candidates:      candidates,
		candidateChunks: spawnCandidateChunks(candidates),
		spawnWanted:     make(map[core.ChunkPos]struct{}),
	}
	if restore.Current != nil {
		player.restoreCandidates = append(player.restoreCandidates, restoreCandidate{
			location: *restore.Current,
		})
	}
	if restore.Safe != nil {
		safe := *restore.Safe
		player.safe = &safe
		player.restoreCandidates = append(player.restoreCandidates, restoreCandidate{
			location:       safe,
			requireSupport: true,
		})
	}
	player.spawnWanted[restore.SpawnAnchor] = struct{}{}
	engine.sessions[id] = &sessionState{
		hasView:   true,
		dimension: restore.SpawnDimension,
		center:    restore.SpawnAnchor,
		wanted:    make(map[core.ChunkKey]struct{}),
		player:    player,
	}
	engine.subscriptionsDirty = true
}

func (engine *Engine) RegisterSession(
	id SessionID,
	dimensionID core.DimensionID,
	anchor core.ChunkPos,
) {
	engine.RegisterPlayer(id, PlayerRestore{
		SpawnDimension: dimensionID,
		SpawnAnchor:    anchor,
	})
}

func (engine *Engine) Player(id SessionID) (PlayerUpdate, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil {
		return PlayerUpdate{}, false
	}
	return session.player.update(id, session, engine.WorldTime()), true
}

func (engine *Engine) PlayerSnapshot(id SessionID) (PlayerSnapshot, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil || !session.player.persistable() {
		return PlayerSnapshot{}, false
	}
	return session.player.snapshot(session.dimension), true
}

// persistable 报告这名玩家的权威状态是否可能已经与登录时恢复的状态分岔，
// 因而必须能被外部观察并落盘。
//
// 从未出生过的待重生玩家没有分岔，跳过它可以避免用尚未校验的锚点列覆盖存档里
// 的精确位置。出生过之后就不同了：死亡结算在同一 tick 内把背包掉进世界、清空
// 权威背包并转入待重生，这段窗口若取不到快照，落盘的会是死亡前的满背包，
// 而掉落物已经随区块持久化躺在地上，一份物品因此变成两份。待重生玩家的
// Current 是 beginReset 置的锚点列（y = MaxY + 1），重连时会被
// validateRestoreCandidate 拒绝并回落到安全点/出生候选，持久化它是无害的。
func (player *playerState) persistable() bool {
	return player.lifecycle == PlayerActive || player.spawned
}

// SetPlayerPositionForTest 直接写入某个会话玩家的权威位置，仅供测试构造固定场景，
// 例如把玩家踩到世界高度下界以下触发 beginReset。
func (engine *Engine) SetPlayerPositionForTest(id SessionID, position mgl32.Vec3) {
	session := engine.sessions[id]
	if session == nil || session.player == nil {
		return
	}
	session.player.state.Position = position
}

func (engine *Engine) UnregisterSession(id SessionID) (PlayerSnapshot, bool) {
	session := engine.sessions[id]
	if session == nil {
		return PlayerSnapshot{}, false
	}
	var snapshot PlayerSnapshot
	hasSnapshot := session.player != nil && session.player.persistable()
	if hasSnapshot {
		snapshot = session.player.snapshot(session.dimension)
	}
	delete(engine.sessions, id)
	engine.subscriptionsDirty = true
	return snapshot, hasSnapshot
}

func (player *playerState) snapshot(
	dimension core.DimensionID,
) PlayerSnapshot {
	snapshot := PlayerSnapshot{
		Current: PlayerLocation{
			Dimension: dimension,
			Position:  player.state.Position,
		},
		Yaw:       player.yaw,
		Pitch:     player.pitch,
		Inventory: player.inventory,
		Health:    player.health,
	}
	if player.safe != nil {
		safe := *player.safe
		snapshot.Safe = &safe
	}
	return snapshot
}

func (engine *Engine) PlayerHash(id SessionID) ([32]byte, bool) {
	session := engine.sessions[id]
	if session == nil || session.player == nil {
		return [32]byte{}, false
	}
	player := session.player
	// 54 字节玩家状态（含 1 字节生命值）+ 1 字节选中栏位 + 每个物品栏位 3 字节。
	var encoded [54 + 1 + core.InventorySlots*3]byte
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
	encoded[offset] = player.health
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
	offset += 8
	encoded[offset] = player.inventory.Hotbar.Selected
	offset++
	for _, stack := range player.inventory.Hotbar.Slots {
		binary.LittleEndian.PutUint16(encoded[offset:], uint16(stack.Item))
		offset += 2
		encoded[offset] = stack.Count
		offset++
	}
	for _, stack := range player.inventory.Backpack {
		binary.LittleEndian.PutUint16(encoded[offset:], uint16(stack.Item))
		offset += 2
		encoded[offset] = stack.Count
		offset++
	}
	return sha256.Sum256(encoded[:]), true
}

func (player *playerState) update(
	id SessionID,
	session *sessionState,
	worldTime uint64,
) PlayerUpdate {
	return PlayerUpdate{
		WorldTimeTicks:    worldTime,
		Session:           id,
		Dimension:         session.dimension,
		ViewCenter:        session.center,
		State:             player.state,
		Yaw:               player.yaw,
		Pitch:             player.pitch,
		LastInputSequence: player.lastInputSequence,
		Ready:             player.lifecycle == PlayerActive,
		Reset:             player.reset,
		Mining:            player.mining.update(),
		Health:            player.health,
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
		result.Players = append(
			result.Players, session.player.update(id, session, result.WorldTimeTicks),
		)
		session.player.reset = false
	}
}

// publishInventories 为每名 Active 且 dirty 的玩家产出本 tick 唯一一份完整物品状态。
func (engine *Engine) publishInventories(result *TickResult) {
	sessions := make([]SessionID, 0, len(engine.sessions))
	for id, session := range engine.sessions {
		if session.player != nil && session.player.inventoryDirty &&
			session.player.lifecycle == PlayerActive {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	for _, id := range sessions {
		player := engine.sessions[id].player
		result.Inventories = append(result.Inventories, InventoryUpdate{
			Session:   id,
			Inventory: player.inventory,
		})
		player.inventoryDirty = false
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
		// 自动回复只在 Active 期间推进，这是有意的：待重生玩家不在世界里，
		// 计时冻结；重生本身回满生命值，冻结与否都观察不到差别。计时放在
		// reset 短路之前同样是有意的：reset 只是位置跳变的当 tick 标记，
		// 玩家仍在世界里，回复不应因此停摆。
		player.advanceHealthRegen(engine.tunables.RegenDelayTicks, engine.tunables.RegenIntervalTicks)
		if player.reset {
			continue
		}
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
		wasOnGround := player.state.OnGround
		if wasOnGround {
			player.peakY = player.state.Position.Y()
		}
		step := physics.Step(
			player.state,
			player.input,
			dimensionCollisionSource{dimension: engine.dimensions[session.dimension]},
		)
		player.state = step.State
		if player.state.OnGround {
			if !wasOnGround {
				player.applyFallDamage()
			}
			player.peakY = player.state.Position.Y()
		} else if y := player.state.Position.Y(); y > player.peakY {
			player.peakY = y
		}
		engine.updateSafeLocation(session)
	}
}

// applyFallDamage 在"上一 tick 不在地面、这一 tick 在地面"的边沿按固定曲线结算
// 一次摔落伤害：伤害 = floor(峰值Y − 落地Y) − 3，负值取 0。本组只负责扣血，生命值
// 归零后的死亡/重生/掉落结算不在这里处理。
func (player *playerState) applyFallDamage() {
	fallHeight := float64(player.peakY - player.state.Position.Y())
	damage := int32(math.Floor(fallHeight)) - 3
	if damage <= 0 {
		return
	}
	player.resetRegenTimer()
	if damage >= int32(player.health) {
		player.health = 0
		return
	}
	player.health -= uint8(damage)
}

func (engine *Engine) updateSafeLocation(session *sessionState) {
	player := session.player
	if !player.state.OnGround {
		return
	}
	dimension := engine.dimensions[session.dimension]
	if dimension == nil || !restoreLocationChunksReady(
		dimension,
		player.state.Position,
	) {
		return
	}
	source := dimensionCollisionSource{dimension: dimension}
	free, ready := playerBoundsAreFree(player.state.Position, source)
	completeSupport, _ := playerSupport(player.state.Position, source)
	if !ready || !free || !completeSupport {
		return
	}
	if player.safe == nil {
		player.safe = &PlayerLocation{
			Dimension: session.dimension,
			Position:  player.state.Position,
		}
		return
	}
	player.safe.Dimension = session.dimension
	player.safe.Position = player.state.Position
}

func restoreLocationChunksReady(
	dimension *Dimension,
	position mgl32.Vec3,
) bool {
	bounds := physics.PlayerBounds(position)
	minX, maxX := blockSpan(bounds.Min.X(), bounds.Max.X())
	minZ, maxZ := blockSpan(bounds.Min.Z(), bounds.Max.Z())
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			chunk := (core.BlockPos{X: x, Z: z}).Chunk()
			info, exists := dimension.Info(chunk)
			if !exists || info.State != ChunkReady {
				return false
			}
		}
	}
	return true
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
	player.peakY = player.state.Position.Y()
	player.input = physics.Input{}
	player.miningHeld = false
	player.mining = playerMiningState{}
	player.reset = false
	player.inventoryDirty = true
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

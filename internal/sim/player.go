package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
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
	// Oxygen 是本 tick 结束时的权威氧气，0..core.MaxOxygenTicks；同 Health，
	// 只发布给玩家本人，不进入任何远端玩家消息。
	Oxygen uint16
	// Hunger 是本 tick 结束时的权威饥饿值，0..`core.MaxHunger`；同 `Health` 与
	// `Oxygen`，只发布给玩家本人。三层饥饿状态里只有它随协议上线：饱和度与
	// 疲劳值是纯服务端推进量，界面不呈现。
	Hunger uint8
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
	// Hunger、SaturationMilli、ExhaustionMilli 是存档中的三层权威饥饿状态，
	// 只有 HasHunger 为真时才生效。
	Hunger          uint8
	SaturationMilli uint16
	ExhaustionMilli uint16
	// HasHunger 报告上面三个字段是否来自一份真实存档。
	//
	// 它不能像 Health 那样用"零值代表缺失"：三层状态**全部**可以合法地为零
	// （饿到零、烧空饱和、疲劳刚跨过阈值），把 0 当缺失会让饿着下线的玩家一
	// 重登就回到 20/5000/0——重登因此成为免费进食途径，与 design.md D4
	// "beginReset 不回满"要挡的正是同一个漏洞。
	HasHunger bool
}

type PlayerSnapshot struct {
	Current    PlayerLocation
	Yaw, Pitch float32
	Safe       *PlayerLocation
	Inventory  core.Inventory
	Health     uint8
	// Hunger、SaturationMilli、ExhaustionMilli 是本次快照的三层权威饥饿状态，
	// 由持久化路径原样落盘（玩家 schema v7）。这里没有"缺失"语义：快照总是
	// 由权威 playerState 现取，三者恒为有效值。
	Hunger          uint8
	SaturationMilli uint16
	ExhaustionMilli uint16
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
	// actorState 内嵌玩家与伙伴共有的运动/朝向/背包/采掘状态，字段经提升访问；
	// 生命周期、生命与输入序号等玩家专属语义留在本结构体。提取范围与动机
	// 见 actor.go。
	actorState
	lifecycle         PlayerLifecycle
	anchor            core.ChunkPos
	lastInputSequence uint64
	reset             bool
	// spawned 记录这名玩家是否至少出生过一次。出生之前权威状态与登录时恢复的
	// 状态完全一致；出生之后两者就可能分岔，快照因而必须可被观察。见 persistable。
	spawned bool
	// health 是服务端单写者拥有的权威生命值，0..core.MaxHealth。
	health uint8
	// peakY 是离地后到达过的最高高度，瞬态字段，不持久化、不进入快照/哈希。
	// 身体浸没时它被逐 tick 重置到当前高度，因此水中不会累积出摔落伤害。
	// 落地、传送、重生、维度 reset 都会把它重置为当前高度。
	peakY float32
	// ticksSinceDamage 是自最后一次受伤以来连续未受伤的 tick 数，瞬态字段，
	// 不持久化、不进入快照/哈希；满血时不推进。见 health_regen.go。
	ticksSinceDamage uint32
	// oxygen 是服务端单写者拥有的权威氧气，0..core.MaxOxygenTicks。瞬态字段，
	// 不持久化、不进入快照/哈希：氧气不占存档字段（玩家 schema v7 只追加了饥饿三层），重连后由 `RegisterPlayer`
	// 初始化为满值。见 oxygen.go。
	oxygen uint16
	// drownTicks 是氧气归零后距离下一次溺水伤害已经过的 tick 数，瞬态字段，
	// 语义同 ticksSinceDamage。见 oxygen.go。
	drownTicks uint32
	// 以下四个字段是服务端单写者拥有的权威饥饿状态。三层状态**全为整数**：
	// 权威推进不用浮点，否则 Memory/TCP 两传输的重放一致与跨平台逐位相同这
	// 两条契约不可证。写者只有权威 tick 内的串行路径，没有 goroutine 也没有锁。
	// 见 hunger.go。
	//
	// hunger 是饥饿值，单位「点」，合法区间 0..core.MaxHunger（20）。
	hunger uint8
	// saturationMilli 是饱和度，单位千分位，上界是 hunger×
	// core.SaturationMilliPerPoint（因此绝对上界 20000，远在 uint16 之内）。
	// 它是饥饿值之上的缓冲：疲劳先烧它，烧空了才动饥饿值。
	saturationMilli uint16
	// exhaustionMilli 是疲劳值，单位千分位。每累积满
	// Tunables.ExhaustionThresholdMilli 就结算一次消耗并减去一个阈值，因此
	// 稳态下它恒小于阈值（uint16 的上界只在中间值上被短暂触及，见
	// applyExhaustion 在 uint32 上做累加的理由）。
	exhaustionMilli uint16
	// starvationTicks 是饥饿值归零后距离下一次饥饿伤害已经过的 tick 数，
	// 瞬态字段，语义同 drownTicks。见 hunger.go 的 advanceStarvation。
	starvationTicks uint32
	// eatingHeld 是玩家本 tick 的持续进食意图，来自 `Command.Eating`
	// （协议 v24 的 `PlayerInput.Eating`），语义与 `miningHeld` 完全对称：
	// 有效输入写入、中性输入与非法输入清零、重生清零。
	//
	// 它留在 playerState 而不是上移到 actorState：伙伴不进食，把它放进共有
	// 结构体会给伙伴凭空多出一个永远为假的字段。
	eatingHeld bool
	// eating 是权威进食进度状态机（见 eating.go）。它与 `mining` 一样是瞬态
	// 字段，不持久化、不进入快照/哈希，也不上线协议：进度只在服务端存在，
	// 客户端按自己的按键预测呈现。
	//
	// 它同样留在 playerState 而不是上移 actorState：伙伴不进食。
	eating eatingState

	restoreCandidates []restoreCandidate
	nextRestore       int
	restoreWanted     map[core.ChunkKey]struct{}
	safe              *PlayerLocation
	candidates        []spawnColumn
	candidateChunks   []core.ChunkPos
	nextCandidate     int
	// spawnFallback 是本轮候选扫描中最优的降级出生点，跨 tick 保留（见
	// internal/sim/spawn.go 的 spawnFallback）。
	spawnFallback      spawnFallback
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
		actorState: actorState{state: physics.State{Position: mgl32.Vec3{
			float32(restore.SpawnAnchor.X)*core.SectionSize + 0.5,
			core.MaxY + 1,
			float32(restore.SpawnAnchor.Z)*core.SectionSize + 0.5,
		}},
			yaw: restore.Yaw, pitch: restore.Pitch,
			inventory:      restore.Inventory,
			inventoryDirty: true},
		health: health,
		// 氧气不在 PlayerRestore 里，也不会出现在存档中：登录一律满值。
		// 这条初始化是「氧气不跨重启保留」唯一的来源，不能依赖第一个 tick 的
		// 「出水立即回满」代劳——在水里重生的玩家不会走那条分支。
		oxygen:          core.MaxOxygenTicks,
		restoreWanted:   make(map[core.ChunkKey]struct{}),
		candidates:      candidates,
		candidateChunks: spawnCandidateChunks(candidates),
		spawnWanted:     make(map[core.ChunkPos]struct{}),
	}
	// 先落到固定初值（初值只由 resetHunger 一处给出，注册、死亡结算与旧存档
	// 迁移共用同一组常量），再让带饥饿状态的存档覆盖它。没有存档可恢复的路径
	// ——新玩家、缺失玩家、只给维度与锚点的 RegisterSession——因此一律得到初值。
	player.resetHunger()
	if restore.HasHunger {
		player.hunger = restore.Hunger
		player.saturationMilli = restore.SaturationMilli
		player.exhaustionMilli = restore.ExhaustionMilli
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
		// 三层饥饿状态原样进快照：持久化路径（internal/server 的 save/restore）
		// 是它跨重启保留的唯一通道，任何一个字段漏进快照都会在重登时静默落回初值。
		Hunger:          player.hunger,
		SaturationMilli: player.saturationMilli,
		ExhaustionMilli: player.exhaustionMilli,
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
		Oxygen:            player.oxygen,
		Hunger:            player.hunger,
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
		if player.advanceHealthRegen(
			engine.tunables.RegenDelayTicks,
			engine.tunables.RegenIntervalTicks,
			engine.tunables.RegenHungerThreshold,
		) {
			// 疲劳表：自然回血每回 1 点生命值累积固定疲劳（见 hunger.go）。
			// 它是全表最大的一项，一次调用会跨过多个阈值。
			player.applyExhaustion(
				exhaustionRegenPerHealthMilli, engine.tunables.ExhaustionThresholdMilli,
			)
		}
		// 饥饿伤害与回血计时同处：它同样只在 Active 期间推进，也同样放在 reset
		// 短路之前——reset 只是位置跳变的当 tick 标记，玩家仍在世界里挨饿。
		player.advanceStarvation(engine.tunables.StarvationDamageIntervalTicks)
		// 进食推进排在饥饿伤害之后：饥饿伤害走 `applyDamage`，而 `applyDamage` 会
		// 中断进食。反过来排的话，"饿到零的玩家在挨这一拳的同一 tick 吃完面包"
		// 会先结算进食、再被同一 tick 的伤害打断一个已经不存在的进度——读起来
		// 像是伤害没能打断进食。它同样放在 `reset` 短路之前：`reset` 只是位置跳变
		// 的当 tick 标记，而"位置跳变中断进食"由 `advanceEating` 自己的 `reset`
		// 判据表达，不靠这里的短路代劳。
		player.advanceEating(engine.tunables.EatingTicks)
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
		source := dimensionCollisionSource{dimension: engine.dimensions[session.dimension]}
		// 浸没标志由权威侧在 tick 边界用共享纯函数从自己的方块镜像算出，
		// 再随 Input 传进物理步——流体没有碰撞盒，prism 里区分不出水与空气。
		input := player.input
		input.BodyInFluid, input.EyeInFluid = physics.SubmersionFlags(player.state.Position, source)
		// 氧气按「本 tick 开始时的眼睛浸没标志」结算，与传给物理步的是同一个值：
		// 水下视觉、水中积分与溺水三处共用这一份判定，不存在第二套。
		player.advanceOxygen(input.EyeInFluid, engine.tunables.DrownDamageIntervalTicks)
		wasOnGround := player.state.OnGround
		// 步首这次重置在「玩家自己游进水里」的路径上是冗余的：步末那次每 tick
		// 无条件执行，而本 tick 的步首位置恒等于上 tick 的步末位置。它唯一还能
		// 独立生效的窗口是**流体在两次玩家步之间流进玩家所在格**——advanceFluids
		// 与玩家推进同在一个权威 tick 里，这个窗口是真实存在的，不是假想，
		// 所以保留。
		if wasOnGround || input.BodyInFluid {
			player.peakY = player.state.Position.Y()
		}
		positionBeforeStep := player.state.Position
		step := physics.Step(player.state, input, source)
		player.state = step.State
		// —— 疲劳表的两个运动判定点（见 hunger.go 的固定表）——
		//
		// 起跳：物理积分只在「不在流体里、步首在地面、按下 Jump」这一组条件下
		// 施加起跳冲量（见 physics.Step 的垂直分支次序：BodyInFluid && Jump 的
		// 持续上浮分支排在 OnGround && Jump 之前，因此水里按跳是上浮不是起跳）。
		// 这里逐条复刻那组条件，另外再要求**步末已经离地**：它把「起跳」钉成
		// 「冲量真的把玩家抬离了地面」而不是「冲量被施加了」——否则某种让冲量
		// 当步就被吃掉的几何下，按住跳跃会逐 tick 反复施加冲量，疲劳变成可刷读数。
		// 注意：在当前整格碰撞几何下**低天花板触发不了这一分项**——头顶最小间隙
		// 0.2（`physics.PlayerHeight` 1.8 对 2 格洞）远大于 `physics.GroundProbe`，
		// 贴顶跳也会真实离地一瞬并照常计费；该分项只在单步位移落进探针容差内时
		// 生效（例如 `JumpSpeed` tunable 被调到极小），覆盖它的用例正是这样构造的。
		// physics.Step 的输出里没有现成的「本步起跳了」标志位，所以判据只能在
		// 这里由 sim 可见的量复刻；将来若 physics 输出该标志，这里应改为直接复用。
		if input.Jump && wasOnGround && !input.BodyInFluid && !player.state.OnGround {
			player.applyExhaustion(exhaustionJumpMilli, engine.tunables.ExhaustionThresholdMilli)
		}
		// 游泳：身体浸没时按本步的水平位移计费。位移为零（原地泡着）自然得到
		// 零疲劳，不需要额外分支。
		if input.BodyInFluid {
			player.applyExhaustion(
				swimExhaustionMilli(positionBeforeStep, player.state.Position),
				engine.tunables.ExhaustionThresholdMilli,
			)
		}
		// 落点也要判一次：水浅、下落又快时，本步开始时玩家还在水面之上、结束
		// 时已经踩到水底，只看步首标志会让这一跤照旧结算摔落伤害。
		if landedInFluid, _ := physics.SubmersionFlags(player.state.Position, source); landedInFluid {
			player.peakY = player.state.Position.Y()
		}
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

// applyFallDamage 在"上一 tick 不在地面、这一 tick 在地面"的边沿按固定曲线算出
// 一次摔落伤害：伤害 = floor(峰值Y − 落地Y) − 3，负值取 0。它只负责曲线，扣血本身
// 交给 applyDamage。
func (player *playerState) applyFallDamage() {
	fallHeight := float64(player.peakY - player.state.Position.Y())
	player.applyDamage(int32(math.Floor(fallHeight)) - 3)
}

// applyDamage 是全部伤害来源共用的唯一结算入口：重置自动回复计时、扣血并把
// 生命值钳到 0。非正伤害是 no-op（摔落曲线在安全高度会算出负值）。
//
// 每个新伤害来源都必须经这里，不要复制它的三行内容：只有走这条入口才能同时拿到
// 「重置回血计时」（否则玩家一边挨打一边回血）、客户端的确认伤害红色边缘反馈，
// 以及生命值归零后由本 tick 稍后的 settleDeaths 做的死亡/重生/掉落结算——绕开它
// 的伤害会静默丢掉这三件事，而且没有任何报错。
func (player *playerState) applyDamage(damage int32) {
	if damage <= 0 {
		return
	}
	player.resetRegenTimer()
	// 受伤中断进食（spec「受伤或死亡 MUST 清零进度且 MUST NOT 扣除食物」），
	// 与上一行"重置回血计时"同处同理：两者都是"真正挨了一下"才发生的副作用，
	// 因此都必须排在非正伤害的短路**之后**——摔落曲线在安全高度每次落地都会
	// 算出负值，写在函数第一行会让"跳一下"打断进食。清空只丢进度，不碰背包。
	player.eating = eatingState{}
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
	// 重生/传送把玩家挪到锚点列的空中，氧气随之回满：否则刚被淹死的玩家会带着
	// 空氧气重生，下一次入水立刻继续掉血。
	player.oxygen = core.MaxOxygenTicks
	player.drownTicks = 0
	player.input = physics.Input{}
	player.miningHeld = false
	player.eatingHeld = false
	player.mining = miningState{}
	// 死亡与位置跳变都经这里，进食进度随之作废：重生后站在出生点继续吃完
	// 死前那半块面包没有任何语义，与 `mining` 上一行同理。
	player.eating = eatingState{}
	player.reset = false
	player.inventoryDirty = true
	player.nextCandidate = 0
	player.spawnFallback = spawnFallback{}
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

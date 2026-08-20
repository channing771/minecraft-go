package sim

import (
	"log/slog"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// defaultSpawnRadius 是出生扫描的编译期默认半径。唯一读取入口是 Tunables 快照，
// 不得再以导出常量暴露——见 internal/archcheck 的 TestTunableConstantsAreNotExported。
const defaultSpawnRadius = int32(16)

// minSpawnRadius 与 maxSpawnRadius 是出生扫描半径的硬区间，与
// internal/config 的 Fields() 里 sim.spawnRadius 的 Min/Max 一致。
// SetTunables 按这两个界兜底，见那里的说明。
const (
	minSpawnRadius = int32(1)
	maxSpawnRadius = int32(64)
)

type spawnColumn struct {
	X, Z int32
}

// spawnCandidates 按到 anchor 的距离升序枚举半径 radius 内的候选出生列。
// radius 由调用方传入本 tick 的快照值，这个自由函数本身绝不读取 ActiveTunables。
//
// 容量安全依赖 radius 已被钳制在 [minSpawnRadius, maxSpawnRadius]：下面的容量
// 计算随 radius 平方增长，未钳制的大数会在此处触发一次巨额分配。该钳制由本包的
// SetTunables 兜底（internal/config 加载配置时也会按同一区间钳一遍，但 sim 按
// 架构约束不得导入 config，不能把不变量托付给隔壁包）。
func spawnCandidates(anchor core.ChunkPos, radius int32) []spawnColumn {
	anchorX := anchor.X << core.SectionShift
	anchorZ := anchor.Z << core.SectionShift
	candidates := make([]spawnColumn, 0, (radius*2+1)*(radius*2+1))
	for x := anchorX - radius; x <= anchorX+radius; x++ {
		for z := anchorZ - radius; z <= anchorZ+radius; z++ {
			candidates = append(candidates, spawnColumn{X: x, Z: z})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		li := int64(candidates[i].X-anchorX)*int64(candidates[i].X-anchorX) +
			int64(candidates[i].Z-anchorZ)*int64(candidates[i].Z-anchorZ)
		lj := int64(candidates[j].X-anchorX)*int64(candidates[j].X-anchorX) +
			int64(candidates[j].Z-anchorZ)*int64(candidates[j].Z-anchorZ)
		if li != lj {
			return li < lj
		}
		if candidates[i].X != candidates[j].X {
			return candidates[i].X < candidates[j].X
		}
		return candidates[i].Z < candidates[j].Z
	})
	return candidates
}

func spawnCandidateChunks(candidates []spawnColumn) []core.ChunkPos {
	unique := make(map[core.ChunkPos]struct{})
	for _, candidate := range candidates {
		chunk := (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk()
		unique[chunk] = struct{}{}
	}
	chunks := make([]core.ChunkPos, 0, len(unique))
	for chunk := range unique {
		chunks = append(chunks, chunk)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].X != chunks[j].X {
			return chunks[i].X < chunks[j].X
		}
		return chunks[i].Z < chunks[j].Z
	})
	return chunks
}

type dimensionCollisionSource struct {
	dimension *Dimension
}

func (source dimensionCollisionSource) CollisionBoxes(
	position core.BlockPos,
) physics.CollisionBoxSet {
	block, ready := source.dimension.BlockAt(position)
	return physics.BlockCollisionBoxes(block, ready)
}

// IsFluidAt 让权威维度充当 physics.FluidSource：浸没判定的规则全部在
// physics.SubmersionFlags 里，这里只交付「这一格是不是流体」这一份方块视图，
// 与客户端 Mirror 的同名方法逐条对应。未就绪的区块返回 false——权威侧宁可
// 漏判也不能凭空造水。
func (source dimensionCollisionSource) IsFluidAt(position core.BlockPos) bool {
	block, ready := source.dimension.BlockAt(position)
	return ready && core.IsFluid(block)
}

func (engine *Engine) advancePendingPlayers() {
	sessions := make([]SessionID, 0, len(engine.sessions))
	for id, session := range engine.sessions {
		if session.player != nil && session.player.lifecycle == PlayerPendingSpawn {
			sessions = append(sessions, id)
		}
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i] < sessions[j] })
	for _, id := range sessions {
		engine.advancePendingPlayer(id, engine.sessions[id])
	}
}

func (engine *Engine) advancePendingPlayer(id SessionID, session *sessionState) {
	player := session.player
	for player.nextRestore < len(player.restoreCandidates) {
		candidate := player.restoreCandidates[player.nextRestore]
		engine.retainRestoreChunks(session, candidate)
		valid, ready, onGround := engine.validateRestoreCandidate(candidate)
		if !ready {
			return
		}
		if valid {
			player.activate(session, candidate.location, onGround)
			engine.subscriptionsDirty = true
			return
		}
		player.nextRestore++
	}

	dimension := engine.dimensions[session.dimension]
	if player.exhausted {
		if !spawnRevisionsChanged(dimension, player) {
			if (engine.tick.Load()+1)%100 == 0 {
				slog.Warn("玩家仍在等待可用出生点", "session", id)
			}
			return
		}
		player.exhausted = false
		player.exhaustedRevisions = nil
		player.nextCandidate = 0
		// 世界已经变了，上一轮记下的降级候选不再可信，整轮重来。
		player.spawnFallback = spawnFallback{}
	}

	source := dimensionCollisionSource{dimension: dimension}
	for player.nextCandidate < len(player.candidates) {
		candidate := player.candidates[player.nextCandidate]
		engine.retainSpawnChunk(session, (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk())
		position, tier, ready := findSpawnInColumn(candidate, dimension, source)
		if !ready {
			engine.retryFailedSpawnChunk(session, (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk())
			return
		}
		if tier == spawnTierDry {
			player.spawnFallback = spawnFallback{}
			player.activate(session, PlayerLocation{
				Dimension: session.dimension,
				Position:  position,
			}, true)
			engine.subscriptionsDirty = true
			return
		}
		player.spawnFallback.consider(position, tier)
		player.nextCandidate++
	}

	// 候选列全部走完仍没有干地：用记录里最优的降级候选出生，绝不以"永久
	// PendingSpawn"为终态（见 spawnTier 的说明）。
	if position, ok := player.spawnFallback.take(); ok {
		player.activate(session, PlayerLocation{
			Dimension: session.dimension,
			Position:  position,
		}, true)
		engine.subscriptionsDirty = true
		return
	}

	player.exhausted = true
	player.exhaustedRevisions = make([]uint64, len(player.candidateChunks))
	for index, chunk := range player.candidateChunks {
		info, ok := dimension.Info(chunk)
		if !ok || info.State != ChunkReady {
			panic("sim: exhausted spawn candidate chunk is not ready")
		}
		player.exhaustedRevisions[index] = info.Revision
	}
}

func (player *playerState) activate(
	session *sessionState,
	location PlayerLocation,
	onGround bool,
) {
	player.lifecycle = PlayerActive
	player.spawned = true
	player.state = physics.State{
		Position: location.Position,
		OnGround: onGround,
	}
	// 传送（重连恢复位置）与重生都经过这里；峰值 Y 必须随之重置为当前高度，
	// 否则携带的旧峰值会在接下来的落地边沿被误结算成巨额伤害。
	player.peakY = location.Position.Y()
	player.input = physics.Input{Yaw: player.yaw}
	player.lastInputSequence = 0
	player.reset = true
	session.dimension = location.Dimension
	session.center = (core.BlockPos{
		X: int32(math.Floor(float64(location.Position.X()))),
		Z: int32(math.Floor(float64(location.Position.Z()))),
	}).Chunk()
	player.restoreCandidates = nil
	player.nextRestore = 0
	player.restoreWanted = nil
}

func (engine *Engine) retainRestoreChunks(
	session *sessionState,
	candidate restoreCandidate,
) {
	keys := restoreCandidateChunks(candidate.location)
	for _, key := range keys {
		if _, retained := session.player.restoreWanted[key]; retained {
			continue
		}
		session.player.restoreWanted[key] = struct{}{}
		engine.subscriptionsDirty = true
	}
}

func (engine *Engine) validateRestoreCandidate(
	candidate restoreCandidate,
) (valid bool, ready bool, onGround bool) {
	if !physics.ValidState(physics.State{Position: candidate.location.Position}) {
		return false, true, false
	}
	bounds := physics.PlayerBounds(candidate.location.Position)
	if bounds.Min.Y() < float32(core.MinY) || bounds.Max.Y() > float32(core.MaxY) {
		return false, true, false
	}
	dimension := engine.dimensions[candidate.location.Dimension]
	if dimension == nil {
		return false, true, false
	}
	for _, key := range restoreCandidateChunks(candidate.location) {
		info, exists := dimension.Info(key.Pos)
		if !exists || info.State != ChunkReady {
			return false, false, false
		}
	}
	source := dimensionCollisionSource{dimension: dimension}
	free, ready := playerBoundsAreFree(candidate.location.Position, source)
	if !ready {
		return false, false, false
	}
	completeSupport, anyGroundContact := playerSupport(candidate.location.Position, source)
	if candidate.requireSupport && !completeSupport {
		return false, true, anyGroundContact
	}
	return free, true, anyGroundContact
}

func restoreCandidateChunks(location PlayerLocation) []core.ChunkKey {
	bounds := physics.PlayerBounds(location.Position)
	minX, maxX := blockSpan(bounds.Min.X(), bounds.Max.X())
	minZ, maxZ := blockSpan(bounds.Min.Z(), bounds.Max.Z())
	unique := make(map[core.ChunkPos]struct{}, 4)
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			unique[(core.BlockPos{X: x, Z: z}).Chunk()] = struct{}{}
		}
	}
	chunks := make([]core.ChunkPos, 0, len(unique))
	for chunk := range unique {
		chunks = append(chunks, chunk)
	}
	sort.Slice(chunks, func(i, j int) bool {
		if chunks[i].X != chunks[j].X {
			return chunks[i].X < chunks[j].X
		}
		return chunks[i].Z < chunks[j].Z
	})
	keys := make([]core.ChunkKey, 0, len(chunks))
	for _, chunk := range chunks {
		keys = append(keys, core.ChunkKey{
			Dimension: location.Dimension,
			Pos:       chunk,
		})
	}
	return keys
}

func playerSupport(
	position mgl32.Vec3,
	source physics.CollisionSource,
) (completeSupport bool, anyGroundContact bool) {
	bounds := physics.PlayerBounds(position)
	minX, maxX := blockSpan(bounds.Min.X(), bounds.Max.X())
	minZ, maxZ := blockSpan(bounds.Min.Z(), bounds.Max.Z())
	y := int32(math.Floor(float64(position.Y() - physics.GroundProbe)))
	completeSupport = true
	for x := minX; x <= maxX; x++ {
		for z := minZ; z <= maxZ; z++ {
			boxes := source.CollisionBoxes(core.BlockPos{X: x, Y: y, Z: z})
			cellSupported := false
			for index := 0; index < min(int(boxes.Count), len(boxes.Boxes)); index++ {
				box := boxes.Boxes[index]
				worldMin := box.Min.Add(mgl32.Vec3{float32(x), float32(y), float32(z)})
				worldMax := box.Max.Add(mgl32.Vec3{float32(x), float32(y), float32(z)})
				if worldMax.Y() < position.Y()-physics.GroundProbe-physics.CollisionEpsilon ||
					worldMax.Y() > position.Y()+physics.CollisionEpsilon {
					continue
				}
				if bounds.Min.X() < worldMax.X() && bounds.Max.X() > worldMin.X() &&
					bounds.Min.Z() < worldMax.Z() && bounds.Max.Z() > worldMin.Z() {
					anyGroundContact = true
				}
				if worldMin.X() <= max(bounds.Min.X(), float32(x)) &&
					worldMax.X() >= min(bounds.Max.X(), float32(x+1)) &&
					worldMin.Z() <= max(bounds.Min.Z(), float32(z)) &&
					worldMax.Z() >= min(bounds.Max.Z(), float32(z+1)) {
					cellSupported = true
					break
				}
			}
			completeSupport = completeSupport && cellSupported
		}
	}
	return completeSupport, anyGroundContact
}

func (engine *Engine) retainSpawnChunk(
	session *sessionState,
	chunk core.ChunkPos,
) {
	if _, retained := session.player.spawnWanted[chunk]; retained {
		return
	}
	session.player.spawnWanted[chunk] = struct{}{}
	engine.subscriptionsDirty = true
}

func (engine *Engine) retryFailedSpawnChunk(
	session *sessionState,
	chunk core.ChunkPos,
) {
	info, ok := engine.dimensions[session.dimension].Info(chunk)
	if !ok || info.State == ChunkFailed {
		engine.subscriptionsDirty = true
	}
}

// spawnTier 是出生点降级阶梯的档位，**数值越小越优先**。
//
// 为什么需要阶梯而不是"不浸没就是唯一标准"：出生扫描半径只有 16 格
// （spawnRadius，共 33×33 列），而一片海的直径远超它。真实 worldgen 下
// （seed 42、锚点 {8,0}、开启流体）1089 个候选列可以全部浸没，此时"只接受
// 不浸没"的实现会走到 exhausted 并永久停在 PendingSpawn——玩家永远登录不上。
//
// "永远无法登录"比"出生在水里"严重得多，而后者**可自救**：玩家有浮力与持续
// 上浮（组 5）、氧气 300 tick（组 6），浮出水面绰绰有余。所以宁可降档也不
// 拒绝出生。
type spawnTier uint8

const (
	// spawnTierNone 表示该列没有任何可站立的落脚点，不是一个可用档位。
	spawnTierNone spawnTier = iota
	// spawnTierDry：身体完全不浸没，走陆地积分，理想档。
	spawnTierDry
	// spawnTierEyeDry：身体入水但眼睛在水面之上（齐腰水）。会走水中积分，
	// 但眼睛没入水就不消耗氧气，不会溺水。
	spawnTierEyeDry
	// spawnTierSubmerged：连眼睛也在水下。玩家登录即开始掉氧，但有浮力可以
	// 自己游上来；这是"能登录"与"永远登录不了"之间的兜底。
	spawnTierSubmerged
)

// spawnFallback 记录一轮候选列扫描中迄今最优的**降级**候选（第 2/3 档）。
//
// 为什么要把它做成跨 tick 的状态而不是"第一档扫完再扫第二档"：后者是三倍的
// 全量扫描开销。这里只做**一次**遍历——每列照旧自上而下扫一遍、扫到第 1 档
// 立即出生（与加阶梯之前逐字相同的快路径），否则把该列的最优档位记下来继续
// 换列；候选列全部走完仍没有第 1 档时，才拿出记录里最优的那个出生。
// 单次遍历的额外开销只有每个"可站立且不被阻挡"的落脚点上多一次
// physics.SubmersionFlags（每列通常 1 次）加 O(1) 状态，不新增任何一遍扫描。
//
// 之所以必须跨 tick 保留：候选列碰到未就绪区块时整轮扫描会中途返回等待，
// 而 nextCandidate/spawnIndex 不回退，下一 tick 从断点继续——把记录放在栈上
// 会让断点之前那些列的降级候选静默丢失，海洋世界又退回永久 PendingSpawn。
type spawnFallback struct {
	position mgl32.Vec3
	tier     spawnTier
}

// consider 用一个新候选更新记录，只在严格更优时替换（同档位保留先到的那个，
// 因为候选列本身已按"离锚点由近及远"排好序）。
func (f *spawnFallback) consider(position mgl32.Vec3, tier spawnTier) {
	if tier == spawnTierNone {
		return
	}
	if f.tier == spawnTierNone || tier < f.tier {
		f.position, f.tier = position, tier
	}
}

// take 取出记录并清空；没有任何降级候选时返回 false。
//
// **取出的位置是 consider 当时校验的，不是本 tick 重新校验的。** 两者之间可能
// 隔着若干 tick（候选列碰到未就绪区块会中途返回等待），期间他人放置或
// advanceFluids 灌水都可能让那一格变样。这里**刻意不重新复算**，两种陈旧后果
// 都已有归宿：
//
//   - **那一格被填实**：玩家出生后 advancePlayers 的 tryUnstick 先尝试逐 1/16
//     格抬升，抬不出来就 beginReset 回到 PendingSpawn，用当时的世界重新扫一遍
//     完整的出生流程。代价是多一个重生周期，不是状态损坏。伙伴侧沿用既有裁决
//     （卡入方块的解除属玩家生命周期语义，M5B 伙伴保持最小实现）。
//   - **那一格仍空但灌了水**：档位变差而已，等价于本来就只有更低那一档；玩家
//     有浮力可以自己游上来，正是 spawnTierSubmerged 已经接受的结局。
//
// 既有自愈路径已被证明有界，在这里再加一次复算等于为已解决的问题再付一次代价
// （且复算失败后仍要回退到同一条自愈路径）。写下这段是因为读者会自然以为取出
// 的位置是当 tick 校验过的。
func (f *spawnFallback) take() (mgl32.Vec3, bool) {
	if f.tier == spawnTierNone {
		return mgl32.Vec3{}, false
	}
	position := f.position
	*f = spawnFallback{}
	return position, true
}

// findSpawnInColumn 自上而下扫一列，返回该列最优的落脚点及其档位，以及该列
// 涉及的区块是否全部就绪。
//
// **玩家出生与伙伴出生共用本函数**（advancePendingPlayer 与
// advancePendingCompanion），流体判定因此对两者同时生效。这与伙伴寻路把流体
// 当阻挡（见 internal/server 的 productionCompanionPassableBlocks 豁免）方向
// 一致：伙伴同样不该被放进水里。
//
// 阶梯对伙伴的含义与对玩家**不同**，改这里时必须一并想到：伙伴没有浮力、
// 没有氧气也没有溺水结算（其 physics.Input 的 BodyInFluid 恒为零值），第 3 档
// 对伙伴意味着"站在水底且寻路走不出来"，而不是"游上来"。即便如此仍然共用同一
// 条阶梯：伙伴的替代结局是**永远不出生**，那比站在水底更糟，且伙伴可以由玩家
// 用指令重新调度。给伙伴接水中物理属 M5 系列范围。
//
// 扫到第 1 档立即返回（快路径与加阶梯之前逐字相同）；否则记住本列最优档位
// 继续向下找——同一列更低处通常更深，但"水下石台上方还有个气穴"这种形状确实
// 存在，继续扫不额外读任何方块。
func findSpawnInColumn(
	candidate spawnColumn,
	dimension *Dimension,
	source dimensionCollisionSource,
) (mgl32.Vec3, spawnTier, bool) {
	var best spawnFallback
	for y := int32(core.MaxY - 1); y >= core.MinY; y-- {
		blockPosition := core.BlockPos{X: candidate.X, Y: y, Z: candidate.Z}
		block, ready := dimension.BlockAt(blockPosition)
		if !ready {
			return mgl32.Vec3{}, spawnTierNone, false
		}
		boxes := physics.BlockCollisionBoxes(block, true)
		if block == core.AirID || boxes.Count == 0 {
			continue
		}
		position := mgl32.Vec3{float32(candidate.X) + 0.5, float32(y) + 1, float32(candidate.Z) + 0.5}
		free, neighborsReady := playerBoundsAreFree(position, source)
		if !neighborsReady {
			return mgl32.Vec3{}, spawnTierNone, false
		}
		if !free {
			continue
		}
		tier := spawnTierOf(position, source)
		if tier == spawnTierDry {
			return position, spawnTierDry, true
		}
		best.consider(position, tier)
	}
	return best.position, best.tier, true
}

// spawnTierOf 判定一个落脚点的档位。
//
// 浸没判定复用 physics.SubmersionFlags 这唯一一份浸没规则（与权威 tick、
// 客户端预测同一个函数），不在这里另写一套逐格流体扫描——两套实现"一起写错"
// 不会被任何 parity 断言抓到。流体零碰撞体，因此 playerBoundsAreFree 会把
// 海平面以下的地表也读成"可站立"，档位判定是唯一能把它们区分开的东西。
func spawnTierOf(position mgl32.Vec3, source dimensionCollisionSource) spawnTier {
	bodyInFluid, eyeInFluid := physics.SubmersionFlags(position, source)
	switch {
	case eyeInFluid:
		return spawnTierSubmerged
	case bodyInFluid:
		return spawnTierEyeDry
	}
	return spawnTierDry
}

func playerBoundsAreFree(
	position mgl32.Vec3,
	source dimensionCollisionSource,
) (bool, bool) {
	bounds := physics.PlayerBounds(position)
	minX, maxX := blockSpan(bounds.Min.X(), bounds.Max.X())
	minY, maxY := blockSpan(bounds.Min.Y(), bounds.Max.Y())
	minZ, maxZ := blockSpan(bounds.Min.Z(), bounds.Max.Z())
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			for z := minZ; z <= maxZ; z++ {
				blockPosition := core.BlockPos{X: x, Y: y, Z: z}
				boxes := source.CollisionBoxes(blockPosition)
				if !boxes.Loaded {
					return false, false
				}
				count := min(int(boxes.Count), len(boxes.Boxes))
				for index := 0; index < count; index++ {
					offset := mgl32.Vec3{float32(x), float32(y), float32(z)}
					box := core.AABB{
						Min: boxes.Boxes[index].Min.Add(offset),
						Max: boxes.Boxes[index].Max.Add(offset),
					}
					if bounds.Overlaps(box) {
						return false, true
					}
				}
			}
		}
	}
	return true, true
}

func blockSpan(minimum, maximum float32) (int32, int32) {
	return int32(math.Floor(float64(minimum))), int32(math.Ceil(float64(maximum))) - 1
}

func spawnRevisionsChanged(dimension *Dimension, player *playerState) bool {
	return spawnChunkRevisionsChanged(dimension, player.candidateChunks, player.exhaustedRevisions)
}

func spawnChunkRevisionsChanged(
	dimension *Dimension,
	chunks []core.ChunkPos,
	revisions []uint64,
) bool {
	for index, chunk := range chunks {
		info, ok := dimension.Info(chunk)
		if !ok || info.State != ChunkReady || info.Revision != revisions[index] {
			return true
		}
	}
	return false
}

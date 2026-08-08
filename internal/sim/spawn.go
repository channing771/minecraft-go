package sim

import (
	"log/slog"
	"math"
	"sort"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/physics"
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
	}

	source := dimensionCollisionSource{dimension: dimension}
	for player.nextCandidate < len(player.candidates) {
		candidate := player.candidates[player.nextCandidate]
		engine.retainSpawnChunk(session, (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk())
		position, valid, ready := findSpawnInColumn(candidate, dimension, source)
		if !ready {
			engine.retryFailedSpawnChunk(session, (core.BlockPos{X: candidate.X, Z: candidate.Z}).Chunk())
			return
		}
		if valid {
			player.activate(session, PlayerLocation{
				Dimension: session.dimension,
				Position:  position,
			}, true)
			engine.subscriptionsDirty = true
			return
		}
		player.nextCandidate++
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

func findSpawnInColumn(
	candidate spawnColumn,
	dimension *Dimension,
	source dimensionCollisionSource,
) (mgl32.Vec3, bool, bool) {
	for y := int32(core.MaxY - 1); y >= core.MinY; y-- {
		blockPosition := core.BlockPos{X: candidate.X, Y: y, Z: candidate.Z}
		block, ready := dimension.BlockAt(blockPosition)
		if !ready {
			return mgl32.Vec3{}, false, false
		}
		boxes := physics.BlockCollisionBoxes(block, true)
		if block == core.AirID || boxes.Count == 0 {
			continue
		}
		position := mgl32.Vec3{float32(candidate.X) + 0.5, float32(y) + 1, float32(candidate.Z) + 0.5}
		free, neighborsReady := playerBoundsAreFree(position, source)
		if !neighborsReady {
			return mgl32.Vec3{}, false, false
		}
		if free {
			return position, true, true
		}
	}
	return mgl32.Vec3{}, false, true
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
	for index, chunk := range player.candidateChunks {
		info, ok := dimension.Info(chunk)
		if !ok || info.State != ChunkReady || info.Revision != player.exhaustedRevisions[index] {
			return true
		}
	}
	return false
}

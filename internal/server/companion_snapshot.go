// 本文件实现 Companion Manager 的世界读取辅助：以某位置为中心的 3×3 ready
// 区块不可变视图、规划观察快照（PlanSnapshot）构造、寻路网格（PathGrid）构造
// 与生产阻挡表。全部构造只发生在权威 tick 边界（持有 stepMu）：区块经
// CloneReadyChunk 拷贝后 worker 只读不可变值，绝不回调活世界。
package server

import (
	"math"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/world"
)

// companionViewRadiusChunks 是区块视图的半径：3×3。规划快照的水平 ±16 格与
// 寻路窗口 ±16 格在区块对齐后最多横跨 3×3 区块（33 格宽 ≤ 2×16+1），因此
// 3×3 恰好同时覆盖两者，也正好是 sim 的伙伴兴趣范围。
const companionViewRadiusChunks = 1

// companionChunkView 是以某个方块位置为中心的 3×3 ready 区块不可变拷贝。
// 未 ready（或未加载）的槽位保持 nil，读取返回 ok=false。构造即深拷贝，
// 之后与权威世界的一切变化隔离。
type companionChunkView struct {
	dimension core.DimensionID
	origin    core.ChunkPos
	chunks    [9]*world.Chunk
	revisions [9]uint64
}

// chunkViewAt 构造以 position 为中心的 3×3 区块视图。只拷贝当前 Ready 的区块；
// 调用方决定未就绪区块的处理策略（规划快照跳过缺失列，寻路顺延）。
func (m *companionManager) chunkViewAt(dimension core.DimensionID, position [3]float32) companionChunkView {
	center := (core.BlockPos{
		X: int32(math.Floor(float64(position[0]))),
		Z: int32(math.Floor(float64(position[2]))),
	}).Chunk()
	view := companionChunkView{
		dimension: dimension,
		origin:    core.ChunkPos{X: center.X - companionViewRadiusChunks, Z: center.Z - companionViewRadiusChunks},
	}
	for dz := int32(-companionViewRadiusChunks); dz <= companionViewRadiusChunks; dz++ {
		for dx := int32(-companionViewRadiusChunks); dx <= companionViewRadiusChunks; dx++ {
			key := core.ChunkKey{
				Dimension: dimension,
				Pos:       core.ChunkPos{X: center.X + dx, Z: center.Z + dz},
			}
			chunk, revision, ready := m.engine.CloneReadyChunk(key)
			if !ready {
				continue
			}
			view.chunks[(dz+1)*3+(dx+1)] = chunk
			view.revisions[(dz+1)*3+(dx+1)] = revision
		}
	}
	return view
}

// chunkFor 返回世界方块坐标所属的区块拷贝；不属于视图时返回 nil。
func (v companionChunkView) chunkFor(x, z int32) *world.Chunk {
	chunk := (core.BlockPos{X: x, Z: z}).Chunk()
	dx := chunk.X - v.origin.X
	dz := chunk.Z - v.origin.Z
	if dx < 0 || dx > 2 || dz < 0 || dz > 2 {
		return nil
	}
	return v.chunks[dz*3+dx]
}

// blockAt 读取世界坐标方块。区块在视图内且已 ready 时返回 (方块, true)；
// 区块缺失返回 (0, false)。世界 Y 越界由 world.Chunk.BlockAt 归一为空气。
func (v companionChunkView) blockAt(x, y, z int32) (core.BlockID, bool) {
	chunk := v.chunkFor(x, z)
	if chunk == nil {
		return 0, false
	}
	return chunk.BlockAt(int(x&core.SectionMask), y, int(z&core.SectionMask)), true
}

// readyRevisions 返回视图中已 ready 区块的 (X,Z) 字典序升序 revision 列表，
// 供寻路结果失效判定与快照使用。X 是主键：外层必须遍历 X（dx），否则
// PlanSnapshot.Validate 的严格升序校验会在跨 Z 行时拒绝。
func (v companionChunkView) readyRevisions() []companion.ChunkRevision {
	ready := make([]companion.ChunkRevision, 0, 9)
	for dx := int32(0); dx < 3; dx++ {
		for dz := int32(0); dz < 3; dz++ {
			if v.chunks[dz*3+dx] == nil {
				continue
			}
			ready = append(ready, companion.ChunkRevision{
				Chunk:    core.ChunkPos{X: v.origin.X + dx, Z: v.origin.Z + dz},
				Revision: v.revisions[dz*3+dx],
			})
		}
	}
	return ready
}

// allCoveredReady 报告从 origin 起 sizeX×sizeZ 的区块矩形是否全部 ready，
// 供寻路在快照完整时才发起。
func (v companionChunkView) allCoveredReady(origin core.ChunkPos, sizeX, sizeZ int32) bool {
	for dz := int32(0); dz < sizeZ; dz++ {
		for dx := int32(0); dx < sizeX; dx++ {
			if !v.chunkReady(origin.X+dx, origin.Z+dz) {
				return false
			}
		}
	}
	return true
}

// chunkReady 报告指定区块是否在视图中且已拷贝就绪。
func (v companionChunkView) chunkReady(chunkX, chunkZ int32) bool {
	return v.chunkFor(chunkX<<core.SectionShift, chunkZ<<core.SectionShift) != nil
}

// revisionAt 返回视图中区块的内容 revision；区块缺失时返回 0（调用方必须
// 先经 allCoveredReady 保证存在）。
func (v companionChunkView) revisionAt(chunkX, chunkZ int32) uint64 {
	return v.revisions[(chunkZ-v.origin.Z)*3+(chunkX-v.origin.X)]
}

// productionCompanionPassableBlocks 返回寻路阻挡表的生产映射：只有空气可
// 通过，其余一切注册方块阻挡。除流体外，该判定与 collision oracle（physics.
// BlockCollisionBoxes）逐一对齐——每个已加载的非空气固体方块都是整格碰撞体，
// 玻璃与树叶也不例外；未注册编号由 NewPathBlockTable 缺省视为阻挡。
//
// 流体是刻意的例外：它在 oracle 下是零碰撞体（实体可自由穿行），却仍在本表里
// 阻挡。原因是伙伴尚无浮力、屏息或溺水处理，把水面纳入路径会让它走进水里沉底
// 卡死；宁可绕开水域。
//
// 退出条件（**两条都成立才可以放开流体**，缺一条就是制造故障）：
//
//  1. 伙伴走与玩家同一套水中积分——即 sim.advanceActiveCompanions 喂给
//     physics.Step 的 physics.Input 里 BodyInFluid 由 physics.SubmersionFlags
//     真实算出，而不是像现在这样恒为零值；
//  2. 伙伴有自己的氧气与溺水结算——即 sim 的 advanceOxygen 对伙伴也成立。
//
// 两条**都还不成立**：spec fluid-survival 的主语全是"玩家"。原注释写的是
// "后续变更交付浸没物理后移除"，而浸没物理本身（physics.SubmersionFlags、
// 水中积分、玩家溺水）已由变更 fluid-presentation-survival 交付，那句话字面
// 已经成立——照它放开流体，A* 会规划穿水路径而伙伴仍按空气 + 重力积分，正好
// 落进本例外当初要防的故障。给伙伴接水中物理属 M5 系列范围。
//
// 本表与 TestCompanionManagerPathBlockTableMatchesCollisionOracle 里的豁免
// 分支是同一条决定的两半，必须同进同出；那里写着同样的两条退出条件。
func productionCompanionPassableBlocks() map[core.BlockID]bool {
	return map[core.BlockID]bool{core.AirID: true}
}

// scanEnvObservation 扫描伙伴周围水平 ±16、垂直 ±4 窗口内的环境观察：每列
// 地表高度样本（高度表 O(1) 读取）与暴露方块（非空气且六邻域中存在空气邻居
// 的可见表面；邻居落在未加载区块时不视为暴露——宁可少报也不猜测未见过的
// 地形）。规划快照（buildPlanSnapshot）与 Dialogue 环境摘要
// （buildDialogueEnvDigest）共用这一有界扫描：窗口 33×33×9 格的常数扫描，
// 输出 ≤256 暴露方块（经 BoundExposedBlocks 归一）与 1089 高度样本，不随
// 世界规模增长。返回的视图供规划快照继续读取 revision，台词侧忽略它。
func (m *companionManager) scanEnvObservation(
	body companion.Body,
) (view companionChunkView, exposed []companion.PlanBlock, heights []companion.PlanHeight) {
	view = m.chunkViewAt(body.Dimension, body.Position)
	centerX := int32(math.Floor(float64(body.Position[0])))
	centerY := int32(math.Floor(float64(body.Position[1])))
	centerZ := int32(math.Floor(float64(body.Position[2])))
	exposed = make([]companion.PlanBlock, 0, companion.MaxPlanExposedBlocks)
	heights = make([]companion.PlanHeight, 0, companion.MaxPlanHeightSamples)
	lowY := max(centerY-companion.PathWindowVerticalRadius, core.MinY)
	highY := min(centerY+companion.PathWindowVerticalRadius, core.MaxY-1)
	for x := centerX - 16; x <= centerX+16; x++ {
		for z := centerZ - 16; z <= centerZ+16; z++ {
			chunk := view.chunkFor(x, z)
			if chunk == nil {
				continue
			}
			columnHeights := chunk.Heights()
			heights = append(heights, companion.PlanHeight{
				X:      x,
				Z:      z,
				Height: columnHeights.Highest(int(x&core.SectionMask), int(z&core.SectionMask)),
			})
			for y := lowY; y <= highY; y++ {
				id, ok := view.blockAt(x, y, z)
				if !ok || id == core.AirID {
					continue
				}
				if view.hasAirNeighbor(x, y, z) {
					exposed = append(exposed, companion.PlanBlock{
						Pos:   core.BlockPos{X: x, Y: y, Z: z},
						Block: id,
					})
				}
			}
		}
	}
	return view, exposed, heights
}

// buildPlanSnapshot 在 tick 边界构造一次规划的不可变观察快照：
//   - 发令者事实在入队时刻冻结（captureIssuer），同一指令的规划输入不随
//     发令者后续移动而漂移；
//   - 环境摘要是伙伴周围水平 ±16、垂直 ±4 窗口内的暴露方块（≤256，按
//     (X,Y,Z) 确定性排序）与每列地表高度（高度表 O(1) 读取）；
//   - 相关区块 revision 与世界时间取当前权威值。
//
// 全部工作有界：窗口 33×33×9 格的常数扫描，不随世界规模增长。
func (m *companionManager) buildPlanSnapshot(
	definition companion.Definition,
	command companion.TaskCommand,
	issuer companionTaskIssuer,
	body companion.Body,
) (companion.PlanSnapshot, error) {
	view, exposed, heights := m.scanEnvObservation(body)

	snapshot := companion.PlanSnapshot{
		Command: string(command),
		Issuer: companion.PlanPlayer{
			ID:         issuer.playerID,
			Position:   issuer.position,
			Yaw:        issuer.yaw,
			Pitch:      issuer.pitch,
			LookHit:    issuer.lookHit,
			HasLookHit: issuer.hasLookHit,
		},
		Companion: companion.PlanCompanion{
			ID:         definition.ID,
			Position:   body.Position,
			Yaw:        body.Yaw,
			Pitch:      body.Pitch,
			Inventory:  body.Inventory,
			TaskStatus: m.taskStatusLabel(definition.ID),
		},
		ExposedBlocks:  companion.BoundExposedBlocks(exposed),
		Heights:        heights,
		ChunkRevisions: view.readyRevisions(),
		OnlinePlayers:  m.onlinePlanPlayers(),
		WorldTimeTicks: m.engine.WorldTime(),
	}
	return snapshot, snapshot.Validate()
}

// onlinePlanPlayers 返回 tick 边界的在线玩家集合（已按 ID 升序去重、至多
// MaxPlanOnlinePlayers 名）：经注入的会话注册表读取。注入缺失（防御路径，
// 生产构造总会注入）时返回 nil——快照因此不含任何在线玩家，follow 计划
// 会被解码层按「目标不在在线集合」拒绝，绝不凭空编造目标。
func (m *companionManager) onlinePlanPlayers() []companion.PlanPlayer {
	if m.onlinePlayers == nil {
		return nil
	}
	return m.onlinePlayers()
}

// onlinePlanPlayersSnapshot 枚举 tick 边界的全部在线玩家并归一为快照事实：
// 稳定 ID 来自会话注册表（playerSessions），位置与朝向取权威模拟；经
// BoundOnlinePlayers 按 ID 升序去重并截断到 MaxPlanOnlinePlayers（会话上限
// 同为八名，截断只是防御性内存界，正常路径永不触发）。调用方必须持有
// stepMu——会话表与模拟状态的唯一写者是权威 tick。视线命中方块不在此采集：
// follow 的目标校验只消费 ID 与位置，逐玩家 DDA 只会加大 tick 热路径成本
// 而没有任何已交付的消费方。
func (server *Server) onlinePlanPlayersSnapshot() []companion.PlanPlayer {
	players := make([]companion.PlanPlayer, 0, len(server.playerSessions))
	for playerID, sessionID := range server.playerSessions {
		player, ok := server.engine.Player(sessionID)
		if !ok {
			continue
		}
		players = append(players, companion.PlanPlayer{
			ID:       playerID,
			Position: [3]float32(player.State.Position),
			Yaw:      player.Yaw,
			Pitch:    player.Pitch,
		})
	}
	return companion.BoundOnlinePlayers(players)
}

// hasAirNeighbor 报告 (x,y,z) 的六邻域中是否存在空气。邻居越出世界竖直边界
// 视为空气（地表/床底面向界外的面是可见的）。
func (v companionChunkView) hasAirNeighbor(x, y, z int32) bool {
	neighbors := [6][3]int32{
		{x - 1, y, z}, {x + 1, y, z},
		{x, y - 1, z}, {x, y + 1, z},
		{x, y, z - 1}, {x, y, z + 1},
	}
	for _, neighbor := range neighbors {
		if neighbor[1] < core.MinY || neighbor[1] >= core.MaxY {
			return true
		}
		if id, ok := v.blockAt(neighbor[0], neighbor[1], neighbor[2]); ok && id == core.AirID {
			return true
		}
	}
	return false
}

// taskStatusLabel 返回伙伴当前任务状态中文摘要（≤96 字节、无模型自由文本），
// 进入 PlanCompanion.TaskStatus 供模型理解伙伴正在做什么。
func (m *companionManager) taskStatusLabel(id companion.ID) string {
	slot := m.slots[id]
	if slot == nil {
		return "空闲"
	}
	if current, ok := slot.queue.Current(); ok {
		return current.State.String()
	}
	return "空闲"
}

// buildPathGrid 在 tick 边界为一次寻路构造不可变网格快照。窗口覆盖的任一
// 区块未 ready 时返回 false——寻路顺延到下一 tick，不计入失败。
func (m *companionManager) buildPathGrid(
	body companion.Body,
	window companion.PathWindow,
) (companion.PathGrid, bool) {
	view := m.chunkViewAt(body.Dimension, body.Position)
	origin := window.Origin()
	sizeX, sizeY, sizeZ := window.Size()
	// 网格覆盖的区块矩形：窗口 33×33 格最多覆盖 3×3 区块，且必然落在 3×3
	// 视图内（视图以中心块所在区块为锚，±16 格不会越出视图角）。
	chunkOrigin := (core.BlockPos{X: origin.X, Z: origin.Z}).Chunk()
	chunkEnd := (core.BlockPos{X: origin.X + sizeX - 1, Z: origin.Z + sizeZ - 1}).Chunk()
	spanX := chunkEnd.X - chunkOrigin.X + 1
	spanZ := chunkEnd.Z - chunkOrigin.Z + 1
	if !view.allCoveredReady(chunkOrigin, spanX, spanZ) {
		return companion.PathGrid{}, false
	}
	revisions := make([]companion.ChunkRevision, 0, spanX*spanZ)
	for dz := chunkOrigin.Z; dz <= chunkEnd.Z; dz++ {
		for dx := chunkOrigin.X; dx <= chunkEnd.X; dx++ {
			revisions = append(revisions, companion.ChunkRevision{
				Chunk:    core.ChunkPos{X: dx, Z: dz},
				Revision: view.revisionAt(dx, dz),
			})
		}
	}
	grid, err := companion.NewPathGrid(
		core.BlockPos{X: origin.X, Y: origin.Y, Z: origin.Z},
		sizeX, sizeY, sizeZ,
		m.table,
		func(x, y, z int32) (core.BlockID, bool) {
			// 覆盖矩形已全部 ready，读取必然成功；防御性 ok=false 让
			// NewPathGrid 显式报错而不是读到半空网格。
			return view.blockAt(x, y, z)
		},
		revisions,
	)
	if err != nil {
		return companion.PathGrid{}, false
	}
	return grid, true
}

// windowRevisions 返回伙伴当前 3×3 兴趣区块的权威 revision，供路径点重验。
// 重验是 Running 任务每 tick 的热路径，只读 ChunkInfo 的 revision 元数据，
// 不做区块深拷贝；结果按 (X,Z) 字典序升序。
func (m *companionManager) windowRevisions(body companion.Body) []companion.ChunkRevision {
	center := (core.BlockPos{
		X: int32(math.Floor(float64(body.Position[0]))),
		Z: int32(math.Floor(float64(body.Position[2]))),
	}).Chunk()
	revisions := make([]companion.ChunkRevision, 0, 9)
	for dx := int32(-companionViewRadiusChunks); dx <= companionViewRadiusChunks; dx++ {
		for dz := int32(-companionViewRadiusChunks); dz <= companionViewRadiusChunks; dz++ {
			key := core.ChunkKey{
				Dimension: body.Dimension,
				Pos:       core.ChunkPos{X: center.X + dx, Z: center.Z + dz},
			}
			info, ok := m.engine.ChunkInfo(key)
			if !ok || info.State != sim.ChunkReady {
				continue
			}
			revisions = append(revisions, companion.ChunkRevision{
				Chunk:    key.Pos,
				Revision: info.Revision,
			})
		}
	}
	return revisions
}

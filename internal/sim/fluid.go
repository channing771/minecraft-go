package sim

import (
	"slices"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/fluid"
	"github.com/channing771/mornlea/internal/world"
)

// fluidQueue 返回 dimension 的流体待更新队列，必要时惰性创建。
//
// 硬约束：**每个维度必须持有独立的 Queue 实例**。internal/fluid 的处理全序是
// (dueTick, ChunkKey, y, z, x)，但 core.BlockPos 不携带维度，queue.go 的实现
// 只能用区块坐标 (X, Z) 近似排序键里的 ChunkKey，并在注释里把「调用方按维度
// 各持一个 Queue」写成了前置假设。若把两个维度的坐标混进同一个 Queue，不同
// 维度里 (X, Z, y) 相同的两格会比较为「相等」，全序退化为偏序，处理次序就变
// 得依赖 map 遍历顺序——确定性、Memory/TCP parity 与存档可复现性会一起静默
// 失效，而且任何单维度测试都照样全绿。因此队列必须按 DimensionID 分桶，
// 绝不能合并成一个全局 Queue。
func (engine *Engine) fluidQueue(dimension core.DimensionID) *fluid.Queue {
	if engine.fluidQueues == nil {
		engine.fluidQueues = make(map[core.DimensionID]*fluid.Queue)
	}
	queue := engine.fluidQueues[dimension]
	if queue == nil {
		queue = fluid.NewQueue()
		engine.fluidQueues[dimension] = queue
	}
	return queue
}

// fluidNeighbors 返回 position 的六个面邻格（上下 + 四个水平方向）。
//
// internal/fluid 内部有一份等价实现但未导出；这里只在入队点用它把「一次方块
// 写入」扩散成「该格及其六邻」，与流动规则本身无关，因此不值得为它扩大 fluid
// 包的公开 API。
func fluidNeighbors(position core.BlockPos) [6]core.BlockPos {
	return [6]core.BlockPos{
		{X: position.X, Y: position.Y + 1, Z: position.Z},
		{X: position.X, Y: position.Y - 1, Z: position.Z},
		{X: position.X + 1, Y: position.Y, Z: position.Z},
		{X: position.X - 1, Y: position.Y, Z: position.Z},
		{X: position.X, Y: position.Y, Z: position.Z + 1},
		{X: position.X, Y: position.Y, Z: position.Z - 1},
	}
}

// enqueueFluidUpdate 把一次权威方块写入的格及其六个面邻格加入流体待更新队列。
//
// 它挂在 recordChange 上，而不是逐个挂在放置、采掘、伙伴放置、伙伴采掘各自的
// 写入点：recordChange 是权威 tick 里「方块真的变了」的唯一汇聚点，挂一次就
// 覆盖全部现有写者，将来新增的方块写者也不可能漏接入队。
//
// 流体自身的写入（fluidWorld.SetBlock）同样流经这里，与 Queue.Advance 在提交
// 之后做的重新入队重复。这份重复是无害的：两边用的是同一个 now（本 tick 的
// engine.tick）与同一个 delay（本 tick 的 tunable 快照），而 Enqueue 按位置去
// 重并保留更早的 dueTick，因此重复入队幂等，不改变任何结果。
func (engine *Engine) enqueueFluidUpdate(
	dimension core.DimensionID,
	position core.BlockPos,
) {
	queue := engine.fluidQueue(dimension)
	now, delay := engine.fluidClock()
	queue.Enqueue(position, now, delay)
	for _, neighbor := range fluidNeighbors(position) {
		queue.Enqueue(neighbor, now, delay)
	}
}

// fluidClock 返回本 tick 的流体时基：now 是当前已完成的 tick 计数（Step 末尾
// 才 +1，因此同一次 Step 内的全部入队点与 Advance 读到同一个值），delay 是本
// tick tunable 快照里的流动延迟。
//
// delay 取自 engine.tunables 而不是 ActiveTunables()：与 advanceChunkFurnaces
// 同一条约定——Step 入口取一次快照，同 tick 内的所有推进函数都用这份快照，
// 参数不会在一个 tick 中途变化。
func (engine *Engine) fluidClock() (now, delay uint64) {
	return engine.tick.Load(), uint64(engine.tunables.FluidFlowDelayTicks)
}

// fluidWorld 是 internal/fluid 的 FluidWorld 在权威引擎上的适配器：把「按世界
// 坐标读写单格」映射到本 tick 推进范围内的已就绪区块，并把每次真实写入汇入
// 既有的区块变更集合（design.md D8：不新增协议消息）。
//
// 硬约束：**推进范围外与未加载的格必须读作「不可替换」，绝不能读作空气。**
// internal/fluid 的收敛证明建立在封闭盆地上——它的存活与替换判定只看单格读数，
// 一旦边界外被读成空气，水就会把边界当成有底洞：每 tick 向外写、写不进去、
// 又把该格算作「变化」重新入队，既永远不收敛，也会把 FluidUpdatesPerTick 预算
// 白白吃光。因此 record 返回 nil 的格一律读作 core.BarrierID——它非空气、非
// 流体，Replaceable 恒为假，也不构成任何存活支撑，正好把真实世界的范围边界
// 变成 internal/fluid 所要求的「封闭」边界。这也是 world.Neighborhood 对未加载
// 邻块采用的同一条约定。
type fluidWorld struct {
	engine    *Engine
	id        core.DimensionID
	dimension *Dimension
	// scope 是本 tick 允许读写的区块集合（活动兴趣区块 ∩ ChunkReady）。
	scope   map[core.ChunkKey]struct{}
	pending map[core.ChunkKey]*pendingChunkChanges
}

// record 定位 position 所属的可读写区块记录；越界、超出推进范围或区块未就绪
// 时返回 nil。
func (w *fluidWorld) record(position core.BlockPos) *ChunkRecord {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		// 世界高度之外同样按「不可替换」处理。Dimension.BlockAt 在这里返回
		// 空气，若沿用那条语义，贴着世界底面的水会永远向 MinY-1 写、永远写
		// 不进去，落入上面描述的假开口死循环。
		return nil
	}
	key := core.ChunkKey{Dimension: w.id, Pos: position.Chunk()}
	if _, inScope := w.scope[key]; !inScope {
		return nil
	}
	record := w.dimension.records[key.Pos]
	if record == nil || record.State != ChunkReady || record.Chunk == nil {
		return nil
	}
	return record
}

// BlockAt 实现 fluid.FluidWorld：范围外的格读作 core.BarrierID（见类型注释）。
func (w *fluidWorld) BlockAt(position core.BlockPos) core.BlockID {
	record := w.record(position)
	if record == nil {
		return core.BarrierID
	}
	x, _, z := position.Local()
	return record.Chunk.BlockAt(x, position.Y, z)
}

// SetBlock 实现 fluid.FluidWorld：写入区块并把变更汇入本 tick 的
// pendingChunkChanges，与放置、采掘、掉落物、熔炉共用同一批广播与存盘。
func (w *fluidWorld) SetBlock(position core.BlockPos, id core.BlockID) {
	record := w.record(position)
	if record == nil {
		// 防御性分支。BlockAt 已把范围外的格读作不可替换，evalCell 因此不会
		// 把写入目标定在范围外；真走到这里说明规则集发生了变化，此时丢弃写入
		// 比越界改写未加载区块安全。
		return
	}
	x, _, z := position.Local()
	if record.Chunk.BlockAt(x, position.Y, z) == id {
		return
	}
	record.Chunk.SetBlock(x, position.Y, z, id)
	w.engine.recordChange(w.id, position, id, w.pending)
}

// fluidBoundaryPlane 描述一个水平邻块中「贴着本区块」的那一层边界平面：邻块
// 相对本区块的区块偏移，以及该平面在邻块内的局部 x/z 范围（y 覆盖整列）。
type fluidBoundaryPlane struct {
	dx, dz         int32
	x0, x1, z0, z1 int
}

// fluidBoundaryPlanes 是四个水平方向上的邻块边界平面。区块是整列结构，没有
// 上下邻块，因此只有四个侧面。
var fluidBoundaryPlanes = [4]fluidBoundaryPlane{
	{dx: 1, x0: 0, x1: 0, z0: 0, z1: core.SectionMask},
	{dx: -1, x0: core.SectionMask, x1: core.SectionMask, z0: 0, z1: core.SectionMask},
	{dz: 1, x0: 0, x1: core.SectionMask, z0: 0, z1: 0},
	{dz: -1, x0: 0, x1: core.SectionMask, z0: core.SectionMask, z1: core.SectionMask},
}

// rescanChunkFluids 对一个刚进入流体推进范围的区块执行一次边界重扫入队。
//
// 硬约束：**重扫必须覆盖全部可能产生写入的格，包括相邻区块贴着本区块那一侧
// 的流体格。** design.md D5「队列不持久化、重启靠重扫恢复」的全部依据是「平衡态
// 是重扫的不动点」，而该性质依赖重扫的完整性：evalCell 对非流体格恒产出空写入，
// 所以「能产生写入的格」是「全部流体格」的子集。spec 与 design 里写的重扫集合是
// 「流体格及其空气邻居」，其中空气邻居那一半在当前规则集下是纯冗余，漏掉无害；
// 真正承重的是每一个**可能产生写入的**流体格都被入队。
//
// enqueueChunkFluids 在此基础上进一步排除了可证的不动点水源（详见
// fluidSourceIsFixedPoint 的论证）。那是对「能产生写入的格」这个集合的精确刻画，
// 不是对完整性的放宽：被排除的格 evalCell 必然返回空写入集合。
//
// 只扫本区块内部就会漏掉接缝另一侧：邻块的水在本区块还没进范围时把本区块读作
// 实心（见 fluidWorld）而静止并从队列中排空，本区块进来之后没有任何东西会重新
// 唤醒它们，水面就永久卡死在区块边界上。邻块未就绪时不必处理——它自己进入范围
// 时会做对称的一次重扫，把本区块边界平面上的流体格入队。
// 重扫是可中断的：它按 engine.fluidRescan 记录的游标（第几个平面、第几个区段）
// 续扫，最多花掉 budget 格的检查额度，返回实际花掉的额度与本区块是否已扫完。
// 未扫完时游标原样保留，下一 tick 从断点继续（见 fluidRescanState）。
func (engine *Engine) rescanChunkFluids(
	queue *fluid.Queue,
	dimension *Dimension,
	pos core.ChunkPos,
	now, delay uint64,
	budget int,
) (spent int, done bool) {
	record := dimension.records[pos]
	if record == nil || record.Chunk == nil {
		engine.fluidRescan.resetCursor()
		return 0, true
	}
	state := &engine.fluidRescan
	// plane == 0 是本区块整块；plane 1..4 依次是四个水平邻块的边界平面。
	for state.plane <= len(fluidBoundaryPlanes) {
		chunk, chunkPos := record.Chunk, pos
		x0, x1, z0, z1 := 0, core.SectionMask, 0, core.SectionMask
		if state.plane > 0 {
			plane := fluidBoundaryPlanes[state.plane-1]
			chunkPos = core.ChunkPos{X: pos.X + plane.dx, Z: pos.Z + plane.dz}
			neighbor := dimension.records[chunkPos]
			if neighbor == nil || neighbor.State != ChunkReady || neighbor.Chunk == nil {
				state.plane++
				state.section = 0
				continue
			}
			chunk = neighbor.Chunk
			x0, x1, z0, z1 = plane.x0, plane.x1, plane.z0, plane.z1
		}
		used, finished := enqueueChunkFluids(
			queue, dimension, chunk, chunkPos,
			x0, x1, z0, z1, now, delay, budget-spent, &state.section,
		)
		spent += used
		if !finished {
			return spent, false
		}
		state.plane++
		state.section = 0
	}
	state.resetCursor()
	return spent, true
}

// fluidRescanBlockAt 按 fluidWorld 的边界约定读取 dimension 中的单格：世界高度
// 之外、未加载、未就绪的格一律读作 core.BarrierID。
//
// 它与 fluidWorld.BlockAt 的差别只有一处——不看本 tick 的推进范围（scope）。
// 这个差别的方向是安全的：fluidWorld 对「已就绪但不在 scope 内」的区块读作
// BarrierID，而 BarrierID 是最"实心"的读数（非空气、非流体，Replaceable 恒假）。
// 因此本函数读出的结果绝不会比 fluidWorld 更实心，凡是本函数判定为「不可替换」
// 的格，fluidWorld 在同一 tick 内也一定判定为「不可替换」。下面两个不动点判据
// 全都建立在这条单向性上。
func fluidRescanBlockAt(dimension *Dimension, position core.BlockPos) core.BlockID {
	if position.Y < core.MinY || position.Y >= core.MaxY {
		return core.BarrierID
	}
	record := dimension.records[position.Chunk()]
	if record == nil || record.State != ChunkReady || record.Chunk == nil {
		return core.BarrierID
	}
	x, _, z := position.Local()
	return record.Chunk.BlockAt(x, position.Y, z)
}

// fluidSealedSourceOffsets 是水源不动点判据要检查的五个邻格偏移：下方与四个
// 水平方向。上方**刻意不在其中**——见 fluidSourceIsFixedPoint 的论证。
var fluidSealedSourceOffsets = [5][3]int32{
	{0, -1, 0},
	{1, 0, 0},
	{-1, 0, 0},
	{0, 0, 1},
	{0, 0, -1},
}

// fluidSourceIsFixedPoint 报告 position 上的**水源**是否可证是重扫的不动点，
// 即 internal/fluid 的 evalCell 对它必然产出空写入集合，因而把它排除出重扫入队
// 集合不会改变任何结果。
//
// # 为什么这条捷径是安全的（这是本函数存在的全部理由）
//
// 逐条对齐 evalCell 的三段逻辑：
//
//  1. 存活判定。evalCell 只对**非源**流动格做 flowingSurvives；源方块走
//     「源永不自然消失」这条规则，直接跳过存活判定。所以对水源来说，上方邻格
//     读到什么都与结果无关——这就是 fluidSealedSourceOffsets 不含上方的原因，
//     也让本判据完全不依赖任何"读到流体才成立"的正向条件（见第 4 点）。
//  2. 垂直传播。源要向下写等级 1 的流动水，当且仅当 Replaceable(below, 1)。
//  3. 水平传播。源的等级读作 0，nextLevel = 1，因此向某个水平邻格写入当且仅当
//     Replaceable(neighbor, 1)。
//
// 三段合起来：水源产生写入 ⟺ 下方或四个水平邻格中至少有一个可被等级 1 替换。
// 全部五个邻格都不可替换时，evalCell 返回空 map，本格是不动点。
//
//  4. 判据只在「读到不可替换」时才跳过，而 fluidRescanBlockAt 的读数绝不会比
//     Advance 时 fluidWorld 的读数更实心（见该函数注释）。因此本函数说"不可
//     替换"时，Advance 时也一定不可替换——误差只可能落在"本函数保守地判定成
//     可替换、于是多入一次队"这一侧，多入队永远无害。
//  5. 被跳过的格不会静默睡死。任何让邻格变得可替换的事件都是一次权威方块写入，
//     而全部方块写入汇聚在 recordChange → enqueueFluidUpdate，后者把改动格
//     **及其六个面邻格**重新入队，本格必在其中。区块重新进入推进范围则由
//     advanceFluids 的重扫兜住。
//
// 判据只覆盖水源，不覆盖流动水：流动水的存活依赖"上方或某个更强的水平邻格是
// 流体"这类**正向**条件，一旦沿用第 4 点的单向性就不再安全（读数偏实心会把
// "会消失"误判成"不动"）。而流动水在一个平衡水体里只出现在表面与边缘，数量本
// 就是 O(表面)，不值得为它把论证复杂化。
// section 与 localX/localY/localZ 是 position 所在区段及其区段内局部坐标：邻格
// 仍落在同一区段内时直接读区段（这是绝大多数情况），避开 dimension.records 的
// map 查找；只有跨区段/跨区块的邻格才走 fluidRescanBlockAt。两条路径读的是同一
// 份数据，只是快慢不同。
func fluidSourceIsFixedPoint(
	dimension *Dimension,
	section *world.Section,
	localX, localY, localZ int,
	position core.BlockPos,
) bool {
	for _, offset := range fluidSealedSourceOffsets {
		nx, ny, nz := localX+int(offset[0]), localY+int(offset[1]), localZ+int(offset[2])
		var neighbor core.BlockID
		if uint(nx) < core.SectionSize && uint(ny) < core.SectionSize && uint(nz) < core.SectionSize {
			neighbor = section.Blocks.Get(nx, ny, nz)
		} else {
			neighbor = fluidRescanBlockAt(dimension, core.BlockPos{
				X: position.X + offset[0],
				Y: position.Y + offset[1],
				Z: position.Z + offset[2],
			})
		}
		if fluid.Replaceable(neighbor, 1) {
			return false
		}
	}
	return true
}

// fluidSectionUnreplaceable 报告 (pos, sectionIndex) 这一整个区段是否都不可被
// 等级 1 的流动水替换。区段索引越出世界上下界、区块未加载或未就绪时按
// fluidRescanBlockAt 的同一条约定读作 core.BarrierID，同样不可替换。
//
// 只承认 IsUniform 的区段：混杂区段逐格判断的成本正是这条捷径要省掉的，
// 判不出来就返回 false 让调用方退回逐格路径，方向仍然是保守的。
func fluidSectionUnreplaceable(dimension *Dimension, pos core.ChunkPos, sectionIndex int) bool {
	if sectionIndex < 0 || sectionIndex >= core.SectionsPerChunk {
		return true
	}
	record := dimension.records[pos]
	if record == nil || record.State != ChunkReady || record.Chunk == nil {
		return true
	}
	id, uniform := record.Chunk.Section(sectionIndex).Blocks.IsUniform()
	return uniform && !fluid.Replaceable(id, 1)
}

// fluidSectionIsFixedPoint 是 fluidSourceIsFixedPoint 的区段级 O(1) 快路径：
// 当整个区段是均匀水源、且「下方区段」与「四个水平邻块的同索引区段」都整段
// 不可被等级 1 替换时，该区段的 4096 格全部满足 fluidSourceIsFixedPoint 的
// 五邻条件（区段内部的邻格就是水源自身，跨面的邻格由这五次 IsUniform 覆盖），
// 于是整段跳过。
//
// 这条快路径承担的正是本次修复的主要收益：海洋内部的均匀水源区段从「4096 次
// 入队」降到「5 次 IsUniform」，重扫量由 O(体积) 变成 O(表面)。
func fluidSectionIsFixedPoint(dimension *Dimension, pos core.ChunkPos, sectionIndex int) bool {
	if !fluidSectionUnreplaceable(dimension, pos, sectionIndex-1) {
		return false
	}
	for _, plane := range fluidBoundaryPlanes {
		neighborPos := core.ChunkPos{X: pos.X + plane.dx, Z: pos.Z + plane.dz}
		if !fluidSectionUnreplaceable(dimension, neighborPos, sectionIndex) {
			return false
		}
	}
	return true
}

// enqueueChunkFluids 把 chunk 中局部 x∈[x0,x1]、z∈[z0,z1] 这一段整列内**可能
// 产生写入的**流体格入队；x0..z1 用区段内局部坐标 0..15，y 恒覆盖世界全高。
//
// 两级捷径，都只跳过可证的不动点，不改变入队集合的语义：
//
//   - 区段级：单值态且该值不是流体的区段整段跳过（evalCell 对非流体格恒产出
//     空写入）；单值态水源区段满足 fluidSectionIsFixedPoint 时也整段跳过。
//   - 逐格级：水源格满足 fluidSourceIsFixedPoint 时跳过。
//
// 水源不动点这一层是必需的而不是锦上添花：一个谷地区块（SEA_LEVEL=64、
// TERRAIN_AMP=48，地形高度可低至 16）单列最多约 44 层满格水，整块约 1.1 万格；
// 若按体积全量入队，八名玩家的最坏兴趣范围（约 200 区块）会在权威 tick 上产生
// 二百多万次入队，远超 20 TPS 的 50 ms 预算。跳过内部水格后入队量降到 O(表面)。
// 预算与续扫：section 是「下一个要扫的区段索引」的读写游标，函数在**每个区段
// 开始前**检查剩余额度，因此单次调用最多超支一个区段。返回实际花掉的额度与
// 本次调用是否把 x0..z1 这一段的全部区段扫完；未扫完时 *section 停在断点。
func enqueueChunkFluids(
	queue *fluid.Queue,
	dimension *Dimension,
	chunk *world.Chunk,
	pos core.ChunkPos,
	x0, x1, z0, z1 int,
	now, delay uint64,
	budget int,
	section_ *int,
) (spent int, done bool) {
	baseX := pos.X << core.SectionShift
	baseZ := pos.Z << core.SectionShift
	for ; *section_ < core.SectionsPerChunk; *section_++ {
		if spent >= budget {
			return spent, false
		}
		sectionIndex := *section_
		section := chunk.Section(sectionIndex)
		if id, uniform := section.Blocks.IsUniform(); uniform {
			if !core.IsFluid(id) {
				// 整段跳过只做了一次 IsUniform，按 1 格额度记账。
				spent++
				continue
			}
			if id == core.WaterSourceID && fluidSectionIsFixedPoint(dimension, pos, sectionIndex) {
				spent++
				continue
			}
		}
		baseY := int32(sectionIndex<<core.SectionShift) + core.MinY
		for localY := range core.SectionSize {
			for localZ := z0; localZ <= z1; localZ++ {
				for localX := x0; localX <= x1; localX++ {
					spent++
					id := section.Blocks.Get(localX, localY, localZ)
					if !core.IsFluid(id) {
						continue
					}
					position := core.BlockPos{
						X: baseX + int32(localX),
						Y: baseY + int32(localY),
						Z: baseZ + int32(localZ),
					}
					if id == core.WaterSourceID && fluidSourceIsFixedPoint(
						dimension, section, localX, localY, localZ, position,
					) {
						continue
					}
					queue.Enqueue(position, now, delay)
				}
			}
		}
	}
	*section_ = 0
	return spent, true
}

// fluidRescanState 是跨 tick 的边界重扫待办。
//
// 为什么重扫必须分摊到多个 tick：一个区块进入推进范围时的重扫工作量正比于该
// 区块（及四个邻块边界平面）里的方块数，与 FluidUpdatesPerTick 无关——那个
// tunable 只截断「处理队列里已有的项」，截不住「把格放进队列」这一步。评审
// 实测八名玩家的最坏兴趣范围一次性进入时，单 tick 要花 204 ms 做入队，而 20
// TPS 的 tick 预算只有 50 ms。因此重扫本身也必须有预算并跨 tick 续做。
//
// 为什么延后重扫是安全的：design.md D5 的不动点性质只要求重扫**最终**发生在
// 该区块处于推进范围内的某个 tick，不要求发生在它进入范围的那一 tick。晚几个
// tick 唤醒的后果只是水晚一点开始流，没有正确性后果。
//
// 为什么离开范围要整条丢弃而不是保留游标：重扫到一半的区块，已入队的那部分
// 会在区块离开范围后被 Advance 取出、读到 core.BarrierID、产出空写入并从队列
// 移除；若此时保留游标、等区块回来只补扫剩下的一半，先扫的那一半就永远没人
// 唤醒了。丢弃后重新进入时从头重扫，代价是重复一次扫描，换来的是完整性。
type fluidRescanState struct {
	// pending 是待重扫区块，按进入推进范围的先后排列（同一 tick 内按
	// activeInterestKeys 的稳定序），先进先扫。
	pending []core.ChunkKey
	// queued 与 pending 同集合，用来 O(1) 去重：区块反复进出范围时不能在
	// pending 里堆出多份。
	queued map[core.ChunkKey]struct{}
	// plane/section 是 pending[0] 的续扫游标：plane 0 表示本区块整块，
	// 1..4 表示 fluidBoundaryPlanes 的第 plane-1 个邻块边界平面；section 是
	// 该平面里下一个要扫的区段索引。
	plane   int
	section int
}

// resetCursor 把续扫游标复位到「从本区块第 0 个区段重新开始」。
func (state *fluidRescanState) resetCursor() {
	state.plane = 0
	state.section = 0
}

// enqueueChunk 把 key 登记为待重扫；已在待办里时不重复登记。
func (state *fluidRescanState) enqueueChunk(key core.ChunkKey) {
	if state.queued == nil {
		state.queued = make(map[core.ChunkKey]struct{})
	}
	if _, exists := state.queued[key]; exists {
		return
	}
	state.queued[key] = struct{}{}
	state.pending = append(state.pending, key)
}

// dropOutOfScope 丢弃已经离开推进范围的待重扫区块。队首被丢弃时游标一并复位，
// 因为游标只对当时的队首有意义。
func (state *fluidRescanState) dropOutOfScope(scope map[core.ChunkKey]struct{}) {
	if len(state.pending) == 0 {
		return
	}
	head := state.pending[0]
	kept := state.pending[:0]
	for _, key := range state.pending {
		if _, inScope := scope[key]; !inScope {
			delete(state.queued, key)
			continue
		}
		kept = append(kept, key)
	}
	state.pending = kept
	if len(kept) == 0 || kept[0] != head {
		state.resetCursor()
	}
}

// runFluidRescans 在本 tick 的重扫额度内推进待办队列。
//
// 额度用完（或待办排空）就停下，未扫完的区块留在队首、游标保留，下一 tick 续扫。
func (engine *Engine) runFluidRescans(now, delay uint64) {
	state := &engine.fluidRescan
	state.dropOutOfScope(engine.fluidScope)
	budget := int(engine.tunables.FluidRescanCellsPerTick)
	for budget > 0 && len(state.pending) > 0 {
		key := state.pending[0]
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			// 维度消失（正常运行里不会发生）：丢弃这条待办，不能让它卡住队首。
			state.resetCursor()
			delete(state.queued, key)
			state.pending = append(state.pending[:0], state.pending[1:]...)
			continue
		}
		spent, done := engine.rescanChunkFluids(
			engine.fluidQueue(key.Dimension), dimension, key.Pos, now, delay, budget,
		)
		budget -= spent
		if !done {
			return
		}
		delete(state.queued, key)
		state.pending = append(state.pending[:0], state.pending[1:]...)
	}
}

// advanceFluids 在单写者权威 tick 中推进活动兴趣范围内的流体。
//
// 形状与 advanceFurnaces 一致：只遍历 activeInterestKeys() 里 State ==
// ChunkReady 且 Chunk != nil 的区块，变更汇入调用方传入的同一批
// pendingChunkChanges。tunable 取自 engine.tunables（本 tick 入口的快照），
// 本函数绝不调用 ActiveTunables()。
//
// 与熔炉不同的是流体是跨区块的：队列按维度全局排序，推进范围由 fluidWorld 的
// scope 强制，而不是靠「逐区块调用」实现——一格的求值要读它的六个邻格，其中
// 可能有邻块的格。
//
// 推进范围的进出由重扫兜住：任何新进入范围的区块都会被登记进 fluidRescan 待办
// 并在额度允许的 tick 里做一次边界重扫。这一条同时覆盖了两件事——区块刚变成
// ChunkReady（此前它根本不在范围内），以及一个早已就绪的区块因玩家移动重新进入
// 范围（spec「区块重新进入兴趣范围后恢复推进」）。后者必须靠重扫恢复：范围外的
// 待更新项仍会被 Advance 取出，读到 core.BarrierID 后产出空写入并从队列中移除，
// 若不重扫就再也没有东西唤醒它们。
//
// 重扫本身有独立预算并跨 tick 分摊（fluidRescanState / FluidRescanCellsPerTick）：
// 它的工作量正比于新进入范围的方块数，与 FluidUpdatesPerTick 无关，不分摊会在
// 大批区块同时进入范围时直接击穿 tick 预算。
func (engine *Engine) advanceFluids(pending map[core.ChunkKey]*pendingChunkChanges) {
	now, delay := engine.fluidClock()
	budget := int(engine.tunables.FluidUpdatesPerTick)

	if engine.fluidScope == nil {
		engine.fluidScope = make(map[core.ChunkKey]struct{})
		engine.fluidScopeNext = make(map[core.ChunkKey]struct{})
	}
	clear(engine.fluidScopeNext)
	keys := engine.activeInterestKeys()
	for _, key := range keys {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record, ok := dimension.records[key.Pos]
		if !ok || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		engine.fluidScopeNext[key] = struct{}{}
	}
	// activeInterestKeys 已按 chunkKeyLess 排好序，重扫待办因此按稳定顺序登记。
	for _, key := range keys {
		if _, inScope := engine.fluidScopeNext[key]; !inScope {
			continue
		}
		if _, wasInScope := engine.fluidScope[key]; wasInScope {
			continue
		}
		engine.fluidRescan.enqueueChunk(key)
	}
	engine.fluidScope, engine.fluidScopeNext = engine.fluidScopeNext, engine.fluidScope
	engine.runFluidRescans(now, delay)

	for _, id := range engine.sortedFluidDimensions() {
		queue := engine.fluidQueues[id]
		dimension := engine.dimensions[id]
		if dimension == nil || queue.Len() == 0 {
			continue
		}
		queue.Advance(now, &fluidWorld{
			engine:    engine,
			id:        id,
			dimension: dimension,
			scope:     engine.fluidScope,
			pending:   pending,
		}, budget, delay)
	}
}

// sortedFluidDimensions 返回持有流体队列的维度 ID，按数值升序。
// 多维度下的推进次序必须确定，不能依赖 map 遍历顺序。
func (engine *Engine) sortedFluidDimensions() []core.DimensionID {
	ids := engine.fluidDimensionScratch[:0]
	for id := range engine.fluidQueues {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	engine.fluidDimensionScratch = ids
	return ids
}

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
// 硬约束：**重扫必须覆盖全部流体格，包括相邻区块贴着本区块那一侧的流体格。**
// design.md D5「队列不持久化、重启靠重扫恢复」的全部依据是「平衡态是重扫的
// 不动点」，而该性质依赖重扫的完整性：evalCell 对非流体格恒产出空写入，所以
// 「能产生写入的格」恰好就是「全部流体格」。spec 与 design 里写的重扫集合是
// 「流体格及其空气邻居」，其中空气邻居那一半在当前规则集下是纯冗余，漏掉无害；
// 真正承重的是每一个流体格都被入队。
//
// 只扫本区块内部就会漏掉接缝另一侧：邻块的水在本区块还没进范围时把本区块读作
// 实心（见 fluidWorld）而静止并从队列中排空，本区块进来之后没有任何东西会重新
// 唤醒它们，水面就永久卡死在区块边界上。邻块未就绪时不必处理——它自己进入范围
// 时会做对称的一次重扫，把本区块边界平面上的流体格入队。
func (engine *Engine) rescanChunkFluids(
	queue *fluid.Queue,
	dimension *Dimension,
	pos core.ChunkPos,
	now, delay uint64,
) {
	record := dimension.records[pos]
	if record == nil || record.Chunk == nil {
		return
	}
	enqueueChunkFluids(queue, record.Chunk, pos, 0, core.SectionMask, 0, core.SectionMask, now, delay)
	for _, plane := range fluidBoundaryPlanes {
		neighborPos := core.ChunkPos{X: pos.X + plane.dx, Z: pos.Z + plane.dz}
		neighbor := dimension.records[neighborPos]
		if neighbor == nil || neighbor.State != ChunkReady || neighbor.Chunk == nil {
			continue
		}
		enqueueChunkFluids(
			queue, neighbor.Chunk, neighborPos,
			plane.x0, plane.x1, plane.z0, plane.z1, now, delay,
		)
	}
}

// enqueueChunkFluids 把 chunk 中局部 x∈[x0,x1]、z∈[z0,z1] 这一段整列内的全部
// 流体格入队；x0..z1 用区段内局部坐标 0..15，y 恒覆盖世界全高。
//
// 逐区段先看 IsUniform：单值态且该值不是流体的区段整段跳过。一次整块重扫名义
// 上是 16×16×384 格，但绝大多数区段是纯空气或纯石头，这条捷径把读取量压到只
// 剩真正混杂的少数区段——重扫发生在权威 tick 内（区块进入推进范围的那一 tick），
// 不能按全高全宽硬扫。它只是性能捷径，不影响入队集合的正确性。
func enqueueChunkFluids(
	queue *fluid.Queue,
	chunk *world.Chunk,
	pos core.ChunkPos,
	x0, x1, z0, z1 int,
	now, delay uint64,
) {
	baseX := pos.X << core.SectionShift
	baseZ := pos.Z << core.SectionShift
	for sectionIndex := range core.SectionsPerChunk {
		section := chunk.Section(sectionIndex)
		if id, uniform := section.Blocks.IsUniform(); uniform && !core.IsFluid(id) {
			continue
		}
		baseY := int32(sectionIndex<<core.SectionShift) + core.MinY
		for localY := range core.SectionSize {
			for localZ := z0; localZ <= z1; localZ++ {
				for localX := x0; localX <= x1; localX++ {
					if !core.IsFluid(section.Blocks.Get(localX, localY, localZ)) {
						continue
					}
					queue.Enqueue(core.BlockPos{
						X: baseX + int32(localX),
						Y: baseY + int32(localY),
						Z: baseZ + int32(localZ),
					}, now, delay)
				}
			}
		}
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
// 推进范围的进出由重扫兜住：任何本 tick 新进入范围的区块都先做一次边界重扫。
// 这一条同时覆盖了两件事——区块刚变成 ChunkReady（此前它根本不在范围内），
// 以及一个早已就绪的区块因玩家移动重新进入范围（spec「区块重新进入兴趣范围后
// 恢复推进」）。后者必须靠重扫恢复：范围外的待更新项仍会被 Advance 取出，读到
// core.BarrierID 后产出空写入并从队列中移除，若不重扫就再也没有东西唤醒它们。
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
	// activeInterestKeys 已按 chunkKeyLess 排好序，重扫因此按稳定顺序发生。
	for _, key := range keys {
		if _, inScope := engine.fluidScopeNext[key]; !inScope {
			continue
		}
		if _, wasInScope := engine.fluidScope[key]; wasInScope {
			continue
		}
		engine.rescanChunkFluids(
			engine.fluidQueue(key.Dimension),
			engine.dimensions[key.Dimension],
			key.Pos, now, delay,
		)
	}
	engine.fluidScope, engine.fluidScopeNext = engine.fluidScopeNext, engine.fluidScope

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

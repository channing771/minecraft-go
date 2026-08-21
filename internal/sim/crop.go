package sim

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// 本文件实现作物的**随机 tick 推进**：抽样、生长规则与两个环境判定。
//
// # 为什么是随机 tick 而不是显式待更新队列（design.md D3）
//
// 流体用队列，作物用抽样，差别来自两者的容错性质而不是口味：
//
//   - 流体是**向平衡态收敛**的。漏掉一次更新，水就永远停在错误状态，因此必须
//     有队列保证「该更新的格一个都不能丢」，代价是队列规模随水体规模增长
//     （瀑布场景实测稳定在 50 万项）。
//   - 作物是**按时间推进**的。漏抽一次只是这株麦子长得慢一点，不破坏任何
//     不变量。于是它可以用一个成本与作物数量完全无关的机制：每 tick 只在每个
//     区段里抽固定几格看一眼。
//
// 这正是 spec「单个 tick 内被考察的格数 MUST 只正比于活动兴趣范围内的区段数，
// MUST NOT 随世界中作物的数量增长」的实现依据。被否决的替代是「每格存下次生长
// tick」：要么每 tick 扫全部已加载作物格（无界），要么还得为它建索引——绕回队列。
//
// # 确定性
//
// 抽样与生长概率都是 (worldSeed, tick, 位置) 的纯整数哈希，不用 math/rand、
// 不用浮点、不遍历 map 决定顺序。枚举顺序由 activeInterestKeys()（已按
// chunkKeyLess 全序）与区段索引升序共同固定。

// splitmix64 是 SplittableRandom 的 mix64 终结器。
//
// 选它而不是 FNV-1a 或 xorshift，理由有三条：
//
//  1. 它是**纯整数**的（加、乘、异或、右移），没有任何浮点运算，在所有平台上
//     逐位一致——权威模拟的确定性要求哈希结果不能有平台差异。
//  2. 雪崩性质好且**低位也好**。抽样最后要取 mod 4096，用的正是最低 12 位；
//     FNV-1a 的低位雪崩很弱，直接取低 12 位会出现可见的条带，而 splitmix64 的
//     两轮 xor-shift-multiply 让每一位都依赖全部输入位。
//  3. 实现只有四行、没有状态、可被逐字复核——权威路径上的哈希越无聊越好。
//
// 三个常量分别是 2^64/φ（黄金比例倒数）与 splitmix64 原始论文给出的两个乘子，
// 不得随意替换：换掉它们会改变全世界的生长抽样序列。
func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// cropSectionHash 把 (worldSeed, tick, 维度, 区块坐标, 区段索引) 折叠成该区段
// 本 tick 的抽样基准哈希。
//
// 逐个输入串行折叠而不是先拼成一个整数再哈希一次：拼接要为每个字段划定位宽，
// 而区块坐标是 32 位有符号、tick 是 64 位，拼不进一个 uint64；串行折叠没有
// 位宽预算，也不会因为某两个字段互换而产生相同结果。
//
// 有符号量一律先转 uint32 再零扩展，保证负坐标（例如 X=-7）与它的补码表示
// 一一对应，不会因为符号扩展与另一个正坐标撞车。
func cropSectionHash(seed int64, tick uint64, key core.ChunkKey, sectionY int) uint64 {
	hash := splitmix64(uint64(seed))
	hash = splitmix64(hash ^ tick)
	hash = splitmix64(hash ^ uint64(uint32(key.Dimension)))
	hash = splitmix64(hash ^ uint64(uint32(key.Pos.X)))
	hash = splitmix64(hash ^ uint64(uint32(key.Pos.Z)))
	return splitmix64(hash ^ uint64(uint32(sectionY)))
}

// sampleCells 返回 (key, sectionY) 这个区段在 tick 上要考察的 n 个格下标。
//
// 下标是区段内的紧凑编号 y*256 + z*16 + x，取值 0..4095，与
// world.ChunkBlockIndex 在区段内使用的编号规则一致。
//
// 结果写入 out[:0] 并返回：调用方（advanceCrops）复用同一个 scratch，因此这条
// 每 tick 执行「区块数 × 24」次的路径上没有任何分配。传 nil 时按需分配，测试
// 因此可以直接 sampleCells(..., nil)。
//
// **允许同一 tick 抽到重复下标**（与 MC 的随机 tick 同）：去重要引入 map 或
// 排序，成本高于「偶尔白看一格」的收益，而且重复只是让本 tick 的有效抽样数
// 略少，不破坏任何不变量。
func sampleCells(seed int64, tick uint64, key core.ChunkKey, sectionY, n int, out []int) []int {
	cells := out[:0]
	if n <= 0 {
		return cells
	}
	base := cropSectionHash(seed, tick, key, sectionY)
	for index := range n {
		// 每条抽样再过一次 splitmix64：base 只算一次并在 n 条之间摊薄，
		// 而 index 的低位差异经这一轮后已经完全雪崩开。
		cells = append(cells, int(splitmix64(base^uint64(index))%core.BlocksPerSection))
	}
	return cells
}

// cropGrowthRollSalt 让生长概率的哈希流与抽样的哈希流互相独立。
//
// 没有它，两者会在相同的 (seed, tick) 前缀上同源，「这一格被抽中」与「这一格
// 通过概率判定」之间就可能出现结构性相关——例如某些区段永远抽中却永远不通过。
const cropGrowthRollSalt = 0xc0ffee5eedca11ed

// cropGrowthRoll 报告 position 上的作物在 tick 上是否通过生长概率判定。
//
// 与抽样同源的设计：纯整数哈希、无全局 RNG、无浮点，结果只依赖
// (worldSeed, tick, 方块坐标)，因此重放同一段 tick 必然得到同一串判定。
//
// `hash % 100` 有理论上的取模偏差（2^64 不是 100 的整数倍），最大偏差量级约
// 1e-17，远小于任何可观察的游戏效果，不值得为它引入拒绝采样循环——而拒绝采样
// 的循环次数不定，反而给权威 tick 引入一个无上界的分支。
func cropGrowthRoll(seed int64, tick uint64, position core.BlockPos, chancePercent uint8) bool {
	// 0% 与 100% 单独短路：它们是测试与调试面板最常用的两个端点值，短路让
	// 「设成 100 必长」这条测试前提不依赖哈希分布。
	if chancePercent == 0 {
		return false
	}
	if chancePercent >= 100 {
		return true
	}
	hash := splitmix64(uint64(seed) ^ cropGrowthRollSalt)
	hash = splitmix64(hash ^ tick)
	hash = splitmix64(hash ^ uint64(uint32(position.X)))
	hash = splitmix64(hash ^ uint64(uint32(position.Y)))
	hash = splitmix64(hash ^ uint64(uint32(position.Z)))
	return hash%100 < uint64(chancePercent)
}

// growCrop 是生长规则本身：给定方块与它所处的环境，返回下一个方块编号与是否
// 发生变化。
//
// 它刻意**不接触世界状态**，也不做概率判定——那两件事分别由调用方的环境查询
// 与 cropGrowthRoll 负责。这样切分是为了让规则本身可以被 32 种输入穷举验证
// （design.md D4）。
//
// 四条规则：
//   - 非作物 → 原样返回（耕地也在此列，见 core.IsCrop 的说明）；
//   - 成熟作物（WheatStage7ID）→ 原样返回；
//   - 未成熟作物且湿润且露天 → 推进一个阶段；
//   - 其余（缺湿或被遮挡）→ 原样返回。
//
// 「推进一个阶段」写成 block+1 是合法的：八个阶段占一段连续的稳定方块编号
// （core/block.go 的 WheatStage0ID..WheatStage7ID），而上面的成熟分支已经保证
// 加一不会越过区间上界。
func growCrop(block core.BlockID, wet, skyExposed bool) (next core.BlockID, changed bool) {
	if !core.IsCrop(block) || block == core.WheatStage7ID {
		return block, false
	}
	if !wet || !skyExposed {
		return block, false
	}
	return block + 1, true
}

// 湿润判定的两个几何常量（spec「耕地的干湿由邻近流体决定并双向转换」）。
const (
	// farmlandWetRadius 是判定湿润时向四周扫的水平切比雪夫距离，4 对应 9×9 的
	// 水平窗口。
	farmlandWetRadius = 4
	// farmlandWetLayersAbove 是除耕地自身所在层外，向上还要扫的层数。取 1 表示
	// 「同层与上一层」两层：水面通常正好铺在耕地那一层或高一格，再往上的水与
	// 耕地之间必然隔着方块或空气，不再算作灌溉。
	farmlandWetLayersAbove = 1
)

// cropSkyExposed 报告 position 上的作物是否露天（design.md D5）。
//
// 判据是 `cropY >= 列顶`，其中列顶取既有的派生高度图 world.Chunk.HighestOpaque
// ——它是"该列最高的非空气方块 Y"，完全由权威方块派生，不进存档、不进 payload。
// **本函数只读高度图，绝不写**。
//
// 作物自己是非空气方块、会抬高自己所在列的列顶，判定依然成立：
//   - 作物就是该列最高的非空气方块时，列顶 == cropY，`>=` 为真；
//   - 作物上方有任何非空气方块（含水）时，列顶 > cropY，为假。
//
// 而作物换阶段不改变"空气 / 非空气"这个属性，所以高度图在整个生长过程中稳定，
// 不会因为长高一阶段就把自己判成被遮挡。
//
// chunk 必须是 position 所属的区块：调用方（advanceCrops）本来就按区块枚举，
// 传进来省掉一次 map 查找。
func cropSkyExposed(chunk *world.Chunk, position core.BlockPos) bool {
	localX, _, localZ := position.Local()
	return position.Y >= chunk.HighestOpaque(localX, localZ)
}

// farmlandIsWet 报告 position 上的耕地是否处于湿润状态：以该格为中心，水平
// 切比雪夫距离 ≤ farmlandWetRadius（9×9）、同层与上一层两层内存在任意流体方块。
//
// **纯查询**：只读 Dimension 的方块，不写任何世界状态，也不碰流体队列。
//
// 相邻区块未加载时按「无水」处理。这是一条刻意的保守约定，方向与流体把范围外
// 的格读作 core.BarrierID（"不可替换"）一致——两者都选择"少发生一点变化"这一侧。
// 后果只是区块边界上的耕地在邻块加载之前可能被判干；邻块加载后它会在下一次被
// 随机 tick 抽中时自动转回湿（干湿是双向的，见 advanceCropCell）。反过来若把
// 未加载读作"可能有水"，就会出现一块永远湿着、谁也说不清水在哪里的耕地。
func farmlandIsWet(dimension *Dimension, position core.BlockPos) bool {
	for dy := int32(0); dy <= farmlandWetLayersAbove; dy++ {
		for dz := int32(-farmlandWetRadius); dz <= farmlandWetRadius; dz++ {
			for dx := int32(-farmlandWetRadius); dx <= farmlandWetRadius; dx++ {
				block, ready := dimension.BlockAt(core.BlockPos{
					X: position.X + dx,
					Y: position.Y + dy,
					Z: position.Z + dz,
				})
				if ready && core.IsFluid(block) {
					return true
				}
			}
		}
	}
	return false
}

// advanceCrops 在单写者权威 tick 中推进活动兴趣范围内的作物与耕地湿度。
//
// 形状与 advanceFluids / advanceFurnaces 一致：只遍历 activeInterestKeys() 里
// State == ChunkReady 且 Chunk != nil 的区块，变更经 recordChange 汇入调用方
// 传入的同一批 pendingChunkChanges（design.md D1：阶段存在方块编号里，因此
// 既有的方块变更广播与存盘天然覆盖，不新增任何协议消息）。tunable 取自
// engine.tunables（本 tick 入口的快照），本函数绝不调用 ActiveTunables()。
//
// **枚举顺序全序、绝不遍历 map**：区块来自 activeInterestKeys()（已按
// chunkKeyLess 排好），区段按索引升序，区段内的 n 条抽样按 i 升序。
// dimension.records[...] 是 map **查找**不是 map 遍历，不引入顺序不确定性。
//
// 成本契约：本 tick 触及的格数恒等于「已就绪的活动区块数 × 24 个区段 ×
// RandomTicksPerSection」，与世界里有多少株作物无关。这里刻意**不**跳过均匀
// 空气区段——那类捷径会让考察量随地形（进而随作物分布）变化，正好破坏 spec
// 「作物数量增加不改变单 tick 考察量」这条契约，而省下的只是几次哈希。
func (engine *Engine) advanceCrops(pending map[core.ChunkKey]*pendingChunkChanges) {
	engine.cropCellsExamined = 0
	samples := int(engine.tunables.RandomTicksPerSection)
	if samples <= 0 {
		return
	}
	tick := engine.tick.Load()
	for _, key := range engine.activeInterestKeys() {
		dimension := engine.dimensions[key.Dimension]
		if dimension == nil {
			continue
		}
		record := dimension.records[key.Pos]
		if record == nil || record.State != ChunkReady || record.Chunk == nil {
			continue
		}
		baseX := key.Pos.X << core.SectionShift
		baseZ := key.Pos.Z << core.SectionShift
		for sectionY := range core.SectionsPerChunk {
			baseY := int32(sectionY<<core.SectionShift) + core.MinY
			engine.cropCellScratch = sampleCells(
				engine.seed, tick, key, sectionY, samples, engine.cropCellScratch,
			)
			for _, cell := range engine.cropCellScratch {
				// 紧凑下标 y*256 + z*16 + x 还原成区段内局部坐标，与
				// world.ChunkBlockIndex 的区段内编号规则一致。
				localX := cell & core.SectionMask
				localZ := (cell >> core.SectionShift) & core.SectionMask
				localY := cell >> (core.SectionShift * 2)
				engine.cropCellsExamined++
				engine.advanceCropCell(dimension, key.Dimension, record.Chunk, core.BlockPos{
					X: baseX + int32(localX),
					Y: baseY + int32(localY),
					Z: baseZ + int32(localZ),
				}, tick, pending)
			}
		}
	}
}

// advanceCropCell 处理一个被随机 tick 抽中的格。
//
// 只有两类方块会被处理，其余一律跳过（绝大多数抽样落在空气或石头上，这是随机
// tick 机制的固有开销，也正是它与作物数量无关的原因）：
//
//   - 作物：算湿润与露天两个环境条件，交给 growCrop 判定是否推进，再由
//     cropGrowthRoll 决定本次是否真的推进。湿润读的是**作物正下方**那一格是否
//     为湿耕地，而不是重扫一遍 9×9——耕地自己的湿度已经由下面那条分支维护好，
//     作物只需要读结果。
//   - 耕地：重判干湿并在需要时双向转换（design.md D6：湿润更新挂在随机 tick，
//     而不是挂 recordChange——挂后者的话，一处水源被移除要立刻更新半径内所有
//     耕地，那个扇出没有上界；挂随机 tick 则天然受同一个抽样预算约束）。
func (engine *Engine) advanceCropCell(
	dimension *Dimension,
	dimensionID core.DimensionID,
	chunk *world.Chunk,
	position core.BlockPos,
	tick uint64,
	pending map[core.ChunkKey]*pendingChunkChanges,
) {
	localX, _, localZ := position.Local()
	block := chunk.BlockAt(localX, position.Y, localZ)
	var next core.BlockID
	switch {
	case core.IsCrop(block):
		below := position
		below.Y--
		belowBlock, ready := dimension.BlockAt(below)
		wet := ready && belowBlock == core.FarmlandWetID
		grown, changed := growCrop(block, wet, cropSkyExposed(chunk, position))
		if !changed {
			return
		}
		if !cropGrowthRoll(
			engine.seed, tick, position, engine.tunables.CropGrowthChancePercent,
		) {
			return
		}
		next = grown
	case core.IsFarmland(block):
		next = core.FarmlandDryID
		if farmlandIsWet(dimension, position) {
			next = core.FarmlandWetID
		}
		if next == block {
			return
		}
	default:
		return
	}
	if _, changed, err := dimension.SetBlock(position, next); err != nil || !changed {
		// 位置来自本区块自身的抽样、区块已就绪，两条失败路径都走不到；真走到
		// 了说明枚举范围与写入范围发生了分歧，此时丢弃这次写入比继续广播一条
		// 没有落地的方块变更安全。
		return
	}
	engine.recordChange(dimensionID, position, next, pending)
}

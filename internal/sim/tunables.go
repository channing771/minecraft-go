package sim

import (
	"sync/atomic"

	"github.com/channing771/mornlea/internal/core"
)

// Tunables 是可在运行时调整的权威模拟参数。
//
// 它按值传递并整体替换。读取方在 tick 入口取一次快照（见 Engine.Step）后全程
// 使用该快照，因此单个 tick 内参数不会中途变化，模拟仍然确定性。写入只做一次
// 原子指针交换，读写之间无锁无竞争。
//
// 只有 cmd 的启动装配与调试面板应当调用 SetTunables。
//
// json tag 与 config.Fields() 的 Name 逐字对应，保证配置文件写出的键名就是
// 设计文档与 README 里写的小写驼峰；读取侧大小写不敏感，加 tag 之前写出的
// 文件仍可正常读入。
type Tunables struct {
	// InteractionReach 是放置、挖掘与开启容器共用的最大交互距离（方块）。
	InteractionReach float32 `json:"interactionReach"`
	// RegenDelayTicks 是最后一次受伤后必须连续经过的 tick 数才进入回复阶段。
	RegenDelayTicks uint32 `json:"regenDelayTicks"`
	// RegenIntervalTicks 是进入回复阶段后，每回复 1 点生命值需要经过的 tick 数。
	RegenIntervalTicks uint32 `json:"regenIntervalTicks"`
	// DrownDamageIntervalTicks 是氧气归零后两次溺水伤害之间的 tick 数
	// （变更 fluid-presentation-survival，internal/sim/oxygen.go 的 advanceOxygen）。
	//
	// 它只决定「多久扣一次血」，不决定「多久开始扣血」——后者由
	// core.MaxOxygenTicks 这个不可调的存档无关常量固定。取 0 会退化成每 tick
	// 扣血，配置层已把下限钳到 1，advanceOxygen 另有一次 max(…, 1) 兜底。
	DrownDamageIntervalTicks uint32 `json:"drownDamageIntervalTicks"`
	// DropPickupDelayTicks 是采掘与方块破坏产生的掉落物可被拾取前的活动 tick 数。
	DropPickupDelayTicks uint8 `json:"dropPickupDelayTicks"`
	// PlayerDropPickupDelayTicks 是玩家主动丢弃或死亡掉落的物品可被拾取前的活动
	// tick 数；它比方块破坏更长，避免刚丢出的物品被自己立刻拾回。
	PlayerDropPickupDelayTicks uint8 `json:"playerDropPickupDelayTicks"`
	// DropLifetimeTicks 是掉落物累计活动 tick 的寿命上限。
	DropLifetimeTicks uint32 `json:"dropLifetimeTicks"`
	// DropPickupRange 是玩家到方块中心的最大拾取距离（方块）。
	DropPickupRange float32 `json:"dropPickupRange"`
	// SpawnRadius 是出生扫描以出生锚点所在列为中心的方形半径（方块）。
	SpawnRadius int32 `json:"spawnRadius"`
	// FurnaceSmeltTicks 是熔炼一格输入所需的进度 tick 数。
	//
	// 只能向下调（让熔炼变快），不能超过 core.FurnaceSmeltTicks（200）：
	// world.FurnaceSlot.Valid() 用编译期常量 core.FurnaceSmeltTicks（而非本字段）
	// 校验 ProgressTicks，区块存盘（internal/storage 的读写）都经过这道校验。
	// 调高本字段会让模拟持久化出 Valid() 拒绝的 ProgressTicks，导致区块存盘失败，
	// 这不是普通的钳制越界，是近数据丢失的故障。上限调整需要先改
	// world.FurnaceSlot.Valid() 的存档契约，不在本字段的调参范围内。
	FurnaceSmeltTicks uint8 `json:"furnaceSmeltTicks"`
	// FurnaceBurnTicks 是单份煤炭燃料提供的燃烧 tick 数。
	//
	// 同 FurnaceSmeltTicks，只能向下调，不能超过 core.FurnaceBurnTicks（1600）：
	// world.FurnaceSlot.Valid() 用编译期常量 core.FurnaceBurnTicks 校验 BurnTicks，
	// 超过会导致该熔炉槽存盘失败。
	FurnaceBurnTicks uint16 `json:"furnaceBurnTicks"`
	// FluidFlowDelayTicks 是流体待更新项从入队到可被处理的延迟 tick 数
	// （变更 authoritative-fluid，internal/fluid.Queue.Advance 的 delay 参数）。
	//
	// 它同时是"水看起来在流动而不是瞬移"的观感来源，也是天然的合并窗口——
	// 同一格在延迟窗口内被重复标记只会合并成一次到期处理。internal/fluid
	// 本身不读取这个值（archcheck 禁止 fluid 依赖 sim），本字段只是快照源，
	// 由 sim 侧在 tick 入口读出后作为调用参数传给 fluid.Queue.Advance——
	// 该接线由 internal/sim/fluid.go 的 fluidClock 在 tick 入口读出后传给
	// fluid.Queue.Advance（任务组 4 已交付）。
	FluidFlowDelayTicks uint32 `json:"fluidFlowDelayTicks"`
	// FluidUpdatesPerTick 是单个权威 tick 内允许处理的流体格数上限
	// （变更 authoritative-fluid，internal/fluid.Queue.Advance 的 budget 参数）。
	//
	// 这是一条硬上界，不是软限流：超出预算的待更新项按 internal/fluid 既定
	// 的全序（lessPos）原样保留到后续 tick 继续处理，绝不丢弃——这正是规格
	// "预算不改变平衡态"（受限预算下最终收敛到与不受限预算相同的平衡态）
	// 这条保证成立的前提。若未来任何改动让超额项被丢弃而不是顺延，平衡态
	// 保证就不再成立。
	//
	// 收敛所需的 tick 数不单调于本预算：受限预算下的收敛速度可能比不受限
	// 更快也可能更慢，取决于水体形状与处理顺序如何与合并窗口相互作用——
	// 实测部分随机形状在 budget=512 下 118 tick 收敛，不受限反而要 126 tick。
	// 因此收敛 tick 数不得被当作性能指标使用，也不能假设调大预算必然让水
	// 更快静止。消费方是 internal/sim/fluid.go 的 advanceFluids（任务组 4 已
	// 交付）。
	FluidUpdatesPerTick uint32 `json:"fluidUpdatesPerTick"`
	// FluidRescanCellsPerTick 是单个权威 tick 内允许用于**边界重扫**的格数
	// 预算（变更 authoritative-fluid，internal/sim/fluid.go 的 advanceFluids）。
	//
	// 它与 FluidUpdatesPerTick 管的是两件不同的事，不能互相替代：
	// FluidUpdatesPerTick 截断的是「处理已在队列里的项」，而区块进入推进范围时
	// 的一次性重扫要先把格**放进**队列，那部分工作完全不受它约束。评审实测过
	// 无预算重扫的后果：八名玩家最坏兴趣范围一次性进入时，单 tick 花 204 ms 做
	// 入队，20 TPS 的 50 ms 预算被直接击穿。
	//
	// 计量单位是「检查过的格数」而不是「入过队的格数」：跳过可证不动点之后，
	// 常态是扫了几千格却一格都没入队（见 fluidSourceIsFixedPoint），按入队数
	// 记账等于不记账。区段级快路径整段跳过时按 1 格记，因为它确实只做 5 次
	// IsUniform。
	//
	// 预算在**区段边界**检查，因此单 tick 最多超支一个区段（4096 格）；换来的
	// 是续扫游标只需记录「第几个平面、第几个区段」两个小整数。
	//
	// 调小它只会让重扫铺开到更多 tick，不会丢掉任何重扫：待重扫区块记在
	// engine.fluidRescan 里跨 tick 保留。这条安全性来自 design.md D5——不动点
	// 性质只要求重扫**最终**发生在该区块处于推进范围内的某个 tick，不要求发生
	// 在它进入范围的那一 tick。区块在重扫完成前离开范围会被整条丢弃并在重新
	// 进入时从头重扫，因此也不会留下"只扫了一半"的区块。
	FluidRescanCellsPerTick uint32 `json:"fluidRescanCellsPerTick"`
}

// 以下是流体三个 tunable 的编译期默认值。它们的消费方都在
// internal/sim/fluid.go 一个文件里（fluidClock 读延迟，advanceFluids 读两条
// 预算），没有各自独立的消费方文件，因此集中定义在这里，而不是像
// defaultRegenDelayTicks 等常量那样分散到各自的消费方文件。
const (
	// defaultFluidFlowDelayTicks 见 Tunables.FluidFlowDelayTicks 的字段说明。
	defaultFluidFlowDelayTicks = 5
	// defaultFluidUpdatesPerTick 见 Tunables.FluidUpdatesPerTick 的字段说明。
	defaultFluidUpdatesPerTick = 512
	// defaultFluidRescanCellsPerTick 见 Tunables.FluidRescanCellsPerTick 的
	// 字段说明。取 65536（16 个满区段）的依据是实测：跳过不动点后逐格检查一格
	// 约 24 ns，65536 格约合 1.6 ms，在 20 TPS 的 50 ms tick 预算里占约 3%，
	// 与流体处理本身（budget=512）叠加后仍留出充足余量；同时它足以让日常路径
	// 一 tick 扫完——走路跨一次区块边界新进入约 5 个区块，满水时合计约 5 万格。
	// 最坏情况（八名玩家兴趣范围一次性进入，约 200 区块、约 230 万格）需要约
	// 35 tick（1.7 秒）扫完，期间水只是晚一点开始流动，没有正确性后果。
	defaultFluidRescanCellsPerTick = 65536
)

// DefaultTunables 返回编译期默认参数。它是配置文件缺省时的取值，
// 也是调试面板“重置”的目标值。
func DefaultTunables() Tunables {
	return Tunables{
		InteractionReach:           defaultInteractionReach,
		RegenDelayTicks:            defaultRegenDelayTicks,
		RegenIntervalTicks:         defaultRegenIntervalTicks,
		DrownDamageIntervalTicks:   defaultDrownDamageIntervalTicks,
		DropPickupDelayTicks:       defaultDropPickupDelayTicks,
		PlayerDropPickupDelayTicks: defaultPlayerDropPickupDelayTicks,
		DropLifetimeTicks:          defaultDropLifetimeTicks,
		DropPickupRange:            defaultDropPickupRange,
		SpawnRadius:                defaultSpawnRadius,
		FurnaceSmeltTicks:          core.FurnaceSmeltTicks,
		FurnaceBurnTicks:           core.FurnaceBurnTicks,
		FluidFlowDelayTicks:        defaultFluidFlowDelayTicks,
		FluidUpdatesPerTick:        defaultFluidUpdatesPerTick,
		FluidRescanCellsPerTick:    defaultFluidRescanCellsPerTick,
	}
}

var active atomic.Pointer[Tunables]

func init() {
	defaults := DefaultTunables()
	active.Store(&defaults)
}

// SetTunables 整体替换生效参数。新参数从下一次 Engine.Step 起生效（引擎在
// tick 入口取一次快照），可以从任意 goroutine 调用。
//
// 后置条件：写入的快照一定满足 RegenIntervalTicks >= 1 且
// SpawnRadius ∈ [minSpawnRadius, maxSpawnRadius]，越界入参被钳制而不是被拒绝。
//
// 这两条不是重复劳动。advanceHealthRegen 拿 RegenIntervalTicks 当取模除数，
// 0 会在权威 tick 内 panic；spawnCandidates 按 (SpawnRadius*2+1)² 分配切片，
// 不钳制会触发巨额分配。internal/config 在加载时按同一区间钳制过一遍，但
// archcheck 禁止 sim 导入 config，那道钳制是隔着一个包、靠约定维持的——
// 拥有这两条不变量的是本包，兜底就必须落在本包。
func SetTunables(tunables Tunables) {
	if tunables.RegenIntervalTicks < 1 {
		tunables.RegenIntervalTicks = 1
	}
	tunables.SpawnRadius = min(max(tunables.SpawnRadius, minSpawnRadius), maxSpawnRadius)
	active.Store(&tunables)
}

// ActiveTunables 返回当前生效参数的快照。
func ActiveTunables() Tunables { return *active.Load() }

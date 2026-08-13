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
}

// DefaultTunables 返回编译期默认参数。它是配置文件缺省时的取值，
// 也是调试面板“重置”的目标值。
func DefaultTunables() Tunables {
	return Tunables{
		InteractionReach:           defaultInteractionReach,
		RegenDelayTicks:            defaultRegenDelayTicks,
		RegenIntervalTicks:         defaultRegenIntervalTicks,
		DropPickupDelayTicks:       defaultDropPickupDelayTicks,
		PlayerDropPickupDelayTicks: defaultPlayerDropPickupDelayTicks,
		DropLifetimeTicks:          defaultDropLifetimeTicks,
		DropPickupRange:            defaultDropPickupRange,
		SpawnRadius:                defaultSpawnRadius,
		FurnaceSmeltTicks:          core.FurnaceSmeltTicks,
		FurnaceBurnTicks:           core.FurnaceBurnTicks,
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

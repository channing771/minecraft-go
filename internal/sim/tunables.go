package sim

import (
	"sync/atomic"

	"minecraft-go/internal/core"
)

// Tunables 是可在运行时调整的权威模拟参数。
//
// 它按值传递并整体替换。读取方在 tick 入口取一次快照（见 Engine.Step）后全程
// 使用该快照，因此单个 tick 内参数不会中途变化，模拟仍然确定性。写入只做一次
// 原子指针交换，读写之间无锁无竞争。
//
// 只有 cmd 的启动装配与调试面板应当调用 SetTunables。
type Tunables struct {
	// InteractionReach 是放置、挖掘与开启容器共用的最大交互距离（方块）。
	InteractionReach float32
	// RegenDelayTicks 是最后一次受伤后必须连续经过的 tick 数才进入回复阶段。
	RegenDelayTicks uint32
	// RegenIntervalTicks 是进入回复阶段后，每回复 1 点生命值需要经过的 tick 数。
	RegenIntervalTicks uint32
	// DropPickupDelayTicks 是采掘与方块破坏产生的掉落物可被拾取前的活动 tick 数。
	DropPickupDelayTicks uint8
	// PlayerDropPickupDelayTicks 是玩家主动丢弃或死亡掉落的物品可被拾取前的活动
	// tick 数；它比方块破坏更长，避免刚丢出的物品被自己立刻拾回。
	PlayerDropPickupDelayTicks uint8
	// DropLifetimeTicks 是掉落物累计活动 tick 的寿命上限。
	DropLifetimeTicks uint32
	// DropPickupRange 是玩家到方块中心的最大拾取距离（方块）。
	DropPickupRange float32
	// SpawnRadius 是出生扫描以出生锚点所在列为中心的方形半径（方块）。
	SpawnRadius int32
	// FurnaceSmeltTicks 是熔炼一格输入所需的进度 tick 数。
	//
	// 只能向下调（让熔炼变快），不能超过 core.FurnaceSmeltTicks（200）：
	// world.FurnaceSlot.Valid() 用编译期常量 core.FurnaceSmeltTicks（而非本字段）
	// 校验 ProgressTicks，区块存盘（internal/storage 的读写）都经过这道校验。
	// 调高本字段会让模拟持久化出 Valid() 拒绝的 ProgressTicks，导致区块存盘失败，
	// 这不是普通的钳制越界，是近数据丢失的故障。上限调整需要先改
	// world.FurnaceSlot.Valid() 的存档契约，不在本字段的调参范围内。
	FurnaceSmeltTicks uint8
	// FurnaceBurnTicks 是单份煤炭燃料提供的燃烧 tick 数。
	//
	// 同 FurnaceSmeltTicks，只能向下调，不能超过 core.FurnaceBurnTicks（1600）：
	// world.FurnaceSlot.Valid() 用编译期常量 core.FurnaceBurnTicks 校验 BurnTicks，
	// 超过会导致该熔炉槽存盘失败。
	FurnaceBurnTicks uint16
}

// DefaultTunables 返回编译期默认参数。它是配置文件缺省时的取值，
// 也是调试面板“重置”的目标值。
func DefaultTunables() Tunables {
	return Tunables{
		InteractionReach:           interactionReach,
		RegenDelayTicks:            RegenDelayTicks,
		RegenIntervalTicks:         RegenIntervalTicks,
		DropPickupDelayTicks:       DropPickupDelayTicks,
		PlayerDropPickupDelayTicks: PlayerDropPickupDelayTicks,
		DropLifetimeTicks:          DropLifetimeTicks,
		DropPickupRange:            dropPickupRange,
		SpawnRadius:                spawnRadius,
		FurnaceSmeltTicks:          core.FurnaceSmeltTicks,
		FurnaceBurnTicks:           core.FurnaceBurnTicks,
	}
}

var active atomic.Pointer[Tunables]

func init() {
	defaults := DefaultTunables()
	active.Store(&defaults)
}

// SetTunables 整体替换生效参数。
func SetTunables(tunables Tunables) { active.Store(&tunables) }

// ActiveTunables 返回当前生效参数的快照。
func ActiveTunables() Tunables { return *active.Load() }

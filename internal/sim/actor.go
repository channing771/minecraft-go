package sim

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// actorState 是玩家与伙伴两类 actor 共有的状态：物理体、本 tick 控制输入、
// 朝向、背包与采掘进度。两者的身体记录本就同构，提取共有字段后，伙伴得以汇入
// 与玩家完全相同的 Rust physics.Step 积分出口与 miningRule 采掘状态机，而不
// 需要第二套积分或计时实现。
//
// 提取范围刻意最小化：生命、重生与玩家输入序号留在 playerState，稳定
// CompanionID 与激活状态留在 companionState。playerState 与 companionState 以
// 匿名内嵌方式复用本结构体，字段经提升访问，禁止在子结构体重复声明遮蔽（由
// TestActorStateExtractionKeepsPlayerBehavior 锁定）。
type actorState struct {
	state physics.State
	input physics.Input
	yaw   float32
	pitch float32
	// inventory 与 inventoryDirty 是共有的权威背包。玩家侧由命令阶段写并逐 tick
	// 发布；伙伴侧由 MineHold 采掘完成（产物直入）与 Place 结算（扣一件并写
	// 方块）写入并置 inventoryDirty，其余时刻仅随恢复/存档往返。
	inventory      core.Inventory
	inventoryDirty bool
	// miningHeld 与 mining 是 M5C 上移的共有采掘状态：按住意图与进度状态机。
	// 玩家的意图来自输入命令（Command.Mining），伙伴的意图来自 MineHold/
	// MineRelease action（伙伴专属的意图目标记录在 companionState.miningTarget，
	// 玩家目标由视线 raycast 逐 tick 派生，不需要持久化字段）；两者的进度语义
	// 完全一致，由 stepMiningProgress 单点推进，完成分叉只差产物去向。
	miningHeld bool
	mining     miningState
}

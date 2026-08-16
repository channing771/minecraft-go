package sim

import (
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// actorState 是玩家与伙伴两类 actor 共有的运动状态：物理体、本 tick 控制输入、
// 朝向与背包。两者的身体记录本就同构，提取共有字段后，伙伴得以汇入与玩家完全
// 相同的 Rust physics.Step 积分出口，而不需要第二套积分实现。
//
// 提取范围刻意最小化（M5B 只需要移动）：生命、重生与玩家输入序号留在
// playerState，稳定 CompanionID 与激活状态留在 companionState；采掘与交互校验
// 共享留给首次需要它们的里程碑。playerState 与 companionState 以匿名内嵌方式
// 复用本结构体，字段经提升访问，禁止在子结构体重复声明遮蔽（由
// TestActorStateExtractionKeepsPlayerBehavior 锁定）。
type actorState struct {
	state physics.State
	input physics.Input
	yaw   float32
	pitch float32
	// inventory 与 inventoryDirty 是共有的权威背包。玩家侧由命令阶段写并逐 tick
	// 发布；伙伴侧 M5B 尚无背包交互，仅随恢复/存档往返，但字段属于两类 actor
	// 同构的身体记录，随提取一起上移。
	inventory      core.Inventory
	inventoryDirty bool
}

package sim

import (
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/physics"
)

// CompanionAction 是 Task Runner 在 tick 边界提交的伙伴移动意图，按 CompanionID
// 寻址。action 刻意不携带 SessionID 或任何玩家会话身份——伙伴独立于玩家会话语义，
// 这条约束由结构体的字段面（ID + 规范移动输入）在编译期保证，并由
// TestCompanionActionInboxBoundedAndSessionless 反射锁定。go_to 等任务只能经由
// 这里提交规范移动输入，实际位移永远由权威物理决定。
type CompanionAction struct {
	ID    companion.ID
	Input physics.Input
}

// EnqueueCompanionAction 可由 Task Runner 并发调用，把一个伙伴 action 投递进有界
// inbox。容量按 companion.MaxActive（每 tick 每伙伴最多一个 action）定容，满员时
// 立即丢弃并返回 false，绝不阻塞权威 tick，也不像玩家命令 inbox 一样无界累积——
// 丢弃是安全的：位置始终权威，下一 tick 重新提交即可。
func (engine *Engine) EnqueueCompanionAction(action CompanionAction) bool {
	engine.inboxMu.Lock()
	defer engine.inboxMu.Unlock()
	if len(engine.companionActions) >= companion.MaxActive {
		return false
	}
	engine.companionActions = append(engine.companionActions, action)
	return true
}

// takeCompanionActions 与其他 inbox 一起在 Step 入口同一把锁内完成 tick 边界
// 排空；返回的切片此后只被权威 tick 单写者触碰。
func (engine *Engine) takeCompanionActions() []CompanionAction {
	engine.inboxMu.Lock()
	actions := append([]CompanionAction(nil), engine.companionActions...)
	engine.companionActions = engine.companionActions[:0]
	engine.inboxMu.Unlock()
	return actions
}

// validCompanionActionInput 与玩家输入校验同界：移动分量必须在 [-1,1]，yaw 必须
// 有限。action 来自服务端 Task Runner 而非网络，这里仍是防御性校验——
// physics.Step 对非法输入直接 panic，权威 tick 绝不能被坏 action 打崩。
func validCompanionActionInput(input physics.Input) bool {
	return input.MoveX >= -1 && input.MoveX <= 1 &&
		input.MoveZ >= -1 && input.MoveZ <= 1 &&
		finiteInputComponent(input.Yaw)
}

// applyCompanionActions 是权威 tick 的伙伴 action 阶段，必须位于玩家命令阶段
// 之后、统一物理推进之前（由 stepPhaseObserver 顺序测试与突变验证锁定）。
//
// 每个 active 伙伴每 tick 至多应用一个 action：按入队顺序取该 ID 最早的一个合法
// action，重复或非法 action 确定性丢弃；未知 ID 或未激活伙伴的 action 同样丢弃，
// 不产生任何会话副作用。没有收到 action 的 active 伙伴写中性输入（仅保留当前
// yaw），与玩家未按键时的步进语义一致：重力与碰撞照常生效，无任务伙伴在地面
// 保持静止。中性输入每 tick 重写，伙伴输入因此不像玩家输入那样跨 tick 保持。
func (engine *Engine) applyCompanionActions(actions []CompanionAction) {
	var inputs map[companion.ID]physics.Input
	if len(actions) != 0 {
		// 容量上限是 companion.MaxActive，这里的临时表不在热路径上放大分配。
		inputs = make(map[companion.ID]physics.Input, len(actions))
		for _, action := range actions {
			if _, duplicate := inputs[action.ID]; duplicate {
				continue
			}
			if !validCompanionActionInput(action.Input) {
				continue
			}
			inputs[action.ID] = action.Input
		}
	}
	for _, id := range engine.activeCompanionIDs() {
		entry := engine.companions[id]
		if input, ok := inputs[id]; ok {
			yaw := normalizeYaw(input.Yaw)
			entry.input = physics.Input{
				MoveX: input.MoveX,
				MoveZ: input.MoveZ,
				Jump:  input.Jump,
				Yaw:   yaw,
			}
			entry.yaw = yaw
		} else {
			entry.input = physics.Input{Yaw: entry.yaw}
		}
	}
}

// advanceActiveCompanions 把所有 active 伙伴汇入与玩家相同的 Rust physics.Step
// 积分出口：每个伙伴用 action 阶段写入的输入步进恰好一次，位移完全由权威物理
// 决定，不新写任何 Go 积分。伙伴状态来源全部经过校验（恢复/出生候选或上一次
// physics.Step 输出），永远有限，因此不需要玩家的非有限状态复位路径；卡入方块
// 的解除与越界复位属于玩家生命周期语义，M5B 伙伴保持最小实现。
//
// 脚下区块变化时置 subscriptionsDirty，让 3×3 兴趣在同一 tick 的 reconcile 中
// 滑动到新中心：新增区块走既有 acquire/generate/persistence 流程，离开的区块按
// 既有规则释放。
func (engine *Engine) advanceActiveCompanions() {
	for _, id := range engine.activeCompanionIDs() {
		entry := engine.companions[id]
		before := companionChunk(entry.state.Position)
		step := physics.Step(
			entry.state,
			entry.input,
			dimensionCollisionSource{dimension: engine.dimensions[entry.dimension]},
		)
		entry.state = step.State
		if companionChunk(entry.state.Position) != before {
			engine.subscriptionsDirty = true
		}
	}
}

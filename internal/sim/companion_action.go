package sim

import (
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// CompanionActionKind 标识伙伴 action 的四种判别载荷之一。零值刻意不是合法
// 载荷：未经初始化的 action 必须被确定性丢弃，而不是被当作移动静默应用。
type CompanionActionKind uint8

const (
	// CompanionActionMove 是既有规范移动输入载荷。
	CompanionActionMove CompanionActionKind = iota + 1
	// CompanionActionMineHold 是采掘按住载荷，携带目标 BlockPos，语义与玩家
	// Mining:true 输入一致：进度由 sim 依 miningRule 累积。
	CompanionActionMineHold
	// CompanionActionMineRelease 是采掘释放载荷，同 tick 清空采掘意图与进度。
	CompanionActionMineRelease
	// CompanionActionPlace 是放置载荷，携带目标 BlockPos 与方块；action 阶段
	// 记录单次放置意图，结算本体（校验链 + 原子扣料写方块）在同一 tick 的
	// settleCompanionPlacements 完成。
	CompanionActionPlace
)

// CompanionAction 是 Task Runner 在 tick 边界提交的伙伴意图，按 CompanionID
// 寻址。action 刻意不携带 SessionID 或任何玩家会话身份——伙伴独立于玩家会话语义，
// 这条约束由结构体的字段面（ID + 判别载荷）在编译期保证，并由
// TestCompanionActionInboxBoundedAndSessionless 反射锁定。go_to 等任务只能经由
// 这里提交规范移动输入或采掘/放置意图，实际位移与方块变更永远由权威模拟决定。
type CompanionAction struct {
	ID   companion.ID
	Kind CompanionActionKind
	// Input 只在 Kind == CompanionActionMove 时有意义（规范移动输入）。
	Input physics.Input
	// Target 是 MineHold 与 Place 载荷的目标方块坐标。
	Target core.BlockPos
	// Block 是 Place 载荷要写入的方块。
	Block core.BlockID
}

// EnqueueCompanionAction 可由 Task Runner 并发调用，把一个伙伴 action 投递进有界
// inbox。容量按全局计：每 tick 至多 companion.MaxActive 条（active 伙伴至多
// MaxActive 个、每伙伴每 tick 至多应用一条 action），满员时立即丢弃并返回
// false，绝不阻塞权威 tick，也不像玩家命令 inbox 一样无界累积——丢弃是安全
// 的：位置始终权威，下一 tick 重新提交即可。注意同一伙伴重复提交的 action
// 同样占用全局名额，极端情况下可占满 inbox 并饿死其他伙伴本 tick 的 action
// （权威侧按 ID 去重后每个伙伴只消费最早一条合法 action）。
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

// validCompanionActionTarget 校验载荷目标坐标：X/Z 可以是任意有限列（跨区块
// 目标由采掘推进的距离校验兜底），Y 必须落在世界高度范围内，越界目标确定性
// 丢弃。
func validCompanionActionTarget(target core.BlockPos) bool {
	return target.Y >= core.MinY && target.Y < core.MaxY
}

// validCompanionAction 按判别载荷做防御校验：Move 校验移动输入，MineHold 校验
// 目标坐标，MineRelease 无载荷约束，Place 额外要求方块已注册且不是空气
// （放置空气没有意义；可放置性、目标为空气、碰撞判定与扣料等真实放置校验链
// 在同一 tick 的 settleCompanionPlacements/completeCompanionPlacement 完成，
// 其行为由 companion_placement_test.go 锁定）。
func validCompanionAction(action CompanionAction) bool {
	switch action.Kind {
	case CompanionActionMove:
		return validCompanionActionInput(action.Input)
	case CompanionActionMineHold, CompanionActionPlace:
		if !validCompanionActionTarget(action.Target) {
			return false
		}
		if action.Kind == CompanionActionPlace {
			return action.Block != core.AirID && core.RegisteredBlock(action.Block)
		}
		return true
	case CompanionActionMineRelease:
		return true
	default:
		return false
	}
}

// applyCompanionActions 是权威 tick 的伙伴 action 阶段，必须位于玩家命令阶段
// 之后、统一物理推进之前（由 stepPhaseObserver 顺序测试与突变验证锁定）。
//
// 每个 active 伙伴每 tick 至多应用一个 action：按入队顺序取该 ID 最早的一个合法
// action，重复或非法 action 确定性丢弃；未知 ID 或未激活伙伴的 action 同样丢弃，
// 不产生任何会话副作用。Move 写入本 tick 移动输入；MineHold/MineRelease 置/清
// 共享的采掘意图（实际进度在物理阶段之后的 advanceMining 统一累积）；Place 记录
// 单次放置意图并交由 settleCompanionPlacements 在同一 tick 的区块写入区结算
// （放置写方块必须晚于 reconcileSubscriptions，见那里的阶段顺序契约注释）。
// 非 Move 载荷与无 action 的伙伴一样写中性输入（仅保留当前 yaw）：重力与碰撞照常
// 生效，无任务伙伴在地面保持静止。中性输入每 tick 重写，伙伴输入因此不像玩家
// 输入那样跨 tick 保持；采掘意图例外——它与玩家的按住语义一致，跨 tick 保持直到
// Release；放置意图没有进度语义，只随本 tick 存活。
//
// 返回值是本 tick 收集的放置意图，按 CompanionID 字节序排列（active 快照本身
// 有序），由 Step 在写入区结算后即丢弃。
func (engine *Engine) applyCompanionActions(actions []CompanionAction) []companionPlaceIntent {
	var intents map[companion.ID]CompanionAction
	var placements []companionPlaceIntent
	if len(actions) != 0 {
		// 容量上限是 companion.MaxActive，这里的临时表不在热路径上放大分配。
		intents = make(map[companion.ID]CompanionAction, len(actions))
		for _, action := range actions {
			if _, duplicate := intents[action.ID]; duplicate {
				continue
			}
			if !validCompanionAction(action) {
				continue
			}
			intents[action.ID] = action
		}
	}
	for _, id := range engine.activeCompanionIDs() {
		entry := engine.companions[id]
		action, ok := intents[id]
		if !ok {
			entry.input = physics.Input{Yaw: entry.yaw}
			continue
		}
		switch action.Kind {
		case CompanionActionMove:
			yaw := normalizeYaw(action.Input.Yaw)
			entry.input = physics.Input{
				MoveX: action.Input.MoveX,
				MoveZ: action.Input.MoveZ,
				Jump:  action.Input.Jump,
				Yaw:   yaw,
			}
			entry.yaw = yaw
		case CompanionActionMineHold:
			entry.input = physics.Input{Yaw: entry.yaw}
			entry.miningHeld = true
			entry.miningTarget = action.Target
		case CompanionActionMineRelease:
			entry.input = physics.Input{Yaw: entry.yaw}
			entry.miningHeld = false
		case CompanionActionPlace:
			// 放置没有进度语义：action 阶段只记录意图并保持中性输入，结算本体
			// （校验链 + 原子扣料写方块）在 settleCompanionPlacements 完成。
			entry.input = physics.Input{Yaw: entry.yaw}
			placements = append(placements, companionPlaceIntent{
				id: id, target: action.Target, block: action.Block,
			})
		}
	}
	return placements
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

package sim

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// exhaustionOf 读出一名玩家当前的三层饥饿状态，供「精确不变」类断言整体比对。
func exhaustionOf(player *playerState) [3]uint32 {
	return [3]uint32{
		uint32(player.hunger),
		uint32(player.saturationMilli),
		uint32(player.exhaustionMilli),
	}
}

// TestSwimExhaustionMilliFixedPointRounding 钉住游泳位移换算的取整规则。
//
// 位移本身是 float32（物理体的位置），但疲劳状态是整数：换算是**唯一**允许出现
// 浮点的地方，且只在这里做一次截断。规则是 `floor(距离 × 1000 × 每格疲劳) / 1000`，
// 等价于「向下取整到 1 千分位疲劳」——先放大再整除，余数一律丢弃，绝不四舍五入
// （四舍五入会让「原地抖动」的玩家凭空积累疲劳）。
func TestSwimExhaustionMilliFixedPointRounding(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after mgl32.Vec3
		want          uint16
	}{
		{"原地不动", mgl32.Vec3{1, 2, 3}, mgl32.Vec3{1, 2, 3}, 0},
		{"只有垂直位移", mgl32.Vec3{1, 2, 3}, mgl32.Vec3{1, 9, 3}, 0},
		{"整格 X 位移", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1, 0, 0}, 10},
		{"整格 Z 反向位移", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, -1}, 10},
		{"半格位移", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0.5, 0, 0}, 5},
		{"两格半位移", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0, 0, 2.5}, 25},
		{"恰好一千分位", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0.1, 0, 0}, 1},
		{"不足一千分位向下取整", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0.09999, 0, 0}, 0},
		{"斜向位移只算水平分量", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{0.3, 100, 0.4}, 5},
		{"极端位移钳到 uint16 上界", mgl32.Vec3{0, 0, 0}, mgl32.Vec3{1e6, 0, 0}, 65535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := swimExhaustionMilli(tc.before, tc.after); got != tc.want {
				t.Fatalf("swimExhaustionMilli(%v→%v) = %d，想要 %d",
					tc.before, tc.after, got, tc.want)
			}
		})
	}
}

// TestJumpAccumulatesExhaustionExactlyOncePerTakeoff 覆盖 Scenario「跳跃累积
// 疲劳」：起跳那一 tick 恰好累积一次跳跃疲劳，滞空期间**一点都不再加**。
//
// 断言是精确值 50 而不是「大于 0」：后者在「每滞空 tick 都加 50」的实现下同样
// 成立。滞空段的重复检查正是这条用例的承重部分。
func TestJumpAccumulatesExhaustionExactlyOncePerTakeoff(t *testing.T) {
	const id = SessionID(51)
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	player := engine.sessions[id].player
	if !player.state.OnGround {
		t.Fatalf("夹具玩家不在地面上: %+v", player.state)
	}
	if player.exhaustionMilli != 0 {
		t.Fatalf("夹具起始疲劳=%d，想要 0", player.exhaustionMilli)
	}

	player.input.Jump = true
	engine.Step()
	if player.state.OnGround {
		t.Fatal("按下跳跃后玩家仍在地面上，夹具没能起跳")
	}
	if player.exhaustionMilli != exhaustionJumpMilli {
		t.Fatalf("起跳后疲劳=%d，想要 %d", player.exhaustionMilli, exhaustionJumpMilli)
	}

	// 滞空期间继续按住跳跃：不在地面上就不可能再起跳一次。
	for range 5 {
		engine.Step()
		if player.exhaustionMilli != exhaustionJumpMilli {
			t.Fatalf("滞空 tick 疲劳=%d，想要保持 %d（一次起跳只算一次）",
				player.exhaustionMilli, exhaustionJumpMilli)
		}
	}
}

// TestWalkingOnFlatGroundAccumulatesNoExhaustion 覆盖 Scenario「平地行走不累积
// 疲劳」：持续行走 60 tick，三层状态必须**逐字段**保持初值。
//
// 断言整个三元组而不只是疲劳值：只看疲劳会漏掉「行走扣饱和度」这一类错误接线。
func TestWalkingOnFlatGroundAccumulatesNoExhaustion(t *testing.T) {
	const id = SessionID(52)
	engine := readyRegenPlayer(t, id, core.MaxHealth)
	player := engine.sessions[id].player
	before := exhaustionOf(player)
	startPosition := player.state.Position

	player.input.MoveZ = -1
	for range 60 {
		engine.Step()
	}
	// 夹具自证：玩家确实走动了。原地不动的夹具在"行走累积疲劳"的错误实现下
	// 也会读出零疲劳，那样这条用例就成了空转。
	moved := player.state.Position.Sub(startPosition)
	if horizontal := (mgl32.Vec2{moved.X(), moved.Z()}); horizontal.Len() < 1 {
		t.Fatalf("玩家几乎没有移动: %v → %v", startPosition, player.state.Position)
	}
	if got := exhaustionOf(player); got != before {
		t.Fatalf("平地行走后三层状态=%v，想要保持 %v", got, before)
	}
}

// TestSwimmingAccumulatesExhaustionAndStandingStillDoesNot 覆盖疲劳表的游泳项。
//
// 三条分支共用同一个泡在水里的夹具，只差输入与是否有水：
//   - 水中移动：疲劳必须严格增长；
//   - 水中不动：疲劳必须**精确**保持 0（这一条杀死「只要浸没就加疲劳」的实现）；
//   - 陆上移动：疲劳必须精确保持 0（这一条杀死「移动就加疲劳」的实现）。
//
// 移动分支断言的是「大于 0」而不是字面值：精确值取决于物理步的实际位移，
// 写死它只会变成 physics.Step 的第二份实现。位移到疲劳的换算规则由
// TestSwimExhaustionMilliFixedPointRounding 逐条钉死。
func TestSwimmingAccumulatesExhaustionAndStandingStillDoesNot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		submerged bool
		moveZ     int8
		wantMoved bool
	}{
		{"水中移动累积", true, -1, true},
		{"水中静止不累积", true, 0, false},
		{"陆上移动不累积", false, -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const id = SessionID(53)
			engine := readyRegenPlayer(t, id, core.MaxHealth)
			player := engine.sessions[id].player
			if tc.submerged {
				floodAroundPlayer(t, engine, player.state.Position)
				// 夹具自证：负向分支（水中静止不累积）如果夹具压根没把玩家泡
				// 进水里，读数同样是零疲劳，用例就成了空转。
				source := dimensionCollisionSource{dimension: engine.dimensions[core.Overworld]}
				if body, _ := physics.SubmersionFlags(player.state.Position, source); !body {
					t.Fatalf("夹具没能把玩家泡进水里: 位置=%v", player.state.Position)
				}
			}
			player.exhaustionMilli = 0
			player.input.MoveZ = tc.moveZ
			for range 20 {
				engine.Step()
			}
			if tc.wantMoved {
				if player.exhaustionMilli == 0 {
					t.Fatal("水中移动没有累积任何疲劳")
				}
				return
			}
			if player.exhaustionMilli != 0 {
				t.Fatalf("疲劳=%d，想要精确保持 0", player.exhaustionMilli)
			}
		})
	}
}

// floodAroundPlayer 把玩家周围 5×5 水平、身体高度上下各两格全部填成水源方块。
// 只用水源（不用流动等级）是刻意的：水源是流体推进的不动点，重扫与流动都不会
// 改动它，夹具因此在整段推进里保持稳定。
func floodAroundPlayer(t *testing.T, engine *Engine, position mgl32.Vec3) {
	t.Helper()
	center := core.BlockPos{
		X: int32(position.X()), Y: int32(position.Y()), Z: int32(position.Z()),
	}
	for dy := int32(0); dy <= 3; dy++ {
		for dz := int32(-2); dz <= 2; dz++ {
			for dx := int32(-2); dx <= 2; dx++ {
				engine.SetBlockForTest(core.BlockPos{
					X: center.X + dx, Y: center.Y + dy, Z: center.Z + dz,
				}, core.WaterSourceID)
			}
		}
	}
}

// TestMiningCompletionAccumulatesExhaustionOnlyOnSuccess 覆盖 Scenario
// 「采掘完成累积疲劳」及其否定面：进度累积期间不加，完成那一 tick 恰好加一次，
// 被拒绝或中途松手一点都不加。
func TestMiningCompletionAccumulatesExhaustionOnlyOnSuccess(t *testing.T) {
	t.Run("完成累积一次", func(t *testing.T) {
		engine, sessions, targets := readyMiningPlayers(t, 1)
		player := engine.sessions[sessions[0]].player
		engine.SetBlockForTest(targets[0], core.StoneID)
		setMiningHeldItem(player, core.ItemNone)
		before := exhaustionOf(player)

		// 裸手采石需要 30 tick；第 29 tick 结束时进度未满，疲劳必须一字不变。
		for range 29 {
			advanceMiningOnce(engine)
		}
		if got := exhaustionOf(player); got != before {
			t.Fatalf("进度未满时三层状态=%v，想要保持 %v（拒绝/中断/未完成都不累积）", got, before)
		}
		advanceMiningOnce(engine)
		if player.exhaustionMilli != uint16(before[2])+exhaustionMiningMilli {
			t.Fatalf("完成后疲劳=%d，想要 %d",
				player.exhaustionMilli, uint16(before[2])+exhaustionMiningMilli)
		}
	})

	t.Run("被拒绝不累积", func(t *testing.T) {
		engine, sessions, targets := readyMiningPlayers(t, 1)
		player := engine.sessions[sessions[0]].player
		// 掉落槽已满：进度照常累积到满，完成分叉却返回 RejectDropCapacity。
		// 这条路径**走到了**完成分叉又被拒绝，正是「拒绝不累积」要守的形状；
		// 换成受保护方块（基岩）反而测不出来——那种方块连进度都不会累积。
		engine.SetBlockForTest(targets[0], core.LightBlockID)
		setMiningHeldItem(player, core.ItemStonePickaxe)
		fillMiningDrops(engine, targets[0])
		before := exhaustionOf(player)

		var result TickResult
		for range 15 {
			result = advanceMiningOnce(engine)
		}
		if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectDropCapacity {
			t.Fatalf("Rejected=%+v，想要恰好一条 RejectDropCapacity", result.Rejected)
		}
		if got := exhaustionOf(player); got != before {
			t.Fatalf("被拒绝的采掘后三层状态=%v，想要保持 %v", got, before)
		}
	})

	t.Run("中途松手不累积", func(t *testing.T) {
		engine, sessions, targets := readyMiningPlayers(t, 1)
		player := engine.sessions[sessions[0]].player
		engine.SetBlockForTest(targets[0], core.StoneID)
		setMiningHeldItem(player, core.ItemNone)
		before := exhaustionOf(player)

		for range 29 {
			advanceMiningOnce(engine)
		}
		player.miningHeld = false
		for range 30 {
			advanceMiningOnce(engine)
		}
		if got := exhaustionOf(player); got != before {
			t.Fatalf("松手后三层状态=%v，想要保持 %v", got, before)
		}
	})
}

// TestTillCompletionAccumulatesExhaustionOnlyOnSuccess 覆盖疲劳表的翻地项：
// 成功翻地恰好累积一次，被拒绝的翻地一点都不累积。
//
// 拒绝路径与「拒绝不磨损锄头」是同一条不变量的两个面：唯一的写入区在六道校验
// 之后，疲劳与耐久一起被压在那里。
func TestTillCompletionAccumulatesExhaustionOnlyOnSuccess(t *testing.T) {
	stoneHoeFull, _ := core.ItemMaxDurability(core.ItemStoneHoe)
	hoe := core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: stoneHoeFull}

	t.Run("翻地成功累积一次", func(t *testing.T) {
		engine, session, yaw, pitch := readyTillPlayer(t, hoe, core.GrassID, core.AirID)
		player := engine.sessions[session].player
		before := exhaustionOf(player)

		result := till(engine, session, yaw, pitch)
		if len(result.Rejected) != 0 {
			t.Fatalf("翻地被拒绝: %+v", result.Rejected)
		}
		if got := tillBlockAt(t, engine, tillTarget); got != core.FarmlandDryID {
			t.Fatalf("翻地后方块=%d，想要干耕地", got)
		}
		if player.exhaustionMilli != uint16(before[2])+exhaustionTillMilli {
			t.Fatalf("翻地后疲劳=%d，想要 %d",
				player.exhaustionMilli, uint16(before[2])+exhaustionTillMilli)
		}
	})

	t.Run("被拒绝不累积", func(t *testing.T) {
		// 目标是石头：六道校验的第四道（必须是泥土或草）当场拒绝。
		engine, session, yaw, pitch := readyTillPlayer(t, hoe, core.StoneID, core.AirID)
		player := engine.sessions[session].player
		before := exhaustionOf(player)

		result := till(engine, session, yaw, pitch)
		if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectInvalidBlock {
			t.Fatalf("Rejected=%+v，想要恰好一条 RejectInvalidBlock", result.Rejected)
		}
		if got := exhaustionOf(player); got != before {
			t.Fatalf("被拒绝的翻地后三层状态=%v，想要保持 %v", got, before)
		}
	})
}

// TestCompanionPathsNeverTouchHungerState 是「伙伴不接饥饿」的**运行时**守卫：
// 在同一个引擎里同时跑一名玩家和一名伙伴，让伙伴走完整条采掘路径并持续移动，
// 玩家的三层状态必须逐字段一字不变。
//
// 为什么不断言「companionState 没有三层字段」：那是存在性断言，编译期就恒真，
// 在「有人把疲劳表接进伙伴路径」的世界里同样成立（伙伴完全可以去改别人的
// playerState）。这里断言的是位置性事实——伙伴的动作**没有任何途径**改到饥饿状态。
func TestCompanionPathsNeverTouchHungerState(t *testing.T) {
	engine, sessions, _ := readyMiningPlayers(t, 1)
	player := engine.sessions[sessions[0]].player
	player.miningHeld = false
	before := exhaustionOf(player)

	// 伙伴采掘：目标与玩家的目标同区块但另一格，走完整条完成结算（产物直入
	// 背包）。装配方式与既有 TestCompanionMiningMatchesPlayerRuleAndTiming 同形。
	id := companionTestID(1)
	activateCompanionAt(t, engine, id, mgl32.Vec3{4.5, 1, 8.5})
	entry := engine.companions[id]
	companionTarget := core.BlockPos{X: 4, Y: 1, Z: 5}
	engine.SetBlockForTest(companionTarget, core.CoalOreID)
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	entry.inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: full,
	}
	entry.inventory.Hotbar.Selected = 0
	entry.miningTarget = companionTarget
	entry.miningHeld = true
	for range 15 {
		advanceMiningOnce(engine)
	}
	if got := companionItemCount(entry, core.ItemCoal); got != 1 {
		t.Fatalf("伙伴没有完成采掘，夹具空转: coal=%d", got)
	}

	// 伙伴移动：走完整条权威积分出口（含跳跃输入与水平位移）。
	entry.miningHeld = false
	entry.input.Jump = true
	entry.input.MoveZ = -1
	for range 40 {
		engine.Step()
	}

	if got := exhaustionOf(player); got != before {
		t.Fatalf("伙伴的采掘与移动改动了玩家三层状态: %v，想要保持 %v", got, before)
	}
}

// TestExhaustionTableIsNotWiredIntoCompanionCode 是「伙伴不接饥饿」的**源码**
// 守卫，与上一条运行时守卫互补：任何以 *companionState 为接收者、或者函数名
// 里带 Companion 的 internal/sim 生产函数，都不得提到疲劳表的任何一个标识符。
//
// 运行时守卫只能覆盖被夹具驱动到的那几条伙伴路径；这条守卫覆盖的是「有人把
// applyExhaustion 写进任意一条伙伴路径」这件事本身，包括还没有测试驱动到的路径。
func TestExhaustionTableIsNotWiredIntoCompanionCode(t *testing.T) {
	banned := map[string]bool{
		"applyExhaustion":               true,
		"advanceStarvation":             true,
		"resetHunger":                   true,
		"swimExhaustionMilli":           true,
		"exhaustionJumpMilli":           true,
		"exhaustionSwimMilliPerBlock":   true,
		"exhaustionMiningMilli":         true,
		"exhaustionTillMilli":           true,
		"exhaustionRegenPerHealthMilli": true,
		"hunger":                        true,
		"saturationMilli":               true,
		"exhaustionMilli":               true,
		"starvationTicks":               true,
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("枚举 internal/sim 的 Go 文件: %v", err)
	}
	// Glob 对不存在的目录静默返回空：包被改名或移动后本守卫会静默失效。
	if len(files) == 0 {
		t.Fatal("internal/sim 下没有 Go 源文件，本守卫会静默失效")
	}
	scanned := 0
	for _, path := range files {
		if filepath.Ext(path) != ".go" || len(path) > 8 && path[len(path)-8:] == "_test.go" {
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !companionScopedFunction(function) {
				continue
			}
			scanned++
			ast.Inspect(function, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if !ok || !banned[identifier.Name] {
					return true
				}
				t.Errorf("%s: 伙伴路径提到了饥饿标识符 %s。伙伴没有饥饿也没有任何疲劳来源；"+
					"要给伙伴接饥饿必须先更新 spec 与本守卫，而不是顺手加一行",
					fileSet.Position(identifier.Pos()), identifier.Name)
				return true
			})
		}
	}
	// 自证：扫描必须真的命中过伙伴函数，否则这条守卫是空循环。
	if scanned < 10 {
		t.Fatalf("只扫到 %d 个伙伴作用域函数，守卫覆盖面可疑", scanned)
	}
}

// companionScopedFunction 报告一个函数声明是否属于伙伴作用域：接收者是
// *companionState，或者函数名里带 Companion。
func companionScopedFunction(function *ast.FuncDecl) bool {
	if function.Recv != nil {
		for _, field := range function.Recv.List {
			expression := field.Type
			if star, ok := expression.(*ast.StarExpr); ok {
				expression = star.X
			}
			if identifier, ok := expression.(*ast.Ident); ok &&
				identifier.Name == "companionState" {
				return true
			}
		}
	}
	name := function.Name.Name
	for index := 0; index+9 <= len(name); index++ {
		if name[index:index+9] == "Companion" {
			return true
		}
	}
	return false
}

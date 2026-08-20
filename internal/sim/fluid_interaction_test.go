package sim

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// eyeBlockOf 返回玩家眼睛所在的方块坐标，与权威射线的起点取同一个 EyeHeight。
// 夹具承重守卫用它复核「水真的在射线起点那一格」，而不是照抄一个手算常量。
func eyeBlockOf(t *testing.T, engine *Engine, session SessionID) core.BlockPos {
	t.Helper()
	player, ok := engine.Player(session)
	if !ok {
		t.Fatalf("会话 %d 没有玩家", session)
	}
	eye := player.State.Position.Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	return core.BlockPos{
		X: int32(math.Floor(float64(eye.X()))),
		Y: int32(math.Floor(float64(eye.Y()))),
		Z: int32(math.Floor(float64(eye.Z()))),
	}
}

// TestMiningRaycastLooksThroughFluid 钉死「泡在水里仍能瞄准并采掘水后面的方块」。
//
// 夹具必须真的把水放在射线路径上，否则改对改错读数完全相同：水源写在眼睛所在
// 格（射线起点，DDA 第一格）与其后的两格路径上，全部**避开采掘目标的六邻**，
// 因此采掘完成时的 recordChange 不会唤醒它们——它们在整个测试期间保持静止，
// 断言不受流动影响。
func TestMiningRaycastLooksThroughFluid(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	session, target := sessions[0], targets[0]

	// 射线自 (0.5,2.62,8.5) 沿 yaw=0 / pitch=-0.4 走向 (0,1,5)，依次穿过
	// (0,2,8)、(0,2,7)、(0,1,7)、(0,1,6) 才到达目标。取前三格灌水：
	// (0,1,6) 是目标的面邻格，灌水会在采掘完成后被唤醒并流动，刻意不用。
	water := []core.BlockPos{
		{X: 0, Y: 2, Z: 8},
		{X: 0, Y: 2, Z: 7},
		{X: 0, Y: 1, Z: 7},
	}
	for _, position := range water {
		engine.SetBlockForTest(position, core.WaterSourceID)
	}

	for range 200 {
		engine.Step()
		if fluidBlockAt(t, engine, target) != core.StoneID {
			break
		}
	}
	if got := fluidBlockAt(t, engine, target); got != core.AirID {
		t.Fatalf("水下采掘目标 %+v=%d，想要被挖成空气 %d", target, got, core.AirID)
	}

	// 夹具承重守卫排在真实断言之后：真实故障不应先被守卫报成「夹具失效」。
	eye := eyeBlockOf(t, engine, session)
	if got := fluidBlockAt(t, engine, eye); !core.IsFluid(got) {
		t.Fatalf("夹具失效：眼睛所在格 %+v=%d 不是流体，射线根本没穿过水", eye, got)
	}
	for _, position := range water {
		if got := fluidBlockAt(t, engine, position); !core.IsFluid(got) {
			t.Fatalf("夹具失效：射线路径上的 %+v=%d 已不是流体", position, got)
		}
	}
}

// fluidChannelZ 是放置测试用的一格宽水渠：x=0、y=1、z∈[1,9]，水源在 z=9，
// 向 -Z 逐级流出 1..7 级流动水。
//
// 渠壁：x=-1 与 z=-1 属于区块 {-1,0}/{0,-1}，它们不在推进范围内，fluidWorld
// 一律读作 core.BarrierID（不可替换），天然就是墙；x=1 那一侧必须自己砌石头，
// 否则水会横向摊成一片，z 方向的等级链就不再是唯一支撑路径，切断中段也不会
// 让下游变干——夹具会退化成恒真。
const (
	fluidChannelSourceZ = int32(9)
	fluidChannelMinZ    = int32(1)
)

func buildFluidChannel(engine *Engine) {
	for z := fluidChannelMinZ; z <= fluidChannelSourceZ; z++ {
		engine.SetBlockForTest(core.BlockPos{X: 1, Y: 1, Z: z}, core.StoneID)
	}
	source := core.BlockPos{X: 0, Y: 1, Z: fluidChannelSourceZ}
	engine.SetBlockForTest(source, core.WaterSourceID)
	// SetBlockForTest 绕过 recordChange，因此不会入队；显式唤醒水源，让它按
	// 正常规则流出整条水渠。
	engine.enqueueFluidUpdate(core.Overworld, source)
}

// TestPlaceBlockThroughFluidReplacesFluidAndRewakesNeighbours 钉死放置的三件事：
//
//  1. 射线穿过水命中水下的固体，落点是**水下那格**而不是贴着水面；
//  2. 落点原本是流体不算「被占用」，放置成功后那格的水消失；
//  3. 落点的相邻流体被重新入队——下游那格失去支撑后必须变干，否则水面会留下
//     一个不会被填回的洞。
func TestPlaceBlockThroughFluidReplacesFluidAndRewakesNeighbours(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	buildFluidChannel(engine)
	player := engine.sessions[session].player
	player.inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStone, Count: core.MaxStackCount,
	}

	for range 200 {
		engine.Step()
	}
	target := core.BlockPos{X: 0, Y: 1, Z: 3}
	downstream := core.BlockPos{X: 0, Y: 1, Z: 2}
	before := fluidBlockAt(t, engine, target)
	beforeDownstream := fluidBlockAt(t, engine, downstream)

	// 俯视 yaw=Pi（+Z）、pitch≈-0.4949：射线自 (0.5,2.62,0.5) 掠过 (0,1,1)、
	// (0,1,2)、(0,1,3) 三格水，从上方进入草地 (0,0,3)，落点因此是 (0,1,3)。
	engine.Enqueue(Command{
		Session: session, Sequence: 2, Kind: CommandPlaceBlock,
		Yaw: float32(math.Pi), Pitch: -0.4949, Slot: 0,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("穿水放置被拒绝: %+v", result.Rejected)
	}
	if got := fluidBlockAt(t, engine, target); got != core.StoneID {
		t.Fatalf("放置落点 %+v=%d，想要 %d（射线应穿过水命中水下地面）", target, got, core.StoneID)
	}

	for range 200 {
		engine.Step()
	}
	if got := fluidBlockAt(t, engine, downstream); got != core.AirID {
		t.Fatalf("放置切断水路后下游 %+v=%d，想要变干成 %d（相邻流体没有被重新入队）",
			downstream, got, core.AirID)
	}
	if got := fluidBlockAt(t, engine, core.BlockPos{X: 0, Y: 1, Z: 4}); !core.IsFluid(got) {
		t.Fatalf("放置点上游 %+v=%d，想要仍是流体", core.BlockPos{X: 0, Y: 1, Z: 4}, got)
	}

	// 夹具承重守卫（排在真实断言之后）：放置前两格必须真的是流动水，否则
	// 「落点变石头」「下游变干」在没有水的世界里同样成立，断言恒绿。
	if !core.IsFluid(before) {
		t.Fatalf("夹具失效：放置落点 %+v 放置前是 %d，不是流体", target, before)
	}
	if !core.IsFluid(beforeDownstream) {
		t.Fatalf("夹具失效：下游 %+v 放置前是 %d，不是流体", downstream, beforeDownstream)
	}
}

// fluidLookDown 是近乎垂直向下的 pitch（留 0.01 弧度避免退化成极点）。
const fluidLookDown = -float32(math.Pi)/2 + 0.01

// fluidRayPathCells 用**生产 DDA** 枚举射线在交互距离内经过的全部方块格
// （谓词恒假，因此射线不会提前停下），供用例把水精确地灌在射线路径上。
//
// 为什么不手算路径格：路径依赖 EyeHeight 与 InteractionReach 两个 tunable，
// 手算的常量在它们变化后会静默失真，「穿水交互」就退化成「水根本不在路上」
// 的恒真断言——而这正是本文件其余守卫要排除的失效模式。
func fluidRayPathCells(
	t *testing.T,
	engine *Engine,
	origin, direction mgl32.Vec3,
) []core.BlockPos {
	t.Helper()
	var cells []core.BlockPos
	_, _, err := core.RaycastBlocks(
		origin,
		direction,
		engine.tunables.InteractionReach,
		func(position core.BlockPos) (bool, error) {
			cells = append(cells, position)
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("枚举射线路径失败：%v", err)
	}
	return cells
}

// floodRayPathWithWater 把射线路径上除 target 之外的**空气**格全部写成水源，
// 返回实际灌水的格。用水源而非流动水是刻意的：源是流动规则的不动点，整个
// 用例期间不会自行消失；SetBlockForTest 又绕过 recordChange，因此这些格连
// 入队都不会发生，断言不受流动影响。
func floodRayPathWithWater(
	t *testing.T,
	engine *Engine,
	origin, direction mgl32.Vec3,
	target core.BlockPos,
) []core.BlockPos {
	t.Helper()
	var water []core.BlockPos
	for _, cell := range fluidRayPathCells(t, engine, origin, direction) {
		if cell == target || fluidBlockAt(t, engine, cell) != core.AirID {
			continue
		}
		engine.SetBlockForTest(cell, core.WaterSourceID)
		water = append(water, cell)
	}
	if len(water) == 0 {
		t.Fatal("夹具失效：射线路径上没有任何可灌水的空气格")
	}
	return water
}

// assertWaterIsLoadBearing 是两条水下交互用例共用的夹具承重守卫：用「非空气
// 即命中」这个**不区分流体**的谓词跑同一条射线，它必须停在水格而不是 target。
//
// 这一条守卫同时证明两件事：水确实落在射线路径上，且生产谓词把水判为可穿透
// 是被真实测量的行为。少了它，即便水被放到了射线旁边，主断言也会照样通过。
func assertWaterIsLoadBearing(
	t *testing.T,
	engine *Engine,
	origin, direction mgl32.Vec3,
	target core.BlockPos,
	water []core.BlockPos,
) {
	t.Helper()
	for _, position := range water {
		if got := fluidBlockAt(t, engine, position); !core.IsFluid(got) {
			t.Fatalf("夹具失效：射线路径上的 %+v=%d 已不是流体", position, got)
		}
	}
	dimension := engine.dimensions[core.Overworld]
	hit, ok, err := core.RaycastBlocks(
		origin,
		direction,
		engine.tunables.InteractionReach,
		func(position core.BlockPos) (bool, error) {
			block, ready := dimension.BlockAt(position)
			if !ready {
				return false, ErrChunkNotReady
			}
			return block != core.AirID, nil
		},
	)
	if err != nil || !ok {
		t.Fatalf("夹具失效：不区分流体的射线没有命中任何方块（err=%v ok=%v）", err, ok)
	}
	if hit.Block == target {
		t.Fatalf("夹具失效：不区分流体的射线也命中了目标 %+v，水没有落在射线路径上", target)
	}
	if !core.IsFluid(fluidBlockAt(t, engine, hit.Block)) {
		t.Fatalf("夹具失效：不区分流体的射线停在非流体格 %+v，途中另有遮挡", hit.Block)
	}
}

// TestOpenContainerRaycastLooksThroughFluid 覆盖 spec fluid-survival 的 Scenario
// 「浸没时仍能开启容器」：视线经过流体格后指向交互距离内的熔炉，打开请求必须
// 被受理，MUST NOT 因为途中的流体被判为无目标。
//
// 本用例与 TestMiningRaycastLooksThroughFluid 走同一个 blockRaycastSampler，
// 但**共用是当前的实现事实，不是契约**：只要采掘那一条有用例，任何人把
// container.go 的采样器重新内联成一份不区分流体的谓词，都不会让任何测试变红。
// 这条 Scenario 因此需要自己的钉子。
func TestOpenContainerRaycastLooksThroughFluid(t *testing.T) {
	engine, session := readyMovementPlayer(t)
	// 玩家出生在 (0.5,1,0.5) 的平地上，眼睛在 y = 1 + EyeHeight；lookDown 近乎
	// 垂直向下，因此把熔炉放在脚下那格，射线必须穿过身体所在的空气格才够得着。
	furnace := core.BlockPos{}
	engine.SetBlockForTest(furnace, core.FurnaceID)
	index, indexed := world.ChunkBlockIndex(furnace)
	if !indexed {
		t.Fatal("熔炉没有区块索引")
	}
	engine.SetChunkFurnaceForTest(core.ChunkKey{Dimension: core.Overworld}, 0,
		world.FurnaceSlot{Generation: 1, Active: true, BlockIndex: index})

	origin := engine.sessions[session].player.state.Position.
		Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	direction := LookDirection(0, fluidLookDown)
	water := floodRayPathWithWater(t, engine, origin, direction, furnace)

	engine.Enqueue(Command{
		Session: session, Sequence: 2, Kind: CommandOpenFurnace, Pitch: fluidLookDown,
	})
	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("穿水打开熔炉被拒绝：%+v", result.Rejected)
	}
	if len(result.Furnaces) != 1 || result.Furnaces[0].Session != session {
		t.Fatalf("穿水打开后未发布熔炉状态：%+v", result.Furnaces)
	}

	// 夹具承重守卫排在真实断言之后：真实故障不应先被守卫报成「夹具失效」。
	assertWaterIsLoadBearing(t, engine, origin, direction, furnace, water)
}

// TestCompanionMiningLineOfSightLooksThroughFluid 覆盖 spec fluid-survival 的
// Scenario「流体不遮挡伙伴视线」：伙伴与采掘目标之间只隔着流体时，视线遮挡
// 校验 MUST NOT 因为流体判定目标被遮挡。
//
// 伙伴采掘不看朝向，射线方向由目标方块中心决定（mining.go）；遮挡一旦成立，
// advanceCompanionMining 会把进度清零，因此「进度逐 tick 累积」就是「视线没有
// 被水挡住」的可判定证据。
func TestCompanionMiningLineOfSightLooksThroughFluid(t *testing.T) {
	fixture := readyCompanionMining(t, core.StoneID, core.ItemStonePickaxe)
	engine, entry, target := fixture.engine, fixture.entry, fixture.target

	origin := entry.state.Position.
		Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	direction := blockCenterVec3(target).Sub(origin)
	water := floodRayPathWithWater(t, engine, origin, direction, target)

	for tick := 1; tick <= 3; tick++ {
		advanceMiningOnce(engine)
		if got := entry.mining.progressTicks; got != uint16(tick) {
			t.Fatalf("tick %d 伙伴进度=%d，想要 %d——水把视线挡住了", tick, got, tick)
		}
	}

	// 夹具承重守卫排在真实断言之后。
	assertWaterIsLoadBearing(t, engine, origin, direction, target, water)
}

package sim

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
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

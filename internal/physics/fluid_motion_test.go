package physics_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// fluidTestGround 是无限平地：y=0 一整层实心，其余全是已加载的空气。
type fluidTestGround struct{}

func (fluidTestGround) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position.Y == 0 {
		return physics.CollisionBoxSet{Loaded: true, Count: 1, Boxes: [8]core.AABB{fullCube}}
	}
	return physics.CollisionBoxSet{Loaded: true}
}

// TestFluidSinkIsSlowerThanAirFreeFall 覆盖 spec Scenario「水中下沉慢于空气中
// 自由落体」：同一时长内水中下落距离不得超过空气中，且垂直速度收敛到有界终端值。
//
// 夹具时长刻意取 40 tick（2 秒）：空气侧此时尚未触到 78.4 的终端速度、仍在加速，
// 水中侧早已钳在 FluidSinkSpeed，两条曲线相差一个量级，不会落进浮点噪声里。
func TestFluidSinkIsSlowerThanAirFreeFall(t *testing.T) {
	const start = float32(400)
	const ticks = 40
	tunables := physics.DefaultTunables()
	air := physics.State{Position: mgl32.Vec3{0.5, start, 0.5}}
	water := air
	var airDrop, waterDrop float32
	for tick := range ticks {
		air = physics.Step(air, physics.Input{}, emptySource{}).State
		water = physics.Step(water, physics.Input{BodyInFluid: true}, emptySource{}).State
		airDrop = start - air.Position.Y()
		waterDrop = start - water.Position.Y()
		if waterDrop > airDrop {
			t.Fatalf("tick %d：水中下落 %f 超过空气中 %f", tick, waterDrop, airDrop)
		}
		if water.Velocity.Y() < -tunables.FluidSinkSpeed {
			t.Fatalf("tick %d：水中垂直速度 %f 越过终端值 %f", tick, water.Velocity.Y(), -tunables.FluidSinkSpeed)
		}
		if tick == 0 {
			// 首个 tick 两边都还没触到各自的终端速度，垂直速度就是各自的
			// gravity*dt——只有在这里才能把「重力衰减」与「终端速度压低」分开
			// 观察。少了这一条，把水中重力改回空气重力仍然会因终端速度而全绿。
			if water.Velocity.Y() <= air.Velocity.Y() {
				t.Fatalf("首 tick 水中垂直速度 %f 未快于空气中 %f：水中重力没有衰减",
					water.Velocity.Y(), air.Velocity.Y())
			}
		}
	}
	if water.Velocity.Y() != -tunables.FluidSinkSpeed {
		t.Fatalf("水中垂直速度=%f，想要收敛到 %f", water.Velocity.Y(), -tunables.FluidSinkSpeed)
	}
	// 夹具前提守卫排在真实断言之后：两种规则必须落在明显分歧的区域，否则
	// 上面的「MUST NOT 超过」在两侧读数几乎相等时也会全绿而什么都没测到。
	if waterDrop*4 > airDrop {
		t.Fatalf("夹具无效：水中下落 %f 与空气中 %f 差距不足 4 倍，断言不承重", waterDrop, airDrop)
	}
}

// TestFluidAscendKeepsRisingUntilOutOfFluid 覆盖 spec Scenario「持续按住上升键
// 可浮出水面」：每 tick 的上升位移恒定（因此不是一次性冲量），并最终脱离流体。
func TestFluidAscendKeepsRisingUntilOutOfFluid(t *testing.T) {
	tunables := physics.DefaultTunables()
	// 水面在 y=4.0（方块 0..3 是水）；玩家脚底从 y=1 起步，完全没入。
	world := fluidLayers(0, 1, 2, 3)
	state := physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}}
	if body, eye := physics.SubmersionFlags(state.Position, world); !body || !eye {
		t.Fatalf("夹具起点 body/eye=%v/%v，想要 true/true", body, eye)
	}
	wantRise := tunables.FluidAscendSpeed * physics.FixedDeltaSeconds
	rises := make([]float32, 0, 64)
	escaped := false
	for range 64 {
		body, _ := physics.SubmersionFlags(state.Position, world)
		if !body {
			escaped = true
			break
		}
		previous := state.Position.Y()
		state = physics.Step(state, physics.Input{Jump: true, BodyInFluid: true}, emptySource{}).State
		rises = append(rises, state.Position.Y()-previous)
	}
	if !escaped {
		t.Fatalf("持续上升 64 tick 仍未脱离流体，最终 y=%f", state.Position.Y())
	}
	for tick, rise := range rises {
		// 冲量语义下这些位移会逐 tick 被重力吃掉直至转为下降；持续上浮下它们恒等。
		if math.Abs(float64(rise-wantRise)) > 1e-5 {
			t.Fatalf("tick %d 上升位移=%f，想要恒为 %f（不得表现为一次性冲量）", tick, rise, wantRise)
		}
	}
	// 夹具前提守卫排在真实断言之后：tick 数太少时「冲量」与「持续上浮」的前
	// 几步读数相同，上面的恒等断言不承重。
	if len(rises) < 8 {
		t.Fatalf("夹具无效：只上升了 %d tick，不足以区分冲量与持续上浮", len(rises))
	}
}

// TestFluidHorizontalSpeedStaysBelowGroundSpeed 覆盖 spec Scenario「水中水平
// 移动更慢」：同样在平地上、同样的输入，水中达到的水平速度不得超过陆地上的。
func TestFluidHorizontalSpeedStaysBelowGroundSpeed(t *testing.T) {
	const ticks = 120
	horizontalSpeed := func(bodyInFluid bool) float32 {
		state := physics.State{Position: mgl32.Vec3{0.5, 1, 0.5}, OnGround: true}
		for range ticks {
			state = physics.Step(
				state,
				physics.Input{MoveX: 1, BodyInFluid: bodyInFluid},
				fluidTestGround{},
			).State
			// 无限平地上位置无关紧要，回到原点避免走出任何夹具边界。
			state.Position = mgl32.Vec3{0.5, state.Position.Y(), 0.5}
		}
		return float32(math.Hypot(float64(state.Velocity.X()), float64(state.Velocity.Z())))
	}
	ground := horizontalSpeed(false)
	water := horizontalSpeed(true)
	if water > ground {
		t.Fatalf("水中水平速度 %f 超过平地上的 %f", water, ground)
	}
	// 夹具前提守卫排在真实断言之后：两侧都必须已经到达各自稳态，否则
	// 「加速中途取样」会让两个读数相同而断言恒真。
	if ground < physics.DefaultTunables().WalkSpeed-1e-4 {
		t.Fatalf("夹具无效：平地水平速度 %f 尚未收敛到 WalkSpeed", ground)
	}
	if water > ground*0.95 {
		t.Fatalf("夹具无效：水中 %f 与平地 %f 差距不足 5%%，断言不承重", water, ground)
	}
}

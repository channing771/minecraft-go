package client

import (
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

func TestApplyPlayerStateReplaysOnlyUnacknowledgedInputs(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 3, Control{MoveZ: 1})
	authority := network.PlayerState{
		ServerTick:        8,
		LastInputSequence: 1,
		Dimension:         core.Overworld,
		Position:          mgl32.Vec3{0.5, 1, 0.4},
		Yaw:               1.1,
		Pitch:             -0.4,
		OnGround:          true,
		Ready:             true,
	}

	result, err := p.ApplyPlayerState(authority, flatClientWorld{})
	if err != nil {
		t.Fatal(err)
	}
	if result != (ReconcileResult{}) {
		t.Fatalf("普通和解错误地请求重置视角: %+v", result)
	}
	if p.HistoryLen() != 2 || p.history[0].sequence != 2 || p.history[1].sequence != 3 {
		t.Fatalf("history=%+v，想要只保留 sequence 2,3", p.history)
	}

	want := physics.State{
		Position: authority.Position,
		Velocity: authority.Velocity,
		OnGround: authority.OnGround,
	}
	for range 2 {
		want = physics.Step(want, physics.Input{MoveZ: 1}, flatClientWorld{}).State
	}
	got, _ := p.State()
	assertStateNear(t, got, want, 1e-6)
}

func TestApplyPlayerStateUpdatesHealthWithoutPrediction(t *testing.T) {
	p := readyPredictor(t)
	if health, ready := p.Health(); !ready || health != 0 {
		t.Fatalf("readyPredictor 初始 health=(%d,%v)，想要 (0,true)", health, ready)
	}

	advanceSteps(t, p, 2, Control{MoveX: 1})
	first := nextAuthority(p)
	first.Health = 9
	if _, err := p.ApplyPlayerState(first, flatClientWorld{}); err != nil {
		t.Fatalf("ApplyPlayerState(health=9): %v", err)
	}
	if health, ready := p.Health(); !ready || health != 9 {
		t.Fatalf("普通和解后 health=(%d,%v)，想要 (9,true)", health, ready)
	}

	advanceSteps(t, p, 2, Control{MoveX: 1})
	second := nextAuthority(p)
	second.Health = 3
	if _, err := p.ApplyPlayerState(second, flatClientWorld{}); err != nil {
		t.Fatalf("ApplyPlayerState(health=3): %v", err)
	}
	if health, ready := p.Health(); !ready || health != 3 {
		t.Fatalf("第二次和解后 health=(%d,%v)，想要 (3,true)，生命值不得被预测/插值", health, ready)
	}
}

func TestApplyPlayerStateIgnoresStaleAndEqualTicks(t *testing.T) {
	for _, test := range []struct {
		name string
		tick uint64
	}{{name: "stale", tick: 6}, {name: "equal", tick: 7}} {
		t.Run(test.name, func(t *testing.T) {
			p := readyPredictor(t)
			advanceSteps(t, p, 2, Control{MoveX: 1})
			p.accumulator = physics.FixedDelta / 2
			p.displayOffset = mgl32.Vec3{0.1, 0.2, 0.3}
			p.correctionRemaining = 75 * time.Millisecond
			before := clonePredictor(p)

			result, err := p.ApplyPlayerState(network.PlayerState{
				ServerTick:        test.tick,
				LastInputSequence: p.maxSentInput,
				Dimension:         core.Overworld,
				Position:          mgl32.Vec3{100, 100, 100},
				Yaw:               1,
				Pitch:             0.5,
				Ready:             false,
			}, flatClientWorld{})
			if err != nil || result != (ReconcileResult{}) {
				t.Fatalf("stale tick=%d result=%+v err=%v", test.tick, result, err)
			}
			assertPredictorSame(t, p, before)
		})
	}
}

func TestApplyPlayerStateAcceptsCanonicalMiningWithoutPredictingIt(t *testing.T) {
	p := readyPredictor(t)
	state := nextAuthority(p)
	state.MiningActive = true
	state.MiningTarget = core.BlockPos{X: 1, Y: 2, Z: 3}
	state.MiningProgressTicks = 6
	state.MiningRequiredTicks = 15
	state.MiningHarvestable = true
	if err := state.Validate(); err != nil {
		t.Fatalf("测试 PlayerState 非法: %v", err)
	}
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatalf("ApplyPlayerState(active mining): %v", err)
	}
	got, ready := p.State()
	want := physics.State{Position: state.Position, Velocity: state.Velocity, OnGround: state.OnGround}
	if !ready {
		t.Fatal("规范采掘状态使 Predictor 退出 Ready")
	}
	assertStateNear(t, got, want, 1e-6)

	inactive := nextAuthority(p)
	if err := inactive.Validate(); err != nil {
		t.Fatalf("测试 inactive PlayerState 非法: %v", err)
	}
	if _, err := p.ApplyPlayerState(inactive, flatClientWorld{}); err != nil {
		t.Fatalf("ApplyPlayerState(inactive mining): %v", err)
	}
}

func TestInvalidPlayerStateIsRejectedAtomically(t *testing.T) {
	invalid := []struct {
		name   string
		mutate func(*network.PlayerState, *Predictor)
	}{
		{name: "position NaN", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Position[0] = float32(math.NaN())
		}},
		{name: "velocity Inf", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Velocity[2] = float32(math.Inf(1))
		}},
		{name: "yaw NaN", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Yaw = float32(math.NaN())
		}},
		{name: "pitch Inf", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Pitch = float32(math.Inf(-1))
		}},
		{name: "pitch above limit", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Pitch = float32(math.Pi / 2)
		}},
		{name: "unknown dimension", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Dimension = core.DimensionID(99)
		}},
		{name: "ack beyond sent input", mutate: func(state *network.PlayerState, p *Predictor) {
			state.LastInputSequence = p.maxSentInput + 1
		}},
		{name: "reset while not ready", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Reset = true
			state.Ready = false
		}},
		{name: "health above max", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Health = core.MaxHealth + 1
		}},
		{name: "hunger above max", mutate: func(state *network.PlayerState, _ *Predictor) {
			state.Hunger = core.MaxHunger + 1
		}},
	}

	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			p := readyPredictor(t)
			advanceSteps(t, p, 2, Control{MoveZ: 1})
			p.accumulator = physics.FixedDelta / 2
			p.displayOffset = mgl32.Vec3{0.1, 0, 0}
			p.correctionRemaining = 75 * time.Millisecond
			state := nextAuthority(p)
			test.mutate(&state, p)
			before := clonePredictor(p)

			result, err := p.ApplyPlayerState(state, flatClientWorld{})
			if err == nil {
				t.Fatal("ApplyPlayerState 接受了非法状态")
			}
			if result != (ReconcileResult{}) {
				t.Fatalf("非法状态返回 result=%+v", result)
			}
			assertPredictorSame(t, p, before)
		})
	}
}

func TestApplyPlayerStateReadyFalseClearsPredictionAndRemembersTick(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 2, Control{MoveX: 1})
	p.accumulator = physics.FixedDelta / 2
	p.suspended = true
	p.suspendSequence = p.maxSentInput
	p.suspendInputSent = true
	p.displayOffset = mgl32.Vec3{0.2, 0, 0}
	p.correctionRemaining = 50 * time.Millisecond
	p.health = 12
	state := nextAuthority(p)
	state.ServerTick = 11
	state.LastInputSequence = 1
	state.Ready = false
	state.Health = 7

	result, err := p.ApplyPlayerState(state, flatClientWorld{})
	if err != nil {
		t.Fatal(err)
	}
	if result != (ReconcileResult{}) {
		t.Fatalf("Ready=false result=%+v", result)
	}
	if health, healthReady := p.Health(); healthReady || health != 0 {
		t.Fatalf("Ready=false 未清空生命值: health=(%d,%v)，想要 (0,false)", health, healthReady)
	}
	got, ready := p.State()
	if ready || got != (physics.State{}) || p.previous != (physics.State{}) ||
		p.HistoryLen() != 0 || p.accumulator != 0 || p.suspended ||
		p.suspendInputSent || p.suspendSequence != 0 ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 ||
		p.lastServerTick != 11 {
		t.Fatalf("Ready=false 未清空预测: predictor=%+v", p)
	}
	if position, ok := p.PresentationPosition(time.Second); ok || position != (mgl32.Vec3{}) {
		t.Fatalf("未就绪仍有展示位置: position=%v ok=%v", position, ok)
	}
}

func TestApplyPlayerStateFirstReadySnapsAndResetsView(t *testing.T) {
	p := NewPredictor()
	state := readyPlayerState()
	state.ServerTick = 1
	state.Yaw = -1.25
	state.Pitch = 0.35

	result, err := p.ApplyPlayerState(state, flatClientWorld{})
	if err != nil {
		t.Fatal(err)
	}
	wantResult := ReconcileResult{ResetView: true, Yaw: state.Yaw, Pitch: state.Pitch}
	if result != wantResult {
		t.Fatalf("first Ready result=%+v，想要 %+v", result, wantResult)
	}
	got, ready := p.State()
	want := physics.State{Position: state.Position, Velocity: state.Velocity, OnGround: true}
	if !ready {
		t.Fatal("first Ready 后仍未就绪")
	}
	assertStateNear(t, got, want, 1e-6)
	displayed, ok := p.PresentationPosition(0)
	if !ok || !displayed.ApproxEqualThreshold(state.Position, 1e-6) {
		t.Fatalf("first Ready 未 snap: displayed=%v ok=%v", displayed, ok)
	}
}

func TestApplyPlayerStateResetAndDimensionChangeSnapAndResetView(t *testing.T) {
	t.Run("Reset", func(t *testing.T) {
		p := readyPredictor(t)
		advanceSteps(t, p, 2, Control{MoveX: 1})
		state := nextAuthority(p)
		state.Reset = true
		state.Position = mgl32.Vec3{8, 9, 10}
		state.Yaw = -0.7
		state.Pitch = 0.25
		assertResetState(t, p, state)
	})

	t.Run("dimension change", func(t *testing.T) {
		p := readyPredictor(t)
		advanceSteps(t, p, 2, Control{MoveX: 1})
		p.dimension = core.DimensionID(1)
		state := nextAuthority(p)
		state.Dimension = core.Overworld
		state.Position = mgl32.Vec3{4, 5, 6}
		state.Yaw = 0.8
		state.Pitch = -0.2
		assertResetState(t, p, state)
	})
}

// TestApplyPlayerStateUpdatesHungerWithoutPrediction 覆盖协议 v24 的客户端
// 半边：饥饿值是纯镜像值——只由权威 `network.PlayerState` 写入，客户端不做
// 任何预测或插值。
//
// 与生命值那条同形，两次和解取两个不同的非零非满值：只看一次的话，「镜像
// 端写死初值」与「镜像端确实透传了权威字段」读数相同。
func TestApplyPlayerStateUpdatesHungerWithoutPrediction(t *testing.T) {
	p := readyPredictor(t)
	if hunger, ready := p.Hunger(); !ready || hunger != 0 {
		t.Fatalf("readyPredictor 初始 hunger=(%d,%v)，想要 (0,true)", hunger, ready)
	}

	for _, want := range []uint8{12, 3} {
		advanceSteps(t, p, 2, Control{MoveX: 1})
		state := nextAuthority(p)
		state.Hunger = want
		if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
			t.Fatalf("ApplyPlayerState(hunger=%d): %v", want, err)
		}
		if hunger, ready := p.Hunger(); !ready || hunger != want {
			t.Fatalf("和解后 hunger=(%d,%v)，想要 (%d,true)，饥饿值不得被预测/插值",
				hunger, ready, want)
		}
	}
}

// TestBeginAdoptsAuthoritativeHunger 覆盖首帧路径：`Begin` 与和解走的是两条
// 不同的赋值语句，只测和解的话，`Begin` 漏抄饥饿值的实现会绿到第一次和解为止。
func TestBeginAdoptsAuthoritativeHunger(t *testing.T) {
	p := NewPredictor()
	message := network.PlayerState{
		Dimension: core.Overworld,
		Ready:     true,
		Hunger:    12,
	}
	if err := p.Begin(message); err != nil {
		t.Fatal(err)
	}
	if hunger, ready := p.Hunger(); !ready || hunger != 12 {
		t.Fatalf("Begin 后 hunger=(%d,%v)，想要 (12,true)", hunger, ready)
	}

	// 越界饥饿值必须在 Begin 处就被拒绝，与生命值、氧气同形。
	message.Hunger = core.MaxHunger + 1
	if err := NewPredictor().Begin(message); err == nil {
		t.Fatal("Begin 接受了越界饥饿值")
	}
}

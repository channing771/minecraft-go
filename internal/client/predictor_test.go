package client

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/world"
)

type loadedAirSource struct{}

func (loadedAirSource) CollisionBoxes(core.BlockPos) physics.CollisionBoxSet {
	return physics.CollisionBoxSet{Loaded: true}
}

func TestPredictorBeginRequiresReadyFiniteState(t *testing.T) {
	p := NewPredictor()
	if _, ready := p.State(); ready {
		t.Fatal("Begin 前 Predictor 已 Ready")
	}
	if len(p.history) != 0 || cap(p.history) != 256 {
		t.Fatalf("初始 history len=%d cap=%d，想要 0,256", len(p.history), cap(p.history))
	}

	base := readyPlayerState()
	invalid := []struct {
		name  string
		state network.PlayerState
	}{
		{name: "not ready", state: func() network.PlayerState {
			state := base
			state.Ready = false
			return state
		}()},
		{name: "position", state: func() network.PlayerState {
			state := base
			state.Position[0] = float32(math.NaN())
			return state
		}()},
		{name: "velocity", state: func() network.PlayerState {
			state := base
			state.Velocity[1] = float32(math.Inf(1))
			return state
		}()},
		{name: "yaw", state: func() network.PlayerState {
			state := base
			state.Yaw = float32(math.NaN())
			return state
		}()},
		{name: "pitch", state: func() network.PlayerState {
			state := base
			state.Pitch = float32(math.Inf(-1))
			return state
		}()},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if err := p.Begin(test.state); err == nil {
				t.Fatal("Begin 接受了非法 PlayerState")
			}
			if _, ready := p.State(); ready {
				t.Fatal("非法 Begin 改变了 Predictor ready 状态")
			}
		})
	}
}

func TestPredictorBeginInitializesAndReusesHistory(t *testing.T) {
	p := NewPredictor()
	p.history = append(p.history, predictedInput{sequence: 99})
	p.accumulator = 17 * time.Millisecond
	p.suspended = true
	p.suspendSequence = 98
	p.suspendInputSent = true
	p.displayOffset = mgl32.Vec3{1, 2, 3}
	p.correctionRemaining = time.Second

	message := readyPlayerState()
	if err := p.Begin(message); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state, ready := p.State()
	want := physics.State{
		Position: message.Position,
		Velocity: message.Velocity,
		OnGround: message.OnGround,
	}
	if !ready || state != want || p.previous != want {
		t.Fatalf("Begin state=%+v previous=%+v ready=%v，想要 %+v", state, p.previous, ready, want)
	}
	if p.dimension != message.Dimension || p.lastServerTick != message.ServerTick ||
		p.maxSentInput != message.LastInputSequence {
		t.Fatalf("Begin metadata dimension=%d tick=%d maxInput=%d", p.dimension, p.lastServerTick, p.maxSentInput)
	}
	if len(p.history) != 0 || cap(p.history) != 256 || p.accumulator != 0 ||
		p.suspended || p.suspendSequence != 0 || p.suspendInputSent ||
		p.displayOffset != (mgl32.Vec3{}) ||
		p.correctionRemaining != 0 {
		t.Fatalf("Begin 未清理状态: history=%d/%d accumulator=%v suspended=%v sequence=%d offset=%v correction=%v",
			len(p.history), cap(p.history), p.accumulator, p.suspended,
			p.suspendSequence, p.displayOffset, p.correctionRemaining)
	}
}

func TestPredictorRejectsInvalidControlBeforeAllocatingSequence(t *testing.T) {
	invalid := []Control{
		{MoveX: -2},
		{MoveX: 2},
		{MoveZ: -2},
		{MoveZ: 2},
		{Yaw: float32(math.NaN())},
		{Pitch: float32(math.Inf(1))},
		{Pitch: float32(math.Pi / 2)},
	}
	for _, control := range invalid {
		p := readyPredictor(t)
		allocated := 0
		err := p.Advance(physics.FixedDelta, control, loadedAirSource{},
			func() uint64 { allocated++; return uint64(allocated) },
			func(network.PlayerInput) error { t.Fatal("非法 Control 被发送"); return nil },
		)
		if err == nil || allocated != 0 {
			t.Fatalf("control=%+v err=%v allocated=%d", control, err, allocated)
		}
	}
}

func TestPredictorDoesNotAdvanceUntilSendSucceeds(t *testing.T) {
	p := readyPredictor(t)
	before, _ := p.State()
	sendErr := errors.New("send failed")
	allocated := 0
	err := p.Advance(physics.FixedDelta, Control{MoveX: 1}, loadedAirSource{},
		func() uint64 { allocated++; return uint64(allocated) },
		func(network.PlayerInput) error {
			state, _ := p.State()
			if state != before || p.HistoryLen() != 0 || p.maxSentInput != 0 {
				t.Fatalf("发送回调前已推进: state=%+v history=%d maxSent=%d",
					state, p.HistoryLen(), p.maxSentInput)
			}
			return sendErr
		},
	)
	if !errors.Is(err, sendErr) || allocated != 1 || p.HistoryLen() != 0 ||
		p.maxSentInput != 0 {
		t.Fatalf("err=%v allocated=%d history=%d maxSent=%d",
			err, allocated, p.HistoryLen(), p.maxSentInput)
	}
	after, _ := p.State()
	if after != before {
		t.Fatalf("发送失败改变状态: before=%+v after=%+v", before, after)
	}
}

func TestPredictorSamplesControlAfterFixedDelta(t *testing.T) {
	p := readyPredictor(t)
	before, _ := p.State()
	var sent []network.PlayerInput
	sequence := uint64(40)
	advance := func(elapsed time.Duration) error {
		return p.Advance(elapsed, Control{
			MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.25, Pitch: -0.5,
		}, loadedAirSource{}, func() uint64 {
			sequence++
			return sequence
		}, func(input network.PlayerInput) error {
			sent = append(sent, input)
			return nil
		})
	}
	if err := advance(physics.FixedDelta - time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 0 {
		t.Fatalf("49ms 已发送 %d 条", len(sent))
	}
	if err := advance(time.Millisecond); err != nil {
		t.Fatal(err)
	}
	want := network.PlayerInput{
		Sequence: 41, MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.25, Pitch: -0.5,
	}
	if len(sent) != 1 || sent[0] != want || p.HistoryLen() != 1 {
		t.Fatalf("sent=%+v history=%d，想要 %+v", sent, p.HistoryLen(), want)
	}
	wantHistory := predictedInput{
		sequence: 41,
		input: physics.Input{
			MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.25,
		},
	}
	if p.history[0] != wantHistory {
		t.Fatalf("history[0]=%+v，想要 %+v", p.history[0], wantHistory)
	}
	after, _ := p.State()
	if after == before || after.Velocity.Y() != physics.JumpSpeed ||
		math.Abs(float64(after.Position.Y()-(before.Position.Y()+physics.JumpSpeed*physics.FixedDeltaSeconds))) > 1e-5 {
		t.Fatalf("成功发送后未执行共享固定步: before=%+v after=%+v", before, after)
	}
}

func TestPredictorRunsAtMostFiveFixedStepsPerFrame(t *testing.T) {
	p := readyPredictor(t)
	var sent []network.PlayerInput
	var sequence uint64
	err := p.Advance(260*time.Millisecond, Control{MoveZ: 1}, loadedAirSource{},
		func() uint64 { sequence++; return sequence },
		func(input network.PlayerInput) error { sent = append(sent, input); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 5 || p.HistoryLen() != 5 {
		t.Fatalf("sent=%d history=%d", len(sent), p.HistoryLen())
	}
}

func TestPredictorDropsAccumulatorWhenFrameNeedsMoreThanFiveSteps(t *testing.T) {
	p := readyPredictor(t)
	sent := 0
	sequence := uint64(0)
	advance := func(elapsed time.Duration) error {
		return p.Advance(elapsed, Control{}, loadedAirSource{}, func() uint64 {
			sequence++
			return sequence
		}, func(network.PlayerInput) error {
			sent++
			return nil
		})
	}
	if err := advance(6 * physics.FixedDelta); err != nil {
		t.Fatal(err)
	}
	if sent != 5 {
		t.Fatalf("300ms sent=%d，想要 5", sent)
	}
	if err := advance(physics.FixedDelta - time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if sent != 5 {
		t.Fatalf("丢弃积压后 49ms sent=%d，想要 5", sent)
	}
}

func TestPredictorDropsOverCapStepsWhenCappedFrameSendFails(t *testing.T) {
	p := readyPredictor(t)
	sendErr := errors.New("send failed")
	sequence := uint64(0)
	attempts := 0
	send := func(network.PlayerInput) error {
		attempts++
		if attempts == 5 {
			return sendErr
		}
		return nil
	}
	next := func() uint64 {
		sequence++
		return sequence
	}

	if err := p.Advance(6*physics.FixedDelta, Control{}, loadedAirSource{}, next, send); !errors.Is(err, sendErr) {
		t.Fatalf("capped frame err=%v", err)
	}
	if attempts != 5 || p.HistoryLen() != 4 {
		t.Fatalf("失败帧 attempts=%d history=%d，想要 5,4", attempts, p.HistoryLen())
	}
	if err := p.Advance(0, Control{}, loadedAirSource{}, next, send); err != nil {
		t.Fatalf("重试未完成的第五步: %v", err)
	}
	if attempts != 6 || p.HistoryLen() != 5 {
		t.Fatalf("重试后 attempts=%d history=%d，超限第六步未丢弃", attempts, p.HistoryLen())
	}
	if err := p.Advance(0, Control{}, loadedAirSource{}, next, send); err != nil {
		t.Fatal(err)
	}
	if attempts != 6 || p.HistoryLen() != 5 {
		t.Fatalf("零 elapsed 又推进: attempts=%d history=%d", attempts, p.HistoryLen())
	}
}

func TestPredictorStopsAtUnknownMirrorBoundary(t *testing.T) {
	p, source := predictorNearMissingChunk(t)
	advanceOneStep(t, p, Control{MoveX: 1}, source)
	state, _ := p.State()
	if state.Position.X() <= 15.5 || state.Position.X() > 15.7+1e-5 {
		t.Fatalf("预测进入未知区块: %+v", state)
	}
}

func TestPredictorSuspendsWithOneNeutralInputAtHistoryCapacity(t *testing.T) {
	p, sequence := fullHistoryPredictor(t)
	before, _ := p.State()
	var sent []network.PlayerInput
	callbackSawSuspended := false
	control := Control{MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.4, Pitch: -0.3}
	err := p.Advance(physics.FixedDelta, control, loadedAirSource{}, func() uint64 {
		(*sequence)++
		return *sequence
	}, func(input network.PlayerInput) error {
		callbackSawSuspended = p.Suspended()
		state, _ := p.State()
		if state != before || p.HistoryLen() != 256 {
			t.Fatalf("中立发送前状态已改变: state=%+v history=%d", state, p.HistoryLen())
		}
		sent = append(sent, input)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := network.PlayerInput{Sequence: 257, Yaw: control.Yaw, Pitch: control.Pitch}
	if len(sent) != 1 || sent[0] != want || !callbackSawSuspended {
		t.Fatalf("suspension sent=%+v callbackSawSuspended=%v，想要 %+v", sent, callbackSawSuspended, want)
	}
	after, _ := p.State()
	if !p.Suspended() || p.HistoryLen() != 256 || after != before ||
		p.suspendSequence != 257 || !p.suspendInputSent || p.maxSentInput != 257 {
		t.Fatalf("suspension state suspended=%v history=%d sequence=%d max=%d before=%+v after=%+v",
			p.Suspended(), p.HistoryLen(), p.suspendSequence, p.maxSentInput, before, after)
	}
	if err := p.Advance(time.Second, Control{}, loadedAirSource{}, func() uint64 {
		t.Fatal("成功中立输入后仍分配 sequence")
		return 0
	}, func(network.PlayerInput) error {
		t.Fatal("成功中立输入后仍重发")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPredictorRetriesFailedNeutralInputEveryFixedDelta(t *testing.T) {
	p, sequence := fullHistoryPredictor(t)
	before, _ := p.State()
	sendErr := errors.New("send failed")
	var sent []network.PlayerInput
	send := func(input network.PlayerInput) error {
		sent = append(sent, input)
		if len(sent) < 3 {
			return sendErr
		}
		return nil
	}
	next := func() uint64 {
		(*sequence)++
		return *sequence
	}

	if err := p.Advance(physics.FixedDelta, Control{MoveX: 1}, loadedAirSource{}, next, send); !errors.Is(err, sendErr) {
		t.Fatalf("首次中立发送 err=%v", err)
	}
	if !p.Suspended() || p.suspendSequence != 0 || p.suspendInputSent ||
		p.maxSentInput != 256 {
		t.Fatalf("首次失败 suspended=%v suspendSequence=%d maxSent=%d", p.Suspended(), p.suspendSequence, p.maxSentInput)
	}
	if err := p.Advance(physics.FixedDelta-time.Millisecond, Control{}, loadedAirSource{}, next, send); err != nil {
		t.Fatalf("49ms retry: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("49ms 内重试 %d 次", len(sent))
	}
	if err := p.Advance(time.Millisecond, Control{}, loadedAirSource{}, next, send); !errors.Is(err, sendErr) {
		t.Fatalf("50ms retry err=%v", err)
	}
	if err := p.Advance(physics.FixedDelta, Control{}, loadedAirSource{}, next, send); err != nil {
		t.Fatalf("成功重试: %v", err)
	}
	if len(sent) != 3 || sent[0].Sequence != 257 || sent[1].Sequence != 258 || sent[2].Sequence != 259 {
		t.Fatalf("retry sequences=%+v", sent)
	}
	for _, input := range sent {
		if input.MoveX != 0 || input.MoveZ != 0 || input.Jump {
			t.Fatalf("retry 非中立输入: %+v", input)
		}
	}
	after, _ := p.State()
	if after != before || p.HistoryLen() != 256 || p.suspendSequence != 259 || p.maxSentInput != 259 {
		t.Fatalf("retry 改变预测或记录错误: before=%+v after=%+v history=%d suspend=%d max=%d",
			before, after, p.HistoryLen(), p.suspendSequence, p.maxSentInput)
	}
	if err := p.Advance(physics.FixedDelta, Control{}, loadedAirSource{}, next, send); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 3 {
		t.Fatalf("成功后又重发，count=%d", len(sent))
	}
}

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
	state := nextAuthority(p)
	state.ServerTick = 11
	state.LastInputSequence = 1
	state.Ready = false

	result, err := p.ApplyPlayerState(state, flatClientWorld{})
	if err != nil {
		t.Fatal(err)
	}
	if result != (ReconcileResult{}) {
		t.Fatalf("Ready=false result=%+v", result)
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

func TestSmallCorrectionDecaysInExactlyHundredMilliseconds(t *testing.T) {
	p := readyPredictor(t)
	before, _ := p.PresentationPosition(0)
	state := authorityOffsetBy(p, mgl32.Vec3{0.25, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}

	mid, _ := p.PresentationPosition(50 * time.Millisecond)
	end, _ := p.PresentationPosition(50 * time.Millisecond)
	if got := distance(mid, before); math.Abs(float64(got-0.125)) > 1e-6 {
		t.Fatalf("50ms 中点=%v before=%v distance=%v，想要 0.125", mid, before, got)
	}
	predicted, _ := p.State()
	if !end.ApproxEqualThreshold(predicted.Position, 1e-6) ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
		t.Fatalf("100ms 后=%v offset=%v remaining=%v，想要 %v",
			end, p.displayOffset, p.correctionRemaining, predicted.Position)
	}
}

func TestSmallCorrectionThresholdDoesNotSmoothSubthresholdError(t *testing.T) {
	p := readyPredictor(t)
	state := authorityOffsetBy(p, mgl32.Vec3{1.0/128 - 0.0001, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	displayed, _ := p.PresentationPosition(0)
	predicted, _ := p.State()
	if !displayed.ApproxEqualThreshold(predicted.Position, 1e-6) ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
		t.Fatalf("<1/128 仍创建平滑: displayed=%v predicted=%v offset=%v remaining=%v",
			displayed, predicted.Position, p.displayOffset, p.correctionRemaining)
	}

	p = readyPredictor(t)
	before, _ := p.PresentationPosition(0)
	state = authorityOffsetBy(p, mgl32.Vec3{1.0 / 128, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	displayed, _ = p.PresentationPosition(0)
	if !displayed.ApproxEqualThreshold(before, 1e-6) || p.correctionRemaining != 100*time.Millisecond {
		t.Fatalf("=1/128 未创建平滑: displayed=%v before=%v remaining=%v",
			displayed, before, p.correctionRemaining)
	}
}

func TestLargeCorrectionSnapsAtHalfBlockThreshold(t *testing.T) {
	p := readyPredictor(t)
	state := authorityOffsetBy(p, mgl32.Vec3{0.5, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	displayed, _ := p.PresentationPosition(0)
	predicted, _ := p.State()
	if !displayed.ApproxEqualThreshold(predicted.Position, 1e-6) ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
		t.Fatalf(">=0.5 未 snap: displayed=%v predicted=%v offset=%v remaining=%v",
			displayed, predicted.Position, p.displayOffset, p.correctionRemaining)
	}
}

func TestSmallCorrectionDuringDecayStartsAtActualDisplayedPosition(t *testing.T) {
	p := readyPredictor(t)
	first := authorityOffsetBy(p, mgl32.Vec3{0.25, 0, 0})
	if _, err := p.ApplyPlayerState(first, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	actual, _ := p.PresentationPosition(25 * time.Millisecond)

	second := authorityOffsetBy(p, mgl32.Vec3{0.25, 0, 0})
	if _, err := p.ApplyPlayerState(second, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	restarted, _ := p.PresentationPosition(0)
	if !restarted.ApproxEqualThreshold(actual, 1e-6) {
		t.Fatalf("再次纠正从错误位置开始: actual=%v restarted=%v", actual, restarted)
	}
	end, _ := p.PresentationPosition(100 * time.Millisecond)
	predicted, _ := p.State()
	if !end.ApproxEqualThreshold(predicted.Position, 1e-6) {
		t.Fatalf("再次纠正 100ms 后=%v，想要 %v", end, predicted.Position)
	}
}

func TestSmallCorrectionWithReplayKeepsInterpolatedPresentationContinuous(t *testing.T) {
	p := readyPredictor(t)
	control := Control{MoveX: 1}
	advanceSteps(t, p, 3, control)
	if err := p.Advance(physics.FixedDelta/2, control, flatClientWorld{}, func() uint64 {
		t.Fatal("半个 fixed step 不应分配 sequence")
		return 0
	}, func(network.PlayerInput) error {
		t.Fatal("半个 fixed step 不应发送输入")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := p.PresentationPosition(0)

	initial := readyPlayerState()
	authorityBase := physics.Step(physics.State{
		Position: initial.Position,
		Velocity: initial.Velocity,
		OnGround: initial.OnGround,
	}, physics.Input{MoveX: 1}, flatClientWorld{}).State
	authority := network.PlayerState{
		ServerTick:        p.lastServerTick + 1,
		LastInputSequence: 1,
		Dimension:         core.Overworld,
		Position:          authorityBase.Position.Add(mgl32.Vec3{0.25, 0, 0}),
		Velocity:          authorityBase.Velocity,
		Yaw:               0.4,
		Pitch:             -0.2,
		OnGround:          authorityBase.OnGround,
		Ready:             true,
	}
	if _, err := p.ApplyPlayerState(authority, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	if p.HistoryLen() != 2 || p.accumulator != physics.FixedDelta/2 ||
		p.previous.Position.ApproxEqualThreshold(p.current.Position, 1e-6) {
		t.Fatalf("测试未保留重放与非零插值状态: history=%d accumulator=%v previous=%v current=%v",
			p.HistoryLen(), p.accumulator, p.previous.Position, p.current.Position)
	}
	continued, _ := p.PresentationPosition(0)
	if !continued.ApproxEqualThreshold(before, 1e-6) {
		t.Fatalf("重放后展示位置跳变: before=%v after=%v", before, continued)
	}

	actual, _ := p.PresentationPosition(25 * time.Millisecond)
	authority.ServerTick++
	authority.Position = authorityBase.Position.Add(mgl32.Vec3{0.5, 0, 0})
	if _, err := p.ApplyPlayerState(authority, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	restarted, _ := p.PresentationPosition(0)
	if !restarted.ApproxEqualThreshold(actual, 1e-6) {
		t.Fatalf("衰减中重放纠正再次跳变: actual=%v restarted=%v", actual, restarted)
	}
}

func TestPresentationPositionInterpolatesPreviousAndCurrentState(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 1, Control{MoveX: 1})
	p.accumulator = physics.FixedDelta / 2
	want := p.previous.Position.Add(p.current.Position).Mul(0.5)
	got, ok := p.PresentationPosition(0)
	if !ok || !got.ApproxEqualThreshold(want, 1e-6) {
		t.Fatalf("interpolation=%v ok=%v，想要 %v", got, ok, want)
	}
}

func TestSuspendedPredictorResumesOnlyAfterFixedNeutralSequenceIsAcknowledged(t *testing.T) {
	p, sequence := fullHistoryPredictor(t)
	if err := p.Advance(physics.FixedDelta, Control{}, loadedAirSource{}, func() uint64 {
		(*sequence)++
		return *sequence
	}, func(network.PlayerInput) error { return nil }); err != nil {
		t.Fatal(err)
	}
	before, _ := p.State()

	early := nextAuthority(p)
	early.LastInputSequence = p.suspendSequence - 1
	early.Position = mgl32.Vec3{20, 20, 20}
	if _, err := p.ApplyPlayerState(early, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	got, _ := p.State()
	if !p.Suspended() || p.HistoryLen() != predictionHistoryCapacity || got != before {
		t.Fatalf("neutral ack 前恢复: suspended=%v history=%d before=%+v got=%+v",
			p.Suspended(), p.HistoryLen(), before, got)
	}

	ack := early
	ack.ServerTick++
	ack.LastInputSequence = p.suspendSequence
	ack.Position = mgl32.Vec3{2, 10, 3}
	ack.Velocity = mgl32.Vec3{0.5, 0, 0}
	if _, err := p.ApplyPlayerState(ack, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	want := physics.State{Position: ack.Position, Velocity: ack.Velocity, OnGround: ack.OnGround}
	got, _ = p.State()
	if p.Suspended() || p.suspendInputSent || p.suspendSequence != 0 ||
		p.HistoryLen() != 0 || p.accumulator != 0 {
		t.Fatalf("neutral ack 后未恢复: suspended=%v sent=%v sequence=%d history=%d accumulator=%v",
			p.Suspended(), p.suspendInputSent, p.suspendSequence, p.HistoryLen(), p.accumulator)
	}
	assertStateNear(t, got, want, 1e-6)
}

func TestSuspendedPredictorDoesNotResumeBeforeNeutralSendSucceeds(t *testing.T) {
	p, sequence := fullHistoryPredictor(t)
	sendErr := errors.New("send failed")
	if err := p.Advance(physics.FixedDelta, Control{}, loadedAirSource{}, func() uint64 {
		(*sequence)++
		return *sequence
	}, func(network.PlayerInput) error { return sendErr }); !errors.Is(err, sendErr) {
		t.Fatalf("neutral send err=%v", err)
	}
	before, _ := p.State()
	state := nextAuthority(p)
	state.LastInputSequence = p.maxSentInput
	state.Position = mgl32.Vec3{30, 30, 30}

	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	got, _ := p.State()
	if !p.Suspended() || p.suspendInputSent || p.suspendSequence != 0 ||
		p.HistoryLen() != predictionHistoryCapacity || got != before {
		t.Fatalf("neutral 发送失败后错误恢复: suspended=%v sent=%v sequence=%d history=%d before=%+v got=%+v",
			p.Suspended(), p.suspendInputSent, p.suspendSequence, p.HistoryLen(), before, got)
	}
}

type flatClientWorld struct{}

func (flatClientWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position.Y == 0 {
		return physics.BlockCollisionBoxes(core.StoneID, true)
	}
	return physics.BlockCollisionBoxes(core.AirID, true)
}

func advanceSteps(t *testing.T, p *Predictor, count int, control Control) {
	t.Helper()
	sequence := p.maxSentInput
	for range count {
		if err := p.Advance(physics.FixedDelta, control, flatClientWorld{}, func() uint64 {
			sequence++
			return sequence
		}, func(network.PlayerInput) error { return nil }); err != nil {
			t.Fatalf("Advance: %v", err)
		}
	}
}

func nextAuthority(p *Predictor) network.PlayerState {
	return network.PlayerState{
		ServerTick:        p.lastServerTick + 1,
		LastInputSequence: p.maxSentInput,
		Dimension:         core.Overworld,
		Position:          p.current.Position,
		Velocity:          p.current.Velocity,
		Yaw:               0.4,
		Pitch:             -0.2,
		OnGround:          p.current.OnGround,
		Ready:             true,
	}
}

func authorityOffsetBy(p *Predictor, offset mgl32.Vec3) network.PlayerState {
	state := nextAuthority(p)
	state.Position = state.Position.Add(offset)
	return state
}

func assertResetState(t *testing.T, p *Predictor, state network.PlayerState) {
	t.Helper()
	result, err := p.ApplyPlayerState(state, flatClientWorld{})
	if err != nil {
		t.Fatal(err)
	}
	wantResult := ReconcileResult{ResetView: true, Yaw: state.Yaw, Pitch: state.Pitch}
	if result != wantResult {
		t.Fatalf("result=%+v，想要 %+v", result, wantResult)
	}
	want := physics.State{Position: state.Position, Velocity: state.Velocity, OnGround: state.OnGround}
	got, _ := p.State()
	assertStateNear(t, got, want, 1e-6)
	displayed, ok := p.PresentationPosition(0)
	if !ok || !displayed.ApproxEqualThreshold(want.Position, 1e-6) ||
		p.HistoryLen() != 0 || p.displayOffset != (mgl32.Vec3{}) ||
		p.correctionRemaining != 0 {
		t.Fatalf("reset 未 snap/清空: displayed=%v ok=%v history=%d offset=%v remaining=%v",
			displayed, ok, p.HistoryLen(), p.displayOffset, p.correctionRemaining)
	}
}

func assertStateNear(t *testing.T, got, want physics.State, tolerance float32) {
	t.Helper()
	if !got.Position.ApproxEqualThreshold(want.Position, tolerance) ||
		!got.Velocity.ApproxEqualThreshold(want.Velocity, tolerance) ||
		got.OnGround != want.OnGround {
		t.Fatalf("state=%+v，想要 %+v", got, want)
	}
}

func distance(a, b mgl32.Vec3) float32 {
	return a.Sub(b).Len()
}

func clonePredictor(p *Predictor) Predictor {
	clone := *p
	clone.history = append([]predictedInput(nil), p.history...)
	return clone
}

func assertPredictorSame(t *testing.T, got *Predictor, want Predictor) {
	t.Helper()
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("Predictor 被修改:\n got=%+v\nwant=%+v", *got, want)
	}
}

func readyPredictor(t *testing.T) *Predictor {
	t.Helper()
	p := NewPredictor()
	if err := p.Begin(readyPlayerState()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	return p
}

func readyPlayerState() network.PlayerState {
	return network.PlayerState{
		ServerTick:        7,
		LastInputSequence: 0,
		Dimension:         core.Overworld,
		Position:          mgl32.Vec3{0.5, 10, 0.5},
		Velocity:          mgl32.Vec3{},
		Yaw:               0.2,
		Pitch:             -0.1,
		OnGround:          true,
		Ready:             true,
	}
}

func predictorNearMissingChunk(t *testing.T) (*Predictor, MirrorCollisionSource) {
	t.Helper()
	chunk := world.NewChunk(core.ChunkPos{})
	for z := 0; z < core.SectionSize; z++ {
		for x := 0; x < core.SectionSize; x++ {
			chunk.SetBlock(x, 0, z, core.StoneID)
		}
	}
	mirror := mirrorWithChunk(t, core.Overworld, chunk)
	p := NewPredictor()
	if err := p.Begin(network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{15.5, 1, 0.5},
		Velocity:   mgl32.Vec3{physics.WalkSpeed, 0, 0},
		OnGround:   true,
		Ready:      true,
	}); err != nil {
		t.Fatalf("Begin boundary predictor: %v", err)
	}
	return p, MirrorCollisionSource{Mirror: mirror, Dimension: core.Overworld}
}

func advanceOneStep(
	t *testing.T,
	p *Predictor,
	control Control,
	source physics.CollisionSource,
) {
	t.Helper()
	sequence := uint64(0)
	if err := p.Advance(physics.FixedDelta, control, source,
		func() uint64 { sequence++; return sequence },
		func(network.PlayerInput) error { return nil },
	); err != nil {
		t.Fatalf("Advance: %v", err)
	}
}

func fullHistoryPredictor(t *testing.T) (*Predictor, *uint64) {
	t.Helper()
	p := readyPredictor(t)
	sequence := uint64(0)
	for p.HistoryLen() < 256 {
		if err := p.Advance(physics.FixedDelta, Control{}, loadedAirSource{}, func() uint64 {
			sequence++
			return sequence
		}, func(network.PlayerInput) error { return nil }); err != nil {
			t.Fatalf("填充 history: %v", err)
		}
	}
	return p, &sequence
}

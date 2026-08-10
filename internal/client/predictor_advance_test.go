package client

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
)

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
		{name: "health", state: func() network.PlayerState {
			state := base
			state.Health = core.MaxHealth + 1
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
	message.Health = 15
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
	if health, healthReady := p.Health(); !healthReady || health != message.Health {
		t.Fatalf("Begin health=(%d,%v)，想要 (%d,true)", health, healthReady, message.Health)
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
			MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.25, Pitch: -0.5, Mining: true,
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
		Sequence: 41, MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.25, Pitch: -0.5, Mining: true,
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
	jumpSpeed := physics.DefaultTunables().JumpSpeed
	if after == before || after.Velocity.Y() != jumpSpeed ||
		math.Abs(float64(after.Position.Y()-(before.Position.Y()+jumpSpeed*physics.FixedDeltaSeconds))) > 1e-5 {
		t.Fatalf("成功发送后未执行共享固定步: before=%+v after=%+v", before, after)
	}
}

func TestPredictorRunsAtMostFiveFixedStepsPerFrame(t *testing.T) {
	p := readyPredictor(t)
	var sent []network.PlayerInput
	var sequence uint64
	err := p.Advance(260*time.Millisecond, Control{MoveZ: 1, Mining: true}, loadedAirSource{},
		func() uint64 { sequence++; return sequence },
		func(input network.PlayerInput) error { sent = append(sent, input); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(sent) != 5 || p.HistoryLen() != 5 {
		t.Fatalf("sent=%d history=%d", len(sent), p.HistoryLen())
	}
	for index, input := range sent {
		if !input.Mining {
			t.Fatalf("固定步 %d 丢失采掘状态: %+v", index, input)
		}
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
	control := Control{MoveX: 1, MoveZ: -1, Jump: true, Yaw: 0.4, Pitch: -0.3, Mining: true}
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
	if len(sent) != 1 || sent[0] != want || sent[0].Mining || !callbackSawSuspended {
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

func TestPredictorSuspensionFreezesFractionalPresentationAfterNeutralSend(t *testing.T) {
	p, sequence := fullMovingHistoryPredictor(t)
	if err := p.Advance(physics.FixedDelta/2, Control{MoveX: 1}, flatClientWorld{},
		func() uint64 { (*sequence)++; return *sequence },
		func(network.PlayerInput) error {
			t.Fatal("半步余量不应发送输入")
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	before, _ := p.PresentationPosition(0)

	sent := 0
	if err := p.Advance(physics.FixedDelta/2, Control{MoveX: 1}, flatClientWorld{},
		func() uint64 { (*sequence)++; return *sequence },
		func(input network.PlayerInput) error {
			sent++
			if input.MoveX != 0 || input.MoveZ != 0 || input.Jump {
				t.Fatalf("suspension input=%+v，想要 neutral", input)
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	after, _ := p.PresentationPosition(0)
	state, _ := p.State()
	if sent != 1 || after.X() < before.X() ||
		!after.ApproxEqualThreshold(state.Position, 1e-6) {
		t.Fatalf("进入 suspension 后 presentation=%v，之前=%v state=%v sent=%d",
			after, before, state.Position, sent)
	}

	if err := p.Advance(5*physics.FixedDelta, Control{}, flatClientWorld{},
		func() uint64 {
			t.Fatal("成功 neutral 后等待期间又分配 sequence")
			return 0
		},
		func(network.PlayerInput) error {
			t.Fatal("成功 neutral 后等待期间又发送")
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	waiting, _ := p.PresentationPosition(0)
	if !waiting.ApproxEqualThreshold(after, 1e-6) {
		t.Fatalf("等待 authority ack 时 presentation=%v，想要冻结在 %v", waiting, after)
	}
}

func TestPredictorSuspensionRetryKeepsFractionalPresentationFrozen(t *testing.T) {
	p, sequence := fullMovingHistoryPredictor(t)
	if err := p.Advance(physics.FixedDelta/2, Control{MoveX: 1}, flatClientWorld{},
		func() uint64 { (*sequence)++; return *sequence },
		func(network.PlayerInput) error { return nil },
	); err != nil {
		t.Fatal(err)
	}
	before, _ := p.PresentationPosition(0)

	sendErr := errors.New("send failed")
	attempts := 0
	send := func(network.PlayerInput) error {
		attempts++
		if attempts < 3 {
			return sendErr
		}
		return nil
	}
	next := func() uint64 { (*sequence)++; return *sequence }
	var frozen mgl32.Vec3
	checkFrozen := func(stage string) {
		t.Helper()
		got, _ := p.PresentationPosition(0)
		if frozen == (mgl32.Vec3{}) {
			state, _ := p.State()
			if got.X() < before.X() || !got.ApproxEqualThreshold(state.Position, 1e-6) {
				t.Fatalf("%s presentation=%v，之前=%v state=%v", stage, got, before, state.Position)
			}
			frozen = got
			return
		}
		if !got.ApproxEqualThreshold(frozen, 1e-6) {
			t.Fatalf("%s presentation=%v，想要冻结在 %v", stage, got, frozen)
		}
	}

	if err := p.Advance(physics.FixedDelta/2, Control{}, flatClientWorld{}, next, send); !errors.Is(err, sendErr) {
		t.Fatalf("首次 neutral err=%v", err)
	}
	checkFrozen("首次发送失败后")
	if err := p.Advance(physics.FixedDelta/2, Control{}, flatClientWorld{}, next, send); err != nil {
		t.Fatalf("半个 retry interval: %v", err)
	}
	checkFrozen("半个 retry interval 后")
	if err := p.Advance(physics.FixedDelta/2, Control{}, flatClientWorld{}, next, send); !errors.Is(err, sendErr) {
		t.Fatalf("第二次 neutral err=%v", err)
	}
	checkFrozen("第二次发送失败后")
	if err := p.Advance(physics.FixedDelta, Control{}, flatClientWorld{}, next, send); err != nil {
		t.Fatalf("成功 neutral retry: %v", err)
	}
	checkFrozen("成功 retry 后")
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

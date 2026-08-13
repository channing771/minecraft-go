package client

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
)

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

func TestExactAckPreservesRemainingInterpolation(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 1, Control{MoveX: 1})
	p.accumulator = physics.FixedDelta / 2
	before, _ := p.PresentationPosition(0)

	if _, err := p.ApplyPlayerState(nextAuthority(p), flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	after, _ := p.PresentationPosition(0)
	if !after.ApproxEqualThreshold(before, 1e-6) {
		t.Fatalf("精确 ack 后展示跳变: before=%v after=%v", before, after)
	}
	if p.HistoryLen() != 0 || p.correctionRemaining != physics.FixedDelta/2 {
		t.Fatalf("ack 后 history=%d remaining=%v", p.HistoryLen(), p.correctionRemaining)
	}

	end, _ := p.PresentationPosition(physics.FixedDelta / 2)
	state, _ := p.State()
	if !end.ApproxEqualThreshold(state.Position, 1e-6) ||
		p.correctionRemaining != 0 || p.displayOffset != (mgl32.Vec3{}) {
		t.Fatalf("剩余固定步后未收敛: end=%v state=%v offset=%v remaining=%v",
			end, state.Position, p.displayOffset, p.correctionRemaining)
	}
}

func TestExactAckPreservesLongerExistingCorrection(t *testing.T) {
	p := readyPredictor(t)
	if _, err := p.ApplyPlayerState(
		authorityOffsetBy(p, mgl32.Vec3{0.25, 0, 0}),
		flatClientWorld{},
	); err != nil {
		t.Fatal(err)
	}
	before, _ := p.PresentationPosition(25 * time.Millisecond)
	if _, err := p.ApplyPlayerState(nextAuthority(p), flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	after, _ := p.PresentationPosition(0)
	if !after.ApproxEqualThreshold(before, 1e-6) {
		t.Fatalf("纠正中精确 ack 后展示跳变: before=%v after=%v", before, after)
	}
	if p.correctionRemaining != 75*time.Millisecond {
		t.Fatalf("纠正中精确 ack 缩短剩余时间: remaining=%v", p.correctionRemaining)
	}

	end, _ := p.PresentationPosition(75 * time.Millisecond)
	state, _ := p.State()
	if !end.ApproxEqualThreshold(state.Position, 1e-6) ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
		t.Fatalf("原纠正剩余时间后未收敛: end=%v state=%v offset=%v remaining=%v",
			end, state.Position, p.displayOffset, p.correctionRemaining)
	}
}

func TestExactAcksKeepHighRateMovementAndJumpContinuous(t *testing.T) {
	for _, test := range []struct {
		name    string
		control Control
	}{
		{name: "move", control: Control{MoveX: 1}},
		{name: "jump", control: Control{Jump: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := readyPlayerState()
			state.Position[1] = 1
			start := state.Position
			p := NewPredictor()
			if err := p.Begin(state); err != nil {
				t.Fatal(err)
			}
			sequence := p.maxSentInput
			previous, _ := p.PresentationPosition(0)
			maxY := previous.Y()
			var pending *network.PlayerState
			const frameElapsed = 5 * time.Millisecond

			for frame := range 80 {
				if pending != nil {
					if _, err := p.ApplyPlayerState(*pending, flatClientWorld{}); err != nil {
						t.Fatalf("frame %d ack: %v", frame, err)
					}
					pending = nil
				}
				if err := p.Advance(
					frameElapsed,
					test.control,
					flatClientWorld{},
					func() uint64 { sequence++; return sequence },
					func(network.PlayerInput) error { return nil },
				); err != nil {
					t.Fatalf("frame %d Advance: %v", frame, err)
				}
				if p.HistoryLen() != 0 {
					ack := nextAuthority(p)
					pending = &ack
				}
				position, _ := p.PresentationPosition(frameElapsed)
				if delta := position.Sub(previous).Len(); delta > 0.06 {
					t.Fatalf("frame %d 展示跳变 %.6f: previous=%v position=%v",
						frame, delta, previous, position)
				}
				previous = position
				maxY = max(maxY, position.Y())
			}
			if pending != nil {
				if _, err := p.ApplyPlayerState(*pending, flatClientWorld{}); err != nil {
					t.Fatal(err)
				}
			}
			position, _ := p.PresentationPosition(physics.FixedDelta)
			predicted, _ := p.State()
			if !position.ApproxEqualThreshold(predicted.Position, 1e-6) ||
				p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
				t.Fatalf("最终展示未收敛: position=%v predicted=%v offset=%v remaining=%v",
					position, predicted.Position, p.displayOffset, p.correctionRemaining)
			}
			switch test.name {
			case "move":
				if predicted.Position.X() <= start.X()+0.1 {
					t.Fatalf("move 未水平前进: start=%v end=%v", start, predicted.Position)
				}
			case "jump":
				if maxY <= start.Y()+0.1 || predicted.Position.Y() >= maxY-0.05 {
					t.Fatalf("jump 未离地并回落: start=%v peak=%v end=%v",
						start, maxY, predicted.Position)
				}
			}
		})
	}
}

func TestSmallCorrectionThresholdKeepsSubthresholdErrorContinuous(t *testing.T) {
	p := readyPredictor(t)
	advanceSteps(t, p, 1, Control{MoveX: 1})
	p.accumulator = physics.FixedDelta / 2
	before, _ := p.PresentationPosition(0)
	state := authorityOffsetBy(p, mgl32.Vec3{1.0/128 - 0.0001, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	displayed, _ := p.PresentationPosition(0)
	predicted, _ := p.State()
	if !displayed.ApproxEqualThreshold(before, 1e-6) ||
		p.correctionRemaining != physics.FixedDelta/2 {
		t.Fatalf("<1/128 ack 后展示不连续: displayed=%v before=%v remaining=%v",
			displayed, before, p.correctionRemaining)
	}
	end, _ := p.PresentationPosition(physics.FixedDelta / 2)
	if !end.ApproxEqualThreshold(predicted.Position, 1e-6) ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
		t.Fatalf("<1/128 未在剩余固定步后收敛: end=%v predicted=%v offset=%v remaining=%v",
			end, predicted.Position, p.displayOffset, p.correctionRemaining)
	}

	p = readyPredictor(t)
	before, _ = p.PresentationPosition(0)
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

func TestSubthresholdAckAfterFailedSendConvergesImmediately(t *testing.T) {
	p := readyPredictor(t)
	sendErr := errors.New("send failed")
	if err := p.Advance(
		physics.FixedDelta,
		Control{MoveX: 1},
		flatClientWorld{},
		func() uint64 { return p.maxSentInput + 1 },
		func(network.PlayerInput) error { return sendErr },
	); !errors.Is(err, sendErr) {
		t.Fatalf("Advance error=%v，想要 %v", err, sendErr)
	}
	if p.accumulator != physics.FixedDelta {
		t.Fatalf("send 失败后 accumulator=%v，想要 %v", p.accumulator, physics.FixedDelta)
	}

	state := authorityOffsetBy(p, mgl32.Vec3{1.0 / 256, 0, 0})
	if _, err := p.ApplyPlayerState(state, flatClientWorld{}); err != nil {
		t.Fatal(err)
	}
	displayed, _ := p.PresentationPosition(0)
	predicted, _ := p.State()
	if !displayed.ApproxEqualThreshold(predicted.Position, 1e-6) ||
		p.displayOffset != (mgl32.Vec3{}) || p.correctionRemaining != 0 {
		t.Fatalf("零时长低阈值 ack 未立即收敛: displayed=%v predicted=%v offset=%v remaining=%v",
			displayed, predicted.Position, p.displayOffset, p.correctionRemaining)
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

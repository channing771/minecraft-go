package client

import (
	"reflect"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

type loadedAirSource struct{}

func (loadedAirSource) CollisionBoxes(core.BlockPos) physics.CollisionBoxSet {
	return physics.CollisionBoxSet{Loaded: true}
}

func (loadedAirSource) IsFluidAt(core.BlockPos) bool { return false }

type flatClientWorld struct{}

func (flatClientWorld) CollisionBoxes(position core.BlockPos) physics.CollisionBoxSet {
	if position.Y == 0 {
		return physics.BlockCollisionBoxes(core.StoneID, true)
	}
	return physics.BlockCollisionBoxes(core.AirID, true)
}

func (flatClientWorld) IsFluidAt(core.BlockPos) bool { return false }

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
		Velocity:   mgl32.Vec3{physics.DefaultTunables().WalkSpeed, 0, 0},
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
	source physics.WorldSource,
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

func fullMovingHistoryPredictor(t *testing.T) (*Predictor, *uint64) {
	t.Helper()
	p := NewPredictor()
	state := readyPlayerState()
	state.Position = mgl32.Vec3{0.5, 1, 0.5}
	if err := p.Begin(state); err != nil {
		t.Fatalf("Begin moving predictor: %v", err)
	}
	sequence := uint64(0)
	for p.HistoryLen() < predictionHistoryCapacity {
		if err := p.Advance(physics.FixedDelta, Control{MoveX: 1}, flatClientWorld{}, func() uint64 {
			sequence++
			return sequence
		}, func(network.PlayerInput) error { return nil }); err != nil {
			t.Fatalf("填充 moving history: %v", err)
		}
	}
	return p, &sequence
}

package client

import (
	"math"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

func FuzzPlayerStateValidationIsAtomic(f *testing.F) {
	for field := uint8(0); field < 7; field++ {
		f.Add(field, field)
	}
	f.Fuzz(func(t *testing.T, field, component uint8) {
		p := readyPredictor(t)
		advanceSteps(t, p, 2, Control{MoveX: 1})
		p.accumulator = 17 * time.Millisecond
		p.displayOffset = mgl32.Vec3{0.1, 0.2, 0.3}
		p.correctionRemaining = 63 * time.Millisecond
		state := nextAuthority(p)

		switch field % 7 {
		case 0:
			state.Position[component%3] = math.Float32frombits(0x7fc00000 | uint32(component))
		case 1:
			state.Velocity[component%3] = float32(math.Inf(int(component&1)*2 - 1))
		case 2:
			state.Yaw = float32(math.NaN())
		case 3:
			state.Pitch = float32(math.Pi / 2)
		case 4:
			state.Dimension = core.DimensionID(99 + int32(component))
		case 5:
			state.LastInputSequence = p.maxSentInput + 1
		case 6:
			state.Ready = false
			state.Reset = true
		}
		before := clonePredictor(p)

		result, err := p.ApplyPlayerState(state, flatClientWorld{})
		if err == nil {
			t.Fatalf("field=%d component=%d 接受非法 PlayerState", field, component)
		}
		if result != (ReconcileResult{}) {
			t.Fatalf("field=%d component=%d result=%+v", field, component, result)
		}
		assertPredictorSame(t, p, before)
	})
}

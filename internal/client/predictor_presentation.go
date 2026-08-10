package client

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/physics"
)

// PresentationPosition 返回插值并应用纠正衰减后的显示脚底位置。
func (p *Predictor) PresentationPosition(frameElapsed time.Duration) (mgl32.Vec3, bool) {
	if !p.ready {
		return mgl32.Vec3{}, false
	}
	if frameElapsed > 0 && p.correctionRemaining > 0 {
		if frameElapsed >= p.correctionRemaining {
			p.displayOffset = mgl32.Vec3{}
			p.correctionRemaining = 0
		} else {
			remaining := p.correctionRemaining - frameElapsed
			p.displayOffset = p.displayOffset.Mul(
				float32(remaining) / float32(p.correctionRemaining),
			)
			p.correctionRemaining = remaining
		}
	}
	return p.presentationPositionNoAdvance(), true
}

func (p *Predictor) presentationPositionNoAdvance() mgl32.Vec3 {
	return p.interpolatedPosition().Add(p.displayOffset)
}

func (p *Predictor) interpolatedPosition() mgl32.Vec3 {
	alpha := float32(p.accumulator) / float32(physics.FixedDelta)
	if alpha < 0 {
		alpha = 0
	} else if alpha > 1 {
		alpha = 1
	}
	return p.previous.Position.Mul(1 - alpha).Add(p.current.Position.Mul(alpha))
}

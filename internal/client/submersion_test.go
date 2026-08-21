package client

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/world"
)

// waterColumnMirror 造一个 y=1..8 全是水、y=0 是石头的测试镜像。
func waterColumnMirror(t *testing.T) *Mirror {
	t.Helper()
	chunk := world.NewChunk(core.ChunkPos{})
	for z := range core.SectionSize {
		for x := range core.SectionSize {
			chunk.SetBlock(x, 0, z, core.StoneID)
			for y := int32(1); y <= 8; y++ {
				chunk.SetBlock(x, y, z, core.WaterSourceID)
			}
		}
	}
	chunk.Compact()
	return mirrorWithChunk(t, core.Overworld, chunk)
}

// TestMirrorFluidSourceReportsWaterAndDryCells 守住镜像适配器只报告真实存在的水。
func TestMirrorFluidSourceReportsWaterAndDryCells(t *testing.T) {
	source := MirrorCollisionSource{Mirror: waterColumnMirror(t), Dimension: core.Overworld}
	if !source.IsFluidAt(core.BlockPos{X: 2, Y: 4, Z: 3}) {
		t.Fatal("水格未被判为流体")
	}
	if source.IsFluidAt(core.BlockPos{X: 2, Y: 0, Z: 3}) {
		t.Fatal("石头被判为流体")
	}
	if source.IsFluidAt(core.BlockPos{X: 2, Y: 9, Z: 3}) {
		t.Fatal("空气被判为流体")
	}
	if source.IsFluidAt(core.BlockPos{X: 2, Y: core.MaxY, Z: 3}) {
		t.Fatal("世界高度之外被判为流体")
	}
}

// TestMirrorFluidSourceTreatsMissingChunkAsDry 守住「缺失区块不得凭空造水」——
// 客户端在区块尚未到达时若判为有水，会预测出一段服务端不存在的水中物理。
func TestMirrorFluidSourceTreatsMissingChunkAsDry(t *testing.T) {
	source := MirrorCollisionSource{Mirror: NewMirror(), Dimension: core.Overworld}
	if source.IsFluidAt(core.BlockPos{X: 32, Y: 4, Z: 0}) {
		t.Fatal("缺失区块被判为流体")
	}
}

// TestPredictionUsesFluidPhysicsInsideWater 守住预测确实把浸没标志喂进了物理步：
// 悬空按住上升键在空气里毫无作用（Jump 只在触地那一 tick 生效），在水里则必须
// 持续上浮。这条一旦变红，说明 Predictor 没有在每步重算浸没标志。
func TestPredictionUsesFluidPhysicsInsideWater(t *testing.T) {
	rise := func(source physics.WorldSource) float32 {
		p := NewPredictor()
		if err := p.Begin(network.PlayerState{
			ServerTick: 7,
			Dimension:  core.Overworld,
			Position:   mgl32.Vec3{0.5, 4, 0.5},
			Ready:      true,
		}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		sequence := uint64(0)
		for range 10 {
			if err := p.Advance(physics.FixedDelta, Control{Jump: true}, source,
				func() uint64 { sequence++; return sequence },
				func(network.PlayerInput) error { return nil },
			); err != nil {
				t.Fatalf("Advance: %v", err)
			}
		}
		return p.current.Position.Y() - 4
	}
	water := rise(MirrorCollisionSource{Mirror: waterColumnMirror(t), Dimension: core.Overworld})
	air := rise(loadedAirSource{})
	if water <= 0 {
		t.Fatalf("水中持续按住上升键位移=%f，想要严格上升", water)
	}
	if air >= 0 {
		t.Fatalf("空气中悬空按住上升键位移=%f，想要下落", air)
	}
}

// TestUnderwaterVisualUsesTheSameEyeFlagAsDrowning 覆盖 spec Scenario
// 「视觉与溺水判定一致」：驱动水下视觉的标志，就是驱动水中物理、并与服务端氧气
// 结算同源的那一个眼睛浸没标志，不存在第二套判定。
//
// 对照落在两个规则明显分歧的世界上——满水与纯空气：一侧必须为真、另一侧必须为假。
// 只在一侧断言会让"永远返回 true"这种变异全绿。逐 tick 与共享判定函数的独立
// 重算比对，则把"标志被缓存成陈旧值"也一并挡住。
func TestUnderwaterVisualUsesTheSameEyeFlagAsDrowning(t *testing.T) {
	advance := func(source physics.WorldSource) *Predictor {
		t.Helper()
		p := NewPredictor()
		if err := p.Begin(network.PlayerState{
			ServerTick: 7,
			Dimension:  core.Overworld,
			Position:   mgl32.Vec3{0.5, 4, 0.5},
			Ready:      true,
		}); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		sequence := uint64(0)
		for range 5 {
			if err := p.Advance(physics.FixedDelta, Control{}, source,
				func() uint64 { sequence++; return sequence },
				func(network.PlayerInput) error { return nil },
			); err != nil {
				t.Fatalf("Advance: %v", err)
			}
			// 每一步之后都比对：预测器报出的标志必须与用同一份镜像、同一个共享
			// 判定函数在**当前**位置独立重算的结果逐位相同。
			_, want := physics.SubmersionFlags(p.current.Position, source)
			if got := p.EyeInFluid(); got != want {
				t.Fatalf("位置 %v：EyeInFluid=%v，共享判定给出 %v",
					p.current.Position, got, want)
			}
		}
		return p
	}

	water := advance(MirrorCollisionSource{Mirror: waterColumnMirror(t), Dimension: core.Overworld})
	air := advance(loadedAirSource{})
	if !water.EyeInFluid() {
		t.Fatal("满水世界里 EyeInFluid=false：水下视觉不会被触发")
	}
	if air.EyeInFluid() {
		t.Fatal("纯空气世界里 EyeInFluid=true：水下视觉会凭空触发")
	}

	// 未就绪的预测器没有相机位置可谈，必须恒为假，否则登录前会闪一帧水色。
	if NewPredictor().EyeInFluid() {
		t.Fatal("未就绪的预测器报告 EyeInFluid=true")
	}
}

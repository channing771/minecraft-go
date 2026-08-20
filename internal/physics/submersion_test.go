package physics_test

import (
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/physics"
)

// testFluidWorld 是最小的 FluidSource：只有登记过的格是流体，其余（含未加载、
// 超出高度）都返回 false。
type testFluidWorld map[core.BlockPos]bool

func (world testFluidWorld) IsFluidAt(position core.BlockPos) bool { return world[position] }

// fluidLayers 把若干整层 y 标成流体，覆盖玩家 AABB 水平范围之外一圈。
func fluidLayers(levels ...int32) testFluidWorld {
	world := testFluidWorld{}
	for _, y := range levels {
		for x := int32(-2); x <= 2; x++ {
			for z := int32(-2); z <= 2; z++ {
				world[core.BlockPos{X: x, Y: y, Z: z}] = true
			}
		}
	}
	return world
}

// TestSubmersionFlagsWaistDeepWaterOnlySubmergesBody 覆盖 spec Scenario
// 「齐腰深水只触发身体浸没」。玩家脚底在 y=10、眼睛在 y=11.62（格 11），
// 水只到格 10 的顶面。
func TestSubmersionFlagsWaistDeepWaterOnlySubmergesBody(t *testing.T) {
	position := mgl32.Vec3{0.5, 10, 0.5}
	body, eye := physics.SubmersionFlags(position, fluidLayers(9, 10))
	if !body || eye {
		t.Fatalf("齐腰深水 body/eye=%v/%v，想要 true/false", body, eye)
	}
	// 夹具前提守卫排在真实断言之后：眼睛必须落在水面之上的那一格，否则这条
	// 用例会退化成「完全没入」而看起来仍然通过。
	if eyeCell := int32(10 + physics.DefaultTunables().EyeHeight); eyeCell != 11 {
		t.Fatalf("夹具前提失效：眼睛格=%d，想要 11", eyeCell)
	}
}

// TestSubmersionFlagsFullySubmergedSetsBothFlags 覆盖 spec Scenario
// 「完全没入触发两个标志」。
func TestSubmersionFlagsFullySubmergedSetsBothFlags(t *testing.T) {
	body, eye := physics.SubmersionFlags(mgl32.Vec3{0.5, 10, 0.5}, fluidLayers(9, 10, 11, 12))
	if !body || !eye {
		t.Fatalf("完全没入 body/eye=%v/%v，想要 true/true", body, eye)
	}
}

// TestSubmersionFlagsDryWorldClearsBothFlags 守住「没有水时两个标志都为假」，
// 否则上面两条会被一个恒真实现同时骗过。
func TestSubmersionFlagsDryWorldClearsBothFlags(t *testing.T) {
	body, eye := physics.SubmersionFlags(mgl32.Vec3{0.5, 10, 0.5}, testFluidWorld{})
	if body || eye {
		t.Fatalf("无水 body/eye=%v/%v，想要 false/false", body, eye)
	}
}

// TestSubmersionFlagsIgnoresZeroVolumeTouch 守住「AABB 只是贴在格边界上不算浸没」。
// 玩家中心 x=0.7、半宽 0.3 时 AABB 上界正好是 x=1.0，与格 x=1 的相交体积为零。
func TestSubmersionFlagsIgnoresZeroVolumeTouch(t *testing.T) {
	position := mgl32.Vec3{0.7, 10, 0.5}
	touching := testFluidWorld{}
	overlapping := testFluidWorld{}
	for y := int32(10); y <= 11; y++ {
		touching[core.BlockPos{X: 1, Y: y, Z: 0}] = true
		overlapping[core.BlockPos{X: 0, Y: y, Z: 0}] = true
	}
	if body, _ := physics.SubmersionFlags(position, touching); body {
		t.Fatal("零体积贴边被判为身体浸没")
	}
	// 对照：同一位置下真正重叠的那一列必须判为浸没，否则上一条会因「扫描范围
	// 整体错位」而假绿。
	if body, _ := physics.SubmersionFlags(position, overlapping); !body {
		t.Fatal("真正重叠的流体格未被判为身体浸没")
	}
}

// TestSubmersionFlagsEyeImpliesBody 守住两个标志的蕴含关系：眼睛（脚底 +1.62）
// 始终落在玩家 AABB（高 1.8）内部，眼睛浸没时身体不可能为假。
func TestSubmersionFlagsEyeImpliesBody(t *testing.T) {
	for _, y := range []float32{-3.25, 0, 10.5, 63.9} {
		for _, layer := range []int32{-4, 0, 10, 11, 12, 64, 65} {
			body, eye := physics.SubmersionFlags(mgl32.Vec3{0.5, y, 0.5}, fluidLayers(layer))
			if eye && !body {
				t.Fatalf("y=%v layer=%d：眼睛浸没但身体未浸没", y, layer)
			}
		}
	}
}

// TestSubmersionFlagsUnloadedSourceStaysDry 守住「未加载的格不得凭空造水」。
func TestSubmersionFlagsUnloadedSourceStaysDry(t *testing.T) {
	body, eye := physics.SubmersionFlags(mgl32.Vec3{0.5, 10, 0.5}, testFluidWorld(nil))
	if body || eye {
		t.Fatalf("未加载 body/eye=%v/%v，想要 false/false", body, eye)
	}
}

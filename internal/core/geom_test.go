package core_test

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"
	"minecraft-go/internal/core"
)

func TestFrustumCullsBehindCamera(t *testing.T) {
	// 相机在原点看向 -Z，这是 OpenGL/WebGPU 的惯例朝向。
	view := mgl32.LookAtV(
		mgl32.Vec3{0, 0, 0},
		mgl32.Vec3{0, 0, -1},
		mgl32.Vec3{0, 1, 0},
	)
	proj := core.Perspective(mgl32.DegToRad(70), 16.0/9.0, 0.1, 1000)
	f := core.FrustumFrom(proj.Mul4(view))

	inFront := core.AABB{
		Min: mgl32.Vec3{-1, -1, -20},
		Max: mgl32.Vec3{1, 1, -18},
	}
	if !f.IntersectsAABB(inFront) {
		t.Fatal("正前方 20 米的盒子被错误剔除")
	}

	behind := core.AABB{
		Min: mgl32.Vec3{-1, -1, 18},
		Max: mgl32.Vec3{1, 1, 20},
	}
	if f.IntersectsAABB(behind) {
		t.Fatal("相机背后的盒子没有被剔除")
	}

	farAway := core.AABB{
		Min: mgl32.Vec3{-1, -1, -2000},
		Max: mgl32.Vec3{1, 1, -1900},
	}
	if f.IntersectsAABB(farAway) {
		t.Fatal("远平面外的盒子没有被剔除")
	}

	wayLeft := core.AABB{
		Min: mgl32.Vec3{500, -1, -20},
		Max: mgl32.Vec3{502, 1, -18},
	}
	if f.IntersectsAABB(wayLeft) {
		t.Fatal("视锥左右范围外的盒子没有被剔除")
	}
}

func TestPerspectiveUsesWebGPUDepthRange(t *testing.T) {
	const near = float32(0.1)
	const far = float32(1000)
	proj := core.Perspective(mgl32.DegToRad(70), 16.0/9.0, near, far)

	ndcZ := func(viewZ float32) float32 {
		clip := proj.Mul4x1(mgl32.Vec4{0, 0, viewZ, 1})
		return clip[2] / clip[3]
	}
	if got := ndcZ(-near); math.Abs(float64(got)) > 1e-5 {
		t.Fatalf("近平面 NDC z = %g，想要 0", got)
	}
	if got := ndcZ(-far); math.Abs(float64(got-1)) > 1e-5 {
		t.Fatalf("远平面 NDC z = %g，想要 1", got)
	}

	frustum := core.FrustumFrom(proj)
	beforeNear := core.AABB{
		Min: mgl32.Vec3{-0.01, -0.01, -0.05},
		Max: mgl32.Vec3{0.01, 0.01, -0.01},
	}
	if frustum.IntersectsAABB(beforeNear) {
		t.Fatal("完全位于近平面之前的盒子没有被剔除")
	}
}

func TestAABBOverlapUsesStrictVolume(t *testing.T) {
	a := core.AABB{Min: mgl32.Vec3{0, 0, 0}, Max: mgl32.Vec3{1, 1, 1}}
	if !a.Overlaps(core.AABB{Min: mgl32.Vec3{0.5, 0, 0}, Max: mgl32.Vec3{1.5, 1, 1}}) {
		t.Fatal("有体积交叠的 AABB 未命中")
	}
	if a.Overlaps(core.AABB{Min: mgl32.Vec3{1, 0, 0}, Max: mgl32.Vec3{2, 1, 1}}) {
		t.Fatal("仅接触边界的 AABB 不应算交叠")
	}
}

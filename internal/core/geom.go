package core

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
)

// AABB 是轴对齐包围盒。
type AABB struct{ Min, Max mgl32.Vec3 }

// Overlaps 判断两个包围盒是否存在严格的体积交叠。
func (a AABB) Overlaps(b AABB) bool {
	return a.Min.X() < b.Max.X() && a.Max.X() > b.Min.X() &&
		a.Min.Y() < b.Max.Y() && a.Max.Y() > b.Min.Y() &&
		a.Min.Z() < b.Max.Z() && a.Max.Z() > b.Min.Z()
}

// Ray 是一条射线，Dir 应为单位向量。
type Ray struct{ Origin, Dir mgl32.Vec3 }

// Frustum 是 6 个平面：左、右、下、上、近、远。
// 每个平面存为 vec4，xyz 是指向视锥内侧的法线，w 是平面到原点的有符号距离。
type Frustum [6]mgl32.Vec4

// Perspective 返回右手坐标系、WebGPU [0,1] 深度范围的透视投影矩阵。
//
// mathgl 的 mgl32.Perspective 使用 OpenGL 的 [-1,1] 深度范围，不能直接交给
// WebGPU；否则近平面会被实际裁在 near 的约两倍距离处。
func Perspective(fovY, aspect, near, far float32) mgl32.Mat4 {
	f := float32(1 / math.Tan(float64(fovY)/2))
	invDepth := 1 / (near - far)
	return mgl32.Mat4{
		f / aspect, 0, 0, 0,
		0, f, 0, 0,
		0, 0, far * invDepth, -1,
		0, 0, far * near * invDepth, 0,
	}
}

// FrustumFrom 用 Gribb-Hartmann 方法从 view-projection 矩阵提取 6 个平面。
//
// mgl32.Mat4 是列主序，索引 m[col*4+row]。输入矩阵使用 WebGPU 的 [0,1]
// 深度范围，因此近平面直接取第 2 行。
func FrustumFrom(m mgl32.Mat4) Frustum {
	row := func(i int) mgl32.Vec4 {
		return mgl32.Vec4{m[0*4+i], m[1*4+i], m[2*4+i], m[3*4+i]}
	}
	r0, r1, r2, r3 := row(0), row(1), row(2), row(3)

	var f Frustum
	f[0] = r3.Add(r0) // 左
	f[1] = r3.Sub(r0) // 右
	f[2] = r3.Add(r1) // 下
	f[3] = r3.Sub(r1) // 上
	f[4] = r2         // 近
	f[5] = r3.Sub(r2) // 远

	// 归一化，使 w 成为真实距离——否则不同平面的尺度不一致。
	for i := range f {
		n := mgl32.Vec3{f[i][0], f[i][1], f[i][2]}
		if l := n.Len(); l > 0 {
			f[i] = f[i].Mul(1 / l)
		}
	}
	return f
}

// IntersectsAABB 判断包围盒是否与视锥相交。
//
// 用「正顶点」测试：对每个平面，取盒子在该平面法线方向上最远的那个角，
// 若它都在平面外侧，则整个盒子都在外侧。这会保留少量假阳性
// （盒子在视锥角落外但被判为相交），对剔除而言是安全的方向。
func (f Frustum) IntersectsAABB(b AABB) bool {
	for _, p := range f {
		positive := mgl32.Vec3{b.Min[0], b.Min[1], b.Min[2]}
		if p[0] >= 0 {
			positive[0] = b.Max[0]
		}
		if p[1] >= 0 {
			positive[1] = b.Max[1]
		}
		if p[2] >= 0 {
			positive[2] = b.Max[2]
		}
		if p[0]*positive[0]+p[1]*positive[1]+p[2]*positive[2]+p[3] < 0 {
			return false
		}
	}
	return true
}

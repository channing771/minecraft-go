package mesh_test

import (
	"math/rand"
	"testing"
	"unsafe"

	"github.com/channing771/mornlea/internal/mesh"
)

func TestQuadPackRemainsEightBytes(t *testing.T) {
	if got := unsafe.Sizeof(mesh.Quad{}.Pack()); got != 8 {
		t.Fatalf("Quad.Pack 大小 = %d，想要 8", got)
	}
}

func TestQuadPackRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 100000; i++ {
		mat := uint16(rng.Intn(65536))
		// 植物 material 只允许出现在 face 6/7 上（quad.go 的双向约束），随机的
		// 轴向面必须避开那 8 个值，否则 Pack 会按约定拒绝。植物 quad 自身的往返
		// 由 TestPlantQuadsSurviveTheUploadRoundTrip 覆盖。
		if mesh.PlantMaterial(mat) {
			mat = mesh.PlantMaterialLast + 1
		}
		want := mesh.Quad{
			X:     uint8(rng.Intn(16)),
			Y:     uint8(rng.Intn(16)),
			Z:     uint8(rng.Intn(16)),
			W:     uint8(rng.Intn(16) + 1),
			H:     uint8(rng.Intn(16) + 1),
			Face:  mesh.Face(rng.Intn(6)),
			Mat:   mat,
			AO:    uint8(rng.Intn(256)),
			Light: uint8(rng.Intn(256)),
		}
		if got := mesh.UnpackQuad(want.Pack()); got != want {
			t.Fatalf("往返不一致:\n实际 %+v\n期望 %+v", got, want)
		}
	}
}

// TestQuadPackRoundTripCarriesCornerHeights 对四个角高度的全部合法组合穷举往返。
//
// 取值域是「顶面顶点的 7..=15」加「非顶面顶点的 0」；角 2 必须非零，否则整条
// quad 会被判成普通 quad（判别规则见 UnpackQuad）。任一角写错位偏移、或高位
// 那两个角在解包时被丢掉，往返都会对不上——后者正是上传路径上的数据丢失。
func TestQuadPackRoundTripCarriesCornerHeights(t *testing.T) {
	values := []uint8{0, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	checked := 0
	for _, c0 := range values {
		for _, c1 := range values {
			for _, c2 := range values {
				if c2 == 0 {
					continue
				}
				for _, c3 := range values {
					want := mesh.Quad{
						X: 1, Y: 2, Z: 3, W: 1, H: 1,
						Face: mesh.FacePosY, Mat: 0xBEEF, AO: 0x5A, Light: 0xA5,
						Corners: [4]uint8{c0, c1, c2, c3},
					}
					packed := want.Pack()
					if got := mesh.UnpackQuad(packed); got != want {
						t.Fatalf("往返不一致:\n实际 %+v\n期望 %+v", got, want)
					}
					// quad 实例格式 MUST 保持 8 字节：bit 63 必须仍然空着。
					if packed>>63 != 0 {
						t.Fatalf("corners=%v 占用了 bit 63: %#016x", want.Corners, packed)
					}
					checked++
				}
			}
		}
	}
	if want := len(values) * len(values) * (len(values) - 1) * len(values); checked != want {
		t.Fatalf("穷举组合数=%d，想要 %d", checked, want)
	}
}

// TestQuadPackRejectsOutOfRangeCorner 锁定 Go 与 Rust 的 pack 断言对称：
// 角高度只有 4 bit，越界值会串进相邻字段，两侧都必须当场炸而不是静默写坏。
func TestQuadPackRejectsOutOfRangeCorner(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("角高度 16 未被拒绝")
		}
	}()
	mesh.Quad{X: 1, Y: 1, Z: 1, W: 1, H: 1, Corners: [4]uint8{0, 0, 16, 0}}.Pack()
}

func TestQuadPackFitsIn55Bits(t *testing.T) {
	full := mesh.Quad{
		X: 15, Y: 15, Z: 15, W: 16, H: 16,
		Face: 5, Mat: 0xFFFF, AO: 0xFF, Light: 0xFF,
	}
	if v := full.Pack(); v>>55 != 0 {
		t.Fatalf("打包用满了高位: %#016x，第 55 位以上应为空", v)
	}
}

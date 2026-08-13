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
		want := mesh.Quad{
			X:     uint8(rng.Intn(16)),
			Y:     uint8(rng.Intn(16)),
			Z:     uint8(rng.Intn(16)),
			W:     uint8(rng.Intn(16) + 1),
			H:     uint8(rng.Intn(16) + 1),
			Face:  mesh.Face(rng.Intn(6)),
			Mat:   uint16(rng.Intn(65536)),
			AO:    uint8(rng.Intn(256)),
			Light: uint8(rng.Intn(256)),
		}
		if got := mesh.UnpackQuad(want.Pack()); got != want {
			t.Fatalf("往返不一致:\n实际 %+v\n期望 %+v", got, want)
		}
	}
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

package worldgen

import (
	"math"
	"testing"
)

func TestPerlinIsDeterministic(t *testing.T) {
	a := newPerlin(42)
	b := newPerlin(42)
	for i := 0; i < 1000; i++ {
		x := float64(i) * 0.137
		z := float64(i) * 0.911
		if a.at(x, z) != b.at(x, z) {
			t.Fatalf("同种子在 (%f,%f) 处结果不同", x, z)
		}
	}
}

func TestPerlinDiffersBySeed(t *testing.T) {
	a, b := newPerlin(1), newPerlin(2)
	same := 0
	for i := 0; i < 1000; i++ {
		x := float64(i) * 0.137
		if a.at(x, 0.5) == b.at(x, 0.5) {
			same++
		}
	}
	if same > 50 {
		t.Fatalf("两个种子有 %d/1000 个采样点相同，种子未生效", same)
	}
}

func TestPerlinRangeAndZeroAtLattice(t *testing.T) {
	p := newPerlin(7)
	for i := 0; i < 10000; i++ {
		x := float64(i)*0.0173 - 80
		z := float64(i)*0.0291 - 40
		v := p.at(x, z)
		if v < -1.5 || v > 1.5 {
			t.Fatalf("噪声在 (%f,%f) 处越界: %f", x, z, v)
		}
	}
	for x := -5; x <= 5; x++ {
		for z := -5; z <= 5; z++ {
			if v := p.at(float64(x), float64(z)); math.Abs(v) > 1e-9 {
				t.Fatalf("格点 (%d,%d) 处噪声 = %f，应为 0", x, z, v)
			}
		}
	}
}

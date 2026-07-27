// Package worldgen 生成地形。
//
// 本包必须是确定性的：同种子 + 同区块坐标 = 完全相同的输出（spec §4.3）。
// 因此包内禁止使用 map 遍历顺序、time、以及未播种的随机源。
package worldgen

import (
	"math"
	"math/rand"
)

// perlin 是经典 2D Perlin 噪声。
type perlin struct {
	// perm 是 0..255 的一个置换重复两遍，共 512 项。
	perm [512]int
}

// newPerlin 用给定种子构造噪声。
func newPerlin(seed int64) *perlin {
	var p perlin
	base := make([]int, 256)
	for i := range base {
		base[i] = i
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(256, func(i, j int) { base[i], base[j] = base[j], base[i] })
	for i := 0; i < 512; i++ {
		p.perm[i] = base[i&255]
	}
	return &p
}

// fade 是 Perlin 的六次插值曲线 6t⁵-15t⁴+10t³。
func fade(t float64) float64 { return t * t * t * (t*(t*6-15) + 10) }

func lerp(a, b, t float64) float64 { return a + t*(b-a) }

// grad2 从哈希值取一个 2D 梯度方向并与偏移做点积。
func grad2(h int, x, y float64) float64 {
	switch h & 3 {
	case 0:
		return x + y
	case 1:
		return -x + y
	case 2:
		return x - y
	default:
		return -x - y
	}
}

// at 返回 (x,z) 处的噪声值，大致落在 [-1, 1]。
func (p *perlin) at(x, z float64) float64 {
	fx, fz := math.Floor(x), math.Floor(z)
	xi, zi := int(fx)&255, int(fz)&255
	xf, zf := x-fx, z-fz
	u, v := fade(xf), fade(zf)

	aa := p.perm[p.perm[xi]+zi]
	ab := p.perm[p.perm[xi]+zi+1]
	ba := p.perm[p.perm[xi+1]+zi]
	bb := p.perm[p.perm[xi+1]+zi+1]

	x1 := lerp(grad2(aa, xf, zf), grad2(ba, xf-1, zf), u)
	x2 := lerp(grad2(ab, xf, zf-1), grad2(bb, xf-1, zf-1), u)
	return lerp(x1, x2, v)
}

// fbm 是分形布朗运动：叠加多个倍频的噪声，得到大尺度起伏与小尺度细节。
func (p *perlin) fbm(x, z float64, octaves int, lacunarity, gain float64) float64 {
	var sum, norm float64
	amp := 1.0
	freq := 1.0
	for i := 0; i < octaves; i++ {
		sum += p.at(x*freq, z*freq) * amp
		norm += amp
		freq *= lacunarity
		amp *= gain
	}
	return sum / norm
}

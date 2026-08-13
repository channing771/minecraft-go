package mesh_test

import (
	"testing"

	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/world"
)

// oracleMaskCell 必须可比较，贪心合并靠 == 判断两格能否合并。
type oracleMaskCell struct {
	used  bool
	mat   uint16
	ao    uint8
	light uint8
}

// meshSectionGoOracle 把一个区段转换成贪心合并后的四边形集合。
func meshSectionGoOracle(n *world.Neighborhood, reg mesh.Registry, light *goLightScratch) []mesh.Quad {
	if light == nil {
		panic("mesh: nil light scratch")
	}
	if id, single := n.Center.Blocks.IsUniform(); single && id == world.AirID {
		return nil
	}
	light.build(n, reg)
	out := make([]mesh.Quad, 0, 256)

	for face := mesh.Face(0); face < 6; face++ {
		axis := face.Axis()
		u := (axis + 1) % 3
		v := (axis + 2) % 3

		step := -1
		if face.Positive() {
			step = 1
		}

		for slice := 0; slice < 16; slice++ {
			var mask [16][16]oracleMaskCell
			any := false

			for vi := 0; vi < 16; vi++ {
				for ui := 0; ui < 16; ui++ {
					var p [3]int
					p[axis], p[u], p[v] = slice, ui, vi

					id := n.Center.Blocks.Get(p[0], p[1], p[2])
					q := p
					q[axis] += step
					if !reg.FaceVisible(id, n.At(q[0], q[1], q[2])) {
						continue
					}
					mask[vi][ui] = oracleMaskCell{
						used:  true,
						mat:   reg.Material(id, face),
						ao:    computeAOOracle(n, reg, p, axis, u, v, step),
						light: light.at(q[0], q[1], q[2]),
					}
					any = true
				}
			}
			if !any {
				continue
			}

			for vi := 0; vi < 16; vi++ {
				for ui := 0; ui < 16; {
					c := mask[vi][ui]
					if !c.used {
						ui++
						continue
					}

					w := 1
					for ui+w < 16 && mask[vi][ui+w] == c {
						w++
					}

					h := 1
				grow:
					for vi+h < 16 {
						for k := 0; k < w; k++ {
							if mask[vi+h][ui+k] != c {
								break grow
							}
						}
						h++
					}

					for dv := 0; dv < h; dv++ {
						for du := 0; du < w; du++ {
							mask[vi+dv][ui+du] = oracleMaskCell{}
						}
					}

					var p [3]int
					p[axis], p[u], p[v] = slice, ui, vi
					out = append(out, mesh.Quad{
						X: uint8(p[0]), Y: uint8(p[1]), Z: uint8(p[2]),
						W: uint8(w), H: uint8(h),
						Face: face, Mat: c.mat, AO: c.ao, Light: c.light,
					})
					ui += w
				}
			}
		}
	}
	return out
}

// computeAOOracle 计算一个面 4 个角的环境光遮蔽，每角 2 位。
func computeAOOracle(n *world.Neighborhood, reg mesh.Registry, p [3]int, axis, u, v, step int) uint8 {
	base := p
	base[axis] += step

	solid := func(du, dv int) int {
		q := base
		q[u] += du
		q[v] += dv
		if reg.Opaque(n.At(q[0], q[1], q[2])) {
			return 1
		}
		return 0
	}

	corners := [4][2]int{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
	var out uint8
	for i, c := range corners {
		s1 := solid(c[0], 0)
		s2 := solid(0, c[1])
		level := 0
		if s1 != 1 || s2 != 1 {
			level = 3 - (s1 + s2 + solid(c[0], c[1]))
		}
		out |= uint8(level) << (i * 2)
	}
	return out
}

func TestGoMeshOracleBuildsDeterministicFixture(t *testing.T) {
	center := world.NewSection()
	center.Blocks.Set(8, 8, 8, world.BlockID(2))
	quads := meshSectionGoOracle(solidNeighbors(center), testRegistry{}, newGoLightScratch())
	if len(quads) != 6 {
		t.Fatalf("Go oracle 孤立方块产生了 %d 个面，想要 6", len(quads))
	}
}

package world_test

import (
	"fmt"
	"math/rand"
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/world"
)

// naiveSection 是对拍用的参考实现：最笨但显然正确。
type naiveSection [core.BlocksPerSection]world.BlockID

func idx(x, y, z int) int { return (y << 8) | (z << 4) | x }

// TestPalettedContainerMatchesNaive 用随机操作序列对拍参考实现。
// 覆盖三态之间的全部升降级路径。
func TestPalettedContainerMatchesNaive(t *testing.T) {
	// 三档不同的方块种类数，分别把容器逼进单值态、索引态、直接态。
	for _, variety := range []int{1, 12, 200, 5000} {
		variety := variety
		t.Run(fmt.Sprintf("variety=%d", variety), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(variety)))
			c := world.NewPalettedContainer(world.AirID)
			var want naiveSection

			for op := 0; op < 20000; op++ {
				x, y, z := rng.Intn(16), rng.Intn(16), rng.Intn(16)
				id := world.BlockID(rng.Intn(variety) + 1)
				c.Set(x, y, z, id)
				want[idx(x, y, z)] = id

				// 每隔一段触发一次降级，确保 Compact 不破坏内容。
				if op%1000 == 999 {
					c.Compact()
				}
			}

			for y := 0; y < 16; y++ {
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						if got := c.Get(x, y, z); got != want[idx(x, y, z)] {
							t.Fatalf("variety=%d (%d,%d,%d): Get = %d，想要 %d",
								variety, x, y, z, got, want[idx(x, y, z)])
						}
					}
				}
			}
		})
	}
}

// TestPalettedContainerUniformCostsNothing 验证单值态不分配位数据。
// 这是 100 MB 内存预算成立的前提（spec §4.1）。
func TestPalettedContainerUniformCostsNothing(t *testing.T) {
	c := world.NewPalettedContainer(world.AirID)
	if _, ok := c.IsUniform(); !ok {
		t.Fatal("新建容器应为单值态")
	}
	if n := c.PayloadBytes(); n > 64 {
		t.Fatalf("单值态占用 %d 字节，应接近 0", n)
	}

	// 写入同一个值不应触发升级。
	c.Set(3, 4, 5, world.AirID)
	if _, ok := c.IsUniform(); !ok {
		t.Fatal("写入相同值后仍应为单值态")
	}

	// 写入不同值触发升级，全部改回后 Compact 应降级回单值态。
	c.Set(3, 4, 5, world.BlockID(7))
	if _, ok := c.IsUniform(); ok {
		t.Fatal("写入不同值后不应还是单值态")
	}
	c.Set(3, 4, 5, world.AirID)
	c.Compact()
	if _, ok := c.IsUniform(); !ok {
		t.Fatal("内容重新统一后 Compact 应降级回单值态")
	}
}

// TestPalettedContainerCloneIsDeep 验证 COW 依赖的深拷贝语义（spec §4.3）。
func TestPalettedContainerCloneIsDeep(t *testing.T) {
	c := world.NewPalettedContainer(world.AirID)
	c.Set(1, 2, 3, world.BlockID(42))

	cp := c.Clone()
	cp.Set(1, 2, 3, world.BlockID(99))

	if got := c.Get(1, 2, 3); got != 42 {
		t.Fatalf("修改副本影响了原件: 原件 Get = %d，想要 42", got)
	}
	if got := cp.Get(1, 2, 3); got != 99 {
		t.Fatalf("副本 Get = %d，想要 99", got)
	}
}

func BenchmarkPalettedContainerGet(b *testing.B) {
	rng := rand.New(rand.NewSource(1))
	c := world.NewPalettedContainer(world.AirID)
	for i := 0; i < 4096; i++ {
		c.Set(rng.Intn(16), rng.Intn(16), rng.Intn(16), world.BlockID(rng.Intn(64)+1))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = c.Get(i&15, (i>>4)&15, (i>>8)&15)
	}
}

package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// cropSampleKey 是抽样测试的基准区块键。取非零坐标是刻意的：坐标全零时
// 「哈希没有吃进区块坐标」与「吃进了但值恰好是 0」无法区分。
var cropSampleKey = core.ChunkKey{
	Dimension: core.Overworld,
	Pos:       core.ChunkPos{X: 3, Z: -7},
}

// TestSampleCellsIsPureAndDeterministic 锁定 spec「相同输入重放结果一致」的
// 最内层前提：抽样是纯函数，同一组输入任意次调用都给出逐元素相同的结果。
func TestSampleCellsIsPureAndDeterministic(t *testing.T) {
	first := sampleCells(0x5eed, 1234, cropSampleKey, 5, 8, nil)
	second := sampleCells(0x5eed, 1234, cropSampleKey, 5, 8, nil)
	if len(first) != 8 || len(second) != 8 {
		t.Fatalf("抽样条数 first=%d second=%d，想要 8", len(first), len(second))
	}
	for index := range first {
		if first[index] != second[index] {
			t.Fatalf("第 %d 条抽样不可复现：%d 与 %d", index, first[index], second[index])
		}
	}
	// 复用调用方缓冲不得改变结果——生产路径正是靠复用 scratch 避免每 tick 分配。
	buffer := make([]int, 0, 8)
	buffer = sampleCells(0x5eed, 1234, cropSampleKey, 5, 8, buffer)
	for index := range first {
		if buffer[index] != first[index] {
			t.Fatalf("复用缓冲改变了第 %d 条抽样：%d 与 %d", index, buffer[index], first[index])
		}
	}
}

// TestSampleCellsVariesWithEveryInput 逐个输入证明它**真的**被折进了哈希。
//
// 这条守卫的判据是位置性而非存在性：只断言「输出落在 0..4095 内」在任何实现下
// 都成立（包括恒返回 0 的实现），等于没测。因此每个输入维度都必须给出一个
// 只改它一处、其余不变的对照，并要求整条抽样结果不同。
func TestSampleCellsVariesWithEveryInput(t *testing.T) {
	const (
		seed     = int64(0x5eed)
		tick     = uint64(1234)
		sectionY = 5
		count    = 8
	)
	base := sampleCells(seed, tick, cropSampleKey, sectionY, count, nil)
	otherDimension := cropSampleKey
	otherDimension.Dimension++
	otherX := cropSampleKey
	otherX.Pos.X++
	otherZ := cropSampleKey
	otherZ.Pos.Z++
	for _, tc := range []struct {
		name   string
		sample []int
	}{
		{"不同世界种子", sampleCells(seed+1, tick, cropSampleKey, sectionY, count, nil)},
		{"不同 tick", sampleCells(seed, tick+1, cropSampleKey, sectionY, count, nil)},
		{"不同维度", sampleCells(seed, tick, otherDimension, sectionY, count, nil)},
		{"不同区块 X", sampleCells(seed, tick, otherX, sectionY, count, nil)},
		{"不同区块 Z", sampleCells(seed, tick, otherZ, sectionY, count, nil)},
		{"不同区段索引", sampleCells(seed, tick, cropSampleKey, sectionY+1, count, nil)},
	} {
		if equalInts(base, tc.sample) {
			t.Errorf("%s 抽出了与基准完全相同的 %v，该输入没有被折进哈希", tc.name, base)
		}
	}
	// 同一区段内不同的 i 也必须给出不同的格：全部相同意味着「抽 n 格」退化成
	// 「抽 1 格重复 n 次」，随机 tick 的推进速率会静默变成 1/n。
	distinct := map[int]struct{}{}
	for _, cell := range base {
		distinct[cell] = struct{}{}
	}
	if len(distinct) < count-1 {
		t.Errorf("同一区段的 %d 条抽样只有 %d 个不同格：%v", count, len(distinct), base)
	}
}

// TestSampleCellsCoversSectionWithoutBias 证明分布非退化：抽出的格既覆盖整个
// 区段的下标空间，也不集中在少数几个值上。
func TestSampleCellsCoversSectionWithoutBias(t *testing.T) {
	const ticks = 4096
	distinct := make(map[int]struct{}, ticks)
	var buffer []int
	for tick := range uint64(ticks) {
		buffer = sampleCells(0, tick, cropSampleKey, 0, 1, buffer)
		cell := buffer[0]
		if cell < 0 || cell >= core.BlocksPerSection {
			t.Fatalf("tick %d 抽到越界下标 %d", tick, cell)
		}
		distinct[cell] = struct{}{}
	}
	// 4096 次独立均匀抽样在 4096 个格上的期望不同值数约为 4096(1-1/e) ≈ 2589。
	// 下界取 2000 留足偏差余量，同时足以否掉「恒定值」「只在少数格间循环」
	// 「只覆盖低位若干格」这几类退化实现。
	if len(distinct) < 2000 {
		t.Fatalf("%d 次抽样只覆盖了 %d 个不同格，分布退化", ticks, len(distinct))
	}
}

// equalInts 报告两个下标切片是否逐元素相同。
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

// TestGrowCropIsExhaustivelySpecified 穷举 8 个小麦阶段 × {湿, 干} ×
// {露天, 遮蔽} 共 32 种输入，逐条写死期望值。
//
// 期望值是**手写常量**而不是「再跑一遍实现」：后者在任何实现下都成立，等于
// 没测。表里 WheatStage7ID 那两行（湿且露天）是成熟不推进这条规则的唯一守卫。
func TestGrowCropIsExhaustivelySpecified(t *testing.T) {
	stages := [8]core.BlockID{
		core.WheatStage0ID, core.WheatStage1ID, core.WheatStage2ID, core.WheatStage3ID,
		core.WheatStage4ID, core.WheatStage5ID, core.WheatStage6ID, core.WheatStage7ID,
	}
	// wantNext[stage] 是「湿且露天」时的期望结果；其余三种环境一律不变。
	wantNext := [8]core.BlockID{
		core.WheatStage1ID, core.WheatStage2ID, core.WheatStage3ID, core.WheatStage4ID,
		core.WheatStage5ID, core.WheatStage6ID, core.WheatStage7ID, core.WheatStage7ID,
	}
	wantChanged := [8]bool{true, true, true, true, true, true, true, false}
	for stage, block := range stages {
		for _, env := range []struct {
			wet, sky bool
		}{{true, true}, {true, false}, {false, true}, {false, false}} {
			next, changed := growCrop(block, env.wet, env.sky)
			expectNext, expectChanged := block, false
			if env.wet && env.sky {
				expectNext, expectChanged = wantNext[stage], wantChanged[stage]
			}
			if next != expectNext || changed != expectChanged {
				t.Errorf(
					"growCrop(阶段 %d, wet=%v, sky=%v) = (%d, %v)，想要 (%d, %v)",
					stage, env.wet, env.sky, next, changed, expectNext, expectChanged,
				)
			}
		}
	}
}

// TestGrowCropLeavesNonCropsAlone 证明非作物编号一律原样返回。耕地必须在其中
// ——它与作物编号相邻，「落在农业编号区间内就推进」这类实现只有这条会红。
func TestGrowCropLeavesNonCropsAlone(t *testing.T) {
	for _, block := range []core.BlockID{
		core.AirID, core.StoneID, core.DirtID, core.GrassID,
		core.FarmlandDryID, core.FarmlandWetID,
		core.WaterSourceID, core.WaterLevel1ID,
	} {
		next, changed := growCrop(block, true, true)
		if next != block || changed {
			t.Errorf("growCrop(%s) = (%d, %v)，非作物必须原样返回", blockLabel(block), next, changed)
		}
	}
}

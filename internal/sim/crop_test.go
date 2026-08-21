package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
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

// —— 生长与干湿的端到端夹具 ——
//
// 三条设计约束，每条都直接对应一类假绿：
//
//  1. **概率必须置满**。CropGrowthChancePercent 不设 100 时，「作物没长」的断言
//     在「本来就没通过概率判定」的情况下也会绿，用例静默失去意义。
//  2. **抽样必须能打到夹具格**。RandomTicksPerSection 置到上限 64，单格每 tick
//     被抽中的概率约 1/64，cropFixtureTicks 个 tick 内期望被抽中约 9 次。抽样是
//     纯哈希、seed 与 tick 序列都固定，因此这不是"大概率"而是确定的事实。
//  3. **每条「不发生」都要有只改一个条件的对照**。对照会发生变化，就证明夹具格
//     确实被抽中过——否则「没长」既可能是规则拒绝，也可能是根本没看过这一格。
const cropFixtureTicks = 600

// 夹具坐标。取区块中央而不是原点：9×9 的湿润窗口必须整个落在唯一一个已就绪
// 区块内，否则「相邻区块未加载按无水」会混进判定；同时也避开出生点所在的列。
var (
	cropFixtureFarmland = core.BlockPos{X: 8, Y: 1, Z: 8}
	cropFixtureCrop     = core.BlockPos{X: 8, Y: 2, Z: 8}
	cropFixtureCover    = core.BlockPos{X: 8, Y: 3, Z: 8}
)

// cropFlatChunk 生成作物测试用的平坦区块：y=-1 与 y=0 两层石头地基、y=1 草皮，
// 其余空气。
//
// 地基是必需的：夹具里的水源可能放在耕地的下一层（y=0），下方必须实心，否则
// 水会一路流到世界底部，「范围内有水」这个前置在第一个 tick 就自己消失。两层
// 而不是一层，正是为了让 y=0 这一层也能放水。列顶仍是 y=1，露天判定不受影响。
func cropFlatChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	for x := range core.SectionSize {
		for z := range core.SectionSize {
			chunk.SetBlock(x, -1, z, core.StoneID)
			chunk.SetBlock(x, 0, z, core.StoneID)
			chunk.SetBlock(x, 1, z, core.GrassID)
		}
	}
	chunk.Compact()
	return chunk
}

// placeContainedWater 放一格水源，并把它周围仍是空气的六个面邻格填成石头。
//
// 这样这一格水在流体规则下是不动点：源永不自然消失，六个邻格都不可被等级 1
// 替换，evalCell 对它产出空写入集合。夹具里的水**必须原地不动**——它一旦流走，
// 「范围内有水」这个前置就在第一个 tick 自己消失，用例会因为前置蒸发而绿。
// 复用 internal/sim/fluid.go 的 fluidNeighbors，不另写一份邻格表。
func placeContainedWater(t *testing.T, engine *Engine, position core.BlockPos) {
	t.Helper()
	for _, neighbor := range fluidNeighbors(position) {
		if cropBlockAt(t, engine, neighbor) == core.AirID {
			engine.SetBlockForTest(neighbor, core.StoneID)
		}
	}
	engine.SetBlockForTest(position, core.WaterSourceID)
}

// cropFixture 描述一个作物夹具的四个可独立开关的条件。对照用例只改其中一个。
type cropFixture struct {
	// farmland 是 cropFixtureFarmland 处写入的耕地编号。
	farmland core.BlockID
	// crop 是 cropFixtureCrop 处写入的作物编号；AirID 表示不放作物。
	crop core.BlockID
	// waterDistance > 0 时在耕地的 waterDY 层、该水平距离处放一个水源；
	// 0 表示不放水。
	waterDistance int32
	// waterDY 是水源相对耕地的层偏移：0 同层、+1 上一层、-1 下一层。规格写的
	// 是「同层或上一层」，三个取值恰好覆盖窗口的内侧、上边界与下边界外侧。
	waterDY int32
	// cover 是作物正上方写入的方块编号；AirID 表示不遮挡。
	//
	// 类型是方块编号而不是 bool：规格说的是「之上不存在**任何非空气**方块」，
	// 而实现读的是 world.Chunk.HighestOpaque——那个名字里的 Opaque 名不副实
	// （它返回的是最高**非空气**方块）。只用石头遮挡的话，「不透明才算遮挡」
	// 这种实现照样全绿，名字与规格之间的缝没人守。
	cover core.BlockID
}

// readyCropWorld 构造一名 active 玩家与一个已 Ready 的平坦区块，并把
// RandomTicksPerSection 与 CropGrowthChancePercent 置到端到端测试的设置。
//
// viewRadius 取 0，因此只有区块 (0,0) 会就绪：活动兴趣范围里的其余 24 个 key
// 一直是 Absent，被 advanceCrops 跳过。这让单 tick 的考察量固定为
// 24 个区段 × 64 条抽样，测试跑得起 600 个 tick。
func readyCropWorld(t *testing.T) (*Engine, SessionID) {
	t.Helper()
	engine, sessions := readyCropWorldAt(t, core.ChunkPos{})
	return engine, sessions[0]
}

// readyCropWorldAt 是 readyCropWorld 的多锚点版本：每个 anchor 注册一名玩家，
// 因此**恰好**这些区块会就绪。跨区块用例靠它同时让两个区块进入 Ready，再靠
// UnregisterSession 让其中一个离开。
func readyCropWorldAt(t *testing.T, anchors ...core.ChunkPos) (*Engine, []SessionID) {
	t.Helper()
	t.Cleanup(func() { SetTunables(DefaultTunables()) })
	tunables := DefaultTunables()
	tunables.RandomTicksPerSection = 64
	tunables.CropGrowthChancePercent = 100
	SetTunables(tunables)

	engine := NewEngine(0, 0, 0)
	sessions := make([]SessionID, 0, len(anchors))
	for index, anchor := range anchors {
		session := SessionID(index + 1)
		sessions = append(sessions, session)
		engine.RegisterSession(session, core.Overworld, anchor)
	}
	for range 8 {
		result := engine.Step()
		for _, key := range result.Acquire {
			engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
		}
		for _, key := range result.Generate {
			engine.SubmitGenerated(GeneratedChunk{
				Dimension: key.Dimension,
				Pos:       key.Pos,
				Chunk:     cropFlatChunk(key.Pos),
			})
		}
	}
	for _, session := range sessions {
		if player, ok := engine.Player(session); !ok || !player.Ready {
			t.Fatalf("会话 %d 的玩家未 Ready: %+v", session, player)
		}
	}
	return engine, sessions
}

// setCropGrowthChance 改写生效中的生长概率。readyCropWorldAt 已经登记了恢复
// 默认值的 Cleanup，因此这里不必再登记一次。
func setCropGrowthChance(percent uint8) {
	tunables := ActiveTunables()
	tunables.CropGrowthChancePercent = percent
	SetTunables(tunables)
}

// applyCropFixture 把夹具写进已就绪的世界。
func applyCropFixture(t *testing.T, engine *Engine, fixture cropFixture) {
	t.Helper()
	engine.SetBlockForTest(cropFixtureFarmland, fixture.farmland)
	if fixture.crop != core.AirID {
		engine.SetBlockForTest(cropFixtureCrop, fixture.crop)
	}
	if fixture.waterDistance > 0 {
		water := cropFixtureFarmland
		water.X += fixture.waterDistance
		water.Y += fixture.waterDY
		placeContainedWater(t, engine, water)
	}
	if fixture.cover != core.AirID {
		if core.IsFluid(fixture.cover) {
			// 流体遮挡物同样要封住，否则它会流走，「被遮挡」这个前置在第一个
			// tick 就消失。placeContainedWater 会先把六个空气邻格填成石头，
			// 其中「下方」那一格是作物本身（非空气、非流体，不可替换），因此
			// 不会被填掉，也不会被水冲走。
			placeContainedWater(t, engine, cropFixtureCover)
		} else {
			engine.SetBlockForTest(cropFixtureCover, fixture.cover)
		}
	}
}

// newCropWorld 一步构造世界并写入夹具。
func newCropWorld(t *testing.T, fixture cropFixture) *Engine {
	t.Helper()
	engine, _ := readyCropWorld(t)
	applyCropFixture(t, engine, fixture)
	return engine
}

// cropBlockAt 读取主世界某格的权威方块，区块未就绪时直接失败。
func cropBlockAt(t *testing.T, engine *Engine, position core.BlockPos) core.BlockID {
	t.Helper()
	block, ready := engine.dimensions[core.Overworld].BlockAt(position)
	if !ready {
		t.Fatalf("方块 %+v 所在区块未就绪", position)
	}
	return block
}

// stepUntilBlock 推进权威 tick 直到 position 变成 want，返回花掉的 tick 数；
// 到 cropFixtureTicks 仍未变成 want 时返回 (0, false)。
func stepUntilBlock(
	engine *Engine, position core.BlockPos, want core.BlockID,
) (ticks int, ok bool) {
	for tick := 1; tick <= cropFixtureTicks; tick++ {
		engine.Step()
		block, ready := engine.dimensions[core.Overworld].BlockAt(position)
		if ready && block == want {
			return tick, true
		}
	}
	return 0, false
}

// stepCropTicks 推进固定 tick 数。
func stepCropTicks(engine *Engine) {
	for range cropFixtureTicks {
		engine.Step()
	}
}

// assertCropGrowth 断言夹具格上的作物「相对起始阶段是否推进过」。
//
// 断言的是**阶段号的大小关系**而不是某个具体阶段：cropFixtureTicks 个 tick 里
// 这一格会被抽中若干次，具体停在哪一阶段取决于抽中次数，钉死具体阶段等于把
// 哈希序列的实现细节写进期望值。而「推进过 / 没推进过」正是 Scenario 要问的。
func assertCropGrowth(t *testing.T, engine *Engine, start core.BlockID, wantGrowth bool) {
	t.Helper()
	got := cropBlockAt(t, engine, cropFixtureCrop)
	if !core.IsCrop(got) {
		t.Fatalf("夹具格上是 %s，已经不是作物", blockLabel(got))
	}
	grew := core.CropStage(got) > core.CropStage(start)
	if grew != wantGrowth {
		t.Fatalf("%d 个 tick 后作物从 %s 变成 %s（推进=%v），想要推进=%v",
			cropFixtureTicks, blockLabel(start), blockLabel(got), grew, wantGrowth)
	}
}

// —— Scenario：作物按时间推进阶段，且只在露天与湿润时生长 ——

// TestExposedWetCropAdvancesStage 覆盖 Scenario「露天且湿润的作物推进阶段」。
func TestExposedWetCropAdvancesStage(t *testing.T) {
	engine := newCropWorld(t, cropFixture{
		farmland:      core.FarmlandWetID,
		crop:          core.WheatStage0ID,
		waterDistance: 4,
	})
	ticks, ok := stepUntilBlock(engine, cropFixtureCrop, core.WheatStage1ID)
	if !ok {
		t.Fatalf(
			"%d 个 tick 后作物仍是 %s，露天湿润的作物必须推进阶段",
			cropFixtureTicks, blockLabel(cropBlockAt(t, engine, cropFixtureCrop)),
		)
	}
	t.Logf("第 %d 个 tick 推进到阶段 1", ticks)
	// 耕地必须始终是湿的：范围内的水一直在，干湿双向转换不得把它误判成干。
	if got := cropBlockAt(t, engine, cropFixtureFarmland); got != core.FarmlandWetID {
		t.Fatalf("耕地变成了 %s，范围内有水时必须保持湿耕地", blockLabel(got))
	}
}

// TestCoveredCropDoesNotGrow 覆盖 Scenario「被遮挡的作物不生长」。
//
// 四条子用例共用同一个夹具构造，**只差 cover 一个字段**：对照必须长，
// 被遮挡的必须不长。没有对照的话，「不长」在「600 个 tick 里恰好没抽中这一格」
// 时同样成立，断言与规则无关。
//
// 三种遮挡物缺一不可。规格写的是「之上不存在**任何非空气**方块」，而实现读的
// 是 world.Chunk.HighestOpaque——那个名字里的 Opaque 名不副实（它返回的是最高
// 非空气方块）。只用石头的话，「只有不透明方块才算遮挡」这种实现照样全绿：
// 玻璃是非空气、非不透明的 cutout 类，水是非空气、非不透明的流体，两者正好
// 卡在名字与规格之间的那道缝上。
func TestCoveredCropDoesNotGrow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cover      core.BlockID
		wantGrowth bool
	}{
		{"对照：无遮挡必须推进", core.AirID, true},
		{"正上方是石头时不推进", core.StoneID, false},
		{"正上方是玻璃时不推进", core.GlassID, false},
		{"正上方是水时不推进", core.WaterSourceID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newCropWorld(t, cropFixture{
				farmland:      core.FarmlandWetID,
				crop:          core.WheatStage0ID,
				waterDistance: 4,
				cover:         tc.cover,
			})
			stepCropTicks(engine)
			assertCropGrowth(t, engine, core.WheatStage0ID, tc.wantGrowth)
		})
	}
}

// TestCropOnDryFarmlandDoesNotGrow 覆盖 Scenario「干耕地上的作物不生长」。
//
// 同样是「只改一个条件」的成对用例：对照在范围内放水（耕地保持湿），
// 主用例不放水（耕地保持干），其余完全相同。
//
// **farmland 与 waterDistance 必须一起改，这不是"改了两个条件"。** 干湿是由
// 邻近流体双向决定的：只改 farmland 不放水，湿耕地会被第一次随机 tick 改回干；
// 只放水不改 farmland，干耕地会被改成湿。两个字段合起来才是「耕地的湿度」这
// 一个条件，拆开任何一半都会被双向转换自动改回去。后人请勿"修正"成单字段。
func TestCropOnDryFarmlandDoesNotGrow(t *testing.T) {
	for _, tc := range []struct {
		name          string
		farmland      core.BlockID
		waterDistance int32
		wantGrowth    bool
	}{
		{"对照：湿耕地上必须推进", core.FarmlandWetID, 4, true},
		{"干耕地上不推进", core.FarmlandDryID, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newCropWorld(t, cropFixture{
				farmland:      tc.farmland,
				crop:          core.WheatStage0ID,
				waterDistance: tc.waterDistance,
			})
			stepCropTicks(engine)
			assertCropGrowth(t, engine, core.WheatStage0ID, tc.wantGrowth)
		})
	}
}

// TestMatureCropStaysMature 覆盖 Scenario「成熟作物不再推进」。
//
// 对照是同一夹具下的阶段 6：它必须推进到阶段 7，证明这一格在 600 个 tick 里
// 确实被抽中过；而阶段 7 必须停在阶段 7。
func TestMatureCropStaysMature(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start core.BlockID
		want  core.BlockID
	}{
		{"对照：阶段 6 推进到阶段 7", core.WheatStage6ID, core.WheatStage7ID},
		{"阶段 7 保持阶段 7", core.WheatStage7ID, core.WheatStage7ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newCropWorld(t, cropFixture{
				farmland:      core.FarmlandWetID,
				crop:          tc.start,
				waterDistance: 4,
			})
			stepCropTicks(engine)
			if got := cropBlockAt(t, engine, cropFixtureCrop); got != tc.want {
				t.Fatalf("%d 个 tick 后作物是 %s，想要 %s",
					cropFixtureTicks, blockLabel(got), blockLabel(tc.want))
			}
		})
	}
}

// —— Scenario：耕地的干湿由邻近流体决定并双向转换 ——

// TestFarmlandTurnsWetWithWaterInRange 覆盖 Scenario「水源在范围内使耕地变湿」。
func TestFarmlandTurnsWetWithWaterInRange(t *testing.T) {
	engine := newCropWorld(t, cropFixture{
		farmland:      core.FarmlandDryID,
		crop:          core.AirID,
		waterDistance: 4,
	})
	if _, ok := stepUntilBlock(engine, cropFixtureFarmland, core.FarmlandWetID); !ok {
		t.Fatalf("%d 个 tick 后耕地仍是 %s，范围内有水时必须变湿",
			cropFixtureTicks, blockLabel(cropBlockAt(t, engine, cropFixtureFarmland)))
	}
}

// TestFarmlandTurnsDryAfterWaterRemoved 覆盖 Scenario「水被移除后耕地变干」。
//
// 夹具**先证明它湿过**再移除水：若起手就是干耕地，「改不改都是干」，断言恒真。
func TestFarmlandTurnsDryAfterWaterRemoved(t *testing.T) {
	engine := newCropWorld(t, cropFixture{
		farmland:      core.FarmlandDryID,
		crop:          core.AirID,
		waterDistance: 4,
	})
	if _, ok := stepUntilBlock(engine, cropFixtureFarmland, core.FarmlandWetID); !ok {
		t.Fatalf("前置失败：耕地始终没有变湿，「变干」无从谈起")
	}
	water := cropFixtureFarmland
	water.X += 4
	engine.SetBlockForTest(water, core.AirID)
	if _, ok := stepUntilBlock(engine, cropFixtureFarmland, core.FarmlandDryID); !ok {
		t.Fatalf("%d 个 tick 后耕地仍是 %s，范围内无水时必须变干",
			cropFixtureTicks, blockLabel(cropBlockAt(t, engine, cropFixtureFarmland)))
	}
}

// TestFarmlandWetnessRangeBoundary 覆盖 Scenario「范围外的水不产生湿润」。
//
// 四条子用例把湿润窗口的**四个边界**各钉一颗钉子，每一对都只差一个字段：
//
//   - 水平方向：距离 4 湿、距离 5 不湿。只测距离 5 的话，夹具在距离 4 处也没有
//     水，「不湿」在任何半径实现下都成立（包括半径写成 0 的实现）。
//   - 垂直方向：上一层湿、只在下一层不湿。规格写的是「同层**或上一层**」，
//     而所有正向夹具的水都放在同层——上界删掉（只看同层）与下界放宽（连下一层
//     也算）这两种实现，在只有同层夹具时都照样全绿。
func TestFarmlandWetnessRangeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		distance int32
		dy       int32
		want     core.BlockID
	}{
		{"同层距离 4 的水使耕地变湿", 4, 0, core.FarmlandWetID},
		{"同层距离 5 的水不使耕地变湿", 5, 0, core.FarmlandDryID},
		{"上一层距离 4 的水使耕地变湿", 4, +1, core.FarmlandWetID},
		{"只在下一层的水不使耕地变湿", 4, -1, core.FarmlandDryID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newCropWorld(t, cropFixture{
				farmland:      core.FarmlandDryID,
				crop:          core.AirID,
				waterDistance: tc.distance,
				waterDY:       tc.dy,
			})
			stepCropTicks(engine)
			if got := cropBlockAt(t, engine, cropFixtureFarmland); got != tc.want {
				t.Fatalf("%d 个 tick 后耕地是 %s，想要 %s",
					cropFixtureTicks, blockLabel(got), blockLabel(tc.want))
			}
		})
	}
}

// —— Scenario：生长推进完全确定且成本与作物数量无关 ——

// cropFieldWater 是作物田夹具里那一格水源的位置：它在田正中，因此距离 4 以内的
// 耕地会变湿、更远的会变干，一次夹具同时覆盖两个方向的转换。
var cropFieldWater = core.BlockPos{X: 8, Y: 1, Z: 8}

// plantCropField 在整个区块的 y=1 铺满耕地、y=2 铺满阶段 0 的小麦，并在正中
// 放一格水源，共 255 株作物。
func plantCropField(engine *Engine) {
	for x := range int32(core.SectionSize) {
		for z := range int32(core.SectionSize) {
			ground := core.BlockPos{X: x, Y: 1, Z: z}
			if ground == cropFieldWater {
				engine.SetBlockForTest(ground, core.WaterSourceID)
				continue
			}
			engine.SetBlockForTest(ground, core.FarmlandDryID)
			engine.SetBlockForTest(core.BlockPos{X: x, Y: 2, Z: z}, core.WheatStage0ID)
		}
	}
}

// TestCropGrowthReplaysIdentically 覆盖 Scenario「相同输入重放结果一致」。
//
// 比的是整块区块的 Hash 而不是某一格：逐格一致这条契约在任何一格上都成立，
// 用 Hash 一次覆盖 24 × 4096 格。同时断言「跑完之后的 Hash 与跑之前不同」——
// 否则两个什么都没发生的世界也会一致，断言恒真。
func TestCropGrowthReplaysIdentically(t *testing.T) {
	const replayTicks = 200
	key := core.ChunkKey{Dimension: core.Overworld}
	run := func() (before, after [32]byte) {
		engine, _ := readyCropWorld(t)
		plantCropField(engine)
		before, _, ok := engine.ChunkHash(key)
		if !ok {
			t.Fatalf("区块 %+v 未就绪", key)
		}
		for range replayTicks {
			engine.Step()
		}
		after, _, ok = engine.ChunkHash(key)
		if !ok {
			t.Fatalf("区块 %+v 未就绪", key)
		}
		return before, after
	}
	firstBefore, firstAfter := run()
	secondBefore, secondAfter := run()
	if firstBefore != secondBefore {
		t.Fatalf("两次的初始世界就不同，夹具本身不确定")
	}
	if firstAfter == firstBefore {
		t.Fatalf("%d 个 tick 里世界一动没动，重放一致的断言恒真", replayTicks)
	}
	if firstAfter != secondAfter {
		t.Fatalf("重放 %d 个 tick 后区块 Hash 不同：%x 与 %x",
			replayTicks, firstAfter, secondAfter)
	}
}

// TestCropTickCostIsIndependentOfCropCount 覆盖 Scenario
// 「作物数量增加不改变单 tick 考察量」。
//
// 两个世界的区段数完全相同，只差作物数：一个 0 株、一个 255 株。夹具必须这样
// 取——两个世界作物数相同的话，考察量当然相等，断言恒真。
func TestCropTickCostIsIndependentOfCropCount(t *testing.T) {
	barren, _ := readyCropWorld(t)
	planted, _ := readyCropWorld(t)
	plantCropField(planted)

	barren.Step()
	planted.Step()

	if barren.cropCellsExamined == 0 {
		t.Fatal("空世界一格都没考察，两边相等的断言恒真")
	}
	if barren.cropCellsExamined != planted.cropCellsExamined {
		t.Fatalf("考察量随作物数量变化：0 株世界 %d 格，255 株世界 %d 格",
			barren.cropCellsExamined, planted.cropCellsExamined)
	}
	// 考察量必须正好是「已就绪区块数 × 区段数 × 每区段抽样数」。这条把
	// 「相等」升级成「等于一个与作物无关的解析式」，堵住"两边都退化成 0"
	// 以外的其他共同漂移。
	want := core.SectionsPerChunk * 64
	if barren.cropCellsExamined != want {
		t.Fatalf("单 tick 考察量 %d，想要 %d（1 个已就绪区块 × %d 区段 × 64 抽样）",
			barren.cropCellsExamined, want, core.SectionsPerChunk)
	}
}

// —— 生长概率(CropGrowthChancePercent) ——

// cropRollPositions 是概率判定测试用的一组位置：坐标含负值与零，避免"只在正
// 坐标上正确"的实现蒙混过关。
var cropRollPositions = []core.BlockPos{
	{X: 0, Y: 0, Z: 0},
	{X: 8, Y: 1, Z: 8},
	{X: -3, Y: 70, Z: 21},
	{X: 1024, Y: -60, Z: -1024},
}

// TestCropGrowthRollHonoursEndpoints 守住两个端点的规范语义：0 = 永不推进，
// 100 = 抽中即推进。
//
// 「0 = 永不推进」在端到端层面很难与"恰好没抽中"区分，所以必须在纯函数层面
// 钉死；「100 恒真」则是全部端到端夹具的前提，它自己不能只靠夹具来证明。
func TestCropGrowthRollHonoursEndpoints(t *testing.T) {
	for _, position := range cropRollPositions {
		for tick := range uint64(64) {
			if cropGrowthRoll(0x5eed, tick, core.Overworld, position, 0) {
				t.Fatalf("概率 0 在 tick %d、%+v 上通过了判定", tick, position)
			}
			if !cropGrowthRoll(0x5eed, tick, core.Overworld, position, 100) {
				t.Fatalf("概率 100 在 tick %d、%+v 上未通过判定", tick, position)
			}
		}
	}
}

// cropRollStream 把 ticks 个 tick 的概率判定结果收成一个布尔序列，供下面几条
// 「两条序列必须不同」的守卫比较。
func cropRollStream(
	seed int64, dimension core.DimensionID, position core.BlockPos, percent uint8, ticks int,
) []bool {
	stream := make([]bool, ticks)
	for tick := range ticks {
		stream[tick] = cropGrowthRoll(seed, uint64(tick), dimension, position, percent)
	}
	return stream
}

// equalBools 报告两个布尔序列是否逐元素相同。
func equalBools(a, b []bool) bool {
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

// TestCropGrowthRollAtFiftyIsDeterministicAndIndependent 覆盖**默认配置下唯一
// 会执行的那段哈希**（默认 CropGrowthChancePercent = 50，两个端点都短路掉了，
// 端到端夹具又一律把它设成 100 或 0，所以这段代码此前一次都没被测试跑过）。
//
// 四条断言，每条守一个独立性质：
//
//  1. 可复现——同输入两次逐元素相同，这是「相同输入重放一致」在概率侧的前提；
//  2. 分布非退化——1000 次里通过 400..600 次。恒 true / 恒 false / 只在少数
//     tick 上翻转的实现都会红，而只断言"有 true 也有 false"不会；
//  3. **维度被折进哈希**——两个维度的判定序列必须不同。core.BlockPos 不带维度，
//     不折的话两个世界里同坐标的作物每 tick 拿到逐位相同的判定；
//  4. **与抽样不是同一条哈希流**——这是 cropGrowthRollSalt 存在的理由。它守的
//     是"概率判定直接复用抽样哈希"这种同源实现，不是 salt 常量本身的取值。
func TestCropGrowthRollAtFiftyIsDeterministicAndIndependent(t *testing.T) {
	const (
		seed  = int64(0x5eed)
		ticks = 1000
	)
	position := core.BlockPos{X: 8, Y: 1, Z: 8}

	stream := cropRollStream(seed, core.Overworld, position, 50, ticks)
	if !equalBools(stream, cropRollStream(seed, core.Overworld, position, 50, ticks)) {
		t.Fatal("同输入两次的概率判定序列不同，判定不是纯函数")
	}

	passed := 0
	for _, pass := range stream {
		if pass {
			passed++
		}
	}
	if passed < 400 || passed > 600 {
		t.Fatalf("%d 次判定通过 %d 次，50%% 的分布退化了", ticks, passed)
	}

	if equalBools(stream, cropRollStream(seed, core.Overworld+1, position, 50, ticks)) {
		t.Fatal("两个维度的概率判定序列逐位相同，维度没有被折进哈希")
	}

	// 抽样流按同一组 (seed, tick) 取出一个格下标，再折成同样 50% 的布尔序列。
	// 两条流必须不是同一个函数。
	var buffer []int
	sampling := make([]bool, ticks)
	key := core.ChunkKey{Dimension: core.Overworld, Pos: position.Chunk()}
	for tick := range ticks {
		buffer = sampleCells(seed, uint64(tick), key, position.SectionIndex(), 1, buffer)
		sampling[tick] = buffer[0]%100 < 50
	}
	if equalBools(stream, sampling) {
		t.Fatal("概率判定与抽样是同一条哈希流，salt 没有起作用")
	}
}

// TestZeroGrowthChanceNeverAdvancesCrop 覆盖「0 = 永不推进」的端到端语义。
//
// 夹具起手是**干**耕地加范围内的水：于是耕地必须在测试期间转成湿。这条附带
// 断言是本用例的反空转手段——它证明随机 tick 一直在照常抽样，「作物没长」因此
// 只可能是概率判定拒绝，不可能是抽样停摆。对照把概率改成 100，同夹具必须长。
func TestZeroGrowthChanceNeverAdvancesCrop(t *testing.T) {
	for _, tc := range []struct {
		name       string
		percent    uint8
		wantGrowth bool
	}{
		{"对照：概率 100 必须推进", 100, true},
		{"概率 0 永不推进", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := newCropWorld(t, cropFixture{
				farmland:      core.FarmlandDryID,
				crop:          core.WheatStage0ID,
				waterDistance: 4,
			})
			setCropGrowthChance(tc.percent)
			stepCropTicks(engine)
			if got := cropBlockAt(t, engine, cropFixtureFarmland); got != core.FarmlandWetID {
				t.Fatalf("耕地是 %s，想要湿耕地——随机 tick 没在照常抽样，"+
					"「作物没长」的断言失去意义", blockLabel(got))
			}
			assertCropGrowth(t, engine, core.WheatStage0ID, tc.wantGrowth)
		})
	}
}

// —— 跨区块湿润与「相邻区块未加载按无水」 ——

// 跨区块夹具：耕地在世界 x=0（区块 (0,0) 的局部 x=0），它的 9×9 湿润窗口
// x ∈ [-4, 4] 因此跨进区块 (-1,0)；水放在 x=-2，**只存在于邻块里**。
var (
	cropCrossFarmland = core.BlockPos{X: 0, Y: 1, Z: 8}
	cropCrossWater    = core.BlockPos{X: -2, Y: 1, Z: 8}
	cropCrossChunk    = core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: -1}}
)

// TestFarmlandWetnessCrossesChunkBoundary 覆盖湿润窗口跨区块的两半语义，
// 两半互相咬合、缺一条都会让另一条失去意义：
//
//   - **邻块已加载**：窗口必须真的读进邻块。跳过跨区块邻格的实现在这一半红。
//   - **邻块未加载**：按「无水」保守处理。夹具让邻块离开活动兴趣范围后转为
//     非 Ready，**而水方块本身一格都没动**——这正是该约定唯一可观察的后果。
//     把未加载读作"可能有水"的实现在这一半红。
//
// 只写第一半的话，「未加载按无水」没人守；只写第二半的话，一个从不跨区块读的
// 实现（永远判干）照样全绿。
func TestFarmlandWetnessCrossesChunkBoundary(t *testing.T) {
	engine, sessions := readyCropWorldAt(t, core.ChunkPos{}, core.ChunkPos{X: -1})
	engine.SetBlockForTest(cropCrossFarmland, core.FarmlandDryID)
	placeContainedWater(t, engine, cropCrossWater)

	if _, ok := stepUntilBlock(engine, cropCrossFarmland, core.FarmlandWetID); !ok {
		t.Fatalf("邻块已加载时耕地仍是 %s：湿润窗口没有读进相邻区块",
			blockLabel(cropBlockAt(t, engine, cropCrossFarmland)))
	}

	// 注销锚在 (-1,0) 的会话：该区块随即离开活动兴趣范围与订阅集合。
	// **水方块没有被删除**，改变的只有"它所在的区块还在不在线上"。
	engine.UnregisterSession(sessions[1])
	engine.Step()
	if info, ok := engine.ChunkInfo(cropCrossChunk); ok && info.State == ChunkReady {
		t.Fatalf("邻块仍是 Ready（State=%d），本用例根本没造出「未加载」条件", info.State)
	}
	if _, ok := stepUntilBlock(engine, cropCrossFarmland, core.FarmlandDryID); !ok {
		t.Fatalf("邻块卸载后耕地仍是 %s：未加载的邻块被当成了有水",
			blockLabel(cropBlockAt(t, engine, cropCrossFarmland)))
	}
}

package sim

import (
	"os"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

// 本文件是变更 fluid-presentation-survival 任务组 10「流动前沿性能复测」的测量
// 夹具。`fluidEnabled` 默认翻为 true 之后，流体从实验特性变成所有玩家都会跑到的
// 代码路径，因此需要在真实权威 tick 上复测流动前沿的单 tick 成本。
//
// 三条纪律，改动本文件时必须保持：
//
//  1. **数值只记录，不做门禁。**这里没有任何「耗时必须小于 X」的断言，也绝不
//     允许为了让数字好看去调 FluidUpdatesPerTick / FluidRescanCellsPerTick 等
//     tunable——那是一条上报项，不是一次修改。
//  2. **每个耗时数字必须附带规模坐标。**一次「测了但没测到风险区间」的测量与
//     不测等价，而它看起来像测过了。所以每条样本都记录该 tick 的 q.pending
//     规模，报告里同时打印峰值规模，供与 F1 记录的 20 万项风险区间对照。
//  3. **默认跳过。**这些用例会构造 25 个区块的满水世界并跑数百个 tick，耗时
//     远超常规单测；只有显式设置环境变量 MORNLEA_FLUID_PERF=1 时才运行，
//     常规 `go test ./...` 不受影响。

// fluidPerfEnv 是启用本文件全部测量用例的环境变量名。
const fluidPerfEnv = "MORNLEA_FLUID_PERF"

// requireFluidPerf 在未显式启用测量时跳过用例。
func requireFluidPerf(t *testing.T) {
	t.Helper()
	if os.Getenv(fluidPerfEnv) != "1" {
		t.Skipf("性能测量用例默认跳过；设置 %s=1 后运行", fluidPerfEnv)
	}
}

const (
	// damWaterTop 是大坝场景里蓄水体的顶层 y（水从 y=1 铺到这一层）。
	damWaterTop = 40
	// damWallTop 是坝体石墙的顶层 y，必须高于水面，否则水会直接漫顶，
	// 「挖开坝体」这个动作就不再是前沿展开的唯一触发源。
	damWallTop = 48
	// shelfY 是瀑布场景里悬崖平台的高度：水源铺在 shelfY+1 上，从平台
	// 边缘越过后一路下落到地面，形成持续的水柱。
	shelfY = 100
)

// damChunk 生成大坝场景的区块：y=0 一层石头地面；世界 X<0 的整片区域从 y=1
// 铺到 damWaterTop 全是水源（蓄水体）；世界 X==0 是一道从 y=1 到 damWallTop
// 的石墙（坝体）；X>0 是空的下游河谷。
//
// 蓄水体的西侧与南北两侧靠推进范围边界封闭（范围外读作 core.BarrierID），
// 因此坝体未破时整个水体都是重扫的不动点，队列会彻底排空——测量前的「静止
// 态」是真的静止，而不是「还没扫到」。
func damChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	baseX := int(pos.X) << core.SectionShift
	for localX := range core.SectionSize {
		worldX := baseX + localX
		for localZ := range core.SectionSize {
			chunk.SetBlock(localX, 0, localZ, core.StoneID)
			switch {
			case worldX < 0:
				for y := int32(1); y <= damWaterTop; y++ {
					chunk.SetBlock(localX, y, localZ, core.WaterSourceID)
				}
			case worldX == 0:
				for y := int32(1); y <= damWallTop; y++ {
					chunk.SetBlock(localX, y, localZ, core.StoneID)
				}
			}
		}
	}
	chunk.Compact()
	return chunk
}

// waterfallChunk 生成瀑布场景的区块：y=0 一层石头地面；世界 X<0 在 y=shelfY
// 有一整层石头平台，平台上一层（shelfY+1）全是水源。平台在 X=-1 处到头，水从
// 那条边缘越过后一路下落约 100 格到地面并向下游铺开，形成持续下落的水柱。
//
// 平台上的水源除了紧贴边缘的那一列之外都是重扫的不动点（下方是石头、四个水平
// 邻格是水），所以重扫结束后队列里只剩下真正的前沿。
func waterfallChunk(pos core.ChunkPos) *world.Chunk {
	chunk := world.NewChunk(pos)
	baseX := int(pos.X) << core.SectionShift
	for localX := range core.SectionSize {
		worldX := baseX + localX
		for localZ := range core.SectionSize {
			chunk.SetBlock(localX, 0, localZ, core.StoneID)
			if worldX >= 0 {
				continue
			}
			chunk.SetBlock(localX, shelfY, localZ, core.StoneID)
			chunk.SetBlock(localX, shelfY+1, localZ, core.WaterSourceID)
		}
	}
	chunk.Compact()
	return chunk
}

// fluidPerfEngine 构造一名玩家 + 一片按 gen 生成的已就绪世界，并把边界重扫跑到
// 排空，返回处于静止态的引擎。
//
// 推进范围取 DropInterestRadius，与流体推进范围（活动兴趣区块）重合：5×5=25
// 个区块。
func fluidPerfEngine(t *testing.T, gen func(core.ChunkPos) *world.Chunk) *Engine {
	t.Helper()
	engine := NewEngine(DropInterestRadius, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	for range 12 {
		result := engine.Step()
		for _, key := range result.Acquire {
			engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
		}
		for _, key := range result.Generate {
			engine.SubmitGenerated(GeneratedChunk{
				Dimension: key.Dimension,
				Pos:       key.Pos,
				Chunk:     gen(key.Pos),
			})
		}
	}
	if player, ok := engine.Player(1); !ok || !player.Ready {
		t.Fatalf("玩家未 Ready: %+v", player)
	}
	for tick := 0; len(engine.fluidRescan.pending) > 0; tick++ {
		if tick > 5000 {
			t.Fatal("边界重扫在 5000 tick 内没有排空")
		}
		engine.Step()
	}
	return engine
}

// fluidPerfAdapter 构造一个指向本 tick 推进范围的 FluidWorld 适配器。
// 只读探针用它当占位（budget=0 时 Advance 根本不会碰世界），破坝时用它写方块
// ——SetBlock 会经 recordChange 汇入变更并触发入队，与采掘走的是同一条路径。
func fluidPerfAdapter(engine *Engine) *fluidWorld {
	return &fluidWorld{
		engine:    engine,
		id:        core.Overworld,
		dimension: engine.dimensions[core.Overworld],
		scope:     engine.fluidScope,
		pending:   make(map[core.ChunkKey]*pendingChunkChanges),
	}
}

// fluidTickSample 是一个权威 tick 的测量样本。每条样本都带规模坐标
// （queueBefore / queueAfter），没有规模坐标的耗时数字在本组不算证据。
type fluidTickSample struct {
	// tick 是本样本对应的权威 tick 序号。
	tick uint64
	// queueBefore / queueAfter 是 Step 前后的 q.pending 项数。
	queueBefore int
	queueAfter  int
	// scan / scanSort 是两个只读探针，都以 budget=0 调 Advance（既不写世界也
	// 不改队列）。**它们量的是 fluid-presentation-survival 任务组 10b 之前的
	// 那版实现**：那时 Advance 每 tick 无条件遍历整张 q.pending（scan，用
	// now=0 调，delay>=1 保证一项都不到期），再收集全部到期项并按全序排序
	// （scanSort，用当前 tick 调），两者相减即得排序自身的开销。
	//
	// 10b 把取批换成最小堆之后，budget=0 意味着取批循环**一次都不执行**，两个
	// 探针双双退化成 O(1)，读数落在几十到几百纳秒。这不是探针失效，恰恰是 10b
	// 要证的结论：「遍历整张 map」与「排序整批到期项」这两项工作已经不存在了。
	// 因此在 10b 之后的报告里，承重的数字是 fluidTail 与 step，scan / scanSort
	// 两列读作「≈0，该工作已被结构性消除」。
	//
	// 保留而不删除，是为了让「修复前 / 修复后」两次测量输出同一张表、可以逐列
	// 对照；测量代码一行未改，改的只是这段说明。
	scan     time.Duration
	scanSort time.Duration
	// fluidTail 是从 phaseFluidAdvance 进入到 Step 返回的墙钟时间，
	// 即 advanceFluids + 容器移动 + 采掘推进 + finishChanges。无命令时后三者
	// 近乎为零，因此这是 advanceFluids 的保守上界。
	fluidTail time.Duration
	// step 是整个权威 tick 的墙钟时间，与 20 TPS 的 50 ms 预算直接可比。
	step time.Duration
}

// measureFluidTicks 推进 ticks 个权威 tick，逐 tick 采样。
//
// 只读探针（scan / scanSort）刻意放在 Step **之前**：它们测的是这一 tick 真正
// 要付的成本，而不是 Step 处理完之后剩下的残余队列。
func measureFluidTicks(t *testing.T, engine *Engine, ticks int) []fluidTickSample {
	t.Helper()
	queue := engine.fluidQueue(core.Overworld)
	delay := uint64(engine.tunables.FluidFlowDelayTicks)
	// 这条守卫最初的理由是「delay=0 会让 now=0 的只读探针变成真处理」。任务组 10b
	// 之后该理由**已不成立**：两个探针都以 budget=0 调 Advance，取批循环一次都
	// 不执行，delay 取什么值都不会让它们动到世界或队列。
	//
	// 守卫本身保留（Ruling 61：只改注释与失效前提的说明，不动测量代码），现在守的
	// 是另一件仍然成立的事：delay=0 时待更新项入队即到期，流动没有合并窗口，
	// 逐 tick 的队列规模与耗时构成都和默认 tunable 下的场景不是同一回事，测出来的
	// 数字不能与其它次测量并排比较。它是**夹具前提**守卫，不是正确性守卫。
	if delay == 0 {
		t.Fatal("FluidFlowDelayTicks=0：入队即到期、没有合并窗口，本次测量与默认 tunable 下的场景不可比")
	}
	adapter := fluidPerfAdapter(engine)

	var phaseAt time.Time
	engine.stepPhaseObserver = func(phase stepPhase) {
		if phase == phaseFluidAdvance {
			phaseAt = time.Now()
		}
	}
	defer func() { engine.stepPhaseObserver = nil }()

	samples := make([]fluidTickSample, 0, ticks)
	for range ticks {
		now := engine.tick.Load()
		before := queue.Len()

		start := time.Now()
		changed := queue.Advance(0, adapter, 0, delay)
		scan := time.Since(start)
		if len(changed) != 0 || queue.Len() != before {
			t.Fatalf("遍历探针改变了状态: changed=%d, Len %d→%d", len(changed), before, queue.Len())
		}

		start = time.Now()
		changed = queue.Advance(now, adapter, 0, delay)
		scanSort := time.Since(start)
		if len(changed) != 0 || queue.Len() != before {
			t.Fatalf("排序探针改变了状态: changed=%d, Len %d→%d", len(changed), before, queue.Len())
		}

		phaseAt = time.Time{}
		start = time.Now()
		engine.Step()
		step := time.Since(start)
		stepEnd := time.Now()
		var tail time.Duration
		if !phaseAt.IsZero() {
			tail = stepEnd.Sub(phaseAt)
		}

		samples = append(samples, fluidTickSample{
			tick:        now,
			queueBefore: before,
			queueAfter:  queue.Len(),
			scan:        scan,
			scanSort:    scanSort,
			fluidTail:   tail,
			step:        step,
		})
	}
	return samples
}

// reportFluidSamples 打印场景的规模坐标与最坏 tick 的耗时构成，并返回队列规模
// 最大的那条样本，供调用方做夹具有效性守卫。
//
// 报告三条不同口径的「最坏」：整 tick 最慢、流体段最慢、队列最大。三者常常不是
// 同一个 tick，只报其中一条会掩盖另外两条——本次复测里「整 tick 最慢」与「队列
// 最大」就落在相差近两千 tick 的两个位置上。
func reportFluidSamples(t *testing.T, name string, samples []fluidTickSample) fluidTickSample {
	t.Helper()
	if len(samples) == 0 {
		t.Fatalf("%s: 没有采到任何样本", name)
	}
	worstStep, worstTail, peakQueue := samples[0], samples[0], samples[0]
	for _, sample := range samples[1:] {
		if sample.step > worstStep.step {
			worstStep = sample
		}
		if sample.fluidTail > worstTail.fluidTail {
			worstTail = sample
		}
		if sample.queueBefore > peakQueue.queueBefore {
			peakQueue = sample
		}
	}
	t.Logf("[%s] 采样 %d 个 tick；队列峰值 %d 项（相对 F1 记录的 20 万项风险区间：%.1f%%）",
		name, len(samples), peakQueue.queueBefore,
		100*float64(peakQueue.queueBefore)/float64(fluidRiskScale))
	for _, item := range []struct {
		label  string
		sample fluidTickSample
	}{
		{"整 tick 最慢", worstStep},
		{"流体段最慢", worstTail},
		{"队列最大", peakQueue},
	} {
		s := item.sample
		// 两个只读探针与真实 Advance 是三次独立的墙钟测量，差值在处理成本
		// 低于测量噪声时可能为负；按 0 记并如实标注，不倒填一个好看的正数。
		t.Logf("[%s] %s: tick=%d 队列 %d→%d 项 | Step=%v 流体段=%v | 遍历 map=%v 排序=%v 处理及其余=%v",
			name, item.label, s.tick, s.queueBefore, s.queueAfter,
			s.step, s.fluidTail, s.scan,
			clampNonNegative(s.scanSort-s.scan), clampNonNegative(s.fluidTail-s.scanSort))
	}
	return peakQueue
}

// clampNonNegative 把差值测量里的负数噪声归零。
func clampNonNegative(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// damMinBreachEnqueue / damMinPeakQueue 是大坝场景的夹具承重下界。
//
// 大坝够不到 20 万项的风险区间是实测事实，因此它用不了 requireRiskScale；但它做得到
// 的那两个数必须被钉住，否则夹具退化时只有 t.Logf 的输出会变、用例照样全绿——那正是
// 本变更反复抓到的空转形态。取值留足余量：实测破坝入队 10,608 项、队列峰值 140,122 项。
const (
	damMinBreachEnqueue = 10_000
	damMinPeakQueue     = 100_000
)

// fluidRiskScale 是 F1（归档变更 authoritative-fluid）记录的残余风险规模：
// 队列约 20 万项时 Queue.Advance 单独就能吃满 20 TPS 的 50 ms tick 预算。
//
// 它是本组全部测量的**规模坐标原点**：一个够不到这个规模的场景对该风险区间
// 什么也没说，而它的报告读起来像做过了。
const fluidRiskScale = 200_000

// requireRiskScale 是夹具有效性守卫：场景没把队列撑进风险区间就直接判失败，
// 而不是安静地报一个「很快」的数字。
func requireRiskScale(t *testing.T, name string, peak fluidTickSample) {
	t.Helper()
	if peak.queueBefore < fluidRiskScale {
		t.Fatalf("%s: 队列峰值只有 %d 项，够不到 %d 项的风险区间，本次测量对该区间无效",
			name, peak.queueBefore, fluidRiskScale)
	}
}

// TestFluidPerfDamBreak 场景一：玩家挖穿大坝。
//
// 坝体未破时整个水体是重扫的不动点、队列排空；随后整面坝墙在同一 tick 内被改成
// 空气（比玩家单格采掘更激进，取的是前沿展开的上界），水向下游河谷倾泻。
func TestFluidPerfDamBreak(t *testing.T) {
	requireFluidPerf(t)
	engine := fluidPerfEngine(t, damChunk)
	queue := engine.fluidQueue(core.Overworld)
	if got := queue.Len(); got != 0 {
		t.Fatalf("破坝前队列应为空（封闭水体是重扫的不动点），实得 %d 项", got)
	}

	// 破坝：整面 X=0 的石墙改空气，走 fluidWorld.SetBlock → recordChange，
	// 与采掘完全同一条入队路径。
	breaker := fluidPerfAdapter(engine)
	const span = (2*DropInterestRadius + 1) * core.SectionSize
	for z := int32(-span / 2); z < span/2; z++ {
		for y := int32(1); y <= damWallTop; y++ {
			breaker.SetBlock(core.BlockPos{X: 0, Y: y, Z: z}, core.AirID)
		}
	}
	breachQueued := queue.Len()
	t.Logf("破坝写入 %d 格，入队后队列 %d 项", int(span)*damWallTop, breachQueued)
	if breachQueued < damMinBreachEnqueue {
		t.Fatalf("大坝: 破坝只入队 %d 项（实测基线 10,608），不足 %d，前沿展开的起点已退化，本次测量无效",
			breachQueued, damMinBreachEnqueue)
	}

	// 与瀑布同样采样 12000 tick。实测本场景在窗口末尾仍未收敛：队列峰值
	// 14.0 万项（风险区间的 70.1%），第 11275 tick 上仍有 13.0 万项在队。
	// 这里刻意**不加** requireRiskScale 守卫——本场景够不到 20 万项是实测事实，
	// 加一条它注定不满足的守卫只会让用例长期红，如实报出 70.1% 这个坐标才是
	// 该有的记录方式。
	//
	// 但「不加做不到的守卫」不等于「不加守卫」：下面按本场景**做得到**的规模设
	// 下界。没有它，将来某改动把大坝夹具退化到几百项时只有日志会变、用例照样绿。
	peak := reportFluidSamples(t, "大坝溃决", measureFluidTicks(t, engine, 12000))
	if peak.queueBefore < damMinPeakQueue {
		t.Fatalf("大坝: 队列峰值只有 %d 项（实测基线 140,122），不足 %d，夹具已退化，本次测量无效",
			peak.queueBefore, damMinPeakQueue)
	}
}

// TestFluidPerfWaterfall 场景二：注水世界里的瀑布。
//
// 悬崖平台上的水源从边缘越过后持续下落约 100 格并在地面铺开；水源不消耗，
// 因此前沿一直在推进，不像大坝那样有个明确的终点。
func TestFluidPerfWaterfall(t *testing.T) {
	requireFluidPerf(t)
	engine := fluidPerfEngine(t, waterfallChunk)
	queue := engine.fluidQueue(core.Overworld)
	if queue.Len() == 0 {
		t.Fatal("瀑布场景在测量开始前队列就是空的，说明边缘水源被误判为不动点，夹具无效")
	}
	t.Logf("重扫排空后队列 %d 项（悬崖边缘前沿）", queue.Len())

	// 12000 tick 不是随手取的：600 tick 时队列只到约 1.7 万项（风险区间的
	// 8.7%），那是一次「测了但没测到风险区间」的空转测量。队列要到约 5700
	// tick 才爬到峰值，因此采样窗口必须覆盖到那里，下面的守卫把这一点钉死。
	requireRiskScale(t, "瀑布", reportFluidSamples(t, "瀑布", measureFluidTicks(t, engine, 12000)))
}

// TestFluidPerfSyntheticRiskScale 合成场景：直接把队列撑到 F1 记录的 20 万项
// 风险区间，测 Advance 在该规模下的单 tick 成本。
//
// **这是合成场景，不是玩法可达性证明**：它不回答「玩家能不能把队列堆到 20 万」
// （那由 10.2 的结构性判定回答），只回答「一旦堆到 20 万，权威 tick 要付多少」。
func TestFluidPerfSyntheticRiskScale(t *testing.T) {
	requireFluidPerf(t)
	engine := fluidPerfEngine(t, damChunk)
	queue := engine.fluidQueue(core.Overworld)
	now, delay := engine.fluidClock()

	// 在推进范围内、水面之上的空气层里逐格入队，直到达到风险规模。
	// 选空气格是刻意的：它让处理阶段（evalCell 对非流体格恒产出空写入）尽可能
	// 便宜，从而把测出来的成本尽量归因到「遍历 + 排序」这段无预算约束的工作上。
	const span = (2*DropInterestRadius + 1) * core.SectionSize
	for y := int32(damWallTop + 1); queue.Len() < fluidRiskScale && y < core.MaxY; y++ {
		for z := int32(-span / 2); z < span/2 && queue.Len() < fluidRiskScale; z++ {
			for x := int32(-span / 2); x < span/2 && queue.Len() < fluidRiskScale; x++ {
				queue.Enqueue(core.BlockPos{X: x, Y: y, Z: z}, now, delay)
			}
		}
	}
	t.Logf("合成队列 %d 项（风险区间 %d 项）", queue.Len(), fluidRiskScale)

	// 只测 delay 个 tick：到期项要等 delay 之后才可处理，而每 tick 只消化
	// FluidUpdatesPerTick 项，队列规模在这段窗口里基本不变。
	requireRiskScale(t, "合成 20 万项",
		reportFluidSamples(t, "合成 20 万项", measureFluidTicks(t, engine, int(delay)+5)))
}

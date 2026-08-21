package sim

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
)

// 本文件是变更 authoritative-farming 任务 8.7 的测量夹具，回答一个问题：
// 作物阶段在**活动兴趣范围满编**（默认服务端上限 8 名玩家、兴趣范围互不重叠）
// 时，单个权威 tick 要付多少墙钟时间。
//
// 两条纪律，改动本文件时必须保持：
//
//  1. **数值只记录，不做门禁。**这里没有任何「耗时必须小于 X」的断言，也绝不
//     允许为了让数字好看去调 RandomTicksPerSection——那是一条上报项，不是一次
//     修改。成本契约本身（「考察量与作物数无关」）由
//     TestCropTickCostIsIndependentOfCropCount 断言，不由这里的耗时数字断言。
//  2. **每个耗时数字必须附带规模坐标。**一次「测了但没测到风险区间」的测量与
//     不测等价，而它看起来像测过了。因此每条 benchmark 都用 ReportMetric 打出
//     本次实际触及的格数（cells/op）与已就绪区块数，耗时永远与这两个坐标一起读。
//
// Benchmark 而不是 gated Test：`go test ./...` 默认不跑 benchmark，无需再加
// fluid_perf_test.go 那样的环境变量门（那里是 Test 函数，不加门会拖垮常规单测）。

const (
	// cropPerfSessions 是满编的会话数，取 server.DefaultConfig 的 MaxPlayers。
	cropPerfSessions = 8
	// cropPerfSessionStride 让相邻会话的兴趣范围**恰好不重叠**：兴趣范围边长是
	// 2*DropInterestRadius+1 个区块，步长取同一个值，于是活动区块总数正好是
	// 会话数 × 25，而不会因为重叠被 activeInterestKeys 的去重悄悄缩水。
	cropPerfSessionStride = 2*DropInterestRadius + 1
	// cropPerfChunks 是满编时的活动区块数：8 × 5 × 5 = 200。
	cropPerfChunks = cropPerfSessions * cropPerfSessionStride * cropPerfSessionStride
)

// cropPerfEngine 构造满编世界：cropPerfSessions 个互不重叠的会话，全部区块用
// cropFlatChunk 生成并推进到 Ready，tunable 保持编译默认值
// （RandomTicksPerSection = 3、CropGrowthChancePercent = 50）。
//
// **刻意读默认值而不是像 readyCropWorldAt 那样把抽样率拉到 64**：本组量的是
// 「玩家在默认配置下真实要付的成本」，把抽样率调高 21 倍会得到一个谁也不会
// 跑到的数字。
func cropPerfEngine(b *testing.B) *Engine {
	b.Helper()
	b.Cleanup(func() { SetTunables(DefaultTunables()) })
	SetTunables(DefaultTunables())

	engine := NewEngine(DropInterestRadius, 0, 0)
	for index := range cropPerfSessions {
		engine.RegisterSession(SessionID(index+1), core.Overworld, core.ChunkPos{
			X: int32(index * cropPerfSessionStride),
		})
	}
	// 12 轮足够把 8 × 25 个区块从 Acquire 走到 Generate 再走到 Ready；
	// 下面的 Ready 断言是这条循环够不够的守卫，不靠"应该够了"。
	for range 12 {
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
	for index := range cropPerfSessions {
		if player, ok := engine.Player(SessionID(index + 1)); !ok || !player.Ready {
			b.Fatalf("会话 %d 的玩家未 Ready: %+v", index+1, player)
		}
	}
	if got := len(engine.activeInterestKeys()); got != cropPerfChunks {
		b.Fatalf("活动区块数 %d，想要满编的 %d——夹具没铺满，本次测量对满编无效",
			got, cropPerfChunks)
	}
	return engine
}

// cropPerfPlant 把 chunks 个活动区块的整层 y=1 改成干耕地、y=2 种上小麦，
// 返回实际种下的株数。
//
// 刻意用**干**耕地而不是湿耕地：干耕地会把 9×9×2 = 162 格的湿润扫描**整个扫完**
// 才得出"无水"（湿耕地一碰到水就短路返回），因此这是耕地分支的最坏成本；同时
// 世界里没有水源意味着 advanceCropCell 对耕地算出的下一状态就是它当前的状态、
// 对作物算出 !wet 不推进，两条分支都**不产生任何方块写入**。于是各条 benchmark
// 之间唯一的变量就是「世界里有多少作物」，不掺进 recordChange 与 pending 累积
// 的成本（runCropPerf 尾部的 pending 守卫钉死这一点）。
func cropPerfPlant(engine *Engine, chunks int) int {
	planted := 0
	for index, key := range engine.activeInterestKeys() {
		if index >= chunks {
			break
		}
		baseX := key.Pos.X << core.SectionShift
		baseZ := key.Pos.Z << core.SectionShift
		for localX := range int32(core.SectionSize) {
			for localZ := range int32(core.SectionSize) {
				x, z := baseX+localX, baseZ+localZ
				engine.SetBlockForTest(core.BlockPos{X: x, Y: 1, Z: z}, core.FarmlandDryID)
				engine.SetBlockForTest(core.BlockPos{X: x, Y: 2, Z: z}, core.WheatStage0ID)
				planted++
			}
		}
	}
	return planted
}

// runCropPerf 逐 tick 调 advanceCrops 并报告耗时与规模坐标。
//
// 直接调 advanceCrops 而不是整个 Step：Step 还包含掉落物、熔炉、流体与
// finishChanges，混在一起的读数无法归因到作物阶段（这也正是 phaseCropAdvance
// 单独登记而不折进 phaseFluidAdvance 的原因）。
//
// tick 每轮 +1 是必要的：抽样是 (seed, tick, 位置) 的纯函数，tick 不动的话
// 每一轮抽到的是同一批格，测出来的是被 CPU 缓存彻底喂熟的最好情况。
func runCropPerf(b *testing.B, engine *Engine, crops int) {
	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		engine.tick.Add(1)
		engine.advanceCrops(pending)
	}
	b.StopTimer()
	if len(pending) != 0 {
		b.Fatalf("夹具产生了 %d 个区块的方块变更，两次测量不再只差「有没有作物」", len(pending))
	}
	want := cropPerfChunks * core.SectionsPerChunk * int(DefaultTunables().RandomTicksPerSection)
	if engine.cropCellsExamined != want {
		b.Fatalf("单 tick 触及 %d 格，想要 %d（%d 区块 × %d 区段 × %d 抽样）",
			engine.cropCellsExamined, want, cropPerfChunks, core.SectionsPerChunk,
			DefaultTunables().RandomTicksPerSection)
	}
	b.ReportMetric(float64(engine.cropCellsExamined), "cells/op")
	b.ReportMetric(float64(cropPerfChunks), "chunks")
	b.ReportMetric(float64(crops), "crops")
}

// BenchmarkCropAdvanceFullInterestBarren 是满编但**一株作物都没有**的基准线。
// benchmark 世界与刚生成的新世界都属于这一类：作物阶段照样每 tick 枚举全部
// 区段，这条读数量的正是「阶段本身」的固定开销。
func BenchmarkCropAdvanceFullInterestBarren(b *testing.B) {
	runCropPerf(b, cropPerfEngine(b), 0)
}

// BenchmarkCropAdvanceFullInterestPlanted 是「几百株作物」的对照组：一个区块
// 整层种满，256 株。
//
// **这一条单独读没有统计功效，必须与 Dense 一起读。**每 tick 抽 14400 格、
// 世界里有 200 × 24 × 4096 ≈ 1966 万格，256 株作物平均每 tick 只被抽中约 0.4
// 次——耗时差落在测量噪声以下是必然的，不是"成本无关"的证据。真正承重的证据
// 是两条：cells/op 逐个相等（runCropPerf 的解析式守卫），以及下面把作物数放大
// 200 倍的 Dense。
func BenchmarkCropAdvanceFullInterestPlanted(b *testing.B) {
	engine := cropPerfEngine(b)
	runCropPerf(b, engine, cropPerfPlant(engine, 1))
}

// BenchmarkCropAdvanceFullInterestDense 把全部 200 个活动区块整层种满：
// 51200 株小麦 + 51200 格干耕地，作物数是 Planted 的 200 倍。
//
// 这条才是「耗时不随作物数增长」的墙钟侧证据：此时每 tick 约有 75 次抽样落在
// 耕地或作物上（而不是 Planted 的 0.4 次），其中落在耕地上的每一次都要付满
// 162 格的湿润扫描。若成本真的随作物数增长，差异会在这个规模上显形。
func BenchmarkCropAdvanceFullInterestDense(b *testing.B) {
	engine := cropPerfEngine(b)
	runCropPerf(b, engine, cropPerfPlant(engine, cropPerfChunks))
}

// BenchmarkCropAdvanceAllFarmland 量的是**每条落在耕地上的抽样**要付多少钱。
//
// 为什么需要它：advanceCrops 的成本契约按「被抽中的格数」计量，而那个数确实
// 与作物数无关（三条 FullInterest benchmark 的 cells/op 逐个相等）。但每格的
// **单价**并不相同——抽中空气或石头是一次方块读取，抽中耕地要再扫 9×9×2 = 162
// 格判湿润。Dense 比 Barren 慢 40% 正是这条单价差的体现。
//
// 这条 benchmark 把世界做成极端：单个区块的 24 个区段全部填满干耕地，于是
// **每一条抽样都落在耕地上**，ns/op ÷ cells/op 就是耕地样本的单价上界。
// 它回答的是「假如玩家把整个活动范围铺成耕地，作物阶段的墙钟上界是多少」，
// 数值只记录（见 design.md 遗留清单 21）。
func BenchmarkCropAdvanceAllFarmland(b *testing.B) {
	b.Cleanup(func() { SetTunables(DefaultTunables()) })
	SetTunables(DefaultTunables())

	engine := NewEngine(0, 0, 0)
	engine.RegisterSession(1, core.Overworld, core.ChunkPos{})
	for range 8 {
		result := engine.Step()
		for _, key := range result.Acquire {
			engine.SubmitAcquired(AcquiredChunk{Key: key, Missing: true})
		}
		for _, key := range result.Generate {
			engine.SubmitGenerated(GeneratedChunk{
				Dimension: key.Dimension, Pos: key.Pos, Chunk: cropFlatChunk(key.Pos),
			})
		}
	}
	if player, ok := engine.Player(1); !ok || !player.Ready {
		b.Fatalf("玩家未 Ready: %+v", player)
	}
	for y := int32(core.MinY); y < int32(core.MaxY); y++ {
		for x := range int32(core.SectionSize) {
			for z := range int32(core.SectionSize) {
				engine.SetBlockForTest(core.BlockPos{X: x, Y: y, Z: z}, core.FarmlandDryID)
			}
		}
	}

	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		engine.tick.Add(1)
		engine.advanceCrops(pending)
	}
	b.StopTimer()
	if len(pending) != 0 {
		b.Fatalf("全耕地世界不该产生方块变更（没有水源，干耕地保持干），实得 %d 个区块", len(pending))
	}
	want := core.SectionsPerChunk * int(DefaultTunables().RandomTicksPerSection)
	if engine.cropCellsExamined != want {
		b.Fatalf("单 tick 触及 %d 格，想要 %d", engine.cropCellsExamined, want)
	}
	b.ReportMetric(float64(engine.cropCellsExamined), "cells/op")
}

package worldgen_test

// 本文件覆盖变更 authoritative-fluid 的「世界生成注水与门控」需求(任务 5.6/5.7)。
//
// 关闭态的基线证明分工:本文件锁住「关闭态不产生任何流体」与「关闭态与
// pre-fluid 语义逐格一致」;generator_test.go 的 TestGenerateChunkGolden 用本
// 变更之前录制的 testdata/golden_seed42.txt 锁住字节级不变。三者合起来构成
// 10.4「视觉 golden 字节不变」的前提。

import (
	"testing"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/worldgen"
)

// fluidTestSeed / fluidTestChunks 是注水断言的语料:含起伏地形、洞穴以上的
// 空气柱与海平面以下的空气格。fixture 前提由各测试自行断言,不靠注释约束。
const fluidTestSeed int64 = 42

var fluidTestChunks = []core.ChunkPos{
	{X: 0, Z: 0}, {X: 1, Z: 0}, {X: -1, Z: -1}, {X: 37, Z: -104}, {X: -8, Z: 5},
}

// seaLevelY 是世界生成的海平面 Y,与 engine `SEA_LEVEL_Y` 及 oracle
// `oracleSeaLevel` 同值。注水范围是 [core.MinY, seaLevelY]。
const seaLevelY int32 = 64

// TestFluidGateOffProducesNoFluid 覆盖 spec 场景「开关关闭时世界与当前基线
// 一致」的后半句:关闭态世界中不存在任何流体方块。
//
// 门控实现为「water 字段填 air 编号」(design D6),一旦门控失效(关闭态也传
// 真实 water 编号),海平面以下的空气格会变成源方块,本断言立刻变红。
func TestFluidGateOffProducesNoFluid(t *testing.T) {
	airBelowSeaLevel := 0
	for _, pos := range fluidTestChunks {
		chunk := worldgen.New(fluidTestSeed, false).GenerateChunk(pos)
		for y := int32(core.MinY); y < core.MaxY; y++ {
			for z := 0; z < core.SectionSize; z++ {
				for x := 0; x < core.SectionSize; x++ {
					id := chunk.BlockAt(x, y, z)
					if core.IsFluid(id) {
						t.Fatalf("关闭态 chunk=%+v (%d,%d,%d) 出现流体 %d", pos, x, y, z, id)
					}
					if id == core.AirID && y <= seaLevelY {
						airBelowSeaLevel++
					}
				}
			}
		}
	}
	// 夹具前提:语料必须真的含海平面以下的空气格,否则"没有流体"是空断言
	// ——门控失效时正是这些格会变成水。
	if airBelowSeaLevel == 0 {
		t.Fatal("夹具失效:语料区块在海平面以下没有空气格,本测试无法证伪门控失效")
	}
}

// TestFluidGateOnFillsSeaLevel 覆盖 spec 场景「开关开启时海平面以下注水」:
// 海平面及其以下、原本为空气的格全部为源方块,海平面以上不含流体。
func TestFluidGateOnFillsSeaLevel(t *testing.T) {
	filled := 0
	for _, pos := range fluidTestChunks {
		dry := worldgen.New(fluidTestSeed, false).GenerateChunk(pos)
		wet := worldgen.New(fluidTestSeed, true).GenerateChunk(pos)
		for y := int32(core.MinY); y < core.MaxY; y++ {
			for z := 0; z < core.SectionSize; z++ {
				for x := 0; x < core.SectionSize; x++ {
					before, after := dry.BlockAt(x, y, z), wet.BlockAt(x, y, z)
					switch {
					case before == core.AirID && y <= seaLevelY:
						if after != core.WaterSourceID {
							t.Fatalf("chunk=%+v (%d,%d,%d) 海平面以下的空气格未注水: %d",
								pos, x, y, z, after)
						}
						filled++
					default:
						// 其余格(含海平面以上的空气)必须原样保留——不只是
						// "不是流体":把它改写成任意别的方块同样是注水越界。
						if after != before {
							t.Fatalf("chunk=%+v (%d,%d,%d) 不该注水却被改写: 关闭=%d 开启=%d",
								pos, x, y, z, before, after)
						}
					}
				}
			}
		}
	}
	if filled == 0 {
		t.Fatal("夹具失效:语料区块没有任何可注水的格")
	}
}

// TestFluidGateKeepsNonFluidCells 覆盖 spec 场景「注水不改变其他生成结果」:
// 非空气且非流体的格在开关两态下逐格一致。
//
// 断言同时统计地表分层、矿石与树木三类方块的出现数量并要求非零:注水在
// 实现上排在橡树写入之后,若这三类里任何一类在语料中缺席,"不受影响"就是
// 一句空话。
func TestFluidGateKeepsNonFluidCells(t *testing.T) {
	var terrain, ores, trees int
	for _, pos := range fluidTestChunks {
		dry := worldgen.New(fluidTestSeed, false).GenerateChunk(pos)
		wet := worldgen.New(fluidTestSeed, true).GenerateChunk(pos)
		for y := int32(core.MinY); y < core.MaxY; y++ {
			for z := 0; z < core.SectionSize; z++ {
				for x := 0; x < core.SectionSize; x++ {
					before, after := dry.BlockAt(x, y, z), wet.BlockAt(x, y, z)
					if before == core.AirID || core.IsFluid(before) {
						continue
					}
					if after != before {
						t.Fatalf("chunk=%+v (%d,%d,%d) 注水改写了非流体格: 关闭=%d 开启=%d",
							pos, x, y, z, before, after)
					}
					switch before {
					case core.IronOreID, core.CoalOreID:
						ores++
					case core.OakLogID, core.LeavesID:
						trees++
					default:
						terrain++
					}
				}
			}
		}
	}
	if terrain == 0 || ores == 0 || trees == 0 {
		t.Fatalf("夹具失效:语料缺少分层/矿石/树木之一(分层=%d 矿石=%d 树木=%d)",
			terrain, ores, trees)
	}
}

package sim_test

import (
	"testing"

	"minecraft-go/internal/core"
	"minecraft-go/internal/sim"
	"minecraft-go/internal/world"
)

var furnaceMaterials = []struct {
	name          string
	input, output core.ItemID
}{
	{"粗铁", core.ItemRawIron, core.ItemIronIngot},
	{"沙子", core.ItemSand, core.ItemGlass},
	{"黏土块", core.ItemClay, core.ItemBrick},
}

// smeltingFurnace 返回一个燃料充足且可以立即推进指定材料的熔炉槽。
func smeltingFurnace(index uint32, materials ...core.ItemID) world.FurnaceSlot {
	input, output := core.ItemRawIron, core.ItemNone
	if len(materials) > 0 {
		input = materials[0]
	}
	if len(materials) > 1 {
		output = materials[1]
	}
	slot := world.FurnaceSlot{
		Generation: 1, Active: true, BlockIndex: index,
		Fuel: core.ItemStack{Item: core.ItemCoal, Count: 1},
	}
	if input != core.ItemNone {
		slot.Input = core.ItemStack{Item: input, Count: 4}
	}
	if output != core.ItemNone {
		slot.Output = core.ItemStack{Item: output, Count: 1}
	}
	return slot
}

// readyFurnacePlayer 构造一个已 Ready 的玩家，并在中心区块放置一个熔炉。
func readyFurnacePlayer(t *testing.T, slot world.FurnaceSlot) (*sim.Engine, sim.SessionID, uint32) {
	t.Helper()
	engine, session := readyFlatPlayer(t)
	index := dropTargetIndex(t)
	key := core.ChunkKey{Dimension: core.Overworld}
	engine.SetBlockForTest(core.BlockPos{}, core.FurnaceID)
	slot.BlockIndex = index
	engine.SetChunkFurnaceForTest(key, 0, slot)
	return engine, session, index
}

func currentFurnace(t *testing.T, engine *sim.Engine, slot int) world.FurnaceSlot {
	t.Helper()
	chunk, _, ok := engine.CloneReadyChunk(core.ChunkKey{Dimension: core.Overworld})
	if !ok {
		t.Fatal("中心区块不可用")
	}
	return chunk.Furnace(slot)
}

func TestFurnaceMaterialsLightFuelAndAdvanceSameTick(t *testing.T) {
	for _, material := range furnaceMaterials {
		t.Run(material.name, func(t *testing.T) {
			engine, _, _ := readyFurnacePlayer(t, smeltingFurnace(0, material.input, core.ItemNone))

			engine.Step()
			got := currentFurnace(t, engine, 0)
			if got.Fuel.Count != 0 {
				t.Fatalf("燃料未被消耗: %+v", got.Fuel)
			}
			if got.BurnTicks != core.FurnaceBurnTicks-1 {
				t.Fatalf("剩余燃烧 tick = %d，想要 %d", got.BurnTicks, core.FurnaceBurnTicks-1)
			}
			if got.ProgressTicks != 1 {
				t.Fatalf("熔炼进度 = %d，想要 1", got.ProgressTicks)
			}
			if got.Input != (core.ItemStack{Item: material.input, Count: 4}) {
				t.Fatalf("点燃时修改了输入: %+v", got.Input)
			}
		})
	}
}

func TestFurnaceProducesMaterialsAtTwoHundredTicks(t *testing.T) {
	for _, material := range furnaceMaterials {
		for _, outputCount := range []uint8{0, 2} {
			name := material.name + "/空输出"
			if outputCount != 0 {
				name = material.name + "/累加同产物"
			}
			t.Run(name, func(t *testing.T) {
				slot := smeltingFurnace(0, material.input, core.ItemNone)
				slot.Fuel = core.ItemStack{}
				slot.ProgressTicks = core.FurnaceSmeltTicks - 1
				slot.BurnTicks = 10
				if outputCount != 0 {
					slot.Output = core.ItemStack{Item: material.output, Count: outputCount}
				}
				engine, _, _ := readyFurnacePlayer(t, slot)

				engine.Step()
				got := currentFurnace(t, engine, 0)
				want := core.ItemStack{Item: material.output, Count: outputCount + 1}
				if got.Output != want || got.Input.Count != 3 || got.ProgressTicks != 0 || got.BurnTicks != 9 {
					t.Fatalf("完成熔炼 = %+v，想要 output=%+v input=3 progress=0 burn=9", got, want)
				}
			})
		}
	}
}

func TestOneCoalYieldsExactlyEightIngots(t *testing.T) {
	slot := smeltingFurnace(0)
	slot.Input = core.ItemStack{Item: core.ItemRawIron, Count: 20}
	engine, _, _ := readyFurnacePlayer(t, slot)

	// 一个煤提供 1600 个燃烧 tick，恰好支持 8 个 200 tick 的铁锭。
	for range core.FurnaceBurnTicks {
		engine.Step()
	}
	got := currentFurnace(t, engine, 0)
	if got.Output.Count != 8 {
		t.Fatalf("一个煤产出 %d 个铁锭，想要 8", got.Output.Count)
	}
	if got.BurnTicks != 0 {
		t.Fatalf("燃烧 tick = %d，想要耗尽", got.BurnTicks)
	}

	// 燃料耗尽后不再推进。
	engine.Step()
	if after := currentFurnace(t, engine, 0); after.Output.Count != 8 || after.ProgressTicks != 0 {
		t.Fatalf("无燃料仍推进: %+v", after)
	}
}

func TestFurnaceMaterialsPauseWithoutWastingFuel(t *testing.T) {
	cases := []struct {
		name          string
		input, output core.ItemID
		outputCount   uint8
	}{
		{"输入为空", core.ItemNone, core.ItemNone, 0},
		{"粗铁输出已满", core.ItemRawIron, core.ItemIronIngot, core.MaxStackCount},
		{"沙子输出已满", core.ItemSand, core.ItemGlass, core.MaxStackCount},
		{"黏土块输出已满", core.ItemClay, core.ItemBrick, core.MaxStackCount},
		{"粗铁输出冲突", core.ItemRawIron, core.ItemGlass, 1},
		{"沙子输出冲突", core.ItemSand, core.ItemBrick, 1},
		{"黏土块输出冲突", core.ItemClay, core.ItemIronIngot, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			slot := smeltingFurnace(0, tc.input, core.ItemNone)
			slot.Output = core.ItemStack{Item: tc.output, Count: tc.outputCount}
			slot.ProgressTicks, slot.BurnTicks = 137, 1463
			engine, _, _ := readyFurnacePlayer(t, slot)

			engine.Step()
			got := currentFurnace(t, engine, 0)
			if got.Input != slot.Input || got.Fuel != slot.Fuel || got.Output != slot.Output ||
				got.ProgressTicks != 137 || got.BurnTicks != 1463 {
				t.Fatalf("暂停时状态变化: %+v", got)
			}
		})
	}
}

func TestFurnaceMaterialsResumeFromStoredValues(t *testing.T) {
	for _, material := range furnaceMaterials {
		t.Run(material.name, func(t *testing.T) {
			slot := smeltingFurnace(0, material.input, material.output)
			slot.Output.Count = core.MaxStackCount
			slot.ProgressTicks, slot.BurnTicks = 137, 1463
			engine, _, _ := readyFurnacePlayer(t, slot)
			engine.Step()
			if got := currentFurnace(t, engine, 0); got.ProgressTicks != 137 {
				t.Fatalf("输出满时未暂停: %+v", got)
			}

			// 取走输出后必须从原值继续，而不是重新开始。
			key := core.ChunkKey{Dimension: core.Overworld}
			resumed := currentFurnace(t, engine, 0)
			resumed.Output = core.ItemStack{}
			engine.SetChunkFurnaceForTest(key, 0, resumed)

			engine.Step()
			got := currentFurnace(t, engine, 0)
			if got.ProgressTicks != 138 || got.BurnTicks != 1462 {
				t.Fatalf("恢复后 progress=%d burn=%d，想要 138/1462", got.ProgressTicks, got.BurnTicks)
			}
		})
	}
}

func TestFurnaceAdvancesOnceWithOverlappingViewers(t *testing.T) {
	engine, _, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	// 第二名玩家的活动范围覆盖同一个区块。
	const second = sim.SessionID(2)
	engine.RegisterPlayer(second, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	for range 4 {
		engine.Step()
		if got := currentFurnace(t, engine, 0); got.ProgressTicks > 4 {
			t.Fatalf("重叠观察重复推进: progress=%d", got.ProgressTicks)
		}
	}
	got := currentFurnace(t, engine, 0)
	if got.ProgressTicks != 4 {
		t.Fatalf("四个 tick 后进度 = %d，想要 4", got.ProgressTicks)
	}
}

func TestFurnacePausesWithoutReadyPlayerInRange(t *testing.T) {
	engine, session, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	engine.Step()
	if got := currentFurnace(t, engine, 0); got.ProgressTicks != 1 {
		t.Fatalf("有玩家时未推进: %+v", got)
	}

	// 最后一名玩家离开后该区块不再属于任何活动范围，熔炉停止推进。
	engine.UnregisterSession(session)
	for range 5 {
		engine.Step()
	}
	key := core.ChunkKey{Dimension: core.Overworld}
	if _, _, ok := engine.CloneReadyChunk(key); ok {
		t.Fatal("无玩家时区块仍处于活动状态")
	}
	if len(engine.ActiveInterestKeysForTest()) != 0 {
		t.Fatal("无 Ready 玩家时仍有活动区块需要推进")
	}
}

func TestFurnaceChunkRevisionRisesOnce(t *testing.T) {
	engine, _, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	key := core.ChunkKey{Dimension: core.Overworld}
	// 同一区块内再放一个熔炉，本 tick 两个都会变化。
	second, ok := world.ChunkBlockIndex(core.BlockPos{X: 2, Y: 0, Z: 2})
	if !ok {
		t.Fatal("第二个熔炉位置没有区块索引")
	}
	engine.SetBlockForTest(core.BlockPos{X: 2, Y: 0, Z: 2}, core.FurnaceID)
	engine.SetChunkFurnaceForTest(key, 1, smeltingFurnace(second))

	_, before, ok := engine.CloneReadyChunk(key)
	if !ok {
		t.Fatal("中心区块不可用")
	}
	engine.Step()
	_, after, _ := engine.CloneReadyChunk(key)
	if after != before+1 {
		t.Fatalf("同区块多炉 revision 从 %d 变为 %d，想要只加一", before, after)
	}
	if currentFurnace(t, engine, 0).ProgressTicks != 1 ||
		currentFurnace(t, engine, 1).ProgressTicks != 1 {
		t.Fatal("两个熔炉未同时推进")
	}
}

// fullFurnaceLoadEngine 构造最坏活动负载：8 名 Ready 玩家共享同一批 25 个区块，
// 每个区块 32 个活动熔炉，输入与燃料充足因此每 tick 都会变化。
func fullFurnaceLoadEngine(tb testing.TB) *sim.Engine {
	tb.Helper()
	// 视野半径 2 让 8 名玩家共享的 25 个兴趣区块全部驻留。
	engine := sim.NewEngine(core.DropInterestRadius, 0)
	for index := range 8 {
		engine.RegisterPlayer(sim.SessionID(index+1), sim.PlayerRestore{
			SpawnDimension: core.Overworld,
			SpawnAnchor:    core.ChunkPos{},
		})
	}
	for range 4 {
		requested := engine.Step()
		submitAcquiredMisses(engine, requested.Acquire)
		for _, key := range requested.Acquire {
			engine.SubmitGenerated(sim.GeneratedChunk{
				Dimension: core.Overworld,
				Pos:       key.Pos,
				Chunk:     generateFlatChunk(key.Pos),
			})
		}
		engine.Step()
	}

	loaded := 0
	for dx := int32(-core.DropInterestRadius); dx <= core.DropInterestRadius; dx++ {
		for dz := int32(-core.DropInterestRadius); dz <= core.DropInterestRadius; dz++ {
			key := core.ChunkKey{Dimension: core.Overworld, Pos: core.ChunkPos{X: dx, Z: dz}}
			if _, _, ok := engine.CloneReadyChunk(key); !ok {
				continue
			}
			loaded++
			for slot := range core.FurnacesPerChunk {
				position := core.BlockPos{
					X: key.Pos.X<<core.SectionShift + int32(slot%16),
					Y: 2,
					Z: key.Pos.Z<<core.SectionShift + int32(slot/16),
				}
				index, ok := world.ChunkBlockIndex(position)
				if !ok {
					tb.Fatalf("熔炉位置 %+v 没有区块索引", position)
				}
				engine.SetBlockForTest(position, core.FurnaceID)
				engine.SetChunkFurnaceForTest(key, slot, world.FurnaceSlot{
					Generation: 1, Active: true, BlockIndex: index,
					Input: core.ItemStack{Item: core.ItemRawIron, Count: core.MaxStackCount},
					Fuel:  core.ItemStack{Item: core.ItemCoal, Count: core.MaxStackCount},
				})
			}
		}
	}
	want := (2*core.DropInterestRadius + 1) * (2*core.DropInterestRadius + 1)
	if loaded != want {
		tb.Fatalf("满负载只加载了 %d 个区块，想要 %d", loaded, want)
	}
	return engine
}

func BenchmarkAdvanceFurnaces6400(b *testing.B) {
	engine := fullFurnaceLoadEngine(b)
	b.ReportAllocs()
	for b.Loop() {
		engine.AdvanceFurnacesForBenchmark()
	}
}

func TestAdvanceFurnacesDoesNotAllocate(t *testing.T) {
	engine := fullFurnaceLoadEngine(t)
	allocations := testing.AllocsPerRun(64, func() {
		engine.AdvanceFurnacesForBenchmark()
	})
	if allocations != 0 {
		t.Fatalf("满负载推进分配 %.1f 次，想要 0", allocations)
	}
}

func openFurnaceCommand(session sim.SessionID, sequence uint64) sim.Command {
	return sim.Command{
		Session:  session,
		Sequence: sequence,
		Kind:     sim.CommandOpenFurnace,
		Pitch:    lookDown,
	}
}

func TestOpenFurnaceRequiresAuthoritativeTarget(t *testing.T) {
	engine, session, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	engine.Enqueue(openFurnaceCommand(session, 2))

	result := engine.Step()
	if len(result.Rejected) != 0 {
		t.Fatalf("命中熔炉仍被拒绝: %+v", result.Rejected)
	}
	if len(result.Furnaces) != 1 {
		t.Fatalf("打开后未发布状态: %+v", result.Furnaces)
	}
	got := result.Furnaces[0]
	if got.Session != session || got.Furnace.Slot != 0 || got.Furnace.Generation != 1 {
		t.Fatalf("发布引用 = %+v", got.Furnace)
	}
	if got.ProgressTicks != 1 {
		t.Fatalf("发布进度 = %d，想要本 tick 推进后的 1", got.ProgressTicks)
	}
}

func TestOpenFurnaceRejectsNonFurnaceTarget(t *testing.T) {
	engine, session := readyFlatPlayer(t)
	engine.Enqueue(openFurnaceCommand(session, 2))

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectNoTarget {
		t.Fatalf("非熔炉目标 result=%+v", result)
	}
	if len(result.Furnaces) != 0 {
		t.Fatalf("被拒绝的打开仍发布状态: %+v", result.Furnaces)
	}
}

func TestOpenFurnaceRejectsPendingSpawn(t *testing.T) {
	engine := sim.NewEngine(0, 0)
	const session = sim.SessionID(1)
	engine.RegisterSession(session, core.Overworld, core.ChunkPos{})
	engine.Enqueue(openFurnaceCommand(session, 1))

	result := engine.Step()
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != sim.RejectPlayerNotReady {
		t.Fatalf("PendingSpawn result=%+v", result)
	}
}

func TestFurnaceViewersShareOneAuthoritativeState(t *testing.T) {
	engine, first, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	const second = sim.SessionID(2)
	engine.RegisterPlayer(second, sim.PlayerRestore{
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	})
	engine.Step()

	engine.Enqueue(openFurnaceCommand(first, 3))
	engine.Enqueue(openFurnaceCommand(second, 3))
	result := engine.Step()
	if len(result.Furnaces) != 2 {
		t.Fatalf("两名查看者未同时收到状态: %+v", result.Furnaces)
	}
	if result.Furnaces[0].Session != first || result.Furnaces[1].Session != second {
		t.Fatalf("发布顺序不稳定: %+v", result.Furnaces)
	}
	left, right := result.Furnaces[0], result.Furnaces[1]
	if left.Furnace != right.Furnace || left.ProgressTicks != right.ProgressTicks ||
		left.BurnTicks != right.BurnTicks || left.Input != right.Input {
		t.Fatalf("两名查看者看到不同状态: %+v vs %+v", left, right)
	}
}

func TestCloseFurnaceStopsPublishing(t *testing.T) {
	engine, session, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	engine.Step()

	engine.Enqueue(sim.Command{
		Session: session, Sequence: 3, Kind: sim.CommandCloseFurnace,
	})
	result := engine.Step()
	if len(result.Furnaces) != 0 || len(result.FurnaceEnds) != 0 {
		t.Fatalf("显式关闭后仍发布: %+v / %+v", result.Furnaces, result.FurnaceEnds)
	}
	if next := engine.Step(); len(next.Furnaces) != 0 {
		t.Fatalf("关闭后继续发布: %+v", next.Furnaces)
	}
}

func TestFurnaceViewEndsExactlyOnceWhenTargetDies(t *testing.T) {
	engine, session, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	engine.Step()

	// 熔炉被停用后 generation 不再匹配，查看关系必须精确关闭一次。
	engine.SetChunkFurnaceForTest(core.ChunkKey{Dimension: core.Overworld}, 0,
		world.FurnaceSlot{Generation: 1})

	result := engine.Step()
	if len(result.FurnaceEnds) != 1 || result.FurnaceEnds[0].Session != session {
		t.Fatalf("目标失效未精确关闭: %+v", result.FurnaceEnds)
	}
	if len(result.Furnaces) != 0 {
		t.Fatalf("失效后仍发布状态: %+v", result.Furnaces)
	}
	if next := engine.Step(); len(next.FurnaceEnds) != 0 {
		t.Fatalf("关闭通知被重复发送: %+v", next.FurnaceEnds)
	}
}

func TestFurnaceViewEndsOnDisconnect(t *testing.T) {
	engine, session, _ := readyFurnacePlayer(t, smeltingFurnace(0))
	engine.Enqueue(openFurnaceCommand(session, 2))
	engine.Step()

	engine.UnregisterSession(session)
	result := engine.Step()
	if len(result.Furnaces) != 0 {
		t.Fatalf("断线后仍向其发布状态: %+v", result.Furnaces)
	}
}

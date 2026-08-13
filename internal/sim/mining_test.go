package sim

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/world"
)

const miningTestPitch = float32(-0.4)

func TestMiningRule(t *testing.T) {
	tests := []struct {
		block       core.BlockID
		held        core.ItemID
		ticks       uint16
		harvestable bool
	}{
		{core.DirtID, core.ItemNone, 5, true},
		{core.GrassID, core.ItemDirt, 5, true},
		{core.StoneID, core.ItemNone, 30, true},
		{core.StoneID, core.ItemBrokenStonePickaxe, 30, true},
		{core.StoneID, core.ItemDirt, 30, false},
		{core.StoneID, core.ItemStonePickaxe, 15, true},
		{core.StoneID, core.ItemIronPickaxe, 8, true},
		{core.StoneBrickID, core.ItemNone, 30, false},
		{core.StoneBrickID, core.ItemStonePickaxe, 15, true},
		{core.FurnaceID, core.ItemIronPickaxe, 8, true},
		{core.CoalOreID, core.ItemStonePickaxe, 15, true},
		{core.CoalOreID, core.ItemBrokenIronPickaxe, 30, false},
		{core.IronOreID, core.ItemIronPickaxe, 8, true},
		{core.IronBlockID, core.ItemNone, 40, false},
		{core.IronBlockID, core.ItemStonePickaxe, 20, false},
		{core.IronBlockID, core.ItemIronPickaxe, 10, true},
		{core.LightBlockID, core.ItemNone, 30, false},
		{core.LightBlockID, core.ItemStonePickaxe, 15, true},
		{core.LightBlockID, core.ItemIronPickaxe, 8, true},
		{core.LightBlockID, core.ItemStone, 30, false},
		{core.BedrockID, core.ItemIronPickaxe, 0, false},
		{core.AirID, core.ItemNone, 0, false},
		{core.BarrierID, core.ItemIronPickaxe, 0, false},
	}
	for _, test := range tests {
		ticks, harvestable := miningRule(test.block, test.held)
		if ticks != test.ticks || harvestable != test.harvestable {
			t.Fatalf("miningRule(%d, %d) = %d, %v，想要 %d, %v",
				test.block, test.held, ticks, harvestable, test.ticks, test.harvestable)
		}
	}
}

func TestCommonBlockMaterialMiningRules(t *testing.T) {
	assertMiningRule := func(block core.BlockID, held core.ItemID, wantTicks uint16, wantHarvestable bool) {
		t.Helper()
		gotTicks, gotHarvestable := miningRule(block, held)
		if gotTicks != wantTicks || gotHarvestable != wantHarvestable {
			t.Fatalf("miningRule(%d,%d)=(%d,%v)，想要 (%d,%v)",
				block, held, gotTicks, gotHarvestable, wantTicks, wantHarvestable)
		}
	}
	for _, block := range []core.BlockID{
		core.SandID, core.GravelID, core.LeavesID, core.GlassID,
		core.WhiteWoolID, core.ClayID, core.SnowBlockID,
	} {
		assertMiningRule(block, core.ItemCoal, 5, true)
	}
	for _, block := range []core.BlockID{core.OakLogID, core.OakPlanksID} {
		assertMiningRule(block, core.ItemCoal, 15, true)
	}
	for _, block := range []core.BlockID{
		core.CobblestoneID, core.SmoothStoneID, core.BrickID,
		core.RoofTileID, core.MossyCobblestoneID,
	} {
		assertMiningRule(block, core.ItemNone, 30, true)
		assertMiningRule(block, core.ItemBrokenStonePickaxe, 30, true)
		assertMiningRule(block, core.ItemStonePickaxe, 15, true)
		assertMiningRule(block, core.ItemIronPickaxe, 8, true)
		assertMiningRule(block, core.ItemCoal, 30, false)
	}
}

func TestMiningProgressPublishesAndIncrementsExactlyOnce(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	session := sessions[0]
	result := engine.Step()

	if len(result.Players) != 1 {
		t.Fatalf("Players=%+v，想要一个", result.Players)
	}
	want := MiningUpdate{
		Active: true, Target: targets[0], ProgressTicks: 1,
		RequiredTicks: 30, Harvestable: true,
	}
	if got := result.Players[0].Mining; got != want {
		t.Fatalf("首 tick Mining=%+v，想要 %+v", got, want)
	}

	engine.Step()
	if got := engine.sessions[session].player.mining.update(); got.ProgressTicks != 2 {
		t.Fatalf("连续命中进度=%+v，想要恰好 2", got)
	}
}

func TestMiningReleaseClearsSameTick(t *testing.T) {
	engine, sessions, _ := readyMiningPlayers(t, 1)
	advanceMiningOnce(engine)
	player := engine.sessions[sessions[0]].player
	player.miningHeld = false
	advanceMiningOnce(engine)
	if player.mining != (playerMiningState{}) {
		t.Fatalf("松键后 mining=%+v，想要零值", player.mining)
	}
}

func TestMiningTargetBlockAndToolChangesRestartAtOne(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Engine, *sessionState, core.BlockPos)
		want   core.BlockPos
	}{
		{
			name: "视角目标变化",
			mutate: func(engine *Engine, session *sessionState, _ core.BlockPos) {
				engine.SetBlockForTest(core.BlockPos{X: 1, Y: 1, Z: 5}, core.StoneID)
				session.player.yaw = -0.3
			},
			want: core.BlockPos{X: 1, Y: 1, Z: 5},
		},
		{
			name: "选中工具变化",
			mutate: func(_ *Engine, session *sessionState, _ core.BlockPos) {
				full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
				session.player.inventory.Hotbar.Slots[0] = core.ItemStack{
					Item: core.ItemStonePickaxe, Count: 1, Durability: full,
				}
			},
			want: core.BlockPos{X: 0, Y: 1, Z: 5},
		},
		{
			name: "方块 ID 变化",
			mutate: func(engine *Engine, _ *sessionState, target core.BlockPos) {
				engine.SetBlockForTest(target, core.DirtID)
			},
			want: core.BlockPos{X: 0, Y: 1, Z: 5},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			advanceMiningOnce(engine)
			session := engine.sessions[sessions[0]]
			test.mutate(engine, session, targets[0])
			advanceMiningOnce(engine)
			got := session.player.mining
			if got.target != test.want || got.progressTicks != 1 {
				t.Fatalf("变化后 mining=%+v，想要目标 %+v 从 1 开始", got, test.want)
			}
		})
	}
}

func TestMiningInvalidTargetsClearWithoutRejection(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Engine, *sessionState, core.BlockPos)
	}{
		{
			name: "超距",
			mutate: func(engine *Engine, session *sessionState, target core.BlockPos) {
				engine.SetBlockForTest(target, core.AirID)
				engine.SetBlockForTest(core.BlockPos{X: 0, Y: 2, Z: 1}, core.StoneID)
				session.player.pitch = 0
			},
		},
		{
			name: "区块未就绪",
			mutate: func(engine *Engine, session *sessionState, target core.BlockPos) {
				engine.SetBlockForTest(target, core.AirID)
				session.player.state.Position = mgl32.Vec3{8.5, 1, 1.5}
				session.player.pitch = 0
				delete(engine.dimensions[core.Overworld].records, core.ChunkPos{Z: -1})
			},
		},
		{
			name: "视野丢失",
			mutate: func(_ *Engine, session *sessionState, _ core.BlockPos) {
				session.hasView = false
			},
		},
		{
			name: "打开熔炉",
			mutate: func(_ *Engine, session *sessionState, _ core.BlockPos) {
				session.viewContainer = true
			},
		},
		{
			name: "reset",
			mutate: func(_ *Engine, session *sessionState, _ core.BlockPos) {
				session.player.reset = true
			},
		},
		{
			name: "基岩",
			mutate: func(engine *Engine, _ *sessionState, target core.BlockPos) {
				engine.SetBlockForTest(target, core.BedrockID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			advanceMiningOnce(engine)
			session := engine.sessions[sessions[0]]
			test.mutate(engine, session, targets[0])
			result := advanceMiningOnce(engine)
			if session.player.mining != (playerMiningState{}) {
				t.Fatalf("无效目标后 mining=%+v，想要零值", session.player.mining)
			}
			if len(result.Rejected) != 0 {
				t.Fatalf("无效目标产生拒绝=%+v", result.Rejected)
			}
		})
	}
}

func TestMiningLifecyclePathsClearIntentAndProgress(t *testing.T) {
	t.Run("beginReset", func(t *testing.T) {
		engine, sessions, _ := readyMiningPlayers(t, 1)
		advanceMiningOnce(engine)
		player := engine.sessions[sessions[0]].player
		player.beginReset()
		if player.miningHeld || player.mining != (playerMiningState{}) {
			t.Fatalf("beginReset 后 held=%v mining=%+v", player.miningHeld, player.mining)
		}
	})

	t.Run("非法输入", func(t *testing.T) {
		engine, sessions, _ := readyMiningPlayers(t, 1)
		advanceMiningOnce(engine)
		session := sessions[0]
		engine.Enqueue(Command{
			Session: session, Sequence: 101, Kind: CommandPlayerInput,
			Yaw: float32(math.NaN()), Mining: true,
		})
		result := engine.Step()
		player := engine.sessions[session].player
		if player.miningHeld || player.mining != (playerMiningState{}) {
			t.Fatalf("非法输入后 held=%v mining=%+v", player.miningHeld, player.mining)
		}
		if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectInvalidInput {
			t.Fatalf("非法输入拒绝=%+v", result.Rejected)
		}
	})

	t.Run("未就绪输入", func(t *testing.T) {
		engine, sessions, _ := readyMiningPlayers(t, 1)
		advanceMiningOnce(engine)
		session := sessions[0]
		player := engine.sessions[session].player
		player.lifecycle = PlayerPendingSpawn
		engine.Enqueue(Command{
			Session: session, Sequence: 102, Kind: CommandPlayerInput, Mining: true,
		})
		result := engine.Step()
		if player.miningHeld || player.mining != (playerMiningState{}) {
			t.Fatalf("未就绪输入后 held=%v mining=%+v", player.miningHeld, player.mining)
		}
		if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectPlayerNotReady {
			t.Fatalf("未就绪拒绝=%+v", result.Rejected)
		}
	})
}

func TestMiningEightSessionsKeepIndependentSortedStates(t *testing.T) {
	engine, _, targets := readyMiningPlayers(t, 8)
	result := advanceMiningOnce(engine)
	engine.publishPlayers(&result)
	if len(result.Players) != 8 {
		t.Fatalf("Players=%d，想要 8", len(result.Players))
	}
	for index, update := range result.Players {
		wantSession := SessionID(index + 1)
		if update.Session != wantSession {
			t.Fatalf("Players[%d].Session=%d，想要 %d", index, update.Session, wantSession)
		}
		want := MiningUpdate{
			Active: true, Target: targets[index], ProgressTicks: 1,
			RequiredTicks: 30, Harvestable: true,
		}
		if update.Mining != want {
			t.Fatalf("session %d Mining=%+v，想要 %+v", update.Session, update.Mining, want)
		}
	}
}

func TestMiningCompletionUsesFixedToolAndDropRules(t *testing.T) {
	tests := []struct {
		name     string
		block    core.BlockID
		held     core.ItemID
		ticks    int
		wantItem core.ItemID
	}{
		{"裸手采石掉落石头", core.StoneID, core.ItemNone, 30, core.ItemStone},
		{"普通物品采石不掉落", core.StoneID, core.ItemDirt, 30, core.ItemNone},
		{"石镐采煤矿", core.CoalOreID, core.ItemStonePickaxe, 15, core.ItemCoal},
		{"石镐采铁矿", core.IronOreID, core.ItemStonePickaxe, 15, core.ItemRawIron},
		{"错误工具采煤矿", core.CoalOreID, core.ItemDirt, 30, core.ItemNone},
		{"错误工具采铁矿", core.IronOreID, core.ItemDirt, 30, core.ItemNone},
		{"石镐采铁块不掉落", core.IronBlockID, core.ItemStonePickaxe, 20, core.ItemNone},
		{"铁镐采铁块", core.IronBlockID, core.ItemIronPickaxe, 10, core.ItemIronBlock},
		{"错误工具采发光块不掉落", core.LightBlockID, core.ItemStone, 30, core.ItemNone},
		{"石镐采发光块", core.LightBlockID, core.ItemStonePickaxe, 15, core.ItemLightBlock},
		{"铁镐采发光块", core.LightBlockID, core.ItemIronPickaxe, 8, core.ItemLightBlock},
		{"裸手采沙子", core.SandID, core.ItemNone, 5, core.ItemSand},
		{"普通物品采原木", core.OakLogID, core.ItemCoal, 15, core.ItemOakLog},
		{"石镐采圆石", core.CobblestoneID, core.ItemStonePickaxe, 15, core.ItemCobblestone},
		{"普通物品采砖块不掉落", core.BrickID, core.ItemCoal, 30, core.ItemNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			target := targets[0]
			engine.SetBlockForTest(target, test.block)
			setMiningHeldItem(engine.sessions[sessions[0]].player, test.held)
			var result TickResult
			for range test.ticks {
				result = advanceMiningOnce(engine)
			}
			record := miningTargetRecord(t, engine, target)
			x, _, z := target.Local()
			if got := record.Chunk.BlockAt(x, target.Y, z); got != core.AirID {
				t.Fatalf("完成后方块=%d，想要空气", got)
			}
			if len(result.Rejected) != 0 {
				t.Fatalf("完成被拒绝=%+v", result.Rejected)
			}
			gotDrops := miningDropTotals(record.Chunk)
			if test.wantItem == core.ItemNone {
				if len(gotDrops) != 0 {
					t.Fatalf("错误工具掉落=%+v，想要空", gotDrops)
				}
			} else if gotDrops[test.wantItem] != 1 || len(gotDrops) != 1 {
				t.Fatalf("掉落=%+v，想要一个 %d", gotDrops, test.wantItem)
			}
		})
	}
}

func TestMiningConsumesOneDurabilityPerBrokenBlock(t *testing.T) {
	tests := []struct {
		name  string
		tool  core.ItemID
		block core.BlockID
		ticks int
	}{
		{name: "石镐", tool: core.ItemStonePickaxe, block: core.CoalOreID, ticks: 15},
		{name: "铁镐", tool: core.ItemIronPickaxe, block: core.IronBlockID, ticks: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			player := engine.sessions[sessions[0]].player
			engine.SetBlockForTest(targets[0], test.block)
			setMiningHeldItem(player, test.tool)
			full, _ := core.ItemMaxDurability(test.tool)

			for range test.ticks {
				advanceMiningOnce(engine)
			}

			got := player.inventory.Hotbar.Slots[0]
			if got != (core.ItemStack{Item: test.tool, Count: 1, Durability: full - 1}) {
				t.Fatalf("破坏一个方块后栏位 = %+v，想要 %d 耐久 %d", got, test.tool, full-1)
			}
			if !player.inventoryDirty {
				t.Fatal("扣减耐久没有标记 inventoryDirty")
			}
		})
	}
}

func TestMiningWrongToolStillConsumesDurability(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	player := engine.sessions[sessions[0]].player
	target := targets[0]
	engine.SetBlockForTest(target, core.IronBlockID)
	setMiningHeldItem(player, core.ItemStonePickaxe)
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)

	for range 20 {
		advanceMiningOnce(engine)
	}

	record := miningTargetRecord(t, engine, target)
	x, _, z := target.Local()
	if got := record.Chunk.BlockAt(x, target.Y, z); got != core.AirID {
		t.Fatalf("用错等级的工具完成后方块 = %d，想要空气", got)
	}
	if got := miningDropTotals(record.Chunk); len(got) != 0 {
		t.Fatalf("用错等级的工具产生了掉落 = %+v", got)
	}
	if got := player.inventory.Hotbar.Slots[0].Durability; got != full-1 {
		t.Fatalf("用错等级的工具破坏方块后耐久 = %d，想要 %d", got, full-1)
	}
	if !player.inventoryDirty {
		t.Fatal("用错等级的工具扣减耐久没有标记 inventoryDirty")
	}
}

func TestMiningTurnsToolIntoBrokenFormAtZero(t *testing.T) {
	tests := []struct {
		name     string
		tool     core.ItemID
		broken   core.ItemID
		block    core.BlockID
		wantDrop core.ItemID
		ticks    int
	}{
		{
			name: "石镐", tool: core.ItemStonePickaxe, broken: core.ItemBrokenStonePickaxe,
			block: core.CoalOreID, wantDrop: core.ItemCoal, ticks: 15,
		},
		{
			name: "铁镐", tool: core.ItemIronPickaxe, broken: core.ItemBrokenIronPickaxe,
			block: core.IronBlockID, wantDrop: core.ItemIronBlock, ticks: 10,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			player := engine.sessions[sessions[0]].player
			target := targets[0]
			engine.SetBlockForTest(target, test.block)
			setMiningHeldItem(player, test.tool)
			player.inventory.Hotbar.Slots[0].Durability = 1

			var result TickResult
			for range test.ticks {
				result = advanceMiningOnce(engine)
			}

			if len(result.Rejected) != 0 {
				t.Fatalf("最后一次采掘被拒绝 = %+v", result.Rejected)
			}
			record := miningTargetRecord(t, engine, target)
			x, _, z := target.Local()
			if got := record.Chunk.BlockAt(x, target.Y, z); got != core.AirID {
				t.Fatalf("完成后方块 = %d，想要空气", got)
			}
			if got := miningDropTotals(record.Chunk); got[test.wantDrop] != 1 || len(got) != 1 {
				t.Fatalf("最后一次采掘掉落 = %+v，想要一个 %d", got, test.wantDrop)
			}
			if got := player.inventory.Hotbar.Slots[0]; got != (core.ItemStack{Item: test.broken, Count: 1}) {
				t.Fatalf("耐久耗尽后栏位 = %+v，想要一个损坏工具 %d", got, test.broken)
			}
			if !player.inventoryDirty {
				t.Fatal("工具损坏没有标记 inventoryDirty")
			}
		})
	}
}

func TestMiningProtectedAndUnreadyRejectionsPreserveDurability(t *testing.T) {
	tests := []struct {
		name   string
		block  core.BlockID
		want   RejectReason
		mutate func(*Engine, core.BlockPos)
	}{
		{
			name: "受保护方块", block: core.BedrockID, want: RejectProtectedBlock,
			mutate: func(engine *Engine, target core.BlockPos) {
				engine.SetBlockForTest(target, core.BedrockID)
			},
		},
		{
			name: "区块未就绪", block: core.StoneID, want: RejectChunkNotReady,
			mutate: func(engine *Engine, target core.BlockPos) {
				delete(engine.dimensions[core.Overworld].records, target.Chunk())
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			player := engine.sessions[sessions[0]].player
			setMiningHeldItem(player, core.ItemStonePickaxe)
			before := player.inventory.Hotbar.Slots[0]
			target := targets[0]
			test.mutate(engine, target)

			reason, rejected := engine.completeMining(
				core.Overworld,
				target,
				test.block,
				true,
				make(map[core.ChunkKey]*pendingChunkChanges),
			)
			if !rejected || reason != test.want {
				t.Fatalf("拒绝 = (%d, %v)，想要 (%d, true)", reason, rejected, test.want)
			}
			if got := player.inventory.Hotbar.Slots[0]; got != before {
				t.Fatalf("拒绝路径修改了工具：got=%+v want=%+v", got, before)
			}
			if player.inventoryDirty {
				t.Fatal("拒绝路径标记了 inventoryDirty")
			}
		})
	}
}

func TestCompleteMiningRejectsNoOp(t *testing.T) {
	tests := []struct {
		name  string
		block core.BlockID
		setup func(*testing.T, *Engine, core.BlockPos)
	}{
		{
			name:  "普通方块",
			block: core.StoneID,
			setup: func(_ *testing.T, engine *Engine, target core.BlockPos) {
				engine.SetBlockForTest(target, core.AirID)
			},
		},
		{
			name:  "熔炉",
			block: core.FurnaceID,
			setup: func(t *testing.T, engine *Engine, target core.BlockPos) {
				setMiningFurnace(t, engine, target, world.FurnaceSlot{Generation: 1})
				engine.SetBlockForTest(target, core.AirID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, _, targets := readyMiningPlayers(t, 1)
			target := targets[0]
			test.setup(t, engine, target)
			record := miningTargetRecord(t, engine, target)
			beforeHash := record.Chunk.Hash()
			beforeDropsHash := record.Chunk.DropsHash()
			beforeFurnace := record.Chunk.Furnace(0)
			beforeRevision := record.Revision
			pending := make(map[core.ChunkKey]*pendingChunkChanges)

			reason, rejected := engine.completeMining(
				core.Overworld, target, test.block, true, pending,
			)

			if !rejected || reason != RejectNoTarget {
				t.Fatalf("no-op 完成结果 = (%d, %v)，想要 (%d, true)",
					reason, rejected, RejectNoTarget)
			}
			if got := record.Chunk.Hash(); got != beforeHash {
				t.Fatalf("no-op 完成修改了方块：hash=%x/%x", got, beforeHash)
			}
			if got := record.Chunk.DropsHash(); got != beforeDropsHash {
				t.Fatalf("no-op 完成修改了掉落物：hash=%x/%x", got, beforeDropsHash)
			}
			if got := record.Chunk.Furnace(0); got != beforeFurnace {
				t.Fatalf("no-op 完成修改了熔炉：got=%+v want=%+v", got, beforeFurnace)
			}
			if record.Revision != beforeRevision || len(pending) != 0 {
				t.Fatalf("no-op 完成修改了 revision 或 pending：revision=%d/%d pending=%+v",
					record.Revision, beforeRevision, pending)
			}
		})
	}
}

func TestMiningDoesNotConsumeDurabilityFromNonTools(t *testing.T) {
	tests := []struct {
		name  string
		stack core.ItemStack
	}{
		{name: "空手"},
		{name: "普通物品", stack: core.ItemStack{Item: core.ItemDirt, Count: 1}},
		{name: "损坏物品", stack: core.ItemStack{Item: core.ItemBrokenStonePickaxe, Count: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			player := engine.sessions[sessions[0]].player
			engine.SetBlockForTest(targets[0], core.DirtID)
			player.inventory.Hotbar.Slots[0] = test.stack

			for range 5 {
				advanceMiningOnce(engine)
			}

			if got := player.inventory.Hotbar.Slots[0]; got != test.stack {
				t.Fatalf("非工具栏位被修改：got=%+v want=%+v", got, test.stack)
			}
			if player.inventoryDirty {
				t.Fatal("非工具完成采掘却标记了 inventoryDirty")
			}
		})
	}
}

func TestBrokenToolMinesLikeBareHand(t *testing.T) {
	blocks := []core.BlockID{
		core.DirtID,
		core.GrassID,
		core.StoneID,
		core.StoneBrickID,
		core.FurnaceID,
		core.CoalOreID,
		core.IronOreID,
		core.IronBlockID,
		core.BedrockID,
		core.BarrierID,
	}
	for _, block := range blocks {
		bareTicks, bareHarvest := miningRule(block, core.ItemNone)
		for _, broken := range []core.ItemID{
			core.ItemBrokenStonePickaxe,
			core.ItemBrokenIronPickaxe,
		} {
			ticks, harvest := miningRule(block, broken)
			if ticks != bareTicks || harvest != bareHarvest {
				t.Fatalf("方块 %d 损坏工具 %d = (%d,%v)，空手 = (%d,%v)，两者必须一致",
					block, broken, ticks, harvest, bareTicks, bareHarvest)
			}
		}
	}
}

func TestMiningResetsProgressWhenHeldToolLeavesHand(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*playerState)
	}{
		{name: "换到空栏位", mutate: func(player *playerState) {
			player.inventory.Hotbar.Selected = 5
		}},
		{name: "丢弃工具", mutate: func(player *playerState) {
			player.inventory.Hotbar.Slots[0] = core.ItemStack{}
		}},
		{name: "工具损坏", mutate: func(player *playerState) {
			player.inventory.Hotbar.Slots[0] = core.ItemStack{
				Item: core.ItemBrokenStonePickaxe, Count: 1,
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			player := engine.sessions[sessions[0]].player
			engine.SetBlockForTest(targets[0], core.CoalOreID)
			setMiningHeldItem(player, core.ItemStonePickaxe)

			for range 3 {
				advanceMiningOnce(engine)
			}
			if player.mining.progressTicks != 3 {
				t.Fatalf("进度 = %d，想要 3", player.mining.progressTicks)
			}

			test.mutate(player)
			advanceMiningOnce(engine)

			if player.mining.progressTicks != 1 {
				t.Fatalf("工具离手后进度 = %d，想要重新从 1 开始", player.mining.progressTicks)
			}
			if player.mining.held == core.ItemStonePickaxe {
				t.Fatal("状态机仍记录着已经离手的石镐")
			}
		})
	}
}

func TestMiningHarvestableCapacityFailureIsAtomicRejectsOnceAndPreservesDurability(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	session, target := sessions[0], targets[0]
	player := engine.sessions[session].player
	engine.SetBlockForTest(target, core.LightBlockID)
	setMiningHeldItem(player, core.ItemStonePickaxe)
	beforeTool := player.inventory.Hotbar.Slots[0]
	fillMiningDrops(engine, target)
	record := miningTargetRecord(t, engine, target)
	beforeHash := record.Chunk.Hash()
	beforeDropsHash := record.Chunk.DropsHash()
	beforeRevision := record.Revision

	var result TickResult
	for range 15 {
		result = advanceMiningOnce(engine)
	}
	if len(result.Rejected) != 1 || result.Rejected[0] != (Rejection{
		Session: session, Sequence: 10, Reason: RejectDropCapacity,
	}) {
		t.Fatalf("容量拒绝=%+v", result.Rejected)
	}
	if got := record.Chunk.Hash(); got != beforeHash || record.Revision != beforeRevision {
		t.Fatalf("容量失败修改了区块或 revision: hash=%x/%x revision=%d/%d",
			got, beforeHash, record.Revision, beforeRevision)
	}
	if got := record.Chunk.DropsHash(); got != beforeDropsHash {
		t.Fatalf("容量失败修改了掉落槽: drops=%x/%x", got, beforeDropsHash)
	}
	if engine.sessions[session].player.mining != (playerMiningState{}) {
		t.Fatalf("容量失败后 mining=%+v，想要清零", engine.sessions[session].player.mining)
	}
	if got := player.inventory.Hotbar.Slots[0]; got != beforeTool {
		t.Fatalf("容量拒绝修改了工具：got=%+v want=%+v", got, beforeTool)
	}
	if player.inventoryDirty {
		t.Fatal("容量拒绝标记了 inventoryDirty")
	}

	next := advanceMiningOnce(engine)
	if len(next.Rejected) != 0 || engine.sessions[session].player.mining.progressTicks != 1 {
		t.Fatalf("继续按住下一 tick=%+v mining=%+v，想要新一轮 1",
			next.Rejected, engine.sessions[session].player.mining)
	}
}

func TestMiningWrongToolCompletesWithFullDropCapacity(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	target := targets[0]
	engine.SetBlockForTest(target, core.LightBlockID)
	setMiningHeldItem(engine.sessions[sessions[0]].player, core.ItemDirt)
	fillMiningDrops(engine, target)
	record := miningTargetRecord(t, engine, target)
	beforeDrops := miningDropTotals(record.Chunk)
	beforeRevision := record.Revision

	var result TickResult
	for range 30 {
		result = advanceMiningOnce(engine)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("错误工具被掉落容量阻止=%+v", result.Rejected)
	}
	x, _, z := target.Local()
	if record.Chunk.BlockAt(x, target.Y, z) != core.AirID || record.Revision != beforeRevision+1 {
		t.Fatalf("错误工具未原子完成: revision=%d want=%d", record.Revision, beforeRevision+1)
	}
	if got := miningDropTotals(record.Chunk); !equalMiningDropTotals(got, beforeDrops) {
		t.Fatalf("错误工具修改了掉落物: got=%+v want=%+v", got, beforeDrops)
	}
}

func TestMiningFurnaceCompletionDropsBodyByToolAndPreservesContents(t *testing.T) {
	tests := []struct {
		name     string
		held     core.ItemID
		ticks    int
		fuel     uint8
		wantBody uint8
	}{
		{"错误工具只掉非空内容", core.ItemDirt, 30, 0, 0},
		{"石镐掉本体和全部内容", core.ItemStonePickaxe, 15, 3, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			engine, sessions, targets := readyMiningPlayers(t, 1)
			target := targets[0]
			setMiningHeldItem(engine.sessions[sessions[0]].player, test.held)
			furnace := world.FurnaceSlot{
				Generation: 3,
				Input:      core.ItemStack{Item: core.ItemRawIron, Count: 2},
				Output:     core.ItemStack{Item: core.ItemIronIngot, Count: 4},
			}
			if test.fuel != 0 {
				furnace.Fuel = core.ItemStack{Item: core.ItemCoal, Count: test.fuel}
			}
			furnace = setMiningFurnace(t, engine, target, furnace)
			for range test.ticks {
				advanceMiningOnce(engine)
			}
			record := miningTargetRecord(t, engine, target)
			if got := record.Chunk.Furnace(0); got.Active || got.Generation != 3 {
				t.Fatalf("完成后熔炉槽=%+v", got)
			}
			drops := miningDropTotals(record.Chunk)
			wantKinds := 2 + int(test.wantBody)
			if test.fuel != 0 {
				wantKinds++
			}
			if drops[core.ItemFurnace] != test.wantBody ||
				drops[furnace.Input.Item] != furnace.Input.Count ||
				drops[furnace.Output.Item] != furnace.Output.Count ||
				drops[furnace.Fuel.Item] != furnace.Fuel.Count ||
				len(drops) != wantKinds {
				t.Fatalf("熔炉掉落=%+v，body=%d", drops, test.wantBody)
			}
		})
	}
}

func TestMiningWrongToolEmptyFurnaceDropsNothing(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	target := targets[0]
	setMiningHeldItem(engine.sessions[sessions[0]].player, core.ItemDirt)
	setMiningFurnace(t, engine, target, world.FurnaceSlot{Generation: 7})
	for range 30 {
		advanceMiningOnce(engine)
	}
	record := miningTargetRecord(t, engine, target)
	if drops := miningDropTotals(record.Chunk); len(drops) != 0 {
		t.Fatalf("错误工具破坏空熔炉掉落=%+v，想要空", drops)
	}
	if got := record.Chunk.Furnace(0); got.Active || got.Generation != 7 {
		t.Fatalf("空熔炉完成后槽=%+v", got)
	}
}

func TestMiningFurnaceBatchCapacityFailureIsAtomic(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	target := targets[0]
	setMiningHeldItem(engine.sessions[sessions[0]].player, core.ItemIronPickaxe)
	setMiningFurnace(t, engine, target, world.FurnaceSlot{
		Generation: 5,
		Input:      core.ItemStack{Item: core.ItemRawIron, Count: 1},
		Fuel:       core.ItemStack{Item: core.ItemCoal, Count: 1},
		Output:     core.ItemStack{Item: core.ItemIronIngot, Count: 1},
	})
	fillMiningDrops(engine, target)
	record := miningTargetRecord(t, engine, target)
	beforeHash := record.Chunk.Hash()
	beforeRevision := record.Revision

	var result TickResult
	for range 8 {
		result = advanceMiningOnce(engine)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectDropCapacity {
		t.Fatalf("熔炉容量拒绝=%+v", result.Rejected)
	}
	if got := record.Chunk.Hash(); got != beforeHash || record.Revision != beforeRevision {
		t.Fatalf("熔炉容量失败修改状态: hash=%x/%x revision=%d/%d",
			got, beforeHash, record.Revision, beforeRevision)
	}
}

func TestMiningTwoSessionsCompleteOneTargetOnce(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 2)
	target := core.BlockPos{X: 0, Y: 2, Z: 5}
	engine.SetBlockForTest(targets[0], core.AirID)
	engine.SetBlockForTest(targets[1], core.AirID)
	engine.SetBlockForTest(target, core.StoneID)
	for _, id := range sessions {
		player := engine.sessions[id].player
		player.state.Position = mgl32.Vec3{0.5, 1, 8.5}
		player.pitch = 0
		setMiningHeldItem(player, core.ItemStonePickaxe)
	}
	for range 14 {
		advanceMiningOnce(engine)
	}
	result := advanceMiningOnce(engine)
	if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 {
		t.Fatalf("竞争完成 Changes=%+v，想要一次", result.Changes)
	}
	record := miningTargetRecord(t, engine, target)
	if drops := miningDropTotals(record.Chunk); drops[core.ItemStone] != 1 || len(drops) != 1 {
		t.Fatalf("竞争完成掉落=%+v，想要一个石头", drops)
	}
	if engine.sessions[sessions[1]].player.mining != (playerMiningState{}) {
		t.Fatalf("后处理会话未看到空气并清零: %+v", engine.sessions[sessions[1]].player.mining)
	}
	full, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	if got := engine.sessions[sessions[0]].player.inventory.Hotbar.Slots[0].Durability; got != full-1 {
		t.Fatalf("实际移除方块的玩家耐久 = %d，想要 %d", got, full-1)
	}
	if got := engine.sessions[sessions[1]].player.inventory.Hotbar.Slots[0].Durability; got != full {
		t.Fatalf("未移除方块的玩家耐久 = %d，想要保持 %d", got, full)
	}
}

func TestAuthoritativeMiningEightPlayersDoesNotAllocate(t *testing.T) {
	engine, sessions, _ := readyMiningPlayers(t, 8)
	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	result := TickResult{}
	engine.advanceMining(pending, &result)
	for _, session := range engine.sessions {
		session.player.mining = playerMiningState{}
	}

	allocations := testing.AllocsPerRun(1000, func() {
		result.Rejected = result.Rejected[:0]
		engine.advanceMining(pending, &result)
		for _, session := range engine.sessions {
			session.player.mining = playerMiningState{}
		}
	})
	if allocations != 0 {
		t.Fatalf("八人采掘每 tick 分配=%f，想要 0", allocations)
	}

	engine.advanceMining(pending, &result)
	for _, id := range sessions {
		if got := engine.sessions[id].player.mining.progressTicks; got != 1 {
			t.Fatalf("session %d 预热后单次进度=%d，想要 1", id, got)
		}
	}
}

func BenchmarkAuthoritativeMiningEightPlayers(b *testing.B) {
	engine := readyMiningBenchmarkPlayers(b)
	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	result := TickResult{}
	engine.advanceMining(pending, &result)
	for _, session := range engine.sessions {
		session.player.mining = playerMiningState{}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result.Rejected = result.Rejected[:0]
		engine.advanceMining(pending, &result)
		for _, session := range engine.sessions {
			session.player.mining = playerMiningState{}
		}
	}
}

func advanceMiningOnce(engine *Engine) TickResult {
	result := TickResult{}
	pending := make(map[core.ChunkKey]*pendingChunkChanges)
	engine.advanceMining(pending, &result)
	engine.finishChanges(pending, &result)
	return result
}

func setMiningHeldItem(player *playerState, item core.ItemID) {
	if item == core.ItemNone {
		player.inventory.Hotbar.Slots[0] = core.ItemStack{}
		return
	}
	full, _ := core.ItemMaxDurability(item)
	player.inventory.Hotbar.Slots[0] = core.ItemStack{Item: item, Count: 1, Durability: full}
}

func miningTargetRecord(t *testing.T, engine *Engine, target core.BlockPos) *ChunkRecord {
	t.Helper()
	record := engine.dimensions[core.Overworld].records[target.Chunk()]
	if record == nil || record.Chunk == nil {
		t.Fatalf("目标区块 %+v 未就绪", target.Chunk())
	}
	return record
}

func miningDropTotals(chunk *world.Chunk) map[core.ItemID]uint8 {
	totals := make(map[core.ItemID]uint8)
	for slot := range core.DropsPerChunk {
		drop := chunk.Drop(slot)
		if drop.Active {
			totals[drop.Stack.Item] += drop.Stack.Count
		}
	}
	return totals
}

func equalMiningDropTotals(left, right map[core.ItemID]uint8) bool {
	if len(left) != len(right) {
		return false
	}
	for item, count := range left {
		if right[item] != count {
			return false
		}
	}
	return true
}

func fillMiningDrops(engine *Engine, target core.BlockPos) {
	key := core.ChunkKey{Dimension: core.Overworld, Pos: target.Chunk()}
	for slot := range core.DropsPerChunk {
		engine.SetChunkDropForTest(key, slot, world.DropSlot{
			Generation: 1,
			Active:     true,
			Stack:      core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount},
		})
	}
}

func setMiningFurnace(
	t *testing.T,
	engine *Engine,
	target core.BlockPos,
	furnace world.FurnaceSlot,
) world.FurnaceSlot {
	t.Helper()
	index, ok := world.ChunkBlockIndex(target)
	if !ok {
		t.Fatalf("熔炉目标 %+v 没有区块索引", target)
	}
	engine.SetBlockForTest(target, core.FurnaceID)
	furnace.Active = true
	furnace.BlockIndex = index
	engine.SetChunkFurnaceForTest(
		core.ChunkKey{Dimension: core.Overworld, Pos: target.Chunk()}, 0, furnace,
	)
	return furnace
}

func readyMiningPlayers(
	t *testing.T,
	count int,
) (*Engine, []SessionID, []core.BlockPos) {
	t.Helper()
	engine, _ := readyMovementPlayer(t)
	for id := count; id >= 2; id-- {
		engine.RegisterSession(SessionID(id), core.Overworld, core.ChunkPos{})
	}
	if count > 1 {
		engine.Step()
	}
	sessions := make([]SessionID, count)
	targets := make([]core.BlockPos, count)
	for index := range count {
		id := SessionID(index + 1)
		sessions[index] = id
		target := core.BlockPos{X: int32(index * 2), Y: 1, Z: 5}
		targets[index] = target
		engine.SetBlockForTest(target, core.StoneID)
		session := engine.sessions[id]
		session.player.state.Position = mgl32.Vec3{float32(index*2) + 0.5, 1, 8.5}
		session.player.yaw = 0
		session.player.pitch = miningTestPitch
		session.player.miningHeld = true
		session.player.lastInputSequence = uint64(10 + index)
		session.player.reset = false
	}
	return engine, sessions, targets
}

func readyMiningBenchmarkPlayers(b *testing.B) *Engine {
	b.Helper()
	engine := NewEngine(0, 0)
	dimension := engine.dimensions[core.Overworld]
	if !dimension.BeginGeneration(core.ChunkPos{}) {
		b.Fatal("benchmark 区块未开始生成")
	}
	if err := dimension.ApplyGenerated(core.ChunkPos{}, movementFlatChunk(core.ChunkPos{})); err != nil {
		b.Fatal(err)
	}
	for id := 8; id >= 1; id-- {
		engine.RegisterSession(SessionID(id), core.Overworld, core.ChunkPos{})
	}
	engine.advancePendingPlayers()
	for index := range 8 {
		id := SessionID(index + 1)
		target := core.BlockPos{X: int32(index * 2), Y: 1, Z: 5}
		engine.SetBlockForTest(target, core.StoneID)
		session := engine.sessions[id]
		session.player.state.Position = mgl32.Vec3{float32(index*2) + 0.5, 1, 8.5}
		session.player.pitch = miningTestPitch
		session.player.miningHeld = true
		session.player.reset = false
	}
	return engine
}

func TestMiningRuleOrdinaryItemsAreNotBareHand(t *testing.T) {
	ordinary := [...]core.ItemID{
		core.ItemStone,
		core.ItemDirt,
		core.ItemGrass,
		core.ItemStoneBrick,
		core.ItemCoal,
		core.ItemRawIron,
		core.ItemIronIngot,
		core.ItemFurnace,
		core.ItemIronBlock,
	}
	for _, held := range ordinary {
		ticks, drop := miningRule(core.StoneID, held)
		if ticks != 30 || drop {
			t.Fatalf("普通物品 %d 采石规则 = %d, %v，想要 30, false", held, ticks, drop)
		}
	}
}

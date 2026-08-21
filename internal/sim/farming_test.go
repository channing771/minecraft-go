package sim

import (
	"math"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/core"
)

// tillTarget 是全部翻地用例共用的目标格：一个悬空的可翻方块，正上方按用例
// 填不同内容。它不在玩家正下方，因此 lookAtBlockCenter 算出的 pitch 不会撞上
// validPlayerLook 的 ±(π/2 − 0.01) 边界。
var tillTarget = core.BlockPos{X: 0, Y: 1, Z: 4}

// lookAtBlockCenter 返回从 eye 看向方块几何中心所需的 (yaw, pitch)，是
// LookDirection 的逆运算。
//
// 夹具用它而不是手写角度常量：EyeHeight 是 tunable，写死的角度在它变动后会
// 静默瞄到别的格子上，而"瞄错了"和"实现错了"在读数上无法区分。
func lookAtBlockCenter(eye mgl32.Vec3, target core.BlockPos) (yaw, pitch float32) {
	delta := blockCenterVec3(target).Sub(eye)
	horizontal := math.Hypot(float64(delta.X()), float64(delta.Z()))
	pitch = float32(math.Atan2(float64(delta.Y()), horizontal))
	yaw = float32(math.Atan2(float64(-delta.X()), float64(-delta.Z())))
	return yaw, pitch
}

// readyTillPlayer 构造一个握着指定物品栏内容、瞄准 tillTarget 的玩家。
// target 是写进 tillTarget 的方块；above 非零时写进它的正上方。
func readyTillPlayer(
	t *testing.T,
	held core.ItemStack,
	target core.BlockID,
	above core.BlockID,
) (*Engine, SessionID, float32, float32) {
	t.Helper()
	engine, session := readyMovementPlayer(t)
	engine.SetBlockForTest(tillTarget, target)
	if above != core.AirID {
		aboveTarget := tillTarget
		aboveTarget.Y++
		engine.SetBlockForTest(aboveTarget, above)
	}
	player := engine.sessions[session].player
	player.inventory.Hotbar.Slots[0] = held
	player.inventory.Hotbar.Selected = 0
	eye := player.state.Position.Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	yaw, pitch := lookAtBlockCenter(eye, tillTarget)
	return engine, session, yaw, pitch
}

// till 发一条翻地命令并推进一个权威 tick。
func till(engine *Engine, session SessionID, yaw, pitch float32) TickResult {
	engine.Enqueue(Command{
		Session: session, Sequence: 2, Kind: CommandTillSoil, Yaw: yaw, Pitch: pitch,
	})
	return engine.Step()
}

func tillBlockAt(t *testing.T, engine *Engine, position core.BlockPos) core.BlockID {
	t.Helper()
	block, ready := engine.dimensions[core.Overworld].BlockAt(position)
	if !ready {
		t.Fatalf("方块 %+v 所在区块未就绪", position)
	}
	return block
}

// TestTillTurnsGrassIntoFarmlandAndSpendsOneDurability 覆盖 Scenario
// 「手持锄头翻开草地」：草变耕地，锄头耐久恰好减少 1。
//
// 耐久断言用**精确值**（full-1）而不是"不大于满值"：后者在扣与不扣两种实现下
// 同时成立，差值恒等于零，等于没测。
func TestTillTurnsGrassIntoFarmlandAndSpendsOneDurability(t *testing.T) {
	for _, tc := range []struct {
		name   string
		hoe    core.ItemID
		target core.BlockID
	}{
		{"石锄翻草", core.ItemStoneHoe, core.GrassID},
		{"石锄翻泥土", core.ItemStoneHoe, core.DirtID},
		{"铁锄翻草", core.ItemIronHoe, core.GrassID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full, _ := core.ItemMaxDurability(tc.hoe)
			held := core.ItemStack{Item: tc.hoe, Count: 1, Durability: full}
			engine, session, yaw, pitch := readyTillPlayer(t, held, tc.target, core.AirID)

			result := till(engine, session, yaw, pitch)

			if len(result.Rejected) != 0 {
				t.Fatalf("合法翻地被拒绝: %+v", result.Rejected)
			}
			if got := tillBlockAt(t, engine, tillTarget); got != core.FarmlandDryID {
				t.Fatalf("翻地结果 = %d，想要干耕地 %d", got, core.FarmlandDryID)
			}
			want := core.ItemStack{Item: tc.hoe, Count: 1, Durability: full - 1}
			player := engine.sessions[session].player
			if got := player.inventory.Hotbar.Slots[0]; got != want {
				t.Fatalf("翻地后栏位 = %+v，想要耐久恰好 −1 的 %+v", got, want)
			}
			// 扣减耐久必须在同一 tick 发布给所属玩家（spec：服务端 MUST 向
			// 所属玩家发布更新后的背包状态）。inventoryDirty 在 Step 末尾的
			// publishInventories 里被清掉，因此断言只能看发布出来的那一份。
			if len(result.Inventories) != 1 || result.Inventories[0].Session != session ||
				result.Inventories[0].Inventory.Hotbar.Slots[0] != want {
				t.Fatalf("翻地没有发布扣减后的背包: %+v", result.Inventories)
			}
			// 方块变更必须经既有 recordChange 汇入本 tick 的批次，
			// 客户端才会收到；只改内存不广播同样能让上面的断言全绿。
			if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 ||
				result.Changes[0].Changes[0] != (BlockChange{
					Position: tillTarget, Block: core.FarmlandDryID,
				}) {
				t.Fatalf("翻地没有广播为区块变更: %+v", result.Changes)
			}
		})
	}
}

// TestTillFinalDurabilityStillTillsAndBreaksHoe 钉死"耐久 1 → 0"的语义：本次
// 翻地仍然生效，锄头同时转为损坏形态（与采掘完全一致）。
func TestTillFinalDurabilityStillTillsAndBreaksHoe(t *testing.T) {
	held := core.ItemStack{Item: core.ItemIronHoe, Count: 1, Durability: 1}
	engine, session, yaw, pitch := readyTillPlayer(t, held, core.DirtID, core.AirID)

	if result := till(engine, session, yaw, pitch); len(result.Rejected) != 0 {
		t.Fatalf("最后一点耐久的翻地被拒绝: %+v", result.Rejected)
	}
	if got := tillBlockAt(t, engine, tillTarget); got != core.FarmlandDryID {
		t.Fatalf("耐久耗尽的那次翻地没有生效: 方块 = %d", got)
	}
	want := core.ItemStack{Item: core.ItemBrokenIronHoe, Count: 1}
	if got := engine.sessions[session].player.inventory.Hotbar.Slots[0]; got != want {
		t.Fatalf("耐久归零后栏位 = %+v，想要损坏铁锄 %+v", got, want)
	}
}

// TestTillRejectsWhenBlockAboveIsNotAir 覆盖 Scenario「上方非空气时拒绝翻地」，
// 同时钉死"上方判定读的是方块编号，不是碰撞体"。
//
// 主用例上方是石头；水用例是真正的守卫：水既非空气又零碰撞体，若实现误用
// physics.BlockCollisionBoxes 之类的碰撞判定，石头用例照样拒绝、只有水用例会
// 放行。作物同理（零碰撞体），编号一落地即可照抄本用例。
//
// 拒绝原因是 RejectOccupied 这件事本身也在承重：它只可能由"目标确实是泥土或
// 草、且上方非空气"这条分支产生。射线若被上方那块石头先命中，原因会是
// RejectInvalidBlock，用例立刻变红——夹具瞄错格子不会静默通过。
func TestTillRejectsWhenBlockAboveIsNotAir(t *testing.T) {
	for _, tc := range []struct {
		name  string
		above core.BlockID
	}{
		{"上方是石头", core.StoneID},
		{"上方是水", core.WaterSourceID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			full, _ := core.ItemMaxDurability(core.ItemStoneHoe)
			held := core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: full}
			engine, session, yaw, pitch := readyTillPlayer(t, held, core.DirtID, tc.above)

			result := till(engine, session, yaw, pitch)

			if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectOccupied {
				t.Fatalf("Rejected = %+v，想要恰好一条 RejectOccupied", result.Rejected)
			}
			if got := tillBlockAt(t, engine, tillTarget); got != core.DirtID {
				t.Fatalf("被拒绝的翻地改了方块: %d", got)
			}
			if got := engine.sessions[session].player.inventory.Hotbar.Slots[0]; got != held {
				t.Fatalf("被拒绝的翻地磨损了锄头: %+v，想要一字不变的 %+v", got, held)
			}
		})
	}
}

// TestTillRejectsNonHoeHeldItems 覆盖 Scenario「非锄头不能翻地」。
//
// 四种手持缺一不可：空手、镐、普通方块、**损坏的锄头**。最后一种最容易漏——
// 损坏形态是独立物品编号，"是不是锄头"若写成按名字前缀或按编号区间判定，
// 只有它会漏网。
func TestTillRejectsNonHoeHeldItems(t *testing.T) {
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	for _, tc := range []struct {
		name string
		held core.ItemStack
	}{
		{"空手", core.ItemStack{}},
		{"石镐", core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}},
		{"普通方块", core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}},
		{"损坏的石锄", core.ItemStack{Item: core.ItemBrokenStoneHoe, Count: 1}},
		{"损坏的铁锄", core.ItemStack{Item: core.ItemBrokenIronHoe, Count: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine, session, yaw, pitch := readyTillPlayer(t, tc.held, core.GrassID, core.AirID)

			result := till(engine, session, yaw, pitch)

			if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectInvalidBlock {
				t.Fatalf("Rejected = %+v，想要恰好一条 RejectInvalidBlock", result.Rejected)
			}
			if got := tillBlockAt(t, engine, tillTarget); got != core.GrassID {
				t.Fatalf("非锄头翻动了方块: %d", got)
			}
			player := engine.sessions[session].player
			if got := player.inventory.Hotbar.Slots[0]; got != tc.held {
				t.Fatalf("非锄头路径改了栏位: %+v，想要一字不变的 %+v", got, tc.held)
			}
			if len(result.Inventories) != 0 {
				t.Fatalf("被拒绝的翻地发布了背包变化: %+v", result.Inventories)
			}
		})
	}
}

// TestTillRejectsTargetsThatAreNotDirtOrGrass 覆盖 tool-durability 的新 Scenario
// 「翻地被拒绝不磨损锄头」：目标不是泥土或草时拒绝，耐久保持精确值。
//
// 耕地自身也在表里：翻过的地不能再翻一次（否则耐久会被无限消耗在同一格上）。
func TestTillRejectsTargetsThatAreNotDirtOrGrass(t *testing.T) {
	for _, target := range []core.BlockID{
		core.StoneID, core.OakLogID, core.FarmlandDryID, core.FarmlandWetID,
	} {
		full, _ := core.ItemMaxDurability(core.ItemIronHoe)
		held := core.ItemStack{Item: core.ItemIronHoe, Count: 1, Durability: full}
		engine, session, yaw, pitch := readyTillPlayer(t, held, target, core.AirID)

		result := till(engine, session, yaw, pitch)

		if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectInvalidBlock {
			t.Fatalf("目标 %d 的 Rejected = %+v，想要恰好一条 RejectInvalidBlock",
				target, result.Rejected)
		}
		if got := tillBlockAt(t, engine, tillTarget); got != target {
			t.Fatalf("被拒绝的翻地改了目标 %d: %d", target, got)
		}
		if got := engine.sessions[session].player.inventory.Hotbar.Slots[0]; got != held {
			t.Fatalf("目标 %d 被拒绝时磨损了锄头: %+v，想要 %+v", target, got, held)
		}
	}
}

// TestTillRespectsInteractionReach 覆盖 Scenario「超出触及距离拒绝翻地」。
//
// 同一夹具跑两遍、只改 InteractionReach：默认距离下必须成功，收紧到 2 格后
// 必须被拒且耐久一点不掉。两遍对照才让这条断言是**位置性**的——只跑"被拒"
// 那一遍的话，一个永远拒绝翻地的实现也会全绿。
func TestTillRespectsInteractionReach(t *testing.T) {
	t.Cleanup(func() { SetTunables(DefaultTunables()) })

	run := func(t *testing.T, reach float32) (TickResult, core.ItemStack, core.BlockID) {
		t.Helper()
		tunables := DefaultTunables()
		tunables.InteractionReach = reach
		SetTunables(tunables)
		full, _ := core.ItemMaxDurability(core.ItemStoneHoe)
		held := core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: full}
		engine, session, yaw, pitch := readyTillPlayer(t, held, core.DirtID, core.AirID)
		result := till(engine, session, yaw, pitch)
		return result,
			engine.sessions[session].player.inventory.Hotbar.Slots[0],
			tillBlockAt(t, engine, tillTarget)
	}

	full, _ := core.ItemMaxDurability(core.ItemStoneHoe)
	result, hoe, block := run(t, DefaultTunables().InteractionReach)
	if len(result.Rejected) != 0 || block != core.FarmlandDryID ||
		hoe.Durability != full-1 {
		t.Fatalf("默认交互距离下的翻地 = %+v / 方块 %d / 锄头 %+v，想要成功",
			result.Rejected, block, hoe)
	}

	result, hoe, block = run(t, 2)
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectNoTarget {
		t.Fatalf("超距翻地 Rejected = %+v，想要恰好一条 RejectNoTarget", result.Rejected)
	}
	if block != core.DirtID {
		t.Fatalf("超距翻地改了方块: %d", block)
	}
	if hoe.Durability != full {
		t.Fatalf("超距翻地磨损了锄头: 耐久 = %d，想要保持 %d", hoe.Durability, full)
	}
}

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
	return lookAtPoint(eye, blockCenterVec3(target))
}

// lookAtBlockTop 返回从 eye 看向方块**顶面中心**所需的 (yaw, pitch)。
//
// 种植夹具必须用它而不是 lookAtBlockCenter：从站立高度俯视地面方块时，指向
// 几何中心的射线在到达该格之前就已经下穿 y 平面，命中的是更近一列方块的顶面。
// 瞄准点落在顶面上时，射线恰好在该格的列内穿过 y 平面，命中格与瞄准格一致，
// 命中面必然是 +Y——「玩家瞄准耕地顶面种下种子」这条主路径才被真正覆盖。
func lookAtBlockTop(eye mgl32.Vec3, target core.BlockPos) (yaw, pitch float32) {
	return lookAtPoint(eye, mgl32.Vec3{
		float32(target.X) + 0.5,
		float32(target.Y) + 1,
		float32(target.Z) + 0.5,
	})
}

// lookAtPoint 是 LookDirection 的逆运算：返回从 eye 看向世界坐标 point 所需的
// (yaw, pitch)。
func lookAtPoint(eye, point mgl32.Vec3) (yaw, pitch float32) {
	delta := point.Sub(eye)
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

// plantBelow 是种植用例的落脚格（readyMovementPlayer 的 flat 世界里 y=0 全是
// 草，用例按需把它替换成耕地或别的方块），plantTarget 是它正上方的空气格，
// 也就是种子唯一应该落进去的位置。
var (
	plantBelow  = core.BlockPos{X: 0, Y: 0, Z: 3}
	plantTarget = core.BlockPos{X: 0, Y: 1, Z: 3}
)

// plantSeedCount 是种植夹具持有的种子数量。它必须 ≥ 2 且断言取**精确值**：
// 夹具若只放 0 或 1 颗种子，「拒绝时种子不变」在扣与不扣两种实现下都是同一个
// 读数（0 恒为 0、1 扣完即空槽也可能被误读成"本来就没有"），差值恒等于零。
const plantSeedCount = uint8(4)

// readyPlantingPlayer 构造一个持有 plantSeedCount 颗种子、俯视 plantBelow
// 顶面的玩家；below 写进 plantBelow，决定种子的落脚方块是不是耕地。
func readyPlantingPlayer(
	t *testing.T,
	below core.BlockID,
) (*Engine, SessionID, float32, float32) {
	t.Helper()
	engine, session := readyMovementPlayer(t)
	engine.SetBlockForTest(plantBelow, below)
	player := engine.sessions[session].player
	player.inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemWheatSeeds, Count: plantSeedCount,
	}
	player.inventory.Hotbar.Selected = 0
	eye := player.state.Position.Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
	yaw, pitch := lookAtBlockTop(eye, plantBelow)
	return engine, session, yaw, pitch
}

// blockLabel 返回方块的中文显示名，用作子用例名——失败输出里"干耕地"比裸编号
// 好读得多。
func blockLabel(id core.BlockID) string {
	name, _ := core.BlockDisplayName(id)
	return name
}

// plantSeeds 发一条放置命令（选中第 0 格的种子）并推进一个权威 tick。
func plantSeeds(engine *Engine, session SessionID, yaw, pitch float32) TickResult {
	engine.Enqueue(Command{
		Session: session, Sequence: 2, Kind: CommandPlaceBlock, Slot: 0,
		Yaw: yaw, Pitch: pitch,
	})
	return engine.Step()
}

// TestPlantSeedsOnFarmland 覆盖 Scenario「在耕地上种下种子」：耕地正上方出现
// 第一阶段作物，种子恰好减少 1。
//
// 两种瞄法都必须成立：俯视耕地顶面（命中面就是耕地）与瞄准旁边方块的侧面
// （命中的是石头、目标格才落在耕地上方）。前置读的是**目标格正下方**，不是
// 命中面，第二种瞄法是这条判据的位置性守卫——若实现改成"命中面必须是耕地"，
// 只有它会红。
func TestPlantSeedsOnFarmland(t *testing.T) {
	for _, farmland := range []core.BlockID{core.FarmlandDryID, core.FarmlandWetID} {
		for _, aim := range []struct {
			name string
			look func(*Engine, SessionID) (float32, float32)
		}{
			{
				name: "俯视耕地顶面",
				look: func(engine *Engine, session SessionID) (float32, float32) {
					player := engine.sessions[session].player
					eye := player.state.Position.
						Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
					return lookAtBlockTop(eye, plantBelow)
				},
			},
			{
				// 侧面瞄法：plantTarget 的 +Z 邻格放一块石头，射线命中它的 −Z
				// 侧面，目标格因此正是 plantTarget。
				name: "瞄准旁边方块的侧面",
				look: func(engine *Engine, session SessionID) (float32, float32) {
					side := plantTarget
					side.Z++
					engine.SetBlockForTest(side, core.StoneID)
					player := engine.sessions[session].player
					eye := player.state.Position.
						Add(mgl32.Vec3{0, engine.physicsTunables.EyeHeight, 0})
					return lookAtBlockCenter(eye, side)
				},
			},
		} {
			t.Run(blockLabel(farmland)+"/"+aim.name, func(t *testing.T) {
				engine, session, _, _ := readyPlantingPlayer(t, farmland)
				yaw, pitch := aim.look(engine, session)

				result := plantSeeds(engine, session, yaw, pitch)

				if len(result.Rejected) != 0 {
					t.Fatalf("合法种植被拒绝: %+v", result.Rejected)
				}
				if got := tillBlockAt(t, engine, plantTarget); got != core.WheatStage0ID {
					t.Fatalf("种植结果 = %d，想要第一阶段作物 %d", got, core.WheatStage0ID)
				}
				want := core.ItemStack{Item: core.ItemWheatSeeds, Count: plantSeedCount - 1}
				player := engine.sessions[session].player
				if got := player.inventory.Hotbar.Slots[0]; got != want {
					t.Fatalf("种植后栏位 = %+v，想要恰好 −1 的 %+v", got, want)
				}
				// 方块变更必须经既有 recordChange 汇入本 tick 的批次；只改内存
				// 不广播同样能让上面的断言全绿。
				if len(result.Changes) != 1 || len(result.Changes[0].Changes) != 1 ||
					result.Changes[0].Changes[0] != (BlockChange{
						Position: plantTarget, Block: core.WheatStage0ID,
					}) {
					t.Fatalf("种植没有广播为区块变更: %+v", result.Changes)
				}
			})
		}
	}
}

// TestPlantSeedsRejectsNonFarmlandGround 覆盖 Scenario「非耕地上拒绝种植」：
// 泥土、草与石头之上都不能种，且种子数量一颗不掉。
func TestPlantSeedsRejectsNonFarmlandGround(t *testing.T) {
	for _, below := range []core.BlockID{core.DirtID, core.GrassID, core.StoneID} {
		t.Run(blockLabel(below), func(t *testing.T) {
			engine, session, yaw, pitch := readyPlantingPlayer(t, below)

			result := plantSeeds(engine, session, yaw, pitch)

			if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectInvalidBlock {
				t.Fatalf("Rejected = %+v，想要恰好一条 RejectInvalidBlock", result.Rejected)
			}
			if got := tillBlockAt(t, engine, plantTarget); got != core.AirID {
				t.Fatalf("被拒绝的种植写了方块: %d", got)
			}
			want := core.ItemStack{Item: core.ItemWheatSeeds, Count: plantSeedCount}
			player := engine.sessions[session].player
			if got := player.inventory.Hotbar.Slots[0]; got != want {
				t.Fatalf("被拒绝的种植扣了种子: %+v，想要一字不变的 %+v", got, want)
			}
			if len(result.Changes) != 0 {
				t.Fatalf("被拒绝的种植广播了区块变更: %+v", result.Changes)
			}
		})
	}
}

// TestPlantSeedsRejectsFluidTarget 覆盖 Scenario「耕地上方是水时种植被拒」：
// 落脚格是合法耕地，但种子真正要落进去的目标格（正上方）已经是一格水——
// 规格要求种子 MUST NOT 被放置在非空气格，流体也不例外（与组 4 翻地把流体
// 判为占用一致），种子数必须一颗不掉。
func TestPlantSeedsRejectsFluidTarget(t *testing.T) {
	engine, session, yaw, pitch := readyPlantingPlayer(t, core.FarmlandDryID)
	engine.SetBlockForTest(plantTarget, core.WaterSourceID)

	result := plantSeeds(engine, session, yaw, pitch)

	if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectInvalidBlock {
		t.Fatalf("Rejected = %+v，想要恰好一条 RejectInvalidBlock", result.Rejected)
	}
	if got := tillBlockAt(t, engine, plantTarget); got != core.WaterSourceID {
		t.Fatalf("被拒绝的种植改了目标格: %d，想要保留原来的水 %d", got, core.WaterSourceID)
	}
	want := core.ItemStack{Item: core.ItemWheatSeeds, Count: plantSeedCount}
	player := engine.sessions[session].player
	if got := player.inventory.Hotbar.Slots[0]; got != want {
		t.Fatalf("被拒绝的种植扣了种子: %+v，想要一字不变的 %+v", got, want)
	}
	if len(result.Changes) != 0 {
		t.Fatalf("被拒绝的种植广播了区块变更: %+v", result.Changes)
	}
}

// TestPlaceNonSeedItemsIgnoreFarmlandPrecondition 是「非种子物品的放置行为
// 一字不变」的守卫：同一个非耕地夹具下，石头必须照常放得下去。
//
// 没有它的话，一个把耕地前置错加到**全部**放置物上的实现会让上面两条种植用例
// 全绿——「种子被正确拒绝」和「所有放置都被拒绝」在种子用例里读数完全相同。
func TestPlaceNonSeedItemsIgnoreFarmlandPrecondition(t *testing.T) {
	engine, session, yaw, pitch := readyPlantingPlayer(t, core.DirtID)
	player := engine.sessions[session].player
	player.inventory.Hotbar.Slots[1] = core.ItemStack{
		Item: core.ItemStone, Count: core.MaxStackCount,
	}
	engine.Enqueue(Command{
		Session: session, Sequence: 2, Kind: CommandPlaceBlock, Slot: 1,
		Yaw: yaw, Pitch: pitch,
	})

	result := engine.Step()

	if len(result.Rejected) != 0 {
		t.Fatalf("泥土之上放置石头被拒绝: %+v", result.Rejected)
	}
	if got := tillBlockAt(t, engine, plantTarget); got != core.StoneID {
		t.Fatalf("非种子放置结果 = %d，想要石头 %d", got, core.StoneID)
	}
}

// farmingCropIDs 是八个小麦阶段编号，收获用例按阶段穷举。
var farmingCropIDs = [...]core.BlockID{
	core.WheatStage0ID, core.WheatStage1ID, core.WheatStage2ID, core.WheatStage3ID,
	core.WheatStage4ID, core.WheatStage5ID, core.WheatStage6ID, core.WheatStage7ID,
}

// TestFarmingMiningRules 覆盖 authoritative-mining 的农业条目：作物在任意手持
// 下恰好 1 tick、耕地在任意手持下恰好 5 tick，两者都可收获。
//
// tick 数断言取**精确值**（与既有泥土用例同风格）：只断言"能挖"的话任意正数
// 都绿，规则被改成 30 tick 也读不出来。1 tick 是最小权威量子——0 是 miningRule
// 的"不可采掘"哨兵（基岩语义），作物用 0 会永远挖不动。
func TestFarmingMiningRules(t *testing.T) {
	held := [...]core.ItemID{
		core.ItemNone, core.ItemStone, core.ItemIronPickaxe, core.ItemStoneHoe,
		core.ItemBrokenIronHoe,
	}
	for _, block := range farmingCropIDs {
		for _, item := range held {
			ticks, harvestable := miningRule(block, item)
			if ticks != 1 || !harvestable {
				t.Fatalf("miningRule(作物 %d, %d) = (%d,%v)，想要 (1,true)",
					block, item, ticks, harvestable)
			}
		}
	}
	for _, block := range []core.BlockID{core.FarmlandDryID, core.FarmlandWetID} {
		for _, item := range held {
			ticks, harvestable := miningRule(block, item)
			if ticks != 5 || !harvestable {
				t.Fatalf("miningRule(耕地 %d, %d) = (%d,%v)，想要 (5,true)",
					block, item, ticks, harvestable)
			}
		}
	}
}

// harvest 把 block 写进采掘目标格并按住采掘推进 ticks 个权威 tick，返回最后
// 一个 tick 的结果、目标区块记录与目标格坐标。
func harvest(
	t *testing.T,
	block core.BlockID,
	ticks int,
) (*Engine, TickResult, *ChunkRecord, core.BlockPos) {
	t.Helper()
	engine, _, targets := readyMiningPlayers(t, 1)
	target := targets[0]
	engine.SetBlockForTest(target, block)
	var result TickResult
	for range ticks {
		result = advanceMiningOnce(engine)
	}
	return engine, result, miningTargetRecord(t, engine, target), target
}

func harvestBlockAt(t *testing.T, record *ChunkRecord, target core.BlockPos) core.BlockID {
	t.Helper()
	x, _, z := target.Local()
	return record.Chunk.BlockAt(x, target.Y, z)
}

// TestHarvestMatureWheatDropsWheatAndSeeds 覆盖 Scenario「收获成熟作物」与
// 「任意手持状态一个 tick 内收获成熟作物」：1 tick 破坏，掉落 1 小麦 + 2 种子。
//
// 断言列的是**物品种类与精确数量**（len(got) == 2 一起钉）：只断言"掉落物非空"
// 的话，未成熟路径的那 1 颗种子也能让它绿，多产物分支根本没被覆盖。
func TestHarvestMatureWheatDropsWheatAndSeeds(t *testing.T) {
	_, result, record, target := harvest(t, core.WheatStage7ID, 1)

	if len(result.Rejected) != 0 {
		t.Fatalf("收获成熟小麦被拒绝: %+v", result.Rejected)
	}
	if got := harvestBlockAt(t, record, target); got != core.AirID {
		t.Fatalf("一个 tick 后方块 = %d，想要空气", got)
	}
	got := miningDropTotals(record.Chunk)
	if len(got) != 2 || got[core.ItemWheat] != 1 || got[core.ItemWheatSeeds] != 2 {
		t.Fatalf("成熟小麦掉落 = %+v，想要恰好 1 小麦 + 2 种子", got)
	}
}

// TestHarvestImmatureWheatStillDropsSeed 覆盖 Scenario「误挖未成熟作物不亏
// 种子」：阶段 0..6 都在 1 tick 内破坏并恰好掉 1 颗种子，一颗小麦都不掉。
func TestHarvestImmatureWheatStillDropsSeed(t *testing.T) {
	for _, block := range farmingCropIDs[:len(farmingCropIDs)-1] {
		t.Run(blockLabel(block), func(t *testing.T) {
			_, result, record, target := harvest(t, block, 1)

			if len(result.Rejected) != 0 {
				t.Fatalf("收获未成熟作物被拒绝: %+v", result.Rejected)
			}
			if got := harvestBlockAt(t, record, target); got != core.AirID {
				t.Fatalf("一个 tick 后方块 = %d，想要空气", got)
			}
			got := miningDropTotals(record.Chunk)
			if len(got) != 1 || got[core.ItemWheatSeeds] != 1 {
				t.Fatalf("未成熟作物掉落 = %+v，想要恰好 1 颗种子", got)
			}
		})
	}
}

// TestHarvestFarmlandDropsDirtAfterFiveTicks 覆盖 Scenario「采掘耕地掉落
// 泥土」：第 4 tick 时耕地必须还在，第 5 tick 才破坏并掉 1 泥土。
//
// 两个读数缺一不可：只看第 5 tick 的话，一个 1 tick 就挖穿耕地的实现照样全绿
// （断言变成存在性的"最终挖掉了"）。
func TestHarvestFarmlandDropsDirtAfterFiveTicks(t *testing.T) {
	for _, farmland := range []core.BlockID{core.FarmlandDryID, core.FarmlandWetID} {
		t.Run(blockLabel(farmland), func(t *testing.T) {
			engine, _, record, target := harvest(t, farmland, 4)
			if got := harvestBlockAt(t, record, target); got != farmland {
				t.Fatalf("第 4 tick 的耕地 = %d，想要仍是 %d", got, farmland)
			}
			if got := miningDropTotals(record.Chunk); len(got) != 0 {
				t.Fatalf("第 4 tick 就产生了掉落物: %+v", got)
			}

			result := advanceMiningOnce(engine)

			if len(result.Rejected) != 0 {
				t.Fatalf("第 5 tick 采掘耕地被拒绝: %+v", result.Rejected)
			}
			if got := harvestBlockAt(t, record, target); got != core.AirID {
				t.Fatalf("第 5 tick 后方块 = %d，想要空气", got)
			}
			got := miningDropTotals(record.Chunk)
			if len(got) != 1 || got[core.ItemDirt] != 1 {
				t.Fatalf("耕地掉落 = %+v，想要恰好 1 泥土", got)
			}
		})
	}
}

// TestHarvestMatureWheatCapacityFailureIsAtomic 钉死成熟小麦的多产物没有绕开
// 既有掉落物容量门禁：掉落槽满时整体拒绝，方块不变、掉落槽逐字节不变——
// 绝不允许"先掉了小麦、种子放不下"的半掉落。
func TestHarvestMatureWheatCapacityFailureIsAtomic(t *testing.T) {
	engine, sessions, targets := readyMiningPlayers(t, 1)
	session, target := sessions[0], targets[0]
	engine.SetBlockForTest(target, core.WheatStage7ID)
	fillMiningDrops(engine, target)
	record := miningTargetRecord(t, engine, target)
	beforeHash := record.Chunk.Hash()
	beforeDropsHash := record.Chunk.DropsHash()
	beforeRevision := record.Revision

	result := advanceMiningOnce(engine)

	if len(result.Rejected) != 1 || result.Rejected[0] != (Rejection{
		Session: session, Sequence: 10, Reason: RejectDropCapacity,
	}) {
		t.Fatalf("容量拒绝 = %+v，想要恰好一条 RejectDropCapacity", result.Rejected)
	}
	if got := harvestBlockAt(t, record, target); got != core.WheatStage7ID {
		t.Fatalf("容量失败破坏了作物: %d", got)
	}
	if got := record.Chunk.Hash(); got != beforeHash || record.Revision != beforeRevision {
		t.Fatalf("容量失败修改了区块或 revision: hash=%x/%x revision=%d/%d",
			got, beforeHash, record.Revision, beforeRevision)
	}
	if got := record.Chunk.DropsHash(); got != beforeDropsHash {
		t.Fatalf("容量失败修改了掉落槽: drops=%x/%x", got, beforeDropsHash)
	}
}

// farmingBlockIDs 是全部十个农业方块编号（八个作物阶段 + 干湿耕地），伙伴
// 防御清单用例按它穷举。
var farmingBlockIDs = append(farmingCropIDs[:], core.FarmlandDryID, core.FarmlandWetID)

// TestCompanionMineableBlockRejectsFarmingBlocks 锁定伙伴采掘防御清单对农业
// 方块的**显式**拒绝（design.md D7 / Ruling 5）。
//
// 前置守卫是这条用例的承重墙：十个农业方块在 core.BlockDrop 里都有单一产物
// 登记，"必须有单一 BlockDrop"这条通用判据会**放行**它们。因此本用例断言的是
// "农业方块被显式拒绝"这个事实本身，不是"多掉落被拒"的副产品——把显式拒绝
// 删掉，或者把成熟小麦的多掉落放宽进 core，本用例都必须红。
func TestCompanionMineableBlockRejectsFarmingBlocks(t *testing.T) {
	for _, block := range farmingBlockIDs {
		if _, ok := core.BlockDrop(block); !ok {
			t.Fatalf("农业方块 %d 已不在 core.BlockDrop 中，本用例的前提失效", block)
		}
		if companionMineableBlock(block) {
			t.Fatalf("companionMineableBlock(%d) = true，伙伴必须被显式拒绝", block)
		}
	}
}

// TestCompanionPlaceableBlockRejectsFarmingBlocks 锁定伙伴放置防御清单对农业
// 方块的**显式**拒绝（Ruling 8）。
//
// 前置守卫钉死"往返二重校验本身放行种植"：BlockDrop(阶段 0) = 种子、
// ItemPlacement(种子) = 阶段 0，往返成立，因此 completeCompanionPlacement 的
// 既有校验链挡不住伙伴种地。本用例断言的是显式拒绝本身。
func TestCompanionPlaceableBlockRejectsFarmingBlocks(t *testing.T) {
	item, ok := core.BlockDrop(core.WheatStage0ID)
	if !ok || item != core.ItemWheatSeeds {
		t.Fatalf("BlockDrop(阶段 0) = (%d,%v)，本用例的前提失效", item, ok)
	}
	if placement, ok := core.ItemPlacement(item); !ok || placement != core.WheatStage0ID {
		t.Fatalf("ItemPlacement(种子) = (%d,%v)，往返已不成立，本用例的前提失效",
			placement, ok)
	}
	for _, block := range farmingBlockIDs {
		if _, ok := companionPlaceableBlock(block); ok {
			t.Fatalf("companionPlaceableBlock(%d) 放行，伙伴必须被显式拒绝", block)
		}
	}
}

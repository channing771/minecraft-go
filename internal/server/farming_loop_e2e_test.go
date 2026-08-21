package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

// 端到端脚本用到的固定坐标。玩家在 flatGenerator 的平坦世界里出生在 (0,0) 这
// 一列的地面上（草在 y=0，其上全是空气），因此脚下那格就是 farmingSurface。
//
// 脚本把玩家**向下挖两格**换两块石头，落到 y=-2 那一层再耕作：这样作物所在的
// 竖井里没有任何非空气方块，露天判定天然成立，而两块石头的掉落物都恰好落在
// 玩家脚边（拾取距离 1.25，方块中心到玩家 0.5），全程不需要移动。
var (
	// farmingSurface 是出生时的落脚格；夹具把它由草改成石头。
	farmingSurface = core.BlockPos{}
	// farmingCrop 是挖掉第二格石头后空出来的格，也就是种子长出来的位置。
	farmingCrop = core.BlockPos{Y: -1}
	// farmingGround 是挖穿两层后的新落脚格；夹具把它由石头改成草，好被翻。
	farmingGround = core.BlockPos{Y: -2}
	// farmingWater 是灌溉水源，与耕地同层、水平距离 2（湿润窗口是 9×9）。
	// 六个面全是石头，因此它在流体规则下是不动点，绝不会流走。
	farmingWater = core.BlockPos{X: 2, Y: -2}
)

const (
	// farmingMineBudget 是单块石头徒手采掘的 tick 预算。规则是 30 tick，
	// 外加下落与掉落物拾取延迟，60 已经宽裕；超出即判定脚本卡住而不是慢。
	farmingMineBudget = 60
	// farmingGrowthBudget 是「耕地转湿 + 七次阶段推进」的 tick 预算。
	// RandomTicksPerSection 置到上限 64 时单格每 tick 被抽中约 1/64，
	// 八次命中期望约 512 tick；预算给到 6000 以吸收哈希分布的抖动。
	farmingGrowthBudget = 6000
	// farmingSettleTicks 是一条命令之后让方块变更与背包发布收敛的 tick 数。
	farmingSettleTicks = 3
	// farmingPickupTicks 是等掉落物过完拾取延迟（默认 10 tick）并入包的 tick 数。
	farmingPickupTicks = 40
)

// TestFarmingLoopEndToEndMemory 是组 1–6 的集成回归：一名**从未存在过**的玩家
// 登录之后，只靠一次性材料包里的种子和自己挖来的石头，走完
// 「登录 → 得石头 → 合成石锄 → 翻地 → 种 → 长到成熟 → 收 → 再种」整条闭环。
//
// 三条刻意的设计约束，每条都对应一类假绿：
//
//  1. **成熟必须靠生长跑出来**。脚本只写入 WheatStage0ID，随后把生长概率置
//     100、抽样率置上限，再推进权威 tick 等它自己走到 WheatStage7ID。直接
//     SetBlock 成熟阶段的话，组 6 的生长规则一行都没被覆盖。
//  2. **循环自持断言的是精确数列 63 → 65 → 64**，不是「种子还剩一些」。
//     成熟小麦少掉一颗种子（收获后 64、再种后 63）在「> 0」下照样全绿，
//     而那正是闭环从自持变成净亏的分界。
//  3. **掉落物必须走既有拾取路径**。脚本不往背包里塞收获产物，只是站在原地
//     等 DropPickupDelayTicks 过去；因此「收获产出」与「产出真的进得了背包」
//     是两件被同时钉住的事。
//
// 石头刻意选**徒手采掘**而不是夹具直塞背包：夹具塞背包会让「材料包不含石镐、
// 玩家得自己动手」这条前提凭空消失，而徒手挖石头本来就是玩家拿到第一把锄头
// 的真实路径。夹具只负责摆放地形（脚下换石头、井底换草、埋一格水源），
// 不碰任何背包状态。
func TestFarmingLoopEndToEndMemory(t *testing.T) {
	// 生长需要把 sim 的全局 tunable 调到端点值；用完必须还原，否则会污染
	// 同包内后续用例。
	t.Cleanup(func() { sim.SetTunables(sim.DefaultTunables()) })

	identity := integrationIdentity(0x9e, "Farmer")
	// 刻意**不**预存玩家：只有 LoadPlayer 返回 ErrPlayerNotFound 的路径才会
	// 构造一次性材料包，而脚本的第一步正是要看这份材料包里的种子。
	store := storage.NewMemory(storage.Metadata{
		FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
	})

	config := hostTestConfig()
	config.ViewRadius = 1
	// 生长阶段要跑数千个 tick，自动存盘调远一点，免得脚本里混进无关的存盘噪声。
	config.AutosaveTicks = 1 << 20
	host := mustNewHost(t, config, flatGenerator{}, store)
	endpoint, _, closeTransport := openParityTransport(t, host, "memory", identity)
	t.Cleanup(func() {
		_ = endpoint.Close()
		ctx, cancel := context.WithTimeout(context.Background(), waitDeadline)
		defer cancel()
		_ = host.Shutdown(ctx)
		closeTransport()
	})

	mirror := client.NewMirror()
	step := func() {
		t.Helper()
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			if rejected, ok := message.(network.CommandRejected); ok {
				t.Fatalf("端到端脚本的命令被拒绝: %+v", rejected)
			}
		}
	}
	send := func(command network.ClientMessage) {
		t.Helper()
		sendIntegration(t, endpoint, command)
		waitIntegrationCondition(t, fmt.Sprintf("farming loop %T queued", command), func() bool {
			return len(host.world.incoming) > 0
		})
		step()
	}
	settle := func() {
		t.Helper()
		for range farmingSettleTicks {
			step()
		}
	}
	authoritativeInventory := func() core.Inventory {
		t.Helper()
		host.mu.Lock()
		active := host.activeByPlayer[identity.PlayerID]
		host.mu.Unlock()
		if active == nil {
			t.Fatal("玩家还没有 active 会话")
		}
		snapshot, ok := host.world.PlayerSnapshotFor(active.Session)
		if !ok {
			t.Fatal("没有权威玩家快照")
		}
		return snapshot.Inventory
	}
	mirrorBlock := func(position core.BlockPos) core.BlockID {
		t.Helper()
		block, loaded := mirror.BlockAt(core.Overworld, position)
		if !loaded {
			t.Fatalf("%+v 没有进入客户端镜像", position)
		}
		return block
	}

	// —— 第 1 步：登录即持有 64 颗种子，且全身上下没有锄头 ——
	ready, inventoryReady := false, false
	for !ready || !inventoryReady || !parityViewLoaded(mirror) {
		_, messages := parityStep(t, host, endpoint, mirror)
		for _, message := range messages {
			switch message := message.(type) {
			case network.PlayerState:
				ready = ready || message.Ready
			case network.InventoryState:
				inventoryReady = inventoryReady ||
					message.Inventory.Backpack[starterSeedSlot].Item == core.ItemWheatSeeds
			}
		}
	}
	start := authoritativeInventory()
	wantSeeds := core.ItemStack{Item: core.ItemWheatSeeds, Count: core.MaxStackCount}
	if got := start.Backpack[starterSeedSlot]; got != wantSeeds {
		t.Fatalf("登录后材料包第 %d 格 = %+v，想要 %+v",
			starterSeedSlot+1, got, wantSeeds)
	}
	// 「没有锄头」必须扫全部 36 格：只看快捷栏的话，一个把锄头塞进背包的
	// 材料包照样能让这一步绿，而那种材料包会让后面的合成步骤失去意义。
	forEachStack(start, func(slot int, stack core.ItemStack) {
		if core.TillingTool(stack.Item) {
			t.Fatalf("登录时统一索引 %d 已持有锄头 %+v，材料包不该发锄头", slot, stack)
		}
	})
	if countItem(start, core.ItemStone) != 0 {
		t.Fatalf("登录时已持有石头 %d 个，材料包不该发石头", countItem(start, core.ItemStone))
	}

	// —— 第 2 步：徒手挖两格石头，合成石锄 ——
	//
	// 三处夹具只摆地形：脚下换石头（平坦测试世界里地表之上没有裸露的石头，
	// 而这一步要考的是合成链而不是采掘规则——后者由组 5 与既有采掘集成用例
	// 覆盖）、井底换草（挖穿两层后要有可翻的地面）、埋一格水源（玩家还没有
	// 水桶，灌溉只能由夹具提供）。
	host.world.SetBlockForTest(farmingSurface, core.StoneID)
	host.world.SetBlockForTest(farmingGround, core.GrassID)
	host.world.SetBlockForTest(farmingWater, core.WaterSourceID)

	sequence := uint64(0)
	// 挖石头前把选中格切到空的第 9 格：拾到第一块石头之后，第 0 格里就有石头
	// 了，而"手持石头挖石头"在采掘规则里是**错误工具**（30 tick、不掉落），
	// 第二块石头会白挖。空手才是石头的合法采掘手。
	sequence++
	send(network.SelectHotbar{Sequence: sequence, Slot: core.HotbarSlots - 1})
	settle()
	mineOnce := func(target core.BlockPos) {
		t.Helper()
		sequence++
		send(network.PlayerInput{Sequence: sequence, Pitch: tillSoilLookDown, Mining: true})
		for ticks := 0; mirrorBlock(target) != core.AirID; ticks++ {
			if ticks > farmingMineBudget {
				t.Fatalf("按住采掘 %d 个 tick 仍未挖开 %+v", ticks, target)
			}
			step()
		}
		// 目标一破就立刻松手：采掘键不松的话，下落之后的新落脚格（井底那格
		// 草，5 tick 就能挖穿）会被顺手挖掉，耕作的地面凭空消失。
		sequence++
		send(network.PlayerInput{Sequence: sequence, Pitch: tillSoilLookDown})
	}
	mineOnce(farmingSurface)
	mineOnce(farmingCrop)
	for ticks := 0; countItem(authoritativeInventory(), core.ItemStone) != 2; ticks++ {
		if ticks > farmingPickupTicks {
			t.Fatalf("等了 %d 个 tick 仍未拾满两块石头，当前背包 = %+v",
				ticks, authoritativeInventory())
		}
		step()
	}
	// 井底那格草有没有被顺手挖掉，由后面的翻地步骤兜底：草没了玩家就落到石头
	// 上，TillSoil 会被 RejectInvalidBlock 拒绝，而 step 对任何拒绝都直接 Fatal。
	// 这里不读客户端镜像——夹具写入不经 recordChange，镜像里井底仍是原来的石头。

	if core.RecipeStoneHoe != 9 {
		t.Fatalf("RecipeStoneHoe = %d，端到端脚本按 recipe 9 合成石锄", core.RecipeStoneHoe)
	}
	sequence++
	send(network.CraftRecipe{Sequence: sequence, Recipe: core.RecipeStoneHoe})
	settle()
	full, _ := core.ItemMaxDurability(core.ItemStoneHoe)
	wantHoe := core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: full}
	crafted := authoritativeInventory()
	if got := crafted.Hotbar.Slots[0]; got != wantHoe {
		t.Fatalf("合成后第 0 格 = %+v，想要满耐久石锄 %+v", got, wantHoe)
	}
	if got := countItem(crafted, core.ItemStone); got != 0 {
		t.Fatalf("合成后剩余石头 = %d，想要 0（石锄恰好吃掉两块）", got)
	}
	// 翻地只认权威选中格，把锄头那一格选回来。
	sequence++
	send(network.SelectHotbar{Sequence: sequence, Slot: 0})
	settle()
	if got := authoritativeInventory().Hotbar.Selected; got != 0 {
		t.Fatalf("权威选中格 = %d，想要 0（翻地只认选中格）", got)
	}

	// 种子必须先搬进快捷栏：PlaceBlock 只接受 0..8 的快捷栏栏位。
	sequence++
	send(network.MoveInventoryStack{
		Sequence: sequence, From: uint8(core.HotbarSlots + starterSeedSlot), To: 1,
	})
	settle()
	if got := authoritativeInventory().Hotbar.Slots[1]; got != wantSeeds {
		t.Fatalf("搬运后快捷栏第 1 格 = %+v，想要 %+v", got, wantSeeds)
	}

	// —— 第 3 步：翻地，耐久 −1 ——
	sequence++
	send(network.TillSoil{Sequence: sequence, Pitch: tillSoilLookDown})
	settle()
	if got := mirrorBlock(farmingGround); got != core.FarmlandDryID {
		t.Fatalf("翻地后落脚格 = %d，想要干耕地 %d", got, core.FarmlandDryID)
	}
	wantWornHoe := core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: full - 1}
	if got := authoritativeInventory().Hotbar.Slots[0]; got != wantWornHoe {
		t.Fatalf("翻地后锄头 = %+v，想要恰好扣一点的 %+v", got, wantWornHoe)
	}

	// —— 第 4 步：种下一颗，种子 64 → 63 ——
	sequence++
	send(network.PlaceBlock{Sequence: sequence, Pitch: tillSoilLookDown, Slot: 1})
	settle()
	if got := mirrorBlock(farmingCrop); got != core.WheatStage0ID {
		t.Fatalf("种下后作物格 = %d，想要第一阶段 %d", got, core.WheatStage0ID)
	}
	seedsAfterPlant := countItem(authoritativeInventory(), core.ItemWheatSeeds)
	if seedsAfterPlant != 63 {
		t.Fatalf("种下一颗后种子 = %d，想要 63", seedsAfterPlant)
	}

	// —— 第 5 步：靠随机 tick 生长到成熟 ——
	//
	// 只调概率与抽样率两个 tunable，生长规则本身一行没动：脚本从不写入
	// WheatStage1..7 中的任何一个，成熟完全是 advanceCrops 跑出来的。
	tunables := sim.DefaultTunables()
	tunables.RandomTicksPerSection = 64
	tunables.CropGrowthChancePercent = 100
	sim.SetTunables(tunables)
	growthTicks := 0
	for ; mirrorBlock(farmingCrop) != core.WheatStage7ID; growthTicks++ {
		if growthTicks > farmingGrowthBudget {
			t.Fatalf("推进 %d 个 tick 后作物仍是 %d，想要成熟 %d（耕地 = %d）",
				growthTicks, mirrorBlock(farmingCrop), core.WheatStage7ID,
				mirrorBlock(farmingGround))
		}
		step()
	}
	if growthTicks == 0 {
		t.Fatal("作物在零个 tick 内就成熟了：生长根本没被推进")
	}
	t.Logf("作物由随机 tick 从 stage0 长到成熟用了 %d 个权威 tick", growthTicks)
	if got := mirrorBlock(farmingGround); got != core.FarmlandWetID {
		t.Fatalf("成熟时耕地 = %d，想要湿耕地 %d（生长的前置条件）",
			got, core.FarmlandWetID)
	}

	// —— 第 6 步：收获，1 小麦 + 2 种子经既有拾取路径入包 ——
	sequence++
	send(network.PlayerInput{Sequence: sequence, Pitch: tillSoilLookDown, Mining: true})
	sequence++
	send(network.PlayerInput{Sequence: sequence, Pitch: tillSoilLookDown})
	if got := mirrorBlock(farmingCrop); got != core.AirID {
		t.Fatalf("收获后作物格 = %d，想要空气（成熟作物 1 tick 破坏）", got)
	}
	for ticks := 0; countItem(authoritativeInventory(), core.ItemWheat) != 1; ticks++ {
		if ticks > farmingPickupTicks {
			t.Fatalf("等了 %d 个 tick 仍未拾到小麦，当前背包 = %+v",
				ticks, authoritativeInventory())
		}
		step()
	}
	harvested := authoritativeInventory()
	if got := countItem(harvested, core.ItemWheat); got != 1 {
		t.Fatalf("收获后小麦 = %d，想要恰好 1", got)
	}
	seedsAfterHarvest := countItem(harvested, core.ItemWheatSeeds)
	if seedsAfterHarvest != 65 {
		t.Fatalf("收获后种子 = %d，想要 65（种下后 63 + 掉落 2）", seedsAfterHarvest)
	}
	if got := mirrorBlock(farmingGround); !core.IsFarmland(got) {
		t.Fatalf("收获后落脚格 = %d，想要仍是耕地", got)
	}

	// —— 第 7 步：再种一颗，断言循环自持 ——
	sequence++
	send(network.PlaceBlock{Sequence: sequence, Pitch: tillSoilLookDown, Slot: 1})
	settle()
	if got := mirrorBlock(farmingCrop); got != core.WheatStage0ID {
		t.Fatalf("再种后作物格 = %d，想要第一阶段 %d", got, core.WheatStage0ID)
	}
	seedsAfterReplant := countItem(authoritativeInventory(), core.ItemWheatSeeds)
	if seedsAfterReplant != 64 {
		t.Fatalf("再种后种子 = %d，想要 64", seedsAfterReplant)
	}
	// 精确数列 63 → 65 → 64 已逐点断言；这一条把「自持」这个结论本身也写下来：
	// 种一收一之后手里的种子比种下那一刻**多**，闭环才是净赚而不是净亏。
	if seedsAfterReplant <= seedsAfterPlant {
		t.Fatalf("一轮种收之后种子 %d → %d，闭环没有自持",
			seedsAfterPlant, seedsAfterReplant)
	}
}

// forEachStack 按统一索引 0..35 遍历完整物品状态的每一格。
func forEachStack(inventory core.Inventory, visit func(slot int, stack core.ItemStack)) {
	for slot, stack := range inventory.Hotbar.Slots {
		visit(slot, stack)
	}
	for slot, stack := range inventory.Backpack {
		visit(core.HotbarSlots+slot, stack)
	}
}

// countItem 统计完整物品状态里某种物品的总数量。
func countItem(inventory core.Inventory, item core.ItemID) int {
	total := 0
	forEachStack(inventory, func(_ int, stack core.ItemStack) {
		if stack.Item == item {
			total += int(stack.Count)
		}
	})
	return total
}

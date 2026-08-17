// mine/place 步骤编排的端到端测试：httptest 假模型产出 mine/place 计划，
// 验证事件序列（Accepted→TaskStarted→[TaskProgress]→终态）、走入交互距离
// 后按住采掘至完成（产物入包、耐久扣减）、背包无容量以 TaskFailInventoryFull
// 失败且方块不变、放置成功同 tick 原子扣料、物品耗尽失败且已成交变更保留、
// 目标被改按目标变化语义（WorldChanged）处理，以及目标远处时先走近再交互。
// 伴身体经存档种子携带确定性背包（工具/物品），世界方块经 SetBlockForTest
// 构造固定目标，绝不访问真实模型服务。
package server

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/sim"
	"github.com/channing771/mornlea/internal/storage"
)

// interactionCompanionPosition 是种子伙伴的出生位置：平地草面 Y=0 之上的
// 站立高度 Y=1，远离玩家出生点，避免路径与玩家体碰撞互相干扰。
var interactionCompanionPosition = [3]float32{4.5, 1, 4.5}

// newInteractionHost 构造带背包种子的交互测试 Host：存档预置一条与配置 ID
// 匹配的伙伴身体记录，NewHost 因此按已知位置与背包恢复它（而不是随机出生
// 扫描），测试由此获得确定性的起点几何与物品事实。
func newInteractionHost(
	t *testing.T,
	id companion.ID,
	model *fakeCompanionModel,
	inventory core.Inventory,
) (*Host, network.ClientEndpoint) {
	t.Helper()
	store := newHostTestStore()
	seed := companion.Body{
		ID:        id,
		Dimension: core.Overworld,
		Position:  interactionCompanionPosition,
		Inventory: inventory,
	}
	if err := store.MemoryStore.SaveCompanions(
		context.Background(), storage.CompanionSave{Revision: 1, Records: []companion.Body{seed}},
	); err != nil {
		t.Fatalf("种子伙伴身体: %v", err)
	}
	config := hostTestConfig()
	config.Companions = []companion.Definition{{ID: id, Name: "阿木"}}
	config.MaxPlayers = 2
	config.OutboxCapacity = 4096
	if model != nil {
		config.AIModel.Endpoint = model.server.URL + "/v1"
	}
	host := mustNewHost(t, config, flatTestGenerator{}, store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Errorf("Host.Shutdown: %v", err)
		}
	})
	client := openCompanionChatClient(t, host, "memory", integrationIdentity(0xa1, "发令者"))
	stepUntilCompanionManagerReady(t, host, []network.ClientEndpoint{client}, id)
	return host, client
}

// interactionBlockAt 读取世界方块的当前权威值（测试断言用）。
func interactionBlockAt(t *testing.T, host *Host, position core.BlockPos) core.BlockID {
	t.Helper()
	chunk, _, ready := host.world.engine.CloneReadyChunk(core.ChunkKey{
		Dimension: core.Overworld,
		Pos:       position.Chunk(),
	})
	if !ready {
		t.Fatalf("目标 %v 所在区块未 ready", position)
	}
	return chunk.BlockAt(
		int(position.X&core.SectionMask), position.Y, int(position.Z&core.SectionMask))
}

// setInteractionBlock 在世界中放置一个测试目标方块（构造 mine 目标或占位）。
func setInteractionBlock(t *testing.T, host *Host, position core.BlockPos, block core.BlockID) {
	t.Helper()
	host.world.engine.SetBlockForTest(position, block)
	if got := interactionBlockAt(t, host, position); got != block {
		t.Fatalf("设置方块 %v=%d 失败，读到 %d", position, block, got)
	}
}

// minePlanJSON 构造单一 mine 步骤的计划正文（假模型脚本条目）。
func minePlanJSON(target core.BlockPos) string {
	encoded, _ := json.Marshal(map[string]any{
		"summary": "采掘目标",
		"steps":   []map[string]any{{"kind": "mine", "x": target.X, "y": target.Y, "z": target.Z}},
	})
	return string(encoded)
}

// placePlanJSON 构造一串 place 步骤的计划正文（假模型脚本条目）。
func placePlanJSON(blockName string, targets ...core.BlockPos) string {
	steps := make([]map[string]any, 0, len(targets))
	for _, target := range targets {
		steps = append(steps, map[string]any{
			"kind": "place", "x": target.X, "y": target.Y, "z": target.Z, "block": blockName,
		})
	}
	encoded, _ := json.Marshal(map[string]any{"summary": "放置方块", "steps": steps})
	return string(encoded)
}

// interactionInventoryCount 统计背包中某物品的总数（36 格统一索引）。
func interactionInventoryCount(inventory core.Inventory, item core.ItemID) int {
	count := 0
	for slot := uint8(0); slot < core.InventorySlots; slot++ {
		stack, _ := inventory.Slot(slot)
		if stack.Item == item {
			count += int(stack.Count)
		}
	}
	return count
}

// fullStoneInventory 构造 36 格全满的石料背包：每个栏位都是 64 件 ItemStone，
// 任何非石料产物都无法合入或占位——「背包无容量」的确定性构造。
func fullStoneInventory() core.Inventory {
	var inventory core.Inventory
	stack := core.ItemStack{Item: core.ItemStone, Count: core.MaxStackCount}
	for slot := range inventory.Hotbar.Slots {
		inventory.Hotbar.Slots[slot] = stack
	}
	for slot := range inventory.Backpack {
		inventory.Backpack[slot] = stack
	}
	return inventory
}

// pickaxeInventory 构造手握完好石镐的最小背包（快捷栏栏位 0 即选中栏位）。
func pickaxeInventory() core.Inventory {
	durability, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: durability,
	}
	return inventory
}

// collectInteractionEvents 发送指令并逐 tick 收集该指令的全部任务事件，
// 直到 stop 返回 true 或达到 maxTicks；返回收集到的事件全集（含跨 tick）。
func collectInteractionEvents(
	t *testing.T,
	host *Host,
	client network.ClientEndpoint,
	command string,
	maxTicks int,
	stop func(events []network.ChatEvent) bool,
) []network.ChatEvent {
	t.Helper()
	sendIntegration(t, client, network.ChatCommand{Text: command})
	waitForIncomingChatDepth(t, host.world, 1)
	var collected []network.ChatEvent
	for range maxTicks {
		result := host.world.StepForTest()
		collected = append(collected,
			companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
		if stop != nil && stop(collected) {
			return collected
		}
	}
	return collected
}

// interactionEventsOf 过滤出指定指令文本的事件（排除其他源）。
func interactionEventsOf(events []network.ChatEvent, command string) []network.ChatEvent {
	filtered := make([]network.ChatEvent, 0, len(events))
	for _, event := range events {
		if event.Command == command {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

// assertInteractionEventSequence 断言事件类别序列与 EventID 严格递增，并逐条
// 校验事件合法性。
func assertInteractionEventSequence(
	t *testing.T,
	events []network.ChatEvent,
	wantKinds ...network.ChatEventKind,
) {
	t.Helper()
	if len(events) != len(wantKinds) {
		t.Fatalf("事件序列=%v，想要 %v", chatEventKinds(events), wantKinds)
	}
	for index, event := range events {
		if event.Kind != wantKinds[index] {
			t.Fatalf("事件序列=%v，想要 %v", chatEventKinds(events), wantKinds)
		}
		if err := event.Validate(); err != nil {
			t.Fatalf("事件 %d Validate: %v", event.EventID, err)
		}
	}
	assertStrictlyIncreasingEventIDs(t, events)
}

// interactionFailureReason 返回序列中唯一 TaskFailed 事件的失败原因；序列
// 不含失败或含多条时直接 Fatal。
func interactionFailureReason(t *testing.T, events []network.ChatEvent) network.TaskFailReason {
	t.Helper()
	var failed []network.ChatEvent
	for _, event := range events {
		if event.Kind == network.ChatEventTaskFailed {
			failed = append(failed, event)
		}
	}
	if len(failed) != 1 {
		t.Fatalf("TaskFailed 事件数=%d（事件=%v），想要恰好 1 次",
			len(failed), chatEventKinds(events))
	}
	return network.TaskFailReason(failed[0].RejectReason)
}

// waitInteractionMining 逐 tick 推进并观察权威 TickResult 中目标伙伴的采掘
// 进度，直到满足 want 或超时；返回最后一次观察与窗口内收到的全部任务事件
// （保持客户端消息流同步，等待期事件不丢失）。
func waitInteractionMining(
	t *testing.T,
	host *Host,
	client network.ClientEndpoint,
	id companion.ID,
	maxTicks int,
	want func(mining sim.MiningUpdate) bool,
) (sim.MiningUpdate, []network.ChatEvent) {
	t.Helper()
	last := sim.MiningUpdate{}
	var events []network.ChatEvent
	for range maxTicks {
		result := host.world.StepForTest()
		events = append(events,
			companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
		for _, update := range result.Companions {
			if update.ID == id {
				last = update.Mining
			}
		}
		if want(last) {
			return last, events
		}
	}
	t.Fatalf("等待采掘进度超时：最后观察=%+v", last)
	return last, events
}

// horizontalDistance 返回两个位置的水平（XZ）距离。
func horizontalDistance(a, b [3]float32) float32 {
	dx, dz := a[0]-b[0], a[2]-b[2]
	return float32(math.Sqrt(float64(dx*dx + dz*dz)))
}

// TestCompanionManagerMineCompletesWithEventsAndLoot 验证 mine 步骤的完整
// 编排：走入交互距离后持续按住采掘直至完成，事件序列恰为 Accepted→
// TaskStarted→TaskCompleted 且 EventID 严格递增；完成 tick 三方原子成立——
// 目标方块变为空气、石镐耐久按既有规则扣减一件、产物（煤炭）直接出现在
// 伙伴背包中。
func TestCompanionManagerMineCompletesWithEventsAndLoot(t *testing.T) {
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	host, client := newInteractionHost(t, id, model, pickaxeInventory())
	target := core.BlockPos{X: 8, Y: 1, Z: 4}
	setInteractionBlock(t, host, target, core.CoalOreID)
	start := currentCompanionBody(t, host, id).Position
	model.setPlanScript(minePlanJSON(target))

	events := collectInteractionEvents(t, host, client, "@阿木 挖掉那块矿", 900,
		func(events []network.ChatEvent) bool {
			return len(eventsWithKind(events, network.ChatEventTaskCompleted)) > 0 ||
				len(eventsWithKind(events, network.ChatEventTaskFailed)) > 0
		})
	taskEvents := interactionEventsOf(events, "挖掉那块矿")
	assertInteractionEventSequence(t, taskEvents,
		network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskCompleted)

	if got := interactionBlockAt(t, host, target); got != core.AirID {
		t.Fatalf("采掘完成后目标方块=%d，想要空气", got)
	}
	body := currentCompanionBody(t, host, id)
	if count := interactionInventoryCount(body.Inventory, core.ItemCoal); count != 1 {
		t.Fatalf("产物煤炭数量=%d，想要 1（背包=%+v）", count, body.Inventory)
	}
	pickaxe := body.Inventory.Hotbar.Slots[0]
	if pickaxe.Item != core.ItemStonePickaxe || pickaxe.Durability != 130 {
		t.Fatalf("石镐=%+v，想要耐久 131→130", pickaxe)
	}
	// 采掘发生在交互距离内：最终位置必须比起始位置显著逼近目标。
	targetCenter := [3]float32{float32(target.X) + 0.5, 1, float32(target.Z) + 0.5}
	if horizontalDistance(body.Position, targetCenter) >= horizontalDistance(start, targetCenter) {
		t.Fatalf("完成后位置 %v 未比起始 %v 更接近目标 %v", body.Position, start, targetCenter)
	}
}

// TestCompanionManagerMineInventoryFullKeepsBlock 验证背包无容量的失败语义：
// 36 格全满时采掘按既有规则累积至满格，sim 容量前验拒绝结算（方块保持），
// Manager 观察到「就绪但无容量」的稳定状态后以 TaskFailInventoryFull 失败，
// 方块与背包都必须保持不变。
func TestCompanionManagerMineInventoryFullKeepsBlock(t *testing.T) {
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	host, client := newInteractionHost(t, id, model, fullStoneInventory())
	target := core.BlockPos{X: 8, Y: 1, Z: 4}
	setInteractionBlock(t, host, target, core.DirtID)
	model.setPlanScript(minePlanJSON(target))

	events := collectInteractionEvents(t, host, client, "@阿木 挖那块土", 900,
		func(events []network.ChatEvent) bool {
			return len(eventsWithKind(events, network.ChatEventTaskFailed)) > 0 ||
				len(eventsWithKind(events, network.ChatEventTaskCompleted)) > 0
		})
	taskEvents := interactionEventsOf(events, "挖那块土")
	assertInteractionEventSequence(t, taskEvents,
		network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskFailed)
	if reason := interactionFailureReason(t, taskEvents); reason != network.TaskFailInventoryFull {
		t.Fatalf("失败原因=%d，想要 TaskFailInventoryFull", reason)
	}
	if got := interactionBlockAt(t, host, target); got != core.DirtID {
		t.Fatalf("无容量失败后目标方块=%d，想要保持泥土", got)
	}
	body := currentCompanionBody(t, host, id)
	if count := interactionInventoryCount(body.Inventory, core.ItemStone); count != core.InventorySlots*core.MaxStackCount {
		t.Fatalf("背包石料总数=%d，想要保持 %d（背包 MUST 不变）",
			count, core.InventorySlots*core.MaxStackCount)
	}
	if count := interactionInventoryCount(body.Inventory, core.ItemDirt); count != 0 {
		t.Fatalf("背包出现泥土 %d 件，想要 0", count)
	}
}

// TestCompanionManagerMineTargetReplacedFailsWorldChanged 验证采掘中目标被
// 其他 actor 替换的目标变化语义：进度过半前替换目标方块，sim 的目标替换
// 失效语义令进度重置，Manager 观察到进度回退后以 TaskFailWorldChanged 失败，
// 新方块 MUST NOT 被破坏或继承进度。
func TestCompanionManagerMineTargetReplacedFailsWorldChanged(t *testing.T) {
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	host, client := newInteractionHost(t, id, model, core.Inventory{})
	target := core.BlockPos{X: 8, Y: 1, Z: 4}
	// 石头无工具需要 30 tick，给替换留出确定的观察窗口。
	setInteractionBlock(t, host, target, core.StoneID)
	model.setPlanScript(minePlanJSON(target))

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 挖那块石头"})
	waitForIncomingChatDepth(t, host.world, 1)
	// 等待采掘真正累积（进度 ≥ 2）再替换：保证 Manager 已记录进度事实。
	_, waitEvents := waitInteractionMining(t, host, client, id, 600, func(mining sim.MiningUpdate) bool {
		return mining.Active && mining.ProgressTicks >= 2
	})
	setInteractionBlock(t, host, target, core.DirtID)

	collected := waitEvents
	for range 200 {
		result := host.world.StepForTest()
		collected = append(collected,
			companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
		if len(eventsWithKind(collected, network.ChatEventTaskFailed)) > 0 {
			break
		}
	}
	taskEvents := interactionEventsOf(collected, "挖那块石头")
	assertInteractionEventSequence(t, taskEvents,
		network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskFailed)
	if reason := interactionFailureReason(t, taskEvents); reason != network.TaskFailWorldChanged {
		t.Fatalf("失败原因=%d，想要 TaskFailWorldChanged", reason)
	}
	if got := interactionBlockAt(t, host, target); got != core.DirtID {
		t.Fatalf("替换后的方块=%d，想要保持泥土（新方块 MUST NOT 被破坏）", got)
	}
	body := currentCompanionBody(t, host, id)
	if count := interactionInventoryCount(body.Inventory, core.ItemDirt); count != 0 {
		t.Fatalf("背包出现泥土 %d 件，想要 0（新方块不得被收获）", count)
	}
}

// TestCompanionManagerPlaceAtomicConsumeAndDepletion 验证 place 步骤的原子
// 扣料与物品耗尽语义：成功放置时目标位置出现计划方块且背包对应堆恰好减少
// 一件（同一权威 tick 成立）；计划连续放置两次而背包只剩一件时，第一次
// 放置的成交结果（世界与背包变更）保留，第二次以 TaskFailInventoryFull
// 失败且不再改变世界。
func TestCompanionManagerPlaceAtomicConsumeAndDepletion(t *testing.T) {
	t.Run("AtomicConsume", func(t *testing.T) {
		id := chatTestCompanionID(1)
		model := newFakeCompanionModel(t)
		var inventory core.Inventory
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 3}
		host, client := newInteractionHost(t, id, model, inventory)
		target := core.BlockPos{X: 6, Y: 1, Z: 4}
		model.setPlanScript(placePlanJSON("dirt", target))

		events := collectInteractionEvents(t, host, client, "@阿木 垫一块土", 900,
			func(events []network.ChatEvent) bool {
				return len(eventsWithKind(events, network.ChatEventTaskCompleted)) > 0 ||
					len(eventsWithKind(events, network.ChatEventTaskFailed)) > 0
			})
		taskEvents := interactionEventsOf(events, "垫一块土")
		assertInteractionEventSequence(t, taskEvents,
			network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskCompleted)
		if got := interactionBlockAt(t, host, target); got != core.DirtID {
			t.Fatalf("放置后目标方块=%d，想要泥土", got)
		}
		body := currentCompanionBody(t, host, id)
		if count := interactionInventoryCount(body.Inventory, core.ItemDirt); count != 2 {
			t.Fatalf("背包泥土=%d，想要 3-1=2（原子扣一件）", count)
		}
	})

	t.Run("DepletionKeepsFirstPlacement", func(t *testing.T) {
		id := chatTestCompanionID(1)
		model := newFakeCompanionModel(t)
		var inventory core.Inventory
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 1}
		host, client := newInteractionHost(t, id, model, inventory)
		first := core.BlockPos{X: 6, Y: 1, Z: 4}
		second := core.BlockPos{X: 10, Y: 1, Z: 4}
		model.setPlanScript(placePlanJSON("dirt", first, second))

		events := collectInteractionEvents(t, host, client, "@阿木 垫两块土", 900,
			func(events []network.ChatEvent) bool {
				return len(eventsWithKind(events, network.ChatEventTaskFailed)) > 0 ||
					len(eventsWithKind(events, network.ChatEventTaskCompleted)) > 0
			})
		taskEvents := interactionEventsOf(events, "垫两块土")
		assertInteractionEventSequence(t, taskEvents,
			network.ChatEventAccepted, network.ChatEventTaskStarted,
			network.ChatEventTaskProgress, network.ChatEventTaskFailed)
		if reason := interactionFailureReason(t, taskEvents); reason != network.TaskFailInventoryFull {
			t.Fatalf("失败原因=%d，想要 TaskFailInventoryFull", reason)
		}
		if got := interactionBlockAt(t, host, first); got != core.DirtID {
			t.Fatalf("已成交的首个放置=%d，想要泥土（成交变更 MUST 保留）", got)
		}
		if got := interactionBlockAt(t, host, second); got != core.AirID {
			t.Fatalf("第二个目标=%d，想要空气（物品耗尽 MUST NOT 放置）", got)
		}
		body := currentCompanionBody(t, host, id)
		if count := interactionInventoryCount(body.Inventory, core.ItemDirt); count != 0 {
			t.Fatalf("背包泥土=%d，想要 0", count)
		}
	})
}

// TestCompanionManagerPlaceTargetOccupiedFailsWorldChanged 验证放置目标被
// 其他 actor 占据时的目标变化语义：Runner 观察到目标非空气且不等于计划
// 方块后以 TaskFailWorldChanged 失败，背包保持不变。
func TestCompanionManagerPlaceTargetOccupiedFailsWorldChanged(t *testing.T) {
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 2}
	host, client := newInteractionHost(t, id, model, inventory)
	target := core.BlockPos{X: 6, Y: 1, Z: 4}
	setInteractionBlock(t, host, target, core.StoneID)
	model.setPlanScript(placePlanJSON("dirt", target))

	events := collectInteractionEvents(t, host, client, "@阿木 垫一块土", 900,
		func(events []network.ChatEvent) bool {
			return len(eventsWithKind(events, network.ChatEventTaskFailed)) > 0 ||
				len(eventsWithKind(events, network.ChatEventTaskCompleted)) > 0
		})
	taskEvents := interactionEventsOf(events, "垫一块土")
	assertInteractionEventSequence(t, taskEvents,
		network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskFailed)
	if reason := interactionFailureReason(t, taskEvents); reason != network.TaskFailWorldChanged {
		t.Fatalf("失败原因=%d，想要 TaskFailWorldChanged", reason)
	}
	if got := interactionBlockAt(t, host, target); got != core.StoneID {
		t.Fatalf("目标方块=%d，想要保持石头", got)
	}
	body := currentCompanionBody(t, host, id)
	if count := interactionInventoryCount(body.Inventory, core.ItemDirt); count != 2 {
		t.Fatalf("背包泥土=%d，想要 2（校验失败不扣料）", count)
	}
}

// TestCompanionManagerMineApproachesBeforeMining 验证距离先行：目标在交互
// 距离之外时，mine 步骤必须先复用 go_to 的移动语义走向目标（位置显著逼
// 近），采掘进度只有在伙伴进入交互距离后才开始累积，随后完成。
func TestCompanionManagerMineApproachesBeforeMining(t *testing.T) {
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	host, client := newInteractionHost(t, id, model, core.Inventory{})
	target := core.BlockPos{X: 14, Y: 1, Z: 4}
	setInteractionBlock(t, host, target, core.DirtID)
	start := currentCompanionBody(t, host, id).Position
	model.setPlanScript(minePlanJSON(target))

	sendIntegration(t, client, network.ChatCommand{Text: "@阿木 去挖远处的土"})
	waitForIncomingChatDepth(t, host.world, 1)

	// 先确认目标确实在交互距离之外（水平 > 交互距离 6），否则测试几何失效。
	targetCenter := [3]float32{float32(target.X) + 0.5, 1, float32(target.Z) + 0.5}
	if startDistance := horizontalDistance(start, targetCenter); startDistance <= 6 {
		t.Fatalf("测试几何无效：起点距离 %f 必须大于交互距离", startDistance)
	}
	positionAtMiningStart := start
	var collected []network.ChatEvent
	miningStarted := false
	for range 900 {
		result := host.world.StepForTest()
		collected = append(collected,
			companionChatEvents(receiveCompanionChatTick(t, client, result.Tick))...)
		position := currentCompanionBody(t, host, id).Position
		for _, update := range result.Companions {
			if update.ID == id && update.Mining.Active && !miningStarted {
				miningStarted = true
				positionAtMiningStart = position
			}
		}
		if len(eventsWithKind(collected, network.ChatEventTaskCompleted)) > 0 ||
			len(eventsWithKind(collected, network.ChatEventTaskFailed)) > 0 {
			break
		}
	}
	taskEvents := interactionEventsOf(collected, "去挖远处的土")
	assertInteractionEventSequence(t, taskEvents,
		network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskCompleted)
	if !miningStarted {
		t.Fatalf("采掘始终未开始（事件=%v）", chatEventKinds(collected))
	}
	// 走近证据：采掘开始时的位置必须比起始位置显著逼近目标，且落在交互
	// 距离附近（水平距离 ≤ 交互距离 + 一格裕量）。
	approach := horizontalDistance(start, targetCenter) - horizontalDistance(positionAtMiningStart, targetCenter)
	if approach < 3 {
		t.Fatalf("采掘开始前位移不足：起点 %v → 采掘时 %v（目标 %v）",
			start, positionAtMiningStart, targetCenter)
	}
	if distance := horizontalDistance(positionAtMiningStart, targetCenter); distance > 7 {
		t.Fatalf("采掘开始时距离=%f，想要 ≤7（交互距离附近）", distance)
	}
	if got := interactionBlockAt(t, host, target); got != core.AirID {
		t.Fatalf("完成后目标方块=%d，想要空气", got)
	}
}

// TestCompanionManagerPlaceApproachesBeforePlacing 验证 place 的距离先行与
// Runner 侧先验交互距离（C3 Ruling：sim 放置无距离校验）：目标在交互距离
// 之外时必须先走向目标邻近站立格，伙伴未逼近前不得出现放置结果。
func TestCompanionManagerPlaceApproachesBeforePlacing(t *testing.T) {
	id := chatTestCompanionID(1)
	model := newFakeCompanionModel(t)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	host, client := newInteractionHost(t, id, model, inventory)
	target := core.BlockPos{X: 14, Y: 1, Z: 4}
	start := currentCompanionBody(t, host, id).Position
	model.setPlanScript(placePlanJSON("dirt", target))

	events := collectInteractionEvents(t, host, client, "@阿木 去垫远处的土", 900,
		func(events []network.ChatEvent) bool {
			return len(eventsWithKind(events, network.ChatEventTaskCompleted)) > 0 ||
				len(eventsWithKind(events, network.ChatEventTaskFailed)) > 0
		})
	taskEvents := interactionEventsOf(events, "去垫远处的土")
	assertInteractionEventSequence(t, taskEvents,
		network.ChatEventAccepted, network.ChatEventTaskStarted, network.ChatEventTaskCompleted)

	// 走近证据：放置成交时伙伴必须已从起点显著逼近目标。
	targetCenter := [3]float32{float32(target.X) + 0.5, 1, float32(target.Z) + 0.5}
	if startDistance := horizontalDistance(start, targetCenter); startDistance <= 6 {
		t.Fatalf("测试几何无效：起点距离 %f 必须大于交互距离", startDistance)
	}
	final := currentCompanionBody(t, host, id).Position
	if approach := horizontalDistance(start, targetCenter) - horizontalDistance(final, targetCenter); approach < 3 {
		t.Fatalf("放置前位移不足：起点 %v → 完成 %v（目标 %v）", start, final, targetCenter)
	}
	// 起点距离内不得成交：完成前目标一直是空气——起点即在交互距离之外，
	// 任何「提交即成交」的距离旁路都会让方块在位移前出现，被上面的位移
	// 断言与这里的成交断言共同捕获。
	if got := interactionBlockAt(t, host, target); got != core.DirtID {
		t.Fatalf("放置后目标方块=%d，想要泥土", got)
	}
	if count := interactionInventoryCount(currentCompanionBody(t, host, id).Inventory, core.ItemDirt); count != 0 {
		t.Fatalf("背包泥土=%d，想要 0（恰好扣一件）", count)
	}
}

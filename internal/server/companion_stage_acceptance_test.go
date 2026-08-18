// D8 阶段总验收（M5A→M5D）的端到端集成测试：在单一测试内按传输（Memory 与
// TCP）各串一遍完整闭环——磁盘种子存档恢复伙伴身体与背包（M5A 具名伙伴）→
// @指令寻址 → go_to/mine/place 混合计划（follow 三节点已由 D6 wiring 测试
// 承担）→ 开始/进展/终态台词事件序列（进展集合与 SelectProgressSteps 一致、
// 事实与台词共用 EventID 计数器全程严格递增）→ 终态摘要写入 → 关服落盘
// （companions.ai 头部 schema v4 + summary-only 载荷）→ 同一磁盘存档重启
// 恢复摘要 → 新任务台词请求体携带恢复摘要与生效人设 → 双传输台词序列一致。
// 全部使用 httptest 假模型与无窗口测试宿主，绝不打开前台窗口。
//
// persona 的「外部文件 → 真实 config.Load → server.Config → 台词请求体」
// 半链由 cmd/mornlea-server 的阶段验收测试承担：archcheck 的
// TestOnlyCommandsImportConfig 禁止任何 internal 包（含测试导入）依赖
// internal/config，本包无法触达真实配置装载路径；本测试以内联生效人设
// （ResolvedPersona）锁定「生效人设 → 台词请求 → CompanionSpeech 广播」的
// 服务端半链，与 cmd 侧互补构成完整验收。
package server

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/storage"
)

// stageAcceptancePersona 是阶段验收伙伴的生效人设（M5D 用户可观察结果：
// 有 persona 的伙伴广播符合人设语气的台词；人设透传进每一次台词请求）。
const stageAcceptancePersona = "沉稳寡言的老向导，说话简短。"

// stageAcceptanceSummary 与假台词模型终态响应携带的固定摘要一致（D6 起的
// 既有假模型行为），落盘与重启断言都以该文本为基准。
const stageAcceptanceSummary = "最近完成了任务"

// stageAcceptanceSeedInventory 构造阶段验收伙伴的种子背包：快捷栏 0 手握
// 完好石镐（mine 步骤的采掘计时与耐久规则）、快捷栏 1 两块泥土（place 步骤
// 的扣料来源），与交互测试同一构造纪律。
func stageAcceptanceSeedInventory() core.Inventory {
	durability, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	var inventory core.Inventory
	inventory.Hotbar.Slots[0] = core.ItemStack{
		Item: core.ItemStonePickaxe, Count: 1, Durability: durability,
	}
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemDirt, Count: 2}
	return inventory
}

// stageAcceptancePlanJSON 构造 go_to→mine→place 三步混合计划的假模型脚本
// 正文（M5C 交付的四种步骤 kind 中排除 follow——follow 的三节点全集由
// TestCompanionDialogueFollowExactlyThreeNodes 承担）。目标几何刻意拉开间距
// （走向 2 格外 → 回头 6 格挖矿 → 再走 7 格回填），保证相邻台词节点之间有
// 多个权威 tick 的走步窗口，台词往返不被每伙伴单在途约束挤掉。
func stageAcceptancePlanJSON(walkTo, mineTarget, placeTarget core.BlockPos) string {
	steps := []map[string]any{
		{"kind": "go_to", "x": walkTo.X, "y": walkTo.Y, "z": walkTo.Z},
		{"kind": "mine", "x": mineTarget.X, "y": mineTarget.Y, "z": mineTarget.Z},
		{"kind": "place", "x": placeTarget.X, "y": placeTarget.Y, "z": placeTarget.Z, "block": "dirt"},
	}
	encoded, _ := json.Marshal(map[string]any{"summary": "挖矿之后回填", "steps": steps})
	return string(encoded)
}

// stageAcceptanceResult 是单传输一次完整闭环的观察结果：两段生命周期（重启
// 前后）的台词文本序列与各自的事实事件类别序列，供跨传输 parity 比对。
type stageAcceptanceResult struct {
	speechTexts     []string
	firstFactKinds  []network.ChatEventKind
	secondFactKinds []network.ChatEventKind
}

// TestM5StageAcceptancePersonaDialogueEndToEnd 是 M5A–M5D 的阶段总验收：
// 见文件头注释的链路清单。两段生命周期共享同一磁盘存档目录与同一伙伴定义
// （internal/server 侧的「同一 config/personas」等价物：定义在两次 NewHost
// 间保持不变，文件来源解析由 cmd 侧测试覆盖）。
func TestM5StageAcceptancePersonaDialogueEndToEnd(t *testing.T) {
	id := chatTestCompanionID(1)
	definitions := []companion.Definition{{
		ID:              id,
		Name:            "阿木",
		ResolvedPersona: stageAcceptancePersona,
	}}

	// runTransport 在指定传输上执行一次完整闭环（两段生命周期）。
	runTransport := func(transport string) stageAcceptanceResult {
		root := t.TempDir()

		// 磁盘种子存档：确定性出生几何与背包（伙伴身体按已知位置恢复，
		// 不依赖出生扫描的随机结果），与交互测试共用同一出生位置。
		seedStore, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{
			Create: storage.Metadata{
				FormatVersion: 2, Seed: 42, SpawnDimension: core.Overworld,
			},
		})
		if err != nil {
			t.Fatalf("OpenDisk 种子存档: %v", err)
		}
		seed := companion.Body{
			ID:        id,
			Dimension: core.Overworld,
			Position:  interactionCompanionPosition,
			Inventory: stageAcceptanceSeedInventory(),
		}
		if err := seedStore.SaveCompanions(context.Background(), storage.CompanionSave{
			Revision: 1, Records: []companion.Body{seed},
		}); err != nil {
			t.Fatalf("种子伙伴存档: %v", err)
		}
		if err := seedStore.Close(); err != nil {
			t.Fatalf("关闭种子 store: %v", err)
		}

		// openLifetime 打开同一磁盘存档的一段宿主生命周期；Host.Shutdown 会
		// Flush 伙伴持久层并关闭 store，因此每段生命周期重新 OpenDisk。
		// closeHost 经 t.Cleanup 兜底且幂等（Host.Shutdown 幂等）：中途
		// t.Fatalf 时也必须回收 world goroutine 与磁盘 store 句柄，否则会
		// 污染后续测试的 goroutine 基线断言（D6 已裁决的教训）。
		openLifetime := func() (*Host, func()) {
			store, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{})
			if err != nil {
				t.Fatalf("OpenDisk: %v", err)
			}
			config := hostTestConfig()
			config.Companions = append([]companion.Definition(nil), definitions...)
			config.MaxPlayers = 2
			config.OutboxCapacity = 4096
			config.HeartbeatInterval = time.Hour
			config.HeartbeatTimeout = time.Hour
			host := mustNewHost(t, config, flatTestGenerator{}, store)
			closed := false
			closeHost := func() {
				if closed {
					return
				}
				closed = true
				ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
				defer cancel()
				if err := host.Shutdown(ctx); err != nil {
					t.Errorf("Host.Shutdown: %v", err)
				}
			}
			t.Cleanup(closeHost)
			return host, closeHost
		}
		closeLifetime := func(closeHost func()) {
			closeHost()
		}

		// ---------- 生命周期 1：四 kind 计划 + 台词事件序列 ----------
		host, closeHost := openLifetime()
		issuer := integrationIdentity(0x71, "发令者")
		client := openCompanionChatClient(t, host, transport, issuer)
		stepUntilCompanionManagerReady(t, host, []network.ClientEndpoint{client}, id)

		planner := newFakeCompanionModel(t)
		planner.setPlanScript(stageAcceptancePlanJSON(
			core.BlockPos{X: 2, Y: 1, Z: 4},
			core.BlockPos{X: 8, Y: 1, Z: 4},
			core.BlockPos{X: 15, Y: 1, Z: 4},
		))
		host.world.companionManager.replacePlannerForTest(t, planner)
		dialogue := newFakeDialogueModel(t)
		host.world.companionManager.replaceDialogueForTest(t, dialogue)
		// mine 目标方块在计划派发前就位（计划解码对照快照校验目标可采掘）。
		setInteractionBlock(t, host, core.BlockPos{X: 8, Y: 1, Z: 4}, core.CoalOreID)

		sendIntegration(t, client, network.ChatCommand{Text: "@阿木 挖矿再回填"})
		waitForIncomingChatDepth(t, host.world, 1)
		// 三步计划的台词节点全集 = 开始 + 2 个进展（SelectProgressSteps(3)
		// 全选 [0,1,2]，末步完成迁移产出 Completed、其表达由终态台词承载）+ 终态。
		events := collectDialogueEvents(t, host, client, 2000, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskCompleted) == 1 &&
				countKind(events, network.ChatEventCompanionSpeech) == 4
		})
		if countKind(events, network.ChatEventTaskCompleted) != 1 {
			t.Fatalf("[%s] 混合计划任务未完成：事件=%v", transport, chatEventKinds(events))
		}

		// 事实序列：Accepted → TaskStarted → 前两步各一次 TaskProgress → Completed。
		firstTaskEvents := interactionEventsOf(events, "挖矿再回填")
		wantFirstFacts := []network.ChatEventKind{
			network.ChatEventAccepted, network.ChatEventTaskStarted,
			network.ChatEventTaskProgress, network.ChatEventTaskProgress,
			network.ChatEventTaskCompleted,
		}
		if got := chatEventKinds(firstTaskEvents); len(got) != len(wantFirstFacts) {
			t.Fatalf("[%s] 事实事件序列=%v，想要 %v", transport, got, wantFirstFacts)
		} else {
			for index, kind := range got {
				if kind != wantFirstFacts[index] {
					t.Fatalf("[%s] 事实事件序列=%v，想要 %v", transport, got, wantFirstFacts)
				}
			}
		}
		// 事实与台词共用全服 EventID 计数器：整段生命周期内严格递增。
		assertStrictlyIncreasingEventIDs(t, events)

		// 台词序列：开始一句、go_to 与 mine 完成各一句、终态一句；事件字段
		// 携带完整伙伴身份与发令者身份（M5D「阿木：台词」呈现的广播事实）。
		firstSpeeches := eventsWithKind(events, network.ChatEventCompanionSpeech)
		if len(firstSpeeches) != 4 {
			t.Fatalf("[%s] 台词事件数=%d，想要 4（事件=%v）",
				transport, len(firstSpeeches), chatEventKinds(events))
		}
		firstSpeechTexts := make([]string, 0, 4)
		for index, event := range firstSpeeches {
			if err := event.Validate(); err != nil {
				t.Fatalf("[%s] 台词事件 %d Validate: %v", transport, index, err)
			}
			if event.CompanionID != id || event.CompanionName != "阿木" ||
				event.RejectReason != network.ChatRejectNone || event.Command != "" ||
				event.PlayerID != issuer.PlayerID || event.PlayerName != "发令者" {
				t.Fatalf("[%s] 台词事件字段=%+v，想要伙伴+发令者身份+reason None",
					transport, event)
			}
			firstSpeechTexts = append(firstSpeechTexts, event.Speech)
		}
		wantLine := "我出发了"
		for index := 0; index < 3; index++ {
			if firstSpeechTexts[index] != wantLine {
				t.Fatalf("[%s] 非终态台词 %d=%q，想要假模型固定台词 %q",
					transport, index, firstSpeechTexts[index], wantLine)
			}
		}
		if firstSpeechTexts[3] != "完成了" {
			t.Fatalf("[%s] 终态台词=%q，想要假模型终态台词", transport, firstSpeechTexts[3])
		}

		// 台词请求输入：节点序列 start→progress(go_to)→progress(mine)→terminal，
		// 每次请求都携带生效人设；首段任务尚无近期记忆，摘要为空。请求数严格
		// 不超过每任务八次预算。
		waitDialogueRequests(t, dialogue, 4)
		firstRecords := dialogue.snapshotDialogueRequests()
		if len(firstRecords) > companion.MaxDialogueRequestsPerTask {
			t.Fatalf("[%s] 台词请求数=%d 超过预算 %d",
				transport, len(firstRecords), companion.MaxDialogueRequestsPerTask)
		}
		wantNodes := []dialogueRequestRecord{
			{NodeKind: "start"},
			{NodeKind: "progress", StepKind: "go_to"},
			{NodeKind: "progress", StepKind: "mine"},
			{NodeKind: "terminal"},
		}
		if len(firstRecords) != len(wantNodes) {
			t.Fatalf("[%s] 台词请求节点序列=%+v，想要 %+v",
				transport, firstRecords, wantNodes)
		}
		for index, record := range firstRecords {
			if record.NodeKind != wantNodes[index].NodeKind ||
				record.StepKind != wantNodes[index].StepKind {
				t.Fatalf("[%s] 台词请求 %d=%+v，想要 %+v（序列=%+v）",
					transport, index, record, wantNodes[index], firstRecords)
			}
			if record.Persona != stageAcceptancePersona {
				t.Fatalf("[%s] 台词请求 %d 人设=%q，想要生效人设透传",
					transport, index, record.Persona)
			}
			if record.Summary != "" {
				t.Fatalf("[%s] 首段任务请求 %d 携带摘要 %q，想要空（尚无近期记忆）",
					transport, index, record.Summary)
			}
		}

		// 终态摘要写入 manager 状态后再关服（Flush 在 Shutdown 内发生）。
		deadline := time.Now().Add(waitDeadline)
		for time.Now().Before(deadline) &&
			companionDialogueSlotSummary(t, host, id) != stageAcceptanceSummary {
			stepDialogueTick(t, host, []network.ClientEndpoint{client})
		}
		if summary := companionDialogueSlotSummary(t, host, id); summary != stageAcceptanceSummary {
			t.Fatalf("[%s] 终态摘要未写入 manager：summary=%q", transport, summary)
		}
		closeLifetime(closeHost)

		// ---------- 磁盘断言：schema v4 + summary-only 载荷 ----------
		// companions.ai 头部 32 字节：magic[0:4] "MCAI" + envelope 版本 u32
		// [4:8] + schema u32 [8:12]（companion_codec.go encodeCompanions）。
		// 直接读原始字节断言 schema=v4：解码 API 只回显数据，无法证明「落盘
		// 字节确实是当前 schema 而非旧版迁移读入」。
		raw, err := os.ReadFile(filepath.Join(root, "companions.ai"))
		if err != nil {
			t.Fatalf("[%s] 读取 companions.ai: %v", transport, err)
		}
		if len(raw) < 32 || string(raw[:4]) != "MCAI" {
			t.Fatalf("[%s] companions.ai 头部=%v，想要 MCAI envelope", transport, raw[:4])
		}
		if schema := binary.LittleEndian.Uint32(raw[8:12]); schema != 4 {
			t.Fatalf("[%s] companions.ai schema=%d，想要 v4", transport, schema)
		}
		reopened, err := storage.OpenDisk(context.Background(), root, storage.OpenOptions{})
		if err != nil {
			t.Fatalf("[%s] 重开存档: %v", transport, err)
		}
		loaded, err := reopened.LoadCompanions(context.Background())
		if err != nil {
			t.Fatalf("[%s] LoadCompanions: %v", transport, err)
		}
		if len(loaded.Queues) != 1 || loaded.Queues[0].ID != id ||
			loaded.Queues[0].Summary != stageAcceptanceSummary ||
			loaded.Queues[0].HasCurrent || len(loaded.Queues[0].Pending) != 0 {
			t.Fatalf("[%s] 落盘队列=%+v，想要 summary-only 载荷保留摘要", transport, loaded.Queues)
		}

		// ---------- 生命周期 2：同一磁盘存档重启恢复 + 摘要复用 ----------
		config2 := hostTestConfig()
		config2.Companions = append([]companion.Definition(nil), definitions...)
		config2.MaxPlayers = 2
		config2.OutboxCapacity = 4096
		config2.HeartbeatInterval = time.Hour
		config2.HeartbeatTimeout = time.Hour
		host2 := mustNewHost(t, config2, flatTestGenerator{}, reopened)
		// 第二段生命周期同样注册幂等 cleanup 兜底（理由同 openLifetime）。
		closed2 := false
		closeHost2 := func() {
			if closed2 {
				return
			}
			closed2 = true
			ctx, cancel := context.WithTimeout(context.Background(), longWaitDeadline)
			defer cancel()
			if err := host2.Shutdown(ctx); err != nil {
				t.Errorf("第二段 Host.Shutdown: %v", err)
			}
		}
		t.Cleanup(closeHost2)
		client2 := openCompanionChatClient(t, host2, transport, issuer)
		body2 := stepUntilCompanionManagerReady(t, host2, []network.ClientEndpoint{client2}, id)
		if summary := companionDialogueSlotSummary(t, host2, id); summary != stageAcceptanceSummary {
			t.Fatalf("[%s] 重启后摘要未恢复：summary=%q", transport, summary)
		}

		// 新任务：两步 go_to（目标取重启后身体当前位置，沿 +Z 拉开以避开
		// 生命周期 1 放置的泥土方块），台词节点 = start + progress + terminal。
		baseX := int32(body2.Position[0])
		baseY := int32(body2.Position[1])
		baseZ := int32(body2.Position[2])
		planner2 := newFakeCompanionModel(t,
			[3]int32{baseX, baseY, baseZ + 2},
			[3]int32{baseX, baseY, baseZ + 4},
		)
		host2.world.companionManager.replacePlannerForTest(t, planner2)
		dialogue2 := newFakeDialogueModel(t)
		host2.world.companionManager.replaceDialogueForTest(t, dialogue2)

		sendIntegration(t, client2, network.ChatCommand{Text: "@阿木 再走两步"})
		waitForIncomingChatDepth(t, host2.world, 1)
		events2 := collectDialogueEvents(t, host2, client2, 800, func(events []network.ChatEvent) bool {
			return countKind(events, network.ChatEventTaskCompleted) == 1 &&
				countKind(events, network.ChatEventCompanionSpeech) == 3
		})
		if countKind(events2, network.ChatEventTaskCompleted) != 1 {
			t.Fatalf("[%s] 重启后任务未完成：事件=%v", transport, chatEventKinds(events2))
		}
		secondTaskEvents := interactionEventsOf(events2, "再走两步")
		wantSecondFacts := []network.ChatEventKind{
			network.ChatEventAccepted, network.ChatEventTaskStarted,
			network.ChatEventTaskProgress, network.ChatEventTaskCompleted,
		}
		if got := chatEventKinds(secondTaskEvents); len(got) != len(wantSecondFacts) {
			t.Fatalf("[%s] 第二段事实序列=%v，想要 %v", transport, got, wantSecondFacts)
		} else {
			for index, kind := range got {
				if kind != wantSecondFacts[index] {
					t.Fatalf("[%s] 第二段事实序列=%v，想要 %v", transport, got, wantSecondFacts)
				}
			}
		}
		assertStrictlyIncreasingEventIDs(t, events2)
		secondSpeeches := eventsWithKind(events2, network.ChatEventCompanionSpeech)
		if len(secondSpeeches) != 3 {
			t.Fatalf("[%s] 第二段台词事件数=%d，想要 3（事件=%v）",
				transport, len(secondSpeeches), chatEventKinds(events2))
		}
		secondSpeechTexts := make([]string, 0, 3)
		for _, event := range secondSpeeches {
			secondSpeechTexts = append(secondSpeechTexts, event.Speech)
		}

		// 摘要复用：重启后的每一次台词请求都以恢复摘要为近期记忆输入，
		// 生效人设继续透传（M5D「重启保留最近对话摘要」的用户可观察结果）。
		waitDialogueRequests(t, dialogue2, 3)
		secondRecords := dialogue2.snapshotDialogueRequests()
		if len(secondRecords) != 3 {
			t.Fatalf("[%s] 第二段台词请求=%+v，想要 start+progress+terminal",
				transport, secondRecords)
		}
		wantSecondNodes := []string{"start", "progress", "terminal"}
		for index, record := range secondRecords {
			if record.NodeKind != wantSecondNodes[index] {
				t.Fatalf("[%s] 第二段请求 %d 节点=%q，想要 %q（序列=%+v）",
					transport, index, record.NodeKind, wantSecondNodes[index], secondRecords)
			}
			if record.Summary != stageAcceptanceSummary {
				t.Fatalf("[%s] 第二段请求 %d 摘要=%q，想要恢复摘要作为输入",
					transport, index, record.Summary)
			}
			if record.Persona != stageAcceptancePersona {
				t.Fatalf("[%s] 第二段请求 %d 人设=%q，想要生效人设继续透传",
					transport, index, record.Persona)
			}
		}
		closeLifetime(closeHost2)

		return stageAcceptanceResult{
			speechTexts:     append(firstSpeechTexts, secondSpeechTexts...),
			firstFactKinds:  chatEventKinds(firstTaskEvents),
			secondFactKinds: chatEventKinds(secondTaskEvents),
		}
	}

	memory := runTransport("memory")
	tcp := runTransport("tcp")

	// Memory/TCP parity：两传输的台词序列（两段生命周期拼接）与事实事件
	// 类别序列逐条一致（EventID 绝对值不跨传输断言，见 parity 投影的既有纪律）。
	if len(memory.speechTexts) == 0 {
		t.Fatal("Memory 传输没有任何台词事件")
	}
	if len(memory.speechTexts) != len(tcp.speechTexts) {
		t.Fatalf("台词序列不一致 memory=%v tcp=%v", memory.speechTexts, tcp.speechTexts)
	}
	for index := range memory.speechTexts {
		if memory.speechTexts[index] != tcp.speechTexts[index] {
			t.Fatalf("台词 %d 不一致 memory=%q tcp=%q（序列=%v/%v）",
				index, memory.speechTexts[index], tcp.speechTexts[index],
				memory.speechTexts, tcp.speechTexts)
		}
	}
	for index, kind := range memory.firstFactKinds {
		if kind != tcp.firstFactKinds[index] {
			t.Fatalf("第一段事实序列 %d 不一致 memory=%v tcp=%v",
				index, memory.firstFactKinds, tcp.firstFactKinds)
		}
	}
	for index, kind := range memory.secondFactKinds {
		if kind != tcp.secondFactKinds[index] {
			t.Fatalf("第二段事实序列 %d 不一致 memory=%v tcp=%v",
				index, memory.secondFactKinds, tcp.secondFactKinds)
		}
	}
}

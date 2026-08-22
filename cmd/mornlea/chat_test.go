//go:build darwin

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
)

func TestChatInputAcceptsChineseAndBackspaceRemovesOneRune(t *testing.T) {
	var input chatInput
	input.Open()
	for _, char := range "@阿木 挖石头" {
		input.Append(char)
	}
	input.Backspace()
	command, ok := input.Submit()
	if !ok || command.Text != "@阿木 挖石" {
		t.Fatalf("Submit=(%q,%v)", command.Text, ok)
	}
}

func TestChatInputCapsUTF8At1024Bytes(t *testing.T) {
	var input chatInput
	input.Open()
	text := strings.Repeat("界", 340) + "abcd"
	for _, char := range text {
		input.Append(char)
	}
	if input.bytes != 1024 || input.overflow {
		t.Fatalf("1024 bytes state=%+v", input)
	}
	input.Append('x')
	if !input.overflow {
		t.Fatal("1025th byte did not make input sticky-invalid")
	}
}

func TestChatPaste1024ASCIIIsAcceptedAnd1025IsNotPartiallySent(t *testing.T) {
	var accepted chatInput
	accepted.Open()
	for range 1024 {
		accepted.Append('a')
	}
	if command, ok := accepted.Submit(); !ok || len(command.Text) != 1024 {
		t.Fatalf("1024 ASCII Submit=(%d,%v)", len(command.Text), ok)
	}

	var rejected chatInput
	rejected.Open()
	for range 1025 {
		rejected.Append('b')
	}
	if command, ok := rejected.Submit(); ok || command.Text != "" {
		t.Fatalf("1025 ASCII partially sent (%d,%v)", len(command.Text), ok)
	}
}

func TestChatOverflowRemainsInvalidAfterBackspaceAndNeverSendsTruncatedPrefix(t *testing.T) {
	var input chatInput
	input.Open()
	for range 1025 {
		input.Append('a')
	}
	input.Backspace()
	if !input.overflow || input.count != 1023 {
		t.Fatalf("after Backspace state=%+v", input)
	}
	if command, ok := input.Submit(); ok || command.Text != "" {
		t.Fatalf("sticky overflow Submit=(%q,%v)", command.Text, ok)
	}
}

// TestChatInputBoundaryLocksToCompanionMaxPlanCommandBytes 锁定客户端聊天输入
// 上限与 companion.MaxPlanCommandBytes 同源（E7 同源化锁）：边界夹具由 companion
// 常量构造（340 个三字节 rune + 4 个 ASCII，覆盖多字节字节计数路径），恰好填满
// 上限时不得置 overflow 且 Submit 产出能通过 network 校验的 ChatCommand；再追加
// 一字节即进入 sticky overflow。companion 常量或客户端输入上限任何一侧漂移，
// 本测试都会变红。
func TestChatInputBoundaryLocksToCompanionMaxPlanCommandBytes(t *testing.T) {
	// (MaxPlanCommandBytes-4) 必须能被 3 整除（1024-4=1020），否则夹具断言先失败。
	text := strings.Repeat("界", (companion.MaxPlanCommandBytes-4)/3) + "abcd"
	if len(text) != companion.MaxPlanCommandBytes {
		t.Fatalf("边界夹具 = %d bytes，想要 %d", len(text), companion.MaxPlanCommandBytes)
	}

	var accepted chatInput
	accepted.Open()
	for _, char := range text {
		accepted.Append(char)
	}
	if accepted.bytes != companion.MaxPlanCommandBytes || accepted.overflow {
		t.Fatalf("边界输入状态=%+v", accepted)
	}
	command, ok := accepted.Submit()
	if !ok || len(command.Text) != companion.MaxPlanCommandBytes {
		t.Fatalf("边界 Submit=(%d,%v)", len(command.Text), ok)
	}
	if err := command.Validate(); err != nil {
		t.Fatalf("边界指令未通过 network 校验: %v", err)
	}

	var rejected chatInput
	rejected.Open()
	for _, char := range text {
		rejected.Append(char)
	}
	rejected.Append('x')
	if !rejected.overflow {
		t.Fatal("超出一字节未触发 sticky overflow")
	}
	if _, ok := rejected.Submit(); ok {
		t.Fatal("sticky overflow Submit 被放行")
	}
}

func TestChatSubmitTrimsOuterWhitespaceBeforeValidation(t *testing.T) {
	var input chatInput
	input.Open()
	for _, char := range "  @阿木 挖石头 　" {
		input.Append(char)
	}
	command, ok := input.Submit()
	if !ok || command.Text != "@阿木 挖石头" {
		t.Fatalf("trimmed Submit=(%q,%v)", command.Text, ok)
	}
}

func TestTextInputWhileChatClosedIsDrainedAndNeverLeaksIntoNextChat(t *testing.T) {
	app, _, window := newChatLoopApplication(t, []chatWindowFrame{
		{text: []rune("leak")},
		{keys: map[client.Key]bool{client.KeyEnter: true}},
		{},
	})
	if err := runInteractive(app); err != nil {
		t.Fatal(err)
	}
	if !app.chatInput.open || app.chatInput.text != "" || window.drainCalls != 3 {
		t.Fatalf("chat=%+v drainCalls=%d", app.chatInput, window.drainCalls)
	}
}

func TestChatOpenSuppressesMovementMiningPlacementInventoryAndHotbar(t *testing.T) {
	tests := []struct {
		name  string
		frame chatWindowFrame
		setup func(*testing.T, *application)
	}{
		{name: "movement-mining", frame: chatWindowFrame{
			keys: map[client.Key]bool{client.KeyW: true}, primary: true,
		}},
		{name: "placement", frame: chatWindowFrame{secondary: true}, setup: func(t *testing.T, app *application) {
			var inventory core.Inventory
			inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
			if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "inventory", frame: chatWindowFrame{keys: map[client.Key]bool{client.KeyE: true}}},
		{name: "hotbar", frame: chatWindowFrame{keys: map[client.Key]bool{client.Key1: true}}},
		{name: "drop", frame: chatWindowFrame{keys: map[client.Key]bool{client.KeyQ: true}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.frame.delay = 55 * time.Millisecond
			app, endpoint, _ := newChatLoopApplication(t, []chatWindowFrame{
				{keys: map[client.Key]bool{client.KeyEnter: true}},
				test.frame,
			})
			if err := app.predictor.Begin(network.PlayerState{
				ServerTick: 1, Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 10, 0.5},
				OnGround: true, Ready: true,
			}); err != nil {
				t.Fatal(err)
			}
			if test.setup != nil {
				test.setup(t, app)
			}
			if err := runInteractive(app); err != nil {
				t.Fatal(err)
			}
			if !app.chatInput.open || app.inventoryOpen {
				t.Fatalf("chatOpen=%v inventoryOpen=%v", app.chatInput.open, app.inventoryOpen)
			}
			messages := drainChatClientMessages(endpoint)
			if len(messages) == 0 {
				t.Fatal("chat-open loop sent no neutral PlayerInput")
			}
			for _, message := range messages {
				input, ok := message.(network.PlayerInput)
				if !ok || input.MoveX != 0 || input.MoveZ != 0 || input.Jump || input.Mining {
					t.Fatalf("chat-open message=%#v", message)
				}
			}
		})
	}
}

func TestChatEnterSendsAndEscapeCancels(t *testing.T) {
	t.Run("send", func(t *testing.T) {
		app, endpoint, window := newChatLoopApplication(t, []chatWindowFrame{
			{keys: map[client.Key]bool{client.KeyEnter: true}},
			{text: []rune("@阿木 挖石头")},
			{keys: map[client.Key]bool{client.KeyEnter: true}},
		})
		if err := runInteractive(app); err != nil {
			t.Fatal(err)
		}
		message := receiveChatClientMessage(t, endpoint)
		command, ok := message.(network.ChatCommand)
		if !ok || command.Text != "@阿木 挖石头" || app.chatInput.open || !window.captured {
			t.Fatalf("message=%#v chat=%+v captured=%v", message, app.chatInput, window.captured)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		app, endpoint, window := newChatLoopApplication(t, []chatWindowFrame{
			{keys: map[client.Key]bool{client.KeyEnter: true}},
			{text: []rune("@阿木 挖石头")},
			{keys: map[client.Key]bool{client.KeyEscape: true}},
		})
		if err := runInteractive(app); err != nil {
			t.Fatal(err)
		}
		if app.chatInput.open || app.chatInput.count != 0 || !window.captured {
			t.Fatalf("chat=%+v captured=%v", app.chatInput, window.captured)
		}
		if messages := drainChatClientMessages(endpoint); len(messages) != 0 {
			t.Fatalf("cancel sent messages=%#v", messages)
		}
	})
}

func TestChatEscapeWinsOverSimultaneousEnter(t *testing.T) {
	for _, test := range []struct {
		name  string
		panel bool
	}{
		{name: "gameplay"},
		{name: "visible-panel", panel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, endpoint, window := newChatLoopApplication(t, []chatWindowFrame{{
				keys: map[client.Key]bool{client.KeyEscape: true, client.KeyEnter: true},
			}})
			app.chatInput.Open()
			for _, char := range "@阿木 挖石头" {
				app.chatInput.Append(char)
			}
			if test.panel {
				app.panel = newPanelState(config.Defaults())
				app.panel.visible = true
				app.panel.selectFieldForTest(t, "render.fovDegrees")
				app.panel.effective.Render.FovDegrees = 42
			}
			if err := runInteractive(app); err != nil {
				t.Fatal(err)
			}
			if app.chatInput.open || app.chatInput.count != 0 || !window.captured {
				t.Fatalf("simultaneous Escape+Enter chat=%+v captured=%v", app.chatInput, window.captured)
			}
			if test.panel && app.panel.effective.Render.FovDegrees != 42 {
				t.Fatalf("simultaneous Escape+Enter leaked into panel: fov=%v", app.panel.effective.Render.FovDegrees)
			}
			if messages := drainChatClientMessages(endpoint); len(messages) != 0 {
				t.Fatalf("simultaneous Escape+Enter sent messages=%#v", messages)
			}
		})
	}
}

func TestChatCloseRecapturesCursorAndResetsMouseBaseline(t *testing.T) {
	for _, test := range []struct {
		name   string
		frames []chatWindowFrame
	}{
		{"escape", []chatWindowFrame{
			{keys: map[client.Key]bool{client.KeyEnter: true}, cursorX: 100, cursorY: 100},
			{keys: map[client.Key]bool{client.KeyEscape: true}, cursorX: 500, cursorY: 300},
			{cursorX: 500, cursorY: 300},
		}},
		{"enter", []chatWindowFrame{
			{keys: map[client.Key]bool{client.KeyEnter: true}, cursorX: 100, cursorY: 100},
			{text: []rune("@阿木 挖石头"), cursorX: 100, cursorY: 100},
			{keys: map[client.Key]bool{client.KeyEnter: true}, cursorX: 500, cursorY: 300},
			{cursorX: 500, cursorY: 300},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _, window := newChatLoopApplication(t, test.frames)
			app.camera.Yaw, app.camera.Pitch = 0.2, -0.1
			app.render.MouseSensitivity = 1
			if err := runInteractive(app); err != nil {
				t.Fatal(err)
			}
			if !window.captured || app.camera.Yaw != 0.2 || app.camera.Pitch != -0.1 {
				t.Fatalf("captured=%v camera=(%v,%v) history=%v", window.captured, app.camera.Yaw, app.camera.Pitch, window.captureHistory)
			}
		})
	}
}

func TestChatProtocolCloseRecapturesCursorAndResetsMouseBaseline(t *testing.T) {
	var serverEndpoint network.ServerEndpoint
	app, endpoint, window := newChatLoopApplication(t, []chatWindowFrame{
		{
			keys:    map[client.Key]bool{client.KeyEnter: true},
			cursorX: 100, cursorY: 100,
		},
		{
			cursorX: 500, cursorY: 300,
			onPoll: func() {
				sendInteractiveServerMessage(t, serverEndpoint, network.CompanionDespawn{
					ID: companion.ID{0: 0x12, 6: 0x40, 8: 0x80, 15: 3},
				})
			},
		},
	})
	serverEndpoint = endpoint
	app.camera.Yaw, app.camera.Pitch = 0.2, -0.1
	app.render.MouseSensitivity = 1

	if err := runInteractive(app); err != nil {
		t.Fatal(err)
	}
	if err := app.receiver.Err(); err != nil {
		t.Fatalf("receiver error=%v want=nil after mirror protocol close", err)
	}
	if !app.clientSessionClosed || app.chatInput.open || !window.captured {
		t.Fatalf("closed=%v chatOpen=%v captured=%v",
			app.clientSessionClosed, app.chatInput.open, window.captured)
	}
	if app.camera.Yaw != 0.2 || app.camera.Pitch != -0.1 {
		t.Fatalf("camera=(%v,%v) want=(0.2,-0.1)", app.camera.Yaw, app.camera.Pitch)
	}
}

func TestChatInputAndFormattedLinesResetOnDisconnect(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	window := &fakeInteractiveWindow{}
	app.window = window
	app.chatEvents = &client.ChatEvents{}
	if err := app.chatEvents.Apply(acceptedChatEvent(1)); err != nil {
		t.Fatal(err)
	}
	app.chatInput.Open()
	app.chatInput.Append('中')
	if overlay := app.chatOverlay(); len(overlay.Lines) != 1 || overlay.Input != "中" {
		t.Fatalf("warm overlay=%+v", overlay)
	}
	app.closeClientSession(nil)
	if overlay := app.chatOverlay(); overlay.Open || overlay.Input != "" || len(overlay.Lines) != 0 || !window.captured {
		t.Fatalf("closed overlay=%+v captured=%v", overlay, window.captured)
	}
}

func TestChatEnterDefersToOpenInventoryOrVisibleDebugPanel(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *application)
		check func(*testing.T, *application)
	}{
		{"inventory", func(_ *testing.T, app *application) { app.inventoryOpen = true }, nil},
		{"container", func(t *testing.T, app *application) {
			if err := app.chest.Apply(network.ChestState{Chest: core.ContainerRef{Kind: core.ContainerKindChest, Generation: 1}}); err != nil {
				t.Fatal(err)
			}
		}, nil},
		{"panel", func(t *testing.T, app *application) {
			app.panel = newPanelState(config.Defaults())
			app.panel.visible = true
			app.panel.selectFieldForTest(t, "render.fovDegrees")
			app.panel.effective.Render.FovDegrees = 42
		}, func(t *testing.T, app *application) {
			if got, want := app.panel.effective.Render.FovDegrees, config.Defaults().Render.FovDegrees; got != want {
				t.Fatalf("panel Enter reset fov=%v want=%v", got, want)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, _, _ := newChatLoopApplication(t, []chatWindowFrame{{keys: map[client.Key]bool{client.KeyEnter: true}}})
			test.setup(t, app)
			if err := runInteractive(app); err != nil {
				t.Fatal(err)
			}
			if app.chatInput.open {
				t.Fatal("Enter opened chat over higher-priority UI")
			}
			if test.check != nil {
				test.check(t, app)
			}
		})
	}
	app, _, _ := newChatLoopApplication(t, []chatWindowFrame{{keys: map[client.Key]bool{client.KeyEnter: true}}})
	if err := runInteractive(app); err != nil {
		t.Fatal(err)
	}
	if !app.chatInput.open {
		t.Fatal("Enter without a higher-priority UI did not open chat")
	}
}

func TestChatEventFormattingIsStableForAcceptedInvalidAndUnknown(t *testing.T) {
	accepted := acceptedChatEvent(1)
	invalid := rejectedChatEvent(2, network.ChatRejectInvalidFormat, "")
	unknown := rejectedChatEvent(3, network.ChatRejectUnknownCompanion, "阿树")
	got := []string{formatChatEvent(accepted), formatChatEvent(invalid), formatChatEvent(unknown)}
	want := []string{
		"Chen → 阿木：挖石头",
		"系统：格式应为 @伙伴名 指令",
		"系统：未找到伙伴 阿树",
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("format[%d]=%q want=%q", index, got[index], want[index])
		}
	}
	app := &application{chatEvents: &client.ChatEvents{}}
	for _, event := range []network.ChatEvent{accepted, invalid, unknown} {
		if err := app.chatEvents.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	app.chatOverlay()
	if allocations := testing.AllocsPerRun(1000, func() { app.chatOverlay() }); allocations != 0 {
		t.Fatalf("unchanged chat event formatting allocations=%v", allocations)
	}
}

// TestFormatChatEventUnknownKindFallsBackToNeutralLine 直接构造 wire 校验不可
// 达的未知 kind 与未知拒绝理由，锁定 `formatChatEvent` 的防御兜底「未知事件」
// （M5E 递延 3 的清偿）：kind switch 无 default 子句，未知 kind 落入二级
// reason switch 的 default；未来新增 kind/reason 漏加 case 时本测试守住
// 「宁可中性占位行也不静默复用其他行格式」的 E9/C2 契约。
func TestFormatChatEventUnknownKindFallsBackToNeutralLine(t *testing.T) {
	unknownKind := network.ChatEvent{
		Kind: network.ChatEventKind(200), CompanionName: "阿木", Command: "挖石头",
	}
	if got, want := formatChatEvent(unknownKind), "未知事件"; got != want {
		t.Fatalf("formatChatEvent(未知 kind) = %q, want %q", got, want)
	}
	unknownReason := network.ChatEvent{
		Kind: network.ChatEventRejected, RejectReason: network.ChatRejectReason(200),
		CompanionName: "阿木", Command: "挖石头",
	}
	if got, want := formatChatEvent(unknownReason), "未知事件"; got != want {
		t.Fatalf("formatChatEvent(未知拒绝理由) = %q, want %q", got, want)
	}
}

func TestApplicationRendersHealthBeforeInventoryConfirmation(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		// 氧气给满值：本用例只关心生命条的爱心实例数，未满氧气会额外画出氧气条。
		Position: mgl32.Vec3{0.5, 10, 0.5}, Ready: true, Health: 12,
		Oxygen: core.MaxOxygenTicks,
	}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	// 未确认背包时 HUD 只画生命值爱心(quad 流恰为爱心实例数)。
	if _, quads, _ := app.hotbarRenderer.FrameStreams(); len(quads)/48 != int(healthQuadInstancesForHUDTest) {
		t.Fatalf("unconfirmed inventory health quads=%d want=%d", len(quads)/48, healthQuadInstancesForHUDTest)
	}
}

func TestApplicationRendersChatBeforeInventoryConfirmation(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	app.chatEvents = &client.ChatEvents{}
	if err := app.chatEvents.Apply(acceptedChatEvent(1)); err != nil {
		t.Fatal(err)
	}
	app.chatInput.Open()
	for _, char := range "@阿木 挖石头" {
		app.chatInput.Append(char)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	// 背包未确认时聊天字形仍进入 HUD glyph 流。
	if _, _, hudGlyphs := app.hotbarRenderer.FrameStreams(); len(hudGlyphs) == 0 {
		t.Fatal("chat glyphs were not laid out before inventory confirmation")
	}
}

// TestChatEventsTaskLifecycleFactLinesAreStableChinese 锁定任务生命周期事件与
// QueueFull/NotFollowing 拒绝的稳定中文事实行，含 v18 的 TaskStopped 终态与
// TaskFailInventoryFull 容量失败原因。ChatEvent wire 上唯一的文本字段就是玩家
// 原始指令与身份名，不存在模型自由文本槽位；因此"模型文本不上屏"在本层的锁定
// 方式是：事实行必须逐字节等于只由伙伴名、固定中文模板与指令摘要组成的固定串。
func TestChatEventsTaskLifecycleFactLinesAreStableChinese(t *testing.T) {
	tests := []struct {
		name string
		id   uint64
		make func(uint64) network.ChatEvent
		want string
	}{
		{"task started", 2, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskStarted, network.ChatRejectNone)
		}, "阿木 开始执行：去东边"},
		{"task progress", 3, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskProgress, network.ChatRejectNone)
		}, "阿木 正在执行：去东边"},
		{"task completed", 4, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskCompleted, network.ChatRejectNone)
		}, "阿木 已完成：去东边"},
		{"task timed out", 5, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskTimedOut, network.ChatRejectNone)
		}, "阿木 任务超时：去东边"},
		{"task stopped", 6, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskStopped, network.ChatRejectNone)
		}, "阿木 已停止：去东边"},
		{"task failed planner unavailable", 7, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskFailed,
				network.ChatRejectReason(network.TaskFailPlannerUnavailable))
		}, "阿木 任务失败（规划器不可用）：去东边"},
		{"task failed invalid plan", 8, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskFailed,
				network.ChatRejectReason(network.TaskFailInvalidPlan))
		}, "阿木 任务失败（计划无效）：去东边"},
		{"task failed path unreachable", 9, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskFailed,
				network.ChatRejectReason(network.TaskFailPathUnreachable))
		}, "阿木 任务失败（路径不可达）：去东边"},
		{"task failed world changed", 10, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskFailed,
				network.ChatRejectReason(network.TaskFailWorldChanged))
		}, "阿木 任务失败（世界已变化）：去东边"},
		{"task failed inventory full", 11, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventTaskFailed,
				network.ChatRejectReason(network.TaskFailInventoryFull))
		}, "阿木 任务失败（背包已满）：去东边"},
		{"queue full rejection", 12, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventRejected, network.ChatRejectQueueFull)
		}, "系统：阿木 任务队列已满：去东边"},
		{"not following rejection", 13, func(id uint64) network.ChatEvent {
			return taskChatEvent(id, network.ChatEventRejected, network.ChatRejectNotFollowing)
		}, "系统：阿木 没有可停止的持续任务：去东边"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatChatEvent(test.make(test.id)); got != test.want {
				t.Fatalf("formatChatEvent = %q, want %q", got, test.want)
			}
		})
	}
}

// TestChatEventsTaskLifecycleHUDLinesRespectBoundsAndExcludeModelText 锁定任务事件
// 进入 HUD 的端到端呈现：事件环滚动后 HUD 只显示最近 6 行、每行经 32 rune 截断，
// 任务事件行与寻址行同为稳定中文事实行，且新事件未到达时重复刷新零分配。
func TestChatEventsTaskLifecycleHUDLinesRespectBoundsAndExcludeModelText(t *testing.T) {
	app := &application{chatEvents: &client.ChatEvents{}}
	events := []network.ChatEvent{
		acceptedChatEvent(1),
		taskChatEvent(2, network.ChatEventTaskStarted, network.ChatRejectNone),
		taskChatEvent(3, network.ChatEventTaskProgress, network.ChatRejectNone),
		taskChatEvent(4, network.ChatEventTaskCompleted, network.ChatRejectNone),
		taskChatEvent(5, network.ChatEventTaskFailed,
			network.ChatRejectReason(network.TaskFailPlannerUnavailable)),
		taskChatEvent(6, network.ChatEventTaskTimedOut, network.ChatRejectNone),
		taskChatEvent(7, network.ChatEventTaskStopped, network.ChatRejectNone),
		taskChatEvent(8, network.ChatEventRejected, network.ChatRejectQueueFull),
		taskChatEvent(9, network.ChatEventRejected, network.ChatRejectNotFollowing),
	}
	for _, event := range events {
		if err := app.chatEvents.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	overlay := app.chatOverlay()
	// 环内 9 条，HUD 只保留最近 6 行；最早三条被挤出显示但不离开事件环。
	// TaskStopped 行必须是稳定事实行（v18 起替换 C1 期间的 Accepted 兜底格式）。
	wantLines := []string{
		"阿木 已完成：去东边",
		"阿木 任务失败（规划器不可用）：去东边",
		"阿木 任务超时：去东边",
		"阿木 已停止：去东边",
		"系统：阿木 任务队列已满：去东边",
		"系统：阿木 没有可停止的持续任务：去东边",
	}
	if len(overlay.Lines) != len(wantLines) {
		t.Fatalf("HUD 行数 = %d，want %d（lines=%q）", len(overlay.Lines), len(wantLines), overlay.Lines)
	}
	for index, want := range wantLines {
		if got := overlay.Lines[index]; got != want {
			t.Fatalf("HUD 行 %d = %q，want %q", index, got, want)
		}
		if line := []rune(overlay.Lines[index]); len(line) > 32 {
			t.Fatalf("HUD 行 %d 超过 32 rune：%d", index, len(line))
		}
	}
	if allocations := testing.AllocsPerRun(1000, func() { app.chatOverlay() }); allocations != 0 {
		t.Fatalf("无新事件时的 HUD 刷新 allocations=%v want=0", allocations)
	}

	// 长指令摘要经既有 32 rune 截断：完整行 8 rune 模板 + 40 rune 指令 = 48
	// rune，截断输出恰好 32 rune——第 32 rune 是省略号、前 31 rune 与原文逐
	// rune 一致（F-5 收紧：精确到前缀逐项相等，取代只排除长原文子串的松断言）。
	longCommand := strings.Repeat("挖", 40)
	long := taskChatEvent(10, network.ChatEventTaskStarted, network.ChatRejectNone)
	long.Command = longCommand
	if err := app.chatEvents.Apply(long); err != nil {
		t.Fatal(err)
	}
	overlay = app.chatOverlay()
	gotLine := overlay.Lines[len(overlay.Lines)-1]
	runes := []rune(gotLine)
	if len(runes) != 32 || runes[31] != '…' {
		t.Fatalf("长指令行 = %q（%d rune），want 32 rune 且以 … 结尾", gotLine, len(runes))
	}
	fullLine := []rune("阿木 开始执行：" + longCommand)
	if string(runes[:31]) != string(fullLine[:31]) {
		t.Fatalf("长指令行前 31 rune = %q，want 与原文前 31 rune %q 一致",
			string(runes[:31]), string(fullLine[:31]))
	}
	if strings.Contains(gotLine, strings.Repeat("挖", 26)) {
		t.Fatalf("长指令行未按 32 rune 截断：%q", gotLine)
	}
}

// TestChatEventCompanionSpeechRendersAsPrefixedLine 锁定 v19 台词事件的核心呈现事实：
// CompanionSpeech 是 ChatEvent 中唯一携带模型生成文本的 kind，客户端对它的唯一处理是
// 「伙伴名：台词原文」一行——不改写、清洗或加引号；短台词必须逐 rune 原样进入 HUD。
func TestChatEventCompanionSpeechRendersAsPrefixedLine(t *testing.T) {
	speech := speechChatEvent(1, "我们先把工具修好。")
	if got, want := formatChatEvent(speech), "阿木：我们先把工具修好。"; got != want {
		t.Fatalf("formatChatEvent = %q, want %q", got, want)
	}
	app := &application{chatEvents: &client.ChatEvents{}}
	if err := app.chatEvents.Apply(speech); err != nil {
		t.Fatal(err)
	}
	overlay := app.chatOverlay()
	if len(overlay.Lines) != 1 || overlay.Lines[0] != "阿木：我们先把工具修好。" {
		t.Fatalf("overlay lines=%q", overlay.Lines)
	}
}

// TestChatEventCompanionSpeechLineTruncatedAt32RunesWithPrefix 锁定台词行宽度纪律：
// 伙伴名前缀与台词共用既有事实行的 32 rune 行上限（前缀计入），超长时第 32 个 rune
// 是既有截断省略号，而不是为台词行另设上限或整行丢弃。F-5 收紧：超长行断言
// 精确到「恰好 32 rune、第 32 rune 为 …、前 31 rune 与原文逐 rune 一致」；
// 29 rune 台词 + 3 rune 前缀恰好 32 rune，是不触发截断的上界邻近值，必须整行
// 逐 rune 原样输出（第 32 rune 是原文台词而非省略号）。
func TestChatEventCompanionSpeechLineTruncatedAt32RunesWithPrefix(t *testing.T) {
	app := &application{chatEvents: &client.ChatEvents{}}
	if err := app.chatEvents.Apply(speechChatEvent(1, strings.Repeat("话", 40))); err != nil {
		t.Fatal(err)
	}
	overlay := app.chatOverlay()
	if len(overlay.Lines) != 1 {
		t.Fatalf("overlay lines=%q", overlay.Lines)
	}
	runes := []rune(overlay.Lines[0])
	if len(runes) != 32 || runes[31] != '…' {
		t.Fatalf("speech line = %q（%d rune），want 32 rune 且以 … 结尾", overlay.Lines[0], len(runes))
	}
	if !strings.HasPrefix(overlay.Lines[0], "阿木：") {
		t.Fatalf("speech line lost companion prefix: %q", overlay.Lines[0])
	}
	fullLine := []rune("阿木：" + strings.Repeat("话", 40))
	if string(runes[:31]) != string(fullLine[:31]) {
		t.Fatalf("超长台词行前 31 rune = %q，want 与原文前 31 rune %q 一致",
			string(runes[:31]), string(fullLine[:31]))
	}

	// 29 rune 台词是不触发截断的上界邻近值：3 rune 前缀 + 29 rune 台词恰好
	// 32 rune，整行原样输出，第 32 rune 仍是原文（而非省略号）。
	boundary := speechChatEvent(2, strings.Repeat("话", 29))
	if err := app.chatEvents.Apply(boundary); err != nil {
		t.Fatal(err)
	}
	overlay = app.chatOverlay()
	if len(overlay.Lines) != 2 {
		t.Fatalf("overlay lines=%q", overlay.Lines)
	}
	boundaryRunes := []rune(overlay.Lines[1])
	if want := "阿木：" + strings.Repeat("话", 29); string(boundaryRunes) != want {
		t.Fatalf("29 rune 台词行 = %q（%d rune），want 原样输出 %q（%d rune）",
			overlay.Lines[1], len(boundaryRunes), want, len([]rune(want)))
	}
}

// TestChatEventCompanionSpeechOccupiesOwnLineAmongTaskFacts 锁定混排纪律：台词行与
// 任务事实行在同一个 EventID 环内各占一行、顺序保持，不存在把台词并入事实行或
// 整行吞掉的路径。
func TestChatEventCompanionSpeechOccupiesOwnLineAmongTaskFacts(t *testing.T) {
	app := &application{chatEvents: &client.ChatEvents{}}
	events := []network.ChatEvent{
		acceptedChatEvent(1),
		taskChatEvent(2, network.ChatEventTaskStarted, network.ChatRejectNone),
		speechChatEvent(3, "收到，这就去。"),
		taskChatEvent(4, network.ChatEventTaskCompleted, network.ChatRejectNone),
	}
	for _, event := range events {
		if err := app.chatEvents.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	overlay := app.chatOverlay()
	want := []string{
		"Chen → 阿木：挖石头",
		"阿木 开始执行：去东边",
		"阿木：收到，这就去。",
		"阿木 已完成：去东边",
	}
	if len(overlay.Lines) != len(want) {
		t.Fatalf("HUD 行数 = %d，want %d（lines=%q）", len(overlay.Lines), len(want), overlay.Lines)
	}
	for index, line := range want {
		if got := overlay.Lines[index]; got != line {
			t.Fatalf("HUD 行 %d = %q, want %q", index, got, line)
		}
	}
}

// TestCompanionSpeechLinesResetOnDisconnect 确认断线清空路径对台词事件同样生效：
// 清空逻辑作用于整个 ChatEvent 环与格式化行缓存，没有按 kind 过滤的白名单。
// 本用例不打开聊天输入，因此不断言光标重捕获（那由 chatWasOpen 分支负责）。
func TestCompanionSpeechLinesResetOnDisconnect(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	app.window = &fakeInteractiveWindow{}
	app.chatEvents = &client.ChatEvents{}
	if err := app.chatEvents.Apply(speechChatEvent(1, "我们先把工具修好。")); err != nil {
		t.Fatal(err)
	}
	if overlay := app.chatOverlay(); len(overlay.Lines) != 1 || overlay.Lines[0] != "阿木：我们先把工具修好。" {
		t.Fatalf("warm overlay=%+v", overlay)
	}
	app.closeClientSession(nil)
	if overlay := app.chatOverlay(); overlay.Open || overlay.Input != "" || len(overlay.Lines) != 0 {
		t.Fatalf("closed overlay=%+v", overlay)
	}
}

func taskChatEvent(id uint64, kind network.ChatEventKind, reason network.ChatRejectReason) network.ChatEvent {
	return network.ChatEvent{
		EventID:       id,
		PlayerID:      core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1},
		PlayerName:    "Chen",
		CompanionID:   companion.ID{0: 0x12, 6: 0x40, 8: 0x80, 15: 2},
		CompanionName: "阿木",
		Kind:          kind,
		RejectReason:  reason,
		Command:       "去东边",
	}
}

type chatWindowFrame struct {
	keys               map[client.Key]bool
	text               []rune
	overflow           bool
	primary, secondary bool
	cursorX, cursorY   float64
	delay              time.Duration
	onPoll             func()
}

type scriptedChatWindow struct {
	fakeInteractiveWindow
	frames          []chatWindowFrame
	frame           int
	drained         bool
	drainCalls      int
	pendingText     []rune
	pendingOverflow bool
	captureHistory  []bool
}

func (window *scriptedChatWindow) ShouldClose() bool { return window.frame >= len(window.frames)-1 }
func (window *scriptedChatWindow) Poll() {
	window.frame++
	window.drained = false
	frame := window.frames[window.frame]
	window.pendingText = append(window.pendingText, frame.text...)
	window.pendingOverflow = window.pendingOverflow || frame.overflow
	time.Sleep(frame.delay)
	if frame.onPoll != nil {
		frame.onPoll()
	}
}
func (window *scriptedChatWindow) KeyDown(key client.Key) bool {
	return window.frame >= 0 && window.frames[window.frame].keys[key]
}
func (window *scriptedChatWindow) PrimaryButtonDown() bool {
	return window.frame >= 0 && window.frames[window.frame].primary
}
func (window *scriptedChatWindow) SecondaryButtonDown() bool {
	return window.frame >= 0 && window.frames[window.frame].secondary
}
func (window *scriptedChatWindow) CursorPos() (float64, float64) {
	if window.frame < 0 {
		return 0, 0
	}
	frame := window.frames[window.frame]
	return frame.cursorX, frame.cursorY
}
func (window *scriptedChatWindow) FramebufferSize() (int, int) { return 16, 16 }
func (window *scriptedChatWindow) DrainTextInput(dst []rune) ([]rune, bool) {
	window.drainCalls++
	if window.drained || window.frame < 0 {
		return dst, false
	}
	window.drained = true
	dst = append(dst, window.pendingText...)
	overflow := window.pendingOverflow
	window.pendingText = window.pendingText[:0]
	window.pendingOverflow = false
	return dst, overflow
}
func (window *scriptedChatWindow) SetCursorCaptured(captured bool) {
	window.fakeInteractiveWindow.SetCursorCaptured(captured)
	window.captureHistory = append(window.captureHistory, captured)
}

func newChatLoopApplication(
	t *testing.T,
	frames []chatWindowFrame,
) (*application, network.ServerEndpoint, *scriptedChatWindow) {
	t.Helper()
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	clientEndpoint, serverEndpoint := network.NewMemoryPair(64)
	app.clientEndpoint = clientEndpoint
	app.receiver = client.NewReceiver(clientEndpoint, 64)
	app.chatEvents = &client.ChatEvents{}
	window := &scriptedChatWindow{frames: frames, frame: -1}
	app.window = window
	t.Cleanup(func() { _ = serverEndpoint.Close() })
	return app, serverEndpoint, window
}

func receiveChatClientMessage(t *testing.T, endpoint network.ServerEndpoint) network.ClientMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := endpoint.Recv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func drainChatClientMessages(endpoint network.ServerEndpoint) []network.ClientMessage {
	var messages []network.ClientMessage
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		message, err := endpoint.Recv(ctx)
		cancel()
		if err != nil {
			return messages
		}
		messages = append(messages, message)
	}
}

func acceptedChatEvent(id uint64) network.ChatEvent {
	return network.ChatEvent{
		EventID:       id,
		PlayerID:      core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1},
		PlayerName:    "Chen",
		CompanionID:   companion.ID{0: 0x12, 6: 0x40, 8: 0x80, 15: 2},
		CompanionName: "阿木",
		Kind:          network.ChatEventAccepted,
		Command:       "挖石头",
	}
}

// speechChatEvent 构造一条合法的 v19 台词事件：携带完整玩家与伙伴身份，
// 文本槽位只写 Speech（与 Command 互斥），供客户端呈现测试复用。
func speechChatEvent(id uint64, speech string) network.ChatEvent {
	return network.ChatEvent{
		EventID:       id,
		PlayerID:      core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1},
		PlayerName:    "Chen",
		CompanionID:   companion.ID{0: 0x12, 6: 0x40, 8: 0x80, 15: 2},
		CompanionName: "阿木",
		Kind:          network.ChatEventCompanionSpeech,
		Speech:        speech,
	}
}

func rejectedChatEvent(id uint64, reason network.ChatRejectReason, name string) network.ChatEvent {
	return network.ChatEvent{
		EventID:       id,
		PlayerID:      core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: 1},
		PlayerName:    "Chen",
		CompanionName: name,
		Kind:          network.ChatEventRejected,
		RejectReason:  reason,
	}
}

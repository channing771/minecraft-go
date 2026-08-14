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

func TestApplicationRendersHealthBeforeInventoryConfirmation(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, Ready: true, Health: 12,
	}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	passes := dev.lastPasses()
	if len(passes) != 2 || passes[0] != "terrain pass" || passes[1] != "hotbar pass" {
		t.Fatalf("passes=%v", passes)
	}
	if got := dev.lastDrawInstanceCount(); got != healthQuadInstancesForHUDTest {
		t.Fatalf("unconfirmed inventory health quads=%d want=%d", got, healthQuadInstancesForHUDTest)
	}
}

func TestApplicationRendersChatBeforeInventoryConfirmation(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
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
	passes := dev.lastPasses()
	if len(passes) != 2 || passes[0] != "terrain pass" || passes[1] != "hotbar pass" {
		t.Fatalf("passes=%v", passes)
	}
	if len(dev.bufferByLabel(t, "hotbar dynamic upload").lastWrite) <= 11776 {
		t.Fatal("chat glyphs were not uploaded before inventory confirmation")
	}
}

type chatWindowFrame struct {
	keys               map[client.Key]bool
	text               []rune
	overflow           bool
	primary, secondary bool
	cursorX, cursorY   float64
	delay              time.Duration
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
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
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

//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/physics"
	"github.com/channing771/mornlea/internal/render/hud"
)

func TestInteractiveInputUsesDrainedReadyResetInSameFrame(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	app.camera.Pos = mgl32.Vec3{99, 99, 99}
	state := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{4.5, 20, -2.5},
		Yaw:        0.75,
		Pitch:      -0.2,
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, state)

	app.drainServerMessages(1)
	app.applyInteractiveInput(physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, true)

	wantPosition := mgl32.Vec3{4.5, 20 + physics.DefaultTunables().EyeHeight, -2.5}
	if app.camera.Pos != wantPosition || app.camera.Yaw != 0.75 || app.camera.Pitch != -0.2 {
		t.Fatalf("Ready Reset 同帧相机=%+v yaw=%v pitch=%v，想要 pos=%+v yaw=0.75 pitch=-0.2",
			app.camera.Pos, app.camera.Yaw, app.camera.Pitch, wantPosition)
	}
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	input, ok := message.(network.PlayerInput)
	if !ok || input != (network.PlayerInput{Sequence: 1, Yaw: 0.75, Pitch: -0.2, Mining: true}) {
		t.Fatalf("Ready Reset 同帧动作=%#v", message)
	}
}

func TestInteractiveInputPresentsDrainedLargeCorrectionInSameFrame(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	begin := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, begin)
	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)

	corrected := begin
	corrected.ServerTick = 2
	corrected.Position = mgl32.Vec3{8.5, 30, -4.5}
	corrected.Reset = false
	sendInteractiveServerMessage(t, serverEndpoint, corrected)

	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)

	want := mgl32.Vec3{8.5, 30 + physics.DefaultTunables().EyeHeight, -4.5}
	if app.camera.Pos != want {
		t.Fatalf("大纠正同帧相机=%+v，想要 %+v", app.camera.Pos, want)
	}
}

func TestInteractiveInputUsesDrainedNotReadyForActionAndInputGate(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	ready := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, ready)
	app.drainServerMessages(1)
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)

	notReady := ready
	notReady.ServerTick = 2
	notReady.Ready = false
	notReady.Reset = false
	sendInteractiveServerMessage(t, serverEndpoint, notReady)

	app.drainServerMessages(1)
	app.applyInteractiveInput(
		physics.FixedDelta,
		client.Movement{MoveZ: 1},
		client.Actions{Mining: true},
		true,
	)

	if _, ready := app.predictor.State(); ready {
		t.Fatal("drain Ready=false 后 predictor 仍 Ready")
	}
	if app.sequence != 0 {
		t.Fatalf("Ready=false 同帧分配 sequence=%d", app.sequence)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

// 杀死变异：从本地按键推进采掘条或忽略 inactive 权威状态都会改变镜像。
func TestApplicationMiningOverlayUsesOnlyConfirmedPlayerState(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	state := network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
		MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 10, Z: 2},
		MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	want := hud.MiningOverlay{
		Active: true, ProgressTicks: 6, RequiredTicks: 15, Harvestable: true,
	}
	if app.miningOverlay != want {
		t.Fatalf("权威采掘镜像=%+v，想要 %+v", app.miningOverlay, want)
	}

	for range 2 {
		app.applyInteractiveInput(
			physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, true,
		)
		if _, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput); !ok {
			t.Fatal("本地按住没有发送持续输入")
		}
		if app.miningOverlay != want {
			t.Fatalf("无新 PlayerState 时本地输入改写采掘镜像: %+v", app.miningOverlay)
		}
	}

	inactive := state
	inactive.ServerTick = 2
	inactive.MiningActive = false
	inactive.MiningTarget = core.BlockPos{}
	inactive.MiningProgressTicks = 0
	inactive.MiningRequiredTicks = 0
	inactive.MiningHarvestable = false
	sendInteractiveServerMessage(t, serverEndpoint, inactive)
	app.drainServerMessages(1)
	if app.miningOverlay != (hud.MiningOverlay{}) {
		t.Fatalf("inactive 后采掘镜像=%+v，想要零值", app.miningOverlay)
	}
}

// 杀死变异：旧或重复 PlayerState 不得回滚 app 的已确认 tick、采掘条或 reset 生命周期。
func TestApplicationMiningOverlayIgnoresStaleAndEqualPlayerState(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	active := network.PlayerState{
		ServerTick: 2, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
		MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 10, Z: 2},
		MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, active)
	app.drainServerMessages(1)
	want := hud.MiningOverlay{
		Active: true, ProgressTicks: 6, RequiredTicks: 15, Harvestable: true,
	}
	app.inventoryOpen = true
	app.inventorySource = 8

	for _, tick := range []uint64{1, 2} {
		sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
			ServerTick: tick, Dimension: core.Overworld,
			Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true, Reset: true,
		})
		app.drainServerMessages(1)
		if app.serverTick != 2 || app.miningOverlay != want {
			t.Fatalf("tick=%d 后 app tick/overlay=%d/%+v，想要 2/%+v",
				tick, app.serverTick, app.miningOverlay, want)
		}
		if !app.inventoryOpen || app.inventorySource != 8 {
			t.Fatalf("tick=%d 的旧 reset 改写界面: open=%v source=%d",
				tick, app.inventoryOpen, app.inventorySource)
		}
	}

	newer := active
	newer.ServerTick = 3
	newer.MiningProgressTicks = 7
	sendInteractiveServerMessage(t, serverEndpoint, newer)
	app.drainServerMessages(1)
	if app.serverTick != 3 || app.miningOverlay.ProgressTicks != 7 {
		t.Fatalf("更新状态未生效: tick/overlay=%d/%+v", app.serverTick, app.miningOverlay)
	}
}

// 杀死变异：reset 或连接关闭遗漏清理会把上一会话进度留在下一帧。
func TestApplicationMiningOverlayClearsOnResetAndSessionClose(t *testing.T) {
	for _, test := range []struct {
		name  string
		clear func(*application, network.ServerEndpoint)
	}{
		{
			name: "Reset",
			clear: func(app *application, endpoint network.ServerEndpoint) {
				sendInteractiveServerMessage(t, endpoint, network.PlayerState{
					ServerTick: 2, Dimension: core.Overworld,
					Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true, Reset: true,
				})
				app.drainServerMessages(1)
			},
		},
		{name: "关闭会话", clear: func(app *application, _ network.ServerEndpoint) {
			app.closeClientSession(nil)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, serverEndpoint := newInteractiveTestApplication(t)
			sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
				ServerTick: 1, Dimension: core.Overworld,
				Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
				MiningActive: true, MiningTarget: core.BlockPos{X: 1, Y: 10, Z: 2},
				MiningProgressTicks: 6, MiningRequiredTicks: 15, MiningHarvestable: true,
			})
			app.drainServerMessages(1)
			if !app.miningOverlay.Active {
				t.Fatal("测试前置没有建立 active 权威采掘镜像")
			}

			test.clear(app, serverEndpoint)
			if app.miningOverlay != (hud.MiningOverlay{}) {
				t.Fatalf("清理后采掘镜像=%+v，想要零值", app.miningOverlay)
			}
		})
	}
}

func TestCursorReleaseSendsNeutralFixedStepAfterHeldInput(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
	}); err != nil {
		t.Fatal(err)
	}
	held := client.Movement{MoveX: 1, MoveZ: -1, Jump: true}
	app.applyInteractiveCursorInput(
		physics.FixedDelta, held, client.Actions{}, true, false,
	)
	first, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || first.Sequence != 1 || first.MoveX != held.MoveX ||
		first.MoveZ != held.MoveZ || first.Jump != held.Jump {
		t.Fatalf("captured held input=%+v", first)
	}

	app.applyInteractiveCursorInput(
		physics.FixedDelta, held, client.Actions{}, false, false,
	)
	neutral, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || neutral.Sequence != 2 || neutral.MoveX != 0 ||
		neutral.MoveZ != 0 || neutral.Jump {
		t.Fatalf("cursor release input=%+v，想要下一 fixed-step neutral", neutral)
	}
}

func TestHotbarSelectionOnlySendsRequestAndKeepsMirror(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 10, 0.5},
		OnGround: true, Ready: true, Reset: true,
	})
	var confirmed core.Hotbar
	confirmed.Selected = 1
	confirmed.Slots[1] = core.ItemStack{Item: core.ItemStone, Count: 3}
	sendInteractiveServerMessage(t, serverEndpoint, network.InventoryState{Inventory: core.Inventory{Hotbar: confirmed}})
	app.drainServerMessages(2)

	app.applyInteractiveInput(0, client.Movement{}, client.Actions{
		Select: true, SelectSlot: 7,
	}, true)

	message := receiveInteractiveClientMessage(t, serverEndpoint)
	if got, ok := message.(network.SelectHotbar); !ok ||
		got != (network.SelectHotbar{Sequence: 1, Slot: 7}) {
		t.Fatalf("选择请求=%#v，想要 Sequence 1 Slot 7", message)
	}
	if got, ok := app.inventory.Hotbar(); !ok || got != confirmed {
		t.Fatalf("未确认的选择改写了镜像: %+v, %v", got, ok)
	}
}

func TestHotbarPlaceUsesLastConfirmedSlot(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 10, 0.5},
		OnGround: true, Ready: true, Reset: true,
	})
	var confirmed core.Hotbar
	confirmed.Selected = 5
	confirmed.Slots[5] = core.ItemStack{Item: core.ItemDirt, Count: 2}
	sendInteractiveServerMessage(t, serverEndpoint, network.InventoryState{Inventory: core.Inventory{Hotbar: confirmed}})
	app.drainServerMessages(2)

	app.applyInteractiveInput(0, client.Movement{}, client.Actions{Place: true}, true)

	message := receiveInteractiveClientMessage(t, serverEndpoint)
	place, ok := message.(network.PlaceBlock)
	if !ok || place.Slot != 5 {
		t.Fatalf("放置=%#v，想要引用已确认的栏位 5", message)
	}
	if got, ok := app.inventory.Hotbar(); !ok || got != confirmed {
		t.Fatalf("放置后镜像被本地预测修改: %+v, %v", got, ok)
	}
}

func TestHotbarPlaceWaitsForFirstConfirmedState(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 10, 0.5},
		OnGround: true, Ready: true, Reset: true,
	})
	app.drainServerMessages(1)

	app.applyInteractiveInput(0, client.Movement{}, client.Actions{Place: true}, true)

	if app.sequence != 0 {
		t.Fatalf("尚未确认快捷栏就分配 sequence=%d", app.sequence)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestPlaceOpensLocalMirrorFurnaceWithoutPredictingUI(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 3.5}, OnGround: true, Ready: true,
	}); err != nil {
		t.Fatal(err)
	}
	app.camera = client.Camera{Pos: mgl32.Vec3{0.5, 10.5, 3.5}}
	loadInteractiveBlock(t, app, core.BlockPos{X: 0, Y: 10, Z: 0}, core.FurnaceID)

	app.placeBlock()
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	open, ok := message.(network.OpenContainer)
	if !ok || open != (network.OpenContainer{Sequence: 1}) {
		t.Fatalf("打开熔炉请求 = %#v，想要 sequence 1 与当前视角", message)
	}
	if app.inventoryOpen {
		t.Fatal("服务端确认前本地打开了熔炉界面")
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("打开请求本地改写了熔炉镜像")
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestPlaceKeepsBlockRequestForNonFurnaceHit(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 3.5}, OnGround: true, Ready: true,
	}); err != nil {
		t.Fatal(err)
	}
	app.camera = client.Camera{Pos: mgl32.Vec3{0.5, 10.5, 3.5}}
	loadInteractiveBlock(t, app, core.BlockPos{X: 0, Y: 10, Z: 0}, core.StoneID)
	var inventory core.Inventory
	inventory.Hotbar.Selected = 4
	inventory.Hotbar.Slots[4] = core.ItemStack{Item: core.ItemDirt, Count: 1}
	if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
		t.Fatal(err)
	}

	app.placeBlock()
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	place, ok := message.(network.PlaceBlock)
	if !ok || place != (network.PlaceBlock{Sequence: 1, Slot: 4}) {
		t.Fatalf("非熔炉右键请求 = %#v，想要放置已确认栏位 4", message)
	}
}

func loadInteractiveBlock(
	t *testing.T,
	app *application,
	position core.BlockPos,
	block core.BlockID,
) {
	t.Helper()
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		sections[index] = network.SectionData{
			Y: int32(index), Storage: network.SectionSingle, Single: core.AirID,
		}
	}
	chunk := position.Chunk()
	if _, err := app.mirror.Apply(network.ChunkSnapshot{
		Dimension: core.Overworld, Chunk: chunk, Revision: 1, Sections: sections,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.mirror.Apply(network.BlockChanges{
		Dimension: core.Overworld, Chunk: chunk, BaseRevision: 1, NewRevision: 2,
		Changes: []network.BlockChange{{Position: position, Block: block}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHotbarMirrorResetsWithClientSession(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	var confirmed core.Hotbar
	confirmed.Slots[0] = core.ItemStack{Item: core.ItemGrass, Count: 1}
	sendInteractiveServerMessage(t, serverEndpoint, network.InventoryState{Inventory: core.Inventory{Hotbar: confirmed}})
	app.drainServerMessages(1)
	if _, ok := app.inventory.Hotbar(); !ok {
		t.Fatal("权威快捷栏未进入镜像")
	}

	app.closeClientSession(nil)
	if hotbar, ok := app.inventory.Hotbar(); ok || hotbar != (core.Hotbar{}) {
		t.Fatalf("关闭会话后镜像=%+v, %v，想要空且未确认", hotbar, ok)
	}
}
func TestInventoryTwoClicksSendOneMoveRequest(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld, Position: mgl32.Vec3{0.5, 10, 0.5},
		OnGround: true, Ready: true, Reset: true,
	})
	app.drainServerMessages(1)
	app.setInventoryOpen(true)

	width, height := uint32(1280), uint32(720)
	sourceX, sourceY := inventorySlotCenter(t, 1, width, height)
	targetX, targetY := inventorySlotCenter(t, 30, width, height)

	app.clickInventorySlot(sourceX, sourceY, width, height)
	if app.inventorySource != 1 {
		t.Fatalf("首次点击来源 = %d，想要 1", app.inventorySource)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)

	app.clickInventorySlot(targetX, targetY, width, height)
	if app.inventorySource != -1 {
		t.Fatalf("第二次点击后来源未清除: %d", app.inventorySource)
	}
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	if got, ok := message.(network.MoveInventoryStack); !ok || got.From != 1 || got.To != 30 {
		t.Fatalf("移动请求 = %#v，想要 1 → 30", message)
	}
}

func TestInventoryClickOutsideSlotsDoesNothing(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	app.setInventoryOpen(true)
	app.clickInventorySlot(0, 0, 1280, 720)
	if app.inventorySource != -1 {
		t.Fatalf("界外点击记录了来源: %d", app.inventorySource)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestAuthoritativeFurnaceStateOpensUI(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	state := network.FurnaceState{
		Furnace:       core.FurnaceRef{Dimension: core.Overworld, Generation: 1},
		Input:         core.ItemStack{Item: core.ItemRawIron, Count: 2},
		Fuel:          core.ItemStack{Item: core.ItemCoal, Count: 3},
		ProgressTicks: 17, BurnTicks: 1599,
	}

	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	got, opened := app.furnace.State()
	if !opened || got != state || !app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("权威熔炉界面 state=%+v opened=%v ui=%v source=%d",
			got, opened, app.inventoryOpen, app.inventorySource)
	}
	app.inventorySource = 1
	state.ProgressTicks++
	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	if app.inventorySource != 1 {
		t.Fatalf("连续权威更新清除了已选来源: %d", app.inventorySource)
	}
}

func TestFurnaceTwoClicksSendOneMoveWithoutPrediction(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	var inventory core.Inventory
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemRawIron, Count: 2}
	if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	state := network.FurnaceState{
		Furnace: core.FurnaceRef{Dimension: core.Overworld, Slot: 2, Generation: 3},
		Fuel:    core.ItemStack{Item: core.ItemCoal, Count: 1},
	}
	if err := app.furnace.Apply(state); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true

	width, height := uint32(1280), uint32(720)
	sourceX, sourceY := furnaceSlotCenter(t, 1, width, height)
	targetX, targetY := furnaceSlotCenter(t, core.FurnaceInputSlot, width, height)
	app.clickInventorySlot(sourceX, sourceY, width, height)
	assertNoInteractiveClientMessage(t, serverEndpoint)
	app.clickInventorySlot(targetX, targetY, width, height)

	message := receiveInteractiveClientMessage(t, serverEndpoint)
	want := network.MoveContainerStack{
		Sequence: 1, Container: state.Furnace, From: 1, To: core.FurnaceInputSlot,
	}
	if got, ok := message.(network.MoveContainerStack); !ok || got != want {
		t.Fatalf("跨容器移动 = %#v，想要 %+v", message, want)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
	if got, _ := app.inventory.State(); got != inventory {
		t.Fatalf("移动请求改写了物品镜像: %+v", got)
	}
	if got, _ := app.furnace.State(); got != state {
		t.Fatalf("移动请求改写了熔炉镜像: %+v", got)
	}
}

func TestExplicitFurnaceCloseClearsUIAndSendsOnce(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	window := &fakeInteractiveWindow{}
	app.window = window
	state := network.FurnaceState{
		Furnace: core.FurnaceRef{Dimension: core.Overworld, Generation: 1},
	}
	if err := app.furnace.Apply(state); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true
	app.inventorySource = core.FurnaceFuelSlot

	app.setInventoryOpen(false)
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	if got, ok := message.(network.CloseContainer); !ok || got != (network.CloseContainer{Sequence: 1}) {
		t.Fatalf("关闭熔炉请求 = %#v", message)
	}
	app.setInventoryOpen(false)
	assertNoInteractiveClientMessage(t, serverEndpoint)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("关闭后 ui=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if !window.CursorCaptured() {
		t.Fatal("关闭熔炉后未恢复鼠标捕获")
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("显式关闭后仍保留熔炉镜像")
	}
}

func TestFurnaceClosedMessageClearsUIWithoutEcho(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	state := network.FurnaceState{
		Furnace: core.FurnaceRef{Dimension: core.Overworld, Slot: 1, Generation: 2},
	}
	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	app.inventorySource = core.FurnaceOutputSlot

	sendInteractiveServerMessage(t, serverEndpoint, network.ContainerClosed{Container: state.Furnace})
	app.drainServerMessages(1)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("服务端关闭后 ui=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("服务端关闭后仍保留熔炉镜像")
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestPlaceOpensLocalMirrorChestWithoutPredictingUI(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 3.5}, OnGround: true, Ready: true,
	}); err != nil {
		t.Fatal(err)
	}
	app.camera = client.Camera{Pos: mgl32.Vec3{0.5, 10.5, 3.5}}
	loadInteractiveBlock(t, app, core.BlockPos{X: 0, Y: 10, Z: 0}, core.ChestID)

	app.placeBlock()
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	open, ok := message.(network.OpenContainer)
	if !ok || open != (network.OpenContainer{Sequence: 1}) {
		t.Fatalf("打开箱子请求 = %#v，想要 sequence 1 与当前视角", message)
	}
	if app.inventoryOpen {
		t.Fatal("服务端确认前本地打开了箱子界面")
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("打开请求本地改写了箱子镜像")
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func chestTestState() network.ChestState {
	var state network.ChestState
	state.Chest = core.ContainerRef{Dimension: core.Overworld, Kind: core.ContainerKindChest, Generation: 1}
	return state
}

func TestAuthoritativeChestStateOpensUI(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	state := chestTestState()
	state.Items[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	state.Items[26] = core.ItemStack{Item: core.ItemCoal, Count: 1}

	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	got, opened := app.chest.State()
	if !opened || got != state || !app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("权威箱子界面 state=%+v opened=%v ui=%v source=%d",
			got, opened, app.inventoryOpen, app.inventorySource)
	}
	app.inventorySource = 1
	state.Items[0].Count++
	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	if app.inventorySource != 1 {
		t.Fatalf("连续权威更新清除了已选来源: %d", app.inventorySource)
	}
}

func TestChestTwoClicksSendOneMoveWithoutPrediction(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	var inventory core.Inventory
	inventory.Hotbar.Slots[1] = core.ItemStack{Item: core.ItemRawIron, Count: 2}
	if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	state := chestTestState()
	state.Chest.Slot, state.Chest.Generation = 2, 3
	if err := app.chest.Apply(state); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true

	width, height := uint32(1280), uint32(720)
	sourceX, sourceY := chestSlotCenter(t, 1, width, height)
	targetX, targetY := chestSlotCenter(t, core.ChestFirstSlot+5, width, height)
	app.clickInventorySlot(sourceX, sourceY, width, height)
	assertNoInteractiveClientMessage(t, serverEndpoint)
	app.clickInventorySlot(targetX, targetY, width, height)

	message := receiveInteractiveClientMessage(t, serverEndpoint)
	want := network.MoveContainerStack{
		Sequence: 1, Container: state.Chest, From: 1, To: core.ChestFirstSlot + 5,
	}
	if got, ok := message.(network.MoveContainerStack); !ok || got != want {
		t.Fatalf("跨容器移动 = %#v，想要 %+v", message, want)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
	if got, _ := app.inventory.State(); got != inventory {
		t.Fatalf("移动请求改写了物品镜像: %+v", got)
	}
	if got, _ := app.chest.State(); got != state {
		t.Fatalf("移动请求改写了箱子镜像: %+v", got)
	}
}

func TestExplicitChestCloseClearsUIAndSendsOnce(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	window := &fakeInteractiveWindow{}
	app.window = window
	state := chestTestState()
	if err := app.chest.Apply(state); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true
	app.inventorySource = core.ChestFirstSlot + 3

	app.setInventoryOpen(false)
	message := receiveInteractiveClientMessage(t, serverEndpoint)
	if got, ok := message.(network.CloseContainer); !ok || got != (network.CloseContainer{Sequence: 1}) {
		t.Fatalf("关闭箱子请求 = %#v", message)
	}
	app.setInventoryOpen(false)
	assertNoInteractiveClientMessage(t, serverEndpoint)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("关闭后 ui=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if !window.CursorCaptured() {
		t.Fatal("关闭箱子后未恢复鼠标捕获")
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("显式关闭后仍保留箱子镜像")
	}
}

func TestChestClosedMessageClearsUIWithoutEcho(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	state := chestTestState()
	state.Chest.Slot, state.Chest.Generation = 1, 2
	sendInteractiveServerMessage(t, serverEndpoint, state)
	app.drainServerMessages(1)
	app.inventorySource = core.ChestFirstSlot

	sendInteractiveServerMessage(t, serverEndpoint, network.ContainerClosed{Container: state.Chest})
	app.drainServerMessages(1)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("服务端关闭后 ui=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("服务端关闭后仍保留箱子镜像")
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

// 杀死变异：熔炉与箱子镜像必须互斥；否则点击分流会用错容器，
// 或者渲染会同时按两种叠加值布局。
func TestNewContainerStateClearsStaleMirrorOfOtherKind(t *testing.T) {
	t.Run("箱子状态到达时清除旧熔炉镜像", func(t *testing.T) {
		app, serverEndpoint := newInteractiveTestApplication(t)
		furnaceState := network.FurnaceState{
			Furnace: core.FurnaceRef{Dimension: core.Overworld, Generation: 1},
		}
		sendInteractiveServerMessage(t, serverEndpoint, furnaceState)
		app.drainServerMessages(1)
		if _, opened := app.furnace.State(); !opened {
			t.Fatal("熔炉状态未进入镜像")
		}

		chestState := chestTestState()
		chestState.Chest.Slot = 1
		sendInteractiveServerMessage(t, serverEndpoint, chestState)
		app.drainServerMessages(1)
		if _, opened := app.furnace.State(); opened {
			t.Fatal("新箱子状态到达后仍保留过期熔炉镜像")
		}
		if got, opened := app.chest.State(); !opened || got != chestState {
			t.Fatalf("箱子镜像 = %+v, opened=%v", got, opened)
		}
	})

	t.Run("熔炉状态到达时清除旧箱子镜像", func(t *testing.T) {
		app, serverEndpoint := newInteractiveTestApplication(t)
		chestState := chestTestState()
		sendInteractiveServerMessage(t, serverEndpoint, chestState)
		app.drainServerMessages(1)
		if _, opened := app.chest.State(); !opened {
			t.Fatal("箱子状态未进入镜像")
		}

		furnaceState := network.FurnaceState{
			Furnace: core.FurnaceRef{Dimension: core.Overworld, Slot: 1, Generation: 1},
		}
		sendInteractiveServerMessage(t, serverEndpoint, furnaceState)
		app.drainServerMessages(1)
		if _, opened := app.chest.State(); opened {
			t.Fatal("新熔炉状态到达后仍保留过期箱子镜像")
		}
		if got, opened := app.furnace.State(); !opened || got != furnaceState {
			t.Fatalf("熔炉镜像 = %+v, opened=%v", got, opened)
		}
	})
}

func TestPlayerResetClosesChestUI(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.chest.Apply(chestTestState()); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true
	app.inventorySource = core.ChestFirstSlot
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true, Reset: true,
	})

	app.drainServerMessages(1)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("reset 后 ui=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("reset 后仍保留箱子镜像")
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestClientSessionCloseClearsChestMirror(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	if err := app.chest.Apply(chestTestState()); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true
	app.inventorySource = core.ChestFirstSlot

	app.closeClientSession(nil)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("断线后 open=%v source=%d，想要界面关闭且来源清除", app.inventoryOpen, app.inventorySource)
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("断线后仍保留箱子镜像")
	}
}

func chestSlotCenter(t *testing.T, slot int, width, height uint32) (float64, float64) {
	t.Helper()
	for x := range int(width) {
		for y := range int(height) {
			got, ok := hud.ChestSlotAt(float64(x), float64(y), width, height)
			if ok && int(got) == slot {
				return float64(x), float64(y)
			}
		}
	}
	t.Fatalf("找不到箱子统一栏位 %d 的像素", slot)
	return 0, 0
}

// 杀死变异：跳过已确认背包检查、发送错误配方、预测结果或重复发送都会失败。
func TestCraftRecipeClickUsesConfirmedInventory(t *testing.T) {
	for _, test := range []struct {
		name   string
		recipe core.RecipeID
		input  core.ItemStack
	}{
		{"石砖", core.RecipeStoneBricks, core.ItemStack{Item: core.ItemStone, Count: 4}},
		{"熔炉", core.RecipeFurnace, core.ItemStack{Item: core.ItemStone, Count: 8}},
		{"铁块", core.RecipeIronBlock, core.ItemStack{Item: core.ItemIronIngot, Count: 9}},
		{"箱子", core.RecipeChest, core.ItemStack{Item: core.ItemStone, Count: 8}},
		{"橡木木板", core.RecipeOakPlanks, core.ItemStack{Item: core.ItemOakLog, Count: 1}},
		{"发光方块", core.RecipeLightBlock, core.ItemStack{Item: core.ItemGlass, Count: 4}},
	} {
		t.Run(test.name, func(t *testing.T) {
			app, serverEndpoint := newInteractiveTestApplication(t)
			var inventory core.Inventory
			inventory.Hotbar.Slots[0] = test.input
			if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
				t.Fatal(err)
			}
			app.inventorySource = 5
			width, height := uint32(1280), uint32(720)
			x, y := recipeButtonCenter(t, test.recipe, width, height)

			app.clickInventorySlot(x, y, width, height)
			message := receiveInteractiveClientMessage(t, serverEndpoint)
			craft, ok := message.(network.CraftRecipe)
			if !ok || craft.Recipe != test.recipe {
				t.Fatalf("合成请求 = %#v，想要 recipe %d", message, test.recipe)
			}
			assertNoInteractiveClientMessage(t, serverEndpoint)
			if app.inventorySource != -1 {
				t.Fatalf("合成后来源未清除: %d", app.inventorySource)
			}
			got, confirmed := app.inventory.State()
			if !confirmed || got != inventory {
				t.Fatalf("合成请求本地改写镜像: %+v, %v", got, confirmed)
			}
		})
	}
}

func TestUnavailableCraftRecipeClickDoesNothing(t *testing.T) {
	for _, recipe := range []core.RecipeID{
		core.RecipeStoneBricks, core.RecipeFurnace, core.RecipeIronBlock,
	} {
		t.Run(fmt.Sprintf("recipe_%d", recipe), func(t *testing.T) {
			app, serverEndpoint := newInteractiveTestApplication(t)
			inventory := core.Inventory{}
			if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
				t.Fatal(err)
			}
			x, y := recipeButtonCenter(t, recipe, 1280, 720)

			app.clickInventorySlot(x, y, 1280, 720)
			assertNoInteractiveClientMessage(t, serverEndpoint)
			got, confirmed := app.inventory.State()
			if !confirmed || got != inventory {
				t.Fatalf("不可用配方改写镜像: %+v, %v", got, confirmed)
			}
		})
	}

	t.Run("产物无容量", func(t *testing.T) {
		app, serverEndpoint := newInteractiveTestApplication(t)
		inventory := core.Inventory{}
		for slot := range inventory.Hotbar.Slots {
			inventory.Hotbar.Slots[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
		}
		for slot := range inventory.Backpack {
			inventory.Backpack[slot] = core.ItemStack{Item: core.ItemDirt, Count: core.MaxStackCount}
		}
		inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 5}
		if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
			t.Fatal(err)
		}
		x, y := recipeButtonCenter(t, core.RecipeStoneBricks, 1280, 720)

		app.clickInventorySlot(x, y, 1280, 720)
		assertNoInteractiveClientMessage(t, serverEndpoint)
		got, confirmed := app.inventory.State()
		if !confirmed || got != inventory {
			t.Fatalf("产物无容量时改写镜像: %+v, %v", got, confirmed)
		}
	})
}

func TestCraftRecipeClickWaitsForConfirmedInventory(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	x, y := recipeButtonCenter(t, core.RecipeStoneBricks, 1280, 720)

	app.clickInventorySlot(x, y, 1280, 720)
	assertNoInteractiveClientMessage(t, serverEndpoint)
	if app.sequence != 0 {
		t.Fatalf("未确认背包消耗了 sequence: %d", app.sequence)
	}
	if _, confirmed := app.inventory.State(); confirmed {
		t.Fatal("点击后空镜像被标记为已确认")
	}
}

func TestPlayerResetClearsInventorySource(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	app.inventoryOpen = true
	app.inventorySource = 8
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true, Reset: true,
	})

	app.drainServerMessages(1)
	if !app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("reset 后 open=%v source=%d，想要界面保持且来源清除", app.inventoryOpen, app.inventorySource)
	}
}

func TestPlayerResetClosesFurnaceUI(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	if err := app.furnace.Apply(network.FurnaceState{
		Furnace: core.FurnaceRef{Dimension: core.Overworld, Generation: 1},
	}); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true
	app.inventorySource = core.FurnaceInputSlot
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true, Reset: true,
	})

	app.drainServerMessages(1)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("reset 后 ui=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("reset 后仍保留熔炉镜像")
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)
}

func TestClientSessionCloseClearsInventoryUIState(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	if err := app.furnace.Apply(network.FurnaceState{
		Furnace: core.FurnaceRef{Dimension: core.Overworld, Generation: 1},
	}); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen = true
	app.inventorySource = 8

	app.closeClientSession(nil)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("断线后 open=%v source=%d，想要界面关闭且来源清除", app.inventoryOpen, app.inventorySource)
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("断线后仍保留熔炉镜像")
	}
}

func TestInventoryCloseClearsSourceAndRecapturesCursor(t *testing.T) {
	app, _ := newInteractiveTestApplication(t)
	window := &fakeInteractiveWindow{}
	app.window = window
	app.setInventoryOpen(true)
	if window.CursorCaptured() {
		t.Fatal("打开背包后仍捕获鼠标")
	}
	width, height := uint32(1280), uint32(720)
	x, y := inventorySlotCenter(t, 5, width, height)
	app.clickInventorySlot(x, y, width, height)

	app.setInventoryOpen(false)
	if app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("关闭后 open=%v source=%d", app.inventoryOpen, app.inventorySource)
	}
	if !window.CursorCaptured() {
		t.Fatal("关闭背包后未恢复鼠标捕获")
	}
}

func inventorySlotCenter(t *testing.T, slot int, width, height uint32) (float64, float64) {
	t.Helper()
	for x := range int(width) {
		for y := range int(height) {
			got, ok := hud.InventorySlotAt(float64(x), float64(y), width, height)
			if ok && int(got) == slot {
				return float64(x), float64(y)
			}
		}
	}
	t.Fatalf("找不到栏位 %d 的像素", slot)
	return 0, 0
}

func furnaceSlotCenter(t *testing.T, slot int, width, height uint32) (float64, float64) {
	t.Helper()
	for x := range int(width) {
		for y := range int(height) {
			got, ok := hud.FurnaceSlotAt(float64(x), float64(y), width, height)
			if ok && int(got) == slot {
				return float64(x), float64(y)
			}
		}
	}
	t.Fatalf("找不到熔炉统一栏位 %d 的像素", slot)
	return 0, 0
}

func recipeButtonCenter(t *testing.T, recipe core.RecipeID, width, height uint32) (float64, float64) {
	t.Helper()
	for y := range int(height) {
		for x := range int(width) {
			if got, ok := hud.RecipeButtonAt(float64(x), float64(y), width, height); ok &&
				got == recipe {
				return float64(x), float64(y)
			}
		}
	}
	t.Fatalf("找不到 recipe %d 的按钮像素", recipe)
	return 0, 0
}

func receiveInteractiveClientMessage(
	t *testing.T,
	endpoint network.ServerEndpoint,
) network.ClientMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := endpoint.Recv(ctx)
	if err != nil {
		t.Fatalf("接收客户端消息: %v", err)
	}
	return message
}

func assertNoInteractiveClientMessage(t *testing.T, endpoint network.ServerEndpoint) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	message, err := endpoint.Recv(ctx)
	if err == nil {
		t.Fatalf("意外客户端消息: %#v", message)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("检查无客户端消息: %v", err)
	}
}

func TestInteractiveDropSendsOnlyWhenReadyAndAllowed(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	ready := network.PlayerState{
		ServerTick: 1,
		Dimension:  core.Overworld,
		Position:   mgl32.Vec3{0.5, 10, 0.5},
		OnGround:   true,
		Ready:      true,
		Reset:      true,
	}

	// 未 Ready：不得发送，也不得分配序号。
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{Drop: true}, true)
	if app.sequence != 0 {
		t.Fatalf("未 Ready 时分配了 sequence=%d", app.sequence)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)

	sendInteractiveServerMessage(t, serverEndpoint, ready)
	app.drainServerMessages(1)

	// Ready 但操作被抑制：同样不得发送。
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{Drop: true}, false)
	if app.sequence != 0 {
		t.Fatalf("allowActions=false 时分配了 sequence=%d", app.sequence)
	}
	assertNoInteractiveClientMessage(t, serverEndpoint)

	// Ready 且允许：恰好发送一条只携带序号的请求。
	beforeInventory, beforeHasInventory := app.inventory.State()
	beforeDrops := len(app.itemDrops.Presentations())
	app.applyInteractiveInput(0, client.Movement{}, client.Actions{Drop: true}, true)

	message := receiveInteractiveClientMessage(t, serverEndpoint)
	drop, ok := message.(network.DropSelectedItem)
	if !ok {
		t.Fatalf("上行消息 = %T，想要 DropSelectedItem", message)
	}
	if drop.Sequence != 1 {
		t.Fatalf("序号 = %d，想要 1", drop.Sequence)
	}
	// 客户端不预测：本地背包与掉落物镜像都不得改变。
	if got, has := app.inventory.State(); got != beforeInventory || has != beforeHasInventory {
		t.Fatalf("客户端预测了背包扣减：%+v", got)
	}
	if got := len(app.itemDrops.Presentations()); got != beforeDrops {
		t.Fatalf("客户端创建了本地掉落物：%d", got)
	}
}

// TestUseKeySendsTillSoilOnlyForHoeAgainstSoil 覆盖任务 4.7：手持锄头对着
// 泥土或草按「使用」键必须发翻地命令，其余手持物与目标组合的行为一字不变。
//
// 表里既有"必须发翻地"的行，也有四行"必须仍发放置"的对照（普通方块、镐、
// 损坏锄头、锄头对着石头）。只测翻地那一行的话，一个把所有「使用」都改发
// 翻地的实现也会全绿。
func TestUseKeySendsTillSoilOnlyForHoeAgainstSoil(t *testing.T) {
	stoneFull, _ := core.ItemMaxDurability(core.ItemStonePickaxe)
	hoeFull, _ := core.ItemMaxDurability(core.ItemStoneHoe)
	for _, tc := range []struct {
		name   string
		target core.BlockID
		held   core.ItemStack
		till   bool
	}{
		{"锄头对草发翻地", core.GrassID,
			core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: hoeFull}, true},
		{"锄头对泥土发翻地", core.DirtID,
			core.ItemStack{Item: core.ItemIronHoe, Count: 1, Durability: 3}, true},
		{"锄头对石头仍发放置", core.StoneID,
			core.ItemStack{Item: core.ItemStoneHoe, Count: 1, Durability: hoeFull}, false},
		{"损坏锄头对草仍发放置", core.GrassID,
			core.ItemStack{Item: core.ItemBrokenStoneHoe, Count: 1}, false},
		{"镐对草仍发放置", core.GrassID,
			core.ItemStack{Item: core.ItemStonePickaxe, Count: 1, Durability: stoneFull}, false},
		{"普通方块对草仍发放置", core.GrassID,
			core.ItemStack{Item: core.ItemDirt, Count: 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, serverEndpoint := newInteractiveTestApplication(t)
			if err := app.predictor.Begin(network.PlayerState{
				ServerTick: 1, Dimension: core.Overworld,
				Position: mgl32.Vec3{0.5, 10, 3.5}, OnGround: true, Ready: true,
			}); err != nil {
				t.Fatal(err)
			}
			app.camera = client.Camera{Pos: mgl32.Vec3{0.5, 10.5, 3.5}}
			target := core.BlockPos{X: 0, Y: 10, Z: 0}
			loadInteractiveBlock(t, app, target, tc.target)
			var inventory core.Inventory
			inventory.Hotbar.Selected = 4
			inventory.Hotbar.Slots[4] = tc.held
			if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
				t.Fatal(err)
			}

			app.placeBlock()
			message := receiveInteractiveClientMessage(t, serverEndpoint)
			if tc.till {
				till, ok := message.(network.TillSoil)
				if !ok || till != (network.TillSoil{Sequence: 1}) {
					t.Fatalf("请求 = %#v，想要 sequence 1 的翻地命令", message)
				}
			} else if place, ok := message.(network.PlaceBlock); !ok ||
				place != (network.PlaceBlock{Sequence: 1, Slot: 4}) {
				t.Fatalf("请求 = %#v，想要放置已确认栏位 4", message)
			}
			// 客户端绝不预测式改方块：目标格必须仍是服务端发来的那个编号。
			if got, loaded := app.mirror.BlockAt(core.Overworld, target); !loaded ||
				got != tc.target {
				t.Fatalf("本地镜像被预测式改写: %d, loaded=%v，想要保持 %d",
					got, loaded, tc.target)
			}
			assertNoInteractiveClientMessage(t, serverEndpoint)
		})
	}
}

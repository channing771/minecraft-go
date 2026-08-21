package main

import (
	"slices"
	"testing"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
)

func captureSceneByName(t *testing.T, name string) captureScene {
	t.Helper()
	var matches []captureScene
	for _, scene := range captureScenes {
		if scene.Name == name {
			matches = append(matches, scene)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("capture scene %q count=%d，想要 1", name, len(matches))
	}
	return matches[0]
}

// TestWaterUnderwaterCaptureSceneIsLast 钉住 water-underwater 排在场景表最后。
//
// 这条约束承的重是**位置性**的：该场景注入的权威 PlayerState 带一个远大于真实
// 值的 ServerTick，之后一切真实 PlayerState 都会被 Predictor 的单调校验静默
// 忽略，眼睛浸没标志因此永久停在"在水里"。排在它之后的任何场景都会带着水色
// 叠加与被压低的可见半径出图——实测把它插在 ai-companion 之前时，ai-companion
// 有 98.75% 的像素随之改变。
//
// 断言写成"最后一个是它"而不是"它在表里"：后者是存在性断言，插到中间也照样
// 通过，正是要挡的那种改动。
func TestWaterUnderwaterCaptureSceneIsLast(t *testing.T) {
	last := captureScenes[len(captureScenes)-1]
	if last.Name != "water-underwater" {
		t.Fatalf("场景表最后一个是 %q，想要 water-underwater", last.Name)
	}
	if last.Prepare == nil || last.Apply == nil || last.WarmupFrames != 8 {
		t.Fatalf("water-underwater 场景不完整: %+v", last)
	}
}

// TestCaptureSceneOrderAndAICompanionDeterminism 钉住整张场景表的顺序，并覆盖
// ai-companion 夹具的确定性。
//
// 原名是 ...AICompanionIsLast...，但 ai-companion 已不再是最后一个：水景两个
// 场景追加在它之后（新增场景一律追加在表末尾，见 captureScenes 的说明），
// 而 water-underwater 另有必须排最后的硬理由，见
// TestWaterUnderwaterCaptureSceneIsLast。
func TestCaptureSceneOrderAndAICompanionDeterminism(t *testing.T) {
	wantNames := []string{
		"terrain-noon", "hud-hotbar-health", "avatar-nametag", "inventory-crafting",
		"debug-panel", "skylight-tunnel", "block-light-room", "materials-showcase",
		"target-block-feedback", "oak-grove", "ai-companion",
		"water-surface-slope", "water-underwater",
	}
	gotNames := make([]string, len(captureScenes))
	for index, scene := range captureScenes {
		gotNames[index] = scene.Name
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("capture scenes=%v，想要 %v", gotNames, wantNames)
	}
	scene := captureSceneByName(t, "ai-companion")
	if scene.Prepare == nil || scene.Apply == nil || scene.WarmupFrames != 8 {
		t.Fatalf("ai-companion 场景不完整: %+v", scene)
	}

	mesher := client.NewMesher(assets.NewRegistry(), 1)
	t.Cleanup(mesher.Close)
	app := newCaptureAICompanionState()
	app.mirror, app.mesher = client.NewMirror(), mesher
	if err := scene.Prepare(app); err != nil {
		t.Fatalf("准备 ai-companion: %v", err)
	}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			chunk, ok := app.mirror.Chunk(core.Overworld, core.ChunkPos{X: x, Z: z})
			wantRevision := uint64(1)
			if x == 0 && z == 0 {
				wantRevision = 2
			}
			if !ok || chunk.Revision != wantRevision {
				t.Fatalf("chunk (%d,%d) revision/loaded=%d/%v，想要 %d/true",
					x, z, chunk.Revision, ok, wantRevision)
			}
		}
	}
	for _, test := range []struct {
		position core.BlockPos
		want     core.BlockID
	}{
		{position: core.BlockPos{X: 5, Y: -1, Z: 4}, want: core.StoneID},
		{position: core.BlockPos{X: 5, Y: 0, Z: 4}, want: core.GrassID},
		{position: core.BlockPos{X: -1, Y: 0, Z: 4}, want: core.AirID},
	} {
		got, loaded := app.mirror.BlockAt(core.Overworld, test.position)
		if !loaded || got != test.want {
			t.Fatalf("BlockAt(%+v)=%d/%v，想要 %d/true", test.position, got, loaded, test.want)
		}
	}
	if mesher.Stats().DirtySections == 0 {
		t.Fatal("ai-companion 夹具没有经 mirror 标记 dirty section")
	}

	if err := scene.Apply(app); err != nil {
		t.Fatalf("应用 ai-companion: %v", err)
	}
	assertCaptureAICompanionState(t, app)
	overlay := app.chatOverlay()
	if !overlay.Open || overlay.Input != "@阿木 挖石头" ||
		!slices.Equal(overlay.Lines, []string{"旅人 → 阿木：挖石头"}) {
		t.Fatalf("chat overlay=%+v", overlay)
	}
	uniqueRunes := map[rune]struct{}{}
	for _, text := range append([]string{"阿木", overlay.Input}, overlay.Lines...) {
		for _, value := range text {
			uniqueRunes[value] = struct{}{}
		}
	}
	if len(uniqueRunes) > 32 {
		t.Fatalf("ai-companion 独特 rune=%d，想要不超过 32", len(uniqueRunes))
	}
}

func TestCaptureAICompanionClearsPriorClientState(t *testing.T) {
	app := newCaptureAICompanionState()
	if err := app.remotePlayers.Apply(network.RemotePlayerSpawn{
		PlayerID: core.PlayerID{6: 0x40, 8: 0x80, 15: 1}, DisplayName: "旧玩家",
		ServerTick: 1, Position: mgl32.Vec3{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	oldCompanionID := companion.ID{0: 1, 6: 0x40, 8: 0x80, 15: 2}
	if err := app.companions.ApplySpawn(network.CompanionSpawn{
		ID: oldCompanionID, Name: "旧友", Tick: 1, Dimension: core.Overworld,
		Position: mgl32.Vec3{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.chatEvents.Apply(network.ChatEvent{
		EventID: 1, PlayerID: core.PlayerID{0: 1, 6: 0x40, 8: 0x80, 15: 3},
		PlayerName: "旧客", CompanionID: oldCompanionID, CompanionName: "旧友",
		Kind: network.ChatEventAccepted, Command: "旧命令",
	}); err != nil {
		t.Fatal(err)
	}
	app.chatInput.Open()
	for _, value := range "旧输入" {
		app.chatInput.Append(value)
	}
	app.chatInput.overflow = true
	app.chatEventBuffer[0] = network.ChatEvent{EventID: 99}
	app.chatLines[0], app.chatLineCount, app.formattedChatEventID = "旧缓存", 1, 99
	inventory := core.Inventory{}
	inventory.Hotbar.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 1}
	if err := app.inventory.Apply(network.InventoryState{Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	app.inventoryOpen, app.inventorySource, app.panel.visible = true, 7, true
	if err := app.furnace.Apply(network.FurnaceState{Furnace: core.FurnaceRef{Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := app.chest.Apply(network.ChestState{Chest: core.ContainerRef{
		Kind: core.ContainerKindChest, Generation: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	app.miningOverlay = hud.MiningOverlay{Active: true, ProgressTicks: 3, RequiredTicks: 9}
	app.damageFeedback = damageFeedback{hasHealth: true, health: 7, remaining: 1}
	app.damageStrength = 0.75
	app.remotePresentations = []client.RemotePresentation{{DisplayName: "旧玩家"}}
	app.companionPresentations = []client.CompanionPresentation{{Name: "旧友"}}
	app.remoteAvatars = []render.Avatar{{Position: mgl32.Vec3{1, 2, 3}}}
	app.remoteNameTags = []render.NameTag{{Text: "旧标签"}}
	app.itemDropInstances = []render.ItemDrop{{}}
	app.blockTargetReset = true
	if err := app.itemDrops.Apply(network.ItemDropUpserts{Drops: []network.ItemDrop{{
		ID:         core.DropID{Dimension: core.Overworld, Generation: 1},
		BlockIndex: 1, Item: core.ItemStone, Count: 1,
	}}}); err != nil {
		t.Fatal(err)
	}
	app.worldTimeTicks = 18000
	app.camera = client.Camera{Pos: mgl32.Vec3{99, 99, 99}, Yaw: 1, Pitch: 1}

	scene := captureSceneByName(t, "ai-companion")
	if err := scene.Apply(app); err != nil {
		t.Fatal(err)
	}
	assertCaptureAICompanionState(t, app)
	if len(app.remotePlayers.Presentations()) != 0 || len(app.itemDrops.Presentations()) != 0 {
		t.Fatalf("旧实体未清空: remote=%v drops=%v",
			app.remotePlayers.Presentations(), app.itemDrops.Presentations())
	}
	if len(app.remotePresentations) != 0 || len(app.companionPresentations) != 0 ||
		len(app.remoteAvatars) != 0 || len(app.remoteNameTags) != 0 ||
		len(app.itemDropInstances) != 0 || app.blockTargetReset ||
		app.center != cameraChunk(app.camera.Pos) {
		t.Fatalf("派生呈现状态未重置: remote=%d companion=%d avatars=%d tags=%d drops=%d targetReset=%v center=%+v",
			len(app.remotePresentations), len(app.companionPresentations), len(app.remoteAvatars),
			len(app.remoteNameTags), len(app.itemDropInstances), app.blockTargetReset, app.center)
	}
	if app.chatEventBuffer != ([32]network.ChatEvent{}) || app.chatLines != ([6]string{}) ||
		app.chatLineCount != 0 || app.formattedChatEventID != 0 {
		t.Fatalf("旧聊天缓存未清空: buffer=%+v lines=%+v count=%d id=%d",
			app.chatEventBuffer, app.chatLines, app.chatLineCount, app.formattedChatEventID)
	}
	if _, confirmed := app.inventory.State(); confirmed || app.inventoryOpen || app.inventorySource != -1 {
		t.Fatalf("inventory 未重置: confirmed=%v open=%v source=%d",
			confirmed, app.inventoryOpen, app.inventorySource)
	}
	if app.panel.visible {
		t.Fatal("panel 未隐藏")
	}
	if _, opened := app.furnace.State(); opened {
		t.Fatal("furnace 未重置")
	}
	if _, opened := app.chest.State(); opened {
		t.Fatal("chest 未重置")
	}
	if app.miningOverlay != (hud.MiningOverlay{}) || app.damageFeedback != (damageFeedback{}) ||
		app.damageStrength != 0 {
		t.Fatalf("反馈状态未重置: mining=%+v damage=%+v strength=%v",
			app.miningOverlay, app.damageFeedback, app.damageStrength)
	}
}

func newCaptureAICompanionState() *application {
	return &application{
		remotePlayers:   client.NewRemotePlayers(),
		companions:      &client.Companions{},
		chatEvents:      &client.ChatEvents{},
		itemDrops:       client.NewItemDrops(),
		inventorySource: -1,
		panel:           &panelState{},
	}
}

func assertCaptureAICompanionState(t *testing.T, app *application) {
	t.Helper()
	wantCamera := client.Camera{
		Pos: mgl32.Vec3{5.5, 3.2, 9.5}, Yaw: 0, Pitch: -0.05,
		FovY: mgl32.DegToRad(70), Aspect: float32(captureWidth) / captureHeight,
		Near: 0.1, Far: 2000,
	}
	if app.worldTimeTicks != 6000 || app.camera != wantCamera {
		t.Fatalf("固定环境 time=%d camera=%+v，想要 6000/%+v",
			app.worldTimeTicks, app.camera, wantCamera)
	}
	presentations := app.companions.AppendPresentations(nil)
	wantID := companion.ID{0: 0x42, 6: 0x40, 8: 0x80, 15: 0x14}
	if len(presentations) != 1 || presentations[0] != (client.CompanionPresentation{
		ID: wantID, Name: "阿木", Dimension: core.Overworld,
		Position: mgl32.Vec3{5.5, 1, 4}, Yaw: 3.1415927,
	}) {
		t.Fatalf("companion presentations=%+v", presentations)
	}
	events := app.chatEvents.Events(nil)
	wantEvent := network.ChatEvent{
		EventID: 1, PlayerID: core.PlayerID{0: 0x23, 6: 0x40, 8: 0x80, 15: 0x11},
		PlayerName: "旅人", CompanionID: wantID, CompanionName: "阿木",
		Kind: network.ChatEventAccepted, Command: "挖石头",
	}
	if len(events) != 1 || events[0] != wantEvent {
		t.Fatalf("chat events=%+v，想要 [%+v]", events, wantEvent)
	}
	if !app.chatInput.open || app.chatInput.text != "@阿木 挖石头" || app.chatInput.overflow {
		t.Fatalf("chat input=%+v", app.chatInput)
	}
}

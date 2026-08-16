//go:build darwin

package main

// 渲染切换到 Rust 后,本文件的断言面从 gfx 假设备的 pass/uniform 捕获改为
// CPU 侧可见事实:实体/名牌集合、pass 段字节流、调用计数与错误传播。
// pass 录制顺序与 uniform 布线已内化于 Rust 渲染器(crate 结构 + 既有
// golden capture 字节不变共同守护),对应的假设备用例随 Go GPU 栈退役。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/companion"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
	"github.com/channing771/mornlea/internal/world"
)

// newRemoteRenderApplication 构造带真实离屏 Rust 渲染器的最小 application;
// 名牌/HUD/面板使用 layout-only 变体并注入给定 GlyphSource。无 GPU 跳过。
func newRemoteRenderApplication(t *testing.T, glyphs render.GlyphSource) *application {
	t.Helper()
	renderer, err := client.NewRenderer(64, 64)
	if errors.Is(err, client.ErrNoGPUAdapter) {
		t.Skip("无 GPU 适配器")
	}
	if err != nil {
		t.Fatal(err)
	}
	reg := assets.NewRegistry()
	layers, pixels := reg.AtlasPixels()
	renderer.UploadAtlas(layers, pixels)
	app := &application{
		renderer:        renderer,
		scheduler:       render.NewSectionScheduler(renderer, applicationUploadPerFrame),
		frameWidth:      64,
		frameHeight:     64,
		nameTagRenderer: render.NewNameTagLayouter(glyphs),
		hotbarRenderer:  hud.NewHotbarLayout(glyphs, reg),
		itemDrops:       client.NewItemDrops(),
		remotePlayers:   client.NewRemotePlayers(),
		companions:      &client.Companions{},
		remoteNameTags:  make([]render.NameTag, 0, maxFrameNameTags),
		mirror:          client.NewMirror(),
		predictor:       client.NewPredictor(),
		mesher:          client.NewMesher(reg, 1),
		camera:          client.Camera{FovY: mgl32.DegToRad(70), Aspect: 1, Near: 0.1, Far: 100},
		loadedChunks:    make(map[core.ChunkPos]struct{}),
	}
	app.releaseResources = app.releaseOwnedResources
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestApplicationRendersSevenPlayersAndFourCompanionsInOneAvatarStream(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	app.companions = &client.Companions{}
	configureTargetFeedback(t, app)
	for index, name := range [...]string{"甲", "乙", "丙", "丁", "戊", "己", "庚"} {
		if err := app.remotePlayers.Apply(remoteSpawn(
			byte(index+1), name, 1, mgl32.Vec3{float32(index), 2, -4},
		)); err != nil {
			t.Fatal(err)
		}
	}
	for index, name := range [...]string{"阿木", "小石", "青叶", "星尘"} {
		id := companion.ID(integrationPlayerID(byte(index + 1)))
		if err := app.companions.ApplySpawn(network.CompanionSpawn{
			ID: id, Name: name, Tick: 1, Dimension: core.Overworld,
			Position: mgl32.Vec3{float32(index), 2, -8},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := len(app.remoteAvatars), 11; got != want {
		t.Fatalf("avatars=%d，想要 %d", got, want)
	}
	if got, want := len(app.remoteNameTags), 12; got != want {
		t.Fatalf("name tags=%d，想要 %d", got, want)
	}
	// 11 具身体 × 6 部件在单一实例流中(单 pass 的等价断言)。
	if got, want := len(app.avatarStream), 11*6*80; got != want {
		t.Fatalf("avatar 实例流=%d 字节，想要 %d", got, want)
	}
	avatarKeys := make(map[render.EntityKey]struct{}, len(app.remoteAvatars))
	for _, avatar := range app.remoteAvatars {
		avatarKeys[avatar.Key] = struct{}{}
	}
	if len(avatarKeys) != 11 {
		t.Fatalf("Avatar 实体键去重后=%d，想要 11", len(avatarKeys))
	}
	playerKey := render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(integrationPlayerID(1))}
	companionKey := render.EntityKey{Kind: render.EntityCompanion, ID: playerKey.ID}
	if _, ok := avatarKeys[playerKey]; !ok {
		t.Fatalf("缺少玩家键 %v", playerKey)
	}
	if _, ok := avatarKeys[companionKey]; !ok {
		t.Fatalf("缺少伙伴键 %v", companionKey)
	}
	nameTagKeys := make(map[render.EntityKey]struct{}, len(app.remoteNameTags))
	for _, tag := range app.remoteNameTags {
		nameTagKeys[tag.Key] = struct{}{}
	}
	if len(nameTagKeys) != 12 {
		t.Fatalf("NameTag 实体键去重后=%d，想要 12", len(nameTagKeys))
	}
	if _, ok := nameTagKeys[render.EntityKey{Kind: render.EntityTarget}]; !ok {
		t.Fatal("缺少目标 EntityTarget 名牌")
	}
	if glyphs.flushes != 1 {
		t.Fatalf("NameTag Prepare/Flush 次数=%d，想要 1", glyphs.flushes)
	}
	if err := validateEntityPresentationCounts(make([]render.Avatar, 12), app.remoteNameTags); err == nil {
		t.Fatal("12 个 Avatar 未被 App 层原子拒绝")
	}
	if err := validateEntityPresentationCounts(app.remoteAvatars, make([]render.NameTag, 13)); err == nil {
		t.Fatal("13 个 NameTag 未被 App 层原子拒绝")
	}
}

func TestApplicationRejectsActorOverflowBeforeGPUOrAtlasMutation(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	configureTargetFeedback(t, app)
	for index := range 12 {
		if err := app.remotePlayers.Apply(remoteSpawn(
			byte(index+1), "Remote", 1, mgl32.Vec3{float32(index), 2, -4},
		)); err != nil {
			t.Fatal(err)
		}
	}
	app.scheduler.QueueSection(core.SectionPos{Y: 4}, []mesh.Quad{{
		W: 1, H: 1, Face: mesh.FacePosY, AO: 0xff, Light: 0xf0,
	}})
	app.blockTargetReset = true
	glyphs.requests = 0
	glyphs.flushes = 0
	framesBefore := app.renderer.FrameCalls()

	if rendered, err := app.renderFrame(1); err == nil || rendered {
		t.Fatalf("overflow renderFrame=(%v,%v)，想要 false/error", rendered, err)
	}
	if got := app.renderer.FrameCalls(); got != framesBefore {
		t.Fatalf("overflow 后 render FFI=%d,想要保持 %d(GPU 未被触碰)", got, framesBefore)
	}
	if glyphs.requests != 0 || glyphs.flushes != 0 {
		t.Fatalf("overflow 后 atlas request/flush=%d/%d，想要 0/0", glyphs.requests, glyphs.flushes)
	}
	if !app.blockTargetReset {
		t.Fatal("overflow 消费了尚未成功呈现的 block target reset")
	}

	app.remotePlayers.Reset()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("移除 overflow 后 renderFrame=(%v,%v)", rendered, err)
	}
	if app.blockTargetReset {
		t.Fatal("成功帧没有消费 block target reset")
	}
	// reset 当帧目标反馈不呈现:无轮廓实例、无名牌。
	if len(app.outlineStream) != 0 || len(app.remoteNameTags) != 0 {
		t.Fatalf("reset 成功帧 outline/名牌=%d/%d,想要 0/0",
			len(app.outlineStream), len(app.remoteNameTags))
	}

	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("reset 后一帧 renderFrame=(%v,%v)", rendered, err)
	}
	// reset 后一帧恢复:12 实例轮廓 + 目标名牌。
	if len(app.outlineStream) != 12*80 || len(app.remoteNameTags) != 1 {
		t.Fatalf("reset 后一帧 outline/名牌=%d/%d,想要 %d/1",
			len(app.outlineStream), len(app.remoteNameTags), 12*80)
	}
}

func TestApplicationBlockTargetStreamsAndCapacity(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	configureTargetFeedback(t, app)
	for index, name := range [...]string{"甲", "乙", "丙", "丁", "戊", "己", "庚"} {
		if err := app.remotePlayers.Apply(remoteSpawn(
			byte(index+1), name, 1, mgl32.Vec3{float32(index), 2, 3},
		)); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.itemDrops.Apply(network.ItemDropUpserts{
		ServerTick: 2,
		Drops: []network.ItemDrop{{
			ID:   core.DropID{Dimension: core.Overworld, Generation: 1},
			Item: core.ItemStone, Count: 1, BlockIndex: 9,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	// 各 pass 段齐备:avatar/掉落物/轮廓实例流非空。
	if len(app.avatarStream) == 0 || len(app.dropStream) == 0 || len(app.outlineStream) != 12*80 {
		t.Fatalf("pass 段 avatar/drop/outline=%d/%d/%d",
			len(app.avatarStream), len(app.dropStream), len(app.outlineStream))
	}
	if len(app.remoteNameTags) != 8 || cap(app.remoteNameTags) != maxFrameNameTags {
		t.Fatalf("name tags len/cap=%d/%d，想要 8/%d", len(app.remoteNameTags), cap(app.remoteNameTags), maxFrameNameTags)
	}
	wantTargetTag := render.NameTag{
		Key:  render.EntityKey{Kind: render.EntityTarget},
		Text: "砖块", Anchor: mgl32.Vec3{0.5, 4.15, -2.5},
	}
	if got := app.remoteNameTags[7]; got != wantTargetTag {
		t.Fatalf("目标名牌=%+v，想要 %+v", got, wantTargetTag)
	}
	if glyphs.flushes != 1 {
		t.Fatalf("NameTag Prepare/Flush 次数=%d，想要 1", glyphs.flushes)
	}
}

func TestApplicationBlockTargetHiddenByUIAndSessionState(t *testing.T) {
	tests := []struct {
		name string
		hide func(*testing.T, *application)
	}{
		{name: "背包", hide: func(_ *testing.T, app *application) { app.inventoryOpen = true }},
		{name: "熔炉", hide: func(t *testing.T, app *application) {
			if err := app.furnace.Apply(network.FurnaceState{Furnace: core.FurnaceRef{Generation: 1}}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "箱子", hide: func(t *testing.T, app *application) {
			if err := app.chest.Apply(network.ChestState{Chest: core.ContainerRef{Kind: core.ContainerKindChest, Generation: 1}}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "调试面板", hide: func(_ *testing.T, app *application) {
			app.panel = &panelState{visible: true}
		}},
		{name: "reset 当帧", hide: func(_ *testing.T, app *application) {
			app.blockTargetReset = true
		}},
		{name: "断线", hide: func(_ *testing.T, app *application) {
			app.closeClientSession(nil)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newRemoteRenderApplication(t, &integrationGlyphSource{})
			configureTargetFeedback(t, app)
			test.hide(t, app)
			if rendered, err := app.renderFrame(1); err != nil || !rendered {
				t.Fatalf("renderFrame=(%v,%v)", rendered, err)
			}
			if len(app.remoteNameTags) != 0 {
				t.Fatalf("隐藏状态仍提交 %d 个名牌", len(app.remoteNameTags))
			}
			if len(app.outlineStream) != 0 {
				t.Fatalf("隐藏状态仍提交 %d 字节轮廓实例", len(app.outlineStream))
			}
		})
	}
}

func TestApplicationPlayerResetHidesBlockTargetForOneFrame(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	configureTargetFeedback(t, app)
	clientEndpoint, serverEndpoint := network.NewMemoryPair(4)
	app.clientEndpoint = clientEndpoint
	app.receiver = client.NewReceiver(clientEndpoint, 4)
	t.Cleanup(func() { _ = serverEndpoint.Close() })
	sendInteractiveServerMessage(t, serverEndpoint, network.PlayerState{
		ServerTick: 2, Dimension: core.Overworld, Ready: true, Reset: true,
	})
	app.drainServerMessages(1)

	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("reset 当帧 renderFrame=(%v,%v)", rendered, err)
	}
	if len(app.outlineStream) != 0 || len(app.remoteNameTags) != 0 {
		t.Fatalf("reset 当帧 outline/名牌=%d/%d,想要 0/0",
			len(app.outlineStream), len(app.remoteNameTags))
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("reset 后一帧 renderFrame=(%v,%v)", rendered, err)
	}
	if len(app.outlineStream) != 12*80 || len(app.remoteNameTags) != 1 {
		t.Fatalf("reset 后一帧 outline/名牌=%d/%d,想要 %d/1",
			len(app.outlineStream), len(app.remoteNameTags), 12*80)
	}
}

func TestApplicationBlockTargetStablePathDoesNotAllocate(t *testing.T) {
	app := targetBlockHitApplication(t)
	remoteTags := make([]render.NameTag, 7, maxFrameNameTags)
	for index := range remoteTags {
		id := integrationPlayerID(byte(index + 1))
		remoteTags[index] = render.NameTag{
			Key: render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte(id)}, Text: "A",
		}
	}
	glyphs := &integrationGlyphSource{}
	nameTags := render.NewNameTagLayouter(glyphs)
	budget := render.NewUploadBudget(1 << 20)
	var tags []render.NameTag
	var outline render.BlockOutline
	var outlineStream []byte
	var outlineEncoder render.InstanceEncoder
	run := func() {
		tags, outline = app.appendCurrentBlockTarget(remoteTags[:7])
		if err := nameTags.Prepare(tags, budget); err != nil {
			panic(err)
		}
		outlineStream = outlineEncoder.EncodeBlockOutlineInstances(outlineStream, outline)
	}
	run()
	if allocations := testing.AllocsPerRun(100, run); allocations != 0 {
		t.Fatalf("稳定目标反馈路径分配=%v，想要 0", allocations)
	}
	if len(tags) != 8 || !outline.Visible || len(outlineStream) != 12*80 {
		t.Fatalf("稳定路径 tags/outline/流=%d/%+v/%d，想要 8/可见/%d",
			len(tags), outline, len(outlineStream), 12*80)
	}
}

func configureTargetFeedback(t *testing.T, app *application) {
	t.Helper()
	app.camera = client.Camera{Pos: mgl32.Vec3{0.5, 3.5, 2.5}, FovY: mgl32.DegToRad(70), Aspect: 1, Near: 0.1, Far: 100}
	if err := app.predictor.Begin(network.PlayerState{
		ServerTick: 1, Dimension: core.Overworld, Ready: true,
	}); err != nil {
		t.Fatal(err)
	}
	applyTargetMirrorChunk(t, app.mirror, world.NewChunk(core.ChunkPos{}))
	applyTargetMirrorChunk(t, app.mirror, world.NewChunk(core.ChunkPos{Z: -1}))
	setTargetMirrorBlock(t, app.mirror, core.BlockPos{X: 0, Y: 3, Z: -3}, core.BrickID)
}

// Mutation killed: swallowing or replacing the atlas worker error prevents
// errors.Is from observing it at the frame boundary.
func TestRemoteGlyphErrorPropagatesFromFrame(t *testing.T) {
	glyphErr := errors.New("injected glyph worker failure")
	app := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: glyphErr})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	rendered, err := app.frame(0, 0, 25*time.Millisecond)
	if rendered || !errors.Is(err, glyphErr) {
		t.Fatalf("frame=(%v,%v), want wrapped glyph error", rendered, err)
	}
}

func TestApplicationConstructionSkipsDebugPanelRendererWhenDevOff(t *testing.T) {
	app := newRemoteRenderApplication(t, &integrationGlyphSource{})
	if app.debugPanelRenderer != nil {
		t.Fatal("Dev 为假时 debugPanelRenderer 必须是 nil")
	}
	if app.panel != nil {
		t.Fatal("Dev 为假时 panel 必须是 nil")
	}
}

func TestApplicationConstructionCreatesDebugPanelRendererWhenDevOn(t *testing.T) {
	rawEndpoint, _ := network.NewMemoryPair(1)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawEndpoint}
	t.Cleanup(func() { _ = rawEndpoint.Close() })
	stream := &connectionTestClientStream{}
	window := &connectionTestWindow{}
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return stream, nil
	}
	dependencies.loginClient = func(
		context.Context, network.ClientPacketStream, network.Identity,
	) (network.ClientEndpoint, error) {
		return endpoint, nil
	}
	dependencies.newWindow = func(int, int, string) (applicationWindow, error) { return window, nil }
	dependencies.newWindowedRenderer = func(applicationWindow) (*client.Renderer, error) {
		renderer, err := client.NewRenderer(64, 64)
		if errors.Is(err, client.ErrNoGPUAdapter) {
			t.Skip("无 GPU 适配器")
		}
		return renderer, err
	}

	options := remoteConnectionOptions()
	options.Dev = true
	app, err := newApplicationWithDependencies(options, dependencies)
	if err != nil {
		t.Fatalf("newApplication dev=true: %v", err)
	}
	if app.debugPanelRenderer == nil {
		t.Fatal("Dev 为真时 debugPanelRenderer 不能是 nil")
	}
	if app.panel == nil {
		t.Fatal("Dev 为真时 panel 不能是 nil")
	}
	if err := app.Close(); err != nil {
		t.Fatalf("dev=true application Close: %v", err)
	}
}

// Mutation killed: 未确认权威快捷栏时提交 HUD 段会改变段字节。
func TestApplicationDrawsHotbarHUDOnlyWhenConfirmed(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("未确认快捷栏 renderFrame=(%v,%v)", rendered, err)
	}
	if _, quads, _ := app.hotbarRenderer.FrameStreams(); len(quads) != 0 {
		t.Fatalf("未确认快捷栏仍产生 %d 字节 HUD quad", len(quads))
	}

	var confirmed core.Hotbar
	confirmed.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{Hotbar: confirmed}}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("已确认快捷栏 renderFrame=(%v,%v)", rendered, err)
	}
	if _, quads, _ := app.hotbarRenderer.FrameStreams(); len(quads) == 0 {
		t.Fatal("已确认快捷栏没有产生 HUD quad")
	}
}

// Mutation killed: 渲染动画把插值状态写回镜像会改变 Presentations。
func TestApplicationItemDropsDoNotMutateMirror(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app := newRemoteRenderApplication(t, glyphs)
	drop := network.ItemDrop{
		ID:   core.DropID{Dimension: core.Overworld, Slot: 0, Generation: 1},
		Item: core.ItemStone, Count: 1, BlockIndex: 9,
	}
	if err := app.itemDrops.Apply(network.ItemDropUpserts{
		ServerTick: 3, Drops: []network.ItemDrop{drop},
	}); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("有掉落物 renderFrame=(%v,%v)", rendered, err)
	}
	if len(app.dropStream) == 0 {
		t.Fatal("掉落物未进入实例流")
	}
	got := app.itemDrops.Presentations()
	if len(got) != 1 || got[0].BlockIndex != drop.BlockIndex || got[0].Count != drop.Count {
		t.Fatalf("渲染修改了镜像: %+v", got)
	}
}

func TestApplicationItemDropMirrorResetsWithSession(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	sendInteractiveServerMessage(t, serverEndpoint, network.ItemDropUpserts{
		ServerTick: 1,
		Drops: []network.ItemDrop{{
			ID:   core.DropID{Dimension: core.Overworld, Slot: 0, Generation: 1},
			Item: core.ItemGrass, Count: 1, BlockIndex: 4,
		}},
	})
	app.drainServerMessages(1)
	if len(app.itemDrops.Presentations()) != 1 {
		t.Fatal("掉落物 upsert 未进入镜像")
	}

	app.closeClientSession(nil)
	if got := app.itemDrops.Presentations(); len(got) != 0 {
		t.Fatalf("关闭会话后镜像 = %+v，想要为空", got)
	}
}

type integrationGlyphSource struct {
	flushErr error
	requests int
	flushes  int
}

func (source *integrationGlyphSource) Request(string) { source.requests++ }

func (source *integrationGlyphSource) FlushUploads(*render.UploadBudget) error {
	source.flushes++
	return source.flushErr
}

func (source *integrationGlyphSource) Glyph(rune) render.Glyph {
	return render.Glyph{Width: 8, Height: 8}
}

func (source *integrationGlyphSource) Kern(rune, rune) float32 { return 0 }

// TextureView 是旧 GPU 构造遗留的接口占位;layout-only 路径不使用,
// 删除段随 GlyphSource 接口收敛一并移除。
func (source *integrationGlyphSource) TextureView() gfx.TextureView { return nil }

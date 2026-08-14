//go:build darwin

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"reflect"
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

func TestApplicationRendersSevenPlayersAndFourCompanionsInOneAvatarAndNameTagPass(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
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
	avatarPasses, nameTagPasses := 0, 0
	for _, label := range dev.lastPasses() {
		switch label {
		case "avatar pass":
			avatarPasses++
		case "name-tag pass":
			nameTagPasses++
		}
	}
	if avatarPasses != 1 || nameTagPasses != 1 || glyphs.flushes != 1 {
		t.Fatalf("Avatar/NameTag pass/flush=%d/%d/%d，想要 1/1/1", avatarPasses, nameTagPasses, glyphs.flushes)
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
	app, dev := newRemoteRenderApplication(t, glyphs)
	configureTargetFeedback(t, app)
	for index := range 12 {
		if err := app.remotePlayers.Apply(remoteSpawn(
			byte(index+1), "Remote", 1, mgl32.Vec3{float32(index), 2, -4},
		)); err != nil {
			t.Fatal(err)
		}
	}
	app.renderer.QueueSection(core.SectionPos{Y: 4}, []mesh.Quad{{
		W: 1, H: 1, Face: mesh.FacePosY, AO: 0xff, Light: 0xf0,
	}})
	app.blockTargetReset = true
	dev.resetPasses()
	for _, buffer := range dev.buffers {
		buffer.writes = 0
		buffer.lastWrite = nil
	}
	glyphs.requests = 0
	glyphs.flushes = 0

	if rendered, err := app.renderFrame(1); err == nil || rendered {
		t.Fatalf("overflow renderFrame=(%v,%v)，想要 false/error", rendered, err)
	}
	if passes := dev.lastPasses(); len(passes) != 0 {
		t.Fatalf("overflow 后 render passes=%v，想要空", passes)
	}
	for label, buffer := range dev.buffers {
		if buffer.writes != 0 || len(buffer.lastWrite) != 0 {
			t.Errorf("overflow 后 buffer %q write=%d/%d，想要 0/0", label, buffer.writes, len(buffer.lastWrite))
		}
	}
	if glyphs.requests != 0 || glyphs.flushes != 0 {
		t.Fatalf("overflow 后 atlas request/flush=%d/%d，想要 0/0", glyphs.requests, glyphs.flushes)
	}
	if !app.blockTargetReset {
		t.Fatal("overflow 消费了尚未成功呈现的 block target reset")
	}

	app.remotePlayers.Reset()
	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("移除 overflow 后 renderFrame=(%v,%v)", rendered, err)
	}
	if app.blockTargetReset {
		t.Fatal("成功帧没有消费 block target reset")
	}
	if got, want := dev.lastPasses(), []string{"terrain pass", "hotbar pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reset 成功帧 passes=%v want=%v", got, want)
	}
	if len(app.remoteNameTags) != 0 {
		t.Fatalf("reset 成功帧仍提交 %d 个目标名牌", len(app.remoteNameTags))
	}

	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("reset 后一帧 renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "block outline pass", "name-tag pass", "hotbar pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reset 后一帧 passes=%v want=%v", got, want)
	}
}

// Mutation killed: swapping, omitting, clearing, or creating empty remote
// passes changes the real command encoder's captured pass descriptors.
func TestApplicationRenderPassOrder(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	rendered, err := app.renderFrame(1)
	if err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{"terrain pass", "avatar pass", "name-tag pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v want=%v", got, want)
	}
	if glyphs.lastBudget != app.renderer.UploadBudget() {
		t.Fatal("name-tag Prepare did not receive terrain renderer's shared upload budget")
	}
	cameraBytes := dev.bufferByLabel(t, "name-tag dynamic upload").data[:96]
	readFloat := func(offset int) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(cameraBytes[offset:]))
	}
	if got := [6]float32{
		readFloat(64), readFloat(68), readFloat(72),
		readFloat(80), readFloat(84), readFloat(88),
	}; got != ([6]float32{1, 0, 0, 0, 1, 0}) {
		t.Fatalf("billboard right/up=%v want [1 0 0]/[0 1 0]", got)
	}
	app.remotePlayers.Reset()
	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("empty renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{"terrain pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("zero-remote passes=%v want=%v", got, want)
	}
}

// 杀死变异：漏画轮廓、把轮廓放到掉落物之前或名牌之后、丢失目标名称
// 或为第八个名牌重新分配，都会改变这些可观察结果。
func TestApplicationBlockTargetRenderOrderAndCapacity(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
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
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "avatar pass", "item drop pass", "block outline pass", "name-tag pass", "hotbar pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v want=%v", got, want)
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

	readFloat := func(data []byte, offset int) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
	}
	outlineUpload := dev.bufferByLabel(t, "block outline dynamic upload").lastWrite
	wantViewProj := app.camera.ViewProj()
	for index, want := range wantViewProj {
		if got := readFloat(outlineUpload, index*4); math.Abs(float64(got-want)) > 1e-5 {
			t.Fatalf("outline ViewProj[%d]=%v，想要 %v", index, got, want)
		}
	}
	if got, want := readFloat(outlineUpload, 64), render.DayNightAt(app.worldTimeTicks).Daylight; got != want {
		t.Fatalf("outline Daylight=%v，想要 %v", got, want)
	}
	nameTagUpload := dev.bufferByLabel(t, "name-tag dynamic upload").lastWrite
	if got := [3]float32{
		readFloat(nameTagUpload, 1472), readFloat(nameTagUpload, 1476), readFloat(nameTagUpload, 1480),
	}; got != wantTargetTag.Anchor {
		t.Fatalf("排序后目标名牌锚点=%v，想要 %v", got, wantTargetTag.Anchor)
	}
}

func TestApplicationBlockTargetHiddenByUIAndSessionState(t *testing.T) {
	tests := []struct {
		name    string
		hideHUD bool
		hide    func(*testing.T, *application)
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
		{name: "断线", hideHUD: true, hide: func(_ *testing.T, app *application) {
			app.closeClientSession(nil)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
			configureTargetFeedback(t, app)
			test.hide(t, app)
			if rendered, err := app.renderFrame(1); err != nil || !rendered {
				t.Fatalf("renderFrame=(%v,%v)", rendered, err)
			}
			want := []string{"terrain pass", "hotbar pass"}
			if test.hideHUD {
				want = []string{"terrain pass"}
			}
			if got := dev.lastPasses(); !reflect.DeepEqual(got, want) {
				t.Fatalf("passes=%v want=%v", got, want)
			}
			if len(app.remoteNameTags) != 0 {
				t.Fatalf("隐藏状态仍提交 %d 个名牌", len(app.remoteNameTags))
			}
		})
	}
}

func TestApplicationPlayerResetHidesBlockTargetForOneFrame(t *testing.T) {
	app, dev := newRemoteRenderApplication(t, &integrationGlyphSource{})
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
	if got, want := dev.lastPasses(), []string{"terrain pass", "hotbar pass"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reset 当帧 passes=%v want=%v", got, want)
	}
	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("reset 后一帧 renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "block outline pass", "name-tag pass", "hotbar pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reset 后一帧 passes=%v want=%v", got, want)
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
	dev := &integrationRenderDevice{}
	glyphs := &integrationGlyphSource{}
	nameTags := render.NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, glyphs)
	defer nameTags.Release()
	outlineRenderer := render.NewBlockOutlineRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	defer outlineRenderer.Release()
	encoder := &blockTargetAllocationEncoder{}
	budget := render.NewUploadBudget(1 << 20)
	var tags []render.NameTag
	var outline render.BlockOutline
	run := func() {
		tags, outline = app.appendCurrentBlockTarget(remoteTags[:7])
		if err := nameTags.Prepare(tags, budget); err != nil {
			panic(err)
		}
		outlineRenderer.Render(encoder, nil, nil, render.Camera{}, outline)
		nameTags.Render(encoder, nil, nil, render.BillboardCamera{})
	}
	run()
	if allocations := testing.AllocsPerRun(100, run); allocations != 0 {
		t.Fatalf("稳定目标反馈路径分配=%v，想要 0", allocations)
	}
	if len(tags) != 8 || !outline.Visible {
		t.Fatalf("稳定路径 tags/outline=%d/%+v，想要 8/可见", len(tags), outline)
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
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: glyphErr})
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	rendered, err := app.frame(0, 0, 25*time.Millisecond)
	if rendered || !errors.Is(err, glyphErr) {
		t.Fatalf("frame=(%v,%v), want wrapped glyph error", rendered, err)
	}
}

// Mutation killed: reordering or repeating any top-level release marker, or
// resetting the roster after renderer release, changes the observed lifecycle.
func TestApplicationCloseReleasesRemoteRenderersInOrder(t *testing.T) {
	dev := &integrationRenderDevice{}
	reg := assets.NewRegistry()
	terrain := render.New(dev, reg, gfx.FormatRGBA8Unorm)
	atlas, err := render.NewGlyphAtlas(dev)
	if err != nil {
		t.Fatal(err)
	}
	avatar := render.NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	nameTag := render.NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, atlas)
	hotbar := hud.NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas, reg)
	itemDrop := render.NewItemDropRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	blockOutline := render.NewBlockOutlineRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float)
	damage := render.NewDamageOverlayRenderer(dev, gfx.FormatRGBA8Unorm)
	color := dev.CreateTexture(gfx.TextureDesc{Label: "main color", Width: 4, Height: 4, Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget})
	app := &application{
		dev: dev, color: color, colorView: color.View(gfx.TextureViewDesc{}),
		depth: newDepthTarget(dev, 4, 4), renderer: terrain,
		glyphAtlas: atlas, avatarRenderer: avatar, nameTagRenderer: nameTag,
		hotbarRenderer: hotbar, itemDropRenderer: itemDrop,
		blockOutlineRenderer: blockOutline, damageOverlayRenderer: damage,
		remotePlayers: client.NewRemotePlayers(),
	}
	app.releaseResources = app.releaseOwnedResources
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("roster after Close=%d", got)
	}
	markers := dev.releaseMarkers([]string{
		"damage overlay resources", "block outline resources", "item drop resources", "hotbar resources", "name-tag resources", "glyph-atlas texture", "avatar resources", "terrain resources",
		"main depth texture", "main color view", "main color texture", "device",
	})
	want := []string{"damage overlay resources", "block outline resources", "item drop resources", "hotbar resources", "name-tag resources", "glyph-atlas texture", "avatar resources", "terrain resources", "main depth texture", "main color view", "main color texture", "device"}
	if !reflect.DeepEqual(markers, want) {
		t.Fatalf("release markers=%v want=%v; all=%v", markers, want, dev.releases)
	}
	for _, marker := range want {
		if count := dev.releaseCount(marker); count != 1 {
			t.Fatalf("release %q count=%d", marker, count)
		}
	}
}

// Mutation 已验证：远端 renderer 构造乱序或受伤遮罩构造失败后正序清理，
// 都会改变资源释放标记。
func TestApplicationConstructionFailureReleasesRemoteResourcesInReverse(t *testing.T) {
	wantErr := errors.New("injected damage-overlay construction failure")
	rawEndpoint, serverEndpoint := network.NewMemoryPair(4)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawEndpoint}
	t.Cleanup(func() { _ = serverEndpoint.Close() })
	dev := &integrationRenderDevice{}
	window := &connectionTestWindow{}
	surface := &connectionTestSurface{}
	var constructionOrder []string
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return &connectionTestClientStream{}, nil
	}
	dependencies.loginClient = func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, error) {
		return endpoint, nil
	}
	dependencies.newWindow = func(int, int, string) (applicationWindow, error) { return window, nil }
	dependencies.newDevice = func(gfx.NativeWindowHandle, uint32, uint32) (gfx.Device, gfx.Surface, error) {
		return dev, surface, nil
	}
	dependencies.newGlyphAtlas = func(device gfx.Device) (*render.GlyphAtlas, error) {
		constructionOrder = append(constructionOrder, "atlas")
		return render.NewGlyphAtlas(device)
	}
	dependencies.newAvatarRenderer = func(device gfx.Device, color, depth gfx.TextureFormat) (*render.AvatarRenderer, error) {
		constructionOrder = append(constructionOrder, "avatar")
		return render.NewAvatarRenderer(device, color, depth), nil
	}
	dependencies.newNameTagRenderer = func(
		device gfx.Device,
		color, depth gfx.TextureFormat,
		atlas render.GlyphSource,
	) (*render.NameTagRenderer, error) {
		constructionOrder = append(constructionOrder, "name-tag")
		return render.NewNameTagRenderer(device, color, depth, atlas), nil
	}
	dependencies.newHotbarRenderer = func(
		device gfx.Device,
		color gfx.TextureFormat,
		atlas render.GlyphSource,
		blocks *assets.Registry,
	) (*hud.HotbarRenderer, error) {
		constructionOrder = append(constructionOrder, "hotbar")
		return hud.NewHotbarRenderer(device, color, atlas, blocks), nil
	}
	dependencies.newItemDropRenderer = func(
		device gfx.Device,
		color, depth gfx.TextureFormat,
	) (*render.ItemDropRenderer, error) {
		constructionOrder = append(constructionOrder, "item-drop")
		return render.NewItemDropRenderer(device, color, depth), nil
	}
	dependencies.newBlockOutlineRenderer = func(
		device gfx.Device,
		color, depth gfx.TextureFormat,
	) (*render.BlockOutlineRenderer, error) {
		constructionOrder = append(constructionOrder, "block-outline")
		return render.NewBlockOutlineRenderer(device, color, depth), nil
	}
	dependencies.newDamageOverlayRenderer = func(
		gfx.Device,
		gfx.TextureFormat,
	) (*render.DamageOverlayRenderer, error) {
		constructionOrder = append(constructionOrder, "damage-overlay")
		return nil, wantErr
	}
	app, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if app != nil || !errors.Is(err, wantErr) {
		t.Fatalf("construction result=(%v,%v), want nil/wrapped failure", app, err)
	}
	if want := []string{"atlas", "avatar", "name-tag", "hotbar", "item-drop", "block-outline", "damage-overlay"}; !reflect.DeepEqual(constructionOrder, want) {
		t.Fatalf("remote construction order=%v want=%v", constructionOrder, want)
	}
	wantMarkers := []string{
		"block outline resources", "item drop resources", "hotbar resources", "name-tag resources",
		"avatar resources", "glyph-atlas texture", "terrain resources",
		"main depth texture", "device",
	}
	markers := dev.releaseMarkers(wantMarkers)
	if !reflect.DeepEqual(markers, wantMarkers) {
		t.Fatalf("failure release markers=%v want=%v; all=%v", markers, wantMarkers, dev.releases)
	}
	if endpoint.closeCalls.Load() != 1 || window.closeCalls.Load() != 1 || surface.releaseCalls.Load() != 1 {
		t.Fatalf("failure ownership endpoint/window/surface=%d/%d/%d want 1/1/1",
			endpoint.closeCalls.Load(), window.closeCalls.Load(), surface.releaseCalls.Load())
	}
}

func TestApplicationBlockOutlineConstructionFailureReleasesPartialResources(t *testing.T) {
	wantErr := errors.New("注入 block-outline 构造失败")
	rawEndpoint, serverEndpoint := network.NewMemoryPair(4)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawEndpoint}
	t.Cleanup(func() { _ = serverEndpoint.Close() })
	dev := &integrationRenderDevice{}
	window := &connectionTestWindow{}
	surface := &connectionTestSurface{}
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return &connectionTestClientStream{}, nil
	}
	dependencies.loginClient = func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, error) {
		return endpoint, nil
	}
	dependencies.newWindow = func(int, int, string) (applicationWindow, error) { return window, nil }
	dependencies.newDevice = func(gfx.NativeWindowHandle, uint32, uint32) (gfx.Device, gfx.Surface, error) {
		return dev, surface, nil
	}
	dependencies.newBlockOutlineRenderer = func(
		device gfx.Device,
		color, depth gfx.TextureFormat,
	) (*render.BlockOutlineRenderer, error) {
		return render.NewBlockOutlineRenderer(device, color, depth), wantErr
	}

	app, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if app != nil || !errors.Is(err, wantErr) {
		t.Fatalf("construction result=(%v,%v)，想要 nil/包装后失败", app, err)
	}
	wantMarkers := []string{
		"block outline resources", "item drop resources", "hotbar resources",
		"name-tag resources", "avatar resources", "glyph-atlas texture",
		"terrain resources", "main depth texture", "device",
	}
	if got := dev.releaseMarkers(wantMarkers); !reflect.DeepEqual(got, wantMarkers) {
		t.Fatalf("部分构造失败释放=%v，想要 %v；all=%v", got, wantMarkers, dev.releases)
	}
	if endpoint.closeCalls.Load() != 1 || window.closeCalls.Load() != 1 || surface.releaseCalls.Load() != 1 {
		t.Fatalf("失败后 endpoint/window/surface=%d/%d/%d，想要 1/1/1",
			endpoint.closeCalls.Load(), window.closeCalls.Load(), surface.releaseCalls.Load())
	}
}

type integrationGlyphSource struct {
	flushErr   error
	lastBudget *render.UploadBudget
	requests   int
	flushes    int
}

func (atlas *integrationGlyphSource) Request(string) { atlas.requests++ }
func (atlas *integrationGlyphSource) FlushUploads(budget *render.UploadBudget) error {
	atlas.lastBudget = budget
	atlas.flushes++
	return atlas.flushErr
}
func (*integrationGlyphSource) Glyph(rune) render.Glyph {
	return render.Glyph{Advance: 10, BearingY: 8, Width: 8, Height: 10}
}
func (*integrationGlyphSource) Kern(rune, rune) float32      { return 0 }
func (*integrationGlyphSource) TextureView() gfx.TextureView { return &integrationView{} }

func newRemoteRenderApplication(t *testing.T, glyphs render.GlyphSource) (*application, *integrationRenderDevice) {
	t.Helper()
	dev := &integrationRenderDevice{}
	reg := assets.NewRegistry()
	color := dev.CreateTexture(gfx.TextureDesc{Label: "test color", Width: 16, Height: 16, Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget})
	app := &application{
		dev: dev, color: color, colorView: color.View(gfx.TextureViewDesc{}), frameWidth: 16, frameHeight: 16,
		depth: newDepthTarget(dev, 16, 16), renderer: render.New(dev, reg, gfx.FormatRGBA8Unorm),
		avatarRenderer:  render.NewAvatarRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float),
		nameTagRenderer: render.NewNameTagRenderer(dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float, glyphs),
		hotbarRenderer:  hud.NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, glyphs, reg),
		damageOverlayRenderer: render.NewDamageOverlayRenderer(
			dev, gfx.FormatRGBA8Unorm,
		),
		itemDropRenderer: render.NewItemDropRenderer(
			dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float,
		),
		blockOutlineRenderer: render.NewBlockOutlineRenderer(
			dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float,
		),
		itemDrops:      client.NewItemDrops(),
		remotePlayers:  client.NewRemotePlayers(),
		companions:     &client.Companions{},
		remoteNameTags: make([]render.NameTag, 0, maxFrameNameTags),
		mirror:         client.NewMirror(), predictor: client.NewPredictor(),
		mesher: client.NewMesher(reg, 1), camera: client.Camera{FovY: mgl32.DegToRad(70), Aspect: 1, Near: 0.1, Far: 100},
		loadedChunks: make(map[core.ChunkPos]struct{}),
	}
	app.releaseResources = app.releaseOwnedResources
	t.Cleanup(func() { _ = app.Close() })
	return app, dev
}

type integrationRenderDevice struct {
	releases, passes []string
	events           []string
	draws            []string
	drawInstances    []uint32
	buffers          map[string]*integrationBuffer
}

func (d *integrationRenderDevice) CreateBuffer(desc gfx.BufferDesc) gfx.Buffer {
	if d.buffers == nil {
		d.buffers = make(map[string]*integrationBuffer)
	}
	buffer := &integrationBuffer{desc: desc, release: func() { d.releases = append(d.releases, desc.Label) }}
	d.buffers[desc.Label] = buffer
	return buffer
}
func (d *integrationRenderDevice) CreateShaderModule(string) gfx.ShaderModule {
	return &integrationResource{}
}
func (d *integrationRenderDevice) CreateRenderPipeline(desc gfx.RenderPipelineDesc) gfx.RenderPipeline {
	return &integrationResource{release: func() { d.releases = append(d.releases, desc.Label+" pipeline") }}
}
func (d *integrationRenderDevice) CreateComputePipeline(desc gfx.ComputePipelineDesc) gfx.ComputePipeline {
	return &integrationResource{release: func() { d.releases = append(d.releases, desc.Label+" pipeline") }}
}
func (d *integrationRenderDevice) CreateBindGroup(desc gfx.BindGroupDesc) gfx.BindGroup {
	return &integrationResource{release: func() { d.releases = append(d.releases, desc.Label) }}
}
func (d *integrationRenderDevice) CreateTexture(desc gfx.TextureDesc) gfx.Texture {
	return &integrationTexture{label: desc.Label, releases: &d.releases}
}
func (d *integrationRenderDevice) CreateSampler(desc gfx.SamplerDesc) gfx.Sampler {
	return &integrationResource{release: func() { d.releases = append(d.releases, desc.Label) }}
}
func (d *integrationRenderDevice) CreateCommandEncoder() gfx.CommandEncoder {
	return &integrationEncoder{device: d}
}
func (d *integrationRenderDevice) Submit(...gfx.CommandBuffer) {
	d.events = append(d.events, "submit")
}
func (d *integrationRenderDevice) Poll(bool)            { d.events = append(d.events, "poll") }
func (d *integrationRenderDevice) Release()             { d.releases = append(d.releases, "device") }
func (d *integrationRenderDevice) lastPasses() []string { return append([]string(nil), d.passes...) }
func (d *integrationRenderDevice) resetPasses()         { d.passes = nil }
func (d *integrationRenderDevice) lastDrawInstanceCount() uint32 {
	if len(d.drawInstances) == 0 {
		return 0
	}
	return d.drawInstances[len(d.drawInstances)-1]
}
func (d *integrationRenderDevice) bufferByLabel(t *testing.T, label string) *integrationBuffer {
	t.Helper()
	buffer := d.buffers[label]
	if buffer == nil {
		t.Fatalf("missing buffer %q", label)
	}
	return buffer
}
func (d *integrationRenderDevice) releaseCount(label string) int {
	count := 0
	for _, got := range d.releases {
		if got == label {
			count++
		}
	}
	return count
}
func (d *integrationRenderDevice) releaseMarkers(labels []string) []string {
	set := map[string]bool{}
	for _, label := range labels {
		set[label] = true
	}
	var out []string
	for _, label := range d.releases {
		if set[label] {
			out = append(out, label)
		}
	}
	return out
}

type integrationBuffer struct {
	desc      gfx.BufferDesc
	data      []byte
	lastWrite []byte
	writes    int
	release   func()
}

func (b *integrationBuffer) Size() uint64 { return b.desc.Size }
func (b *integrationBuffer) Write(offset uint64, data []byte) {
	end := int(offset) + len(data)
	if len(b.data) < end {
		b.data = make([]byte, end)
	}
	copy(b.data[int(offset):], data)
	// lastWrite 只记录本次 Write 调用的确切字节数，用来分辨这一帧到底画了
	// 多少内容；data 只增不减，无法区分帧与帧之间上传的字节数是否变少。
	b.lastWrite = append(b.lastWrite[:0], data...)
	b.writes++
}
func (b *integrationBuffer) ReadBack() []byte { return append([]byte(nil), b.data...) }
func (b *integrationBuffer) Release() {
	if b.release != nil {
		b.release()
		b.release = nil
	}
}

type integrationResource struct{ release func() }

func (r *integrationResource) Release() {
	if r.release != nil {
		r.release()
		r.release = nil
	}
}

type integrationTexture struct {
	label    string
	releases *[]string
	released bool
}

func (t *integrationTexture) View(gfx.TextureViewDesc) gfx.TextureView {
	return &integrationView{label: t.label + " view", releases: t.releases}
}
func (*integrationTexture) WriteLayer(uint32, uint32, []byte)                                  {}
func (*integrationTexture) WriteRegion(uint32, uint32, uint32, uint32, uint32, uint32, []byte) {}
func (*integrationTexture) ReadLayer(uint32, uint32) []byte                                    { return nil }
func (t *integrationTexture) Release() {
	if !t.released {
		*t.releases = append(*t.releases, t.label+" texture")
		t.released = true
	}
}

type integrationView struct {
	label    string
	releases *[]string
	released bool
}

func (v *integrationView) Release() {
	if !v.released && v.releases != nil {
		*v.releases = append(*v.releases, v.label)
		v.released = true
	}
}

type integrationEncoder struct{ device *integrationRenderDevice }

func (e *integrationEncoder) BeginRenderPass(desc gfx.RenderPassDesc) gfx.RenderPass {
	e.device.passes = append(e.device.passes, desc.Label)
	return &integrationPass{device: e.device}
}
func (*integrationEncoder) BeginComputePass(string) gfx.ComputePass                           { return &integrationComputePass{} }
func (*integrationEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {}
func (e *integrationEncoder) Finish() gfx.CommandBuffer {
	e.device.events = append(e.device.events, "finish")
	return &integrationResource{release: func() { e.device.events = append(e.device.events, "release") }}
}

type integrationPass struct{ device *integrationRenderDevice }

func (*integrationPass) SetPipeline(gfx.RenderPipeline)             {}
func (*integrationPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*integrationPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*integrationPass) SetIndexBuffer(gfx.Buffer, uint64)          {}
func (p *integrationPass) DrawIndexedIndirect(gfx.Buffer, uint64) {
	p.device.draws = append(p.device.draws, "indirect")
}
func (p *integrationPass) Draw(vertexCount, instanceCount uint32) {
	if vertexCount == 3 {
		p.device.draws = append(p.device.draws, "sky triangle")
		return
	}
	p.device.draws = append(p.device.draws, "draw")
	p.device.drawInstances = append(p.device.drawInstances, instanceCount)
}
func (*integrationPass) End() {}

type integrationComputePass struct{}

func (*integrationComputePass) SetPipeline(gfx.ComputePipeline)    {}
func (*integrationComputePass) SetBindGroup(uint32, gfx.BindGroup) {}
func (*integrationComputePass) Dispatch(uint32, uint32, uint32)    {}
func (*integrationComputePass) End()                               {}

type blockTargetAllocationEncoder struct {
	pass blockTargetAllocationPass
}

func (encoder *blockTargetAllocationEncoder) BeginRenderPass(gfx.RenderPassDesc) gfx.RenderPass {
	return &encoder.pass
}
func (*blockTargetAllocationEncoder) BeginComputePass(string) gfx.ComputePass {
	panic("目标反馈不应创建 compute pass")
}
func (*blockTargetAllocationEncoder) CopyBufferToBuffer(gfx.Buffer, uint64, gfx.Buffer, uint64, uint64) {
	panic("目标反馈不应复制 buffer")
}
func (*blockTargetAllocationEncoder) Finish() gfx.CommandBuffer { return nil }

type blockTargetAllocationPass struct{}

func (*blockTargetAllocationPass) SetPipeline(gfx.RenderPipeline)             {}
func (*blockTargetAllocationPass) SetBindGroup(uint32, gfx.BindGroup)         {}
func (*blockTargetAllocationPass) SetVertexBuffer(uint32, gfx.Buffer, uint64) {}
func (*blockTargetAllocationPass) SetIndexBuffer(gfx.Buffer, uint64)          {}
func (*blockTargetAllocationPass) DrawIndexedIndirect(gfx.Buffer, uint64)     {}
func (*blockTargetAllocationPass) Draw(uint32, uint32)                        {}
func (*blockTargetAllocationPass) End()                                       {}

// TestApplicationConstructionSkipsDebugPanelRendererWhenDevOff 与
// TestApplicationConstructionCreatesDebugPanelRendererWhenDevOn 一起守住
// --dev 只门控调试面板这条约束：字段是否非 nil 必须严格跟随 options.Dev，
// 不能悄悄在两条路径上都创建或都不创建 GPU 资源。
func TestApplicationConstructionSkipsDebugPanelRendererWhenDevOff(t *testing.T) {
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
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
	surface := &connectionTestSurface{}
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
	dependencies.newDevice = func(gfx.NativeWindowHandle, uint32, uint32) (gfx.Device, gfx.Surface, error) {
		device, err := gfx.NewHeadlessDevice()
		return device, surface, err
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

// Mutation killed: drawing the HUD before terrain/avatar/name tags, or drawing
// it without a confirmed authoritative state, changes the observed pass order.
func TestApplicationDrawsHotbarHUDLast(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("未确认快捷栏 renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "avatar pass", "name-tag pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("未确认快捷栏 passes=%v want=%v", got, want)
	}

	var confirmed core.Hotbar
	confirmed.Slots[0] = core.ItemStack{Item: core.ItemStone, Count: 4}
	if err := app.inventory.Apply(network.InventoryState{Inventory: core.Inventory{Hotbar: confirmed}}); err != nil {
		t.Fatal(err)
	}
	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("已确认快捷栏 renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "avatar pass", "name-tag pass", "hotbar pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("已确认快捷栏 passes=%v want=%v", got, want)
	}
}

// Mutation killed: drawing drops after name tags, skipping the mirror, or
// leaking animation into the mirror changes the observed pass order or values.
func TestApplicationDrawsItemDropsAfterAvatars(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("空掉落物镜像 renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "avatar pass", "name-tag pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("空掉落物镜像 passes=%v want=%v", got, want)
	}

	drop := network.ItemDrop{
		ID:   core.DropID{Dimension: core.Overworld, Slot: 0, Generation: 1},
		Item: core.ItemStone, Count: 1, BlockIndex: 9,
	}
	if err := app.itemDrops.Apply(network.ItemDropUpserts{
		ServerTick: 3, Drops: []network.ItemDrop{drop},
	}); err != nil {
		t.Fatal(err)
	}
	dev.resetPasses()
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("有掉落物 renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "avatar pass", "item drop pass", "name-tag pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("掉落物 passes=%v want=%v", got, want)
	}

	// 动画不得回写镜像。
	got := app.itemDrops.Presentations()
	if len(got) != 1 || got[0].BlockIndex != drop.BlockIndex || got[0].Count != drop.Count {
		t.Fatalf("渲染修改了镜像: %+v", got)
	}
}

// Mutation killed: 让 terrain/avatar/item-drop 使用不同矩阵或 daylight，
// 或让 sky 使用错误的 inverse/天体参数，会改变捕获到的 uniform buffer 内容。
func TestApplicationRenderFrameCameraAndSkyParameters(t *testing.T) {
	glyphs := &integrationGlyphSource{}
	app, dev := newRemoteRenderApplication(t, glyphs)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{1, 2, 3})); err != nil {
		t.Fatal(err)
	}
	drop := network.ItemDrop{
		ID:   core.DropID{Dimension: core.Overworld, Slot: 0, Generation: 1},
		Item: core.ItemStone, Count: 1, BlockIndex: 9,
	}
	if err := app.itemDrops.Apply(network.ItemDropUpserts{
		ServerTick: 3, Drops: []network.ItemDrop{drop},
	}); err != nil {
		t.Fatal(err)
	}
	app.camera.Pos = mgl32.Vec3{17.25, 64.5, -93.75}
	app.worldTimeTicks = 6000
	if rendered, err := app.renderFrame(1); err != nil || !rendered {
		t.Fatalf("renderFrame=(%v,%v)", rendered, err)
	}
	if got, want := dev.lastPasses(), []string{
		"terrain pass", "avatar pass", "item drop pass", "name-tag pass",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("passes=%v want=%v", got, want)
	}

	dayNight := render.DayNightAt(app.worldTimeTicks)
	wantViewProj := app.camera.ViewProj()
	wantViewProjInv := wantViewProj.Inv()
	readFloat := func(data []byte, offset int) float32 {
		return math.Float32frombits(binary.LittleEndian.Uint32(data[offset:]))
	}
	mat4From := func(data []byte, offset int) mgl32.Mat4 {
		var out mgl32.Mat4
		for i := range out {
			out[i] = readFloat(data, offset+i*4)
		}
		return out
	}

	terrain := dev.bufferByLabel(t, "terrain camera")
	sky := dev.bufferByLabel(t, "sky uniform")
	if got := mat4From(terrain.data, 0); !got.ApproxEqualThreshold(wantViewProj, 1e-4) {
		t.Fatalf("terrain ViewProj=%v want=%v", got, wantViewProj)
	}
	if got := mat4From(sky.data, 0); !got.ApproxEqualThreshold(wantViewProjInv, 1e-4) {
		t.Fatalf("sky ViewProjInv=%v want=%v", got, wantViewProjInv)
	}
	// avatar 与 item-drop 继续与 terrain 共享同一正向矩阵和 daylight。
	for _, label := range []string{"avatar dynamic upload", "item drop dynamic upload"} {
		buffer := dev.bufferByLabel(t, label)
		if got := mat4From(buffer.data, 0); !got.ApproxEqualThreshold(wantViewProj, 1e-4) {
			t.Fatalf("%s ViewProj=%v want=%v", label, got, wantViewProj)
		}
		if got := readFloat(buffer.data, 64); got != dayNight.Daylight {
			t.Fatalf("%s Daylight=%v want=%v", label, got, dayNight.Daylight)
		}
	}
	// sky uniform 布局：ViewProjInv 64 字节 + SunDirection xyz + Daylight + StarVisibility。
	for i, want := range dayNight.SunDirection {
		if got := readFloat(sky.data, 64+i*4); got != want {
			t.Fatalf("sky SunDirection[%d]=%v want=%v", i, got, want)
		}
	}
	if got := readFloat(sky.data, 76); got != dayNight.Daylight {
		t.Fatalf("sky Daylight=%v want=%v", got, dayNight.Daylight)
	}
	if got := readFloat(sky.data, 80); got != dayNight.StarVisibility {
		t.Fatalf("sky StarVisibility=%v want=%v", got, dayNight.StarVisibility)
	}
	cloudOffset := render.CloudOffsetAt(app.worldTimeTicks)
	if got := binary.LittleEndian.Uint32(sky.data[84:88]); got != cloudOffset.MacroX {
		t.Fatalf("sky CloudOffset.MacroX=%d want=%d", got, cloudOffset.MacroX)
	}
	for offset, want := range map[int]float32{96: app.camera.Pos[0], 100: app.camera.Pos[1], 104: app.camera.Pos[2], 108: cloudOffset.Local} {
		if got := readFloat(sky.data, offset); got != want {
			t.Fatalf("sky cloud float at %d=%v want=%v", offset, got, want)
		}
	}
	if got := readFloat(terrain.data, 76); got != dayNight.Daylight {
		t.Fatalf("terrain Daylight=%v want=%v", got, dayNight.Daylight)
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

//go:build darwin

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/render"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
)

func TestPerformanceRecordersOnlyEnableSaveSamplingForBenchmark(t *testing.T) {
	interactiveTicks, interactiveSaves := newPerformanceRecorders(false)
	if interactiveTicks == nil || interactiveSaves != nil {
		t.Fatalf("交互模式 recorders ticks=%v saves=%v，想要 tick recorder 且无 save recorder",
			interactiveTicks, interactiveSaves)
	}

	benchmarkTicks, benchmarkSaves := newPerformanceRecorders(true)
	if benchmarkTicks == nil || benchmarkSaves == nil {
		t.Fatalf("benchmark recorders ticks=%v saves=%v，想要两者都有",
			benchmarkTicks, benchmarkSaves)
	}
}

// Mutation killed: routing any remote-player message through Mirror closes the
// endpoint instead of completing the spawn/state/despawn roster lifecycle.
func TestRemoteMessagesRouteOnlyToRoster(t *testing.T) {
	app, serverEndpoint, endpoint, _ := newRemoteProtocolApplication(t)
	spawn := remoteSpawn(2, "Remote-2", 1, mgl32.Vec3{1, 64, 3})
	sendInteractiveServerMessage(t, serverEndpoint, spawn)
	app.drainServerMessages(1)
	got := app.remotePlayers.Presentations()
	if len(got) != 1 || got[0].PlayerID != spawn.PlayerID || got[0].DisplayName != spawn.DisplayName {
		t.Fatalf("spawn presentations=%+v", got)
	}
	states := network.RemotePlayerStates{ServerTick: 2, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld,
		Position: mgl32.Vec3{9, 65, -4}, Yaw: 0.7, Pitch: -0.2,
	}}}
	sendInteractiveServerMessage(t, serverEndpoint, states)
	app.drainServerMessages(1)
	got = app.remotePlayers.Presentations()
	if len(got) != 1 || got[0].Position != states.Players[0].Position || got[0].Yaw != 0.7 || got[0].Pitch != -0.2 {
		t.Fatalf("state presentations=%+v", got)
	}
	sendInteractiveServerMessage(t, serverEndpoint, network.RemotePlayerDespawn{PlayerID: spawn.PlayerID})
	app.drainServerMessages(1)
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("despawn roster=%d", got)
	}
	if got := endpoint.closeCalls.Load(); got != 0 {
		t.Fatalf("valid remote lifecycle closed endpoint %d times", got)
	}
}

// Mutation killed: calling serverCancel, leaving the endpoint open, or failing
// to reset the roster on a duplicate Spawn violates client-session isolation.
func TestRemoteProtocolErrorClosesOnlyClientEndpoint(t *testing.T) {
	app, serverEndpoint, endpoint, cancelCount := newRemoteProtocolApplication(t)
	spawn := remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{0, 64, 0})
	sendInteractiveServerMessage(t, serverEndpoint, spawn)
	sendInteractiveServerMessage(t, serverEndpoint, spawn)
	app.drainServerMessages(1)
	if got := len(app.remotePlayers.Presentations()); got != 1 {
		t.Fatalf("roster after valid spawn=%d", got)
	}
	if got := endpoint.closeCalls.Load(); got != 0 {
		t.Fatalf("first valid spawn closed endpoint %d times", got)
	}
	app.drainServerMessages(1)
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("protocol close count=%d", got)
	}
	if got := cancelCount(); got != 0 {
		t.Fatalf("server cancel count=%d", got)
	}
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("roster after protocol close=%d", got)
	}
}

// Mutation killed: observing a transport close without invoking the session
// cleanup leaves stale remote players visible in the disconnected world.
func TestRemoteConnectionCloseResetsRoster(t *testing.T) {
	app, serverEndpoint, endpoint, cancelCount := newRemoteProtocolApplication(t)
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	if err := serverEndpoint.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for app.receiver.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, err := app.frame(0, 0, 0); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("frame after disconnect error=%v want network.ErrClosed", err)
	}
	if got := len(app.remotePlayers.Presentations()); got != 0 {
		t.Fatalf("roster after disconnect=%d", got)
	}
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("disconnect endpoint Close calls=%d", got)
	}
	if got := cancelCount(); got != 0 {
		t.Fatalf("disconnect server cancel calls=%d", got)
	}
}

// Mutation killed: Advance before drain, a missing Advance, or two Advances
// produces position 8, 8, or 4 instead of the hand-derived midpoint 2.
func TestFrameAdvancesRemotePlayersOnceAfterDrain(t *testing.T) {
	app, serverEndpoint, _, _ := newRemoteProtocolApplication(t)
	spawn := remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{0, 64, 0})
	if err := app.remotePlayers.Apply(spawn); err != nil {
		t.Fatal(err)
	}
	if err := app.remotePlayers.Apply(network.RemotePlayerStates{ServerTick: 2, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{4, 64, 0},
	}}}); err != nil {
		t.Fatal(err)
	}
	sendInteractiveServerMessage(t, serverEndpoint, network.RemotePlayerStates{ServerTick: 3, Players: []network.RemotePlayerState{{
		PlayerID: spawn.PlayerID, Dimension: core.Overworld, Position: mgl32.Vec3{8, 64, 0},
	}}})
	rendered, err := app.frame(1, 1, 25*time.Millisecond)
	if err != nil || rendered {
		t.Fatalf("frame=(%v,%v), want (false,nil) for zero framebuffer", rendered, err)
	}
	if got := app.remotePlayers.Presentations()[0].Position; got != (mgl32.Vec3{2, 64, 0}) {
		t.Fatalf("advanced position=%v want [2 64 0]", got)
	}
}

func TestFrameKeepsMesherWorkBoundIndependentFromMessageDrain(t *testing.T) {
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{})
	sections := make([]network.SectionData, core.SectionsPerChunk)
	for index := range sections {
		sections[index] = network.SectionData{
			Y: int32(index), Storage: network.SectionSingle, Single: core.AirID,
		}
	}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			if _, err := app.mirror.Apply(network.ChunkSnapshot{
				Dimension: core.Overworld,
				Chunk:     core.ChunkPos{X: x, Z: z},
				Revision:  1,
				Sections:  sections,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	first := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{Y: 0}}
	second := core.SectionKey{Dimension: core.Overworld, Pos: core.SectionPos{Y: 1}}
	release := app.mesher.BlockForTest(first)
	t.Cleanup(release)
	app.mesher.MarkDirty(first, second)

	rendered, err := app.frame(4096, 1, 0)
	if err != nil || !rendered {
		t.Fatalf("frame=(%v,%v)", rendered, err)
	}
	stats := app.mesher.Stats()
	if scheduled := stats.QueuedJobs + stats.InFlightJobs; scheduled != 1 {
		t.Fatalf("drain=4096 mesh=1 scheduled=%d stats=%+v", scheduled, stats)
	}
}

// Mutation killed: dropping identity/motion/name fields, sorting by input
// order, or anchoring at the feet changes these literal render values.
func TestRemotePresentationConversionPreservesSortedRenderData(t *testing.T) {
	presentations := []client.RemotePresentation{
		{PlayerID: integrationPlayerID(2), DisplayName: "乙", Position: mgl32.Vec3{8, 9, 10}, Yaw: 0.8, Pitch: -0.3},
		{PlayerID: integrationPlayerID(1), DisplayName: "甲", Position: mgl32.Vec3{1, 2, 3}, Yaw: -0.4, Pitch: 0.2},
	}
	avatars, tags := remoteRenderPresentations(presentations)
	wantAvatars := []render.Avatar{
		{PlayerID: integrationPlayerID(1), Position: mgl32.Vec3{1, 2, 3}, Yaw: -0.4, Pitch: 0.2},
		{PlayerID: integrationPlayerID(2), Position: mgl32.Vec3{8, 9, 10}, Yaw: 0.8, Pitch: -0.3},
	}
	wantTags := []render.NameTag{
		{PlayerID: integrationPlayerID(1), Text: "甲", Anchor: mgl32.Vec3{1, 4.05, 3}},
		{PlayerID: integrationPlayerID(2), Text: "乙", Anchor: mgl32.Vec3{8, 11.05, 10}},
	}
	if !reflect.DeepEqual(avatars, wantAvatars) || !reflect.DeepEqual(tags, wantTags) {
		t.Fatalf("converted avatars/tags=%+v/%+v want=%+v/%+v", avatars, tags, wantAvatars, wantTags)
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
	hotbar := render.NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, atlas, reg)
	color := dev.CreateTexture(gfx.TextureDesc{Label: "main color", Width: 4, Height: 4, Format: gfx.FormatRGBA8Unorm, Usage: gfx.TextureUsageRenderTarget})
	app := &application{
		dev: dev, color: color, colorView: color.View(gfx.TextureViewDesc{}),
		depth: newDepthTarget(dev, 4, 4), renderer: terrain,
		glyphAtlas: atlas, avatarRenderer: avatar, nameTagRenderer: nameTag,
		hotbarRenderer: hotbar,
		remotePlayers:  client.NewRemotePlayers(),
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
		"hotbar resources", "name-tag resources", "glyph-atlas texture", "avatar resources", "terrain resources",
		"main depth texture", "main color view", "main color texture", "device",
	})
	want := []string{"hotbar resources", "name-tag resources", "glyph-atlas texture", "avatar resources", "terrain resources", "main depth texture", "main color view", "main color texture", "device"}
	if !reflect.DeepEqual(markers, want) {
		t.Fatalf("release markers=%v want=%v; all=%v", markers, want, dev.releases)
	}
	for _, marker := range want {
		if count := dev.releaseCount(marker); count != 1 {
			t.Fatalf("release %q count=%d", marker, count)
		}
	}
}

// Mutation killed: constructing name tags before avatars, or cleaning a failed
// name-tag construction in forward order, moves the avatar/atlas release markers.
func TestApplicationConstructionFailureReleasesRemoteResourcesInReverse(t *testing.T) {
	wantErr := errors.New("injected name-tag construction failure")
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
	dependencies.newNameTagRenderer = func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat, render.GlyphSource) (*render.NameTagRenderer, error) {
		constructionOrder = append(constructionOrder, "name-tag")
		return nil, wantErr
	}
	app, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if app != nil || !errors.Is(err, wantErr) {
		t.Fatalf("construction result=(%v,%v), want nil/wrapped failure", app, err)
	}
	if want := []string{"atlas", "avatar", "name-tag"}; !reflect.DeepEqual(constructionOrder, want) {
		t.Fatalf("remote construction order=%v want=%v", constructionOrder, want)
	}
	markers := dev.releaseMarkers([]string{"avatar resources", "glyph-atlas texture", "terrain resources", "main depth texture", "device"})
	wantMarkers := []string{"avatar resources", "glyph-atlas texture", "terrain resources", "main depth texture", "device"}
	if !reflect.DeepEqual(markers, wantMarkers) {
		t.Fatalf("failure release markers=%v want=%v; all=%v", markers, wantMarkers, dev.releases)
	}
	if endpoint.closeCalls.Load() != 1 || window.closeCalls.Load() != 1 || surface.releaseCalls.Load() != 1 {
		t.Fatalf("failure ownership endpoint/window/surface=%d/%d/%d want 1/1/1",
			endpoint.closeCalls.Load(), window.closeCalls.Load(), surface.releaseCalls.Load())
	}
}

func newRemoteProtocolApplication(t *testing.T) (*application, network.ServerEndpoint, *connectionTestEndpoint, func() int) {
	t.Helper()
	rawClient, serverEndpoint := network.NewMemoryPair(16)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawClient}
	cancelCalls := 0
	app := &application{
		clientEndpoint: endpoint, receiver: client.NewReceiver(endpoint, 16),
		mirror: client.NewMirror(), predictor: client.NewPredictor(),
		remotePlayers: client.NewRemotePlayers(), serverCancel: func() { cancelCalls++ },
	}
	t.Cleanup(func() { app.closeClientSession(nil); _ = serverEndpoint.Close() })
	return app, serverEndpoint, endpoint, func() int { return cancelCalls }
}

func remoteSpawn(id byte, name string, tick uint64, position mgl32.Vec3) network.RemotePlayerSpawn {
	return network.RemotePlayerSpawn{PlayerID: integrationPlayerID(id), DisplayName: name, ServerTick: tick, Dimension: core.Overworld, Position: position}
}

func integrationPlayerID(last byte) core.PlayerID {
	return core.PlayerID{0: 0x12, 6: 0x40, 8: 0x80, 15: last}
}

type integrationGlyphSource struct {
	flushErr   error
	lastBudget *render.UploadBudget
}

func (*integrationGlyphSource) Request(string) {}
func (atlas *integrationGlyphSource) FlushUploads(budget *render.UploadBudget) error {
	atlas.lastBudget = budget
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
		hotbarRenderer:  render.NewHotbarRenderer(dev, gfx.FormatRGBA8Unorm, glyphs, reg),
		itemDropRenderer: render.NewItemDropRenderer(
			dev, gfx.FormatRGBA8Unorm, gfx.FormatDepth32Float,
		),
		itemDrops:     client.NewItemDrops(),
		remotePlayers: client.NewRemotePlayers(), mirror: client.NewMirror(), predictor: client.NewPredictor(),
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

func TestBenchmarkTCPDialFailureClosesListenerBeforeWaitingForAccept(t *testing.T) {
	dialErr := errors.New("injected benchmark dial failure")
	listener := newBenchmarkDialFailureListener()
	dial := func(context.Context, string) (network.ClientPacketStream, error) {
		<-listener.accepted
		return nil, dialErr
	}
	type result struct {
		endpoint network.ClientEndpoint
		err      error
	}
	returned := make(chan result, 1)
	go func() {
		endpoint, err := assembleBenchmarkObserverConnection(
			context.Background(), nil, "tcp",
			func(string) (network.Listener, error) { return listener, nil },
			dial,
		)
		returned <- result{endpoint: endpoint, err: err}
	}()

	select {
	case got := <-returned:
		if got.endpoint != nil || !errors.Is(got.err, dialErr) {
			t.Fatalf("dial failure result = (%T, %v), want (nil, dial error)", got.endpoint, got.err)
		}
	case <-time.After(500 * time.Millisecond):
		_ = listener.Close()
		<-returned
		t.Fatal("benchmark TCP dial failure waited on Accept before closing listener")
	}
	if got := listener.closeCalls.Load(); got != 1 {
		t.Fatalf("listener Close calls = %d, want 1", got)
	}
	select {
	case <-listener.acceptDone:
	default:
		t.Fatal("benchmark TCP dial failure returned before Accept goroutine exited")
	}
}

type benchmarkDialFailureListener struct {
	accepted   chan struct{}
	closed     chan struct{}
	acceptDone chan struct{}
	closeOnce  sync.Once
	closeCalls atomic.Int32
}

func newBenchmarkDialFailureListener() *benchmarkDialFailureListener {
	return &benchmarkDialFailureListener{
		accepted:   make(chan struct{}),
		closed:     make(chan struct{}),
		acceptDone: make(chan struct{}),
	}
}

func (listener *benchmarkDialFailureListener) Accept(ctx context.Context) (network.ServerPacketStream, error) {
	close(listener.accepted)
	defer close(listener.acceptDone)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-listener.closed:
		return nil, network.ErrClosed
	}
}

func (*benchmarkDialFailureListener) Addr() string { return "benchmark.invalid:1" }

func (listener *benchmarkDialFailureListener) Close() error {
	listener.closeOnce.Do(func() {
		listener.closeCalls.Add(1)
		close(listener.closed)
	})
	return nil
}

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
	want := render.MiningOverlay{
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
	if app.miningOverlay != (render.MiningOverlay{}) {
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
	want := render.MiningOverlay{
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
			if app.miningOverlay != (render.MiningOverlay{}) {
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

func TestApplicationConnectionRemoteAssemblyNeverOpensLocalStore(t *testing.T) {
	dialErr := errors.New("dial failed")
	storeCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		storeCalls++
		return nil, errors.New("remote opened local store")
	}
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return nil, dialErr
	}

	_, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if !errors.Is(err, dialErr) {
		t.Fatalf("newApplication error=%v, want dial failure", err)
	}
	if storeCalls != 0 {
		t.Fatalf("remote openStore calls=%d, want 0", storeCalls)
	}
}

func TestApplicationConnectionLocalNeverDialsTCP(t *testing.T) {
	openErr := errors.New("open local store failed")
	dialCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return nil, openErr
	}
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		dialCalls++
		return nil, errors.New("local dialed TCP")
	}

	_, err := newApplicationWithDependencies(localConnectionOptions(), dependencies)
	if !errors.Is(err, openErr) {
		t.Fatalf("newApplication error=%v, want local store failure", err)
	}
	if dialCalls != 0 {
		t.Fatalf("local DialTCP calls=%d, want 0", dialCalls)
	}
}

func TestApplicationConnectionRemoteDialFailurePrecedesWindow(t *testing.T) {
	dialErr := errors.New("dial failed")
	windowCalls := 0
	loginCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return nil, dialErr
	}
	dependencies.loginClient = func(
		context.Context, network.ClientPacketStream, network.Identity,
	) (network.ClientEndpoint, error) {
		loginCalls++
		return nil, errors.New("login called after dial failure")
	}
	dependencies.newWindow = connectionTestWindowFactory(&windowCalls)

	_, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if !errors.Is(err, dialErr) {
		t.Fatalf("newApplication error=%v, want dial failure", err)
	}
	if loginCalls != 0 || windowCalls != 0 {
		t.Fatalf("after dial failure login calls=%d window calls=%d, want 0/0", loginCalls, windowCalls)
	}
}

func TestApplicationConnectionRemoteLoginFailureClosesStreamBeforeWindow(t *testing.T) {
	loginErr := errors.New("login failed")
	stream := &connectionTestClientStream{}
	windowCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return stream, nil
	}
	dependencies.loginClient = func(
		context.Context, network.ClientPacketStream, network.Identity,
	) (network.ClientEndpoint, error) {
		return nil, loginErr
	}
	dependencies.newWindow = connectionTestWindowFactory(&windowCalls)

	_, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if !errors.Is(err, loginErr) {
		t.Fatalf("newApplication error=%v, want login failure", err)
	}
	if got := stream.closeCalls.Load(); got != 1 {
		t.Fatalf("remote stream Close calls=%d, want 1", got)
	}
	if windowCalls != 0 {
		t.Fatalf("remote login failure window calls=%d, want 0", windowCalls)
	}
}

func TestApplicationConnectionRemoteLoginSuccessReturnsOwnedApplicationAfterGraphics(t *testing.T) {
	rawEndpoint, _ := network.NewMemoryPair(1)
	endpoint := &connectionTestEndpoint{ClientEndpoint: rawEndpoint}
	t.Cleanup(func() { _ = rawEndpoint.Close() })
	stream := &connectionTestClientStream{}
	window := &connectionTestWindow{}
	surface := &connectionTestSurface{}
	loginComplete := false
	windowCalls := 0
	windowTitle := ""
	deviceCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.dialTCP = func(context.Context, string) (network.ClientPacketStream, error) {
		return stream, nil
	}
	dependencies.loginClient = func(
		_ context.Context,
		got network.ClientPacketStream,
		_ network.Identity,
	) (network.ClientEndpoint, error) {
		if got != stream {
			t.Fatalf("LoginClient stream=%T, want dialed stream", got)
		}
		loginComplete = true
		return endpoint, nil
	}
	dependencies.newWindow = func(_, _ int, title string) (applicationWindow, error) {
		windowCalls++
		windowTitle = title
		if !loginComplete {
			t.Fatal("window created before remote login completed")
		}
		return window, nil
	}
	dependencies.newDevice = func(
		gfx.NativeWindowHandle,
		uint32,
		uint32,
	) (gfx.Device, gfx.Surface, error) {
		deviceCalls++
		if !loginComplete || windowCalls != 1 {
			t.Fatal("device created before remote login and window")
		}
		device, err := gfx.NewHeadlessDevice()
		return device, surface, err
	}

	app, err := newApplicationWithDependencies(remoteConnectionOptions(), dependencies)
	if err != nil {
		t.Fatalf("newApplication remote success: %v", err)
	}
	if app == nil {
		t.Fatal("remote success returned nil application")
	}
	if app.clientEndpoint != endpoint || app.receiver == nil {
		t.Fatalf("remote application ownership endpoint=%T receiver=%p", app.clientEndpoint, app.receiver)
	}
	if app.host != nil || app.serverCancel != nil || app.serverDone != nil {
		t.Fatalf("remote application acquired local Host lifecycle: host=%v cancel=%v done=%v", app.host, app.serverCancel, app.serverDone)
	}
	if windowCalls != 1 || deviceCalls != 1 {
		t.Fatalf("remote success graphics calls window=%d device=%d, want 1/1", windowCalls, deviceCalls)
	}
	if windowTitle != "minecraft-go — M3C multiplayer world" {
		t.Fatalf("interactive window title = %q, want M3C title", windowTitle)
	}
	if got := endpoint.closeCalls.Load(); got != 0 {
		t.Fatalf("live remote application endpoint Close calls=%d, want 0", got)
	}
	if err := app.Close(); err != nil {
		t.Fatalf("remote application Close: %v", err)
	}
	if got := endpoint.closeCalls.Load(); got != 1 {
		t.Fatalf("remote application endpoint Close calls=%d, want 1", got)
	}
	if got := window.closeCalls.Load(); got != 1 {
		t.Fatalf("remote application window Close calls=%d, want 1", got)
	}
	if got := surface.releaseCalls.Load(); got != 1 {
		t.Fatalf("remote application surface Release calls=%d, want 1", got)
	}
}

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

// stubApplicationHost 是 TestApplicationRemoteReflectsHostAndServerPresence
// 用的最小 applicationHost 实现：三个方法都不会被调用，只用来在测试里制造
// 一个非 nil 的 host 值。
type stubApplicationHost struct{}

func (stubApplicationHost) Run(context.Context, network.Listener) error { return nil }
func (stubApplicationHost) AcceptStream(context.Context, network.ServerPacketStream) error {
	return nil
}
func (stubApplicationHost) Shutdown(context.Context) error { return nil }

// TestApplicationRemoteReflectsHostAndServerPresence 锁住 a.remote() 的判定：
// 它决定面板 physics/sim 组能不能写，取反会让单机变只读、真联机反而能写权威
// 参数，这条谓词必须有测试守着，不能只靠代码走查。
func TestApplicationRemoteReflectsHostAndServerPresence(t *testing.T) {
	tests := []struct {
		name string
		app  *application
		want bool
	}{
		{name: "本地内嵌 Host（单机）", app: &application{host: stubApplicationHost{}}, want: false},
		{name: "benchmark 内嵌可信 server", app: &application{server: &server.Server{}}, want: false},
		{name: "host 与 server 均为 nil（真远程联机）", app: &application{}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.app.remote(); got != test.want {
				t.Fatalf("remote() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestApplicationConnectionLocalHostFailureClosesStoreBeforeWindow(t *testing.T) {
	hostErr := errors.New("construct host failed")
	store := newConnectionTestStore(42)
	windowCalls := 0
	memoryCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return store, nil
	}
	dependencies.newHost = func(server.Config, server.Generator, storage.WorldStore) (applicationHost, error) {
		return nil, hostErr
	}
	dependencies.newMemoryStreamPair = func(int) (network.ClientPacketStream, network.ServerPacketStream, error) {
		memoryCalls++
		return nil, nil, errors.New("memory pair called after host failure")
	}
	dependencies.newWindow = connectionTestWindowFactory(&windowCalls)

	_, err := newApplicationWithDependencies(localConnectionOptions(), dependencies)
	if !errors.Is(err, hostErr) {
		t.Fatalf("newApplication error=%v, want host failure", err)
	}
	if got := store.closeCalls.Load(); got != 1 {
		t.Fatalf("local store Close calls=%d, want 1", got)
	}
	if memoryCalls != 0 || windowCalls != 0 {
		t.Fatalf("after host failure memory calls=%d window calls=%d, want 0/0", memoryCalls, windowCalls)
	}
}

func TestApplicationConnectionLocalMemoryFailureStopsHostAndClosesStoreBeforeWindow(t *testing.T) {
	memoryErr := errors.New("memory stream assembly failed")
	store := newConnectionTestStore(42)
	host := newConnectionTestHost(store)
	windowCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return store, nil
	}
	dependencies.newHost = func(server.Config, server.Generator, storage.WorldStore) (applicationHost, error) {
		return host, nil
	}
	dependencies.newMemoryStreamPair = func(int) (network.ClientPacketStream, network.ServerPacketStream, error) {
		return nil, nil, memoryErr
	}
	dependencies.newWindow = connectionTestWindowFactory(&windowCalls)

	_, err := newApplicationWithDependencies(localConnectionOptions(), dependencies)
	if !errors.Is(err, memoryErr) {
		t.Fatalf("newApplication error=%v, want memory assembly failure", err)
	}
	if got := host.shutdownCalls.Load(); got != 1 {
		t.Fatalf("local host Shutdown calls=%d, want 1", got)
	}
	if got := store.closeCalls.Load(); got != 1 {
		t.Fatalf("local store Close calls=%d, want 1", got)
	}
	if windowCalls != 0 {
		t.Fatalf("local memory failure window calls=%d, want 0", windowCalls)
	}
}

func TestApplicationConnectionLocalLoginFailureCleansStreamsHostAndStoreBeforeWindow(t *testing.T) {
	loginErr := errors.New("local login failed")
	store := newConnectionTestStore(42)
	host := newConnectionTestHost(store)
	clientStream := &connectionTestClientStream{}
	serverStream := &connectionTestServerStream{}
	windowCalls := 0
	dependencies := connectionTestDependencies(t)
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return store, nil
	}
	dependencies.newHost = func(server.Config, server.Generator, storage.WorldStore) (applicationHost, error) {
		return host, nil
	}
	dependencies.newMemoryStreamPair = func(int) (network.ClientPacketStream, network.ServerPacketStream, error) {
		return clientStream, serverStream, nil
	}
	dependencies.loginClient = func(
		context.Context, network.ClientPacketStream, network.Identity,
	) (network.ClientEndpoint, error) {
		return nil, loginErr
	}
	dependencies.newWindow = connectionTestWindowFactory(&windowCalls)

	_, err := newApplicationWithDependencies(localConnectionOptions(), dependencies)
	if !errors.Is(err, loginErr) {
		t.Fatalf("newApplication error=%v, want local login failure", err)
	}
	if got := clientStream.closeCalls.Load(); got != 1 {
		t.Errorf("local client stream Close calls=%d, want 1", got)
	}
	if got := serverStream.closeCalls.Load(); got != 1 {
		t.Errorf("local server stream Close calls=%d, want 1", got)
	}
	if got := host.shutdownCalls.Load(); got != 1 {
		t.Errorf("local host Shutdown calls=%d, want 1", got)
	}
	if got := store.closeCalls.Load(); got != 1 {
		t.Errorf("local store Close calls=%d, want 1", got)
	}
	if windowCalls != 0 {
		t.Errorf("local login failure window calls=%d, want 0", windowCalls)
	}
}

func TestApplicationConnectionLocalAttachmentFailureCleansOwnedResourcesBeforeWindow(t *testing.T) {
	attachmentErr := errors.New("local attachment failed")
	store := newConnectionTestStore(42)
	store.loadPlayerErr = attachmentErr
	windowCalls := 0
	dependencies := defaultApplicationDependencies()
	dependencies.openStore = func(context.Context, applicationOptions) (storage.WorldStore, error) {
		return store, nil
	}
	dependencies.newWindow = connectionTestWindowFactory(&windowCalls)

	_, err := newApplicationWithDependencies(localConnectionOptions(), dependencies)
	if err == nil {
		t.Fatal("newApplication accepted local attachment failure")
	}
	if got := store.closeCalls.Load(); got != 1 {
		t.Fatalf("local attachment failure store Close calls=%d, want 1", got)
	}
	if windowCalls != 0 {
		t.Fatalf("local attachment failure window calls=%d, want 0", windowCalls)
	}
}

func connectionTestDependencies(t *testing.T) applicationDependencies {
	t.Helper()
	unexpected := func(name string) {
		t.Helper()
		t.Fatalf("unexpected application dependency call: %s", name)
	}
	return applicationDependencies{
		openStore: func(context.Context, applicationOptions) (storage.WorldStore, error) {
			unexpected("openStore")
			return nil, nil
		},
		dialTCP: func(context.Context, string) (network.ClientPacketStream, error) {
			unexpected("dialTCP")
			return nil, nil
		},
		loginClient: func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, error) {
			unexpected("loginClient")
			return nil, nil
		},
		newHost: func(server.Config, server.Generator, storage.WorldStore) (applicationHost, error) {
			unexpected("newHost")
			return nil, nil
		},
		newMemoryStreamPair: func(int) (network.ClientPacketStream, network.ServerPacketStream, error) {
			unexpected("newMemoryStreamPair")
			return nil, nil, nil
		},
		newWindow: connectionTestWindowFactory(new(int)),
	}
}

func connectionTestWindowFactory(calls *int) func(int, int, string) (applicationWindow, error) {
	return func(int, int, string) (applicationWindow, error) {
		*calls++
		return nil, errors.New("unexpected window creation")
	}
}

func remoteConnectionOptions() applicationOptions {
	identity := connectionTestIdentity()
	return applicationOptions{
		Connect: "example.invalid:25565", Identity: &identity, Render: config.Defaults().Render,
	}
}

func localConnectionOptions() applicationOptions {
	identity := connectionTestIdentity()
	return applicationOptions{
		Seed: 42, WorldPath: "unused", Identity: &identity, Render: config.Defaults().Render,
	}
}

func connectionTestIdentity() network.Identity {
	return network.Identity{
		PlayerID:    core.PlayerID{6: 0x40, 8: 0x80, 15: 1},
		DisplayName: "Test Player",
	}
}

type connectionTestStore struct {
	*storage.MemoryStore
	loadPlayerErr error
	closeCalls    atomic.Int32
}

func newConnectionTestStore(seed int64) *connectionTestStore {
	return &connectionTestStore{MemoryStore: storage.NewMemory(storage.Metadata{
		FormatVersion: 2,
		Seed:          seed,
	})}
}

func (store *connectionTestStore) LoadPlayer(
	ctx context.Context,
	id core.PlayerID,
) (storage.StoredPlayer, error) {
	if store.loadPlayerErr != nil {
		return storage.StoredPlayer{}, store.loadPlayerErr
	}
	return store.MemoryStore.LoadPlayer(ctx, id)
}

func (store *connectionTestStore) Close() error {
	store.closeCalls.Add(1)
	return store.MemoryStore.Close()
}

type connectionTestHost struct {
	store         storage.WorldStore
	shutdownCalls atomic.Int32
}

func newConnectionTestHost(store storage.WorldStore) *connectionTestHost {
	return &connectionTestHost{store: store}
}

func (host *connectionTestHost) Run(ctx context.Context, _ network.Listener) error {
	<-ctx.Done()
	return ctx.Err()
}

func (*connectionTestHost) AcceptStream(ctx context.Context, _ network.ServerPacketStream) error {
	<-ctx.Done()
	return ctx.Err()
}

func (host *connectionTestHost) Shutdown(context.Context) error {
	host.shutdownCalls.Add(1)
	return host.store.Close()
}

type connectionTestClientStream struct{ closeCalls atomic.Int32 }

func (*connectionTestClientStream) Send(context.Context, network.State, network.ClientPacket) error {
	return nil
}
func (*connectionTestClientStream) Recv(context.Context, network.State) (network.ServerPacket, error) {
	return nil, network.ErrClosed
}
func (stream *connectionTestClientStream) Close() error {
	stream.closeCalls.Add(1)
	return nil
}

type connectionTestEndpoint struct {
	network.ClientEndpoint
	closeCalls atomic.Int32
}

func (endpoint *connectionTestEndpoint) Close() error {
	endpoint.closeCalls.Add(1)
	return endpoint.ClientEndpoint.Close()
}

type connectionTestWindow struct {
	fakeInteractiveWindow
	closeCalls atomic.Int32
}

func (*connectionTestWindow) NativeHandle() gfx.NativeWindowHandle {
	return gfx.NativeWindowHandle{}
}

func (window *connectionTestWindow) Close() {
	window.closeCalls.Add(1)
}

type connectionTestSurface struct{ releaseCalls atomic.Int32 }

func (*connectionTestSurface) Acquire() gfx.TextureView             { return nil }
func (*connectionTestSurface) Present()                             {}
func (*connectionTestSurface) SetPresentMode(gfx.PresentMode) error { return nil }
func (*connectionTestSurface) Resize(uint32, uint32)                {}
func (*connectionTestSurface) Format() gfx.TextureFormat            { return gfx.FormatBGRA8UnormSrgb }
func (surface *connectionTestSurface) Release() {
	surface.releaseCalls.Add(1)
}

type connectionTestServerStream struct{ closeCalls atomic.Int32 }

func (*connectionTestServerStream) Send(context.Context, network.State, network.ServerPacket) error {
	return nil
}
func (*connectionTestServerStream) Recv(context.Context, network.State) (network.ClientPacket, error) {
	return nil, network.ErrClosed
}
func (*connectionTestServerStream) Peer() string { return "test" }
func (stream *connectionTestServerStream) Close() error {
	stream.closeCalls.Add(1)
	return nil
}

func TestApplicationCloseReturnsPersistenceErrorAndReleasesOnce(t *testing.T) {
	persistenceErr := errors.New("持久化刷盘失败")
	serverDone := make(chan error, 1)
	serverDone <- persistenceErr

	cancelCalls := 0
	releaseCalls := 0
	app := &application{
		serverCancel: func() { cancelCalls++ },
		serverDone:   serverDone,
		releaseResources: func() {
			releaseCalls++
		},
	}

	first := app.Close()
	second := app.Close()
	if !errors.Is(first, persistenceErr) {
		t.Fatalf("Close error=%v，想要包含 %v", first, persistenceErr)
	}
	if first != second {
		t.Fatalf("第二次 Close error=%v，不是缓存的第一次结果 %v", second, first)
	}
	if cancelCalls != 1 || releaseCalls != 1 {
		t.Fatalf("Close 调用次数 cancel=%d release=%d，想要各 1 次", cancelCalls, releaseCalls)
	}
}

func TestApplicationCloseCancelsBeforeWaitingAndSharesSuccessfulResult(t *testing.T) {
	serverDone := make(chan error)
	cancelObserved := make(chan struct{}, 1)
	releaseObserved := make(chan struct{}, 1)
	cancelCalls := 0
	releaseCalls := 0
	app := &application{
		serverCancel: func() {
			cancelCalls++
			cancelObserved <- struct{}{}
		},
		serverDone: serverDone,
		releaseResources: func() {
			releaseCalls++
			releaseObserved <- struct{}{}
		},
	}

	const callers = 8
	start := make(chan struct{})
	results := make(chan error, callers)
	var callersDone sync.WaitGroup
	callersDone.Add(callers)
	for range callers {
		go func() {
			defer callersDone.Done()
			<-start
			results <- app.Close()
		}()
	}
	close(start)

	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("Close 未在等待 serverDone 前调用 serverCancel")
	}
	select {
	case err := <-results:
		t.Fatalf("serverDone 前 Close 已返回: %v", err)
	case <-releaseObserved:
		t.Fatal("serverDone 前已释放资源")
	default:
	}
	select {
	case serverDone <- context.Canceled:
	case <-time.After(time.Second):
		t.Fatal("Close 未等待 serverDone")
	}

	callersDone.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("concurrent Close error=%v，plain context.Canceled 应为 nil", err)
		}
	}
	if err := app.Close(); err != nil {
		t.Fatalf("repeated Close error=%v", err)
	}
	if cancelCalls != 1 || releaseCalls != 1 {
		t.Fatalf("Close 调用次数 cancel=%d release=%d，想要各 1 次", cancelCalls, releaseCalls)
	}
}

func TestRunInteractiveReturnsReceiverDisconnectWithoutRendering(t *testing.T) {
	endpoint, _ := network.NewMemoryPair(1)
	receiver := client.NewReceiver(endpoint, 1)
	if err := endpoint.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for receiver.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	app := &application{
		window:    &fakeInteractiveWindow{},
		receiver:  receiver,
		mirror:    client.NewMirror(),
		predictor: client.NewPredictor(),
	}
	if err := runInteractive(app); !errors.Is(err, network.ErrClosed) {
		t.Fatalf("runInteractive error=%v, want network.ErrClosed", err)
	}
}

type fakeInteractiveWindow struct {
	captured bool
}

func (window *fakeInteractiveWindow) SetCursorCaptured(captured bool) {
	window.captured = captured
}
func (*fakeInteractiveWindow) CursorPos() (float64, float64) { return 0, 0 }
func (*fakeInteractiveWindow) ShouldClose() bool             { return false }
func (*fakeInteractiveWindow) Poll()                         {}
func (*fakeInteractiveWindow) KeyDown(client.Key) bool       { return false }
func (*fakeInteractiveWindow) PrimaryButtonDown() bool       { return false }
func (*fakeInteractiveWindow) SecondaryButtonDown() bool     { return false }
func (window *fakeInteractiveWindow) CursorCaptured() bool   { return window.captured }
func (*fakeInteractiveWindow) FramebufferSize() (int, int)   { return 1, 1 }
func (*fakeInteractiveWindow) ContentSize() (int, int)       { return 1, 1 }
func (*fakeInteractiveWindow) SetContentSize(int, int)       {}
func (*fakeInteractiveWindow) CancelClose()                  {}
func (*fakeInteractiveWindow) NativeHandle() gfx.NativeWindowHandle {
	return gfx.NativeWindowHandle{}
}
func (*fakeInteractiveWindow) Close() {}

func newInteractiveTestApplication(
	t *testing.T,
) (*application, network.ServerEndpoint) {
	t.Helper()
	clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
	t.Cleanup(func() { _ = clientEndpoint.Close() })
	return &application{
		clientEndpoint:  clientEndpoint,
		receiver:        client.NewReceiver(clientEndpoint, 8),
		mirror:          client.NewMirror(),
		itemDrops:       client.NewItemDrops(),
		inventorySource: -1,
		predictor:       client.NewPredictor(),
		serverCancel:    func() {},
	}, serverEndpoint
}

func TestApplicationCelestialParametersKeepLastAcceptedWorldTime(t *testing.T) {
	app, serverEndpoint := newInteractiveTestApplication(t)
	newest := network.PlayerState{
		ServerTick: 2, WorldTimeTicks: 6000, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
	}
	sendInteractiveServerMessage(t, serverEndpoint, newest)
	app.drainServerMessages(1)
	want := render.DayNightAt(newest.WorldTimeTicks)
	if got := render.DayNightAt(app.worldTimeTicks); got != want {
		t.Fatalf("接受新状态后的天体参数 = %+v，想要 %+v", got, want)
	}
	if got := want.SunDirection; math.Abs(float64(got[1]-1)) > 1e-5 || math.Abs(float64(got[0])) > 1e-5 || math.Abs(float64(got[2])) > 1e-5 {
		t.Fatalf("接受新状态后的太阳方向 = %v，想要正午天顶", got)
	}

	for _, stale := range []network.PlayerState{
		{ServerTick: 1, WorldTimeTicks: 18000, Dimension: core.Overworld, Position: newest.Position, OnGround: true, Ready: true},
		{ServerTick: 2, WorldTimeTicks: 12000, Dimension: core.Overworld, Position: newest.Position, OnGround: true, Ready: true},
	} {
		sendInteractiveServerMessage(t, serverEndpoint, stale)
		app.drainServerMessages(1)
		if app.worldTimeTicks != newest.WorldTimeTicks {
			t.Fatalf("旧或重复状态将世界时间改为 %d，想要 %d", app.worldTimeTicks, newest.WorldTimeTicks)
		}
		if got := render.DayNightAt(app.worldTimeTicks); got != want {
			t.Fatalf("旧或重复状态改变天体参数 = %+v，想要 %+v", got, want)
		}
	}
}

func TestApplicationCelestialParametersMatchMemoryAndTCP(t *testing.T) {
	state := network.PlayerState{
		ServerTick: 1, WorldTimeTicks: 18000, Dimension: core.Overworld,
		Position: mgl32.Vec3{0.5, 10, 0.5}, OnGround: true, Ready: true,
	}
	var memory render.DayNight
	for _, transport := range []string{"memory", "tcp"} {
		t.Run(transport, func(t *testing.T) {
			clientEndpoint, serverEndpoint := celestialTestEndpoints(t, transport)
			app := &application{
				clientEndpoint: clientEndpoint,
				receiver:       client.NewReceiver(clientEndpoint, 8),
				mirror:         client.NewMirror(),
				predictor:      client.NewPredictor(),
				serverCancel:   func() {},
			}
			sendInteractiveServerMessage(t, serverEndpoint, state)
			app.drainServerMessages(1)
			got := render.DayNightAt(app.worldTimeTicks)
			if math.Abs(float64(got.SunDirection[1]+1)) > 1e-5 || math.Abs(float64(got.SunDirection[0])) > 1e-5 || math.Abs(float64(got.SunDirection[2])) > 1e-5 {
				t.Fatalf("午夜太阳方向 = %v，想要地平线下方", got.SunDirection)
			}
			if transport == "memory" {
				memory = got
				return
			}
			if got != memory {
				t.Fatalf("TCP 天体参数 = %+v，想要与 Memory 相同的 %+v", got, memory)
			}
		})
	}
}

func celestialTestEndpoints(t *testing.T, transport string) (network.ClientEndpoint, network.ServerEndpoint) {
	t.Helper()
	if transport == "memory" {
		clientEndpoint, serverEndpoint := network.NewMemoryPair(8)
		t.Cleanup(func() {
			_ = clientEndpoint.Close()
			_ = serverEndpoint.Close()
		})
		return clientEndpoint, serverEndpoint
	}
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	dialed := make(chan struct {
		stream network.ClientPacketStream
		err    error
	}, 1)
	go func() {
		stream, err := network.DialTCP(context.Background(), listener.Addr())
		dialed <- struct {
			stream network.ClientPacketStream
			err    error
		}{stream, err}
	}()
	serverStream, err := listener.Accept(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct {
		endpoint network.ServerEndpoint
		err      error
	}, 1)
	go func() {
		pending, err := network.BeginServerLogin(context.Background(), serverStream)
		if err != nil {
			accepted <- struct {
				endpoint network.ServerEndpoint
				err      error
			}{err: err}
			return
		}
		var endpoint network.ServerEndpoint
		err = pending.Accept(context.Background(), func(attached network.ServerEndpoint) error {
			endpoint = attached
			return nil
		})
		accepted <- struct {
			endpoint network.ServerEndpoint
			err      error
		}{endpoint, err}
	}()
	clientStream := <-dialed
	if clientStream.err != nil {
		t.Fatal(clientStream.err)
	}
	clientEndpoint, err := network.LoginClient(context.Background(), clientStream.stream, network.Identity{
		PlayerID: integrationPlayerID(9), DisplayName: "Celestial",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	if server.err != nil {
		t.Fatal(server.err)
	}
	t.Cleanup(func() {
		_ = clientEndpoint.Close()
		_ = server.endpoint.Close()
	})
	return clientEndpoint, server.endpoint
}

func sendInteractiveServerMessage(
	t *testing.T,
	endpoint network.ServerEndpoint,
	message network.ServerMessage,
) {
	t.Helper()
	if err := endpoint.Send(context.Background(), message); err != nil {
		t.Fatalf("发送服务端消息: %v", err)
	}
	// The application intentionally drains a non-blocking Receiver; let its sole
	// blocking reader hand this test message to the inbox before the frame drains.
	time.Sleep(time.Millisecond)
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
	app.worldTimeTicks = 3000
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
			got, ok := render.ChestSlotAt(float64(x), float64(y), width, height)
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
			got, ok := render.InventorySlotAt(float64(x), float64(y), width, height)
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
			got, ok := render.FurnaceSlotAt(float64(x), float64(y), width, height)
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
			if got, ok := render.RecipeButtonAt(float64(x), float64(y), width, height); ok &&
				got == recipe {
				return float64(x), float64(y)
			}
		}
	}
	t.Fatalf("找不到 recipe %d 的按钮像素", recipe)
	return 0, 0
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

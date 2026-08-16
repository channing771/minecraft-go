//go:build darwin

package main

// rust-client-render-terrain 的双后端对照门禁:同一份 section quads、相机、
// 昼夜与可见列表分别驱动 Go Renderer 与 Rust 离屏渲染器,回读图像必须落在
// 既有 diffThreshold 双阈值内。生产路径与 golden 基线不动;本测试是 R2a
// "平行实现与 Go 一致"的判据。

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/gfx"
	"github.com/channing771/mornlea/internal/mesh"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
	"github.com/channing771/mornlea/internal/world"
	"github.com/channing771/mornlea/internal/worldgen"
)

// dualBackendScene 是一份装配好的对照场景:两个后端消费同一批数据。
type dualBackendScene struct {
	quads        map[core.SectionPos][]mesh.Quad
	connectivity map[core.SectionPos]mesh.Connectivity
}

// buildDualBackendScene 直接从 worldgen 生成 3×3 区块并逐 section 网格化,
// 与 oak grove capture 场景同种子同范围,但不经过完整 app 装配。
func buildDualBackendScene(t *testing.T, seed int64) *dualBackendScene {
	t.Helper()
	generator := worldgen.New(seed)
	chunks := map[core.ChunkPos]*world.Chunk{}
	for z := int32(-1); z <= 1; z++ {
		for x := int32(-1); x <= 1; x++ {
			pos := core.ChunkPos{X: x, Z: z}
			chunks[pos] = generator.GenerateChunk(pos)
		}
	}
	reg := assets.NewRegistry()
	light := mesh.NewLightScratch()
	scene := &dualBackendScene{
		quads:        map[core.SectionPos][]mesh.Quad{},
		connectivity: map[core.SectionPos]mesh.Connectivity{},
	}
	for chunkPos, chunk := range chunks {
		for sectionY := 0; sectionY < core.SectionsPerChunk; sectionY++ {
			neighborhood := &world.Neighborhood{
				Center:   chunk.Section(sectionY).Clone(),
				SectionY: sectionY,
			}
			for dz := int32(-1); dz <= 1; dz++ {
				for dx := int32(-1); dx <= 1; dx++ {
					neighbor, ok := chunks[core.ChunkPos{X: chunkPos.X + dx, Z: chunkPos.Z + dz}]
					if !ok {
						continue
					}
					neighborhood.Heights[dx+1][dz+1] = neighbor.Heights()
					neighborhood.HeightsPresent[dx+1][dz+1] = true
					for dy := -1; dy <= 1; dy++ {
						neighborY := sectionY + dy
						if neighborY < 0 || neighborY >= core.SectionsPerChunk {
							continue
						}
						if dx == 0 && dy == 0 && dz == 0 {
							neighborhood.Around[1][1][1] = neighborhood.Center
							continue
						}
						neighborhood.Around[dx+1][dy+1][dz+1] = neighbor.Section(neighborY).Clone()
					}
				}
			}
			sectionPos := core.SectionPos{X: chunkPos.X, Y: int32(sectionY), Z: chunkPos.Z}
			scene.quads[sectionPos] = mesh.MeshSection(neighborhood, reg, light)
			scene.connectivity[sectionPos] = mesh.ComputeConnectivity(neighborhood.Center, reg)
		}
	}
	return scene
}

// dualBackendCameraSection 复刻 render 包的 cameraSection:相机所在 section,
// Y 槽位钳制在合法范围。
func dualBackendCameraSection(pos mgl32.Vec3) core.SectionPos {
	block := core.BlockPos{
		X: int32(floor32(pos[0])),
		Y: int32(floor32(pos[1])),
		Z: int32(floor32(pos[2])),
	}
	y := int32(block.SectionIndex())
	if y < 0 {
		y = 0
	} else if y >= core.SectionsPerChunk {
		y = core.SectionsPerChunk - 1
	}
	return core.SectionPos{X: block.Chunk().X, Y: y, Z: block.Chunk().Z}
}

func floor32(v float32) float64 {
	f := float64(v)
	if f == float64(int64(f)) || f > 0 {
		return float64(int64(f))
	}
	return float64(int64(f) - 1)
}

// TestDualBackendTerrainParity 是 R2a 的核心门禁:oak grove 地形场景下
// Rust 离屏渲染器输出必须与 Go 渲染器落在 captureThresholds 阈值内。
func TestDualBackendTerrainParity(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("无 GPU 适配器: %v", err)
	}
	defer dev.Release()
	rust, err := client.NewRenderer(captureWidth, captureHeight)
	if errors.Is(err, client.ErrNoGPUAdapter) {
		t.Skip("Rust 侧无 GPU 适配器")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rust.Close()

	reg := assets.NewRegistry()
	goRenderer := render.New(dev, reg, gfx.FormatBGRA8UnormSrgb)
	defer goRenderer.Release()
	goRenderer.Resize(captureWidth, captureHeight)
	layers, pixels := reg.AtlasPixels()
	rust.UploadAtlas(layers, pixels)

	scene := buildDualBackendScene(t, captureOakGroveSeed)
	for pos, quads := range scene.quads {
		goRenderer.SetConnectivity(pos, scene.connectivity[pos])
		if len(quads) == 0 {
			continue
		}
		goRenderer.QueueSection(pos, quads)
		packed := make([]byte, 0, len(quads)*8)
		for _, q := range quads {
			var buf [8]byte
			value := q.Pack()
			for i := 0; i < 8; i++ {
				buf[i] = byte(value >> (8 * i))
			}
			packed = append(packed, buf[:]...)
		}
		rust.UploadSection(pos.X, pos.Y, pos.Z, packed)
	}
	// 预算按帧重置;循环冲刷直到 pending 清空,保证两个后端持有同一份数据。
	center := core.ChunkPos{}
	for goRenderer.PendingUploads() > 0 {
		goRenderer.BeginFrame()
		goRenderer.FlushUploads(center)
	}

	// 相机与昼夜:oak grove capture 的取景 + 固定正午时间,两个后端同值。
	camera := client.Camera{
		Pos:    mgl32.Vec3{-3.5, 75.5, 12.5},
		Yaw:    0,
		Pitch:  -0.38,
		FovY:   mgl32.DegToRad(70),
		Aspect: float32(captureWidth) / float32(captureHeight),
		Near:   0.1,
		Far:    2000,
	}
	const worldTime = 6000
	dayNight := render.DayNightAt(worldTime)
	cloud := render.CloudOffsetAt(worldTime)
	viewProj := camera.ViewProj()
	viewProjInv := viewProj.Inv()
	goCamera := render.Camera{
		ViewProj:       viewProj,
		ViewProjInv:    viewProjInv,
		Pos:            camera.Pos,
		CloudOffset:    cloud,
		SunDirection:   dayNight.SunDirection,
		Daylight:       dayNight.Daylight,
		StarVisibility: dayNight.StarVisibility,
		SkyColor:       dayNight.ClearColor,
	}

	// 可见列表:与 Go Render 内部同一算法与顺序。
	var scratch mesh.VisibilityScratch
	visible := mesh.VisibleSectionsInto(
		nil, &scratch, dualBackendCameraSection(camera.Pos), 32,
		core.FrustumFrom(viewProj),
		func(p core.SectionPos) (mesh.Connectivity, bool) {
			c, ok := scene.connectivity[p]
			return c, ok
		})
	rustVisible := make([][3]int32, len(visible))
	for i, p := range visible {
		rustVisible[i] = [3]int32{p.X, p.Y, p.Z}
	}
	rustFrame := client.RenderFrame{
		ViewProj:       viewProj,
		ViewProjInv:    viewProjInv,
		Pos:            camera.Pos,
		Daylight:       dayNight.Daylight,
		SunDirection:   dayNight.SunDirection,
		StarVisibility: dayNight.StarVisibility,
		SkyColor:       dayNight.ClearColor,
		CloudMacroX:    cloud.MacroX,
		CloudLocal:     cloud.Local,
		Visible:        rustVisible,
	}

	// Go 侧离屏 target(与 capture 同格式)。
	color := dev.CreateTexture(gfx.TextureDesc{
		Label:     "dual backend color",
		Width:     captureWidth,
		Height:    captureHeight,
		Format:    gfx.FormatBGRA8UnormSrgb,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageCopySrc,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label:     "dual backend depth",
		Width:     captureWidth,
		Height:    captureHeight,
		Format:    gfx.FormatDepth32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{})
	defer depthView.Release()

	// 两帧渲染:第一帧建 HiZ,第二帧(相机不动)走 HiZ 启用路径——
	// 两个后端同样节奏。
	renderGoFrame := func() {
		encoder := dev.CreateCommandEncoder()
		goRenderer.Render(encoder, colorView, depthView, goCamera)
		cmd := encoder.Finish()
		dev.Submit(cmd)
		cmd.Release()
		dev.Poll(true)
	}
	renderGoFrame()
	renderGoFrame()
	framesBefore := rust.FrameCalls()
	uploadsBefore := rust.UploadCalls()
	rust.RenderFrame(rustFrame)
	rust.RenderFrame(rustFrame)
	if got := rust.FrameCalls() - framesBefore; got != 2 {
		t.Fatalf("两帧渲染触发 %d 次 render FFI,想要 2", got)
	}
	if got := rust.UploadCalls() - uploadsBefore; got != 0 {
		t.Fatalf("无 section 变化的帧触发 %d 次 upload FFI,想要 0", got)
	}

	goImage := bgraToNRGBA(color.ReadLayer(0, 0), captureWidth, captureHeight)
	rustImage := bgraToNRGBA(rust.Readback(), captureWidth, captureHeight)
	diff, _, err := compareImages(rustImage, goImage)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("双后端差异:%s", diff)
	if !diff.withinThreshold(captureThresholds) {
		t.Fatalf("Rust 渲染器与 Go 渲染器超出阈值:%s", diff)
	}
}

// TestDualBackendFullFrameParity 是 R2b 的核心门禁:地形 + avatar + 掉落物
// + 轮廓 + 名牌 + 伤害红边 + HUD + 调试面板的完整帧,Rust 渲染器输出必须
// 与 Go 渲染器落在 captureThresholds 阈值内。字形经 settle 收敛后整图同步。
func TestDualBackendFullFrameParity(t *testing.T) {
	dev, err := gfx.NewHeadlessDevice()
	if err != nil {
		t.Skipf("无 GPU 适配器: %v", err)
	}
	defer dev.Release()
	rust, err := client.NewRenderer(captureWidth, captureHeight)
	if errors.Is(err, client.ErrNoGPUAdapter) {
		t.Skip("Rust 侧无 GPU 适配器")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rust.Close()

	reg := assets.NewRegistry()
	goRenderer := render.New(dev, reg, gfx.FormatBGRA8UnormSrgb)
	defer goRenderer.Release()
	goRenderer.Resize(captureWidth, captureHeight)
	layers, pixels := reg.AtlasPixels()
	rust.UploadAtlas(layers, pixels)

	glyphAtlas, err := render.NewGlyphAtlas(dev)
	if err != nil {
		t.Fatal(err)
	}
	defer glyphAtlas.Release()
	avatarRenderer := render.NewAvatarRenderer(dev, gfx.FormatBGRA8UnormSrgb, gfx.FormatDepth32Float)
	defer avatarRenderer.Release()
	dropRenderer := render.NewItemDropRenderer(dev, gfx.FormatBGRA8UnormSrgb, gfx.FormatDepth32Float)
	defer dropRenderer.Release()
	outlineRenderer := render.NewBlockOutlineRenderer(dev, gfx.FormatBGRA8UnormSrgb, gfx.FormatDepth32Float)
	defer outlineRenderer.Release()
	nameTagRenderer := render.NewNameTagRenderer(dev, gfx.FormatBGRA8UnormSrgb, gfx.FormatDepth32Float, glyphAtlas)
	defer nameTagRenderer.Release()
	overlayRenderer := render.NewDamageOverlayRenderer(dev, gfx.FormatBGRA8UnormSrgb)
	defer overlayRenderer.Release()
	hotbarRenderer := hud.NewHotbarRenderer(dev, gfx.FormatBGRA8UnormSrgb, glyphAtlas, reg)
	defer hotbarRenderer.Release()
	panelRenderer := render.NewDebugPanelRenderer(dev, gfx.FormatBGRA8UnormSrgb, glyphAtlas)
	defer panelRenderer.Release()

	scene := buildDualBackendScene(t, captureOakGroveSeed)
	for pos, quads := range scene.quads {
		goRenderer.SetConnectivity(pos, scene.connectivity[pos])
		if len(quads) == 0 {
			continue
		}
		goRenderer.QueueSection(pos, quads)
		packed := make([]byte, 0, len(quads)*8)
		for _, q := range quads {
			var buf [8]byte
			value := q.Pack()
			for i := 0; i < 8; i++ {
				buf[i] = byte(value >> (8 * i))
			}
			packed = append(packed, buf[:]...)
		}
		rust.UploadSection(pos.X, pos.Y, pos.Z, packed)
	}
	for goRenderer.PendingUploads() > 0 {
		goRenderer.BeginFrame()
		goRenderer.FlushUploads(core.ChunkPos{})
	}

	camera := client.Camera{
		Pos:    mgl32.Vec3{-3.5, 75.5, 12.5},
		Yaw:    0,
		Pitch:  -0.38,
		FovY:   mgl32.DegToRad(70),
		Aspect: float32(captureWidth) / float32(captureHeight),
		Near:   0.1,
		Far:    2000,
	}
	const worldTime = 6000
	const serverTick = 12345
	dayNight := render.DayNightAt(worldTime)
	cloud := render.CloudOffsetAt(worldTime)
	viewProj := camera.ViewProj()
	viewProjInv := viewProj.Inv()
	goCamera := render.Camera{
		ViewProj:       viewProj,
		ViewProjInv:    viewProjInv,
		Pos:            camera.Pos,
		CloudOffset:    cloud,
		SunDirection:   dayNight.SunDirection,
		Daylight:       dayNight.Daylight,
		StarVisibility: dayNight.StarVisibility,
		SkyColor:       dayNight.ClearColor,
	}
	right := mgl32.Vec3{
		float32(math.Cos(float64(camera.Yaw))),
		0,
		-float32(math.Sin(float64(camera.Yaw))),
	}
	billboard := render.BillboardCamera{
		ViewProj: viewProj,
		Right:    right,
		Up:       right.Cross(camera.Forward()).Normalize(),
	}

	avatars := []render.Avatar{
		{
			Key:      render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte{1}},
			Position: mgl32.Vec3{-4.5, 73.5, 6.5},
			Yaw:      0.6,
		},
		{
			Key:      render.EntityKey{Kind: render.EntityCompanion, ID: [16]byte{2}},
			Position: mgl32.Vec3{-1.0, 73.5, 5.0},
			Yaw:      -0.4,
			Pitch:    0.1,
		},
	}
	tags := []render.NameTag{
		{
			Key:    render.EntityKey{Kind: render.EntityPlayer, ID: [16]byte{1}},
			Text:   "旅人Verifier",
			Anchor: mgl32.Vec3{-4.5, 75.6, 6.5},
		},
		{
			Key:    render.EntityKey{Kind: render.EntityCompanion, ID: [16]byte{2}},
			Text:   "伙伴Ren",
			Anchor: mgl32.Vec3{-1.0, 75.6, 5.0},
		},
	}
	drops := []render.ItemDrop{
		{ID: core.DropID{Chunk: core.ChunkPos{X: -1, Z: 0}, Slot: 7}, Block: core.BlockPos{X: -3, Y: 73, Z: 7}, Item: core.ItemID(core.StoneID)},
	}
	outline := render.BlockOutline{Visible: true, Position: core.BlockPos{X: -2, Y: 72, Z: 8}}
	const overlayStrength = float32(0.4)
	readout := render.PanelReadout{
		FrameMillis:  7.5,
		Position:     mgl32.Vec3{-3.5, 75.5, 12.5},
		Yaw:          0,
		Pitch:        -0.38,
		Tick:         serverTick,
		WorldTime:    worldTime,
		LoadedChunks: 9,
		Mode:         "单机",
	}
	budget := render.NewUploadBudget(4 << 20)
	prepareAll := func() {
		budget.BeginFrame()
		if err := nameTagRenderer.Prepare(tags, budget); err != nil {
			t.Fatal(err)
		}
		if err := hotbarRenderer.Prepare(
			core.Inventory{}, true, false, 0, nil, nil,
			hud.MiningOverlay{}, hud.HealthOverlay{Confirmed: true, Value: 18},
			hud.ChatOverlay{}, captureWidth, captureHeight, budget,
		); err != nil {
			t.Fatal(err)
		}
		if err := panelRenderer.Prepare(
			true, readout, nil, captureWidth, captureHeight, budget,
		); err != nil {
			t.Fatal(err)
		}
	}
	// 字形收敛:光栅化在后台 worker,按 capture 的 settle 策略重复 Prepare
	// 直到布局稳定(48 帧上限,4 倍余量)。
	for i := 0; i < 48; i++ {
		prepareAll()
		time.Sleep(2 * time.Millisecond)
	}
	prepareAll()

	color := dev.CreateTexture(gfx.TextureDesc{
		Label:     "dual backend full color",
		Width:     captureWidth,
		Height:    captureHeight,
		Format:    gfx.FormatBGRA8UnormSrgb,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageCopySrc,
	})
	defer color.Release()
	colorView := color.View(gfx.TextureViewDesc{})
	defer colorView.Release()
	depth := dev.CreateTexture(gfx.TextureDesc{
		Label:     "dual backend full depth",
		Width:     captureWidth,
		Height:    captureHeight,
		Format:    gfx.FormatDepth32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	defer depth.Release()
	depthView := depth.View(gfx.TextureViewDesc{})
	defer depthView.Release()

	renderGoFull := func() {
		encoder := dev.CreateCommandEncoder()
		goRenderer.Render(encoder, colorView, depthView, goCamera)
		if err := avatarRenderer.Render(encoder, colorView, depthView, goCamera, avatars); err != nil {
			t.Fatal(err)
		}
		dropRenderer.Render(encoder, colorView, depthView, goCamera, serverTick, drops)
		outlineRenderer.Render(encoder, colorView, depthView, goCamera, outline)
		nameTagRenderer.Render(encoder, colorView, depthView, billboard)
		overlayRenderer.Render(encoder, colorView, overlayStrength)
		hotbarRenderer.Render(encoder, colorView)
		panelRenderer.Render(encoder, colorView)
		cmd := encoder.Finish()
		dev.Submit(cmd)
		cmd.Release()
		dev.Poll(true)
	}
	renderGoFull()
	renderGoFull()

	// Rust 装配:字形整图同步 + HUD 图集 + frame v2 段。
	ntBackgrounds, ntGlyphs := nameTagRenderer.FrameStreams()
	glyphPixels := glyphAtlas.AtlasPixels()
	nonZeroGlyphBytes := 0
	for _, b := range glyphPixels {
		if b != 0 {
			nonZeroGlyphBytes++
		}
	}
	t.Logf("字形图集快照非零字节:%d,名牌流:bg=%d glyph=%d", nonZeroGlyphBytes, len(ntBackgrounds), len(ntGlyphs))
	rust.UploadGlyphRect(0, 0, render.GlyphAtlasSize, render.GlyphAtlasSize, glyphPixels)
	hudW, hudH, hudPixels := hotbarRenderer.AtlasPixels()
	rust.UploadHUDAtlas(hudW, hudH, hudPixels)

	var scratch mesh.VisibilityScratch
	visible := mesh.VisibleSectionsInto(
		nil, &scratch, dualBackendCameraSection(camera.Pos), 32,
		core.FrustumFrom(viewProj),
		func(p core.SectionPos) (mesh.Connectivity, bool) {
			c, ok := scene.connectivity[p]
			return c, ok
		})
	rustVisible := make([][3]int32, len(visible))
	for i, p := range visible {
		rustVisible[i] = [3]int32{p.X, p.Y, p.Z}
	}
	hudViewport, hudQuads, hudGlyphs := hotbarRenderer.FrameStreams()
	panelViewport, panelQuads, panelGlyphs := panelRenderer.FrameStreams()
	rustFrame := client.RenderFrame{
		ViewProj:         viewProj,
		ViewProjInv:      viewProjInv,
		Pos:              camera.Pos,
		Daylight:         dayNight.Daylight,
		SunDirection:     dayNight.SunDirection,
		StarVisibility:   dayNight.StarVisibility,
		SkyColor:         dayNight.ClearColor,
		CloudMacroX:      cloud.MacroX,
		CloudLocal:       cloud.Local,
		Visible:          rustVisible,
		AvatarInstances:  render.EncodeAvatarInstances(nil, avatars),
		DropInstances:    render.EncodeItemDropInstances(nil, serverTick, drops),
		OutlineInstances: render.EncodeBlockOutlineInstances(nil, outline),
		OverlayStrength:  overlayStrength,
		NameTagSegment: client.EncodeQuadSegment(
			render.EncodeBillboardCameraBytes(nil, billboard), ntBackgrounds, ntGlyphs, 64,
		),
		HUDSegment:   client.EncodeQuadSegment(hudViewport, hudQuads, hudGlyphs, 48),
		DebugSegment: client.EncodeQuadSegment(panelViewport, panelQuads, panelGlyphs, 48),
	}
	rust.RenderFrame(rustFrame)
	rust.RenderFrame(rustFrame)

	goImage := bgraToNRGBA(color.ReadLayer(0, 0), captureWidth, captureHeight)
	rustImage := bgraToNRGBA(rust.Readback(), captureWidth, captureHeight)
	diff, _, err := compareImages(rustImage, goImage)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("完整帧双后端差异:%s", diff)
	if !diff.withinThreshold(captureThresholds) {
		outDir := t.TempDir()
		if dump := os.Getenv("DUAL_BACKEND_DUMP"); dump != "" {
			outDir = dump
		}
		_ = writePNG(filepath.Join(outDir, "full-go.png"), goImage)
		_ = writePNG(filepath.Join(outDir, "full-rust.png"), rustImage)
		_, vis, _ := compareImages(rustImage, goImage)
		_ = writePNG(filepath.Join(outDir, "full-diff.png"), vis)
		t.Fatalf("完整帧双后端超出阈值:%s(图见 %s)", diff, outDir)
	}
}

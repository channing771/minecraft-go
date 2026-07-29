//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"runtime"
	"slices"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/assets"
	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
	"minecraft-go/internal/render"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
)

type applicationOptions struct {
	Seed      int64
	Benchmark bool
	WorldPath string
}

type application struct {
	window           *client.Window
	dev              gfx.Device
	surface          gfx.Surface
	color            gfx.Texture
	colorView        gfx.TextureView
	frameWidth       int
	frameHeight      int
	renderer         *render.Renderer
	clientEndpoint   network.ClientEndpoint
	server           *server.Server
	serverCancel     context.CancelFunc
	serverDone       chan error
	mirror           *client.Mirror
	predictor        *client.Predictor
	mesher           *client.Mesher
	depth            *depthTarget
	camera           client.Camera
	center           core.ChunkPos
	sequence         uint64
	selectedBlock    core.BlockID
	loadedChunks     map[core.ChunkPos]struct{}
	ticks            *tickRecorder
	saves            *saveRecorder
	observerFloor    uint64
	closeOnce        sync.Once
	closeErr         error
	releaseResources func()
}

type tickRecorder struct {
	mu      sync.Mutex
	sampler *client.PerfSampler
}

type saveRecorder struct {
	mu      sync.Mutex
	samples []float64
	next    int
	count   int
}

func newSaveRecorder(capacity int) *saveRecorder {
	return &saveRecorder{samples: make([]float64, max(1, capacity))}
}

func newPerformanceRecorders(benchmark bool) (*tickRecorder, *saveRecorder) {
	ticks := newTickRecorder(100_000)
	if !benchmark {
		return ticks, nil
	}
	return ticks, newSaveRecorder(100_000)
}

func (recorder *saveRecorder) add(duration time.Duration) {
	recorder.mu.Lock()
	recorder.samples[recorder.next] = float64(duration.Nanoseconds()) / float64(time.Millisecond)
	recorder.next = (recorder.next + 1) % len(recorder.samples)
	recorder.count = min(recorder.count+1, len(recorder.samples))
	recorder.mu.Unlock()
}

func (recorder *saveRecorder) reset() {
	recorder.mu.Lock()
	recorder.next = 0
	recorder.count = 0
	recorder.mu.Unlock()
}

func (recorder *saveRecorder) summary() client.PersistenceSummary {
	recorder.mu.Lock()
	ordered := make([]float64, recorder.count)
	start := 0
	if recorder.count == len(recorder.samples) {
		start = recorder.next
	}
	for index := range recorder.count {
		ordered[index] = recorder.samples[(start+index)%len(recorder.samples)]
	}
	recorder.mu.Unlock()
	if len(ordered) == 0 {
		return client.PersistenceSummary{}
	}
	slices.Sort(ordered)
	percentile := func(p float64) float64 {
		index := int(math.Ceil(p*float64(len(ordered)))) - 1
		return ordered[max(0, min(index, len(ordered)-1))]
	}
	return client.PersistenceSummary{
		Snapshots: uint64(len(ordered)),
		P50MS:     percentile(0.50),
		P95MS:     percentile(0.95),
		P99MS:     percentile(0.99),
		MaxMS:     ordered[len(ordered)-1],
	}
}

func newTickRecorder(capacity int) *tickRecorder {
	return &tickRecorder{sampler: client.NewPerfSampler(capacity)}
}

func (recorder *tickRecorder) add(duration time.Duration) {
	recorder.mu.Lock()
	recorder.sampler.Add(client.FrameSample{FrameMS: float64(duration.Microseconds()) / 1000})
	recorder.mu.Unlock()
}

func (recorder *tickRecorder) reset() {
	recorder.mu.Lock()
	recorder.sampler.Reset()
	recorder.mu.Unlock()
}

func (recorder *tickRecorder) summary() client.PhaseSummary {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.sampler.Summary(0)
}

func openApplicationStore(
	ctx context.Context,
	options applicationOptions,
) (storage.Store, error) {
	metadata := storage.Metadata{
		FormatVersion:  1,
		Seed:           options.Seed,
		SpawnDimension: core.Overworld,
		SpawnAnchor:    core.ChunkPos{},
	}
	if options.Benchmark {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return storage.NewMemory(metadata), nil
	}
	return storage.OpenDisk(ctx, options.WorldPath, storage.OpenOptions{Create: metadata})
}

func newApplication(options applicationOptions) (*application, error) {
	store, err := openApplicationStore(context.Background(), options)
	if err != nil {
		return nil, fmt.Errorf("打开世界存储: %w", err)
	}

	var window *client.Window
	var dev gfx.Device
	var surface gfx.Surface
	var color gfx.Texture
	var colorView gfx.TextureView
	var colorFormat gfx.TextureFormat
	width, height := 2560, 1440
	if options.Benchmark {
		dev, err = gfx.NewHeadlessDevice()
		colorFormat = gfx.FormatBGRA8UnormSrgb
		if err == nil {
			color = dev.CreateTexture(gfx.TextureDesc{
				Label:     "benchmark offscreen color",
				Width:     uint32(width),
				Height:    uint32(height),
				Format:    colorFormat,
				Dimension: gfx.TextureDimension2D,
				Usage:     gfx.TextureUsageRenderTarget,
			})
			colorView = color.View(gfx.TextureViewDesc{Dimension: gfx.TextureViewDimension2D})
		}
	} else {
		window, err = client.NewWindow(2560, 1440, "minecraft-go — M3A persistent world")
		if err == nil {
			fitFramebuffer(window, width, height)
			width, height = window.FramebufferSize()
			dev, surface, err = gfx.NewDevice(
				window.NativeHandle(), uint32(width), uint32(height),
			)
			if err == nil {
				colorFormat = surface.Format()
			}
		}
	}
	if err != nil {
		if colorView != nil {
			colorView.Release()
		}
		if color != nil {
			color.Release()
		}
		if surface != nil {
			surface.Release()
		}
		if dev != nil {
			dev.Release()
		}
		if window != nil {
			window.Close()
		}
		return nil, errors.Join(err, store.Close())
	}

	reg := assets.NewRegistry()
	renderer := render.New(dev, reg, colorFormat)
	renderer.Resize(uint32(width), uint32(height))
	camera := client.Camera{
		Pos:    mgl32.Vec3{0, 110, 0},
		Pitch:  -0.25,
		FovY:   mgl32.DegToRad(70),
		Aspect: float32(width) / float32(height),
		Near:   0.1,
		Far:    2000,
	}
	clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
	config := server.DefaultConfig(options.Seed)
	config.ViewRadius = viewDistance + 1
	config.TrustedObserver = options.Benchmark
	ticks, saves := newPerformanceRecorders(options.Benchmark)
	config.TickObserver = ticks.add
	if saves != nil {
		config.SaveObserver = saves.add
	}
	running := server.NewEmbedded(config, serverEndpoint, store)
	serverContext, serverCancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- running.Run(serverContext) }()

	app := &application{
		window:         window,
		dev:            dev,
		surface:        surface,
		color:          color,
		colorView:      colorView,
		frameWidth:     width,
		frameHeight:    height,
		renderer:       renderer,
		clientEndpoint: clientEndpoint,
		server:         running,
		serverCancel:   serverCancel,
		serverDone:     serverDone,
		mirror:         client.NewMirror(),
		predictor:      client.NewPredictor(),
		mesher:         client.NewMesher(reg, max(1, runtime.NumCPU()-2)),
		depth:          newDepthTarget(dev, uint32(width), uint32(height)),
		camera:         camera,
		center:         cameraChunk(camera.Pos),
		selectedBlock:  core.StoneID,
		loadedChunks:   make(map[core.ChunkPos]struct{}),
		ticks:          ticks,
		saves:          saves,
	}
	app.releaseResources = app.releaseOwnedResources
	if options.Benchmark {
		if err := app.requestTrustedObserverCenter(app.center); err != nil {
			cleanupErr := app.Close()
			return nil, errors.Join(
				fmt.Errorf("设置初始 trusted observer 中心: %w", err),
				cleanupErr,
			)
		}
	}
	return app, nil
}

func fitFramebuffer(window *client.Window, targetWidth, targetHeight int) {
	contentWidth, contentHeight := window.ContentSize()
	framebufferWidth, framebufferHeight := window.FramebufferSize()
	if framebufferWidth <= 0 || framebufferHeight <= 0 {
		return
	}
	contentWidth = max(1, int(math.Round(float64(targetWidth*contentWidth)/float64(framebufferWidth))))
	contentHeight = max(1, int(math.Round(float64(targetHeight*contentHeight)/float64(framebufferHeight))))
	window.SetContentSize(contentWidth, contentHeight)
	window.Poll()
}

func (a *application) Close() error {
	a.closeOnce.Do(func() {
		if a.clientEndpoint != nil {
			a.closeErr = errors.Join(a.closeErr, a.clientEndpoint.Close())
		}
		if a.serverCancel != nil {
			a.serverCancel()
		}
		if a.serverDone != nil {
			if err := <-a.serverDone; err != nil && err != context.Canceled {
				a.closeErr = errors.Join(a.closeErr, err)
			}
		}
		if a.releaseResources != nil {
			a.releaseResources()
		}
	})
	return a.closeErr
}

func (a *application) releaseOwnedResources() {
	if a.mesher != nil {
		a.mesher.Close()
	}
	if a.renderer != nil {
		a.renderer.Release()
	}
	if a.depth != nil {
		a.depth.Release()
	}
	if a.colorView != nil {
		a.colorView.Release()
	}
	if a.color != nil {
		a.color.Release()
	}
	if a.surface != nil {
		a.surface.Release()
	}
	if a.dev != nil {
		a.dev.Release()
	}
	if a.window != nil {
		a.window.Close()
	}
}

func (a *application) updateCenter() {
	center := cameraChunk(a.camera.Pos)
	if center == a.center {
		return
	}
	a.center = center
	if err := a.requestTrustedObserverCenter(center); err != nil {
		log.Printf("更新视距中心失败: %v", err)
	}
}

func (a *application) requestTrustedObserverCenter(center core.ChunkPos) error {
	_, _, sequence, _ := a.server.AppliedTrustedObserverCenter()
	a.observerFloor = sequence
	return a.server.SetTrustedObserverCenter(core.Overworld, center)
}

func (a *application) nextSequence() uint64 {
	a.sequence++
	return a.sequence
}

// frame 应用服务端消息后绘制一帧。
func (a *application) frame(drainMax int) bool {
	a.drainServerMessages(drainMax)
	return a.renderFrame(drainMax)
}

// renderFrame 绘制一帧，返回 surface 是否实际取得了可呈现纹理。
func (a *application) renderFrame(workMax int) bool {
	width, height := a.framebufferSize()
	if width == 0 || height == 0 {
		return false
	}
	if a.surface != nil && (uint32(width) != a.depth.width || uint32(height) != a.depth.height) {
		a.surface.Resize(uint32(width), uint32(height))
		a.depth.Release()
		a.depth = newDepthTarget(a.dev, uint32(width), uint32(height))
		a.renderer.Resize(uint32(width), uint32(height))
		a.camera.Aspect = float32(width) / float32(height)
	}

	a.renderer.BeginFrame()
	a.mesher.Schedule(a.mirror, workMax)
	for _, result := range a.mesher.Drain(a.mirror, workMax) {
		if result.Dimension != core.Overworld {
			continue
		}
		a.renderer.SetConnectivity(result.Pos, result.Conn)
		a.renderer.QueueSection(result.Pos, result.Quads)
	}
	a.renderer.FlushUploads(a.center)
	a.renderer.DropOutside(a.center, viewDistance)

	target := a.colorView
	if a.surface != nil {
		target = a.surface.Acquire()
		if target == nil {
			return false
		}
	}
	encoder := a.dev.CreateCommandEncoder()
	a.renderer.Render(encoder, target, a.depth.view, render.Camera{
		ViewProj: a.camera.ViewProj(),
		Pos:      a.camera.Pos,
	})
	command := encoder.Finish()
	a.dev.Submit(command)
	command.Release()
	if a.surface != nil {
		a.surface.Present()
	}
	return true
}

func (a *application) drainServerMessages(maxMessages int) {
	if maxMessages <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range maxMessages {
		message, err := a.clientEndpoint.Recv(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, network.ErrClosed) {
			return
		}
		if err != nil {
			log.Printf("接收内置服务端消息失败: %v", err)
			return
		}
		if state, ok := message.(network.PlayerState); ok {
			result, err := a.predictor.ApplyPlayerState(state, client.MirrorCollisionSource{
				Mirror:    a.mirror,
				Dimension: core.Overworld,
			})
			if err != nil {
				log.Printf("服务端协议数据非法，关闭会话: %v", err)
				_ = a.clientEndpoint.Close()
				a.serverCancel()
				return
			}
			if result.ResetView {
				a.camera.Yaw = result.Yaw
				a.camera.Pitch = result.Pitch
			}
			continue
		}
		update, err := a.mirror.Apply(message)
		if err != nil {
			log.Printf("服务端协议数据非法，关闭会话: %v", err)
			_ = a.clientEndpoint.Close()
			a.serverCancel()
			return
		}
		switch message := message.(type) {
		case network.ChunkSnapshot:
			if message.Dimension == core.Overworld {
				a.loadedChunks[message.Chunk] = struct{}{}
			}
		case network.ForgetChunks:
			if message.Dimension == core.Overworld {
				for _, position := range message.Chunks {
					delete(a.loadedChunks, position)
				}
			}
		}
		if update.Resync != nil {
			update.Resync.Sequence = a.nextSequence()
			if err := a.send(update.Resync); err != nil {
				log.Printf("发送区块 resync 失败: %v", err)
			}
		}
		if update.Rejected != nil {
			log.Printf("权威命令被拒绝: sequence=%d reason=%s",
				update.Rejected.Sequence, update.Rejected.Reason)
		}
		a.mesher.MarkDirty(update.Dirty...)
		for _, key := range update.Forgotten {
			if key.Dimension != core.Overworld {
				continue
			}
			a.renderer.DropSection(key.Pos)
			if key.Pos.Y == 0 {
				a.mesher.ForgetChunk(key.Dimension, core.ChunkPos{X: key.Pos.X, Z: key.Pos.Z})
			}
		}
	}
}

func (a *application) breakBlock() {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	if err := a.send(network.BreakBlock{
		Sequence: a.nextSequence(),
		Yaw:      a.camera.Yaw,
		Pitch:    a.camera.Pitch,
	}); err != nil {
		log.Printf("发送挖掘命令失败: %v", err)
	}
}

func (a *application) placeBlock() {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	if err := a.send(network.PlaceBlock{
		Sequence: a.nextSequence(),
		Yaw:      a.camera.Yaw,
		Pitch:    a.camera.Pitch,
		Block:    a.selectedBlock,
	}); err != nil {
		log.Printf("发送放置命令失败: %v", err)
	}
}

func (a *application) send(message network.ClientMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	return a.clientEndpoint.Send(ctx, message)
}

func (a *application) framebufferLabel() string {
	width, height := a.framebufferSize()
	return fmt.Sprintf("%dx%d", width, height)
}

func (a *application) framebufferSize() (int, int) {
	if a.window != nil {
		return a.window.FramebufferSize()
	}
	return a.frameWidth, a.frameHeight
}

func cameraChunk(pos mgl32.Vec3) core.ChunkPos {
	return core.BlockPos{
		X: int32(math.Floor(float64(pos.X()))),
		Z: int32(math.Floor(float64(pos.Z()))),
	}.Chunk()
}

type depthTarget struct {
	texture       gfx.Texture
	view          gfx.TextureView
	width, height uint32
}

func newDepthTarget(dev gfx.Device, width, height uint32) *depthTarget {
	texture := dev.CreateTexture(gfx.TextureDesc{
		Label:     "main depth",
		Width:     width,
		Height:    height,
		Format:    gfx.FormatDepth32Float,
		Dimension: gfx.TextureDimension2D,
		Usage:     gfx.TextureUsageRenderTarget | gfx.TextureUsageBinding,
	})
	view := texture.View(gfx.TextureViewDesc{
		Dimension: gfx.TextureViewDimension2D,
		Aspect:    gfx.AspectDepthOnly,
	})
	return &depthTarget{
		texture: texture,
		view:    view,
		width:   width,
		height:  height,
	}
}

func (d *depthTarget) Release() {
	if d.view != nil {
		d.view.Release()
		d.view = nil
	}
	if d.texture != nil {
		d.texture.Release()
		d.texture = nil
	}
}

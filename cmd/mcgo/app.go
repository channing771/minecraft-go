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
	"minecraft-go/internal/worldgen"
)

type applicationOptions struct {
	Seed               int64
	Benchmark          bool
	BenchmarkTransport string
	WorldPath          string
	Connect            string
	Identity           *network.Identity
}

type application struct {
	window             applicationWindow
	dev                gfx.Device
	surface            gfx.Surface
	color              gfx.Texture
	colorView          gfx.TextureView
	frameWidth         int
	frameHeight        int
	renderer           *render.Renderer
	clientEndpoint     network.ClientEndpoint
	receiver           *client.Receiver
	server             *server.Server
	host               applicationHost
	serverCancel       context.CancelFunc
	serverDone         chan error
	mirror             *client.Mirror
	predictor          *client.Predictor
	mesher             *client.Mesher
	depth              *depthTarget
	camera             client.Camera
	center             core.ChunkPos
	sequence           uint64
	selectedBlock      core.BlockID
	loadedChunks       map[core.ChunkPos]struct{}
	ticks              *tickRecorder
	saves              *saveRecorder
	observerFloor      uint64
	benchmarkTransport string
	closeOnce          sync.Once
	closeErr           error
	releaseResources   func()
}

type applicationWindow interface {
	SetCursorCaptured(bool)
	CursorPos() (float64, float64)
	ShouldClose() bool
	Poll()
	KeyDown(client.Key) bool
	PrimaryButtonDown() bool
	SecondaryButtonDown() bool
	CursorCaptured() bool
	FramebufferSize() (int, int)
	ContentSize() (int, int)
	SetContentSize(int, int)
	CancelClose()
	NativeHandle() gfx.NativeWindowHandle
	Close()
}

type applicationHost interface {
	Run(context.Context, network.Listener) error
	AcceptStream(context.Context, network.ServerPacketStream) error
	Shutdown(context.Context) error
}

type applicationDependencies struct {
	openStore           func(context.Context, applicationOptions) (storage.WorldStore, error)
	dialTCP             func(context.Context, string) (network.ClientPacketStream, error)
	loginClient         func(context.Context, network.ClientPacketStream, network.Identity) (network.ClientEndpoint, error)
	newHost             func(server.Config, server.Generator, storage.WorldStore) (applicationHost, error)
	newMemoryStreamPair func(int) (network.ClientPacketStream, network.ServerPacketStream, error)
	newWindow           func(int, int, string) (applicationWindow, error)
	newDevice           func(gfx.NativeWindowHandle, uint32, uint32) (gfx.Device, gfx.Surface, error)
	newHeadlessDevice   func() (gfx.Device, error)
}

func defaultApplicationDependencies() applicationDependencies {
	return applicationDependencies{
		openStore:   openApplicationStore,
		dialTCP:     network.DialTCP,
		loginClient: network.LoginClient,
		newHost: func(
			config server.Config,
			generator server.Generator,
			store storage.WorldStore,
		) (applicationHost, error) {
			return server.NewHost(config, generator, store), nil
		},
		newMemoryStreamPair: func(capacity int) (
			network.ClientPacketStream,
			network.ServerPacketStream,
			error,
		) {
			clientStream, serverStream := network.NewMemoryStreamPair(capacity)
			return clientStream, serverStream, nil
		},
		newWindow: func(width, height int, title string) (applicationWindow, error) {
			return client.NewWindow(width, height, title)
		},
		newDevice:         gfx.NewDevice,
		newHeadlessDevice: gfx.NewHeadlessDevice,
	}
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
		Snapshots: int64(len(ordered)),
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
) (storage.WorldStore, error) {
	if options.Connect != "" {
		return nil, nil
	}
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
	return newApplicationWithDependencies(options, defaultApplicationDependencies())
}

func newApplicationWithDependencies(
	options applicationOptions,
	dependencies applicationDependencies,
) (*application, error) {
	ctx := context.Background()
	var store storage.WorldStore
	var clientEndpoint network.ClientEndpoint
	var running *server.Server
	var host applicationHost
	var serverCancel context.CancelFunc
	var serverDone chan error
	var err error
	ticks, saves := newPerformanceRecorders(options.Benchmark)
	config := server.DefaultConfig(options.Seed)
	config.ViewRadius = viewDistance + 1
	config.TrustedObserver = options.Benchmark
	config.TickObserver = ticks.add
	if saves != nil {
		config.SaveObserver = saves.add
	}
	if options.Connect != "" {
		if options.Identity == nil {
			return nil, errors.New("远程连接缺少本机身份")
		}
		stream, err := dependencies.dialTCP(ctx, options.Connect)
		if err != nil {
			return nil, fmt.Errorf("连接远程服务器: %w", err)
		}
		clientEndpoint, err = dependencies.loginClient(ctx, stream, *options.Identity)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("远程登录: %w", err), stream.Close())
		}
	} else {
		store, err = dependencies.openStore(ctx, options)
		if err != nil {
			return nil, fmt.Errorf("打开世界存储: %w", err)
		}
	}
	if options.Benchmark {
		running = server.NewWorld(config, worldgen.New(store.Metadata().Seed), store)
		clientEndpoint, err = assembleBenchmarkObserverConnection(
			ctx, running, options.BenchmarkTransport,
		)
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		serverContext, cancel := context.WithCancel(context.Background())
		serverCancel = cancel
		serverDone = make(chan error, 1)
		go func() { serverDone <- running.Run(serverContext) }()
	} else if options.Connect == "" {
		if options.Identity == nil {
			_ = store.Close()
			return nil, errors.New("本地世界缺少本机身份")
		}
		clientEndpoint, host, serverCancel, serverDone, err = assembleLocalApplicationConnection(
			ctx,
			config,
			worldgen.New(store.Metadata().Seed),
			store,
			*options.Identity,
			dependencies,
		)
		if err != nil {
			return nil, fmt.Errorf("连接本地 Host: %w", err)
		}
	}
	receiver := client.NewReceiver(clientEndpoint, 256)

	var window applicationWindow
	var dev gfx.Device
	var surface gfx.Surface
	var color gfx.Texture
	var colorView gfx.TextureView
	var colorFormat gfx.TextureFormat
	width, height := 2560, 1440
	if options.Benchmark {
		dev, err = dependencies.newHeadlessDevice()
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
		window, err = dependencies.newWindow(2560, 1440, "minecraft-go — M3A persistent world")
		if err == nil {
			fitFramebuffer(window, width, height)
			width, height = window.FramebufferSize()
			dev, surface, err = dependencies.newDevice(
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
		connectionErr := receiver.Close()
		if serverCancel != nil {
			serverCancel()
		}
		if serverDone != nil {
			connectionErr = errors.Join(connectionErr, <-serverDone)
		}
		return nil, errors.Join(err, connectionErr)
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
		receiver:       receiver,
		server:         running,
		host:           host,
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
		benchmarkTransport: func() string {
			if options.BenchmarkTransport == "" {
				return "memory"
			}
			return options.BenchmarkTransport
		}(),
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

func assembleBenchmarkObserverConnection(
	ctx context.Context,
	running *server.Server,
	transport string,
) (network.ClientEndpoint, error) {
	if transport == "" || transport == "memory" {
		clientEndpoint, serverEndpoint := network.NewMemoryPair(256)
		if err := running.AttachTrustedObserver(serverEndpoint); err != nil {
			_ = clientEndpoint.Close()
			return nil, err
		}
		return clientEndpoint, nil
	}
	if transport != "tcp" {
		return nil, fmt.Errorf("不支持 benchmark transport %q", transport)
	}
	listener, err := network.ListenTCP("127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		stream, acceptErr := listener.Accept(ctx)
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		pending, loginErr := network.BeginServerLogin(ctx, stream)
		if loginErr == nil {
			loginErr = pending.Accept(ctx, running.AttachTrustedObserver)
		}
		serverDone <- loginErr
	}()
	stream, err := network.DialTCP(ctx, listener.Addr())
	if err != nil {
		return nil, errors.Join(err, <-serverDone)
	}
	identity := network.Identity{
		PlayerID:    core.PlayerID{0x2c, 0xad, 0xe1, 0x90, 0x9d, 0xb6, 0x43, 0x82, 0x8d, 0x31, 0xcb, 0x40, 0xe5, 0xbb, 0x52, 0x29},
		DisplayName: "Benchmark",
	}
	endpoint, loginErr := network.LoginClient(ctx, stream, identity)
	serverErr := <-serverDone
	if err := errors.Join(loginErr, serverErr); err != nil {
		_ = stream.Close()
		return nil, err
	}
	return endpoint, nil
}

type applicationLoginResult struct {
	endpoint network.ClientEndpoint
	err      error
}

func assembleLocalApplicationConnection(
	ctx context.Context,
	config server.Config,
	generator server.Generator,
	store storage.WorldStore,
	identity network.Identity,
	dependencies applicationDependencies,
) (
	network.ClientEndpoint,
	applicationHost,
	context.CancelFunc,
	chan error,
	error,
) {
	host, err := dependencies.newHost(config, generator, store)
	if err != nil {
		return nil, nil, nil, nil, errors.Join(err, store.Close())
	}
	hostContext, cancel := context.WithCancel(ctx)
	hostDone := make(chan error, 1)
	go func() { hostDone <- host.Run(hostContext, nil) }()

	clientStream, serverStream, err := dependencies.newMemoryStreamPair(256)
	if err != nil {
		cleanupErr := cleanupLocalApplicationStartup(
			host, cancel, hostDone, nil, nil, nil, config.ShutdownTimeout,
		)
		return nil, nil, nil, nil, errors.Join(err, cleanupErr)
	}
	acceptDone := make(chan error, 1)
	go func() { acceptDone <- host.AcceptStream(hostContext, serverStream) }()
	loginDone := make(chan applicationLoginResult, 1)
	go func() {
		endpoint, loginErr := dependencies.loginClient(hostContext, clientStream, identity)
		loginDone <- applicationLoginResult{endpoint: endpoint, err: loginErr}
	}()

	select {
	case result := <-loginDone:
		if result.err == nil {
			return result.endpoint, host, cancel, hostDone, nil
		}
		cleanupErr := cleanupLocalApplicationStartup(
			host,
			cancel,
			hostDone,
			acceptDone,
			clientStream,
			serverStream,
			config.ShutdownTimeout,
		)
		return nil, nil, nil, nil, errors.Join(result.err, cleanupErr)
	case hostErr := <-hostDone:
		_ = clientStream.Close()
		_ = serverStream.Close()
		cancel()
		result := <-loginDone
		acceptErr := <-acceptDone
		shutdownErr := shutdownApplicationHost(host, config.ShutdownTimeout)
		return nil, nil, nil, nil, errors.Join(
			hostErr,
			result.err,
			ignoreApplicationStartupCloseError(acceptErr),
			shutdownErr,
		)
	case acceptErr := <-acceptDone:
		_ = clientStream.Close()
		_ = serverStream.Close()
		cancel()
		result := <-loginDone
		hostErr := <-hostDone
		shutdownErr := shutdownApplicationHost(host, config.ShutdownTimeout)
		return nil, nil, nil, nil, errors.Join(
			acceptErr,
			result.err,
			ignoreApplicationStartupCloseError(hostErr),
			shutdownErr,
		)
	}
}

func cleanupLocalApplicationStartup(
	host applicationHost,
	cancel context.CancelFunc,
	hostDone <-chan error,
	acceptDone <-chan error,
	clientStream network.ClientPacketStream,
	serverStream network.ServerPacketStream,
	shutdownTimeout time.Duration,
) error {
	var cleanupErr error
	if clientStream != nil {
		cleanupErr = errors.Join(cleanupErr, clientStream.Close())
	}
	if serverStream != nil {
		cleanupErr = errors.Join(cleanupErr, serverStream.Close())
	}
	cancel()
	if acceptDone != nil {
		cleanupErr = errors.Join(
			cleanupErr,
			ignoreApplicationStartupCloseError(<-acceptDone),
		)
	}
	cleanupErr = errors.Join(
		cleanupErr,
		ignoreApplicationStartupCloseError(<-hostDone),
		shutdownApplicationHost(host, shutdownTimeout),
	)
	return cleanupErr
}

func shutdownApplicationHost(host applicationHost, timeout time.Duration) error {
	shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return host.Shutdown(shutdownContext)
}

func ignoreApplicationStartupCloseError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, network.ErrClosed) {
		return nil
	}
	return err
}

func fitFramebuffer(window applicationWindow, targetWidth, targetHeight int) {
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
		if a.receiver != nil {
			a.closeErr = errors.Join(a.closeErr, a.receiver.Close())
		} else if a.clientEndpoint != nil {
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
	for range maxMessages {
		if a.receiver == nil {
			return
		}
		message, ok := a.receiver.TryRecv()
		if !ok {
			runtime.Gosched()
			message, ok = a.receiver.TryRecv()
			if !ok {
				return
			}
		}
		if state, ok := message.(network.PlayerState); ok {
			result, err := a.predictor.ApplyPlayerState(state, client.MirrorCollisionSource{
				Mirror:    a.mirror,
				Dimension: core.Overworld,
			})
			if err != nil {
				log.Printf("服务端协议数据非法，关闭会话: %v", err)
				_ = a.clientEndpoint.Close()
				if a.serverCancel != nil {
					a.serverCancel()
				}
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
			if a.serverCancel != nil {
				a.serverCancel()
			}
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

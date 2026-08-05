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
	window                  applicationWindow
	dev                     gfx.Device
	surface                 gfx.Surface
	color                   gfx.Texture
	colorView               gfx.TextureView
	frameWidth              int
	frameHeight             int
	renderer                *render.Renderer
	remotePlayers           *client.RemotePlayers
	remotePresentations     []client.RemotePresentation
	remoteAvatars           []render.Avatar
	remoteNameTags          []render.NameTag
	avatarRenderer          *render.AvatarRenderer
	nameTagRenderer         *render.NameTagRenderer
	hotbarRenderer          *render.HotbarRenderer
	inventory               client.InventoryMirror
	furnace                 client.FurnaceMirror
	miningOverlay           render.MiningOverlay
	itemDropRenderer        *render.ItemDropRenderer
	itemDrops               *client.ItemDrops
	itemDropInstances       []render.ItemDrop
	inventoryOpen           bool
	inventorySource         int
	serverTick              uint64
	glyphAtlas              *render.GlyphAtlas
	clientEndpoint          network.ClientEndpoint
	receiver                *client.Receiver
	server                  *server.Server
	host                    applicationHost
	serverCancel            context.CancelFunc
	serverDone              chan error
	mirror                  *client.Mirror
	predictor               *client.Predictor
	mesher                  *client.Mesher
	depth                   *depthTarget
	camera                  client.Camera
	center                  core.ChunkPos
	sequence                uint64
	loadedChunks            map[core.ChunkPos]struct{}
	ticks                   *tickRecorder
	saves                   *saveRecorder
	observerFloor           uint64
	benchmarkTransport      string
	multiplayerRenderTiming *multiplayerRenderTiming
	multiplayerRenderNow    func() time.Time
	closeOnce               sync.Once
	closeErr                error
	clientCloseOnce         sync.Once
	clientCloseErr          error
	releaseResources        func()
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
	newGlyphAtlas       func(gfx.Device) (*render.GlyphAtlas, error)
	newAvatarRenderer   func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat) (*render.AvatarRenderer, error)
	newNameTagRenderer  func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat, render.GlyphSource) (*render.NameTagRenderer, error)
	newHotbarRenderer   func(gfx.Device, gfx.TextureFormat, render.GlyphSource) (*render.HotbarRenderer, error)
	newItemDropRenderer func(gfx.Device, gfx.TextureFormat, gfx.TextureFormat) (*render.ItemDropRenderer, error)
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
		FormatVersion:  2,
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
	if dependencies.newGlyphAtlas == nil {
		dependencies.newGlyphAtlas = render.NewGlyphAtlas
	}
	if dependencies.newAvatarRenderer == nil {
		dependencies.newAvatarRenderer = func(dev gfx.Device, color, depth gfx.TextureFormat) (*render.AvatarRenderer, error) {
			return render.NewAvatarRenderer(dev, color, depth), nil
		}
	}
	if dependencies.newNameTagRenderer == nil {
		dependencies.newNameTagRenderer = func(dev gfx.Device, color, depth gfx.TextureFormat, atlas render.GlyphSource) (*render.NameTagRenderer, error) {
			return render.NewNameTagRenderer(dev, color, depth, atlas), nil
		}
	}
	if dependencies.newHotbarRenderer == nil {
		dependencies.newHotbarRenderer = func(dev gfx.Device, color gfx.TextureFormat, atlas render.GlyphSource) (*render.HotbarRenderer, error) {
			return render.NewHotbarRenderer(dev, color, atlas), nil
		}
	}
	if dependencies.newItemDropRenderer == nil {
		dependencies.newItemDropRenderer = func(dev gfx.Device, color, depth gfx.TextureFormat) (*render.ItemDropRenderer, error) {
			return render.NewItemDropRenderer(dev, color, depth), nil
		}
	}
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
			func(address string) (network.Listener, error) {
				return network.ListenTCP(address)
			},
			dependencies.dialTCP,
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
		window, err = dependencies.newWindow(2560, 1440, "minecraft-go — M3C multiplayer world")
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
	camera := client.Camera{
		Pos:    mgl32.Vec3{0, 110, 0},
		Pitch:  -0.25,
		FovY:   mgl32.DegToRad(70),
		Aspect: float32(width) / float32(height),
		Near:   0.1,
		Far:    2000,
	}
	app := &application{
		window:          window,
		dev:             dev,
		surface:         surface,
		color:           color,
		colorView:       colorView,
		frameWidth:      width,
		frameHeight:     height,
		clientEndpoint:  clientEndpoint,
		receiver:        receiver,
		server:          running,
		host:            host,
		serverCancel:    serverCancel,
		serverDone:      serverDone,
		mirror:          client.NewMirror(),
		itemDrops:       client.NewItemDrops(),
		inventorySource: -1,
		predictor:       client.NewPredictor(),
		remotePlayers:   client.NewRemotePlayers(),
		camera:          camera,
		center:          cameraChunk(camera.Pos),
		loadedChunks:    make(map[core.ChunkPos]struct{}),
		ticks:           ticks,
		saves:           saves,
		benchmarkTransport: func() string {
			if options.BenchmarkTransport == "" {
				return "memory"
			}
			return options.BenchmarkTransport
		}(),
	}
	app.releaseResources = app.releaseOwnedResources
	app.renderer = render.New(dev, reg, colorFormat)
	app.renderer.Resize(uint32(width), uint32(height))
	app.depth = newDepthTarget(dev, uint32(width), uint32(height))
	app.glyphAtlas, err = dependencies.newGlyphAtlas(dev)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("创建字形图集: %w", err), app.Close())
	}
	app.avatarRenderer, err = dependencies.newAvatarRenderer(
		dev, colorFormat, gfx.FormatDepth32Float,
	)
	if err != nil {
		app.releaseRemoteConstructionResources()
		return nil, errors.Join(fmt.Errorf("创建远端玩家渲染器: %w", err), app.Close())
	}
	app.nameTagRenderer, err = dependencies.newNameTagRenderer(
		dev, colorFormat, gfx.FormatDepth32Float, app.glyphAtlas,
	)
	if err != nil {
		app.releaseRemoteConstructionResources()
		return nil, errors.Join(fmt.Errorf("创建昵称渲染器: %w", err), app.Close())
	}
	app.hotbarRenderer, err = dependencies.newHotbarRenderer(dev, colorFormat, app.glyphAtlas)
	if err != nil {
		app.releaseRemoteConstructionResources()
		return nil, errors.Join(fmt.Errorf("创建快捷栏渲染器: %w", err), app.Close())
	}
	app.itemDropRenderer, err = dependencies.newItemDropRenderer(
		dev, colorFormat, gfx.FormatDepth32Float,
	)
	if err != nil {
		app.releaseRemoteConstructionResources()
		return nil, errors.Join(fmt.Errorf("创建掉落物渲染器: %w", err), app.Close())
	}
	app.mesher = client.NewMesher(reg, max(1, runtime.NumCPU()-2))
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

func (a *application) releaseRemoteConstructionResources() {
	if a.itemDropRenderer != nil {
		a.itemDropRenderer.Release()
		a.itemDropRenderer = nil
	}
	if a.hotbarRenderer != nil {
		a.hotbarRenderer.Release()
		a.hotbarRenderer = nil
	}
	if a.nameTagRenderer != nil {
		a.nameTagRenderer.Release()
		a.nameTagRenderer = nil
	}
	if a.avatarRenderer != nil {
		a.avatarRenderer.Release()
		a.avatarRenderer = nil
	}
	if a.glyphAtlas != nil {
		a.glyphAtlas.Release()
		a.glyphAtlas = nil
	}
}

func assembleBenchmarkObserverConnection(
	ctx context.Context,
	running *server.Server,
	transport string,
	listenTCP func(string) (network.Listener, error),
	dialTCP func(context.Context, string) (network.ClientPacketStream, error),
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
	listener, err := listenTCP("127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	acceptContext, cancelAccept := context.WithCancel(ctx)
	defer cancelAccept()
	serverDone := make(chan error, 1)
	go func() {
		stream, acceptErr := listener.Accept(acceptContext)
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		pending, loginErr := network.BeginServerLogin(acceptContext, stream)
		if loginErr == nil {
			loginErr = pending.Accept(acceptContext, running.AttachTrustedObserver)
		}
		serverDone <- loginErr
	}()
	stream, err := dialTCP(ctx, listener.Addr())
	if err != nil {
		cancelAccept()
		closeErr := listener.Close()
		return nil, errors.Join(err, closeErr, <-serverDone)
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
		a.closeClientSession(nil)
		a.closeErr = errors.Join(a.closeErr, a.clientCloseErr)
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
	if a.itemDropRenderer != nil {
		a.itemDropRenderer.Release()
	}
	if a.hotbarRenderer != nil {
		a.hotbarRenderer.Release()
	}
	if a.nameTagRenderer != nil {
		a.nameTagRenderer.Release()
	}
	if a.glyphAtlas != nil {
		a.glyphAtlas.Release()
	}
	if a.avatarRenderer != nil {
		a.avatarRenderer.Release()
	}
	if a.renderer != nil {
		a.renderer.Release()
	}
	if a.mesher != nil {
		a.mesher.Close()
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

// closeClientSession closes only the current client endpoint. The embedded
// server belongs to the whole application and is stopped exclusively by Close.
func (a *application) closeClientSession(cause error) {
	a.clientCloseOnce.Do(func() {
		if cause != nil {
			log.Printf("关闭客户端会话: %v", cause)
		}
		if a.receiver != nil {
			a.clientCloseErr = a.receiver.Close()
		} else if a.clientEndpoint != nil {
			a.clientCloseErr = a.clientEndpoint.Close()
		}
		if a.remotePlayers != nil {
			a.remotePlayers.Reset()
		}
		a.inventory.Reset()
		a.furnace.Reset()
		a.miningOverlay = render.MiningOverlay{}
		a.inventoryOpen = false
		a.inventorySource = -1
		a.itemDrops.Reset()
	})
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
func (a *application) frame(drainMax, meshWorkMax int, elapsed time.Duration) (bool, error) {
	a.drainServerMessages(drainMax)
	if a.receiver != nil {
		if err := a.receiver.Err(); err != nil {
			a.closeClientSession(err)
			return false, err
		}
	}
	if a.remotePlayers != nil {
		a.remotePlayers.Advance(elapsed)
	}
	return a.renderFrame(meshWorkMax)
}

// renderFrame 绘制一帧，返回 surface 是否实际取得了可呈现纹理。
func (a *application) renderFrame(workMax int) (bool, error) {
	width, height := a.framebufferSize()
	if width == 0 || height == 0 {
		return false, nil
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
	a.remotePresentations = a.remotePlayers.AppendPresentations(a.remotePresentations[:0])
	a.remoteAvatars, a.remoteNameTags = remoteRenderPresentationsSortedInto(
		a.remoteAvatars[:0],
		a.remoteNameTags[:0],
		a.remotePresentations,
	)
	avatars, tags := a.remoteAvatars, a.remoteNameTags
	renderTiming := a.multiplayerRenderTiming
	var renderNow func() time.Time
	var nameTagDuration time.Duration
	if renderTiming != nil {
		renderNow = a.multiplayerRenderNow
		if renderNow == nil {
			renderNow = time.Now
		}
		started := renderNow()
		if err := a.nameTagRenderer.Prepare(tags, a.renderer.UploadBudget()); err != nil {
			return false, fmt.Errorf("准备远端玩家昵称: %w", err)
		}
		nameTagDuration = renderNow().Sub(started)
	} else if err := a.nameTagRenderer.Prepare(tags, a.renderer.UploadBudget()); err != nil {
		return false, fmt.Errorf("准备远端玩家昵称: %w", err)
	}
	inventory, inventoryConfirmed := a.inventory.State()
	if inventoryConfirmed {
		var overlay *render.FurnaceOverlay
		if furnace, opened := a.furnace.State(); opened {
			overlay = &render.FurnaceOverlay{
				Input:         furnace.Input,
				Fuel:          furnace.Fuel,
				Output:        furnace.Output,
				ProgressTicks: furnace.ProgressTicks,
				BurnTicks:     furnace.BurnTicks,
			}
		}
		if err := a.hotbarRenderer.Prepare(
			inventory, a.inventoryOpen, a.inventorySource, overlay,
			a.miningOverlay,
			uint32(width), uint32(height), a.renderer.UploadBudget(),
		); err != nil {
			return false, fmt.Errorf("准备快捷栏 HUD: %w", err)
		}
	}
	a.renderer.DropOutside(a.center, viewDistance)

	target := a.colorView
	if a.surface != nil {
		target = a.surface.Acquire()
		if target == nil {
			return false, nil
		}
	}
	encoder := a.dev.CreateCommandEncoder()
	a.renderer.Render(encoder, target, a.depth.view, render.Camera{
		ViewProj: a.camera.ViewProj(),
		Pos:      a.camera.Pos,
	})
	var started time.Time
	if renderTiming != nil {
		started = renderNow()
	}
	a.avatarRenderer.Render(encoder, target, a.depth.view, render.Camera{
		ViewProj: a.camera.ViewProj(),
		Pos:      a.camera.Pos,
	}, avatars)
	if renderTiming != nil {
		renderTiming.recordAvatar(renderNow().Sub(started))
		started = renderNow()
	}
	a.itemDropInstances = appendItemDropInstances(
		a.itemDropInstances[:0], a.itemDrops.Presentations(),
	)
	a.itemDropRenderer.Render(encoder, target, a.depth.view, render.Camera{
		ViewProj: a.camera.ViewProj(),
		Pos:      a.camera.Pos,
	}, a.serverTick, a.itemDropInstances)
	right := mgl32.Vec3{
		float32(math.Cos(float64(a.camera.Yaw))),
		0,
		-float32(math.Sin(float64(a.camera.Yaw))),
	}
	a.nameTagRenderer.Render(encoder, target, a.depth.view, render.BillboardCamera{
		ViewProj: a.camera.ViewProj(),
		Right:    right,
		Up:       right.Cross(a.camera.Forward()).Normalize(),
	})
	if renderTiming != nil {
		renderTiming.recordNameTag(nameTagDuration + renderNow().Sub(started))
	}
	// HUD 在 terrain、avatar 与 name tag 之后绘制。
	if inventoryConfirmed {
		a.hotbarRenderer.Render(encoder, target)
	}
	command := encoder.Finish()
	a.dev.Submit(command)
	command.Release()
	if a.surface != nil {
		a.surface.Present()
	}
	return true, nil
}

func remoteRenderPresentations(presentations []client.RemotePresentation) ([]render.Avatar, []render.NameTag) {
	ordered := append([]client.RemotePresentation(nil), presentations...)
	slices.SortFunc(ordered, func(left, right client.RemotePresentation) int {
		return slices.Compare(left.PlayerID[:], right.PlayerID[:])
	})
	return remoteRenderPresentationsSortedInto(nil, nil, ordered)
}

func remoteRenderPresentationsSortedInto(
	avatars []render.Avatar,
	tags []render.NameTag,
	ordered []client.RemotePresentation,
) ([]render.Avatar, []render.NameTag) {
	for _, presentation := range ordered {
		avatars = append(avatars, render.Avatar{
			PlayerID: presentation.PlayerID, Position: presentation.Position,
			Yaw: presentation.Yaw, Pitch: presentation.Pitch,
		})
		tags = append(tags, render.NameTag{
			PlayerID: presentation.PlayerID,
			Text:     presentation.DisplayName,
			Anchor:   presentation.Position.Add(mgl32.Vec3{0, 2.05, 0}),
		})
	}
	return avatars, tags
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
			if state.ServerTick <= a.serverTick {
				continue
			}
			result, err := a.predictor.ApplyPlayerState(state, client.MirrorCollisionSource{
				Mirror:    a.mirror,
				Dimension: core.Overworld,
			})
			if err != nil {
				a.closeClientSession(err)
				return
			}
			a.serverTick = state.ServerTick
			if state.Reset || !state.MiningActive {
				a.miningOverlay = render.MiningOverlay{}
			} else {
				a.miningOverlay = render.MiningOverlay{
					Active:        true,
					ProgressTicks: state.MiningProgressTicks,
					RequiredTicks: state.MiningRequiredTicks,
					Harvestable:   state.MiningHarvestable,
				}
			}
			if state.Reset {
				if _, opened := a.furnace.State(); opened {
					a.clearFurnaceUI()
				} else {
					a.inventorySource = -1
				}
			}
			if result.ResetView {
				a.camera.Yaw = result.Yaw
				a.camera.Pitch = result.Pitch
			}
			continue
		}
		if state, ok := message.(network.InventoryState); ok {
			if err := a.inventory.Apply(state); err != nil {
				a.closeClientSession(err)
				return
			}
			continue
		}
		if state, ok := message.(network.FurnaceState); ok {
			previous, opened := a.furnace.Ref()
			if err := a.furnace.Apply(state); err != nil {
				a.closeClientSession(err)
				return
			}
			if !opened || previous != state.Furnace {
				a.inventorySource = -1
			}
			a.inventoryOpen = true
			if a.window != nil {
				a.window.SetCursorCaptured(false)
			}
			continue
		}
		if closed, ok := message.(network.FurnaceClosed); ok {
			current, opened := a.furnace.Ref()
			if err := a.furnace.Close(closed); err != nil {
				a.closeClientSession(err)
				return
			}
			if opened && current == closed.Furnace {
				a.clearFurnaceUI()
			}
			continue
		}
		switch message.(type) {
		case network.ItemDropUpserts, network.ItemDropRemoves:
			if err := a.itemDrops.Apply(message); err != nil {
				a.closeClientSession(err)
				return
			}
			continue
		}
		switch message.(type) {
		case network.RemotePlayerSpawn, *network.RemotePlayerSpawn,
			network.RemotePlayerDespawn, *network.RemotePlayerDespawn,
			network.RemotePlayerStates, *network.RemotePlayerStates:
			if err := a.remotePlayers.Apply(message); err != nil {
				a.closeClientSession(err)
				return
			}
			continue
		}
		update, err := a.mirror.Apply(message)
		if err != nil {
			a.closeClientSession(err)
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

func (a *application) placeBlock() {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	hit, found, err := core.RaycastBlocks(
		a.camera.Pos,
		a.camera.Forward(),
		6,
		func(position core.BlockPos) (bool, error) {
			block, loaded := a.mirror.BlockAt(core.Overworld, position)
			return loaded && block != core.AirID, nil
		},
	)
	if err != nil {
		log.Printf("本地熔炉射线失败: %v", err)
	} else if found {
		block, loaded := a.mirror.BlockAt(core.Overworld, hit.Block)
		if loaded && block == core.FurnaceID {
			if err := a.send(network.OpenFurnace{
				Sequence: a.nextSequence(), Yaw: a.camera.Yaw, Pitch: a.camera.Pitch,
			}); err != nil {
				log.Printf("发送打开熔炉请求失败: %v", err)
			}
			return
		}
	}
	// 放置引用最后一个已确认的选中栏位；尚未确认时不发送。
	hotbar, confirmed := a.inventory.Hotbar()
	if !confirmed {
		return
	}
	if err := a.send(network.PlaceBlock{
		Sequence: a.nextSequence(),
		Yaw:      a.camera.Yaw,
		Pitch:    a.camera.Pitch,
		Slot:     hotbar.Selected,
	}); err != nil {
		log.Printf("发送放置命令失败: %v", err)
	}
}

// setInventoryOpen 切换容器界面：显式关闭熔炉时立即清理并通知服务端。
func (a *application) setInventoryOpen(open bool) {
	if !open {
		if _, opened := a.furnace.State(); opened {
			a.clearFurnaceUI()
			if err := a.send(network.CloseFurnace{Sequence: a.nextSequence()}); err != nil {
				log.Printf("发送关闭熔炉请求失败: %v", err)
			}
			return
		}
	}
	a.inventoryOpen = open
	a.inventorySource = -1
	if a.window != nil {
		a.window.SetCursorCaptured(!open)
	}
	if open {
		// 立即发送一次中性输入，清除服务端保留的上一帧移动。
		a.applyInteractiveInput(0, client.Movement{}, client.Actions{}, false)
	}
}

// clearFurnaceUI 丢弃当前熔炉镜像并关闭容器界面，不发送协议消息。
func (a *application) clearFurnaceUI() {
	a.furnace.Reset()
	a.inventoryOpen = false
	a.inventorySource = -1
	if a.window != nil {
		a.window.SetCursorCaptured(true)
	}
}

// clickInventorySlot 处理固定配方按钮，或用两次有效栏位点击组成一次整堆移动请求。
func (a *application) clickInventorySlot(cursorX, cursorY float64, width, height uint32) {
	furnace, furnaceOpen := a.furnace.State()
	if !furnaceOpen {
		if recipe, ok := render.RecipeButtonAt(cursorX, cursorY, width, height); ok {
			inventory, confirmed := a.inventory.State()
			if !confirmed {
				return
			}
			if _, craftable := inventory.Craft(recipe); !craftable {
				return
			}
			a.inventorySource = -1
			if err := a.send(network.CraftRecipe{
				Sequence: a.nextSequence(), Recipe: recipe,
			}); err != nil {
				log.Printf("发送合成请求失败: %v", err)
			}
			return
		}
	}

	var slot uint8
	var ok bool
	if furnaceOpen {
		slot, ok = render.FurnaceSlotAt(cursorX, cursorY, width, height)
	} else {
		slot, ok = render.InventorySlotAt(cursorX, cursorY, width, height)
	}
	if !ok {
		return
	}
	if a.inventorySource < 0 {
		a.inventorySource = int(slot)
		return
	}
	from := uint8(a.inventorySource)
	a.inventorySource = -1
	if from == slot {
		return
	}
	if furnaceOpen {
		if slot == core.FurnaceOutputSlot {
			return
		}
		if err := a.send(network.MoveFurnaceStack{
			Sequence: a.nextSequence(), Furnace: furnace.Furnace, From: from, To: slot,
		}); err != nil {
			log.Printf("发送熔炉移动失败: %v", err)
		}
		return
	}
	if err := a.send(network.MoveInventoryStack{
		Sequence: a.nextSequence(), From: from, To: slot,
	}); err != nil {
		log.Printf("发送背包移动失败: %v", err)
	}
}

// selectHotbarSlot 只发送选择请求，不本地改写快捷栏镜像。
func (a *application) selectHotbarSlot(slot uint8) {
	if _, ready := a.predictor.State(); !ready {
		return
	}
	if err := a.send(network.SelectHotbar{
		Sequence: a.nextSequence(),
		Slot:     slot,
	}); err != nil {
		log.Printf("发送快捷栏选择失败: %v", err)
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

// appendItemDropInstances 把只读镜像转换为渲染实例，复用调用方切片。
func appendItemDropInstances(
	dst []render.ItemDrop,
	drops []client.ItemDropPresentation,
) []render.ItemDrop {
	for _, drop := range drops {
		block, ok := render.ItemDropBlock(drop.ID.Chunk, drop.BlockIndex)
		if !ok {
			continue
		}
		dst = append(dst, render.ItemDrop{ID: drop.ID, Block: block, Item: drop.Item})
	}
	return dst
}

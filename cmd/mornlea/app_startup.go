//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"slices"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"github.com/channing771/mornlea/internal/assets"
	"github.com/channing771/mornlea/internal/client"
	"github.com/channing771/mornlea/internal/config"
	"github.com/channing771/mornlea/internal/core"
	"github.com/channing771/mornlea/internal/network"
	"github.com/channing771/mornlea/internal/render"
	"github.com/channing771/mornlea/internal/render/hud"
	"github.com/channing771/mornlea/internal/server"
	"github.com/channing771/mornlea/internal/storage"
	"github.com/channing771/mornlea/internal/worldgen"
)

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
	// benchmark 与 capture 都要求世界状态与本机磁盘上的真实存档隔离：
	// benchmark 为了性能测量不被磁盘 I/O 干扰，capture 为了抓帧结果不随
	// "这台机器碰巧玩到哪一步"漂移——两者都复用内存 store 达成确定性初始状态。
	if options.Benchmark || options.CaptureDir != "" {
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
	// options.Render 的零值是一个静默退化的配置：ViewDistance=0 会让
	// DropOutside 在每帧把中心区块外的一切都丢弃。真实入口（cmd/mornlea/main.go）
	// 总是先经 resolveConfig 填好这个字段，这里只防漏填 Render 的调用方
	// （包括测试）静默跑在退化配置下而不报错。
	if options.Render.ViewDistance == 0 {
		options.Render = config.Defaults().Render
	}
	if dependencies.newGlyphAtlas == nil {
		dependencies.newGlyphAtlas = render.NewGlyphAtlasWithSink
	}
	if dependencies.newWindowedRenderer == nil {
		dependencies.newWindowedRenderer = defaultApplicationDependencies().newWindowedRenderer
	}
	if dependencies.newOffscreenRenderer == nil {
		dependencies.newOffscreenRenderer = client.NewRenderer
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
	config.Companions = slices.Clone(options.Companions)
	// 模型运行时与已解析密钥原样转发：均为值拷贝，不与 options 共享引用；
	// NewHost 会在打开存档前对非空伙伴列表做完整性校验。
	config.AIModel = options.AIModel
	config.AIAPIKey = options.AIAPIKey
	config.ViewRadius = options.Render.ViewDistance + 1
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
		running = server.NewWorld(config, worldgen.New(store.Metadata().Seed, options.FluidEnabled), store)
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
			worldgen.New(store.Metadata().Seed, options.FluidEnabled),
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
	var rustRenderer *client.Renderer
	width, height := 2560, 1440
	headless := options.Benchmark || options.CaptureDir != ""
	if options.CaptureDir != "" {
		width, height = captureWidth, captureHeight
	}
	if headless {
		rustRenderer, err = dependencies.newOffscreenRenderer(width, height)
	} else {
		window, err = dependencies.newWindow(2560, 1440, "Mornlea — M3C multiplayer world")
		if err == nil {
			fitFramebuffer(window, width, height)
			width, height = window.FramebufferSize()
			rustRenderer, err = dependencies.newWindowedRenderer(window)
		}
	}
	if err != nil {
		if rustRenderer != nil {
			rustRenderer.Close()
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
		FovY:   mgl32.DegToRad(options.Render.FovDegrees),
		Aspect: float32(width) / float32(height),
		Near:   0.1,
		Far:    2000,
	}
	app := &application{
		window:          window,
		renderer:        rustRenderer,
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
		companions:      &client.Companions{},
		chatEvents:      &client.ChatEvents{},
		remoteNameTags:  make([]render.NameTag, 0, maxFrameNameTags),
		camera:          camera,
		center:          cameraChunk(camera.Pos),
		loadedChunks:    make(map[core.ChunkPos]struct{}),
		ticks:           ticks,
		saves:           saves,
		render:          options.Render,
		benchmarkTransport: func() string {
			if options.BenchmarkTransport == "" {
				return "memory"
			}
			return options.BenchmarkTransport
		}(),
	}
	app.releaseResources = app.releaseOwnedResources
	// 材质与 HUD 图集一次性上传;mesh 上传调度经 SectionScheduler 下沉。
	atlasLayers, atlasPixels := reg.AtlasPixels()
	rustRenderer.UploadAtlas(atlasLayers, atlasPixels)
	app.scheduler = render.NewSectionScheduler(rustRenderer, applicationUploadPerFrame)
	app.glyphAtlas, err = dependencies.newGlyphAtlas(rendererGlyphSink{renderer: rustRenderer})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("创建字形图集: %w", err), app.Close())
	}
	app.nameTagRenderer = render.NewNameTagLayouter(app.glyphAtlas)
	app.hotbarRenderer = hud.NewHotbarLayout(app.glyphAtlas, reg)
	hudWidth, hudHeight, hudPixels := app.hotbarRenderer.AtlasPixels()
	rustRenderer.UploadHUDAtlas(hudWidth, hudHeight, hudPixels)
	app.configPath = options.ConfigPath
	if options.Dev {
		app.debugPanelRenderer = render.NewDebugPanelLayouter(app.glyphAtlas)
		// 面板的初始生效值取当前已生效的 physics/sim 快照（main.go 在构造
		// application 之前已经调用过 config.Config.Apply）与调用方传入的
		// Render，三组合起来与启动时真正生效的参数保持一致，不需要额外
		// 传一份完整 config.Config 进 applicationOptions。
		app.panel = newPanelStateFromActive(options.Render)
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

// applicationUploadPerFrame 是每帧 mesh 上传字节预算,与旧渲染器默认一致。
const applicationUploadPerFrame = 4 << 20

// rendererGlyphSink 把字形矩形写入适配到 Rust 渲染器上传入口。
type rendererGlyphSink struct{ renderer *client.Renderer }

// WriteGlyphRect 直传 client 渲染器的字形图集入口。
func (sink rendererGlyphSink) WriteGlyphRect(x, y, width, height uint32, pixels []byte) {
	sink.renderer.UploadGlyphRect(int(x), int(y), int(width), int(height), pixels)
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
	host, err := dependencies.newHost(ctx, config, generator, store)
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

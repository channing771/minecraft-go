//go:build darwin

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"minecraft-go/internal/client"
	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/gfx"
	"minecraft-go/internal/network"
	"minecraft-go/internal/server"
	"minecraft-go/internal/storage"
)

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
	if app.blockOutlineRenderer == nil || len(app.remoteNameTags) != 0 || cap(app.remoteNameTags) != maxFrameNameTags {
		t.Fatalf("remote target feedback renderer/tags=%v/%d/%d，想要非 nil/0/%d",
			app.blockOutlineRenderer != nil, len(app.remoteNameTags), cap(app.remoteNameTags), maxFrameNameTags)
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

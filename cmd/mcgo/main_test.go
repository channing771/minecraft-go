//go:build darwin

package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/profile"
)

// absentConfigArgs 返回指向本次测试临时目录下一个不存在文件的 --config 参数。
//
// runWithDependencies 接线后一路调用 resolveConfig -> config.DefaultPath，未加
// 这层隔离的非 benchmark/capture 用例会读到开发者本机
// ~/Library/Application Support/minecraft-go/config.json（若存在）并通过
// effective.Apply() 改写进程级 physics/sim 全局可调值——这正是 benchmark 隔离
// 规则要防的"结论取决于开发者本机"那类危害，只是下沉到了非 benchmark 路径。
// 指向不存在的文件让 config.Load 落回 config.Defaults()（见
// internal/config/config.go:88-91）。
func absentConfigArgs(t *testing.T) []string {
	t.Helper()
	return []string{"--config", filepath.Join(t.TempDir(), "absent.json")}
}

func TestParseMainOptionsRejectsRemoteLocalConflicts(t *testing.T) {
	for _, args := range [][]string{
		{"--connect", "127.0.0.1:25565", "--world", "worlds/demo"},
		{"--connect", "127.0.0.1:25565", "--benchmark", "--perf-output", "x.json"},
		{"--benchmark", "--perf-output", "x.json", "--name", "Chen"},
	} {
		if _, err := parseMainOptions(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestParseMainOptionsAllowsRemoteWithDefaultWorld(t *testing.T) {
	options, err := parseMainOptions([]string{"--connect", "127.0.0.1:25565"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Application.Connect != "127.0.0.1:25565" || options.Application.WorldPath != "worlds/default" {
		t.Fatalf("options=%+v", options.Application)
	}
}

func TestRunWithDependenciesLoadsProfileOnceForLocalAndRemote(t *testing.T) {
	for _, args := range [][]string{nil, {"--connect", "127.0.0.1:25565"}} {
		t.Run("mode", func(t *testing.T) {
			loads := 0
			identity := network.Identity{PlayerID: core.PlayerID{1}, DisplayName: "Chen"}
			err := runWithDependencies(append(append([]string{}, args...), absentConfigArgs(t)...), runDependencies{
				loadIdentity: func(requested *string) (network.Identity, error) {
					loads++
					return identity, nil
				},
				newApplication: func(options applicationOptions) (*application, error) {
					if options.Identity == nil || *options.Identity != identity {
						t.Fatalf("application identity=%+v", options.Identity)
					}
					return nil, errors.New("stop before window")
				},
			})
			if err == nil || loads != 1 {
				t.Fatalf("run error=%v profile loads=%d, want construction error and 1", err, loads)
			}
		})
	}
}

func TestRunWithDependenciesBypassesProfileForBenchmark(t *testing.T) {
	loads := 0
	err := runWithDependencies([]string{"--benchmark", "--perf-output", "x.json"}, runDependencies{
		loadIdentity: func(*string) (network.Identity, error) {
			loads++
			return network.Identity{}, nil
		},
		newApplication: func(options applicationOptions) (*application, error) {
			if options.Identity != nil {
				t.Fatalf("benchmark identity=%+v, want nil", options.Identity)
			}
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || loads != 0 {
		t.Fatalf("run error=%v profile loads=%d, want construction error and 0", err, loads)
	}
}

// TestRunWithDependenciesDisablesDevForBenchmark 守住"benchmark 产出不应受
// --dev 影响"：同时传 --benchmark 与 --dev 时，传给 newApplication 的
// options.Dev 必须被强制为 false，不能给 benchmark 进程构造面板渲染器、
// 占用它的 GPU 资源。
func TestRunWithDependenciesDisablesDevForBenchmark(t *testing.T) {
	sawCall := false
	var gotDev bool
	err := runWithDependencies([]string{"--benchmark", "--perf-output", "x.json", "--dev"}, runDependencies{
		loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
		newApplication: func(options applicationOptions) (*application, error) {
			sawCall = true
			gotDev = options.Dev
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || !sawCall {
		t.Fatalf("run error=%v sawCall=%v，想要构造期错误且确实调用了 newApplication", err, sawCall)
	}
	if gotDev {
		t.Fatal("--benchmark 必须让 --dev 失效：options.Dev = true")
	}
}

// TestRunWithDependenciesAlwaysEnablesDevForCapture 守住抓帧路径必须构造面板
// 渲染器这条契约。
//
// 该断言与它的前身相反，是有意的：早先抓帧被当作"与 benchmark 同类的基线路径"
// 而排除了 --dev。但 debug-panel 场景要拍的就是面板本身，而基线重生成与 CI
// 调用 capture 时都不会带 --dev——沿用旧规则会让那个场景永远拍到空画面。
//
// 两条基线路径的待遇本就不该一致：benchmark measures 性能，面板不该占用 GPU；
// capture 记录画面，面板是被记录的对象之一。面板默认隐藏，只有该场景的 Apply
// 打开它，因此其余场景的基线不受影响。
func TestRunWithDependenciesAlwaysEnablesDevForCapture(t *testing.T) {
	for _, test := range []struct {
		name string
		dev  bool
	}{
		{"带 --dev", true},
		{"不带 --dev", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sawCall := false
			var gotDev bool
			args := []string{"--capture", t.TempDir()}
			if test.dev {
				args = append(args, "--dev")
			}
			args = append(args, absentConfigArgs(t)...)
			err := runWithDependencies(args, runDependencies{
				loadIdentity: func(*string) (network.Identity, error) { return network.Identity{}, nil },
				newApplication: func(options applicationOptions) (*application, error) {
					sawCall = true
					gotDev = options.Dev
					return nil, errors.New("stop before window")
				},
			})
			if err == nil || !sawCall {
				t.Fatalf("run error=%v sawCall=%v，想要构造期错误且确实调用了 newApplication", err, sawCall)
			}
			if !gotDev {
				t.Fatal("--capture 必须构造面板渲染器：options.Dev = false，debug-panel 场景会拍到空画面")
			}
		})
	}
}

func TestRunWithDependenciesPassesExplicitNameToProfile(t *testing.T) {
	name := "Chen"
	var got *string
	err := runWithDependencies(append([]string{"--name", name}, absentConfigArgs(t)...), runDependencies{
		loadIdentity: func(requested *string) (network.Identity, error) {
			got = requested
			return network.Identity{}, nil
		},
		newApplication: func(applicationOptions) (*application, error) {
			return nil, errors.New("stop before window")
		},
	})
	if err == nil || got == nil || *got != name {
		t.Fatalf("run error=%v requested name=%v", err, got)
	}
}

func TestParseMainOptionsBenchmarkTransport(t *testing.T) {
	defaults, err := parseMainOptions([]string{"--benchmark", "--perf-output", "x.json"})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Application.BenchmarkTransport != "memory" {
		t.Fatalf("default benchmark transport=%q, want memory", defaults.Application.BenchmarkTransport)
	}
	tcp, err := parseMainOptions([]string{
		"--benchmark", "--benchmark-transport", "tcp", "--perf-output", "x.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if tcp.Application.BenchmarkTransport != "tcp" {
		t.Fatalf("TCP benchmark transport=%q", tcp.Application.BenchmarkTransport)
	}
	for _, args := range [][]string{
		{"--benchmark-transport", "tcp"},
		{"--benchmark", "--benchmark-transport", "udp", "--perf-output", "x.json"},
	} {
		if _, err := parseMainOptions(args); err == nil {
			t.Fatalf("accepted invalid benchmark transport args %v", args)
		}
	}
}

// Mutation killed: ignoring renderFrame's error makes the interactive loop
// continue after a glyph worker failure instead of returning it immediately.
func TestRunInteractivePropagatesRemoteGlyphError(t *testing.T) {
	wantErr := errors.New("interactive glyph worker failure")
	app, _ := newRemoteRenderApplication(t, &integrationGlyphSource{flushErr: wantErr})
	app.window = &oneFrameInteractiveWindow{delay: 25 * time.Millisecond}
	clientEndpoint, serverEndpoint := network.NewMemoryPair(4)
	app.clientEndpoint = clientEndpoint
	app.receiver = client.NewReceiver(clientEndpoint, 4)
	t.Cleanup(func() { _ = serverEndpoint.Close() })
	if err := app.remotePlayers.Apply(remoteSpawn(1, "Remote-1", 1, mgl32.Vec3{})); err != nil {
		t.Fatal(err)
	}
	for index, x := range []float32{4, 8} {
		if err := app.remotePlayers.Apply(network.RemotePlayerStates{
			ServerTick: uint64(index + 2),
			Players: []network.RemotePlayerState{{
				PlayerID: integrationPlayerID(1), Dimension: core.Overworld,
				Position: mgl32.Vec3{x, 0, 0},
			}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := runInteractive(app); !errors.Is(err, wantErr) {
		t.Fatalf("runInteractive error=%v want wrapped glyph error", err)
	}
	if x := app.remotePlayers.Presentations()[0].Position[0]; x < 1.5 || x > 3 {
		t.Fatalf("interactive interpolation x=%f want elapsed-driven midpoint range", x)
	}
}

func TestInteractiveInputCarriesMiningOnlyWhenActionsAllowed(t *testing.T) {
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
	app.camera.Yaw = 0.75
	app.camera.Pitch = -0.2

	app.applyInteractiveInput(
		physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, true,
	)
	mining, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || !mining.Mining || mining.Yaw != 0.75 || mining.Pitch != -0.2 {
		t.Fatalf("允许操作时 fixed-step input=%+v", mining)
	}

	app.applyInteractiveInput(
		physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, false,
	)
	neutral, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || neutral.Mining {
		t.Fatalf("抑制操作时 fixed-step input=%+v，想要 Mining=false", neutral)
	}
}

func TestInteractiveCursorInputSuppressesStaleMiningAfterInventoryOpens(t *testing.T) {
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

	// 模拟同帧采样到按住主键后，E 键才打开背包的顺序。
	app.setInventoryOpen(true)
	app.applyInteractiveCursorInput(
		physics.FixedDelta, client.Movement{}, client.Actions{Mining: true}, true, false,
	)
	input, ok := receiveInteractiveClientMessage(t, serverEndpoint).(network.PlayerInput)
	if !ok || input.Mining {
		t.Fatalf("打开背包后的 stale input=%+v，想要 Mining=false", input)
	}
}

type oneFrameInteractiveWindow struct {
	fakeInteractiveWindow
	polled bool
	delay  time.Duration
}

func (window *oneFrameInteractiveWindow) ShouldClose() bool { return window.polled }
func (window *oneFrameInteractiveWindow) Poll() {
	time.Sleep(window.delay)
	window.polled = true
}
func (*oneFrameInteractiveWindow) FramebufferSize() (int, int) { return 16, 16 }

func TestParseMainOptionsCaptureDir(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots"})
	if err != nil {
		t.Fatalf("解析 --capture 失败: %v", err)
	}
	if opts.CaptureDir != "/tmp/shots" {
		t.Fatalf("CaptureDir = %q，想要 %q", opts.CaptureDir, "/tmp/shots")
	}
	if opts.Application.CaptureDir != "/tmp/shots" {
		t.Fatalf("Application.CaptureDir = %q，想要 %q", opts.Application.CaptureDir, "/tmp/shots")
	}
}

func TestParseMainOptionsCaptureRejectsConflicts(t *testing.T) {
	// --capture 与 --benchmark 都会独占无头渲染路径并各自驱动场景，
	// 同时开启的语义无法定义，必须直接拒绝而不是让某一方静默胜出。
	tests := []struct {
		name string
		args []string
	}{
		{"与 benchmark 互斥", []string{"--capture", "/tmp/shots", "--benchmark", "--perf-output", "/tmp/p.json"}},
		{"与 connect 互斥", []string{"--capture", "/tmp/shots", "--connect", "127.0.0.1:25565"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseMainOptions(tc.args); err == nil {
				t.Fatal("想要报错，实际通过")
			}
		})
	}
}

func TestParseMainOptionsUpdateGoldenRequiresCapture(t *testing.T) {
	if _, err := parseMainOptions([]string{"--update-golden"}); err == nil {
		t.Fatal("--update-golden 缺少 --capture 时想要报错，实际通过")
	}
}

func TestParseMainOptionsUpdateGoldenWithCapturePropagates(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots", "--update-golden"})
	if err != nil {
		t.Fatalf("解析 --capture --update-golden 失败: %v", err)
	}
	if !opts.UpdateGolden {
		t.Fatal("UpdateGolden = false，想要 true")
	}
}

func TestParseMainOptionsWithoutUpdateGoldenDefaultsFalse(t *testing.T) {
	opts, err := parseMainOptions([]string{"--capture", "/tmp/shots"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.UpdateGolden {
		t.Fatal("UpdateGolden = true，想要默认 false")
	}
}

func TestParseMainOptionsWithoutCaptureLeavesDirEmpty(t *testing.T) {
	opts, err := parseMainOptions(nil)
	if err != nil {
		t.Fatalf("解析空参数失败: %v", err)
	}
	if opts.CaptureDir != "" {
		t.Fatalf("CaptureDir = %q，想要空", opts.CaptureDir)
	}
}

var _ = profile.Options{}

func TestParseOptionsDefaultsDevOff(t *testing.T) {
	options, err := parseMainOptions([]string{})
	if err != nil {
		t.Fatalf("parseMainOptions: %v", err)
	}
	if options.Dev {
		t.Fatal("--dev 默认必须关闭")
	}
}

func TestParseOptionsAcceptsDevAndConfig(t *testing.T) {
	options, err := parseMainOptions([]string{"--dev", "--config", "/tmp/x.json"})
	if err != nil {
		t.Fatalf("parseMainOptions: %v", err)
	}
	if !options.Dev {
		t.Fatal("--dev 必须被解析")
	}
	if options.ConfigPath != "/tmp/x.json" {
		t.Fatalf("ConfigPath = %q", options.ConfigPath)
	}
}

// TestBenchmarkIgnoresUserConfig 守住"性能门禁不读本机配置"这条不变量。
func TestBenchmarkIgnoresUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	custom := config.Defaults()
	custom.Physics.Gravity = 1
	if err := custom.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	effective, err := resolveConfig(mainOptions{
		ConfigPath:  path,
		Application: applicationOptions{Benchmark: true},
	})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("benchmark 路径必须使用编译默认值，不得读用户配置")
	}
}

// TestRemoteTuningDivergenceWarnCondition 覆盖设计 §3.2 在配置文件这条路径上的
// 缺口：面板用 fieldReadOnly 挡住了联机时改写 physics/sim，但配置文件是始终
// 生效的（§3.1），它绕过那道锁。这里钉住告警条件——只有"连远端 + 这两组偏离
// 默认值"才告警，单机或联机但全默认都不该打扰用户。
//
// 只告警不强制回落默认值：README 把这份配置文件描述为 mcgo 与 mcgod 共用，
// 局域网下两端读同一份调过的文件恰恰是正确用法。
func TestRemoteTuningDivergenceWarnCondition(t *testing.T) {
	tuned := config.Defaults()
	tuned.Physics.Gravity = 12
	tunedSim := config.Defaults()
	tunedSim.Sim.InteractionReach = 3

	cases := []struct {
		name      string
		connect   string
		effective config.Config
		want      bool
	}{
		{name: "联机且物理组偏离默认值", connect: "127.0.0.1:7777", effective: tuned, want: true},
		{name: "联机且模拟组偏离默认值", connect: "127.0.0.1:7777", effective: tunedSim, want: true},
		{name: "联机但全部为默认值", connect: "127.0.0.1:7777", effective: config.Defaults()},
		{name: "单机且物理组偏离默认值", effective: tuned},
		{name: "单机且全部为默认值", effective: config.Defaults()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			options := mainOptions{Application: applicationOptions{Connect: testCase.connect}}
			if got := remoteTuningDiverges(options, testCase.effective); got != testCase.want {
				t.Fatalf("remoteTuningDiverges = %v，want %v", got, testCase.want)
			}
		})
	}
}

func TestCaptureIgnoresUserConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	custom := config.Defaults()
	custom.Physics.Gravity = 1
	if err := custom.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	t.Cleanup(func() { config.Defaults().Apply() })

	effective, err := resolveConfig(mainOptions{ConfigPath: path, CaptureDir: "out"})
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if effective.Physics.Gravity != config.Defaults().Physics.Gravity {
		t.Fatal("抓帧路径必须使用编译默认值，不得读用户配置")
	}
}

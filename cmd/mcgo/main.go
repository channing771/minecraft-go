//go:build darwin

// Command mcgo 启动 M3B TCP 直连与持久世界客户端。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/config"
	"minecraft-go/internal/core"
	"minecraft-go/internal/logging"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
	"minecraft-go/internal/profile"
)

const steadyFrameMeshWorkMax = 64

func init() {
	runtime.LockOSThread()
}

type mainOptions struct {
	Application   applicationOptions
	PerfOutput    string
	RequestedName *string
	CaptureDir    string
	// UpdateGolden 为真时，抓帧结果写入 golden 基线而不是与之比对。
	// 与 applicationOptions 无关：它只影响 runCapture 的行为，从
	// runWithDependencies 直接传给 dependencies.runCapture。
	UpdateGolden bool
	// ConfigPath 是调参配置文件路径；留空表示使用 config.DefaultPath()。
	ConfigPath string
	// Dev 为真时启用调试面板（F3 切换）。它只门控面板可用性，不门控配置文件
	// 是否生效——配置文件里调过的值无论 Dev 是否为真都同样生效。
	Dev bool
}

type runDependencies struct {
	newApplication func(applicationOptions) (*application, error)
	loadIdentity   func(*string) (network.Identity, error)
	runInteractive func(*application) error
	runBenchmark   func(*application, string) error
	runCapture     func(*application, string, bool) error
}

func parseMainOptions(args []string) (mainOptions, error) {
	flags := flag.NewFlagSet("mcgo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	benchmark := flags.Bool("benchmark", false, "运行固定 1440p 性能场景")
	benchmarkTransport := flags.String("benchmark-transport", "memory", "benchmark 业务传输: memory 或 tcp")
	perfOutput := flags.String("perf-output", "", "性能报告 JSON 输出路径")
	worldPath := flags.String("world", "worlds/default", "世界存档目录")
	connect := flags.String("connect", "", "远程 TCP 服务器地址")
	name := flags.String("name", "", "玩家显示名")
	capture := flags.String("capture", "", "视觉抓帧输出目录；非空时走无头抓帧模式")
	updateGolden := flags.Bool("update-golden", false, "把本次抓帧结果写入 golden 基线")
	dev := flags.Bool("dev", false, "启用调试面板（F3 切换）")
	configPath := flags.String("config", "", "配置文件路径，留空使用默认路径")
	if err := flags.Parse(args); err != nil {
		return mainOptions{}, err
	}
	if flags.NArg() != 0 {
		return mainOptions{}, fmt.Errorf("未知位置参数: %v", flags.Args())
	}
	if *benchmark && *perfOutput == "" {
		return mainOptions{}, errors.New("--benchmark 必须同时提供 --perf-output")
	}
	if *capture != "" && *benchmark {
		return mainOptions{}, errors.New("--capture 不能与 --benchmark 同时使用")
	}
	if *capture != "" && *connect != "" {
		return mainOptions{}, errors.New("--capture 不能与 --connect 同时使用")
	}
	if *updateGolden && *capture == "" {
		return mainOptions{}, errors.New("--update-golden 只能与 --capture 同时使用")
	}
	var worldExplicit, nameExplicit, benchmarkTransportExplicit bool
	flags.Visit(func(flag *flag.Flag) {
		worldExplicit = worldExplicit || flag.Name == "world"
		nameExplicit = nameExplicit || flag.Name == "name"
		benchmarkTransportExplicit = benchmarkTransportExplicit || flag.Name == "benchmark-transport"
	})
	if *connect != "" && worldExplicit {
		return mainOptions{}, errors.New("--connect 不能与显式 --world 同时使用")
	}
	if *connect != "" && *benchmark {
		return mainOptions{}, errors.New("--connect 不能与 --benchmark 同时使用")
	}
	if *benchmark && nameExplicit {
		return mainOptions{}, errors.New("--name 不能与 --benchmark 同时使用")
	}
	if benchmarkTransportExplicit && !*benchmark {
		return mainOptions{}, errors.New("--benchmark-transport 只能与 --benchmark 同时使用")
	}
	if *benchmarkTransport != "memory" && *benchmarkTransport != "tcp" {
		return mainOptions{}, fmt.Errorf("无效 --benchmark-transport %q：只支持 memory 或 tcp", *benchmarkTransport)
	}
	seed := int64(42)
	if *benchmark {
		seed = benchmarkSeed
	}
	return mainOptions{
		Application: applicationOptions{
			Seed:               seed,
			Benchmark:          *benchmark,
			BenchmarkTransport: *benchmarkTransport,
			WorldPath:          *worldPath,
			Connect:            *connect,
			CaptureDir:         *capture,
		},
		PerfOutput: *perfOutput,
		RequestedName: func() *string {
			if nameExplicit {
				return name
			}
			return nil
		}(),
		CaptureDir:   *capture,
		UpdateGolden: *updateGolden,
		ConfigPath:   *configPath,
		Dev:          *dev,
	}, nil
}

// resolveConfigPath 决定调参配置文件的实际路径：显式 --config 优先，
// 否则落回用户配置目录下的默认路径。
func resolveConfigPath(options mainOptions) (string, error) {
	if options.ConfigPath != "" {
		return options.ConfigPath, nil
	}
	return config.DefaultPath()
}

// resolveConfig 决定本次运行的生效配置。
//
// benchmark 与抓帧路径强制使用编译默认值：这两条路径的产出会与基线比对，
// 若读入本机配置，结论就取决于开发者本机的配置文件内容而非代码。
func resolveConfig(options mainOptions) (config.Config, error) {
	if options.Application.Benchmark || options.CaptureDir != "" {
		return config.Defaults(), nil
	}
	path, err := resolveConfigPath(options)
	if err != nil {
		return config.Config{}, err
	}
	return config.Load(path)
}

func run(args []string) error {
	return runWithDependencies(args, runDependencies{
		newApplication: newApplication,
		loadIdentity:   loadApplicationIdentity,
		runInteractive: runInteractive,
		runBenchmark:   runBenchmark,
		runCapture:     runCapture,
	})
}

func runWithDependencies(args []string, dependencies runDependencies) error {
	options, err := parseMainOptions(args)
	if err != nil {
		return err
	}
	if !options.Application.Benchmark {
		identity, err := dependencies.loadIdentity(options.RequestedName)
		if err != nil {
			return fmt.Errorf("加载本机 profile: %w", err)
		}
		options.Application.Identity = &identity
	}

	effective, err := resolveConfig(options)
	if err != nil {
		return fmt.Errorf("加载配置: %w", err)
	}
	// 内层 handler 的 Level 固定为 LevelDebug：过滤全部交给 logging 包的包装器，
	// 内层不得二次过滤，否则模块放宽会失效。
	logging.Install(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}), effective.Logging)
	effective.Apply()
	options.Application.Dev = options.Dev
	options.Application.Render = effective.Render
	// 面板 F5 保存需要落盘路径；benchmark 与抓帧路径不进交互循环，不需要它。
	if !options.Application.Benchmark && options.CaptureDir == "" {
		configPath, err := resolveConfigPath(options)
		if err != nil {
			return fmt.Errorf("解析配置文件路径: %w", err)
		}
		options.Application.ConfigPath = configPath
	}

	app, err := dependencies.newApplication(options.Application)
	if err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}

	if options.CaptureDir != "" {
		return errors.Join(
			dependencies.runCapture(app, options.CaptureDir, options.UpdateGolden),
			app.Close(),
		)
	}

	var runErr error
	if options.Application.Benchmark {
		if err := dependencies.runBenchmark(app, options.PerfOutput); err != nil {
			runErr = fmt.Errorf("性能门禁失败: %w", err)
		}
	} else {
		runErr = dependencies.runInteractive(app)
	}
	return errors.Join(runErr, app.Close())
}

func loadApplicationIdentity(requestedName *string) (network.Identity, error) {
	path, err := profile.DefaultPath()
	if err != nil {
		return network.Identity{}, err
	}
	loaded, err := profile.LoadOrCreate(profile.Options{Path: path, RequestedName: requestedName})
	if err != nil {
		return network.Identity{}, err
	}
	return network.Identity{PlayerID: loaded.PlayerID, DisplayName: loaded.DisplayName}, nil
}

// clientMemoryLimit 是客户端进程的 Go 堆软上限。
//
// 视距 32 下快速移动会造成密集的区块加载、卸载与网格化周转。Go 默认要等堆长到
// 活跃集的两倍才回收，实测这会让堆保留冲到 1635MiB，而其中约 400MiB 只是尚未
// 回收的空闲堆。设定软上限让 GC 在接近该值时更积极，实测把进程 RSS 峰值压低约
// 121MiB，活跃数据与帧时间分位数均不受影响。
//
// 取值需高于实测活跃堆峰值（约 1252MiB），否则 GC 会因长期贴近上限而频繁运行。
const clientMemoryLimit = 1500 << 20

func main() {
	debug.SetMemoryLimit(clientMemoryLimit)
	if err := run(os.Args[1:]); err != nil {
		slog.Error("mcgo 退出失败", "error", err)
		os.Exit(1)
	}
}

func runInteractive(app *application) error {
	app.window.SetCursorCaptured(true)
	lastMouseX, lastMouseY := app.window.CursorPos()
	lastFrame := time.Now()
	escapeWasDown := false
	clickWasDown := false
	panelToggleWasDown := false
	panelUpWasDown := false
	panelDownWasDown := false
	panelLeftWasDown := false
	panelRightWasDown := false
	panelEnterWasDown := false
	panelSaveWasDown := false
	panelResetAllWasDown := false
	var input client.InputState

	for !app.window.ShouldClose() {
		app.window.Poll()

		now := time.Now()
		dt := min(now.Sub(lastFrame), 100*time.Millisecond)
		lastFrame = now
		app.drainServerMessages(64)
		if err := app.receiver.Err(); err != nil {
			app.closeClientSession(err)
			return err
		}

		escapeDown := app.window.KeyDown(client.KeyEscape)
		if escapeDown && !escapeWasDown {
			// 背包打开时 Escape 只关闭背包并重新捕获鼠标。
			if app.inventoryOpen {
				app.setInventoryOpen(false)
				lastMouseX, lastMouseY = app.window.CursorPos()
			} else {
				app.window.SetCursorCaptured(false)
			}
		}
		escapeWasDown = escapeDown

		// 调试面板按键：F3/F5/F6、方向键与 Enter 都是边沿触发（按一下走一步），
		// Shift/Alt 是电平读取的修饰键。面板不存在时（未开 --dev）整段直接跳过，
		// 方向键既不驱动面板、也从不驱动玩家移动（移动只读 WASD）。
		if app.panel != nil {
			toggleDown := app.window.KeyDown(client.KeyF3)
			upDown := app.window.KeyDown(client.KeyUp)
			downDown := app.window.KeyDown(client.KeyDown)
			leftDown := app.window.KeyDown(client.KeyLeft)
			rightDown := app.window.KeyDown(client.KeyRight)
			enterDown := app.window.KeyDown(client.KeyEnter)
			saveDown := app.window.KeyDown(client.KeyF5)
			resetAllDown := app.window.KeyDown(client.KeyF6)

			keys := panelKeys{
				Toggle:   toggleDown && !panelToggleWasDown,
				Up:       upDown && !panelUpWasDown,
				Down:     downDown && !panelDownWasDown,
				Left:     leftDown && !panelLeftWasDown,
				Right:    rightDown && !panelRightWasDown,
				Enter:    enterDown && !panelEnterWasDown,
				Save:     saveDown && !panelSaveWasDown,
				ResetAll: resetAllDown && !panelResetAllWasDown,
				Shift:    app.window.KeyDown(client.KeyLeftShift),
				Alt:      app.window.KeyDown(client.KeyLeftAlt),
			}
			panelToggleWasDown = toggleDown
			panelUpWasDown = upDown
			panelDownWasDown = downDown
			panelLeftWasDown = leftDown
			panelRightWasDown = rightDown
			panelEnterWasDown = enterDown
			panelSaveWasDown = saveDown
			panelResetAllWasDown = resetAllDown

			if app.panel.handleKeys(keys, app.remote()) {
				app.applyPanelChange()
			}
			if keys.Save {
				if err := app.panel.save(app.configPath); err != nil {
					slog.Warn("保存调试面板配置失败", "error", err)
				}
			}
		}

		clickDown := app.window.PrimaryButtonDown()
		justCaptured := false
		if clickDown && !clickWasDown && !app.window.CursorCaptured() && !app.inventoryOpen {
			app.window.SetCursorCaptured(true)
			lastMouseX, lastMouseY = app.window.CursorPos()
			justCaptured = true
		}
		clickWasDown = clickDown
		captured := app.window.CursorCaptured()
		if captured && !justCaptured && !app.inventoryOpen {
			mouseX, mouseY := app.window.CursorPos()
			// baseMouseSensitivity 是键鼠灵敏度默认为 1 时对应的原始弧度/像素系数；
			// Render.MouseSensitivity 是相对该基线的倍率，默认值 1 保持行为不变。
			const baseMouseSensitivity = 0.002
			sensitivity := baseMouseSensitivity * app.render.MouseSensitivity
			app.camera.Rotate(
				float32(mouseX-lastMouseX)*sensitivity,
				float32(lastMouseY-mouseY)*sensitivity,
			)
			lastMouseX, lastMouseY = mouseX, mouseY
		}

		number := pressedHotbarNumber(app.window)
		actions := input.Update(
			clickDown, app.window.SecondaryButtonDown(), number,
			app.window.KeyDown(client.KeyE), app.window.KeyDown(client.KeyQ),
			app.inventoryOpen,
		)
		if actions.ToggleInventory {
			app.setInventoryOpen(!app.inventoryOpen)
			if !app.inventoryOpen {
				lastMouseX, lastMouseY = app.window.CursorPos()
			}
		}
		if app.inventoryOpen && actions.Click {
			width, height := app.framebufferSize()
			cursorX, cursorY := app.window.CursorPos()
			app.clickInventorySlot(cursorX, cursorY, uint32(width), uint32(height))
		}

		movement := client.MovementFromKeys(
			app.window.KeyDown(client.KeyW),
			app.window.KeyDown(client.KeyA),
			app.window.KeyDown(client.KeyS),
			app.window.KeyDown(client.KeyD),
			app.window.KeyDown(client.KeySpace),
		)
		if app.inventoryOpen {
			// 界面打开时持续发送中性输入，避免服务端沿用上一帧移动。
			movement = client.Movement{}
		}
		app.applyInteractiveCursorInput(dt, movement, actions, captured, justCaptured)
		app.remotePlayers.Advance(dt)
		if _, err := app.renderFrame(steadyFrameMeshWorkMax); err != nil {
			return err
		}
	}
	return nil
}

func (a *application) applyInteractiveCursorInput(
	elapsed time.Duration,
	movement client.Movement,
	actions client.Actions,
	captured bool,
	justCaptured bool,
) {
	if !captured {
		movement = client.Movement{}
	}
	a.applyInteractiveInput(elapsed, movement, actions, captured && !justCaptured && !a.inventoryOpen)
}

// pressedHotbarNumber 返回当前按下的快捷栏数字键 1..9，没有按下时返回 0。
func pressedHotbarNumber(window applicationWindow) int {
	for index := range core.HotbarSlots {
		if window.KeyDown(client.Key1 + client.Key(index)) {
			return index + 1
		}
	}
	return 0
}

func (a *application) applyInteractiveInput(
	elapsed time.Duration,
	movement client.Movement,
	actions client.Actions,
	allowActions bool,
) {
	if allowActions {
		if actions.Select {
			a.selectHotbarSlot(actions.SelectSlot)
		}
		if actions.Place {
			a.placeBlock()
		}
		if actions.Drop {
			a.dropSelectedItem()
		}
	}

	if _, ready := a.predictor.State(); !ready {
		return
	}
	control := client.Control{
		MoveX:  movement.MoveX,
		MoveZ:  movement.MoveZ,
		Jump:   movement.Jump,
		Yaw:    a.camera.Yaw,
		Pitch:  a.camera.Pitch,
		Mining: allowActions && actions.Mining,
	}
	if err := a.predictor.Advance(
		elapsed,
		control,
		client.MirrorCollisionSource{Mirror: a.mirror, Dimension: core.Overworld},
		a.nextSequence,
		func(input network.PlayerInput) error { return a.send(input) },
	); err != nil {
		slog.Warn("推进玩家预测失败", "error", err)
	}
	if feet, ok := a.predictor.PresentationPosition(elapsed); ok {
		// 相机视线高度必须与服务端交互射线原点使用同一份参数，否则玩家瞄准的方块
		// 与服务端判定的方块不是同一个。
		a.camera.Pos = feet.Add(mgl32.Vec3{0, physics.ActiveTunables().EyeHeight, 0})
		a.center = cameraChunk(a.camera.Pos)
	}
}

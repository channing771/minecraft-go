//go:build darwin

// Command mcgo 启动 M3A 持久世界客户端。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/go-gl/mathgl/mgl32"

	"minecraft-go/internal/client"
	"minecraft-go/internal/core"
	"minecraft-go/internal/network"
	"minecraft-go/internal/physics"
)

const viewDistance = 32

func init() {
	runtime.LockOSThread()
}

type mainOptions struct {
	Application applicationOptions
	PerfOutput  string
}

type runDependencies struct {
	newApplication func(applicationOptions) (*application, error)
	runInteractive func(*application)
	runBenchmark   func(*application, string) error
}

func parseMainOptions(args []string) (mainOptions, error) {
	flags := flag.NewFlagSet("mcgo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	benchmark := flags.Bool("benchmark", false, "运行固定 1440p 性能场景")
	perfOutput := flags.String("perf-output", "", "性能报告 JSON 输出路径")
	worldPath := flags.String("world", "worlds/default", "世界存档目录")
	if err := flags.Parse(args); err != nil {
		return mainOptions{}, err
	}
	if flags.NArg() != 0 {
		return mainOptions{}, fmt.Errorf("未知位置参数: %v", flags.Args())
	}
	if *benchmark && *perfOutput == "" {
		return mainOptions{}, errors.New("--benchmark 必须同时提供 --perf-output")
	}
	seed := int64(42)
	if *benchmark {
		seed = benchmarkSeed
	}
	return mainOptions{
		Application: applicationOptions{
			Seed:      seed,
			Benchmark: *benchmark,
			WorldPath: *worldPath,
		},
		PerfOutput: *perfOutput,
	}, nil
}

func run(args []string) error {
	return runWithDependencies(args, runDependencies{
		newApplication: newApplication,
		runInteractive: runInteractive,
		runBenchmark:   runBenchmark,
	})
}

func runWithDependencies(args []string, dependencies runDependencies) error {
	options, err := parseMainOptions(args)
	if err != nil {
		return err
	}
	app, err := dependencies.newApplication(options.Application)
	if err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}

	var runErr error
	if options.Application.Benchmark {
		if err := dependencies.runBenchmark(app, options.PerfOutput); err != nil {
			runErr = fmt.Errorf("性能门禁失败: %w", err)
		}
	} else {
		dependencies.runInteractive(app)
	}
	return errors.Join(runErr, app.Close())
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("mcgo: %v", err)
		os.Exit(1)
	}
}

func runInteractive(app *application) {
	app.window.SetCursorCaptured(true)
	lastMouseX, lastMouseY := app.window.CursorPos()
	lastFrame := time.Now()
	escapeWasDown := false
	clickWasDown := false
	var input client.InputState

	for !app.window.ShouldClose() {
		app.window.Poll()

		now := time.Now()
		dt := min(now.Sub(lastFrame), 100*time.Millisecond)
		lastFrame = now
		app.drainServerMessages(64)

		escapeDown := app.window.KeyDown(client.KeyEscape)
		if escapeDown && !escapeWasDown {
			app.window.SetCursorCaptured(false)
		}
		escapeWasDown = escapeDown

		clickDown := app.window.PrimaryButtonDown()
		justCaptured := false
		if clickDown && !clickWasDown && !app.window.CursorCaptured() {
			app.window.SetCursorCaptured(true)
			lastMouseX, lastMouseY = app.window.CursorPos()
			justCaptured = true
		}
		clickWasDown = clickDown
		captured := app.window.CursorCaptured()
		if captured && !justCaptured {
			mouseX, mouseY := app.window.CursorPos()
			app.camera.Rotate(
				float32(mouseX-lastMouseX)*0.002,
				float32(lastMouseY-mouseY)*0.002,
			)
			lastMouseX, lastMouseY = mouseX, mouseY
		}

		number := 0
		switch {
		case app.window.KeyDown(client.Key1):
			number = 1
		case app.window.KeyDown(client.Key2):
			number = 2
		case app.window.KeyDown(client.Key3):
			number = 3
		}
		actions := input.Update(clickDown, app.window.SecondaryButtonDown(), number)

		movement := client.MovementFromKeys(
			app.window.KeyDown(client.KeyW),
			app.window.KeyDown(client.KeyA),
			app.window.KeyDown(client.KeyS),
			app.window.KeyDown(client.KeyD),
			app.window.KeyDown(client.KeySpace),
		)
		app.applyInteractiveCursorInput(dt, movement, actions, captured, justCaptured)
		app.renderFrame(64)
	}
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
	a.applyInteractiveInput(elapsed, movement, actions, captured && !justCaptured)
}

func (a *application) applyInteractiveInput(
	elapsed time.Duration,
	movement client.Movement,
	actions client.Actions,
	allowActions bool,
) {
	a.selectedBlock = actions.SelectedBlock
	if allowActions {
		if actions.Break {
			a.breakBlock()
		}
		if actions.Place {
			a.placeBlock()
		}
	}

	if _, ready := a.predictor.State(); !ready {
		return
	}
	control := client.Control{
		MoveX: movement.MoveX,
		MoveZ: movement.MoveZ,
		Jump:  movement.Jump,
		Yaw:   a.camera.Yaw,
		Pitch: a.camera.Pitch,
	}
	if err := a.predictor.Advance(
		elapsed,
		control,
		client.MirrorCollisionSource{Mirror: a.mirror, Dimension: core.Overworld},
		a.nextSequence,
		func(input network.PlayerInput) error { return a.send(input) },
	); err != nil {
		log.Printf("推进玩家预测失败: %v", err)
	}
	if feet, ok := a.predictor.PresentationPosition(elapsed); ok {
		a.camera.Pos = feet.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
		a.center = cameraChunk(a.camera.Pos)
	}
}

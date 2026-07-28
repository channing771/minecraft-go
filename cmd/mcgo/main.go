//go:build darwin

// Command mcgo 启动 M2B 权威地面玩家客户端。
package main

import (
	"flag"
	"log"
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

func main() {
	benchmark := flag.Bool("benchmark", false, "运行固定 1440p 性能场景")
	perfOutput := flag.String("perf-output", "", "性能报告 JSON 输出路径")
	flag.Parse()

	seed := int64(42)
	if *benchmark {
		seed = benchmarkSeed
	}
	app, err := newApplication(seed, *benchmark)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	defer app.Close()

	if *benchmark {
		if *perfOutput == "" {
			log.Fatal("--benchmark 必须同时提供 --perf-output")
		}
		if err := runBenchmark(app, *perfOutput); err != nil {
			log.Fatalf("性能门禁失败: %v", err)
		}
		return
	}
	runInteractive(app)
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

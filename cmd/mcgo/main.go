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
		app.selectedBlock = actions.SelectedBlock
		if captured && !justCaptured {
			if actions.Break {
				app.breakBlock()
			}
			if actions.Place {
				app.placeBlock()
			}
		}

		movement := client.Movement{}
		if captured {
			movement = client.MovementFromKeys(
				app.window.KeyDown(client.KeyW),
				app.window.KeyDown(client.KeyA),
				app.window.KeyDown(client.KeyS),
				app.window.KeyDown(client.KeyD),
				app.window.KeyDown(client.KeySpace),
			)
		}
		if _, ready := app.predictor.State(); ready {
			control := client.Control{
				MoveX: movement.MoveX,
				MoveZ: movement.MoveZ,
				Jump:  movement.Jump,
				Yaw:   app.camera.Yaw,
				Pitch: app.camera.Pitch,
			}
			if err := app.predictor.Advance(
				dt,
				control,
				client.MirrorCollisionSource{Mirror: app.mirror, Dimension: core.Overworld},
				app.nextSequence,
				func(input network.PlayerInput) error { return app.send(input) },
			); err != nil {
				log.Printf("推进玩家预测失败: %v", err)
			}
			if feet, ok := app.predictor.PresentationPosition(dt); ok {
				app.camera.Pos = feet.Add(mgl32.Vec3{0, physics.EyeHeight, 0})
				app.center = cameraChunk(app.camera.Pos)
			}
		}
		app.frame(64)
	}
}

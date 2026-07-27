//go:build darwin

// Command mcgo 启动可自由飞行的 M1 地形客户端。
package main

import (
	"flag"
	"log"
	"runtime"
	"time"

	"minecraft-go/internal/client"
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
	app, err := newApplication(seed)
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

	for !app.window.ShouldClose() {
		app.window.Poll()

		now := time.Now()
		dt := min(float32(now.Sub(lastFrame).Seconds()), 0.1)
		lastFrame = now

		escapeDown := app.window.KeyDown(client.KeyEscape)
		if escapeDown && !escapeWasDown {
			app.window.SetCursorCaptured(false)
		}
		escapeWasDown = escapeDown

		clickDown := app.window.PrimaryButtonDown()
		if clickDown && !clickWasDown && !app.window.CursorCaptured() {
			app.window.SetCursorCaptured(true)
			lastMouseX, lastMouseY = app.window.CursorPos()
		}
		clickWasDown = clickDown

		if app.window.CursorCaptured() {
			mouseX, mouseY := app.window.CursorPos()
			app.camera.Rotate(
				float32(mouseX-lastMouseX)*0.002,
				float32(lastMouseY-mouseY)*0.002,
			)
			lastMouseX, lastMouseY = mouseX, mouseY

			var fwd, right, up float32
			if app.window.KeyDown(client.KeyW) {
				fwd++
			}
			if app.window.KeyDown(client.KeyS) {
				fwd--
			}
			if app.window.KeyDown(client.KeyD) {
				right++
			}
			if app.window.KeyDown(client.KeyA) {
				right--
			}
			if app.window.KeyDown(client.KeySpace) {
				up++
			}
			if app.window.KeyDown(client.KeyLeftShift) {
				up--
			}
			speed := float32(30)
			if app.window.KeyDown(client.KeyLeftControl) {
				speed = 120
			}
			app.camera.Move(fwd*speed*dt, right*speed*dt, up*speed*dt)
			app.updateCenter()
		}
		app.frame(64)
	}
}
